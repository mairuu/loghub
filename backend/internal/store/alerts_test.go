package store_test

import (
	"net/netip"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/mairuu/loghub/backend/internal/store"
	"github.com/mairuu/loghub/backend/internal/store/storetest"
)

func TestCreateAlertRule(t *testing.T) {
	db := storetest.Open(t)
	a, b := db.Tenant(t), db.Tenant(t)
	repo := store.NewAlertRepo(store.New(db.App))

	want := store.NewAlertRule{
		TenantID: a, Name: "failed logins", Source: new("ad"), EventType: new("LogonFailed"),
		Action: new("login"), SeverityMin: new(int16(3)), Tags: []string{"auth_failure"},
		GroupBy: "src_ip", Threshold: 5, WindowMinutes: 5, CooldownMinutes: 10,
		WebhookURL: new("https://hooks.example/loghub"),
	}
	got, err := repo.CreateRule(t.Context(), store.AdminScope, want)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID == 0 || got.CreatedAt.IsZero() {
		t.Errorf("created %+v", got)
	}
	back := store.NewAlertRule{
		TenantID: got.TenantID, Name: got.Name, Source: got.Source, EventType: got.EventType,
		Action: got.Action, SeverityMin: got.SeverityMin, Tags: got.Tags, GroupBy: got.GroupBy,
		Threshold: got.Threshold, WindowMinutes: got.WindowMinutes, CooldownMinutes: got.CooldownMinutes,
		WebhookURL: got.WebhookURL,
	}
	if !reflect.DeepEqual(back, want) {
		t.Errorf("created %s\nwant %s", dump(got), dump(want))
	}

	t.Run("unknown tenant", func(t *testing.T) {
		p := want
		p.TenantID = "no_such_tenant"
		_, err := repo.CreateRule(t.Context(), store.AdminScope, p)
		if code(err) != "unknown_tenant" {
			t.Errorf("err = %v, want unknown_tenant", err)
		}
	})

	t.Run("another tenant's scope", func(t *testing.T) {
		if _, err := repo.CreateRule(t.Context(), store.Scope{TenantID: b}, want); err == nil {
			t.Error("row-level security let tenant b create a rule for tenant a")
		}
	})

	t.Run("listed only in scope", func(t *testing.T) {
		p := want
		p.TenantID, p.Name = b, "b's rule"
		if _, err := repo.CreateRule(t.Context(), store.Scope{TenantID: b}, p); err != nil {
			t.Fatal(err)
		}
		for _, tc := range []struct {
			name   string
			scope  store.Scope
			tenant string
			want   []string
		}{
			{"viewer of a", store.Scope{TenantID: a}, "", []string{a}},
			{"viewer of a asking for b", store.Scope{TenantID: a}, b, nil},
			{"admin asking for b", store.AdminScope, b, []string{b}},
		} {
			rules, err := repo.ListRules(t.Context(), tc.scope, tc.tenant)
			if err != nil {
				t.Fatal(err)
			}
			var tenants []string
			for _, r := range rules {
				tenants = append(tenants, r.TenantID)
			}
			if !slices.Equal(tenants, tc.want) {
				t.Errorf("%s: rules of %q, want %q", tc.name, tenants, tc.want)
			}
		}
	})
}

func TestEvaluateFailedLogins(t *testing.T) {
	db := storetest.Open(t)
	a, b := db.Tenant(t), db.Tenant(t)
	repo := store.NewAlertRepo(store.New(db.App))
	end := time.Now().UTC().Truncate(time.Minute)

	rule := createRule(t, db, store.NewAlertRule{
		TenantID: a, Name: "failed logins", Tags: []string{"auth_failure"},
		GroupBy: "src_ip", Threshold: 3, WindowMinutes: 5, CooldownMinutes: 5,
	})
	failed := func(tenant, ip string, at time.Duration) store.NewEventParams {
		e := event(tenant, "LogonFailed")
		e.Ts, e.Tags = end.Add(at), []string{"auth_failure"}
		if ip != "" {
			addr := netip.MustParseAddr(ip)
			e.SrcIP = &addr
		}
		return e
	}
	insert(t, db,
		// Fires: three in the window, the first at its start.
		failed(a, "203.0.113.1", -5*time.Minute), failed(a, "203.0.113.1", -2*time.Minute), failed(a, "203.0.113.1", -time.Second),
		// Two isn't enough.
		failed(a, "203.0.113.2", -time.Minute), failed(a, "203.0.113.2", -time.Minute),
		// No address to group by.
		failed(a, "", -time.Minute), failed(a, "", -time.Minute), failed(a, "", -time.Minute),
		// Another tenant's events don't count.
		failed(a, "203.0.113.5", -time.Minute), failed(a, "203.0.113.5", -time.Minute),
		failed(b, "203.0.113.5", -time.Minute), failed(b, "203.0.113.5", -time.Minute),
	)
	// Not failed logins.
	for range 3 {
		e := failed(a, "203.0.113.4", -time.Minute)
		e.Tags = []string{"auth_success"}
		insert(t, db, e)
	}

	fired := evaluate(t, repo, rule, end)
	if len(fired) != 1 {
		t.Fatalf("fired %s, want one firing", dump(fired))
	}
	got := fired[0]
	if got.ID == 0 || got.RuleID != rule.ID || got.RuleName != "failed logins" || got.GroupBy != "src_ip" ||
		got.TenantID != a || got.GroupKey != "203.0.113.1" || got.Matched != 3 ||
		!got.WindowStart.Equal(end.Add(-5*time.Minute)) || !got.WindowEnd.Equal(end) || got.CreatedAt.IsZero() {
		t.Errorf("fired %s", dump(got))
	}

	t.Run("same window again", func(t *testing.T) {
		if fired := evaluate(t, repo, rule, end); len(fired) != 0 {
			t.Errorf("fired %s again", dump(fired))
		}
	})

	t.Run("within the cooldown", func(t *testing.T) {
		// The last two events are still in this window, and one more makes three.
		insert(t, db, failed(a, "203.0.113.1", 30*time.Second))
		if fired := evaluate(t, repo, rule, end.Add(time.Minute)); len(fired) != 0 {
			t.Errorf("fired %s within the cooldown", dump(fired))
		}
	})

	t.Run("after the cooldown", func(t *testing.T) {
		insert(t, db, failed(a, "203.0.113.1", 2*time.Minute), failed(a, "203.0.113.1", 3*time.Minute))
		fired := evaluate(t, repo, rule, end.Add(5*time.Minute))
		if len(fired) != 1 || fired[0].GroupKey != "203.0.113.1" || fired[0].Matched != 3 {
			t.Errorf("fired %s, want 203.0.113.1 again", dump(fired))
		}
	})

	t.Run("viewers see their own tenant's", func(t *testing.T) {
		for _, tc := range []struct {
			scope store.Scope
			want  int
		}{{store.Scope{TenantID: a}, 2}, {store.Scope{TenantID: b}, 0}} {
			alerts, err := repo.ListAlerts(t.Context(), tc.scope, "", 10)
			if err != nil {
				t.Fatal(err)
			}
			if len(alerts) != tc.want {
				t.Errorf("tenant %s sees %d alerts, want %d", tc.scope.TenantID, len(alerts), tc.want)
			}
		}
		alerts, err := repo.ListAlerts(t.Context(), store.AdminScope, a, 1)
		if err != nil {
			t.Fatal(err)
		}
		if len(alerts) != 1 || !alerts[0].WindowEnd.Equal(end.Add(5*time.Minute)) || alerts[0].RuleName != "failed logins" {
			t.Errorf("admin's newest alert for a is %s, want the second firing", dump(alerts))
		}
	})
}

func TestEvaluateWindowEdges(t *testing.T) {
	db := storetest.Open(t)
	tenant := db.Tenant(t)
	repo := store.NewAlertRepo(store.New(db.App))
	end := time.Now().UTC().Truncate(time.Minute)

	rule := createRule(t, db, store.NewAlertRule{
		TenantID: tenant, Name: "edges", GroupBy: "host", Threshold: 3, WindowMinutes: 5,
	})
	insert(t, db,
		// Three from the start up to the end.
		labeled(tenant, "1", end.Add(-5*time.Minute), onHost("inside")),
		labeled(tenant, "2", end.Add(-time.Minute), onHost("inside")),
		labeled(tenant, "3", end.Add(-time.Microsecond), onHost("inside")),
		// Two inside, and one either side.
		labeled(tenant, "4", end.Add(-5*time.Minute-time.Microsecond), onHost("edges")),
		labeled(tenant, "5", end.Add(-time.Minute), onHost("edges")),
		labeled(tenant, "6", end.Add(-time.Minute), onHost("edges")),
		labeled(tenant, "7", end, onHost("edges")),
	)

	fired := evaluate(t, repo, rule, end)
	if len(fired) != 1 || fired[0].GroupKey != "inside" {
		t.Errorf("fired %s, want inside only", dump(fired))
	}
}

func TestEvaluateWithoutCooldown(t *testing.T) {
	db := storetest.Open(t)
	tenant := db.Tenant(t)
	repo := store.NewAlertRepo(store.New(db.App))
	end := time.Now().UTC().Truncate(time.Minute)

	rule := createRule(t, db, store.NewAlertRule{
		TenantID: tenant, Name: "busy host", GroupBy: "host", Threshold: 2, WindowMinutes: 5,
	})
	insert(t, db, labeled(tenant, "1", end.Add(-2*time.Minute), onHost("fw01")), labeled(tenant, "2", end.Add(-time.Minute), onHost("fw01")))

	// Each tick asks about a different window, and both hold the events.
	for _, at := range []time.Time{end, end.Add(time.Minute)} {
		if fired := evaluate(t, repo, rule, at); len(fired) != 1 || fired[0].GroupKey != "fw01" {
			t.Errorf("at %v fired %s, want fw01", at, dump(fired))
		}
	}
	// Only the unique key stops the latest window firing twice.
	if fired := evaluate(t, repo, rule, end.Add(time.Minute)); len(fired) != 0 {
		t.Errorf("a window already recorded fired %s again", dump(fired))
	}
}

func TestEvaluateFilters(t *testing.T) {
	db := storetest.Open(t)
	tenant := db.Tenant(t)
	repo := store.NewAlertRepo(store.New(db.App))
	end := time.Now().UTC().Truncate(time.Minute)

	// Threshold 1 and grouped by user, so each event's label says whether it
	// matched.
	rule := createRule(t, db, store.NewAlertRule{
		TenantID: tenant, Name: "filters", Source: new("ad"), EventType: new("LogonFailed"),
		Action: new("login"), SeverityMin: new(int16(5)), Tags: []string{"auth_failure", "external"},
		GroupBy: "user", Threshold: 1, WindowMinutes: 10,
	})
	matching := func(user string, change func(*store.NewEventParams)) store.NewEventParams {
		e := store.NewEventParams{
			Ts: end.Add(-time.Minute), TenantID: tenant, Source: "ad", EventType: new("LogonFailed"),
			Action: new("login"), Severity: new(int16(5)), Tags: []string{"external", "auth_failure"},
			UserName: &user, Raw: []byte(`{}`),
		}
		if change != nil {
			change(&e)
		}
		return e
	}
	insert(t, db,
		matching("match", nil),
		matching("extra_tag", func(e *store.NewEventParams) { e.Tags = append(e.Tags, "vip") }),
		matching("more_severe", func(e *store.NewEventParams) { e.Severity = new(int16(10)) }),
		matching("other_source", func(e *store.NewEventParams) { e.Source = "m365" }),
		matching("other_type", func(e *store.NewEventParams) { e.EventType = new("LogonSucceeded") }),
		matching("other_action", func(e *store.NewEventParams) { e.Action = new("logout") }),
		matching("less_severe", func(e *store.NewEventParams) { e.Severity = new(int16(4)) }),
		matching("no_severity", func(e *store.NewEventParams) { e.Severity = nil }),
		matching("one_tag", func(e *store.NewEventParams) { e.Tags = []string{"auth_failure"} }),
		matching("no_tag", func(e *store.NewEventParams) { e.Tags = nil }),
	)

	var got []string
	for _, f := range evaluate(t, repo, rule, end) {
		got = append(got, f.GroupKey)
	}
	if want := []string{"extra_tag", "match", "more_severe"}; !slices.Equal(got, want) {
		t.Errorf("fired for %q, want %q", got, want)
	}
}

func TestEvaluateGroupsByAddressText(t *testing.T) {
	db := storetest.Open(t)
	tenant := db.Tenant(t)
	repo := store.NewAlertRepo(store.New(db.App))
	end := time.Now().UTC().Truncate(time.Minute)

	rule := createRule(t, db, store.NewAlertRule{
		TenantID: tenant, Name: "by destination", GroupBy: "dst_ip", Threshold: 1, WindowMinutes: 5,
	})
	for _, ip := range []string{"198.51.100.9", "2001:db8::1"} {
		insert(t, db, labeled(tenant, ip, end.Add(-time.Minute), func(e *store.NewEventParams) {
			addr := netip.MustParseAddr(ip)
			e.DstIP = &addr
		}))
	}

	var got []string
	for _, f := range evaluate(t, repo, rule, end) {
		got = append(got, f.GroupKey)
	}
	// No /32 or /128 suffix.
	if want := []string{"198.51.100.9", "2001:db8::1"}; !slices.Equal(got, want) {
		t.Errorf("fired for %q, want %q", got, want)
	}
}

func createRule(t *testing.T, db *storetest.DB, p store.NewAlertRule) store.AlertRule {
	t.Helper()
	rule, err := store.NewAlertRepo(store.New(db.App)).CreateRule(t.Context(), store.Scope{TenantID: p.TenantID}, p)
	if err != nil {
		t.Fatal(err)
	}
	return rule
}

func evaluate(t *testing.T, repo *store.AlertRepo, rule store.AlertRule, end time.Time) []store.Alert {
	t.Helper()
	fired, err := repo.Evaluate(t.Context(), rule, end)
	if err != nil {
		t.Fatal(err)
	}
	return fired
}

func onHost(host string) func(*store.NewEventParams) {
	return func(e *store.NewEventParams) { e.Host = &host }
}
