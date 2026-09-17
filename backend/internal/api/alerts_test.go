package api_test

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mairuu/loghub/backend/internal/api"
	"github.com/mairuu/loghub/backend/internal/platform/errors"
	"github.com/mairuu/loghub/backend/internal/store"
)

// fakeAlerts stands in for the alert store. The end-to-end test uses the
// real one.
type fakeAlerts struct {
	createErr error
	created   []store.NewAlertRule
	rules     []store.AlertRule
	alerts    []store.Alert

	scopes  []store.Scope
	tenants []string
	limits  []int
}

func (f *fakeAlerts) CreateRule(_ context.Context, scope store.Scope, p store.NewAlertRule) (store.AlertRule, error) {
	f.scopes = append(f.scopes, scope)
	f.created = append(f.created, p)
	if f.createErr != nil {
		return store.AlertRule{}, f.createErr
	}
	return store.AlertRule{
		ID: 12, TenantID: p.TenantID, Name: p.Name, Source: p.Source, EventType: p.EventType,
		Action: p.Action, SeverityMin: p.SeverityMin, Tags: p.Tags, GroupBy: p.GroupBy,
		Threshold: p.Threshold, WindowMinutes: p.WindowMinutes, CooldownMinutes: p.CooldownMinutes,
		WebhookURL: p.WebhookURL, CreatedAt: ruleTime,
	}, nil
}

func (f *fakeAlerts) ListRules(_ context.Context, scope store.Scope, tenant string) ([]store.AlertRule, error) {
	f.scopes = append(f.scopes, scope)
	f.tenants = append(f.tenants, tenant)
	return f.rules, nil
}

func (f *fakeAlerts) ListAlerts(_ context.Context, scope store.Scope, tenant string, limit int) ([]store.Alert, error) {
	f.scopes = append(f.scopes, scope)
	f.tenants = append(f.tenants, tenant)
	f.limits = append(f.limits, limit)
	return f.alerts, nil
}

func (f *fakeAlerts) called() bool { return len(f.scopes) > 0 }

var ruleTime = time.Date(2026, 9, 17, 5, 0, 0, 0, time.FixedZone("ICT", 7*3600))

const failedLoginRule = `{"tenant":"demoA","name":"failed logins","tags":["auth_failure"],"group_by":"src_ip","threshold":5,"window_minutes":5}`

func TestCreateAlertRule(t *testing.T) {
	t.Run("defaults", func(t *testing.T) {
		var alerts fakeAlerts
		h := asAdmin(t, newServer(t, api.Config{Alerts: &alerts}))
		call(t, h, "POST", "/api/v1/alert-rules", "application/json", strings.NewReader(failedLoginRule)).
			check(t, 201, `{
				"id":"12","tenant":"demoA","name":"failed logins","tags":["auth_failure"],"group_by":"src_ip",
				"threshold":5,"window_minutes":5,"cooldown_minutes":5,"created_at":"2026-09-16T22:00:00Z"
			}`)
		if want := []store.Scope{store.AdminScope}; !reflect.DeepEqual(alerts.scopes, want) {
			t.Errorf("created as %+v, want %+v", alerts.scopes, want)
		}
	})

	t.Run("everything set", func(t *testing.T) {
		var alerts fakeAlerts
		h := asAdmin(t, newServer(t, api.Config{Alerts: &alerts}))
		body := `{
			"tenant":" demoB ","name":" Denied bursts ","source":"FireWall","event_type":" traffic ",
			"action":" DENY ","severity_min":0,"tags":["b"," a ","b",""],"group_by":"dst_ip",
			"threshold":100,"window_minutes":1440,"cooldown_minutes":0,
			"webhook_url":" https://hooks.example.com/loghub?token=x "
		}`
		res := call(t, h, "POST", "/api/v1/alert-rules", "application/json; charset=utf-8", strings.NewReader(body))
		if res.status != 201 {
			t.Fatalf("got %d %v", res.status, res.body)
		}
		want := store.NewAlertRule{
			TenantID: "demoB", Name: "Denied bursts", Source: new("firewall"), EventType: new("traffic"),
			Action: new("deny"), SeverityMin: new(int16(0)), Tags: []string{"a", "b"}, GroupBy: "dst_ip",
			Threshold: 100, WindowMinutes: 1440, CooldownMinutes: 0,
			WebhookURL: new("https://hooks.example.com/loghub?token=x"),
		}
		if len(alerts.created) != 1 || !reflect.DeepEqual(alerts.created[0], want) {
			t.Errorf("created %s\nwant %s", dump(alerts.created), dump(want))
		}
		if res.body["webhook_url"] != "https://hooks.example.com/loghub?token=x" || res.body["source"] != "firewall" {
			t.Errorf("answered %v", res.body)
		}
	})

	t.Run("unknown tenant", func(t *testing.T) {
		alerts := fakeAlerts{createErr: errors.Invalid("unknown_tenant", `tenant "demoC" does not exist`)}
		h := asAdmin(t, newServer(t, api.Config{Alerts: &alerts}))
		body := strings.Replace(failedLoginRule, "demoA", "demoC", 1)
		call(t, h, "POST", "/api/v1/alert-rules", "application/json", strings.NewReader(body)).
			check(t, 422, `{"code":"invalid_request","message":"the request contains invalid fields","errors":[
				{"field":"tenant","code":"unknown_tenant","message":"tenant \"demoC\" does not exist"}
			]}`)
	})
}

func TestCreateAlertRuleRefusesBody(t *testing.T) {
	const invalid = `"code":"invalid_request","message":"the request contains invalid fields"`
	fieldError := func(field, code, message string) string {
		return `{"field":"` + field + `","code":"` + code + `","message":"` + message + `"}`
	}
	for _, tc := range []struct {
		name, body string
		status     int
		want       string
	}{
		{"not JSON", `{"tenant":`, 400, `{"code":"invalid_json","message":"request body is not a JSON object"}`},
		{"an array", `[]`, 400, `{"code":"invalid_json","message":"request body is not a JSON object"}`},
		{"trailing data", failedLoginRule + `{}`, 400, `{"code":"invalid_json","message":"request body is not a JSON object"}`},
		{"empty", `{}`, 422, `{` + invalid + `,"errors":[` +
			fieldError("tenant", "required", "tenant is required") + `,` +
			fieldError("name", "required", "name is required") + `,` +
			fieldError("group_by", "required", "group_by is required") + `,` +
			fieldError("threshold", "required", "threshold is required") + `,` +
			fieldError("window_minutes", "required", "window_minutes is required") + `]}`},
		{"null", strings.Replace(failedLoginRule, `"threshold":5`, `"threshold":null`, 1), 422, `{` + invalid + `,"errors":[` +
			fieldError("threshold", "required", "threshold is required") + `]}`},
		{"misspelt field", strings.Replace(failedLoginRule, `"tags"`, `"tag"`, 1), 422, `{` + invalid + `,"errors":[` +
			fieldError("tag", "unknown_field", `\"tag\" is not a field of an alert rule`) + `]}`},
		{"wrong type", strings.Replace(failedLoginRule, `"threshold":5`, `"threshold":"5"`, 1), 422, `{` + invalid + `,"errors":[` +
			fieldError("threshold", "invalid_type", "threshold must be an integer of at least 1") + `]}`},
		{"fraction", strings.Replace(failedLoginRule, `"threshold":5`, `"threshold":2.5`, 1), 422, `{` + invalid + `,"errors":[` +
			fieldError("threshold", "invalid_type", "threshold must be an integer of at least 1") + `]}`},
		{"tag of the wrong type", strings.Replace(failedLoginRule, `["auth_failure"]`, `["auth_failure",1]`, 1), 422, `{` + invalid + `,"errors":[` +
			fieldError("tags", "invalid_type", "tags must be an array of texts of at most 2048 bytes each") + `]}`},
		{"every value unusable", `{
			"tenant":"demo A","name":" ","source":"syslog","event_type":"a\u0000b","action":"` + strings.Repeat("x", 2049) + `",
			"severity_min":11,"tags":["\u0000"],"group_by":"ip","threshold":0,"window_minutes":1441,
			"cooldown_minutes":-1,"webhook_url":"ftp://hooks.example.com/"
		}`, 422, `{` + invalid + `,"errors":[` +
			fieldError("tenant", "invalid_format", "tenant must be a tenant ID: 1 to 64 letters, digits, '-' or '_'") + `,` +
			fieldError("name", "invalid_format", "name must be text of 1 to 200 bytes") + `,` +
			fieldError("source", "unknown_value", "source must be one of firewall, network, crowdstrike, aws, m365, ad, api") + `,` +
			fieldError("event_type", "invalid_format", "event_type must be text of at most 2048 bytes") + `,` +
			fieldError("action", "invalid_format", "action must be text of at most 2048 bytes") + `,` +
			fieldError("tags", "invalid_format", "tags must be an array of texts of at most 2048 bytes each") + `,` +
			fieldError("severity_min", "out_of_range", "severity_min must be an integer between 0 and 10") + `,` +
			fieldError("group_by", "unknown_value", "group_by must be one of src_ip, dst_ip, user, host") + `,` +
			fieldError("threshold", "out_of_range", "threshold must be an integer of at least 1") + `,` +
			fieldError("window_minutes", "out_of_range", "window_minutes must be an integer between 1 and 1440") + `,` +
			fieldError("cooldown_minutes", "out_of_range", "cooldown_minutes must be an integer between 0 and 10080") + `,` +
			fieldError("webhook_url", "invalid_format", "webhook_url must be an http or https URL of at most 2048 bytes") + `]}`},
		{"relative webhook", strings.Replace(failedLoginRule, `}`, `,"webhook_url":"/hook"}`, 1), 422, `{` + invalid + `,"errors":[` +
			fieldError("webhook_url", "invalid_format", "webhook_url must be an http or https URL of at most 2048 bytes") + `]}`},
		{"long name", strings.Replace(failedLoginRule, `"failed logins"`, `"`+strings.Repeat("n", 201)+`"`, 1), 422, `{` + invalid + `,"errors":[` +
			fieldError("name", "invalid_format", "name must be text of 1 to 200 bytes") + `]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var alerts fakeAlerts
			h := asAdmin(t, newServer(t, api.Config{Alerts: &alerts}))
			call(t, h, "POST", "/api/v1/alert-rules", "application/json", strings.NewReader(tc.body)).check(t, tc.status, tc.want)
			if alerts.called() {
				t.Error("a refused rule reached the store")
			}
		})
	}

	t.Run("wrong media type", func(t *testing.T) {
		h := asAdmin(t, newServer(t, api.Config{}))
		call(t, h, "POST", "/api/v1/alert-rules", "text/plain", strings.NewReader(failedLoginRule)).
			check(t, 415, `{"code":"unsupported_media_type","message":"expected application/json"}`)
	})
	t.Run("too large", func(t *testing.T) {
		h := asAdmin(t, newServer(t, api.Config{}))
		body := strings.Replace(failedLoginRule, `}`, `,"name":"`+strings.Repeat("n", 64<<10)+`"}`, 1)
		call(t, h, "POST", "/api/v1/alert-rules", "application/json", strings.NewReader(body)).
			check(t, 413, `{"code":"body_too_large","message":"request body exceeds 64 KiB"}`)
	})
}

func TestAlertListScope(t *testing.T) {
	viewer, admin := tokenFor(t, viewerCaller), tokenFor(t, adminCaller)
	viewerScope := store.Scope{TenantID: "demoA"}
	for _, path := range []string{"/api/v1/alert-rules", "/api/v1/alerts"} {
		for _, tc := range []struct {
			name, credential, query string
			wantTenant              string
			wantScope               store.Scope
			refused                 string
		}{
			{name: "viewer, no tenant", credential: viewer, wantTenant: "demoA", wantScope: viewerScope},
			{name: "viewer, own tenant", credential: viewer, query: "tenant=+demoA+", wantTenant: "demoA", wantScope: viewerScope},
			{name: "viewer, another tenant", credential: viewer, query: "tenant=demoB",
				refused: `403 {"code":"tenant_not_permitted","message":"you may not access tenant \"demoB\""}`},
			{name: "admin, no tenant", credential: admin, wantScope: store.AdminScope},
			{name: "admin, any tenant", credential: admin, query: "tenant=demoB", wantTenant: "demoB", wantScope: store.AdminScope},
			{name: "admin, impossible tenant", credential: admin, query: "tenant=demo%00B",
				refused: `400 {"code":"invalid_parameter","message":"tenant must be 1 to 64 letters, digits, '-' or '_'"}`},
		} {
			t.Run(path+", "+tc.name, func(t *testing.T) {
				var alerts fakeAlerts
				res := callAs(t, newServer(t, api.Config{Alerts: &alerts}), tc.credential, "GET", path+"?"+tc.query, "", nil)
				if tc.refused != "" {
					status, body, _ := strings.Cut(tc.refused, " ")
					res.check(t, map[string]int{"400": 400, "403": 403}[status], body)
					if alerts.called() {
						t.Error("a refused list reached the store")
					}
					return
				}
				res.check(t, 200, `{"items":[]}`)
				if len(alerts.tenants) != 1 || alerts.tenants[0] != tc.wantTenant {
					t.Errorf("tenant filter %q, want %q", alerts.tenants, tc.wantTenant)
				}
				if alerts.scopes[0] != tc.wantScope {
					t.Errorf("scope %+v, want %+v", alerts.scopes[0], tc.wantScope)
				}
			})
		}
	}
}

func TestListAlerts(t *testing.T) {
	at := time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC)
	alerts := fakeAlerts{alerts: []store.Alert{{
		ID: 301, RuleID: 12, RuleName: "failed logins", GroupBy: "src_ip", TenantID: "demoA",
		GroupKey: "203.0.113.77", WindowStart: at.Add(-5 * time.Minute), WindowEnd: at, Matched: 5,
		CreatedAt: at.Add(45 * time.Second),
	}}}
	h := asAdmin(t, newServer(t, api.Config{Alerts: &alerts}))
	call(t, h, "GET", "/api/v1/alerts", "", nil).check(t, 200, `{"items":[{
		"id":"301","rule_id":"12","rule_name":"failed logins","tenant":"demoA","group_by":"src_ip",
		"group_key":"203.0.113.77","count":5,"window_start":"2026-09-17T11:55:00Z",
		"window_end":"2026-09-17T12:00:00Z","created_at":"2026-09-17T12:00:45Z"
	}]}`)

	for _, tc := range []struct {
		query string
		limit int
	}{{"", 50}, {"limit=1", 1}, {"limit=1000", 1000}} {
		var alerts fakeAlerts
		call(t, asAdmin(t, newServer(t, api.Config{Alerts: &alerts})), "GET", "/api/v1/alerts?"+tc.query, "", nil).check(t, 200, `{"items":[]}`)
		if len(alerts.limits) != 1 || alerts.limits[0] != tc.limit {
			t.Errorf("%q: listed with limits %v, want %d", tc.query, alerts.limits, tc.limit)
		}
	}
	for _, query := range []string{"limit=0", "limit=1001", "limit=ten"} {
		var alerts fakeAlerts
		call(t, asAdmin(t, newServer(t, api.Config{Alerts: &alerts})), "GET", "/api/v1/alerts?"+query, "", nil).
			check(t, 400, `{"code":"invalid_parameter","message":"limit must be an integer between 1 and 1000"}`)
		if alerts.called() {
			t.Errorf("%q reached the store", query)
		}
	}
}

// A webhook URL often holds a secret, so a viewer doesn't see it.
func TestListAlertRulesHidesWebhooks(t *testing.T) {
	rule := store.AlertRule{
		ID: 12, TenantID: "demoA", Name: "failed logins", Source: new("ad"), SeverityMin: new(int16(3)),
		GroupBy: "src_ip", Threshold: 5, WindowMinutes: 5, CooldownMinutes: 5,
		WebhookURL: new("https://hooks.example.com/secret"), CreatedAt: ruleTime,
	}
	const listed = `"id":"12","tenant":"demoA","name":"failed logins","source":"ad","severity_min":3,"tags":[],
		"group_by":"src_ip","threshold":5,"window_minutes":5,"cooldown_minutes":5,"created_at":"2026-09-16T22:00:00Z"`
	for _, tc := range []struct {
		name, credential, want string
	}{
		{"admin", tokenFor(t, adminCaller), `{"items":[{` + listed + `,"webhook_url":"https://hooks.example.com/secret"}]}`},
		{"viewer", tokenFor(t, viewerCaller), `{"items":[{` + listed + `}]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			alerts := fakeAlerts{rules: []store.AlertRule{rule}}
			callAs(t, newServer(t, api.Config{Alerts: &alerts}), tc.credential, "GET", "/api/v1/alert-rules", "", nil).
				check(t, 200, tc.want)
		})
	}
}

func dump(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
