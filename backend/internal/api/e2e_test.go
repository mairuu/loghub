package api_test

import (
	"bufio"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mairuu/loghub/backend/internal/api"
	"github.com/mairuu/loghub/backend/internal/platform/pg"
	"github.com/mairuu/loghub/backend/internal/store"
	"github.com/mairuu/loghub/backend/internal/store/storetest"
)

// Every sample goes in through the HTTP API and comes back out of search,
// against the real store, as loghub_app.
func TestSamplesEndToEnd(t *testing.T) {
	db := storetest.Open(t)
	tenant := db.Tenant(t)
	h := api.New(api.Config{
		Logger: slog.New(slog.NewJSONHandler(t.Output(), nil)),
		Events: store.NewEventRepo(store.New(db.App)),
		Ready:  func(ctx context.Context) error { return pg.Check(ctx, db.App) },
	}).Handler()

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
