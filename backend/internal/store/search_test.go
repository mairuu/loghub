package store_test

import (
	"cmp"
	"context"
	"fmt"
	"net/netip"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/mairuu/loghub/backend/internal/platform/errors"
	"github.com/mairuu/loghub/backend/internal/store"
	"github.com/mairuu/loghub/backend/internal/store/storetest"
)

// Search tests use fixed dates of their own, away from the events other tests
// insert at the current time, so a search across tenants sees only its own.

func TestSearchFilters(t *testing.T) {
	db := storetest.Open(t)
	a, b := db.Tenant(t), db.Tenant(t)
	base := time.Date(2026, 1, 10, 12, 0, 0, 0, time.UTC)
	insert(t, db,
		labeled(a, "e1", base.Add(-10*time.Minute), func(e *store.NewEventParams) {
			e.Source, e.EventType, e.Action, e.Severity = "firewall", new("traffic"), new("deny"), new(int16(2))
			e.SrcIP, e.Host, e.Tags = ip("10.0.0.1"), new("fw01"), []string{"x"}
			e.Raw = []byte(`{"message": "Blocked DNS 5353"}`)
		}),
		labeled(a, "e2", base.Add(-20*time.Minute), func(e *store.NewEventParams) {
			e.Source, e.EventType, e.Action, e.Severity = "firewall", new("traffic"), new("allow"), new(int16(2))
			e.SrcIP, e.Host = ip("10.0.0.2"), new("fw01")
			e.Raw = []byte(`{"message": "allowed"}`)
		}),
		labeled(a, "e3", base.Add(-30*time.Minute), func(e *store.NewEventParams) {
			e.Source, e.EventType, e.Action, e.Severity = "ad", new("LogonFailed"), new("login"), new(int16(5))
			e.SrcIP, e.UserName, e.Tags = ip("10.0.0.1"), new("alice"), []string{"auth_failure"}
			e.Raw = []byte(`{"EventID": 4625, "detail": {"list": ["Say \"hi\" to C:\\temp", "50%_off"]}}`)
		}),
		labeled(a, "e4", base.Add(-40*time.Minute), func(e *store.NewEventParams) {
			e.Source, e.EventType, e.Action = "aws", new("CreateUser"), new("create")
			e.UserName = new("admin")
			e.Raw = []byte(`{"eventName": "CreateUser"}`)
		}),
		labeled(a, "e5", base.Add(-2*time.Hour), func(e *store.NewEventParams) {
			e.EventType, e.Action, e.Tags = new("app_login_failed"), new("login"), []string{"auth_failure", "x"}
		}),
		labeled(b, "b1", base.Add(-10*time.Minute), func(e *store.NewEventParams) {
			e.Source, e.Action, e.SrcIP = "firewall", new("deny"), ip("10.0.0.1")
		}),
	)

	hourBefore := base.Add(-time.Hour)
	inA := func(f store.EventFilter) store.EventFilter {
		f.Tenant = a
		if f.From == nil {
			f.From = &hourBefore
		}
		if f.To == nil {
			f.To = &base
		}
		return f
	}
	for _, tc := range []struct {
		name   string
		filter store.EventFilter
		order  store.Order
		want   []string
	}{
		{"tenant and window", inA(store.EventFilter{}), "", []string{"e1", "e2", "e3", "e4"}},
		{"oldest first", inA(store.EventFilter{}), store.OrderAsc, []string{"e4", "e3", "e2", "e1"}},
		{"any of several sources", inA(store.EventFilter{Sources: []string{"aws", "ad"}}), "", []string{"e3", "e4"}},
		{"source in capitals", inA(store.EventFilter{Sources: []string{"FIREWALL"}}), "", []string{"e1", "e2"}},
		{"event type", inA(store.EventFilter{EventType: "traffic"}), "", []string{"e1", "e2"}},
		{"action in capitals", inA(store.EventFilter{Action: " DENY "}), "", []string{"e1"}},
		{"severity at least", inA(store.EventFilter{SeverityMin: new(3)}), "", []string{"e3"}},
		{"severity at most skips unset", inA(store.EventFilter{SeverityMax: new(2)}), "", []string{"e1", "e2"}},
		{"severity exactly", inA(store.EventFilter{SeverityMin: new(5), SeverityMax: new(5)}), "", []string{"e3"}},
		{"source address", inA(store.EventFilter{SrcIP: "10.0.0.1"}), "", []string{"e1", "e3"}},
		{"IPv4-mapped source address", inA(store.EventFilter{SrcIP: "::ffff:10.0.0.1"}), "", []string{"e1", "e3"}},
		{"user", inA(store.EventFilter{User: "alice"}), "", []string{"e3"}},
		{"host", inA(store.EventFilter{Host: "fw01"}), "", []string{"e1", "e2"}},
		{"tag", inA(store.EventFilter{Tags: []string{"x"}}), "", []string{"e1"}},
		{"every tag required", inA(store.EventFilter{Tags: []string{"x", "auth_failure"}}), "", nil},
		{"every tag, wider window", inA(store.EventFilter{Tags: []string{"auth_failure", "x"}, From: new(base.Add(-3 * time.Hour))}), "", []string{"e5"}},
		{"several filters together", inA(store.EventFilter{Sources: []string{"firewall"}, Action: "deny", SrcIP: "10.0.0.1"}), "", []string{"e1"}},
		{"from is inclusive", inA(store.EventFilter{From: new(base.Add(-10 * time.Minute))}), "", []string{"e1"}},
		{"to is exclusive", inA(store.EventFilter{To: new(base.Add(-10 * time.Minute))}), "", []string{"e2", "e3", "e4"}},

		{"text ignores case and spans words", inA(store.EventFilter{Query: "blocked dns"}), "", []string{"e1"}},
		{"text inside a string", inA(store.EventFilter{Query: "535"}), "", []string{"e1"}},
		{"text matches a number", inA(store.EventFilter{Query: "4625"}), "", []string{"e3"}},
		{"text matches nested values", inA(store.EventFilter{Query: "_OFF"}), "", []string{"e3"}},
		{"text does not match keys", inA(store.EventFilter{Query: "EventID"}), "", nil},
		{"text with quotes", inA(store.EventFilter{Query: `say "hi"`}), "", []string{"e3"}},
		{"text with a backslash", inA(store.EventFilter{Query: `C:\temp`}), "", []string{"e3"}},
		{"percent is literal", inA(store.EventFilter{Query: "%"}), "", []string{"e3"}},
		{"underscore is literal", inA(store.EventFilter{Query: "_"}), "", []string{"e3"}},
		{"no text match", inA(store.EventFilter{Query: "nothing like this"}), "", nil},

		{"every tenant", store.EventFilter{From: new(base.Add(-15 * time.Minute)), To: &base}, "", []string{"b1", "e1"}},
		{"one other tenant", store.EventFilter{Tenant: b, From: &hourBefore, To: &base}, "", []string{"b1"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := searchAll(t, db, store.AdminScope, store.SearchParams{EventFilter: tc.filter, Order: tc.order})
			if !slices.Equal(got, tc.want) {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}

	t.Run("row-level security", func(t *testing.T) {
		window := store.EventFilter{From: &hourBefore, To: &base}
		for _, tc := range []struct {
			name   string
			scope  store.Scope
			tenant string
			want   []string
		}{
			{"a viewer sees their tenant", store.Scope{TenantID: a}, "", []string{"e1", "e2", "e3", "e4"}},
			{"a viewer can't widen to another tenant", store.Scope{TenantID: a}, b, nil},
			{"no scope sees nothing", store.Scope{}, "", nil},
		} {
			t.Run(tc.name, func(t *testing.T) {
				f := window
				f.Tenant = tc.tenant
				got := searchAll(t, db, tc.scope, store.SearchParams{EventFilter: f})
				if !slices.Equal(got, tc.want) {
					t.Errorf("got %q, want %q", got, tc.want)
				}
			})
		}
	})
}

func TestSearchPaging(t *testing.T) {
	db := storetest.Open(t)
	tenant := db.Tenant(t)
	base := time.Date(2026, 1, 11, 12, 0, 0, 0, time.UTC)
	// e2, e3 and e4 share a time, so pages must also order by id.
	insert(t, db,
		labeled(tenant, "e1", base.Add(-1*time.Minute), nil),
		labeled(tenant, "e2", base.Add(-2*time.Minute), nil),
		labeled(tenant, "e3", base.Add(-2*time.Minute), nil),
		labeled(tenant, "e4", base.Add(-2*time.Minute), nil),
		labeled(tenant, "e5", base.Add(-3*time.Minute), nil),
		labeled(tenant, "e6", base.Add(-4*time.Minute), nil),
		labeled(tenant, "e7", base.Add(-5*time.Minute), nil),
	)
	window := store.EventFilter{Tenant: tenant, From: new(base.Add(-time.Hour)), To: &base}
	desc := []string{"e1", "e4", "e3", "e2", "e5", "e6", "e7"}
	asc := slices.Clone(desc)
	slices.Reverse(asc)

	for _, tc := range []struct {
		order     store.Order
		limit     int
		want      []string
		wantPages int
	}{
		{store.OrderDesc, 2, desc, 4},
		{store.OrderAsc, 2, asc, 4},
		{store.OrderDesc, 3, desc, 3},
		{store.OrderDesc, 6, desc, 2},
		{store.OrderDesc, 7, desc, 1},
		{store.OrderAsc, 1000, asc, 1},
	} {
		t.Run(fmt.Sprintf("%s by %d", tc.order, tc.limit), func(t *testing.T) {
			got, pages := pageThrough(t, db, store.AdminScope, store.SearchParams{EventFilter: window, Order: tc.order, Limit: tc.limit})
			if !slices.Equal(got, tc.want) || pages != tc.wantPages {
				t.Errorf("got %q in %d pages, want %q in %d", got, pages, tc.want, tc.wantPages)
			}
		})
	}

	t.Run("events inserted between pages don't shift them", func(t *testing.T) {
		repo := store.NewEventRepo(store.New(db.App))
		p := store.SearchParams{EventFilter: window, Limit: 3}
		first, err := repo.Search(t.Context(), store.AdminScope, p)
		if err != nil {
			t.Fatal(err)
		}
		insert(t, db, labeled(tenant, "new", base.Add(-30*time.Second), nil))
		p.Cursor = first.NextCursor
		second, err := repo.Search(t.Context(), store.AdminScope, p)
		if err != nil {
			t.Fatal(err)
		}
		if got := labels(second.Events); !slices.Equal(got, []string{"e2", "e5", "e6"}) {
			t.Errorf("second page = %q", got)
		}
	})
}

func TestSearchDefaultWindow(t *testing.T) {
	db := storetest.Open(t)
	tenant := db.Tenant(t)
	now := time.Now()
	insert(t, db,
		labeled(tenant, "fast clock", now.Add(30*time.Minute), nil),
		labeled(tenant, "recent", now.Add(-time.Hour), nil),
		labeled(tenant, "old", now.Add(-25*time.Hour), nil),
		labeled(tenant, "too far ahead", now.Add(2*time.Hour), nil),
	)
	f := store.EventFilter{Tenant: tenant}

	got := searchAll(t, db, store.AdminScope, store.SearchParams{EventFilter: f})
	if want := []string{"fast clock", "recent"}; !slices.Equal(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}

	// Later pages keep the first page's window, even with from and to
	// left out, so paging never drifts.
	got, pages := pageThrough(t, db, store.AdminScope, store.SearchParams{EventFilter: f, Limit: 1})
	if want := []string{"fast clock", "recent"}; !slices.Equal(got, want) || pages != 2 {
		t.Errorf("paged %q in %d pages, want %q in 2", got, pages, want)
	}
}

func TestSearchCursorMustMatch(t *testing.T) {
	db := storetest.Open(t)
	tenant := db.Tenant(t)
	base := time.Date(2026, 1, 12, 12, 0, 0, 0, time.UTC)
	insert(t, db,
		labeled(tenant, "e1", base.Add(-1*time.Minute), nil),
		labeled(tenant, "e2", base.Add(-2*time.Minute), nil),
		labeled(tenant, "e3", base.Add(-3*time.Minute), nil),
	)
	repo := store.NewEventRepo(store.New(db.App))
	from := base.Add(-time.Hour)
	first := store.SearchParams{EventFilter: store.EventFilter{Tenant: tenant, From: &from, To: &base}, Limit: 1}
	page, err := repo.Search(t.Context(), store.AdminScope, first)
	if err != nil || page.NextCursor == "" {
		t.Fatalf("first page: %v, cursor %q", err, page.NextCursor)
	}
	cursor := page.NextCursor

	for _, tc := range []struct {
		name   string
		change func(*store.SearchParams)
		want   string
	}{
		{"same parameters", func(*store.SearchParams) {}, ""},
		{"window left out", func(p *store.SearchParams) { p.From, p.To = nil, nil }, ""},
		{"same filter spelled differently", func(p *store.SearchParams) { p.Tenant = " " + tenant; p.Tags = []string{} }, ""},
		{"different filter", func(p *store.SearchParams) { p.Action = "deny" }, "invalid_cursor"},
		{"different order", func(p *store.SearchParams) { p.Order = store.OrderAsc }, "invalid_cursor"},
		{"different from", func(p *store.SearchParams) { p.From = new(from.Add(time.Second)) }, "invalid_cursor"},
		{"different to", func(p *store.SearchParams) { p.To = new(base.Add(time.Second)) }, "invalid_cursor"},
		{"different page size", func(p *store.SearchParams) { p.Limit = 5 }, ""},
		{"not a cursor", func(p *store.SearchParams) { p.Cursor = "not-a-cursor" }, "invalid_cursor"},
		{"unknown version", func(p *store.SearchParams) { p.Cursor = "eyJ2IjoyfQ" }, "invalid_cursor"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := first
			p.Cursor = cursor
			tc.change(&p)
			_, err := repo.Search(t.Context(), store.AdminScope, p)
			if got := code(err); got != tc.want {
				t.Errorf("code = %q (%v), want %q", got, err, tc.want)
			}
		})
	}
}

func TestSearchRejectsParameters(t *testing.T) {
	// Parameters are checked before any query, so no database is needed.
	repo := store.NewEventRepo(store.New(nil))
	later := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	earlier := later.Add(-time.Second)

	for _, tc := range []struct {
		name string
		p    store.SearchParams
		want string
	}{
		{"limit too large", store.SearchParams{Limit: 1001}, "limit must be an integer between 1 and 1000"},
		{"negative limit", store.SearchParams{Limit: -1}, "limit must be an integer between 1 and 1000"},
		{"unknown order", store.SearchParams{Order: "sideways"}, "order must be asc or desc"},
		{"severity below range", params(store.EventFilter{SeverityMin: new(-1)}), "severity_min must be between 0 and 10"},
		{"severity above range", params(store.EventFilter{SeverityMax: new(11)}), "severity_max must be between 0 and 10"},
		{"severity range inverted", params(store.EventFilter{SeverityMin: new(6), SeverityMax: new(5)}), "severity_min must not be greater than severity_max"},
		{"empty window", params(store.EventFilter{From: &later, To: &later}), "from must be before to"},
		{"inverted window", params(store.EventFilter{From: &later, To: &earlier}), "from must be before to"},
		{"from after the default to", params(store.EventFilter{From: new(time.Now().Add(2 * time.Hour))}), "from must be before to"},
		{"bad address", params(store.EventFilter{SrcIP: "10.0.0"}), "src_ip must be an IPv4 or IPv6 address"},
		{"NUL in text", params(store.EventFilter{Query: "a" + nulByte}), "q must be UTF-8 text without NUL characters"},
		{"NUL in a tag", params(store.EventFilter{Tags: []string{"a" + nulByte}}), "tag must be UTF-8 text without NUL characters"},
		{"invalid UTF-8", params(store.EventFilter{User: "\xff"}), "user must be UTF-8 text without NUL characters"},
		{"invalid UTF-8 source", params(store.EventFilter{Sources: []string{"\xfe"}}), "source must be UTF-8 text without NUL characters"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := repo.Search(t.Context(), store.AdminScope, tc.p)
			if code(err) != "invalid_parameter" || errors.MessageOf(err) != tc.want {
				t.Errorf("err = %v, want invalid_parameter: %s", err, tc.want)
			}
			if errors.KindOf(err) != errors.KindMalformed {
				t.Errorf("kind = %v, want malformed", errors.KindOf(err))
			}
		})
	}
}

func TestSearchError(t *testing.T) {
	timeout := &pgconn.PgError{Code: "57014"}
	if got := code(store.SearchError(t.Context(), timeout)); got != "search_timeout" {
		t.Errorf("statement timeout: code = %s", got)
	}

	// The same code when the request itself was cancelled is not a timeout.
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if got := code(store.SearchError(ctx, timeout)); got != "internal_error" {
		t.Errorf("cancelled request: code = %s", got)
	}
}

const nulByte = "\x00"

func params(f store.EventFilter) store.SearchParams { return store.SearchParams{EventFilter: f} }

// labeled is an api event with its label in rule_id, for tests to name it.
func labeled(tenant, label string, ts time.Time, set func(*store.NewEventParams)) store.NewEventParams {
	e := event(tenant, "")
	e.EventType, e.Ts, e.RuleID = nil, ts, &label
	if set != nil {
		set(&e)
	}
	return e
}

func insert(t *testing.T, db *storetest.DB, events ...store.NewEventParams) {
	t.Helper()
	rejected, err := store.NewEventRepo(store.New(db.App)).Insert(t.Context(), events)
	if err != nil {
		t.Fatal(err)
	}
	for i, err := range rejected {
		if err != nil {
			t.Fatalf("event %d rejected: %v", i, err)
		}
	}
}

// searchAll returns the labels of every match, failing if they don't fit
// on one page.
func searchAll(t *testing.T, db *storetest.DB, scope store.Scope, p store.SearchParams) []string {
	t.Helper()
	page, err := store.NewEventRepo(store.New(db.App)).Search(t.Context(), scope, p)
	if err != nil {
		t.Fatal(err)
	}
	if page.NextCursor != "" {
		t.Fatalf("more than one page")
	}
	return labels(page.Events)
}

func pageThrough(t *testing.T, db *storetest.DB, scope store.Scope, p store.SearchParams) (got []string, pages int) {
	t.Helper()
	repo := store.NewEventRepo(store.New(db.App))
	limit := cmp.Or(p.Limit, 50)
	for pages < 100 {
		page, err := repo.Search(t.Context(), scope, p)
		if err != nil {
			t.Fatalf("page %d: %v", pages+1, err)
		}
		pages++
		if n := len(page.Events); n == 0 || n > limit {
			t.Fatalf("page %d has %d events", pages, n)
		}
		got = append(got, labels(page.Events)...)
		if page.NextCursor == "" {
			return got, pages
		}
		p.Cursor = page.NextCursor
		// Reuse the window the cursor carries, as a client that omits it would.
		p.From, p.To = nil, nil
	}
	t.Fatal("paging does not end")
	return nil, 0
}

func labels(events []store.Event) []string {
	var out []string
	for _, e := range events {
		out = append(out, *e.RuleID)
	}
	return out
}

func code(err error) string {
	if err == nil {
		return ""
	}
	return errors.CodeOf(err)
}

func ip(s string) *netip.Addr {
	a := addr(s)
	return &a
}
