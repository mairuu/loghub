# 0009. JWT sessions and a policy-set RBAC enforcer

- Status: Draft
- Date: 2026-09-16

## Context
Requirement 3.7 needs authentication, an Admin and a Viewer role, and a Viewer who can only see their own tenant. ADR 0003 already isolates tenants in the database and assumes "verified JWT claims" feed `app.tenant_id`, but nothing has decided where those claims come from or how role checks are expressed. `users` already carries `email`, `password_hash`, `role` and a nullable `tenant_id`, and `internal/auth` already hashes passwords with bcrypt.

Two questions are separate and get separate answers: who is calling (authentication), and what that caller may do (authorization). The second has a list-shaped variant the API can't avoid — search and the dashboard must return *some* tenants' rows, and which ones depends on the caller.

## Decision

### Authentication
- `POST /api/v1/auth/login` takes email and password, verifies the bcrypt hash, and returns an HS256 JWT signed with `AUTH_SECRET` from `.env`.
- Typed claims, not `jwt.MapClaims`: subject (user id), `role`, `tenant` (empty for an admin), `exp`, `iat`. A typed struct fails at the unmarshal instead of at each type assertion.
- One access token, no refresh token, TTL 12 hours. Refresh needs a token table and rotation to be worth anything, and nothing in the assignment asks for a session that outlives a demo.
- Middleware validates the token, builds the caller, and puts it in the request context. Requests without a token get the anonymous caller rather than an early 401, so an endpoint's own policy decides whether anonymous is allowed.
- Ingest is machine traffic and does not use JWTs. A new `api_keys` table holds per-tenant bearer tokens, stored hashed; Vector gets one for `/api/v1/ingest/batch` (ADR 0004). This replaces the skeleton's "tenant comes from the event body" that ADR 0003 flagged.

### Authorization
Authorization is code in one place rather than conditionals spread through handlers: a policy set the binary carries, and a decision engine that answers against it. Handlers ask; they do not decide.

A policy grants a role an action on a resource under a scope, read as `Grant(role).As(scope).On(resource).Can(actions...)`. The set is compiled in and registered at boot, one set per feature slice, so the whole of who-may-do-what is a few screens long and reviewable as a diff. A slice that registers an empty set fails at boot: it has almost certainly forgotten to declare its policies, and shipping it would leave its endpoints denying everything, or never asking at all.

A decision is an exact match on (role, resource, scope, action), each axis admitting a wildcard. Resource never wildcards across resources. That is a fixed handful of map probes whatever the policy count, and it needs no policy language, no external engine and no reload path.

Endpoints come in two shapes, so the engine answers two questions:

- `Can(resource, action, target)` decides about an entity in hand. The target resolves the scope — here, whether the row belongs to the caller's tenant.
- `Scopes(resource, action)` decides for lists, which have no entity to resolve a scope from. It asks the inverse — *which tenants may this caller read events under?* — and returns every tenant for an admin, one tenant for a viewer. Search and the dashboard narrow their query to exactly that, so the tenant filter is declared in the policy set instead of being rewritten as an `if role == "viewer"` in every handler.

A denial distinguishes anonymous from authenticated: anonymous is 401, authenticated is 403. "You must log in" never reads as "you may not do this", and an anonymous request stops there instead of falling through a mutation.

Ingest is a caller like any other. An API-key request is a subject carrying its own role and tenant, decided by the same policy set rather than by a special case routed around it.

### How this relates to RLS
The enforcer and RLS are not redundant, and the split is deliberate:

- The enforcer decides role × action × scope at the API boundary — may a Viewer delete an alert rule, may anyone create a user. RLS cannot express any of that.
- RLS (ADR 0003) is the data-layer backstop and stays authoritative for which rows exist. The `tenant` claim is what the request transaction feeds to `set_config`.

A handler that forgets its policy check is caught by RLS; a policy set that is too generous is caught by RLS; RLS being bypassed on a non-`events` table is caught by the policy check. Neither layer is load-bearing alone.

## Consequences
- Two layers have to agree about tenancy. When they disagree the result is an empty response rather than a leak, but the failure reads as a bug rather than as a denial, so the policy set and the RLS predicate belong in the same review.
- This is more machinery than two roles strictly need. It earns its place on the list endpoints — something has to decide which tenants a search returns — and it makes the tenant-level RBAC bonus in §11 a policy edit rather than a refactor.
- A JWT can't be revoked before it expires. A disabled user keeps working for up to 12 hours. Opaque session tokens in Postgres would be revocable at the cost of a lookup per request, and are the obvious change if this matters later.
- New dependency: `github.com/golang-jwt/jwt/v5`. The enforcer needs nothing beyond the stdlib, and its decision table is small enough to test directly.
- Every endpoint has to remember to ask. Nothing in the type system forces a handler through the enforcer, which is the same convention-not-constraint gap ADR 0008 notes for the transaction helper, and the reason RLS sits underneath.
- `api_keys` is new schema and needs its own migration, an admin endpoint to mint keys, and a line in `docs/setup_appliance.md` about the key Vector uses.
