package store

// Alert rules and their firings (ADR 0010). Evaluating a rule is the one
// query here whose shape depends on the rule, so it is built by hand, as
// search is (ADR 0008). Every value goes in as a bind parameter.

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/mairuu/loghub/backend/internal/platform/errors"
	"github.com/mairuu/loghub/backend/internal/platform/pg"
	"github.com/mairuu/loghub/backend/internal/store/gen"
)

type AlertRule = gen.AlertRule

// NewAlertRule is a rule to create. Nil filter fields and empty Tags don't
// filter. The API validates it; the database only refuses what would break
// evaluation.
type NewAlertRule = gen.InsertAlertRuleParams

// Alert is a firing, with the name and grouping of its rule.
type Alert = gen.ListAlertsRow

// GroupBy are the values a rule may group by, as the API names them. Each is
// one of TopFields, and a group's key is recorded as the text Top reports.
var GroupBy = []string{"src_ip", "dst_ip", "user", "host"}

// evaluateTimeout cancels a rule whose query runs longer, so one rule can't
// hold up the others.
const evaluateTimeout = 30 * time.Second

type AlertRepo struct{ store *Store }

func NewAlertRepo(store *Store) *AlertRepo { return &AlertRepo{store: store} }

// CreateRule stores a rule for p.TenantID, which scope must be allowed to
// write to. A tenant that doesn't exist is errors.KindInvalid with code
// unknown_tenant.
func (r *AlertRepo) CreateRule(ctx context.Context, scope Scope, p NewAlertRule) (AlertRule, error) {
	if p.Tags == nil {
		// NULL would fail the NOT NULL constraint.
		p.Tags = []string{}
	}
	var rule AlertRule
	err := r.store.InScope(ctx, scope, func(s *Store) error {
		var err error
		rule, err = s.q.InsertAlertRule(ctx, p)
		var pgErr *pgconn.PgError
		switch {
		case errors.As(err, &pgErr) && pgErr.Code == "23503": // foreign_key_violation
			return unknownTenant(p.TenantID)
		case err != nil:
			return pg.Wrap(err, "cannot create alert rule")
		}
		return nil
	})
	return rule, err
}

// ListRules returns the rules scope may read, oldest first, and only
// tenant's unless tenant is empty.
func (r *AlertRepo) ListRules(ctx context.Context, scope Scope, tenant string) ([]AlertRule, error) {
	var rules []AlertRule
	err := r.store.InScope(ctx, scope, func(s *Store) error {
		var err error
		if tenant == "" {
			rules, err = s.q.ListAlertRules(ctx)
		} else {
			rules, err = s.q.ListTenantAlertRules(ctx, tenant)
		}
		if err != nil {
			return pg.Wrap(err, "cannot list alert rules")
		}
		return nil
	})
	return rules, err
}

// ListAlerts returns the newest limit firings scope may read, newest first,
// and only tenant's unless tenant is empty.
func (r *AlertRepo) ListAlerts(ctx context.Context, scope Scope, tenant string, limit int) ([]Alert, error) {
	var alerts []Alert
	err := r.store.InScope(ctx, scope, func(s *Store) error {
		var err error
		if tenant == "" {
			alerts, err = s.q.ListAlerts(ctx, int32(limit))
		} else {
			var rows []gen.ListTenantAlertsRow
			rows, err = s.q.ListTenantAlerts(ctx, gen.ListTenantAlertsParams{TenantID: tenant, MaxRows: int32(limit)})
			alerts = make([]Alert, len(rows))
			for i, row := range rows {
				alerts[i] = Alert(row)
			}
		}
		if err != nil {
			return pg.Wrap(err, "cannot list alerts")
		}
		return nil
	})
	return alerts, err
}

// Evaluate records a firing for each group of rule's events in the window
// that ends at end, and returns the firings it recorded. It runs as the
// rule's tenant, never as an admin. A group is skipped if a firing for it
// already covers this window, or if its last firing's window ended within
// the rule's cooldown.
func (r *AlertRepo) Evaluate(ctx context.Context, rule AlertRule, end time.Time) ([]Alert, error) {
	sql, args, err := evaluation(rule, end)
	if err != nil {
		return nil, err
	}
	var fired []Alert
	err = r.store.InScope(ctx, Scope{TenantID: rule.TenantID}, func(s *Store) error {
		if _, err := s.db.Exec(ctx, "SELECT set_config('statement_timeout', $1, true)", strconv.FormatInt(evaluateTimeout.Milliseconds(), 10)); err != nil {
			return pg.Wrap(err, "cannot set evaluation timeout")
		}
		rows, err := s.db.Query(ctx, sql, args...)
		if err == nil {
			fired, err = pgx.CollectRows(rows, pgx.RowToStructByName[Alert])
		}
		if err != nil {
			return pg.Wrap(err, "cannot evaluate alert rule").With("rule_id", rule.ID)
		}
		return nil
	})
	return fired, err
}

// evaluation builds the statement Evaluate runs: count the window's
// matching events by group, and insert a firing for each group at or over
// the threshold.
func evaluation(rule AlertRule, end time.Time) (string, []any, error) {
	group, ok := fieldColumns[rule.GroupBy]
	if !ok || !slices.Contains(GroupBy, rule.GroupBy) {
		return "", nil, errors.Internal(fmt.Sprintf("alert rule groups by unknown %q", rule.GroupBy)).With("rule_id", rule.ID)
	}

	var args []any
	arg := func(v any) string {
		args = append(args, v)
		return "$" + strconv.Itoa(len(args))
	}

	window := time.Duration(rule.WindowMinutes) * time.Minute
	cooldown := time.Duration(rule.CooldownMinutes) * time.Minute
	ruleID, tenant := arg(rule.ID), arg(rule.TenantID)
	from, to := arg(end.Add(-window)), arg(end)

	// The tenant is named even though row-level security limits the query
	// to it: Postgres can't use an index for the security policy's OR.
	conds := []string{
		"tenant_id = " + tenant,
		"ts >= " + from,
		"ts < " + to,
		// A group without a key isn't one thing that happened repeatedly.
		group.column + " IS NOT NULL",
	}
	if rule.Source != nil {
		conds = append(conds, "source = "+arg(*rule.Source))
	}
	if rule.EventType != nil {
		conds = append(conds, "event_type = "+arg(*rule.EventType))
	}
	if rule.Action != nil {
		conds = append(conds, "action = "+arg(*rule.Action))
	}
	if rule.SeverityMin != nil {
		conds = append(conds, "severity >= "+arg(*rule.SeverityMin))
	}
	if len(rule.Tags) > 0 {
		conds = append(conds, "tags @> "+arg(rule.Tags))
	}

	// Parameters in the SELECT list are cast, or Postgres would type them as
	// text before seeing the columns they go into.
	sql := `WITH counted AS (
  SELECT ` + group.text + ` AS group_key, count(*) AS matched
  FROM events
  WHERE ` + strings.Join(conds, "\n    AND ") + `
  GROUP BY ` + group.column + `
  HAVING count(*) >= ` + arg(rule.Threshold) + `
)
INSERT INTO alerts (rule_id, tenant_id, group_key, window_start, window_end, matched)
SELECT ` + ruleID + `::bigint, ` + tenant + `::text, c.group_key, ` + from + `::timestamptz, ` + to + `::timestamptz, c.matched
FROM counted c
WHERE NOT EXISTS (
  SELECT FROM alerts a
  WHERE a.rule_id = ` + ruleID + `
    AND a.group_key = c.group_key
    AND a.window_end > ` + arg(end.Add(-cooldown)) + `
)
ORDER BY c.group_key
ON CONFLICT (rule_id, group_key, window_start) DO NOTHING
RETURNING id, rule_id, ` + arg(rule.Name) + `::text AS rule_name, ` + arg(rule.GroupBy) + `::text AS group_by,
  tenant_id, group_key, window_start, window_end, matched, created_at`
	return sql, args, nil
}
