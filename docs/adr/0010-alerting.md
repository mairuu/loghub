# 0010. Threshold alert rules evaluated on a schedule

- Status: Draft
- Date: 2026-09-16

## Context
Requirement 3.6 needs at least one alert rule and one delivery channel, and the acceptance criteria are to create a rule and then watch a notification arrive. The worked example — repeated failed logins from the same IP within five minutes — is a count over a time window, grouped by a field.

The alternative to scheduled evaluation is evaluating each event as it is ingested. That keeps state per rule in memory, has to survive restarts, and would make ingest latency depend on how many rules exist. The events are already in a partitioned, indexed table, so the same question can be asked as a query.

## Decision
- `alert_rules` holds rules as data: a tenant, an optional filter over the normalized fields (`source`, `event_type`, `action`, `severity`), a `group_by` column drawn from an allow-list, a threshold and a window in minutes. The worked example is one row, not code.
- `alerts` holds each firing: rule, tenant, group key, window bounds, the matched count, and `created_at`.
- A ticker in the `serve` process (ADR 0001) evaluates every 60 seconds. Each rule becomes one grouped `COUNT(*) ... HAVING count >= threshold` over its window — the shape `events_tenant_type_ts_idx` and `events_tenant_ts_idx` already serve.
- Evaluation loops over tenants and runs each rule inside the tenant-aware transaction helper (ADR 0003), so RLS applies to the evaluator exactly as it does to a request. The evaluator does not run as an admin.
- Each window ends 30 seconds in the past. Events arriving through Vector's disk buffer can land slightly late, and evaluating right up to `now()` would miss them permanently.
- A firing is keyed by (rule, group key, window start), so re-evaluating an overlapping window cannot insert a duplicate. A per-rule cooldown suppresses repeat firings for the same group key until it elapses — without one, a rule whose window is five minutes fires on every tick for five minutes.
- Delivery is the `alerts` table read by the UI alert page, which is the channel the requirement asks for. An optional webhook URL per rule POSTs the firing; it is best-effort and logged on failure.

## Consequences
- Rules are rows, so adding one during the demo is an API call rather than a deploy.
- Detection lags by up to the tick plus the 30-second lag. Well inside what the requirement asks for, and the cost of not holding rule state in memory.
- Evaluation cost grows with rules × tenants. Fine at demo size; a single query grouped across tenants would be the first optimisation, and it would have to give up running under RLS.
- Alerting joins retention and partition maintenance as a background job in the API process. ADR 0001 already notes that running several replicas needs a lock before any of them are safe.
- The rule shape is deliberately narrow. Anything correlating two different event types, or comparing against a baseline, does not fit and is out of scope.
- `alert_rules` and `alerts` are new schema, and `loghub_app` currently holds only `SELECT, INSERT` on `events` and `SELECT` on `tenants` and `users`. The migration has to grant what the evaluator and the rule API need.
