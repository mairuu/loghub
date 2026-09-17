package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mairuu/loghub/backend/internal/api"
	"github.com/mairuu/loghub/backend/internal/auth"
	"github.com/mairuu/loghub/backend/internal/authz"
	"github.com/mairuu/loghub/backend/internal/platform/errors"
	"github.com/mairuu/loghub/backend/internal/store"
	"github.com/mairuu/loghub/backend/internal/store/storetest"
)

func TestMain(m *testing.M) { storetest.Main(m) }

// fakeEvents stands in for the store, so the handlers are tested without a
// database. The end-to-end tests use the real one.
type fakeEvents struct {
	// refuse, when set, decides each event's insert rejection.
	refuse    func(store.NewEventParams) error
	insertErr error
	inserted  [][]store.NewEventParams

	page      store.EventPage
	searchErr error
	search    func() // runs inside Search, for panics
	searched  []store.SearchParams
	// scopes are the scopes of every search and count, in order.
	scopes []store.Scope

	top       store.Top
	timeline  store.Timeline
	countErr  error
	tops      []store.TopParams
	timelines []store.TimelineParams
}

func (f *fakeEvents) Insert(_ context.Context, events []store.NewEventParams) ([]error, error) {
	f.inserted = append(f.inserted, events)
	if f.insertErr != nil {
		return nil, f.insertErr
	}
	rejected := make([]error, len(events))
	if f.refuse != nil {
		for i, e := range events {
			rejected[i] = f.refuse(e)
		}
	}
	return rejected, nil
}

func (f *fakeEvents) Search(_ context.Context, scope store.Scope, p store.SearchParams) (store.EventPage, error) {
	f.searched = append(f.searched, p)
	f.scopes = append(f.scopes, scope)
	if f.search != nil {
		f.search()
	}
	return f.page, f.searchErr
}

func (f *fakeEvents) Top(_ context.Context, scope store.Scope, p store.TopParams) (store.Top, error) {
	f.tops = append(f.tops, p)
	f.scopes = append(f.scopes, scope)
	return f.top, f.countErr
}

func (f *fakeEvents) Timeline(_ context.Context, scope store.Scope, p store.TimelineParams) (store.Timeline, error) {
	f.timelines = append(f.timelines, p)
	f.scopes = append(f.scopes, scope)
	return f.timeline, f.countErr
}

// read reports whether anything was searched or counted.
func (f *fakeEvents) read() bool {
	return len(f.searched)+len(f.tops)+len(f.timelines) > 0
}

// filters are the filters of every search and count, in order of kind.
func (f *fakeEvents) filters() []store.EventFilter {
	var out []store.EventFilter
	for _, p := range f.searched {
		out = append(out, p.EventFilter)
	}
	for _, p := range f.tops {
		out = append(out, p.EventFilter)
	}
	for _, p := range f.timelines {
		out = append(out, p.EventFilter)
	}
	return out
}

func refuseTenant(tenant string) func(store.NewEventParams) error {
	return func(e store.NewEventParams) error {
		if e.TenantID == tenant {
			return errors.Invalid("unknown_tenant", fmt.Sprintf("tenant %q does not exist", tenant))
		}
		return nil
	}
}

const (
	testSecret    = "test-auth-secret-test-auth-secret"
	testIngestKey = "test-ingest-key-test-ingest-key-0"
)

var (
	adminCaller  = authz.Caller{Role: authz.Admin, UserID: 1}
	viewerCaller = authz.Caller{Role: authz.Viewer, Tenant: "demoA", UserID: 2}
)

// newServer serves the API with the test secrets, to requests as they are
// sent. Unset parts of cfg get stand-ins.
func newServer(t *testing.T, cfg api.Config) http.Handler {
	t.Helper()
	tokens := newTokens(t, nil)
	authn, err := auth.NewAuthenticator(tokens, testIngestKey)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Tokens, cfg.Authenticator = tokens, authn
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.NewJSONHandler(t.Output(), &slog.HandlerOptions{Level: slog.LevelDebug}))
	}
	if cfg.Events == nil {
		cfg.Events = &fakeEvents{}
	}
	if cfg.Users == nil {
		cfg.Users = fakeUsers{}
	}
	if cfg.Tenants == nil {
		cfg.Tenants = &fakeTenants{}
	}
	if cfg.Alerts == nil {
		cfg.Alerts = &fakeAlerts{}
	}
	if cfg.Ready == nil {
		cfg.Ready = func(context.Context) error { return nil }
	}
	s, err := api.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return s.Handler()
}

// newHandler serves the API to requests that come from an admin unless they
// carry a credential of their own. An admin may ingest and search every
// tenant, as any request could before authentication.
func newHandler(t *testing.T, events api.Events) http.Handler {
	t.Helper()
	return newHandlerLogging(t, events, t.Output())
}

func newHandlerLogging(t *testing.T, events api.Events, log io.Writer) http.Handler {
	t.Helper()
	h := newServer(t, api.Config{
		Logger: slog.New(slog.NewJSONHandler(log, &slog.HandlerOptions{Level: slog.LevelDebug})),
		Events: events,
	})
	return asAdmin(t, h)
}

func asAdmin(t *testing.T, h http.Handler) http.Handler {
	admin := tokenFor(t, adminCaller)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "" {
			r.Header.Set("Authorization", "Bearer "+admin)
		}
		h.ServeHTTP(w, r)
	})
}

// newTokens signs with the test secret, at a fixed time if at is set.
func newTokens(t *testing.T, at *time.Time) *auth.Tokens {
	t.Helper()
	cfg := auth.TokenConfig{Secret: testSecret}
	if at != nil {
		cfg.Now = func() time.Time { return *at }
	}
	tokens, err := auth.NewTokens(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return tokens
}

func tokenFor(t *testing.T, c authz.Caller) string {
	t.Helper()
	token, _, err := newTokens(t, nil).Issue(c)
	if err != nil {
		t.Fatal(err)
	}
	return token
}

type response struct {
	status    int
	requestID string
	raw       string
	body      map[string]any
	header    http.Header
}

// call sends a request and checks what every response shares: a request ID,
// and a JSON body that repeats it if it is an error.
func call(t *testing.T, h http.Handler, method, target, contentType string, body io.Reader) response {
	t.Helper()
	return callAs(t, h, "", method, target, contentType, body)
}

// callAs is call with credential as the bearer token, unless it is empty.
func callAs(t *testing.T, h http.Handler, credential, method, target, contentType string, body io.Reader) response {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), method, target, body)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if credential != "" {
		req.Header.Set("Authorization", "Bearer "+credential)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	res := response{
		status: rec.Code, requestID: rec.Header().Get("X-Request-Id"), raw: rec.Body.String(),
		header: rec.Header(),
	}
	if len(res.requestID) != 16 {
		t.Errorf("X-Request-Id = %q, want 16 characters", res.requestID)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("Content-Type = %q, body %s", ct, rec.Body)
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &res.body); err != nil {
		t.Fatalf("body is not a JSON object: %v: %s", err, rec.Body)
	}
	if challenge := res.header.Get("WWW-Authenticate"); (res.status == 401) != (challenge != "") {
		t.Errorf("status %d with WWW-Authenticate %q", res.status, challenge)
	}
	if res.status >= 400 {
		if id := res.body["request_id"]; id != res.requestID {
			t.Errorf("request_id = %v, header says %s", id, res.requestID)
		}
		delete(res.body, "request_id")
	}
	return res
}

// check compares the status and the body, less request_id.
func (r response) check(t *testing.T, status int, body string) {
	t.Helper()
	var want map[string]any
	if err := json.Unmarshal([]byte(body), &want); err != nil {
		t.Fatalf("bad expectation %s: %v", body, err)
	}
	if r.status != status || !reflect.DeepEqual(r.body, want) {
		got, _ := json.Marshal(r.body)
		t.Errorf("got %d %s\nwant %d %s", r.status, got, status, body)
	}
}

func errorBody(code, message string) string {
	b, _ := json.Marshal(map[string]string{"code": code, "message": message})
	return string(b)
}

const apiEvent = `{"tenant":"demoA","source":"api","event_type":"app_login_failed","user":"alice","ip":"203.0.113.7"}`

func TestIngestEvent(t *testing.T) {
	for _, tc := range []struct {
		name        string
		contentType string
		body        string
		events      fakeEvents
		status      int
		want        string
		wantInserts int
	}{
		{
			name: "stored", contentType: "application/json; charset=utf-8", body: apiEvent,
			status: 200, want: `{"accepted":1,"rejected":0}`, wantInserts: 1,
		},
		{
			name: "rejected by the normalizer", contentType: "application/json", body: `{"source":"api"}`,
			status: 200, want: `{"accepted":0,"rejected":1,"errors":[{"index":0,"code":"missing_tenant","message":"tenant is required"}]}`,
		},
		{
			name: "rejected by the store", contentType: "application/json", body: apiEvent,
			events: fakeEvents{refuse: refuseTenant("demoA")},
			status: 200, want: `{"accepted":0,"rejected":1,"errors":[{"index":0,"code":"unknown_tenant","message":"tenant \"demoA\" does not exist"}]}`,
			wantInserts: 1,
		},
		{
			name: "not JSON", contentType: "application/json", body: `{"tenant":`,
			status: 400, want: errorBody("invalid_json", "request body is not a JSON object"),
		},
		{
			name: "JSON but not an object", contentType: "application/json", body: `[` + apiEvent + `]`,
			status: 400, want: errorBody("invalid_json", "request body is not a JSON object"),
		},
		{
			name: "empty", contentType: "application/json", body: ``,
			status: 400, want: errorBody("invalid_json", "request body is not a JSON object"),
		},
		{
			name: "no content type", body: apiEvent,
			status: 415, want: errorBody("unsupported_media_type", "expected application/json"),
		},
		{
			name: "NDJSON", contentType: "application/x-ndjson", body: apiEvent,
			status: 415, want: errorBody("unsupported_media_type", "expected application/json"),
		},
		{
			name: "too large", contentType: "application/json",
			body:   `{"tenant":"demoA","source":"api","pad":"` + strings.Repeat("x", api.MaxRecordBytes) + `"}`,
			status: 413, want: errorBody("body_too_large", "request body exceeds 1 MiB"),
		},
		{
			name: "database unreachable", contentType: "application/json", body: apiEvent,
			events: fakeEvents{insertErr: errors.Unavailable("database_unavailable", "cannot reach the database")},
			status: 503, want: errorBody("database_unavailable", "cannot reach the database"),
			wantInserts: 1,
		},
		{
			name: "database failure", contentType: "application/json", body: apiEvent,
			events: fakeEvents{insertErr: errors.Internalf(io.ErrUnexpectedEOF, "cannot insert events")},
			status: 500, want: errorBody("internal_error", "an unexpected error occurred"),
			wantInserts: 1,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHandler(t, &tc.events)
			call(t, h, "POST", "/api/v1/ingest", tc.contentType, strings.NewReader(tc.body)).check(t, tc.status, tc.want)
			if got := len(tc.events.inserted); got != tc.wantInserts {
				t.Errorf("%d inserts, want %d", got, tc.wantInserts)
			}
		})
	}

	t.Run("normalized event is what gets stored", func(t *testing.T) {
		var events fakeEvents
		call(t, newHandler(t, &events), "POST", "/api/v1/ingest", "application/json", strings.NewReader(apiEvent))
		if len(events.inserted) != 1 || len(events.inserted[0]) != 1 {
			t.Fatalf("inserted %v", events.inserted)
		}
		e := events.inserted[0][0]
		if e.TenantID != "demoA" || e.Source != "api" || *e.UserName != "alice" || e.SrcIP.String() != "203.0.113.7" {
			t.Errorf("stored %+v", e)
		}
	})
}

func TestIngestInternalErrorStaysInLog(t *testing.T) {
	var log bytes.Buffer
	events := fakeEvents{insertErr: errors.Internalf(fmt.Errorf("relation is on fire"), "cannot insert events")}
	res := call(t, newHandlerLogging(t, &events, &log), "POST", "/api/v1/ingest", "application/json", strings.NewReader(apiEvent))

	if got := fmt.Sprint(res.body); strings.Contains(got, "fire") {
		t.Errorf("response leaks the cause: %s", got)
	}
	var found bool
	for _, entry := range logEntries(t, &log) {
		if entry["level"] == "ERROR" && entry["request_id"] == res.requestID {
			found = true
			if msg, _ := entry["error"].(string); !strings.Contains(msg, "relation is on fire") {
				t.Errorf("logged error lacks the cause: %v", entry)
			}
		}
	}
	if !found {
		t.Errorf("no error logged for request %s:\n%s", res.requestID, log.String())
	}
}

func logEntries(t *testing.T, log *bytes.Buffer) []map[string]any {
	t.Helper()
	var out []map[string]any
	for line := range strings.Lines(log.String()) {
		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("log line is not JSON: %s", line)
		}
		out = append(out, entry)
	}
	return out
}

// A collector never reads the response, so rejections must reach the log.
func TestIngestRejectionsLogged(t *testing.T) {
	var log bytes.Buffer
	events := fakeEvents{refuse: refuseTenant("gone")}
	h := newHandlerLogging(t, &events, &log)
	body := strings.Join([]string{apiEvent, `not json`, `{"tenant":"gone","source":"api"}`, `[]`}, "\n")
	res := call(t, h, "POST", "/api/v1/ingest/batch", "application/x-ndjson", strings.NewReader(body))

	var got []map[string]any
	for _, entry := range logEntries(t, &log) {
		if entry["msg"] == "records rejected" {
			delete(entry, "time")
			got = append(got, entry)
		}
	}
	want := map[string]any{
		"level": "WARN", "msg": "records rejected", "request_id": res.requestID,
		"path": "/api/v1/ingest/batch", "accepted": 1.0, "rejected": 3.0,
		"codes": map[string]any{"invalid_json": 2.0, "unknown_tenant": 1.0},
		"first": map[string]any{"index": 1.0, "code": "invalid_json", "message": "record is not a JSON object"},
	}
	if len(got) != 1 || !reflect.DeepEqual(got[0], want) {
		t.Errorf("logged %v\nwant %v", got, want)
	}

	log.Reset()
	call(t, h, "POST", "/api/v1/ingest/batch", "application/x-ndjson", strings.NewReader(apiEvent))
	for _, entry := range logEntries(t, &log) {
		if entry["msg"] == "records rejected" {
			t.Errorf("a clean batch logged %v", entry)
		}
	}
}

func TestIngestBatch(t *testing.T) {
	var events fakeEvents
	events.refuse = refuseTenant("gone")
	body := strings.Join([]string{
		0: apiEvent,
		1: ``,
		2: `   `,
		3: `not json`,
		4: `{"tenant":"demoA","message":"<134>Aug 20 12:44:56 fw01 action=deny src=10.0.1.10"}` + "\r",
		5: `{"tenant":"demoA","source":"nope"}`,
		6: strings.Repeat("x", api.MaxRecordBytes+1),
		7: `{"tenant":"gone","source":"api"}`,
		8: `{"tenant":"demoB","source":"ad"}`, // no newline at the end
	}, "\n")

	res := call(t, newHandler(t, &events), "POST", "/api/v1/ingest/batch", "application/x-ndjson", strings.NewReader(body))
	res.check(t, 200, `{"accepted":3,"rejected":4,"errors":[
		{"index":3,"code":"invalid_json","message":"record is not a JSON object"},
		{"index":5,"code":"unknown_source","message":"source \"nope\" is not a known source category"},
		{"index":6,"code":"record_too_large","message":"record exceeds 1 MiB"},
		{"index":7,"code":"unknown_tenant","message":"tenant \"gone\" does not exist"}
	]}`)

	if len(events.inserted) != 1 {
		t.Fatalf("%d inserts, want one for the whole batch", len(events.inserted))
	}
	var got []string
	for _, e := range events.inserted[0] {
		got = append(got, e.TenantID+"/"+e.Source)
	}
	if want := []string{"demoA/api", "demoA/firewall", "gone/api", "demoB/ad"}; !slices.Equal(got, want) {
		t.Errorf("inserted %q, want %q", got, want)
	}
}

func TestIngestBatchSizes(t *testing.T) {
	// fill is a record of exactly n bytes.
	fill := func(n int) string {
		const head, tail = `{"tenant":"demoA","source":"api","pad":"`, `"}`
		return head + strings.Repeat("x", n-len(head)-len(tail)) + tail
	}
	tooLarge := `"code":"record_too_large","message":"record exceeds 1 MiB"`

	for _, tc := range []struct {
		name   string
		body   string
		status int
		want   string
	}{
		{"longest record", fill(api.MaxRecordBytes) + "\r\n", 200, `{"accepted":1,"rejected":0}`},
		{"record one byte over", fill(api.MaxRecordBytes+1) + "\r\n", 200, `{"accepted":0,"rejected":1,"errors":[{"index":0,` + tooLarge + `}]}`},
		{"surrounding space counts", " " + fill(api.MaxRecordBytes) + "\n", 200, `{"accepted":0,"rejected":1,"errors":[{"index":0,` + tooLarge + `}]}`},
		{"longest record, no line ending", fill(api.MaxRecordBytes), 200, `{"accepted":1,"rejected":0}`},
		{"one byte over, LF only", fill(api.MaxRecordBytes+1) + "\n", 200, `{"accepted":0,"rejected":1,"errors":[{"index":0,` + tooLarge + `}]}`},
		{
			"records either side of one too long",
			apiEvent + "\n" + fill(3*api.MaxRecordBytes) + "\n" + apiEvent,
			200, `{"accepted":2,"rejected":1,"errors":[{"index":1,` + tooLarge + `}]}`,
		},
		{"too long at the end", apiEvent + "\n" + fill(api.MaxRecordBytes+10), 200, `{"accepted":1,"rejected":1,"errors":[{"index":1,` + tooLarge + `}]}`},
		{"largest body", strings.Repeat("x", api.MaxNDJSONBytes), 200, `{"accepted":0,"rejected":1,"errors":[{"index":0,` + tooLarge + `}]}`},
		{"body one byte over", strings.Repeat("x", api.MaxNDJSONBytes+1), 413, errorBody("body_too_large", "request body exceeds 32 MiB")},
		{"empty body", "", 200, `{"accepted":0,"rejected":0}`},
		{"only blank lines", "\n\r\n  \n", 200, `{"accepted":0,"rejected":0}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var events fakeEvents
			res := call(t, newHandler(t, &events), "POST", "/api/v1/ingest/batch", "application/x-ndjson", strings.NewReader(tc.body))
			res.check(t, tc.status, tc.want)
			if tc.status != 200 && len(events.inserted) > 0 {
				t.Error("a failed request stored events")
			}
		})
	}
}

// errors[] is capped, and stays in request order when normalizer and store
// rejections interleave.
func TestIngestBatchReportsFirstRejections(t *testing.T) {
	events := fakeEvents{refuse: refuseTenant("gone")}
	const n = 3 * api.MaxReported
	var lines []string
	for i := range n {
		if i%2 == 0 {
			lines = append(lines, `not json`)
		} else {
			lines = append(lines, `{"tenant":"gone","source":"api"}`)
		}
	}
	res := call(t, newHandler(t, &events), "POST", "/api/v1/ingest/batch", "application/x-ndjson", strings.NewReader(strings.Join(lines, "\n")))

	if res.status != 200 || res.body["accepted"] != 0.0 || res.body["rejected"] != float64(n) {
		t.Fatalf("got %d %v", res.status, res.body)
	}
	reported, _ := res.body["errors"].([]any)
	if len(reported) != api.MaxReported {
		t.Fatalf("%d errors reported, want %d", len(reported), api.MaxReported)
	}
	for i, r := range reported {
		r := r.(map[string]any)
		want := "invalid_json"
		if i%2 == 1 {
			want = "unknown_tenant"
		}
		if r["index"] != float64(i) || r["code"] != want {
			t.Errorf("errors[%d] = %v, want index %d %s", i, r, i, want)
		}
	}
}

func TestIngestFile(t *testing.T) {
	body := `{"event_type":"CreateUser"}` + "\n" + `{"tenant":"demoA","source":"ad"}`

	t.Run("defaults fill what records omit", func(t *testing.T) {
		var events fakeEvents
		res := call(t, newHandler(t, &events), "POST", "/api/v1/ingest/file?tenant=demoB&source=AWS", "application/x-ndjson", strings.NewReader(body))
		res.check(t, 200, `{"accepted":2,"rejected":0}`)
		var got []string
		for _, e := range events.inserted[0] {
			got = append(got, e.TenantID+"/"+e.Source)
		}
		if want := []string{"demoB/aws", "demoA/ad"}; !slices.Equal(got, want) {
			t.Errorf("inserted %q, want %q", got, want)
		}
	})

	t.Run("without defaults", func(t *testing.T) {
		var events fakeEvents
		res := call(t, newHandler(t, &events), "POST", "/api/v1/ingest/file?tenant=&source=", "application/x-ndjson", strings.NewReader(body))
		res.check(t, 200, `{"accepted":1,"rejected":1,"errors":[{"index":0,"code":"missing_tenant","message":"tenant is required"}]}`)
	})

	for _, tc := range []struct {
		name, query, contentType string
		status                   int
		want                     string
	}{
		{"bad tenant", "tenant=demo+A", "application/x-ndjson", 400, errorBody("invalid_parameter", "tenant must be 1 to 64 letters, digits, '-' or '_'")},
		{"unknown source", "source=syslog", "application/x-ndjson", 400, errorBody("invalid_parameter", `source "syslog" is not a known source category`)},
		{"JSON", "", "application/json", 415, errorBody("unsupported_media_type", "expected application/x-ndjson")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var events fakeEvents
			res := call(t, newHandler(t, &events), "POST", "/api/v1/ingest/file?"+tc.query, tc.contentType, strings.NewReader(body))
			res.check(t, tc.status, tc.want)
			if len(events.inserted) > 0 {
				t.Error("a failed request stored events")
			}
		})
	}
}

func TestSearchEventsParams(t *testing.T) {
	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 1, 2, 0, 0, 0, 0, time.FixedZone("", 7*3600))
	for _, tc := range []struct {
		name, query string
		want        store.SearchParams
	}{
		{"none", "", store.SearchParams{}},
		{
			"all",
			"tenant=demoA&from=2026-01-01T00:00:00Z&to=2026-01-02T00:00:00%2B07:00" +
				"&source=aws&source=AD&event_type=CreateUser&action=create" +
				"&severity_min=1&severity_max=9&src_ip=10.0.0.1&user=alice&host=h" +
				"&tag=a&tag=b%20c&q=50%25+off&limit=10&cursor=abc&order=asc",
			store.SearchParams{
				EventFilter: store.EventFilter{
					Tenant: "demoA", From: &from, To: &to,
					Sources: []string{"aws", "AD"}, EventType: "CreateUser", Action: "create",
					SeverityMin: new(1), SeverityMax: new(9),
					SrcIP: "10.0.0.1", User: "alice", Host: "h",
					Tags: []string{"a", "b c"}, Query: "50% off",
				},
				Limit: 10, Cursor: "abc", Order: store.OrderAsc,
			},
		},
		{"blank values are no filter", "tenant=&source=&limit=1000", store.SearchParams{EventFilter: store.EventFilter{Sources: []string{""}}, Limit: 1000}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var events fakeEvents
			call(t, newHandler(t, &events), "GET", "/api/v1/events?"+tc.query, "", nil).check(t, 200, `{"items":[]}`)
			if len(events.searched) != 1 {
				t.Fatalf("%d searches", len(events.searched))
			}
			got := events.searched[0]
			// Times are compared as instants.
			if !sameTime(got.From, tc.want.From) || !sameTime(got.To, tc.want.To) {
				t.Errorf("window = %v..%v, want %v..%v", got.From, got.To, tc.want.From, tc.want.To)
			}
			got.From, got.To, tc.want.From, tc.want.To = nil, nil, nil, nil
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("searched with\n%+v\nwant\n%+v", got, tc.want)
			}
			// An admin's search sees every tenant.
			if events.scopes[0] != store.AdminScope {
				t.Errorf("scope = %+v", events.scopes[0])
			}
		})
	}
}

func sameTime(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Equal(*b)
}

func TestSearchEventsRejects(t *testing.T) {
	const limitMessage = "limit must be an integer between 1 and 1000"
	const timeFormat = " must be an RFC 3339 timestamp, with '+' in the offset escaped as %2B"
	for _, tc := range []struct {
		name, query string
		events      fakeEvents
		status      int
		want        string
		wantSearch  bool
	}{
		{name: "limit zero", query: "limit=0", status: 400, want: errorBody("invalid_parameter", limitMessage)},
		{name: "limit not a number", query: "limit=ten", status: 400, want: errorBody("invalid_parameter", limitMessage)},
		{name: "limit past int32", query: "limit=99999999999", status: 400, want: errorBody("invalid_parameter", limitMessage)},
		{name: "from not a time", query: "from=yesterday", status: 400, want: errorBody("invalid_parameter", "from"+timeFormat)},
		{name: "to with an unescaped plus", query: "to=2026-01-02T00:00:00+07:00", status: 400, want: errorBody("invalid_parameter", "to"+timeFormat)},
		{name: "severity not a number", query: "severity_min=high", status: 400, want: errorBody("invalid_parameter", "severity_min must be an integer between 0 and 10")},
		{name: "unknown source", query: "source=aws&source=syslog", status: 400, want: errorBody("invalid_parameter", `source "syslog" is not a known source category`)},
		{
			name: "store refuses a parameter", query: "cursor=x",
			events: fakeEvents{searchErr: errors.Malformed("invalid_cursor", "cursor is not one this API issued")},
			status: 400, want: errorBody("invalid_cursor", "cursor is not one this API issued"), wantSearch: true,
		},
		{
			name: "search times out", query: "q=x",
			events: fakeEvents{searchErr: errors.Unavailable("search_timeout", "the search took longer than 10s")},
			status: 503, want: errorBody("search_timeout", "the search took longer than 10s"), wantSearch: true,
		},
		{
			name: "handler panics", query: "",
			events: fakeEvents{search: func() { panic("boom") }},
			status: 500, want: errorBody("internal_error", "an unexpected error occurred"), wantSearch: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			call(t, newHandler(t, &tc.events), "GET", "/api/v1/events?"+tc.query, "", nil).check(t, tc.status, tc.want)
			if searched := len(tc.events.searched) > 0; searched != tc.wantSearch {
				t.Errorf("searched = %v, want %v", searched, tc.wantSearch)
			}
		})
	}
}

func TestSearchEventsResponse(t *testing.T) {
	bangkok := time.FixedZone("ICT", 7*3600)
	src, dst := netip.MustParseAddr("10.0.0.1"), netip.MustParseAddr("2001:db8::1")
	events := fakeEvents{page: store.EventPage{
		Events: []store.Event{
			{
				ID: 9007199254740993, Ts: time.Date(2026, 1, 1, 12, 0, 0, 123000000, bangkok),
				ReceivedAt: time.Date(2026, 1, 1, 5, 0, 1, 0, time.UTC), TenantID: "demoA", Source: "firewall",
				Vendor: new("demo"), Product: new("ngfw"), EventType: new("traffic"), EventSubtype: new("dns"),
				Severity: new(int16(7)), Action: new("deny"),
				SrcIP: &src, SrcPort: new(int32(5353)), DstIP: &dst, DstPort: new(int32(53)), Protocol: new("udp"),
				UserName: new("alice"), Host: new("fw01"), Process: new("pf"),
				URL: new("https://example.com/"), HTTPMethod: new("GET"), StatusCode: new(int32(403)),
				RuleName: new("Block-DNS"), RuleID: new("42"), CloudRegion: new("ap-southeast-1"),
				Raw: []byte(`{"message": "<134>x & y"}`), Tags: []string{"auth_failure"},
			},
			{
				ID: 2, Ts: time.Date(2026, 1, 1, 4, 0, 0, 0, time.UTC), ReceivedAt: time.Date(2026, 1, 1, 4, 0, 0, 0, time.UTC),
				TenantID: "demoB", Source: "api", Raw: []byte(`{}`),
			},
		},
		NextCursor: "next",
	}}

	res := call(t, newHandler(t, &events), "GET", "/api/v1/events", "", nil)
	res.check(t, 200, `{
		"items": [
			{
				"id": "9007199254740993", "@timestamp": "2026-01-01T05:00:00.123Z", "received_at": "2026-01-01T05:00:01Z",
				"tenant": "demoA", "source": "firewall", "vendor": "demo", "product": "ngfw",
				"event_type": "traffic", "event_subtype": "dns", "severity": 7, "action": "deny",
				"src_ip": "10.0.0.1", "src_port": 5353, "dst_ip": "2001:db8::1", "dst_port": 53, "protocol": "udp",
				"user": "alice", "host": "fw01", "process": "pf",
				"url": "https://example.com/", "http_method": "GET", "status_code": 403,
				"rule_name": "Block-DNS", "rule_id": "42", "cloud": {"region": "ap-southeast-1"},
				"raw": {"message": "<134>x & y"}, "_tags": ["auth_failure"]
			},
			{
				"id": "2", "@timestamp": "2026-01-01T04:00:00Z", "received_at": "2026-01-01T04:00:00Z",
				"tenant": "demoB", "source": "api", "raw": {}, "_tags": []
			}
		],
		"next_cursor": "next"
	}`)
	// Syslog lines are stored with their <PRI>, and should read as sent.
	if !strings.Contains(res.raw, `"<134>x & y"`) {
		t.Errorf("raw is escaped: %s", res.raw)
	}
}

func TestHealth(t *testing.T) {
	for _, tc := range []struct {
		name   string
		ready  error
		status int
		want   string
	}{
		{"ready", nil, 200, `{"status":"ok"}`},
		{"database unreachable", errors.Unavailable("database_unavailable", "cannot reach the database"), 503, errorBody("database_unavailable", "cannot reach the database")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newServer(t, api.Config{
				Ready: func(ctx context.Context) error {
					if _, ok := ctx.Deadline(); !ok {
						t.Error("readiness check has no deadline")
					}
					return tc.ready
				},
			})
			// Without a credential: the container healthcheck sends none.
			call(t, h, "GET", "/api/healthz", "", nil).check(t, tc.status, tc.want)
		})
	}
}

func TestSpecServed(t *testing.T) {
	res := call(t, newHandler(t, &fakeEvents{}), "GET", "/api/openapi.json", "", nil)
	if res.status != 200 || res.body["openapi"] != "3.0.3" {
		t.Errorf("got %d, openapi %v", res.status, res.body["openapi"])
	}

	rec := httptest.NewRecorder()
	newHandler(t, &fakeEvents{}).ServeHTTP(rec, httptest.NewRequest("GET", "/api/docs", nil))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "/api/openapi.json") {
		t.Errorf("docs page: %d %s", rec.Code, rec.Body)
	}
	// The script runs on the UI's origin, so it must be exactly the one
	// reviewed.
	if !regexp.MustCompile(`<script\s+src="https://cdn\.jsdelivr\.net/npm/@scalar/api-reference@\d+\.\d+\.\d+/[^"]+"\s+integrity="sha384-[A-Za-z0-9+/]{64}"\s+crossorigin="anonymous">`).MatchString(rec.Body.String()) {
		t.Errorf("docs page loads Scalar unpinned:\n%s", rec.Body)
	}
}

func TestRequestIDsDiffer(t *testing.T) {
	h := newHandler(t, &fakeEvents{})
	seen := map[string]bool{}
	for range 100 {
		id := call(t, h, "GET", "/api/healthz", "", nil).requestID
		if seen[id] {
			t.Fatalf("request ID %s repeated", id)
		}
		seen[id] = true
	}
}
