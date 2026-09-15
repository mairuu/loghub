# 0002. PostgreSQL event store with daily partitions

- Status: Accepted
- Date: 2026-09-14

## Context
Events must be searchable by normalized fields and by raw payload. The dashboard needs Top-N and timeline aggregations filtered by tenant, source and time. Data must be kept for at least 7 days. The store has to run next to everything else on an 8 GB appliance. The options were OpenSearch, ClickHouse and PostgreSQL.

## Decision
Use PostgreSQL 18.

- One `events` table holds the common schema as typed columns (`inet` for IPs, `smallint` for severity), plus `raw jsonb` and `tags text[]`.
- The table is `PARTITION BY RANGE (ts)` with one partition per day, plus a DEFAULT partition for timestamps outside the pre-created range.
- Indexes are declared on the parent, so every partition gets them: `(tenant_id, ts DESC)`, `(tenant_id, event_type, ts)`, `src_ip`, `user_name`, and GIN on `raw jsonb_path_ops` and on `tags`.
- Retention runs through `loghub_maintain_partitions(retention_days)`, a `SECURITY DEFINER` function. It creates the next days' partitions, drops partitions older than the window, and deletes expired rows from DEFAULT. The backend calls it hourly.

OpenSearch was ruled out on memory (a 2–4 GB heap). ClickHouse would aggregate faster but has weaker tenant isolation and a less familiar dialect.

## Consequences
- Dropping a partition is instant and leaves no bloat, unlike `DELETE`.
- Time-range queries only touch the days they cover.
- Tenant isolation can be enforced inside the database (ADR 0003).
- The footprint is small, and there is one database to back up.
- Free-text search is weaker than OpenSearch. If searching message text matters, add `pg_trgm` or a `tsvector` column.
- Aggregating many millions of rows will be slower than ClickHouse. That's fine for a demo; at scale the answer is rollup tables or ClickHouse.