package store_test

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mairuu/loghub/backend/internal/ingest"
	"github.com/mairuu/loghub/backend/internal/platform/errors"
	"github.com/mairuu/loghub/backend/internal/store"
	"github.com/mairuu/loghub/backend/internal/store/gen"
	"github.com/mairuu/loghub/backend/internal/store/storetest"
)

func TestMain(m *testing.M) { storetest.Main(m) }

func TestInsertGroupsByTenant(t *testing.T) {
	db := storetest.Open(t)
	a, b := db.Tenant(t), db.Tenant(t)
	repo := store.NewEventRepo(store.New(db.App))

	rejected, err := repo.Insert(t.Context(), []store.NewEventParams{
		event(a, "a1"), event("no_such_tenant", "x"), event(b, "b1"), event(a, "a2"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := codes(rejected), []string{"", "unknown_tenant", "", ""}; !slices.Equal(got, want) {
		t.Errorf("rejections = %q, want %q", got, want)
	}
	if got := eventTypes(stored(t, db, a)); !slices.Equal(got, []string{"a1", "a2"}) {
		t.Errorf("tenant a has %q", got)
	}
	if got := eventTypes(stored(t, db, b)); !slices.Equal(got, []string{"b1"}) {
		t.Errorf("tenant b has %q", got)
	}
}

func TestInsertRoundTrip(t *testing.T) {
	db := storetest.Open(t)
	tenant := db.Tenant(t)
	src, dst := addr("10.0.1.10"), addr("2001:db8::1")
	want := store.NewEventParams{
		Ts: time.Date(2026, 9, 16, 12, 0, 0, 123456000, time.UTC), TenantID: tenant, Source: "firewall",
		Vendor: new("demo"), Product: new("ngfw"), EventType: new("traffic"), EventSubtype: new("dns"),
		Severity: new(int16(7)), Action: new("deny"),
		SrcIP: &src, SrcPort: new(int32(5353)), DstIP: &dst, DstPort: new(int32(53)), Protocol: new("udp"),
		UserName: new("alice"), Host: new("fw01"), Process: new("pf"),
		URL: new("https://example.com/"), HTTPMethod: new("GET"), StatusCode: new(int32(403)),
		RuleName: new("Block-DNS"), RuleID: new("42"),
		CloudAccountID: new("123456789012"), CloudRegion: new("ap-southeast-1"), CloudService: new("iam"),
		Raw:  []byte(`{"message": "x", "n": [1, 2.5]}`),
		Tags: []string{"auth_failure", "invalid:spt"},
	}
	rejected, err := store.NewEventRepo(store.New(db.App)).Insert(t.Context(), []store.NewEventParams{want})
	if err != nil || rejected[0] != nil {
		t.Fatalf("insert: %v, %v", err, rejected)
	}

	rows := stored(t, db, tenant)
	if len(rows) != 1 {
		t.Fatalf("stored %d rows", len(rows))
	}
	got := rows[0]
	if !got.Ts.Equal(want.Ts) {
		t.Errorf("ts = %v, want %v", got.Ts, want.Ts)
	}
	if !sameJSON(got.Raw, want.Raw) {
		t.Errorf("raw = %s, want %s", got.Raw, want.Raw)
	}
	if time.Since(got.ReceivedAt) > time.Minute {
		t.Errorf("received_at = %v", got.ReceivedAt)
	}
	// Everything else comes back exactly as it went in.
	got.Ts, want.Ts = time.Time{}, time.Time{}
	got.Raw, want.Raw = nil, nil
	back := store.NewEventParams{
		TenantID: got.TenantID, Source: got.Source, Vendor: got.Vendor, Product: got.Product,
		EventType: got.EventType, EventSubtype: got.EventSubtype, Severity: got.Severity, Action: got.Action,
		SrcIP: got.SrcIP, SrcPort: got.SrcPort, DstIP: got.DstIP, DstPort: got.DstPort, Protocol: got.Protocol,
		UserName: got.UserName, Host: got.Host, Process: got.Process, URL: got.URL, HTTPMethod: got.HTTPMethod,
		StatusCode: got.StatusCode, RuleName: got.RuleName, RuleID: got.RuleID,
		CloudAccountID: got.CloudAccountID, CloudRegion: got.CloudRegion, CloudService: got.CloudService,
		Tags: got.Tags,
	}
	if !reflect.DeepEqual(back, want) {
		t.Errorf("stored\n got: %s\nwant: %s", dump(back), dump(want))
	}
}

// TestInsertRefusedEvents covers values the normalizer lets through but the
// database refuses: only those events are rejected, in any chunk.
func TestInsertRefusedEvents(t *testing.T) {
	db := storetest.Open(t)
	tenant := db.Tenant(t)
	defer store.SetInsertChunk(4)()

	events := make([]store.NewEventParams, 10)
	for i := range events {
		events[i] = event(tenant, "ok")
	}
	events[1].Severity = new(int16(11))              // CHECK violation
	events[6].Raw = []byte(`{"n": 1e1000000}`)       // numeric overflow in jsonb
	events[7].UserName = new(incompressible(10_000)) // too large to index

	rejected, err := store.NewEventRepo(store.New(db.App)).Insert(t.Context(), events)
	if err != nil {
		t.Fatal(err)
	}
	want := make([]string, len(events))
	want[1], want[6], want[7] = "unstorable_event", "unstorable_event", "unstorable_event"
	if got := codes(rejected); !slices.Equal(got, want) {
		t.Errorf("rejections = %q, want %q", got, want)
	}
	if n := len(stored(t, db, tenant)); n != 7 {
		t.Errorf("stored %d events, want 7", n)
	}
}

// TestInsertNormalized stores what the normalizer makes of every sample and
// of records Postgres would refuse as sent.
func TestInsertNormalized(t *testing.T) {
	db := storetest.Open(t)
	tenant := db.Tenant(t)

	const nul, surrogate = `\u0000`, `\udc00`
	records := []string{
		`{"source":"api","user":"bo` + nul + `b","` + nul + `":["` + nul + `"],"_tags":["t` + nul + `"]}`,
		`{"source":"api","user":"` + surrogate + `","note":"` + "\xff\xfe" + `"}`,
		`{"source":"api","user":"` + incompressible(10_000) + `","_tags":["` + incompressible(10_000) + `"]}`,
		`{"source":"api","raw":{"a":"` + nul + `"}}`,
		`{"message":"<13>1 2026-09-16T16:48:31.447979+07:00 tofu mairuu - - - hi` + nul + ` user=x` + nul + `"}`,
	}
	for _, path := range glob(t, "../../../samples/json/*.json") {
		records = append(records, string(readFile(t, path)))
	}
	for _, path := range glob(t, "../../../samples/syslog/*.log") {
		for line := range strings.Lines(string(readFile(t, path))) {
			if line = strings.TrimSpace(line); line != "" {
				msg, _ := json.Marshal(line)
				records = append(records, `{"message":`+string(msg)+`}`)
			}
		}
	}

	normalizer := ingest.Normalizer{}
	var events []store.NewEventParams
	for _, r := range records {
		e, err := normalizer.Normalize([]byte(r), ingest.Defaults{Tenant: tenant})
		if err != nil {
			t.Fatalf("normalize %.60s: %v", r, err)
		}
		e.TenantID = tenant // the samples name demo tenants
		events = append(events, e)
	}

	rejected, err := store.NewEventRepo(store.New(db.App)).Insert(t.Context(), events)
	if err != nil {
		t.Fatal(err)
	}
	for i, err := range rejected {
		if err != nil {
			t.Errorf("record %d rejected: %v\n%.200s", i, err, records[i])
		}
	}
	if n := len(stored(t, db, tenant)); n != len(records) {
		t.Errorf("stored %d events, want %d", n, len(records))
	}
}

func TestTenantIsolation(t *testing.T) {
	db := storetest.Open(t)
	a, b := db.Tenant(t), db.Tenant(t)
	ctx := t.Context()
	if _, err := store.NewEventRepo(store.New(db.App)).Insert(ctx, []store.NewEventParams{event(a, "a1"), event(b, "b1")}); err != nil {
		t.Fatal(err)
	}

	// visible runs in a transaction with the given context and returns the
	// tenants of the events it can see.
	visible := func(t *testing.T, tenant string, admin bool) []string {
		t.Helper()
		var seen []string
		err := pgx.BeginFunc(ctx, db.App, func(tx pgx.Tx) error {
			if tenant != "" || admin {
				if err := gen.New(tx).SetTenantContext(ctx, gen.SetTenantContextParams{TenantID: tenant, IsAdmin: admin}); err != nil {
					return err
				}
			}
			rows, err := tx.Query(ctx, `SELECT tenant_id FROM events WHERE tenant_id IN ($1, $2) ORDER BY tenant_id`, a, b)
			if err != nil {
				return err
			}
			seen, err = pgx.CollectRows(rows, pgx.RowTo[string])
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		return seen
	}

	t.Run("a tenant sees only its own events", func(t *testing.T) {
		if got := visible(t, a, false); !slices.Equal(got, []string{a}) {
			t.Errorf("tenant a sees %q", got)
		}
	})
	t.Run("no context sees nothing", func(t *testing.T) {
		if got := visible(t, "", false); len(got) != 0 {
			t.Errorf("sees %q", got)
		}
	})
	t.Run("admin sees every tenant", func(t *testing.T) {
		if got := visible(t, "", true); !slices.Equal(got, slices.Sorted(slices.Values([]string{a, b}))) {
			t.Errorf("admin sees %q", got)
		}
	})
	t.Run("a tenant cannot write another tenant's event", func(t *testing.T) {
		err := pgx.BeginFunc(ctx, db.App, func(tx pgx.Tx) error {
			if err := gen.New(tx).SetTenantContext(ctx, gen.SetTenantContextParams{TenantID: a}); err != nil {
				return err
			}
			_, err := tx.Exec(ctx, `INSERT INTO events (ts, tenant_id, source, raw) VALUES (now(), $1, 'api', '{}')`, b)
			return err
		})
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "42501" { // insufficient_privilege
			t.Errorf("err = %v, want a row-level security violation", err)
		}
	})
}

func TestInsertJoinsCallerTransaction(t *testing.T) {
	db := storetest.Open(t)
	tenant := db.Tenant(t)
	errRollback := fmt.Errorf("roll back")

	err := store.New(db.App).InTx(t.Context(), func(s *store.Store) error {
		if _, err := store.NewEventRepo(s).Insert(t.Context(), []store.NewEventParams{event(tenant, "x")}); err != nil {
			return err
		}
		return errRollback
	})
	if !errors.Is(err, errRollback) {
		t.Fatalf("err = %v", err)
	}
	if n := len(stored(t, db, tenant)); n != 0 {
		t.Errorf("stored %d events after the caller rolled back", n)
	}
}

func TestInsertUnreachable(t *testing.T) {
	pool, err := pgxpool.New(t.Context(), "postgres://nobody@127.0.0.1:1/none?connect_timeout=2")
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	_, err = store.NewEventRepo(store.New(pool)).Insert(t.Context(), []store.NewEventParams{event("demoA", "x")})
	if code := errors.CodeOf(err); code != "database_unavailable" {
		t.Errorf("code = %s (%v), want database_unavailable", code, err)
	}
}

func TestInsertNothing(t *testing.T) {
	// A nil pool proves no query runs.
	rejected, err := store.NewEventRepo(store.New(nil)).Insert(t.Context(), nil)
	if err != nil || len(rejected) != 0 {
		t.Errorf("Insert(nil) = %v, %v", rejected, err)
	}
}

func event(tenant, eventType string) store.NewEventParams {
	return store.NewEventParams{
		Ts:        time.Now(),
		TenantID:  tenant,
		Source:    "api",
		EventType: &eventType,
		Raw:       []byte(`{}`),
	}
}

func stored(t *testing.T, db *storetest.DB, tenant string) []gen.Event {
	t.Helper()
	rows, err := db.Owner.Query(t.Context(), `SELECT * FROM events WHERE tenant_id = $1 ORDER BY id`, tenant)
	if err != nil {
		t.Fatal(err)
	}
	events, err := pgx.CollectRows(rows, pgx.RowToStructByName[gen.Event])
	if err != nil {
		t.Fatal(err)
	}
	return events
}

func eventTypes(events []gen.Event) []string {
	var types []string
	for _, e := range events {
		types = append(types, *e.EventType)
	}
	return types
}

func codes(rejected []error) []string {
	out := make([]string, len(rejected))
	for i, err := range rejected {
		if err != nil {
			out[i] = errors.CodeOf(err)
		}
	}
	return out
}

// incompressible returns n characters that Postgres cannot compress below
// its index entry limit, as it would a repeated character.
func incompressible(n int) string {
	b := make([]byte, (n+1)/2)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)[:n]
}

func sameJSON(a, b []byte) bool {
	var x, y any
	return json.Unmarshal(a, &x) == nil && json.Unmarshal(b, &y) == nil && reflect.DeepEqual(x, y)
}

func dump(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func addr(s string) netip.Addr { return netip.MustParseAddr(s) }

func glob(t *testing.T, pattern string) []string {
	t.Helper()
	paths, err := filepath.Glob(pattern)
	if err != nil || len(paths) == 0 {
		t.Fatalf("no files match %s", pattern)
	}
	return paths
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return bytes.TrimSpace(b)
}

func TestRefusal(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want string
	}{
		{"check violation", &pgconn.PgError{Code: "23514"}, "unstorable_event"},
		{"bad encoding", &pgconn.PgError{Code: "22021"}, "unstorable_event"},
		{"index entry too large", &pgconn.PgError{Code: "54000"}, "unstorable_event"},
		{"tenant deleted meanwhile", &pgconn.PgError{Code: "23503"}, "unknown_tenant"},
		{"wrapped", fmt.Errorf("insert: %w", &pgconn.PgError{Code: "22P05"}), "unstorable_event"},
		{"RLS violation is a bug, not the event", &pgconn.PgError{Code: "42501"}, ""},
		{"connection lost", &pgconn.PgError{Code: "08006"}, ""},
		{"not a database error", fmt.Errorf("boom"), ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := ""
			if err := store.Refusal(tc.err, "demoA"); err != nil {
				got = errors.CodeOf(err)
			}
			if got != tc.want {
				t.Errorf("refusal = %q, want %q", got, tc.want)
			}
		})
	}
}
