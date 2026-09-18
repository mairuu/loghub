package api_test

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mairuu/loghub/backend/internal/api"
	"github.com/mairuu/loghub/backend/internal/auth"
	"github.com/mairuu/loghub/backend/internal/authz"
	"github.com/mairuu/loghub/backend/internal/limiter"
	"github.com/mairuu/loghub/backend/internal/platform/errors"
	"github.com/mairuu/loghub/backend/internal/store"
)

// fakeUsers stands in for the users table, keyed by lowercase email.
type fakeUsers map[string]store.User

func (f fakeUsers) FindByEmail(_ context.Context, email string) (store.User, error) {
	u, ok := f[strings.ToLower(email)]
	if !ok {
		return store.User{}, errors.NotFound("user_not_found", "no user has this email")
	}
	return u, nil
}

type failingUsers struct{ err error }

func (f failingUsers) FindByEmail(context.Context, string) (store.User, error) {
	return store.User{}, f.err
}

func hashPassword(t *testing.T, password string) string {
	t.Helper()
	hash, err := auth.HashPassword(password)
	if err != nil {
		t.Fatal(err)
	}
	return hash
}

func TestLogin(t *testing.T) {
	hash := hashPassword(t, "right")
	users := fakeUsers{
		"admin@loghub.local":   {ID: 1, PasswordHash: hash, Role: "admin"},
		"viewer@demoa.local":   {ID: 2, PasswordHash: hash, Role: "viewer", TenantID: new("demoA")},
		"strange@loghub.local": {ID: 3, PasswordHash: hash, Role: "root"},
	}
	badCredentials := errorBody("invalid_credentials", "email or password is incorrect")
	missing := `{"code":"invalid_request","message":"the request contains invalid fields","errors":[
		{"field":"email","code":"required","message":"email is required"},
		{"field":"password","code":"required","message":"password is required"}]}`

	for _, tc := range []struct {
		name        string
		users       api.Users
		credential  string
		contentType string
		body        string
		status      int
		// want is the whole body of a failure.
		want string
		// role and tenant are what a success reports.
		role, tenant string
	}{
		{name: "viewer", body: `{"email":"viewer@demoa.local","password":"right"}`, status: 200, role: "viewer", tenant: "demoA"},
		{name: "admin", body: `{"email":"admin@loghub.local","password":"right"}`, status: 200, role: "admin"},
		{name: "email in any case, with space", body: `{"email":"  Viewer@DemoA.local ","password":"right"}`, status: 200, role: "viewer", tenant: "demoA"},
		{name: "unknown fields are ignored", body: `{"email":"admin@loghub.local","password":"right","remember":true}`, status: 200, role: "admin"},
		{name: "already signed in", credential: "viewer", body: `{"email":"admin@loghub.local","password":"right"}`, status: 200, role: "admin"},
		{name: "wrong password", body: `{"email":"viewer@demoa.local","password":"wrong"}`, status: 401, want: badCredentials},
		{name: "password in another case", body: `{"email":"viewer@demoa.local","password":"RIGHT"}`, status: 401, want: badCredentials},
		{name: "unknown email", body: `{"email":"nobody@demoa.local","password":"right"}`, status: 401, want: badCredentials},
		{name: "password with space around", body: `{"email":"viewer@demoa.local","password":" right"}`, status: 401, want: badCredentials},
		{name: "invalid token", credential: "garbage", body: `{"email":"admin@loghub.local","password":"right"}`, status: 401,
			want: errorBody("invalid_token", "the token is invalid or has expired; sign in again")},
		{name: "empty object", body: `{}`, status: 422, want: missing},
		{name: "blank values", body: `{"email":"  ","password":""}`, status: 422, want: missing},
		{name: "password not a string", body: `{"email":"admin@loghub.local","password":123}`, status: 422,
			want: `{"code":"invalid_request","message":"the request contains invalid fields","errors":[
				{"field":"password","code":"invalid_type","message":"password must be a string"}]}`},
		{name: "not JSON", body: `email=admin`, status: 400, want: errorBody("invalid_json", "request body is not a JSON object")},
		{name: "null", body: `null`, status: 400, want: errorBody("invalid_json", "request body is not a JSON object")},
		{name: "array", body: `[]`, status: 400, want: errorBody("invalid_json", "request body is not a JSON object")},
		{name: "form post", contentType: "application/x-www-form-urlencoded", body: `email=admin`, status: 415,
			want: errorBody("unsupported_media_type", "expected application/json")},
		{name: "too large", body: `{"email":"admin@loghub.local","password":"` + strings.Repeat("x", 64<<10) + `"}`, status: 413,
			want: errorBody("body_too_large", "request body exceeds 64 KiB")},
		{name: "database unreachable", users: failingUsers{errors.Unavailable("database_unavailable", "cannot reach the database")}, body: `{"email":"admin@loghub.local","password":"right"}`, status: 503,
			want: errorBody("database_unavailable", "cannot reach the database")},
		{name: "user with an unknown role", body: `{"email":"strange@loghub.local","password":"right"}`, status: 500,
			want: errorBody("internal_error", "an unexpected error occurred")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.users == nil {
				tc.users = users
			}
			if tc.contentType == "" {
				tc.contentType = "application/json"
			}
			switch tc.credential {
			case "viewer":
				tc.credential = tokenFor(t, viewerCaller)
			}
			h := newServer(t, api.Config{Users: tc.users})
			start := time.Now()
			res := callAs(t, h, tc.credential, "POST", "/api/v1/auth/login", tc.contentType, strings.NewReader(tc.body))
			end := time.Now()
			if tc.status != 200 {
				res.check(t, tc.status, tc.want)
				return
			}

			if res.status != 200 {
				t.Fatalf("got %d %v", res.status, res.body)
			}
			if cc := res.header.Get("Cache-Control"); cc != "no-store" {
				t.Errorf("Cache-Control = %q", cc)
			}
			token, _ := res.body["token"].(string)
			caller, err := newTokens(t, nil).Parse(token)
			if err != nil {
				t.Fatalf("token does not parse: %v", err)
			}
			if caller.Role.String() != tc.role || caller.Tenant != tc.tenant || caller.UserID == 0 {
				t.Errorf("token is for %+v, want %s of %q", caller, tc.role, tc.tenant)
			}
			tenant, hasTenant := res.body["tenant"]
			if res.body["role"] != tc.role || hasTenant != (tc.tenant != "") || (hasTenant && tenant != tc.tenant) {
				t.Errorf("body says role %v tenant %v, want %s %q", res.body["role"], tenant, tc.role, tc.tenant)
			}
			// Token times are whole seconds.
			expires, err := time.Parse(time.RFC3339, res.body["expires_at"].(string))
			if err != nil || expires.Before(start.Truncate(time.Second).Add(auth.TokenTTL)) || expires.After(end.Add(auth.TokenTTL)) {
				t.Errorf("expires_at %v for a request between %v and %v", res.body["expires_at"], start, end)
			}
			if fields := len(res.body); fields != 3 && !(hasTenant && fields == 4) {
				t.Errorf("unexpected fields in %v", res.body)
			}
		})
	}
}

// A failed sign-in is logged with the email tried, and never the password.
func TestLoginLogged(t *testing.T) {
	var log bytes.Buffer
	users := fakeUsers{"viewer@demoa.local": {ID: 2, PasswordHash: hashPassword(t, "right"), Role: "viewer", TenantID: new("demoA")}}
	h := newServer(t, api.Config{
		Users:  users,
		Logger: slog.New(slog.NewJSONHandler(&log, &slog.HandlerOptions{Level: slog.LevelDebug})),
	})

	for _, password := range []string{"secret-guess", "right"} {
		body := `{"email":"viewer@demoa.local","password":"` + password + `"}`
		call(t, h, "POST", "/api/v1/auth/login", "application/json", strings.NewReader(body))
	}
	if strings.Contains(log.String(), "secret-guess") {
		t.Errorf("a password reached the log:\n%s", log.String())
	}
	var got []string
	for _, entry := range logEntries(t, &log) {
		switch entry["msg"] {
		case "sign-in refused":
			got = append(got, "refused "+entry["email"].(string)+" from "+entry["client"].(string))
		case "signed in":
			got = append(got, "signed in "+entry["role"].(string))
		}
	}
	if want := []string{"refused viewer@demoa.local from 192.0.2.1", "signed in viewer"}; !slices.Equal(got, want) {
		t.Errorf("logged %q, want %q", got, want)
	}
}

// forwardedFor sends every request on as Caddy does, from the address given.
func forwardedFor(h http.Handler, value string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Header.Set("X-Forwarded-For", value)
		h.ServeHTTP(w, r)
	})
}

// Each client address has its own allowance of sign-in attempts, right or
// wrong, and is told how much is left.
func TestLoginRateLimited(t *testing.T) {
	users := fakeUsers{"viewer@demoa.local": {ID: 2, PasswordHash: hashPassword(t, "right"), Role: "viewer", TenantID: new("demoA")}}
	var log bytes.Buffer
	now := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	h := newServer(t, api.Config{
		Users:   users,
		SignIns: limiter.New(&limiter.Memory{Now: func() time.Time { return now }}, "sign-in", 2),
		Logger:  slog.New(slog.NewJSONHandler(&log, &slog.HandlerOptions{Level: slog.LevelDebug})),
	})
	signIn := func(client, password string) response {
		t.Helper()
		body := `{"email":"viewer@demoa.local","password":"` + password + `"}`
		target := h
		if client != "" {
			target = forwardedFor(h, client)
		}
		return call(t, target, "POST", "/api/v1/auth/login", "application/json", strings.NewReader(body))
	}
	headers := func(res response) string {
		t.Helper()
		return strings.Join([]string{
			res.header.Get("RateLimit-Limit"), res.header.Get("RateLimit-Remaining"),
			res.header.Get("RateLimit-Reset"), res.header.Get("Retry-After"),
		}, " ")
	}
	limited := errorBody("too_many_attempts", "too many sign-in attempts from this address; try again in 30 seconds")

	for _, step := range []struct {
		name, client, password string
		status                 int
		// headers are RateLimit-Limit, -Remaining, -Reset and Retry-After.
		headers string
	}{
		{"wrong password", "198.51.100.7", "wrong", 401, "2 1 30 "},
		{"right password", "198.51.100.7", "right", 200, "2 0 60 "},
		{"one too many", "198.51.100.7", "right", 429, "2 0 60 30"},
		{"another address", "198.51.100.8", "right", 200, "2 1 30 "},
		{"only the last proxy is believed", "198.51.100.9, 198.51.100.7", "right", 429, "2 0 60 30"},
		{"straight to the backend", "", "right", 200, "2 1 30 "},
		{"IPv6", "2001:db8:1:2::1", "right", 200, "2 1 30 "},
		{"another IPv6 address in the /64", "2001:db8:1:2::ffff", "right", 200, "2 0 60 "},
		{"the /64's third", "2001:db8:1:2:abcd::1", "right", 429, "2 0 60 30"},
		{"another /64", "2001:db8:1:3::1", "right", 200, "2 1 30 "},
		{"IPv4 as IPv6", "::ffff:198.51.100.7", "right", 429, "2 0 60 30"},
	} {
		res := signIn(step.client, step.password)
		if res.status != step.status || headers(res) != step.headers {
			t.Errorf("%s: got %d with %q, want %d with %q", step.name, res.status, headers(res), step.status, step.headers)
		}
		if step.status == 429 {
			res.check(t, 429, limited)
		}
	}

	// A malformed attempt never reaches the password check, so it doesn't
	// count, and has nothing to say about the limit.
	res := call(t, forwardedFor(h, "198.51.100.10"), "POST", "/api/v1/auth/login", "application/json", strings.NewReader(`{}`))
	if res.status != 422 || res.header.Get("RateLimit-Limit") != "" {
		t.Errorf("malformed: got %d with RateLimit-Limit %q", res.status, res.header.Get("RateLimit-Limit"))
	}

	// A second later, the wait is a second shorter.
	now = now.Add(time.Second)
	if res := signIn("198.51.100.7", "right"); res.header.Get("Retry-After") != "29" {
		t.Errorf("a second later: Retry-After %q, want 29", res.header.Get("Retry-After"))
	}

	var got []string
	for _, entry := range logEntries(t, &log) {
		if entry["msg"] == "sign-in limited" {
			got = append(got, entry["email"].(string)+" from "+entry["client"].(string))
		}
	}
	if len(got) != 5 || got[0] != "viewer@demoa.local from 198.51.100.7" || got[2] != "viewer@demoa.local from 2001:db8:1:2:abcd::1" {
		t.Errorf("logged limits %q", got)
	}
}

// Every endpoint answers every kind of caller as its policies say.
func TestAccess(t *testing.T) {
	past := time.Now().Add(-auth.TokenTTL - time.Minute)
	expired, _, err := newTokens(t, &past).Issue(adminCaller)
	if err != nil {
		t.Fatal(err)
	}
	credentials := map[string]string{
		"anonymous": "",
		"garbage":   "abc.def.ghi",
		"expired":   expired,
		"collector": testIngestKey,
		"admin":     tokenFor(t, adminCaller),
		"viewer":    tokenFor(t, viewerCaller),
	}
	const (
		ok        = "200"
		created   = "201"
		needed    = "401 authentication_required"
		invalid   = "401 invalid_token"
		forbidden = "403 permission_denied"
	)
	ingest := map[string]string{
		"anonymous": needed, "garbage": invalid, "expired": invalid,
		"collector": ok, "admin": ok, "viewer": forbidden,
	}
	readEvents := map[string]string{
		"anonymous": needed, "garbage": invalid, "expired": invalid,
		"collector": forbidden, "admin": ok, "viewer": ok,
	}
	for _, tc := range []struct {
		method, target, contentType, body string
		want                              map[string]string
	}{
		{"GET", "/api/healthz", "", "", map[string]string{
			"anonymous": ok, "garbage": invalid, "expired": invalid,
			"collector": ok, "admin": ok, "viewer": ok,
		}},
		{"POST", "/api/v1/ingest", "application/json", apiEvent, ingest},
		{"POST", "/api/v1/ingest/batch", "application/x-ndjson", apiEvent, ingest},
		{"POST", "/api/v1/ingest/file?tenant=demoA", "application/x-ndjson", apiEvent, ingest},
		{"GET", "/api/v1/events", "", "", readEvents},
		{"GET", "/api/v1/events/top?field=src_ip", "", "", readEvents},
		{"GET", "/api/v1/events/timeline", "", "", readEvents},
		{"GET", "/api/v1/tenants", "", "", map[string]string{
			"anonymous": needed, "garbage": invalid, "expired": invalid,
			"collector": forbidden, "admin": ok, "viewer": ok,
		}},
		{"POST", "/api/v1/alert-rules", "application/json", failedLoginRule, map[string]string{
			"anonymous": needed, "garbage": invalid, "expired": invalid,
			"collector": forbidden, "admin": created, "viewer": forbidden,
		}},
		{"GET", "/api/v1/alert-rules", "", "", map[string]string{
			"anonymous": needed, "garbage": invalid, "expired": invalid,
			"collector": forbidden, "admin": ok, "viewer": ok,
		}},
		{"GET", "/api/v1/alerts", "", "", map[string]string{
			"anonymous": needed, "garbage": invalid, "expired": invalid,
			"collector": forbidden, "admin": ok, "viewer": ok,
		}},
	} {
		for who, credential := range credentials {
			t.Run(tc.method+" "+tc.target+" as "+who, func(t *testing.T) {
				var events fakeEvents
				var alerts fakeAlerts
				var tenants fakeTenants
				h := newServer(t, api.Config{Events: &events, Alerts: &alerts, Tenants: &tenants})
				// A refusal comes before the body is read, so a wrong content
				// type doesn't hide it.
				refused := !strings.HasPrefix(tc.want[who], "2")
				contentType := tc.contentType
				if refused && contentType != "" {
					contentType = "text/plain"
				}
				res := callAs(t, h, credential, tc.method, tc.target, contentType, strings.NewReader(tc.body))
				got := fmt.Sprint(res.status)
				if res.status >= 300 {
					got += fmt.Sprint(" ", res.body["code"])
				}
				if got != tc.want[who] {
					t.Errorf("got %s, want %s", got, tc.want[who])
				}
				if res.status == 401 {
					challenge := res.header.Get("WWW-Authenticate")
					if want := strings.HasSuffix(tc.want[who], "invalid_token"); strings.Contains(challenge, `error="invalid_token"`) != want {
						t.Errorf("WWW-Authenticate = %q", challenge)
					}
				}
				if refused && (len(events.inserted) > 0 || events.read() || alerts.called() || len(tenants.asked) > 0) {
					t.Error("a refused request reached the store")
				}
			})
		}
	}
}

// A record is stored only if the caller may write to its tenant. No role in
// the API's policies may write to some tenants and not others, so this one is
// made up: a viewer who may write to their own.
func TestIngestTenantNotPermitted(t *testing.T) {
	defer api.SetPolicies(authz.NewSet("events",
		authz.Grant(authz.Viewer).As(authz.OwnTenant).On(authz.Events).Can(authz.Create),
	))()
	viewer := tokenFor(t, viewerCaller)
	notPermitted := `"code":"tenant_not_permitted","message":"you may not access tenant \"demoB\""`

	t.Run("batch", func(t *testing.T) {
		var events fakeEvents
		body := strings.Join([]string{
			`{"tenant":"demoA","source":"api"}`,
			`{"tenant":"demoB","source":"api"}`,
			`{"source":"api"}`,
			`{"tenant":"demoA","source":"nope"}`,
		}, "\n")
		res := callAs(t, newServer(t, api.Config{Events: &events}), viewer, "POST", "/api/v1/ingest/batch", "application/x-ndjson", strings.NewReader(body))
		res.check(t, 200, `{"accepted":1,"rejected":3,"errors":[
			{"index":1,`+notPermitted+`},
			{"index":2,"code":"missing_tenant","message":"tenant is required"},
			{"index":3,"code":"unknown_source","message":"source \"nope\" is not a known source category"}
		]}`)
		if len(events.inserted) != 1 || len(events.inserted[0]) != 1 || events.inserted[0][0].TenantID != "demoA" {
			t.Errorf("inserted %+v", events.inserted)
		}
	})

	t.Run("file default", func(t *testing.T) {
		var events fakeEvents
		body := `{"source":"api"}` + "\n" + `{"tenant":"demoA","source":"api"}`
		res := callAs(t, newServer(t, api.Config{Events: &events}), viewer, "POST", "/api/v1/ingest/file?tenant=demoB", "application/x-ndjson", strings.NewReader(body))
		res.check(t, 200, `{"accepted":1,"rejected":1,"errors":[{"index":0,`+notPermitted+`}]}`)
	})

	t.Run("single event", func(t *testing.T) {
		var events fakeEvents
		res := callAs(t, newServer(t, api.Config{Events: &events}), viewer, "POST", "/api/v1/ingest", "application/json", strings.NewReader(`{"tenant":"demoB","source":"api"}`))
		res.check(t, 200, `{"accepted":0,"rejected":1,"errors":[{"index":0,`+notPermitted+`}]}`)
		if len(events.inserted) != 0 {
			t.Errorf("inserted %+v", events.inserted)
		}
	})
}

// Searches and counts read what the caller may read, as the caller.
func TestEventsScope(t *testing.T) {
	viewer, admin := tokenFor(t, viewerCaller), tokenFor(t, adminCaller)
	viewerScope := store.Scope{TenantID: "demoA"}
	for _, tc := range []struct {
		name, credential, query string
		// wantTenant is the tenant filter read with.
		wantTenant string
		wantScope  store.Scope
		refused    string
	}{
		{name: "viewer, no tenant", credential: viewer, wantTenant: "demoA", wantScope: viewerScope},
		{name: "viewer, own tenant", credential: viewer, query: "tenant=demoA", wantTenant: "demoA", wantScope: viewerScope},
		{name: "viewer, own tenant with space", credential: viewer, query: "tenant=+demoA+", wantTenant: " demoA ", wantScope: viewerScope},
		{name: "viewer, another tenant", credential: viewer, query: "tenant=demoB", refused: `you may not access tenant \"demoB\"`},
		{name: "viewer, another tenant's case", credential: viewer, query: "tenant=DEMOA", refused: `you may not access tenant \"DEMOA\"`},
		{name: "admin, no tenant", credential: admin, wantScope: store.AdminScope},
		{name: "admin, any tenant", credential: admin, query: "tenant=demoB", wantTenant: "demoB", wantScope: store.AdminScope},
	} {
		for _, path := range []string{"/api/v1/events?", "/api/v1/events/top?field=user&", "/api/v1/events/timeline?"} {
			t.Run(tc.name+" "+path, func(t *testing.T) {
				var events fakeEvents
				res := callAs(t, newServer(t, api.Config{Events: &events}), tc.credential, "GET", path+tc.query, "", nil)
				if tc.refused != "" {
					res.check(t, 403, `{"code":"tenant_not_permitted","message":"`+tc.refused+`"}`)
					if events.read() {
						t.Error("a refused request reached the store")
					}
					return
				}
				if res.status != 200 {
					t.Fatalf("got %d %v", res.status, res.body)
				}
				filters := events.filters()
				if len(filters) != 1 {
					t.Fatalf("%d reads", len(filters))
				}
				if got := filters[0].Tenant; got != tc.wantTenant {
					t.Errorf("tenant filter %q, want %q", got, tc.wantTenant)
				}
				if got := events.scopes[0]; got != tc.wantScope {
					t.Errorf("scope %+v, want %+v", got, tc.wantScope)
				}
			})
		}
	}
}

// Each request's log line says who made it.
func TestRequestLogCaller(t *testing.T) {
	var log bytes.Buffer
	h := newServer(t, api.Config{
		Logger: slog.New(slog.NewJSONHandler(&log, &slog.HandlerOptions{Level: slog.LevelDebug})),
	})
	for _, credential := range []string{"", tokenFor(t, viewerCaller), testIngestKey, "garbage"} {
		callAs(t, h, credential, "GET", "/api/v1/events", "", nil)
	}
	var got []string
	for _, entry := range logEntries(t, &log) {
		if entry["msg"] == "request" {
			line := fmt.Sprint(entry["role"])
			if id, ok := entry["user_id"]; ok {
				line += fmt.Sprint(" ", id)
			}
			got = append(got, line)
		}
	}
	if want := []string{"anonymous", "viewer 2", "collector", "anonymous"}; !slices.Equal(got, want) {
		t.Errorf("logged callers %q, want %q", got, want)
	}
}

// A feature whose policy set is empty stops the server from starting.
func TestEmptyPolicySetFailsNew(t *testing.T) {
	defer api.SetPolicies(authz.NewSet("events"))()
	_, err := api.New(api.Config{})
	if err == nil || !strings.Contains(err.Error(), `policy set "events" is empty`) {
		t.Errorf("New = %v", err)
	}
}
