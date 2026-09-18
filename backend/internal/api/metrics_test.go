package api_test

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mairuu/loghub/backend/internal/api"
	"github.com/mairuu/loghub/backend/internal/limiter"
	"github.com/mairuu/loghub/backend/internal/platform/errors"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func checkMetrics(t *testing.T, reg *prometheus.Registry, name, want string) {
	t.Helper()
	if err := testutil.GatherAndCompare(reg, strings.NewReader(want), name); err != nil {
		t.Error(err)
	}
}

// Requests count under the route they match, even when they are refused
// before routing, and a path or method nobody registered can't add a label
// value of its own.
func TestRequestsCounted(t *testing.T) {
	reg := prometheus.NewRegistry()
	h := newServer(t, api.Config{Metrics: reg})

	call(t, h, "GET", "/api/healthz", "", nil)
	callAs(t, h, "garbage", "GET", "/api/v1/events", "", nil)
	// The mux answers these in plain text, so they skip call's checks.
	for _, target := range []string{"GET /api/v1/nothing", "BREW /api/v1/nothing", "DELETE /api/v1/events"} {
		method, path, _ := strings.Cut(target, " ")
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequestWithContext(t.Context(), method, path, nil))
	}

	checkMetrics(t, reg, "loghub_http_requests_total", `
# HELP loghub_http_requests_total API requests answered, by method, route and status code.
# TYPE loghub_http_requests_total counter
loghub_http_requests_total{code="200",method="GET",route="/api/healthz"} 1
loghub_http_requests_total{code="401",method="GET",route="/api/v1/events"} 1
loghub_http_requests_total{code="404",method="GET",route="unmatched"} 1
loghub_http_requests_total{code="404",method="other",route="unmatched"} 1
loghub_http_requests_total{code="405",method="DELETE",route="unmatched"} 1
`)
	if n, err := testutil.GatherAndCount(reg, "loghub_http_request_duration_seconds"); err != nil || n != 5 {
		t.Errorf("%d duration series (%v), want one for each method and route", n, err)
	}
}

// Sign-ins count by outcome, and only once they reach the password check.
func TestSignInsCounted(t *testing.T) {
	reg := prometheus.NewRegistry()
	now := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	h := newServer(t, api.Config{
		Users:   fakeUsers{"viewer@demoa.local": {ID: 2, PasswordHash: hashPassword(t, "right"), Role: "viewer", TenantID: new("demoA")}},
		SignIns: limiter.New(&limiter.Memory{Now: func() time.Time { return now }}, "sign-in", 2),
		Metrics: reg,
	})

	for _, body := range []string{
		`{}`,
		`{"email":"viewer@demoa.local","password":"wrong"}`,
		`{"email":"viewer@demoa.local","password":"right"}`,
		`{"email":"viewer@demoa.local","password":"right"}`,
	} {
		call(t, h, "POST", "/api/v1/auth/login", "application/json", strings.NewReader(body))
	}

	checkMetrics(t, reg, "loghub_sign_ins_total", `
# HELP loghub_sign_ins_total Sign-in attempts that reached the password check, by outcome: signed_in, invalid_credentials or too_many_attempts.
# TYPE loghub_sign_ins_total counter
loghub_sign_ins_total{outcome="invalid_credentials"} 1
loghub_sign_ins_total{outcome="signed_in"} 1
loghub_sign_ins_total{outcome="too_many_attempts"} 1
`)
}

// Stored events count by tenant and source, and rejected records by code,
// whether the normalizer or the insert turned them away.
func TestIngestCounted(t *testing.T) {
	reg := prometheus.NewRegistry()
	events := fakeEvents{refuse: refuseTenant("gone")}
	h := newServer(t, api.Config{Events: &events, Metrics: reg})

	body := strings.Join([]string{
		`{"tenant":"demoA","source":"api","event_type":"a"}`,
		`{"tenant":"demoB","source":"crowdstrike","event_type":"b"}`,
		`{"tenant":"demoA","source":"api","event_type":"c"}`,
		`{"tenant":"gone","source":"api"}`,
		`{"source":"api"}`,
	}, "\n")
	callAs(t, h, testIngestKey, "POST", "/api/v1/ingest/batch", "application/x-ndjson", strings.NewReader(body)).
		check(t, 200, `{"accepted":3,"rejected":2,"errors":[
			{"index":3,"code":"unknown_tenant","message":"tenant \"gone\" does not exist"},
			{"index":4,"code":"missing_tenant","message":"tenant is required"}]}`)

	checkMetrics(t, reg, "loghub_ingested_events_total", `
# HELP loghub_ingested_events_total Events stored, by tenant and source.
# TYPE loghub_ingested_events_total counter
loghub_ingested_events_total{source="api",tenant="demoA"} 2
loghub_ingested_events_total{source="crowdstrike",tenant="demoB"} 1
`)
	checkMetrics(t, reg, "loghub_rejected_records_total", `
# HELP loghub_rejected_records_total Ingested records rejected, by error code.
# TYPE loghub_rejected_records_total counter
loghub_rejected_records_total{code="missing_tenant"} 1
loghub_rejected_records_total{code="unknown_tenant"} 1
`)
}

// A batch the database refuses as a whole is sent again, so it counts
// nothing.
func TestFailedBatchNotCounted(t *testing.T) {
	reg := prometheus.NewRegistry()
	events := fakeEvents{insertErr: errors.Unavailable("database_unavailable", "cannot reach the database")}
	h := newServer(t, api.Config{Events: &events, Metrics: reg})

	body := `{"tenant":"demoA","source":"api"}` + "\n" + `{"source":"api"}`
	callAs(t, h, testIngestKey, "POST", "/api/v1/ingest/batch", "application/x-ndjson", strings.NewReader(body)).
		check(t, 503, errorBody("database_unavailable", "cannot reach the database"))

	for _, name := range []string{"loghub_ingested_events_total", "loghub_rejected_records_total"} {
		if n, err := testutil.GatherAndCount(reg, name); err != nil || n != 0 {
			t.Errorf("%d %s series (%v), want none", n, name, err)
		}
	}
}
