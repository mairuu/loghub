package authz_test

import (
	"strings"
	"testing"

	. "github.com/mairuu/loghub/backend/internal/authz"
	"github.com/mairuu/loghub/backend/internal/platform/errors"
)

var (
	anonymous = Caller{}
	admin     = Caller{Role: Admin, UserID: 1}
	viewerA   = Caller{Role: Viewer, Tenant: "a", UserID: 2}
	collector = Caller{Role: Collector}
	// A viewer token always carries a tenant; one that doesn't gets nothing.
	viewerNoTenant = Caller{Role: Viewer, UserID: 3}
)

func enforcer(t *testing.T, sets ...Set) *Enforcer {
	t.Helper()
	e, err := New(sets...)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

// The events slice from ADR 0009, and sign-in for everyone.
func adrPolicies(t *testing.T) *Enforcer {
	return enforcer(t,
		NewSet("events",
			Grant(Admin).As(AnyTenant).On(Events).Can(Read, Create),
			Grant(Viewer).As(OwnTenant).On(Events).Can(Read),
			Grant(Collector).As(AnyTenant).On(Events).Can(Create),
		),
		NewSet("sessions",
			Grant(Anonymous).As(AnyTenant).On(Sessions).Can(Create),
		),
	)
}

func TestCan(t *testing.T) {
	e := adrPolicies(t)
	for _, tc := range []struct {
		name     string
		caller   Caller
		resource Resource
		action   Action
		tenant   string
		want     bool
	}{
		{"admin reads any tenant", admin, Events, Read, "a", true},
		{"admin writes any tenant", admin, Events, Create, "b", true},
		{"viewer reads own tenant", viewerA, Events, Read, "a", true},
		{"viewer reads another tenant", viewerA, Events, Read, "b", false},
		{"viewer reads no tenant", viewerA, Events, Read, "", false},
		{"viewer writes own tenant", viewerA, Events, Create, "a", false},
		{"viewer without a tenant", viewerNoTenant, Events, Read, "", false},
		{"collector writes any tenant", collector, Events, Create, "b", true},
		{"collector reads", collector, Events, Read, "a", false},
		{"anonymous reads", anonymous, Events, Read, "a", false},
		{"anonymous writes", anonymous, Events, Create, "a", false},
		{"anonymous signs in", anonymous, Sessions, Create, "", true},
		{"anonymous grant is not for the signed in", admin, Sessions, Create, "", false},
		{"granted action on an ungranted resource", admin, Health, Read, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := e.Can(tc.caller, tc.resource, tc.action, tc.tenant); got != tc.want {
				t.Errorf("Can(%+v, %s, %s, %q) = %v, want %v", tc.caller, tc.resource, tc.action, tc.tenant, got, tc.want)
			}
		})
	}
}

func TestWildcards(t *testing.T) {
	e := enforcer(t,
		NewSet("health", Grant(AnyRole).As(AnyTenant).On(Health).Can(Read)),
		NewSet("events",
			Grant(Viewer).As(OwnTenant).On(Events).Can(AnyAction),
			Grant(Collector).As(AnyTenant).On(Events).Can(Read),
		),
	)
	for _, tc := range []struct {
		name     string
		caller   Caller
		resource Resource
		action   Action
		tenant   string
		want     bool
	}{
		{"any role includes admin", admin, Health, Read, "", true},
		{"any role includes collector", collector, Health, Read, "", true},
		{"any role excludes anonymous", anonymous, Health, Read, "", false},
		{"any role is only for its action", admin, Health, Create, "", false},
		{"any action", viewerA, Events, Create, "a", true},
		{"any action stays in scope", viewerA, Events, Create, "b", false},
		{"any tenant includes one with no tenant of its own", collector, Events, Read, "a", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := e.Can(tc.caller, tc.resource, tc.action, tc.tenant); got != tc.want {
				t.Errorf("Can(%+v, %s, %s, %q) = %v, want %v", tc.caller, tc.resource, tc.action, tc.tenant, got, tc.want)
			}
		})
	}

	// Any tenant covers the caller's own.
	e = enforcer(t, NewSet("events", Grant(Viewer).As(AnyTenant).On(Events).Can(Read)))
	if !e.Can(viewerA, Events, Read, "a") {
		t.Error("an any-tenant grant does not cover the caller's own tenant")
	}
}

func TestScopes(t *testing.T) {
	e := adrPolicies(t)
	type want struct {
		all  bool
		only string
	}
	for _, tc := range []struct {
		name   string
		caller Caller
		action Action
		want   want
	}{
		{"admin reads every tenant", admin, Read, want{all: true}},
		{"viewer reads their own", viewerA, Read, want{only: "a"}},
		{"viewer without a tenant reads none", viewerNoTenant, Read, want{}},
		{"collector reads none", collector, Read, want{}},
		{"collector writes every tenant", collector, Create, want{all: true}},
		{"viewer writes none", viewerA, Create, want{}},
		{"anonymous reads none", anonymous, Read, want{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := e.Scopes(tc.caller, Events, tc.action)
			only, isOne := got.Only()
			if got.All() != tc.want.all || only != tc.want.only || isOne != (tc.want.only != "") {
				t.Errorf("Scopes = all %v, only %q %v; want %+v", got.All(), only, isOne, tc.want)
			}
			if empty := !tc.want.all && tc.want.only == ""; got.Empty() != empty {
				t.Errorf("Empty() = %v, want %v", got.Empty(), empty)
			}
			for _, tenant := range []string{"a", "b", ""} {
				want := tc.want.all || (tenant != "" && tenant == tc.want.only)
				if got.Contains(tenant) != want {
					t.Errorf("Contains(%q) = %v, want %v", tenant, !want, want)
				}
			}
		})
	}
}

func TestDeny(t *testing.T) {
	e := adrPolicies(t)
	for _, tc := range []struct {
		name   string
		caller Caller
		kind   errors.Kind
		code   string
	}{
		{"anonymous must authenticate", anonymous, errors.KindUnauthenticated, "authentication_required"},
		{"viewer is forbidden", viewerA, errors.KindForbidden, "permission_denied"},
		{"collector is forbidden", collector, errors.KindForbidden, "permission_denied"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := e.Authorize(tc.caller, Events, Read, "b")
			if errors.KindOf(err) != tc.kind || errors.CodeOf(err) != tc.code {
				t.Errorf("Authorize = %v, want %s %s", err, tc.kind, tc.code)
			}
		})
	}
	if err := e.Authorize(viewerA, Events, Read, "a"); err != nil {
		t.Errorf("Authorize = %v for an allowed request", err)
	}
}

func TestNewRejectsBadPolicies(t *testing.T) {
	for _, tc := range []struct {
		name string
		sets []Set
		want []string
	}{
		{"empty set", []Set{NewSet("alerts")}, []string{`policy set "alerts" is empty`}},
		{
			"no actions",
			[]Set{NewSet("events", Grant(Admin).As(AnyTenant).On(Events).Can())},
			[]string{`policy set "events": Grant(admin).As(any tenant).On(events).Can([]): no actions`},
		},
		{
			"no resource",
			[]Set{NewSet("events", Grant(Admin).As(AnyTenant).On(0).Can(Read))},
			[]string{"unknown resource"},
		},
		{
			"no scope",
			[]Set{NewSet("events", Grant(Admin).As(0).On(Events).Can(Read))},
			[]string{"unknown scope"},
		},
		{
			"unknown role",
			[]Set{NewSet("events", Grant(AnyRole+1).As(AnyTenant).On(Events).Can(Read))},
			[]string{"unknown role"},
		},
		{
			"no action",
			[]Set{NewSet("events", Grant(Admin).As(AnyTenant).On(Events).Can(Read, 0))},
			[]string{"unknown action"},
		},
		{
			"anonymous has no tenant",
			[]Set{NewSet("events", Grant(Anonymous).As(OwnTenant).On(Events).Can(Read))},
			[]string{"an anonymous caller has no tenant"},
		},
		{
			"every problem is reported",
			[]Set{NewSet("a"), NewSet("b"), NewSet("c", Grant(Admin).As(AnyTenant).On(Events).Can(Read))},
			[]string{`policy set "a" is empty`, `policy set "b" is empty`},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, err := New(tc.sets...)
			if err == nil || e != nil {
				t.Fatalf("New = %v, %v; want an error", e, err)
			}
			for _, want := range tc.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not mention %q", err, want)
				}
			}
		})
	}
}

// A caller whose role no grant names, such as a zero value or a stray
// number, is denied everything and treated as anonymous.
func TestUnknownCallerIsDenied(t *testing.T) {
	e := enforcer(t, NewSet("events", Grant(AnyRole).As(AnyTenant).On(Events).Can(AnyAction)))
	for _, c := range []Caller{{}, {Role: AnyRole + 1}} {
		if e.Can(c, Events, Read, "") || c.Authenticated() {
			t.Errorf("%+v is allowed", c)
		}
		if err := Deny(c); errors.KindOf(err) != errors.KindUnauthenticated {
			t.Errorf("Deny(%+v) = %v, want unauthenticated", c, err)
		}
	}
}

func TestUserRole(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want Role
		ok   bool
	}{
		{"admin", Admin, true},
		{"viewer", Viewer, true},
		{"collector", 0, false},
		{"anonymous", 0, false},
		{"Admin", 0, false},
		{"", 0, false},
	} {
		if got, ok := UserRole(tc.in); got != tc.want || ok != tc.ok {
			t.Errorf("UserRole(%q) = %v, %v; want %v, %v", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}
