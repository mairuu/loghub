// Package alerting evaluates alert rules on a schedule and delivers what they
// raise (ADR 0010). Each tick, every tenant's rules are evaluated as that
// tenant, over a window that ends on a whole minute a little in the past.
package alerting

import (
	"context"
	"log/slog"
	"net/url"
	"strconv"
	"time"

	"github.com/mairuu/loghub/backend/internal/api/gen"
	"github.com/mairuu/loghub/backend/internal/jobs"
	"github.com/mairuu/loghub/backend/internal/platform/errors"
	"github.com/mairuu/loghub/backend/internal/store"
)

const (
	// Interval is how often every rule is evaluated.
	Interval = time.Minute
	// Lag is how far behind the clock a window ends at the least, so events
	// the collector delivers a little late are still counted.
	Lag = 30 * time.Second
)

// WindowEnd is the end of the window evaluated at now: the last whole minute
// at least Lag ago. Every evaluation within the same minute asks about the
// same window, which is what keeps a firing from being recorded twice.
func WindowEnd(now time.Time) time.Time {
	return now.Add(-Lag).Truncate(time.Minute)
}

// Tenants lists every tenant. *store.TenantRepo is the implementation.
type Tenants interface {
	ListIDs(ctx context.Context) ([]string, error)
}

// Rules is where rules are found and their firings recorded.
// *store.AlertRepo is the implementation.
type Rules interface {
	ListRules(ctx context.Context, scope store.Scope, tenant string) ([]store.AlertRule, error)
	Evaluate(ctx context.Context, rule store.AlertRule, end time.Time) ([]store.Alert, error)
}

// Notifier delivers an alert to a rule's webhook URL. *Webhook is the
// implementation.
type Notifier interface {
	Send(ctx context.Context, url string, alert gen.Alert) error
}

type Config struct {
	Tenants Tenants
	Rules   Rules
	Logger  *slog.Logger
	// Notifier nil means a Webhook with the default client.
	Notifier Notifier
	// Now nil means time.Now.
	Now func() time.Time
}

type Evaluator struct {
	tenants  Tenants
	rules    Rules
	logger   *slog.Logger
	notifier Notifier
	now      func() time.Time
}

func New(cfg Config) *Evaluator {
	e := &Evaluator{
		tenants:  cfg.Tenants,
		rules:    cfg.Rules,
		logger:   cfg.Logger,
		notifier: cfg.Notifier,
		now:      cfg.Now,
	}
	if e.notifier == nil {
		e.notifier = NewWebhook(nil)
	}
	if e.now == nil {
		e.now = time.Now
	}
	return e
}

// Job runs Run every Interval, for jobs.Start.
func (e *Evaluator) Job() jobs.Job {
	return jobs.Job{Name: "evaluate_alerts", Every: Interval, Run: e.Run}
}

// Run evaluates every tenant's rules once. A tenant or rule that fails is
// logged and skipped, so it can't hold up the others; the error is only for
// failing to list the tenants, or being stopped.
func (e *Evaluator) Run(ctx context.Context) error {
	end := WindowEnd(e.now())
	tenants, err := e.tenants.ListIDs(ctx)
	if err != nil {
		return errors.Internalf(err, "cannot list tenants")
	}
	for _, tenant := range tenants {
		// Each tenant's rules are read as that tenant, like everything else
		// the evaluator does.
		rules, err := e.rules.ListRules(ctx, store.Scope{TenantID: tenant}, tenant)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			e.logFailure(ctx, "cannot list a tenant's alert rules", err, slog.String("tenant", tenant))
			continue
		}
		for _, rule := range rules {
			e.evaluate(ctx, rule, end)
			if ctx.Err() != nil {
				return ctx.Err()
			}
		}
	}
	return nil
}

func (e *Evaluator) evaluate(ctx context.Context, rule store.AlertRule, end time.Time) {
	fired, err := e.rules.Evaluate(ctx, rule, end)
	if err != nil {
		if ctx.Err() == nil {
			e.logFailure(ctx, "cannot evaluate an alert rule", err,
				slog.Int64("rule_id", rule.ID), slog.String("tenant", rule.TenantID))
		}
		return
	}
	for _, a := range fired {
		e.logger.LogAttrs(ctx, slog.LevelInfo, "alert raised",
			slog.Int64("alert_id", a.ID),
			slog.Int64("rule_id", a.RuleID),
			slog.String("tenant", a.TenantID),
			slog.String("group_by", a.GroupBy),
			slog.String("group_key", a.GroupKey),
			slog.Int64("count", a.Matched),
		)
		if rule.WebhookURL != nil {
			e.deliver(ctx, *rule.WebhookURL, a)
		}
	}
}

// deliver sends one alert to its rule's webhook, once. The alert is already
// recorded, so a failure only costs the notification.
func (e *Evaluator) deliver(ctx context.Context, target string, a store.Alert) {
	attrs := []slog.Attr{
		slog.Int64("alert_id", a.ID),
		slog.Int64("rule_id", a.RuleID),
		// Only the host: a webhook URL often carries a secret.
		slog.String("webhook_host", webhookHost(target)),
	}
	if err := e.notifier.Send(ctx, target, Body(a)); err != nil {
		if ctx.Err() != nil {
			return
		}
		e.logger.LogAttrs(ctx, slog.LevelWarn, "webhook delivery failed", append(attrs, slog.String("error", err.Error()))...)
		return
	}
	e.logger.LogAttrs(ctx, slog.LevelInfo, "webhook delivered", attrs...)
}

func (e *Evaluator) logFailure(ctx context.Context, message string, err error, attrs ...slog.Attr) {
	level := slog.LevelError
	if errors.KindOf(err) == errors.KindUnavailable {
		level = slog.LevelWarn
	}
	attrs = append(attrs, slog.String("error", err.Error()))
	attrs = append(attrs, errors.AttrsOf(err)...)
	e.logger.LogAttrs(ctx, level, message, attrs...)
}

// Body is an alert as the API lists it, and as a webhook receives it.
func Body(a store.Alert) gen.Alert {
	return gen.Alert{
		ID:          strconv.FormatInt(a.ID, 10),
		RuleID:      strconv.FormatInt(a.RuleID, 10),
		RuleName:    a.RuleName,
		Tenant:      a.TenantID,
		GroupBy:     gen.AlertGroupBy(a.GroupBy),
		GroupKey:    a.GroupKey,
		Count:       a.Matched,
		WindowStart: a.WindowStart.UTC(),
		WindowEnd:   a.WindowEnd.UTC(),
		CreatedAt:   a.CreatedAt.UTC(),
	}
}

func webhookHost(target string) string {
	u, err := url.Parse(target)
	if err != nil {
		return ""
	}
	return u.Host
}
