# 0009. JWT sessions and a policy-set RBAC enforcer

- Status: Accepted
- Date: 2026-09-17

## Context
Requirement 3.7 needs authentication, an Admin and a Viewer role, and a Viewer who can only see their own tenant. ADR 0003 already isolates tenants in the database and assumes "verified JWT claims" set `app.tenant_id` and `app.is_admin`, but nothing has decided where those claims come from or how role checks are expressed. `users` already carries `email`, `password_hash`, `role` and a `tenant_id` that is null exactly for admins, and `internal/auth` already hashes passwords with bcrypt.

Two questions are separate and get separate answers: who is calling (authentication), and what that caller may do (authorization). The second has a list-shaped variant the API can't avoid — search and the dashboard must return *some* tenants' rows, and which ones depends on the caller.

Ingest callers are not people, and they are not bound to one tenant. Vector forwards inbox files whose records name their own tenant, and syslog senders are meant to map to tenants (ADR 0004). `samples/post_logs.py` posts demoA and demoB events in one run.

## Decision

### Authentication
- `POST /api/v1/auth/login` takes email and password, verifies the bcrypt hash, and returns an HS256 JWT with its expiry and the caller's role and tenant, so the UI never decodes the token. A wrong password and an unknown email are the same 401 `invalid_credentials`, and an unknown email is still checked against a fixed bcrypt hash, so response time doesn't reveal which accounts exist.
- The token is signed with `AUTH_SECRET`, which `make env` generates with the other secrets. The backend refuses to start without one of at least 32 bytes.
- Typed claims, not `jwt.MapClaims`: subject (user id), `role`, `tenant` (empty for an admin), `exp`, `iat`. A typed struct fails at the unmarshal instead of at each type assertion. Parsing accepts HS256 only, so a token can't choose its own algorithm, `none` included.
- One access token, no refresh token, TTL 12 hours. Refresh needs a token table and rotation to be worth anything, and nothing in the assignment asks for a session that outlives a demo.
- The UI keeps the token in `sessionStorage` and sends it as a bearer header. It survives a reload and ends with the tab. A cookie would hide it from scripts, but it would need CSRF protection, which a same-origin bearer header doesn't.
- Middleware validates the token, builds the caller, and puts it in the request context. A request without a token gets the anonymous caller rather than an early 401, so an endpoint's own policy decides whether anonymous is allowed. A token that is present but invalid or expired is a 401 `invalid_token` at once: treating it as anonymous would hide why the request failed, and the UI needs that code to send the user back to login.

### Ingest credential
- Ingest is machine traffic and doesn't log in. `make env` generates `INGEST_TOKEN`. Compose gives it to Vector as a secret file (ADR 0004) and to the backend as an environment variable, and the backend compares a presented token with it in constant time.
- A request with the key is the caller `collector`, a role no user row can hold, with no tenant. It may create events for any tenant and nothing else, so the key can't search.
- Records keep naming their own tenant, as they do now. The difference is that a record's tenant is now trusted only from a caller allowed to write to it: the collector key or an admin token. The three ingest endpoints accept either, and `post_logs.py` sends the key from `.env`. A Viewer can't ingest.
- There is no `api_keys` table and no endpoint to mint keys. Vector reads its secret when it starts, so a key minted through the API after startup would stop `make up` from being a single command. This replaces the per-tenant keys ADR 0003 anticipated.

### Authorization
Authorization is code in one place rather than conditionals spread through handlers: a policy set the binary carries, and a decision engine that answers against it. Handlers ask; they do not decide.

A policy grants a role an action on a resource under a scope, read as `Grant(role).As(scope).On(resource).Can(actions...)`. There are two scopes: any tenant, and the caller's own. Roles, resources and actions are typed constants, so a misspelt one fails to compile instead of silently matching nothing. Anything not granted is denied. The events slice, for instance:

```go
Grant(Admin).As(AnyTenant).On(Events).Can(Read, Create)
Grant(Viewer).As(OwnTenant).On(Events).Can(Read)
Grant(Collector).As(AnyTenant).On(Events).Can(Create)
```

The set is compiled in and registered at boot, one set per feature slice, so the whole of who-may-do-what is a few screens long and reviewable as a diff. A slice that registers an empty set fails at boot: it has almost certainly forgotten to declare its policies, and shipping it would leave every endpoint in it denying everything.

A decision is an exact match on (role, resource, scope, action). Role, scope and action each admit a wildcard; resource never does, so no grant reaches a resource added after it was written. That is at most eight map probes whatever the policy count, and it needs no policy language, no external engine and no reload path. A role wildcard matches authenticated callers only. Anonymous access is granted by name, as it is for login and health, so a broad grant can't open an endpoint to requests without a token.

Endpoints come in two shapes, so the engine answers two questions:

- `Can(resource, action, target)` decides about an entity in hand. The target's tenant resolves the scope. Ingest asks it for each record, and a record the caller may not write is rejected inside the 200 as `tenant_not_permitted` instead of failing the batch, which Vector would drop (ADR 0004).
- `Scopes(resource, action)` decides for lists, which have no entity to resolve a scope from. It asks the inverse — *which tenants may this caller read events under?* — and answers every tenant for an admin and one tenant for a viewer. Search and the dashboard narrow their query to exactly that, so the tenant filter is declared in the policy set instead of being rewritten as an `if role == "viewer"` in every handler. A `tenant` parameter narrows within that answer. A tenant outside it is a 403 `tenant_not_permitted`, as the search spec already declares, rather than an empty page that looks like there are no events.

A denial distinguishes anonymous from authenticated: anonymous is 401, authenticated is 403. "You must log in" never reads as "you may not do this", and an anonymous request stops there instead of falling through a mutation.

### How this relates to RLS
The enforcer and RLS are not redundant, and the split is deliberate:

- The enforcer decides role × action × scope at the API boundary — may a Viewer create an alert rule, may the collector key search. RLS cannot express any of that.
- RLS (ADR 0003) is the data-layer backstop and stays authoritative for which rows exist. Its context comes from the caller, never from a policy answer: the `tenant` claim sets `app.tenant_id`, and only the admin role sets `app.is_admin`. Search stops passing `store.AdminScope` and passes the caller's scope.

The backstop covers tenancy. A policy set that is too generous about tenants, or a handler that forgets to ask, still reads `events` through the caller's scope, which the store requires. It does not cover actions: a policy that lets a Viewer delete something is caught only in review. Ingest is also outside it, because `EventRepo.Insert` sets each tenant's context itself, so the per-record `Can` is the only tenant check on writes.

It covers only tables with RLS. `users` has none: it is read to find out who is calling, before there is a tenant to scope by, and `loghub_app` can only `SELECT` from it. Tables that hold tenant data, such as ADR 0010's rules and alerts, get the same policy as `events`. Without it, the enforcer is their only protection.

## Consequences
- Two layers have to agree about tenancy. When they disagree the result is an empty response rather than a leak, but the failure looks like a bug rather than a denial, so the policy set and the RLS predicate belong in the same review.
- This is more machinery than two roles strictly need. It earns its place on the list endpoints, where something has to decide which tenants a search returns, and it makes a new role or action a policy edit. The tenant-level RBAC bonus in §11 still needs more: a user with roles in several tenants needs a membership table, and an RLS predicate that takes a set of tenants rather than one.
- A JWT can't be revoked before it expires. A disabled or demoted user keeps their old access for up to 12 hours, and logging out only discards the token in that tab. Opaque session tokens in Postgres would be revocable at the cost of a lookup per request, and are the obvious change if this matters later.
- Login has no rate limit, so bcrypt's cost is the only brake on password guessing. Adding one is the first hardening step for the public SaaS instance. It has since been added: 10 attempts a minute from each client address ([architecture](../architecture.md#authentication-and-authorization)).
- The collector key is one secret for every sender. It can write to any tenant but read nothing, and rotating it means editing `.env` and recreating the backend and Vector. Tenant-bound keys in a table are the change to make once senders outside the appliance need their own, and the own-tenant scope they would use already exists.
- Any script running on the origin can read the token. The only third-party script there is Scalar, which `/api/docs` loads from a CDN without a pinned version, so when tokens land it gets a pinned version and an integrity hash, or is vendored.
- An `.env` written before this change has no `AUTH_SECRET` or `INGEST_TOKEN`, and `make env` never overwrites it. The backend refuses to start and names what is missing, and the lines have to be added by hand.
- New dependency: `github.com/golang-jwt/jwt/v5`. The enforcer needs nothing beyond the stdlib, and its decision table is small enough to test directly.
- Every endpoint has to remember to ask. Nothing in the type system forces a handler through the enforcer, which is the same convention-not-constraint gap ADR 0008 notes for the transaction helper.
- No new schema: login only reads `users`. The spec declares both security schemes (ADR 0007), with the three ingest endpoints accepting either. Calling `POST /ingest` from the acceptance criteria now needs a bearer token, so the Postman collection carries one as a variable, and `docs/setup_appliance.md` covers signing in and the ingest key.
