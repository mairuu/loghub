// Package authz decides what a caller may do (ADR 0009). Who may do what is a
// set of policies compiled into the binary, one set per feature, and an
// Enforcer answers against them. Handlers ask; they do not decide.
//
// A policy grants a role an action on a resource under a scope:
//
//	Grant(Viewer).As(OwnTenant).On(Events).Can(Read)
//
// Anything not granted is denied.
package authz

import (
	"fmt"

	"github.com/mairuu/loghub/backend/internal/platform/errors"
)

// Role is what a caller acts as. The zero value is Anonymous.
type Role uint8

const (
	// Anonymous is a caller that sent no credential. Grants to AnyRole never
	// reach it, so anonymous access is always granted by name.
	Anonymous Role = iota
	Admin
	Viewer
	// Collector is the ingest key. No user row can hold it.
	Collector
	// AnyRole matches every authenticated caller. It stays last: roles
	// between Anonymous and it are the authenticated ones.
	AnyRole
)

var roleNames = [...]string{"anonymous", "admin", "viewer", "collector", "*"}

func (r Role) String() string { return name(roleNames[:], r) }

// UserRole returns the role stored in users.role and in a token's claims.
// Only admin and viewer are user roles.
func UserRole(s string) (Role, bool) {
	switch s {
	case "admin":
		return Admin, true
	case "viewer":
		return Viewer, true
	}
	return 0, false
}

// Resource is what a policy is about. There is no wildcard, so no grant
// reaches a resource added after it was written.
type Resource uint8

const (
	_ Resource = iota
	Health
	// Sessions are sign-ins.
	Sessions
	Events
	AlertRules
	// Alerts are alert rules' firings.
	Alerts
	resourceEnd
)

var resourceNames = [...]string{"", "health", "sessions", "events", "alert_rules", "alerts"}

func (r Resource) String() string { return name(resourceNames[:], r) }

type Action uint8

const (
	_ Action = iota
	Read
	Create
	// AnyAction matches every action on the resource.
	AnyAction
)

var actionNames = [...]string{"", "read", "create", "*"}

func (a Action) String() string { return name(actionNames[:], a) }

// Scope is which tenants a grant covers.
type Scope uint8

const (
	_ Scope = iota
	// OwnTenant is the caller's own tenant, and nothing for a caller with
	// none.
	OwnTenant
	// AnyTenant is every tenant, the caller's own included. It is also the
	// scope of resources that belong to no tenant.
	AnyTenant
)

var scopeNames = [...]string{"", "own tenant", "any tenant"}

func (s Scope) String() string { return name(scopeNames[:], s) }

func name[T ~uint8](names []string, v T) string {
	if int(v) < len(names) && names[v] != "" {
		return names[v]
	}
	return fmt.Sprintf("invalid(%d)", uint8(v))
}

// Caller is who a request comes from.
type Caller struct {
	Role Role
	// Tenant is a viewer's tenant, and empty for every other role.
	Tenant string
	// UserID is the signed-in user, and zero for a caller that isn't one.
	UserID int64
}

// Authenticated reports whether the caller proved who it is.
func (c Caller) Authenticated() bool { return c.Role > Anonymous && c.Role < AnyRole }

// Policy grants one role some actions on one resource under one scope.
// Build it with Grant.
type Policy struct {
	role     Role
	scope    Scope
	resource Resource
	actions  []Action
}

// Grant starts a policy: Grant(role).As(scope).On(resource).Can(actions...).
func Grant(r Role) RoleGrant { return RoleGrant{role: r} }

type RoleGrant struct{ role Role }

func (g RoleGrant) As(s Scope) ScopedGrant { return ScopedGrant{role: g.role, scope: s} }

type ScopedGrant struct {
	role  Role
	scope Scope
}

func (g ScopedGrant) On(r Resource) ResourceGrant {
	return ResourceGrant{role: g.role, scope: g.scope, resource: r}
}

type ResourceGrant struct {
	role     Role
	scope    Scope
	resource Resource
}

func (g ResourceGrant) Can(actions ...Action) Policy {
	return Policy{role: g.role, scope: g.scope, resource: g.resource, actions: actions}
}

func (p Policy) String() string {
	return fmt.Sprintf("Grant(%s).As(%s).On(%s).Can(%v)", p.role, p.scope, p.resource, p.actions)
}

// Set is one feature's policies.
type Set struct {
	name     string
	policies []Policy
}

func NewSet(name string, policies ...Policy) Set { return Set{name: name, policies: policies} }

type grant struct {
	role     Role
	resource Resource
	scope    Scope
	action   Action
}

// Enforcer answers what a caller may do under the policies it was built
// from.
type Enforcer struct {
	grants map[grant]struct{}
}

// New builds an Enforcer from every feature's policy set. A set with no
// policies is an error: its feature has almost certainly forgotten to
// declare them, and would deny everything.
func New(sets ...Set) (*Enforcer, error) {
	e := &Enforcer{grants: map[grant]struct{}{}}
	var problems []error
	for _, set := range sets {
		if len(set.policies) == 0 {
			problems = append(problems, fmt.Errorf("policy set %q is empty", set.name))
		}
		for _, p := range set.policies {
			if err := p.validate(); err != nil {
				problems = append(problems, fmt.Errorf("policy set %q: %s: %w", set.name, p, err))
				continue
			}
			for _, a := range p.actions {
				e.grants[grant{p.role, p.resource, p.scope, a}] = struct{}{}
			}
		}
	}
	if len(problems) > 0 {
		return nil, errors.Join(problems...)
	}
	return e, nil
}

func (p Policy) validate() error {
	switch {
	case p.role > AnyRole:
		return fmt.Errorf("unknown role")
	case p.scope != OwnTenant && p.scope != AnyTenant:
		return fmt.Errorf("unknown scope")
	case p.resource == 0 || p.resource >= resourceEnd:
		return fmt.Errorf("unknown resource")
	case len(p.actions) == 0:
		return fmt.Errorf("no actions")
	case p.role == Anonymous && p.scope == OwnTenant:
		return fmt.Errorf("an anonymous caller has no tenant")
	}
	for _, a := range p.actions {
		if a == 0 || a > AnyAction {
			return fmt.Errorf("unknown action")
		}
	}
	return nil
}

// Can reports whether the caller may act on an entity of tenant, which is
// empty for a resource that belongs to no tenant. The tenant decides the
// scope: the caller's own tenant is matched by OwnTenant and AnyTenant
// grants, and any other only by AnyTenant ones.
func (e *Enforcer) Can(c Caller, r Resource, a Action, tenant string) bool {
	if tenant != "" && tenant == c.Tenant && e.allows(c, r, OwnTenant, a) {
		return true
	}
	return e.allows(c, r, AnyTenant, a)
}

// Authorize is Can as an error: nil when allowed, and otherwise the denial
// Deny describes.
func (e *Enforcer) Authorize(c Caller, r Resource, a Action, tenant string) error {
	if e.Can(c, r, a, tenant) {
		return nil
	}
	return Deny(c).With("resource", r.String(), "action", a.String())
}

// Scopes answers, for a list, which tenants the caller may act under: every
// tenant, the caller's own, or none.
func (e *Enforcer) Scopes(c Caller, r Resource, a Action) Tenants {
	if e.allows(c, r, AnyTenant, a) {
		return Tenants{all: true}
	}
	if c.Tenant != "" && e.allows(c, r, OwnTenant, a) {
		return Tenants{one: c.Tenant}
	}
	return Tenants{}
}

// allows probes the exact grant and its wildcards: at most four lookups.
func (e *Enforcer) allows(c Caller, r Resource, s Scope, a Action) bool {
	roles, n := [...]Role{c.Role, AnyRole}, 1
	if c.Authenticated() {
		n = 2
	}
	for _, role := range roles[:n] {
		for _, action := range [...]Action{a, AnyAction} {
			if _, ok := e.grants[grant{role, r, s, action}]; ok {
				return true
			}
		}
	}
	return false
}

// Tenants is the answer to Scopes. The zero value is no tenant.
type Tenants struct {
	all bool
	one string
}

// All reports whether the set is every tenant.
func (t Tenants) All() bool { return t.all }

func (t Tenants) Empty() bool { return !t.all && t.one == "" }

func (t Tenants) Contains(tenant string) bool {
	return t.all || (tenant != "" && tenant == t.one)
}

// Only returns the set's tenant when it holds exactly one.
func (t Tenants) Only() (string, bool) { return t.one, !t.all && t.one != "" }

// Deny is the error for a request the caller may not make. An anonymous
// caller is told to authenticate rather than that it may not, so the two
// never read alike.
func Deny(c Caller) *errors.Error {
	if !c.Authenticated() {
		return errors.Unauthenticated("authentication_required", "this request needs a credential")
	}
	return errors.Forbidden("permission_denied", "you may not do this").With("role", c.Role.String())
}

// TenantNotPermitted is the error for a request naming a tenant outside the
// caller's reach.
func TenantNotPermitted(tenant string) *errors.Error {
	return errors.Forbidden("tenant_not_permitted", fmt.Sprintf("you may not access tenant %q", tenant))
}
