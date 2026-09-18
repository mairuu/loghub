# Architecture Decision Records

Short records of the decisions that shape loghub.

| # | Decision | Status |
|---|---|---|
| [0001](0001-go-backend.md) | Go single-binary backend | Accepted |
| [0002](0002-postgres-event-store.md) | PostgreSQL event store with daily partitions | Accepted |
| [0003](0003-tenant-isolation-rls.md) | Tenant isolation with row-level security | Accepted |
| [0004](0004-vector-transport-only.md) | Vector as transport only; normalization in Go | Accepted |
| [0005](0005-caddy-edge.md) | Caddy at the edge for TLS and SPA hosting | Accepted |
| [0006](0006-react-vite-frontend.md) | React + Vite + TypeScript frontend | Accepted |
| [0007](0007-openapi-spec-first.md) | Spec-first OpenAPI with code generation | Accepted |
| [0008](0008-sqlc-queries.md) | sqlc for fixed queries, hand-written SQL for search | Accepted |
| [0009](0009-auth-jwt-rbac.md) | JWT sessions and a policy-set RBAC enforcer | Accepted |
| [0010](0010-alerting.md) | Threshold alert rules evaluated on a schedule | Accepted |
| [0011](0011-deployment-compose.md) | One Compose file for both deployment modes | Accepted |

## Template

```markdown
# NNNN. Title

- Status: Draft | Proposed | Accepted | Superseded by NNNN
- Date: YYYY-MM-DD

## Context
The forces at play and the problem that needs a decision.

## Decision
What we're doing, stated plainly.

## Consequences
What gets easier, what gets harder, and what to watch.
```