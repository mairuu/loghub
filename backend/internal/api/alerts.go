package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/mairuu/loghub/backend/internal/alerting"
	"github.com/mairuu/loghub/backend/internal/api/gen"
	"github.com/mairuu/loghub/backend/internal/auth"
	"github.com/mairuu/loghub/backend/internal/authz"
	"github.com/mairuu/loghub/backend/internal/platform/errors"
	"github.com/mairuu/loghub/backend/internal/store"
)

const (
	// maxRuleBytes bounds a rule body.
	maxRuleBytes = 64 << 10
	maxRuleName  = 200
	// maxRuleText is the longest text filter. Events hold no longer text
	// (see internal/ingest), so a longer one could never match.
	maxRuleText      = 2048
	maxWebhookURL    = 2048
	defaultAlertPage = 50
	maxAlertPage     = 1000
)

// ruleFormats describe each field of a new rule, for the errors about it.
var ruleFormats = map[string]string{
	"tenant":           "a tenant ID: 1 to 64 letters, digits, '-' or '_'",
	"name":             fmt.Sprintf("text of 1 to %d bytes", maxRuleName),
	"source":           "one of " + strings.Join(enumValues(gen.SourceFirewall, gen.SourceNetwork, gen.SourceCrowdstrike, gen.SourceAws, gen.SourceM365, gen.SourceAd, gen.SourceAPI), ", "),
	"event_type":       fmt.Sprintf("text of at most %d bytes", maxRuleText),
	"action":           fmt.Sprintf("text of at most %d bytes", maxRuleText),
	"severity_min":     "an integer between 0 and 10",
	"tags":             fmt.Sprintf("an array of texts of at most %d bytes each", maxRuleText),
	"group_by":         "one of " + strings.Join(store.GroupBy, ", "),
	"threshold":        "an integer of at least 1",
	"window_minutes":   "an integer between 1 and 1440",
	"cooldown_minutes": "an integer between 0 and 10080",
	"webhook_url":      fmt.Sprintf("an http or https URL of at most %d bytes", maxWebhookURL),
}

// requiredRuleFields must be present and not null.
var requiredRuleFields = []string{"tenant", "name", "group_by", "threshold", "window_minutes"}

func (s *Server) CreateAlertRule(w http.ResponseWriter, r *http.Request) {
	caller := auth.CallerFrom(r.Context())
	if s.authz.Scopes(caller, authz.AlertRules, authz.Create).Empty() {
		s.fail(w, r, authz.Deny(caller))
		return
	}
	if err := requireMediaType(r, "application/json"); err != nil {
		s.failWith(w, r, http.StatusUnsupportedMediaType, err)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxRuleBytes))
	if err != nil {
		s.failRead(w, r, err, maxRuleBytes)
		return
	}
	p, err := decodeAlertRule(body)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if !s.authz.Can(caller, authz.AlertRules, authz.Create, p.TenantID) {
		s.fail(w, r, authz.TenantNotPermitted(p.TenantID))
		return
	}

	rule, err := s.alerts.CreateRule(r.Context(), scopeOf(caller), p)
	if errors.CodeOf(err) == "unknown_tenant" {
		s.fail(w, r, invalidRequest(errors.FieldError{Field: "tenant", Code: "unknown_tenant", Message: errors.MessageOf(err)}))
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}

	s.logger.LogAttrs(r.Context(), slog.LevelInfo, "alert rule created",
		slog.String("request_id", requestID(r.Context())),
		slog.Int64("rule_id", rule.ID),
		slog.String("tenant", rule.TenantID),
	)
	s.respond(w, r, http.StatusCreated, toAlertRule(rule))
}

func (s *Server) ListAlertRules(w http.ResponseWriter, r *http.Request, p gen.ListAlertRulesParams) {
	caller := auth.CallerFrom(r.Context())
	tenant, ok := s.listTenant(w, r, caller, authz.AlertRules, p.Tenant)
	if !ok {
		return
	}
	rules, err := s.alerts.ListRules(r.Context(), scopeOf(caller), tenant)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := gen.AlertRuleList{Items: make([]gen.AlertRule, len(rules))}
	for i, rule := range rules {
		out.Items[i] = toAlertRule(rule)
		// A webhook URL often carries a secret, so only someone who could
		// have set it sees it.
		if !s.authz.Can(caller, authz.AlertRules, authz.Create, rule.TenantID) {
			out.Items[i].WebhookURL = nil
		}
	}
	s.respond(w, r, http.StatusOK, out)
}

func (s *Server) ListAlerts(w http.ResponseWriter, r *http.Request, p gen.ListAlertsParams) {
	caller := auth.CallerFrom(r.Context())
	tenant, ok := s.listTenant(w, r, caller, authz.Alerts, p.Tenant)
	if !ok {
		return
	}
	limit := defaultAlertPage
	if p.Limit != nil {
		if *p.Limit < 1 || *p.Limit > maxAlertPage {
			s.fail(w, r, invalidParam("limit must be "+paramFormats["limit"]))
			return
		}
		limit = int(*p.Limit)
	}
	alerts, err := s.alerts.ListAlerts(r.Context(), scopeOf(caller), tenant, limit)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := gen.AlertList{Items: make([]gen.Alert, len(alerts))}
	for i, a := range alerts {
		out.Items[i] = alerting.Body(a)
	}
	s.respond(w, r, http.StatusOK, out)
}

// listTenant answers a caller who may read no tenant's resource, and
// otherwise returns the tenant a list is narrowed to: the one asked for,
// which the caller must be allowed to read, or the caller's only tenant, or
// empty for every tenant the caller may read.
func (s *Server) listTenant(w http.ResponseWriter, r *http.Request, caller authz.Caller, resource authz.Resource, asked *string) (string, bool) {
	readable := s.authz.Scopes(caller, resource, authz.Read)
	if readable.Empty() {
		s.fail(w, r, authz.Deny(caller))
		return "", false
	}
	tenant := strings.TrimSpace(deref(asked))
	only, one := readable.Only()
	switch {
	case tenant != "" && !readable.Contains(tenant):
		s.fail(w, r, authz.TenantNotPermitted(tenant))
		return "", false
	case tenant != "" && !store.ValidTenantID(tenant):
		// Only an admin gets here, and no tenant has this ID.
		s.fail(w, r, invalidParam(store.InvalidTenantID))
		return "", false
	case tenant == "" && one:
		tenant = only
	}
	return tenant, true
}

// decodeAlertRule reads and validates a new rule. A field of the wrong type
// or an unknown one is reported alone; otherwise every unusable value is
// reported at once.
func decodeAlertRule(body []byte) (store.NewAlertRule, error) {
	notObject := errors.Malformed("invalid_json", "request body is not a JSON object")
	var present map[string]json.RawMessage
	if err := json.Unmarshal(body, &present); err != nil || present == nil {
		return store.NewAlertRule{}, notObject.Wrapping(err)
	}

	var in gen.NewAlertRule
	dec := json.NewDecoder(bytes.NewReader(body))
	// A misspelt filter would otherwise be dropped, and the rule would match
	// more than intended.
	dec.DisallowUnknownFields()
	err := dec.Decode(&in)
	var typeErr *json.UnmarshalTypeError
	switch {
	case errors.As(err, &typeErr) && ruleFormats[topField(typeErr.Field)] != "":
		field := topField(typeErr.Field)
		return store.NewAlertRule{}, invalidRequest(ruleFieldError(field, "invalid_type"))
	case err != nil && strings.HasPrefix(err.Error(), "json: unknown field "):
		field, _ := strconv.Unquote(strings.TrimPrefix(err.Error(), "json: unknown field "))
		return store.NewAlertRule{}, invalidRequest(errors.FieldError{
			Field: field, Code: "unknown_field", Message: fmt.Sprintf("%q is not a field of an alert rule", field),
		})
	case err != nil:
		return store.NewAlertRule{}, notObject.Wrapping(err)
	}

	var problems []errors.FieldError
	bad := func(field, code string) { problems = append(problems, ruleFieldError(field, code)) }
	for _, field := range requiredRuleFields {
		if raw, ok := present[field]; !ok || string(raw) == "null" {
			problems = append(problems, errors.FieldError{Field: field, Code: "required", Message: field + " is required"})
		}
	}
	missing := func(field string) bool {
		return slices.ContainsFunc(problems, func(p errors.FieldError) bool { return p.Field == field })
	}

	p := store.NewAlertRule{
		TenantID:      strings.TrimSpace(in.Tenant),
		Name:          strings.TrimSpace(in.Name),
		GroupBy:       string(in.GroupBy),
		Threshold:     in.Threshold,
		WindowMinutes: in.WindowMinutes,
		Tags:          []string{},
	}
	if !missing("tenant") && !store.ValidTenantID(p.TenantID) {
		bad("tenant", "invalid_format")
	}
	if !missing("name") && (p.Name == "" || len(p.Name) > maxRuleName || strings.ContainsRune(p.Name, 0)) {
		bad("name", "invalid_format")
	}
	if in.Source != nil {
		source := gen.Source(strings.ToLower(strings.TrimSpace(string(*in.Source))))
		switch {
		case !source.Valid():
			bad("source", "unknown_value")
		default:
			p.Source = new(string(source))
		}
	}
	for _, text := range []struct {
		field string
		in    *string
		out   **string
		fn    func(string) string
	}{
		{"event_type", in.EventType, &p.EventType, nil},
		// Stored lowercase by the normalizer.
		{"action", in.Action, &p.Action, strings.ToLower},
	} {
		v, ok := ruleText(deref(text.in), text.fn)
		switch {
		case !ok:
			bad(text.field, "invalid_format")
		case v != "":
			*text.out = &v
		}
	}
	for _, tag := range deref(in.Tags) {
		v, ok := ruleText(tag, nil)
		if !ok {
			bad("tags", "invalid_format")
			break
		}
		if v != "" && !slices.Contains(p.Tags, v) {
			p.Tags = append(p.Tags, v)
		}
	}
	slices.Sort(p.Tags)
	if in.SeverityMin != nil {
		if *in.SeverityMin < 0 || *in.SeverityMin > 10 {
			bad("severity_min", "out_of_range")
		} else {
			p.SeverityMin = new(int16(*in.SeverityMin))
		}
	}
	if !missing("group_by") && !in.GroupBy.Valid() {
		bad("group_by", "unknown_value")
	}
	if !missing("threshold") && p.Threshold < 1 {
		bad("threshold", "out_of_range")
	}
	if !missing("window_minutes") && (p.WindowMinutes < 1 || p.WindowMinutes > 1440) {
		bad("window_minutes", "out_of_range")
	}
	p.CooldownMinutes = p.WindowMinutes
	if in.CooldownMinutes != nil {
		p.CooldownMinutes = *in.CooldownMinutes
		if p.CooldownMinutes < 0 || p.CooldownMinutes > 10080 {
			bad("cooldown_minutes", "out_of_range")
		}
	}
	if u := strings.TrimSpace(deref(in.WebhookURL)); u != "" {
		if validWebhookURL(u) {
			p.WebhookURL = &u
		} else {
			bad("webhook_url", "invalid_format")
		}
	}

	if len(problems) > 0 {
		return store.NewAlertRule{}, invalidRequest(problems...)
	}
	return p, nil
}

func ruleFieldError(field, code string) errors.FieldError {
	return errors.FieldError{Field: field, Code: code, Message: field + " must be " + ruleFormats[field]}
}

// ruleText trims and maps a text filter, and reports whether the database
// could hold it and an event could match it.
func ruleText(s string, mapping func(string) string) (string, bool) {
	s = strings.TrimSpace(s)
	if mapping != nil {
		s = mapping(s)
	}
	return s, len(s) <= maxRuleText && !strings.ContainsRune(s, 0)
}

func validWebhookURL(s string) bool {
	if len(s) > maxWebhookURL {
		return false
	}
	u, err := url.Parse(s)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

// topField is the top-level field a JSON type error is about: tags, not
// tags.0.
func topField(path string) string {
	field, _, _ := strings.Cut(path, ".")
	return field
}

func enumValues[T ~string](values ...T) []string {
	out := make([]string, len(values))
	for i, v := range values {
		out[i] = string(v)
	}
	return out
}

func toAlertRule(r store.AlertRule) gen.AlertRule {
	out := gen.AlertRule{
		ID:              strconv.FormatInt(r.ID, 10),
		Tenant:          r.TenantID,
		Name:            r.Name,
		EventType:       r.EventType,
		Action:          r.Action,
		Tags:            r.Tags,
		GroupBy:         gen.AlertGroupBy(r.GroupBy),
		Threshold:       r.Threshold,
		WindowMinutes:   r.WindowMinutes,
		CooldownMinutes: r.CooldownMinutes,
		WebhookURL:      r.WebhookURL,
		CreatedAt:       r.CreatedAt.UTC(),
	}
	if r.Source != nil {
		out.Source = new(gen.Source(*r.Source))
	}
	if r.SeverityMin != nil {
		out.SeverityMin = new(int(*r.SeverityMin))
	}
	if out.Tags == nil {
		// Required by the schema, so an empty list rather than null.
		out.Tags = []string{}
	}
	return out
}
