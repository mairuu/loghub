# 0008. sqlc for fixed queries, hand-written SQL for search

- Status: Accepted
- Date: 2026-09-16

## Context
Most database access has a fixed shape: tenants, users, API keys, alert rules and alerts, Top-N and timeline aggregations, retention and batch inserts. Writing `rows.Scan` by hand for each is slow, and mistakes only show up at runtime. Search is the exception: any combination of optional filters, sort order and pagination.

## Decision
- sqlc, pinned to 1.31.1, generates pgx/v5 code from `internal/store/queries/*.sql` into `internal/store/gen`, using the goose migrations as the schema. It runs from the `sqlc/sqlc` Docker image, and generated code is committed.
- Type overrides keep generated structs plain Go rather than `pgtype` wrappers: pointers for nullable columns, `time.Time` for `timestamptz`, `netip.Addr` for `inet`. Changing them later churns every call site, so they are settled here.
- Batch event inserts use sqlc's `:batchexec`, one pgx round trip per request. `COPY` is faster but unavailable to a role under row-level security, and the backend connects as `loghub_app` (ADR 0003).
- The RLS tenant context (ADR 0003) is set by a generated `set_config` query inside the transaction helper on `Store`. Every query against `events` goes through that helper.
- Search is one hand-written builder in `internal/store/search.go`: conditions appended with positional parameters, sort columns from an allow-list. `sqlc.narg()` could express the optional filters, but `($1 IS NULL OR col = $1)` costs index use under prepared statements.

## Consequences
- Queries stay plain SQL, checked against the schema at generate time. A renamed column breaks `make gen-sql`, not production, and `make lint` fails on generated code that has drifted from the queries.
- There is no ORM to learn or explain.
- Data access has two styles. The dynamic one is confined to one file with its own tests.
- Nothing forces a query through the transaction helper. A repository built on the bare pool compiles and sees no rows, so it fails closed rather than leaking, but the helper stays a convention until the `events` repositories can only be built from a transaction.
- sqlc only knows what the migrations declare, so the daily partitions created at runtime are invisible to it.
