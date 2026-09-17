package alerting_test

import (
	"bytes"
	"context"
	"encoding/json"
	stderrors "errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mairuu/loghub/backend/internal/alerting"
	"github.com/mairuu/loghub/backend/internal/api/gen"
	"github.com/mairuu/loghub/backend/internal/platform/errors"
	"github.com/mairuu/loghub/backend/internal/store"
)

func TestWindowEnd(t *testing.T) {
	at := func(clock string) time.Time {
		v, err := time.Parse("15:04:05.000", clock)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	for _, tc := range []struct{ now, want string }{
		{"12:00:29.999", "11:59:00.000"},
		{"12:00:30.000", "12:00:00.000"},
		{"12:00:59.999", "12:00:00.000"},
		{"12:01:00.000", "12:00:00.000"},
		{"12:01:30.000", "12:01:00.000"},
	} {
		if got := alerting.WindowEnd(at(tc.now)); !got.Equal(at(tc.want)) {
			t.Errorf("WindowEnd(%s) = %s, want %s", tc.now, got.Format("15:04:05.000"), tc.want)
		}
	}
}

func TestRunEvaluatesEachTenantsRules(t *testing.T) {
	now := time.Date(2026, 9, 17, 12, 0, 45, 0, time.UTC)
	end := time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC)
	withWebhook := rule(1, "a", "https://hooks.example/secret-token")
	failing := rule(2, "a", "")
	quiet := rule(3, "b", "https://hooks.example/other")
	rules := &fakeRules{
		rules: map[string][]store.AlertRule{"a": {withWebhook, failing}, "b": {quiet}},
		fired: map[int64][]store.Alert{1: {alert(10, withWebhook, "203.0.113.1"), alert(11, withWebhook, "203.0.113.2")}},
		fail:  map[int64]error{2: errors.Internal("boom")},
	}
	notifier := &fakeNotifier{}
	var log logBuffer
	e := alerting.New(alerting.Config{
		Tenants:  fakeTenants{ids: []string{"a", "b"}},
		Rules:    rules,
		Logger:   log.logger(),
		Notifier: notifier,
		Now:      func() time.Time { return now },
	})

	if err := e.Run(t.Context()); err != nil {
		t.Fatal(err)
	}

	// Each tenant's rules are read as that tenant.
	wantLists := []listCall{{store.Scope{TenantID: "a"}, "a"}, {store.Scope{TenantID: "b"}, "b"}}
	if !reflect.DeepEqual(rules.lists, wantLists) {
		t.Errorf("listed %+v, want %+v", rules.lists, wantLists)
	}
	// A failing rule doesn't stop the next.
	if want := []int64{1, 2, 3}; !slices.Equal(rules.evaluated, want) {
		t.Errorf("evaluated rules %v, want %v", rules.evaluated, want)
	}
	for _, got := range rules.ends {
		if !got.Equal(end) {
			t.Errorf("evaluated a window ending %v, want %v", got, end)
		}
	}
	// Only new firings are delivered, each to its own rule's URL.
	want := []sent{
		{"https://hooks.example/secret-token", alerting.Body(rules.fired[1][0])},
		{"https://hooks.example/secret-token", alerting.Body(rules.fired[1][1])},
	}
	if !reflect.DeepEqual(notifier.sent, want) {
		t.Errorf("sent %+v\nwant %+v", notifier.sent, want)
	}

	msgs := log.messages(t)
	if want := []string{"alert raised", "webhook delivered", "alert raised", "webhook delivered", "cannot evaluate an alert rule"}; !slices.Equal(msgs, want) {
		t.Errorf("logged %q, want %q", msgs, want)
	}
	if strings.Contains(log.String(), "secret-token") {
		t.Errorf("the log holds the webhook URL:\n%s", log.String())
	}
}

func TestRunCarriesOnPastFailures(t *testing.T) {
	r := rule(1, "b", "https://hooks.example/x")
	rules := &fakeRules{
		rules:     map[string][]store.AlertRule{"b": {r}},
		fired:     map[int64][]store.Alert{1: {alert(10, r, "fw01")}},
		listFails: map[string]error{"a": errors.Unavailable("database_unavailable", "cannot reach the database")},
	}
	var log logBuffer
	e := alerting.New(alerting.Config{
		Tenants:  fakeTenants{ids: []string{"a", "b"}},
		Rules:    rules,
		Logger:   log.logger(),
		Notifier: &fakeNotifier{err: stderrors.New("webhook answered 500 Internal Server Error")},
	})

	if err := e.Run(t.Context()); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(rules.evaluated, []int64{1}) {
		t.Errorf("evaluated %v, want tenant b's rule", rules.evaluated)
	}
	want := []string{"cannot list a tenant's alert rules", "alert raised", "webhook delivery failed"}
	if got := log.messages(t); !slices.Equal(got, want) {
		t.Errorf("logged %q, want %q", got, want)
	}
	entries := log.entries(t)
	if entries[0]["level"] != "WARN" || entries[0]["tenant"] != "a" {
		t.Errorf("tenant failure logged as %v, want a warning naming the tenant", entries[0])
	}
	if entries[2]["level"] != "WARN" || entries[2]["webhook_host"] != "hooks.example" {
		t.Errorf("delivery failure logged as %v", entries[2])
	}
}

func TestRunFailsWithoutTenants(t *testing.T) {
	e := alerting.New(alerting.Config{
		Tenants: fakeTenants{err: stderrors.New("no database")},
		Rules:   &fakeRules{},
		Logger:  slog.New(slog.DiscardHandler),
	})
	if err := e.Run(t.Context()); err == nil {
		t.Error("Run succeeded without a tenant list")
	}
}

func TestRunStopsWhenCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	rules := &fakeRules{
		rules: map[string][]store.AlertRule{"a": {rule(1, "a", ""), rule(2, "a", "")}},
		// Shutdown arrives during the first rule.
		evaluate: func(context.Context) error { cancel(); return context.Canceled },
	}
	var log logBuffer
	e := alerting.New(alerting.Config{
		Tenants: fakeTenants{ids: []string{"a", "b"}},
		Rules:   rules,
		Logger:  log.logger(),
	})

	if err := e.Run(ctx); err != context.Canceled {
		t.Errorf("Run = %v, want context.Canceled", err)
	}
	if !slices.Equal(rules.evaluated, []int64{1}) {
		t.Errorf("evaluated %v after being stopped", rules.evaluated)
	}
	if got := log.messages(t); len(got) != 0 {
		t.Errorf("logged %q; being stopped isn't a failure", got)
	}
}

func TestWebhookSend(t *testing.T) {
	body := alerting.Body(alert(7, rule(3, "demoA", ""), "203.0.113.77"))

	t.Run("delivered", func(t *testing.T) {
		type request struct {
			method, contentType string
			body                []byte
		}
		received := make(chan request, 1)
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			b, _ := io.ReadAll(r.Body)
			received <- request{r.Method, r.Header.Get("Content-Type"), b}
			w.WriteHeader(http.StatusNoContent)
		}))
		defer srv.Close()

		if err := alerting.NewWebhook(nil).Send(t.Context(), srv.URL+"/hook", body); err != nil {
			t.Fatal(err)
		}
		got := <-received
		if got.method != "POST" || got.contentType != "application/json" {
			t.Errorf("got %s with Content-Type %q", got.method, got.contentType)
		}
		var sentBody gen.Alert
		if err := json.Unmarshal(got.body, &sentBody); err != nil || !reflect.DeepEqual(sentBody, body) {
			t.Errorf("received %s (%v), want %+v", got.body, err, body)
		}
	})

	for _, tc := range []struct {
		name    string
		handler http.HandlerFunc
		want    string
	}{
		{"refused", func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "nope", http.StatusInternalServerError)
		}, "webhook answered 500 Internal Server Error"},
		{"redirected", func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "/elsewhere", http.StatusTemporaryRedirect)
		}, "webhook answered 307 Temporary Redirect"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var requests atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				tc.handler(w, r)
			}))
			defer srv.Close()
			err := alerting.NewWebhook(nil).Send(t.Context(), srv.URL+"/hook", body)
			if err == nil || err.Error() != tc.want {
				t.Errorf("Send = %v, want %s", err, tc.want)
			}
			if n := requests.Load(); n != 1 {
				t.Errorf("%d requests, want 1", n)
			}
		})
	}

	t.Run("slow", func(t *testing.T) {
		release := make(chan struct{})
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			select {
			case <-release:
			case <-r.Context().Done():
			}
		}))
		defer srv.Close()
		defer close(release)
		ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
		defer cancel()
		if err := alerting.NewWebhook(nil).Send(ctx, srv.URL, body); err == nil {
			t.Error("Send waited for a receiver that never answered")
		}
	})

	t.Run("unreachable, without the URL in the error", func(t *testing.T) {
		srv := httptest.NewServer(http.NotFoundHandler())
		target := srv.URL + "/secret-token"
		srv.Close()
		err := alerting.NewWebhook(nil).Send(t.Context(), target, body)
		if err == nil || strings.Contains(err.Error(), "secret-token") {
			t.Errorf("Send = %v", err)
		}
	})
}

func rule(id int64, tenant, webhook string) store.AlertRule {
	r := store.AlertRule{ID: id, TenantID: tenant, Name: "rule", GroupBy: "src_ip", Threshold: 3, WindowMinutes: 5}
	if webhook != "" {
		r.WebhookURL = &webhook
	}
	return r
}

func alert(id int64, r store.AlertRule, key string) store.Alert {
	at := time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC)
	return store.Alert{
		ID: id, RuleID: r.ID, RuleName: r.Name, GroupBy: r.GroupBy, TenantID: r.TenantID, GroupKey: key,
		WindowStart: at.Add(-5 * time.Minute), WindowEnd: at, Matched: 3, CreatedAt: at.Add(time.Minute),
	}
}

type fakeTenants struct {
	ids []string
	err error
}

func (f fakeTenants) ListIDs(context.Context) ([]string, error) { return f.ids, f.err }

type listCall struct {
	scope  store.Scope
	tenant string
}

type fakeRules struct {
	rules     map[string][]store.AlertRule
	listFails map[string]error
	fired     map[int64][]store.Alert
	fail      map[int64]error
	evaluate  func(context.Context) error

	lists     []listCall
	evaluated []int64
	ends      []time.Time
}

func (f *fakeRules) ListRules(_ context.Context, scope store.Scope, tenant string) ([]store.AlertRule, error) {
	f.lists = append(f.lists, listCall{scope, tenant})
	return f.rules[tenant], f.listFails[tenant]
}

func (f *fakeRules) Evaluate(ctx context.Context, r store.AlertRule, end time.Time) ([]store.Alert, error) {
	f.evaluated = append(f.evaluated, r.ID)
	f.ends = append(f.ends, end)
	if f.evaluate != nil {
		if err := f.evaluate(ctx); err != nil {
			return nil, err
		}
	}
	return f.fired[r.ID], f.fail[r.ID]
}

type sent struct {
	url   string
	alert gen.Alert
}

type fakeNotifier struct {
	err  error
	sent []sent
}

func (f *fakeNotifier) Send(_ context.Context, url string, a gen.Alert) error {
	f.sent = append(f.sent, sent{url, a})
	return f.err
}

type logBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *logBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *logBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func (b *logBuffer) logger() *slog.Logger { return slog.New(slog.NewJSONHandler(b, nil)) }

func (b *logBuffer) entries(t *testing.T) []map[string]any {
	t.Helper()
	var out []map[string]any
	dec := json.NewDecoder(strings.NewReader(b.String()))
	for dec.More() {
		var e map[string]any
		if err := dec.Decode(&e); err != nil {
			t.Fatal(err)
		}
		out = append(out, e)
	}
	return out
}

func (b *logBuffer) messages(t *testing.T) []string {
	t.Helper()
	var out []string
	for _, e := range b.entries(t) {
		out = append(out, e["msg"].(string))
	}
	return out
}
