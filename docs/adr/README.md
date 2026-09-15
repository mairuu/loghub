# Architecture Decision Records

Short records of the decisions that shape loghub.

| # | Decision | Status |
|---|---|---|
| [0001](0001-go-backend.md) | Go single-binary backend | Accepted |
| [0002](0002-postgres-event-store.md) | PostgreSQL event store with daily partitions | Accepted |
| [0003](0003-tenant-isolation-rls.md) | Tenant isolation with row-level security | Accepted |

## Template

```markdown
# NNNN. Title

- Status: Proposed | Accepted | Superseded by NNNN
- Date: YYYY-MM-DD

## Context
The forces at play and the problem that needs a decision.

## Decision
What we're doing, stated plainly.

## Consequences
What gets easier, what gets harder, and what to watch.
```