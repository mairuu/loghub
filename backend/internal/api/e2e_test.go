package api_test

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mairuu/loghub/backend/internal/alerting"
	"github.com/mairuu/loghub/backend/internal/api"
	"github.com/mairuu/loghub/backend/internal/authz"
	"github.com/mairuu/loghub/backend/internal/platform/pg"
	"github.com/mairuu/loghub/backend/internal/store"
	"github.com/mairuu/loghub/backend/internal/store/storetest"
)

// Every sample goes in through the HTTP API and comes back out of search,
// against the real store, as loghub_app.
func TestSamplesEndToEnd(t *testing.T) {
	db := storetest.Open(t)
	tenant := db.Tenant(t)
	h := asAdmin(t, newServer(t, api.Config{
		Logger: slog.New(slog.NewJSONHandler(t.Output(), nil)),
		Events: store.NewEventRepo(store.New(db.App)),
		Users:  store.NewUserRepo(store.New(db.App)),
		Ready:  func(ctx context.Context) error { return pg.Check(ctx, db.App) },
	}))

	call(t, h, "GET", "/api/healthz", "", nil).check(t, 200, `{"status":"ok"}`)

	// JSON samples one at a time, as an application would send them.
	paths, _ := filepath.Glob("../../../samples/json/*.json")
	if len(paths) == 0 {
		t.Fatal("no JSON samples")
	}
	for _, p := range paths {
		var record map[string]any
		if err := json.Unmarshal(readSample(t, p), &record); err != nil {
			t.Fatalf("%s: %v", p, err)
		}
		record["tenant"] = tenant
		body, _ := json.Marshal(record)
		call(t, h, "POST", "/api/v1/ingest", "application/json", strings.NewReader(string(body))).
			check(t, 200, `{"accepted":1,"rejected":0}`)
	}

	// Syslog lines as a batch, wrapped the way the collector wraps them, with
	// a line for a tenant that doesn't exist and one that isn't JSON.
	var lines []string
	paths, _ = filepath.Glob("../../../samples/syslog/*.log")
	for _, p := range paths {
		sc := bufio.NewScanner(strings.NewReader(string(readSample(t, p))))
		for sc.Scan() {
			b, _ := json.Marshal(map[string]string{
				"tenant":      tenant,
				"message":     sc.Text(),
				"received_at": time.Now().UTC().Format(time.RFC3339),
				"peer_ip":     "192.0.2.1",
				"input":       "syslog_udp",
			})
			lines = append(lines, string(b))
		}
	}
	if len(lines) != 2 {
		t.Fatalf("%d syslog lines, want 2", len(lines))
	}
	lines = append(lines, `{"tenant":"no_such_tenant","source":"api"}`, `<134>not wrapped`)
	call(t, h, "POST", "/api/v1/ingest/batch", "application/x-ndjson", strings.NewReader(strings.Join(lines, "\n")+"\n")).
		check(t, 200, `{"accepted":2,"rejected":2,"errors":[
			{"index":2,"code":"unknown_tenant","message":"tenant \"no_such_tenant\" does not exist"},
			{"index":3,"code":"invalid_json","message":"record is not a JSON object"}
		]}`)

	// The default window covers them all: the samples' 2025 times are
	// rebased to receipt, and the syslog lines carry no year.
	all := search(t, h, url.Values{"tenant": {tenant}})
	if got, want := sources(all), []string{"ad", "api", "aws", "crowdstrike", "firewall", "m365", "network"}; !slices.Equal(got, want) {
		t.Fatalf("sources = %q, want %q", got, want)
	}

	aws := search(t, h, url.Values{"tenant": {tenant}, "q": {"TEMP-USER"}})
	if len(aws) != 1 {
		t.Fatalf("free text found %d events, want the CloudTrail one", len(aws))
	}
	var got struct {
		Tenant string `json:"tenant"`
		Cloud  struct {
			AccountID string `json:"account_id"`
			Region    string `json:"region"`
			Service   string `json:"service"`
		} `json:"cloud"`
		Raw struct {
			RequestParameters map[string]string `json:"requestParameters"`
		} `json:"raw"`
	}
	remarshal(t, aws[0], &got)
	if got.Tenant != tenant || got.Cloud.AccountID != "123456789012" || got.Cloud.Region != "ap-southeast-1" ||
		got.Cloud.Service != "iam" || got.Raw.RequestParameters["userName"] != "temp-user" {
		t.Errorf("CloudTrail event came back as %+v", got)
	}

	for _, tc := range []struct {
		name  string
		query url.Values
		want  []string
	}{
		{"several sources", url.Values{"source": {"firewall", "NETWORK"}}, []string{"firewall", "network"}},
		{"failed logins, whatever the vendor", url.Values{"tag": {"auth_failure"}}, []string{"ad", "api"}},
		{"denied traffic", url.Values{"action": {"deny"}, "src_ip": {"10.0.1.10"}}, []string{"firewall"}},
		{"severity", url.Values{"severity_min": {"8"}}, []string{"crowdstrike"}},
		{"a tenant with no events", url.Values{"tenant": {db.Tenant(t)}}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q := url.Values{"tenant": {tenant}}
			for k, v := range tc.query {
				q[k] = v
			}
			if got := sources(search(t, h, q)); !slices.Equal(got, tc.want) {
				t.Errorf("sources = %q, want %q", got, tc.want)
			}
		})
	}

	t.Run("paging", func(t *testing.T) {
		q := url.Values{"tenant": {tenant}, "limit": {"3"}, "order": {"asc"}}
		var ids []string
		var pages int
		for {
			res := call(t, h, "GET", "/api/v1/events?"+q.Encode(), "", nil)
			if res.status != 200 {
				t.Fatalf("page %d: %d %v", pages+1, res.status, res.body)
			}
			pages++
			for _, item := range res.body["items"].([]any) {
				ids = append(ids, item.(map[string]any)["id"].(string))
			}
			cursor, ok := res.body["next_cursor"].(string)
			if !ok {
				break
			}
			q.Set("cursor", cursor)
		}
		slices.Sort(ids)
		if pages != 3 || len(slices.Compact(ids)) != len(all) {
			t.Errorf("%d distinct events in %d pages, want %d in 3", len(ids), pages, len(all))
		}

		// A cursor belongs to the filters it came from.
		q.Set("order", "desc")
		call(t, h, "GET", "/api/v1/events?"+q.Encode(), "", nil).
			check(t, 400, errorBody("invalid_cursor", "cursor was issued for different parameters; start again without it"))
	})
}

// The alerting acceptance check, against the real store as loghub_app: an
// admin creates the failed-login rule, the collector sends failed logins, and
// after one evaluation the tenant's viewer sees the alert and the rule's
// webhook has received it.
func TestAlertingEndToEnd(t *testing.T) {
	db := storetest.Open(t)
	a, b := db.Tenant(t), db.Tenant(t)
	appStore := store.New(db.App)
	alerts := store.NewAlertRepo(appStore)
	h := newServer(t, api.Config{
		Events: store.NewEventRepo(appStore),
		Users:  store.NewUserRepo(appStore),
		Alerts: alerts,
		Ready:  func(ctx context.Context) error { return pg.Check(ctx, db.App) },
	})
	admin := tokenFor(t, adminCaller)
	viewerA := tokenFor(t, authz.Caller{Role: authz.Viewer, Tenant: a, UserID: 2})
	viewerB := tokenFor(t, authz.Caller{Role: authz.Viewer, Tenant: b, UserID: 3})

	received := make(chan []byte, 10)
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		received <- body
	}))
	defer hook.Close()

	rule := `{"tenant":"` + a + `","name":"failed logins","tags":["auth_failure"],"group_by":"src_ip",
		"threshold":3,"window_minutes":5,"webhook_url":"` + hook.URL + `/loghub"}`
	callAs(t, h, viewerA, "POST", "/api/v1/alert-rules", "application/json", strings.NewReader(rule)).
		check(t, 403, errorBody("permission_denied", "you may not do this"))
	if res := callAs(t, h, admin, "POST", "/api/v1/alert-rules", "application/json", strings.NewReader(rule)); res.status != 201 {
		t.Fatalf("create rule: %d %v", res.status, res.body)
	}

	// Three failed logins from one address in each tenant, and only a has a
	// rule. The sample's 2025 time is replaced with the time of receipt.
	var lines []string
	for _, tenant := range []string{a, b, a, b, a, b} {
		var record map[string]any
		if err := json.Unmarshal(readSample(t, "../../../samples/json/ad_4625.json"), &record); err != nil {
			t.Fatal(err)
		}
		record["tenant"] = tenant
		line, _ := json.Marshal(record)
		lines = append(lines, string(line))
	}
	callAs(t, h, testIngestKey, "POST", "/api/v1/ingest/batch", "application/x-ndjson", strings.NewReader(strings.Join(lines, "\n"))).
		check(t, 200, `{"accepted":6,"rejected":0}`)

	// A minute on, the window holds all three.
	evaluator := alerting.New(alerting.Config{
		Tenants: store.NewTenantRepo(appStore),
		Rules:   alerts,
		Logger:  slog.New(slog.NewJSONHandler(t.Output(), nil)),
		Now:     func() time.Time { return time.Now().Add(alerting.Lag + time.Minute) },
	})
	if err := evaluator.Run(t.Context()); err != nil {
		t.Fatal(err)
	}

	listed := callAs(t, h, viewerA, "GET", "/api/v1/alerts", "", nil)
	items, _ := listed.body["items"].([]any)
	if listed.status != 200 || len(items) != 1 {
		t.Fatalf("viewer of a lists %d %v, want one alert", listed.status, listed.body)
	}
	got := items[0].(map[string]any)
	if got["tenant"] != a || got["rule_name"] != "failed logins" || got["group_by"] != "src_ip" ||
		got["group_key"] != "203.0.113.77" || got["count"] != 3.0 {
		t.Errorf("alert %v", got)
	}

	if len(received) != 1 {
		t.Fatalf("webhook received %d requests, want 1", len(received))
	}
	var delivered map[string]any
	if err := json.Unmarshal(<-received, &delivered); err != nil || !reflect.DeepEqual(delivered, got) {
		t.Errorf("webhook received %v (%v), want what the API lists, %v", delivered, err, got)
	}

	callAs(t, h, viewerB, "GET", "/api/v1/alerts", "", nil).check(t, 200, `{"items":[]}`)
	rules := callAs(t, h, viewerA, "GET", "/api/v1/alert-rules", "", nil)
	if items, _ := rules.body["items"].([]any); len(items) != 1 || items[0].(map[string]any)["webhook_url"] != nil {
		t.Errorf("viewer of a lists rules %v, want one without its webhook", rules.body)
	}

	t.Run("evaluated again", func(t *testing.T) {
		if err := evaluator.Run(t.Context()); err != nil {
			t.Fatal(err)
		}
		res := callAs(t, h, admin, "GET", "/api/v1/alerts?tenant="+a, "", nil)
		if items, _ := res.body["items"].([]any); len(items) != 1 {
			t.Errorf("admin lists %v, want the one alert", res.body)
		}
		if len(received) != 0 {
			t.Errorf("webhook received %d more requests", len(received))
		}
	})
}

func readSample(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// search returns the items of a single page of results.
func search(t *testing.T, h http.Handler, q url.Values) []any {
	t.Helper()
	res := call(t, h, "GET", "/api/v1/events?"+q.Encode(), "", nil)
	if res.status != 200 {
		t.Fatalf("search %s: %d %v", q.Encode(), res.status, res.body)
	}
	if _, more := res.body["next_cursor"]; more {
		t.Fatalf("search %s has more than one page", q.Encode())
	}
	items, _ := res.body["items"].([]any)
	return items
}

func sources(items []any) []string {
	var out []string
	for _, item := range items {
		out = append(out, item.(map[string]any)["source"].(string))
	}
	slices.Sort(out)
	return out
}

func remarshal(t *testing.T, from, to any) {
	t.Helper()
	b, err := json.Marshal(from)
	if err == nil {
		err = json.Unmarshal(b, to)
	}
	if err != nil {
		t.Fatal(err)
	}
}

// The RBAC acceptance check, against the real store as loghub_app: a viewer
// who signs in sees only their own tenant, and asking for another is refused.
func TestRBACEndToEnd(t *testing.T) {
	db := storetest.Open(t)
	a, b := db.Tenant(t), db.Tenant(t)
	const password = "correct horse"
	hash := hashPassword(t, password)
	users := store.NewUserRepo(store.New(db.Owner))
	emails := map[string]string{"admin": "admin@" + a + ".test", a: "viewer@" + a + ".test", b: "viewer@" + b + ".test"}
	for tenant, email := range emails {
		p := store.NewUserParams{Email: email, PasswordHash: hash, Role: "viewer", TenantID: &tenant}
		if tenant == "admin" {
			p.Role, p.TenantID = "admin", nil
		}
		if _, err := users.CreateIfMissing(t.Context(), p); err != nil {
			t.Fatal(err)
		}
	}

	h := newServer(t, api.Config{
		Events: store.NewEventRepo(store.New(db.App)),
		Users:  store.NewUserRepo(store.New(db.App)),
		Ready:  func(ctx context.Context) error { return pg.Check(ctx, db.App) },
	})

	// The collector sends both tenants' events in one batch.
	var lines []string
	for _, tenant := range []string{a, b, a, b} {
		lines = append(lines, `{"tenant":"`+tenant+`","source":"api","event_type":"rbac"}`)
	}
	callAs(t, h, testIngestKey, "POST", "/api/v1/ingest/batch", "application/x-ndjson", strings.NewReader(strings.Join(lines, "\n"))).
		check(t, 200, `{"accepted":4,"rejected":0}`)

	signIn := func(email, password string) response {
		body, _ := json.Marshal(map[string]string{"email": email, "password": password})
		return call(t, h, "POST", "/api/v1/auth/login", "application/json", strings.NewReader(string(body)))
	}
	token := func(email string) string {
		res := signIn(email, password)
		if res.status != 200 {
			t.Fatalf("sign in as %s: %d %v", email, res.status, res.body)
		}
		return res.body["token"].(string)
	}
	badCredentials := errorBody("invalid_credentials", "email or password is incorrect")
	signIn(emails[a], "wrong").check(t, 401, badCredentials)
	signIn("nobody@"+a+".test", password).check(t, 401, badCredentials)

	admin, viewerA := token(emails["admin"]), token(strings.ToUpper(emails[a]))
	viewerB := token(emails[b])

	// tenants lists the tenant of every event a search returns.
	tenants := func(credential string, q url.Values) []string {
		t.Helper()
		q.Set("event_type", "rbac")
		res := callAs(t, h, credential, "GET", "/api/v1/events?"+q.Encode(), "", nil)
		if res.status != 200 {
			t.Fatalf("search %s: %d %v", q.Encode(), res.status, res.body)
		}
		var out []string
		for _, item := range res.body["items"].([]any) {
			out = append(out, item.(map[string]any)["tenant"].(string))
		}
		slices.Sort(out)
		return out
	}
	for _, tc := range []struct {
		name       string
		credential string
		query      url.Values
		want       []string
	}{
		{"viewer sees their own tenant", viewerA, url.Values{}, []string{a, a}},
		{"other viewer sees theirs", viewerB, url.Values{}, []string{b, b}},
		{"viewer names their own tenant", viewerA, url.Values{"tenant": {a}}, []string{a, a}},
		{"admin sees every tenant", admin, url.Values{}, []string{a, a, b, b}},
		{"admin narrows to one", admin, url.Values{"tenant": {b}}, []string{b, b}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// The tenant IDs are random, so a and b sort either way.
			want := slices.Sorted(slices.Values(tc.want))
			if got := tenants(tc.credential, tc.query); !slices.Equal(got, want) {
				t.Errorf("tenants %q, want %q", got, want)
			}
		})
	}

	t.Run("viewer asks for another tenant", func(t *testing.T) {
		callAs(t, h, viewerA, "GET", "/api/v1/events?tenant="+b, "", nil).
			check(t, 403, errorBody("tenant_not_permitted", `you may not access tenant "`+b+`"`))
	})
	t.Run("viewer can't ingest", func(t *testing.T) {
		callAs(t, h, viewerA, "POST", "/api/v1/ingest", "application/json", strings.NewReader(lines[0])).
			check(t, 403, errorBody("permission_denied", "you may not do this"))
	})
	t.Run("collector can't search", func(t *testing.T) {
		callAs(t, h, testIngestKey, "GET", "/api/v1/events", "", nil).
			check(t, 403, errorBody("permission_denied", "you may not do this"))
	})
	t.Run("anonymous can't search", func(t *testing.T) {
		call(t, h, "GET", "/api/v1/events", "", nil).
			check(t, 401, errorBody("authentication_required", "this request needs a credential"))
	})
}
