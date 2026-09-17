package store_test

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mairuu/loghub/backend/internal/platform/errors"
	"github.com/mairuu/loghub/backend/internal/store"
	"github.com/mairuu/loghub/backend/internal/store/storetest"
)

// Count tests use dates of their own, as search tests do, and tag their
// events with their first tenant, so counts across tenants see only theirs
// even when the tests are run repeatedly.

func tagged(run string, set func(*store.NewEventParams)) func(*store.NewEventParams) {
	return func(e *store.NewEventParams) {
		e.Tags = []string{run}
		if set != nil {
			set(e)
		}
	}
}

func TestTop(t *testing.T) {
	db := storetest.Open(t)
	a, b := db.Tenant(t), db.Tenant(t)
	base := time.Date(2026, 1, 13, 12, 0, 0, 0, time.UTC)
	from := base.Add(-time.Hour)
	at := func(tenant string, ts time.Time, set func(*store.NewEventParams)) store.NewEventParams {
		return labeled(tenant, "", ts, tagged(a, set))
	}
	src := func(addr string) func(*store.NewEventParams) {
		return func(e *store.NewEventParams) { e.SrcIP = ip(addr) }
	}
	insert(t, db,
		at(a, base.Add(-1*time.Minute), src("10.0.0.2")),
		at(a, base.Add(-2*time.Minute), src("10.0.0.2")),
		at(a, base.Add(-3*time.Minute), src("10.0.0.10")),
		at(a, base.Add(-4*time.Minute), src("10.0.0.10")),
		at(a, base.Add(-5*time.Minute), src("2001:db8::1")),
		at(a, base.Add(-6*time.Minute), src("10.0.0.3")),
		at(a, base.Add(-7*time.Minute), src("10.0.0.3")),
		// A different source is still the same value.
		at(a, base.Add(-8*time.Minute), func(e *store.NewEventParams) { e.Source, e.SrcIP = "firewall", ip("10.0.0.3") }),
		at(a, base.Add(-9*time.Minute), func(e *store.NewEventParams) {
			e.Source, e.UserName, e.Host, e.DstIP, e.EventType = "ad", new("alice"), new("dc01"), ip("192.0.2.1"), new("LogonFailed")
		}),
		at(a, base.Add(-10*time.Minute), func(e *store.NewEventParams) {
			e.Source, e.UserName, e.EventType = "ad", new("alice"), new("LogonFailed")
		}),
		at(a, base.Add(-11*time.Minute), func(e *store.NewEventParams) {
			e.Source, e.UserName, e.EventType = "aws", new("bob"), new("CreateUser")
		}),
		// Outside the window, at each end.
		at(a, from.Add(-time.Microsecond), src("10.0.0.2")),
		at(a, base, src("10.0.0.2")),
		// Another tenant's.
		at(b, base.Add(-1*time.Minute), src("10.0.0.2")),
		at(b, base.Add(-2*time.Minute), src("10.0.0.99")),
	)

	type top = []string
	for _, tc := range []struct {
		name  string
		scope store.Scope
		p     store.TopParams
		want  top
	}{
		// Equal counts are in byte order, so 10.0.0.10 comes before 10.0.0.2.
		{"addresses", store.AdminScope, store.TopParams{Field: "src_ip"}, top{"10.0.0.3 3", "10.0.0.10 2", "10.0.0.2 2", "2001:db8::1 1"}},
		{"limited", store.AdminScope, store.TopParams{Field: "src_ip", Limit: 2}, top{"10.0.0.3 3", "10.0.0.10 2"}},
		{"destinations", store.AdminScope, store.TopParams{Field: "dst_ip"}, top{"192.0.2.1 1"}},
		{"users", store.AdminScope, store.TopParams{Field: "user"}, top{"alice 2", "bob 1"}},
		{"hosts", store.AdminScope, store.TopParams{Field: "host"}, top{"dc01 1"}},
		// The helper leaves event_type unset, so only three events have one.
		{"event types", store.AdminScope, store.TopParams{Field: "event_type"}, top{"LogonFailed 2", "CreateUser 1"}},
		{"filtered", store.AdminScope, store.TopParams{Field: "user", EventFilter: store.EventFilter{Sources: []string{"AWS"}}}, top{"bob 1"}},
		{"nothing matches", store.AdminScope, store.TopParams{Field: "user", EventFilter: store.EventFilter{Action: "deny"}}, nil},
		{"tenant's own scope", store.Scope{TenantID: a}, store.TopParams{Field: "event_type"}, top{"LogonFailed 2", "CreateUser 1"}},
		{"another tenant's scope", store.Scope{TenantID: b}, store.TopParams{Field: "src_ip", EventFilter: store.EventFilter{Tenant: a}}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := tc.p
			if tc.scope.Admin {
				p.Tenant = a
			}
			p.From, p.To = &from, &base
			got := topOf(t, db, tc.scope, p)
			if !slices.Equal(got, tc.want) {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}

	t.Run("every tenant", func(t *testing.T) {
		f := store.EventFilter{From: &from, To: &base, Tags: []string{a}}
		got := topOf(t, db, store.AdminScope, store.TopParams{Field: "src_ip", Limit: 2, EventFilter: f})
		if want := (top{"10.0.0.2 3", "10.0.0.3 3"}); !slices.Equal(got, want) {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("a viewer's own tenant, by default", func(t *testing.T) {
		// Row-level security would show b only b's events anyway, but the
		// filter is b's too.
		got := topOf(t, db, store.Scope{TenantID: b}, store.TopParams{Field: "src_ip", EventFilter: store.EventFilter{From: &from, To: &base}})
		if want := (top{"10.0.0.2 1", "10.0.0.99 1"}); !slices.Equal(got, want) {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("ten by default", func(t *testing.T) {
		c := db.Tenant(t)
		var events []store.NewEventParams
		for i := range 11 {
			events = append(events, at(c, base.Add(-time.Minute), func(e *store.NewEventParams) { e.Host = new(fmt.Sprintf("host%02d", i)) }))
		}
		insert(t, db, events...)
		got := topOf(t, db, store.AdminScope, store.TopParams{Field: "host", EventFilter: store.EventFilter{Tenant: c, From: &from, To: &base}})
		if len(got) != 10 || got[0] != "host00 1" || got[9] != "host09 1" {
			t.Errorf("got %q, want host00 to host09", got)
		}
	})

	t.Run("default window", func(t *testing.T) {
		before := time.Now()
		res, err := store.NewEventRepo(store.New(db.App)).Top(t.Context(), store.AdminScope, store.TopParams{Field: "src_ip", EventFilter: store.EventFilter{Tenant: a}})
		after := time.Now()
		if err != nil {
			t.Fatal(err)
		}
		if res.From.Before(before.Add(-24*time.Hour)) || res.From.After(after.Add(-24*time.Hour)) ||
			res.To.Before(before.Add(time.Hour)) || res.To.After(after.Add(time.Hour)) {
			t.Errorf("window %v to %v, want search's default around %v", res.From, res.To, before)
		}
	})
}

func topOf(t *testing.T, db *storetest.DB, scope store.Scope, p store.TopParams) []string {
	t.Helper()
	res, err := store.NewEventRepo(store.New(db.App)).Top(t.Context(), scope, p)
	if err != nil {
		t.Fatal(err)
	}
	if p.From != nil && (!res.From.Equal(*p.From) || !res.To.Equal(*p.To)) {
		t.Errorf("window %v to %v, want %v to %v", res.From, res.To, *p.From, *p.To)
	}
	var out []string
	for _, v := range res.Values {
		out = append(out, fmt.Sprint(v.Value, " ", v.Count))
	}
	return out
}

func TestTimeline(t *testing.T) {
	db := storetest.Open(t)
	a, b := db.Tenant(t), db.Tenant(t)
	day := time.Date(2026, 1, 14, 0, 0, 0, 0, time.UTC)
	at := func(tenant string, ts time.Time) store.NewEventParams { return labeled(tenant, "", ts, tagged(a, nil)) }
	insert(t, db,
		// Inside the first bucket, but before the window.
		at(a, day.Add(10*time.Hour+15*time.Minute)),
		at(a, day.Add(10*time.Hour+30*time.Minute)),
		at(a, day.Add(12*time.Hour-time.Microsecond)),
		at(a, day.Add(12*time.Hour)),
		at(a, day.Add(12*time.Hour+59*time.Minute)),
		// At the end of the window.
		at(a, day.Add(13*time.Hour+30*time.Minute)),
		at(b, day.Add(11*time.Hour)),
	)
	from, to := day.Add(10*time.Hour+30*time.Minute), day.Add(13*time.Hour+30*time.Minute)
	window := store.EventFilter{From: &from, To: &to, Tags: []string{a}}

	for _, tc := range []struct {
		name     string
		scope    store.Scope
		p        store.TimelineParams
		interval string
		want     []string
	}{
		{
			"hourly", store.AdminScope, store.TimelineParams{EventFilter: window, Interval: "1h"}, "1h",
			[]string{"10:00 1", "11:00 2", "12:00 2", "13:00 0"},
		},
		{
			"one tenant", store.AdminScope, store.TimelineParams{EventFilter: withTenant(window, a), Interval: "1h"}, "1h",
			[]string{"10:00 1", "11:00 1", "12:00 2", "13:00 0"},
		},
		{
			"a viewer's own tenant", store.Scope{TenantID: b}, store.TimelineParams{EventFilter: window, Interval: "3h"}, "3h",
			[]string{"09:00 1", "12:00 0"},
		},
		{
			"filtered", store.AdminScope, store.TimelineParams{EventFilter: withSources(withTenant(window, a), "aws"), Interval: "1d"}, "1d",
			[]string{"00:00 0"},
		},
		{
			// Three hours is 180 one-minute buckets.
			"picked", store.AdminScope, store.TimelineParams{EventFilter: withTenant(window, a)}, "1m",
			nil,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res, err := store.NewEventRepo(store.New(db.App)).Timeline(t.Context(), tc.scope, tc.p)
			if err != nil {
				t.Fatal(err)
			}
			if !res.From.Equal(from) || !res.To.Equal(to) || res.Interval != tc.interval {
				t.Errorf("window %v to %v by %s, want %v to %v by %s", res.From, res.To, res.Interval, from, to, tc.interval)
			}
			var got []string
			for _, bk := range res.Buckets {
				if bk.Start.Location() != time.UTC {
					t.Errorf("bucket start %v is not UTC", bk.Start)
				}
				got = append(got, fmt.Sprint(bk.Start.Format("15:04"), " ", bk.Count))
			}
			if tc.want == nil {
				// Only the non-empty buckets, and how many there are.
				var busy []string
				for _, g := range got {
					if !strings.HasSuffix(g, " 0") {
						busy = append(busy, g)
					}
				}
				if want := []string{"10:30 1", "11:59 1", "12:00 1", "12:59 1"}; len(got) != 180 || !slices.Equal(busy, want) {
					t.Errorf("%d buckets, busy %q, want 180, busy %q", len(got), busy, want)
				}
				return
			}
			if !slices.Equal(got, tc.want) {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func withTenant(f store.EventFilter, tenant string) store.EventFilter {
	f.Tenant = tenant
	return f
}

func withSources(f store.EventFilter, sources ...string) store.EventFilter {
	f.Sources = sources
	return f
}

func TestPickInterval(t *testing.T) {
	at := time.Date(2026, 1, 14, 10, 7, 30, 0, time.UTC)
	bangkok := time.FixedZone("ICT", 7*3600)
	for _, tc := range []struct {
		name     string
		from, to time.Time
		interval string
		// want is the interval picked, first the first bucket's start in
		// UTC, and buckets how many there are, or -1 for an error.
		want, first string
		buckets     int
	}{
		{"an hour", at, at.Add(time.Hour), "", "1m", "10:07", 61},
		{"exactly 200 minutes", at.Truncate(time.Minute), at.Truncate(time.Minute).Add(200 * time.Minute), "", "1m", "10:07", 200},
		// The window is 200 minutes, but overlaps 201 of them.
		{"200 minutes off the minute", at, at.Add(200 * time.Minute), "", "1m", "10:07", 201},
		{"just over 200 minutes", at, at.Add(200*time.Minute + time.Microsecond), "", "5m", "10:05", 41},
		{"a day", at, at.Add(24 * time.Hour), "", "15m", "10:00", 97},
		{"the default window", at.Add(-24 * time.Hour), at.Add(time.Hour), "", "15m", "10:00", 101},
		{"a week", at, at.Add(7 * 24 * time.Hour), "", "1h", "10:00", 169},
		{"a month", at, at.Add(30 * 24 * time.Hour), "", "6h", "06:00", 121},
		{"a year", at, at.Add(365 * 24 * time.Hour), "", "1d", "00:00", 366},
		{"days start at UTC midnight", at.In(bangkok), at.Add(24 * time.Hour).In(bangkok), "1d", "1d", "00:00", 2},
		{"a microsecond", at, at.Add(time.Microsecond), "1h", "1h", "10:00", 1},
		{"a day by the minute", at.Truncate(time.Minute), at.Truncate(time.Minute).Add(24 * time.Hour), "1m", "1m", "10:07", 1440},
		{"a day by the minute, off the minute", at, at.Add(24 * time.Hour), "1m", "1m", "10:07", 1441},
		{"the longest window", at.Add(-1440 * 24 * time.Hour), at, "", "1d", "00:00", 1441},
		{"up to the far future", at, time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC), "", "", "", -1},
		{"too many intervals", at, at.Add(24*time.Hour + time.Microsecond), "1m", "", "", -1},
		{"too long", at.Add(-1440*24*time.Hour - time.Microsecond), at, "", "", "", -1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			interval, first, n, err := store.PickInterval(tc.interval, tc.from, tc.to)
			if tc.buckets < 0 {
				if code(err) != "invalid_parameter" {
					t.Errorf("got %s, %d buckets, %v; want invalid_parameter", interval, n, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if interval != tc.want || first.Format("15:04") != tc.first || first.Location() != time.UTC || n != tc.buckets {
				t.Errorf("got %s from %v, %d buckets; want %s from %s, %d", interval, first, n, tc.want, tc.first, tc.buckets)
			}
		})
	}
}

func TestCountsRejectParameters(t *testing.T) {
	// Parameters are checked before any query, so no database is needed.
	repo := store.NewEventRepo(store.New(nil))
	later := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name string
		call func() error
		want string
	}{
		{"no field", func() error { _, err := repo.Top(t.Context(), store.AdminScope, store.TopParams{}); return err },
			"field must be one of src_ip, dst_ip, user, host, event_type"},
		{"unknown field", func() error {
			_, err := repo.Top(t.Context(), store.AdminScope, store.TopParams{Field: "user_name"})
			return err
		}, "field must be one of src_ip, dst_ip, user, host, event_type"},
		{"top limit too large", func() error {
			_, err := repo.Top(t.Context(), store.AdminScope, store.TopParams{Field: "user", Limit: 101})
			return err
		}, "limit must be an integer between 1 and 100"},
		{"negative top limit", func() error {
			_, err := repo.Top(t.Context(), store.AdminScope, store.TopParams{Field: "user", Limit: -1})
			return err
		}, "limit must be an integer between 1 and 100"},
		{"top filter", func() error {
			_, err := repo.Top(t.Context(), store.AdminScope, store.TopParams{Field: "user", EventFilter: store.EventFilter{SrcIP: "x"}})
			return err
		}, "src_ip must be an IPv4 or IPv6 address"},
		{"top window", func() error {
			_, err := repo.Top(t.Context(), store.AdminScope, store.TopParams{Field: "user", EventFilter: store.EventFilter{From: &later, To: &later}})
			return err
		}, "from must be before to"},
		{"unknown interval", func() error {
			_, err := repo.Timeline(t.Context(), store.AdminScope, store.TimelineParams{Interval: "1w"})
			return err
		}, "interval must be one of 1m, 5m, 15m, 30m, 1h, 3h, 6h, 12h, 1d"},
		{"interval in capitals", func() error {
			_, err := repo.Timeline(t.Context(), store.AdminScope, store.TimelineParams{Interval: "1H"})
			return err
		}, "interval must be one of 1m, 5m, 15m, 30m, 1h, 3h, 6h, 12h, 1d"},
		{"too many intervals", func() error {
			f := store.EventFilter{From: new(later.AddDate(0, 0, -2)), To: &later}
			_, err := repo.Timeline(t.Context(), store.AdminScope, store.TimelineParams{EventFilter: f, Interval: "1m"})
			return err
		}, "from and to are more than 1440 intervals of 1m apart; choose a longer interval or a shorter window"},
		{"too far apart", func() error {
			_, err := repo.Timeline(t.Context(), store.AdminScope, store.TimelineParams{EventFilter: store.EventFilter{From: new(later.AddDate(-5, 0, 0)), To: &later}})
			return err
		}, "from and to are more than 1440 days apart, too far for a timeline"},
		{"timeline filter", func() error {
			_, err := repo.Timeline(t.Context(), store.AdminScope, store.TimelineParams{EventFilter: store.EventFilter{SeverityMin: new(11)}})
			return err
		}, "severity_min must be between 0 and 10"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.call()
			if code(err) != "invalid_parameter" || errors.MessageOf(err) != tc.want {
				t.Errorf("err = %v, want invalid_parameter: %s", err, tc.want)
			}
		})
	}
}
