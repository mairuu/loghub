# 0010. Threshold alert rules evaluated on a schedule

- Status: Accepted
- Date: 2026-09-17

## Context
Requirement 3.6 needs at least one alert rule, and a triggered alert shown on a UI page, sent to a webhook or emailed. The acceptance criteria are to create a sample rule and then observe a notification. The worked example — repeated failed logins from the same IP within five minutes — is a count over a time window, grouped by a field.

The alternative to scheduled evaluation is evaluating each event as it is ingested. That keeps state per rule in memory, has to survive restarts, and would make ingest latency depend on how many rules exist. The events are already in a partitioned, indexed table, so the same question can be asked as a query.

## Decision

### Rules and firings
- `alert_rules` holds rules as data: a tenant, an optional filter over the normalized fields (`source`, `event_type`, `action`, `severity`, `tags`), a `group_by` column drawn from an allow-list, a threshold, a window in minutes and a cooldown. The worked example is one row, not code: `tags` contains `auth_failure`, grouped by `src_ip`. The normalizer adds that tag to failed logins from Active Directory, Microsoft 365, CloudTrail and API senders, so the rule doesn't have to list each vendor's event name.
- `alerts` holds each firing: rule, tenant, group key, window bounds, the matched count, and `created_at`.
- Both tables carry `tenant_id` and get the same RLS policy as `events`, as ADR 0009 requires. `loghub_app` gets `SELECT, INSERT` on both, which is all the API and the evaluator below need.
- Admins create and read rules for any tenant. A Viewer reads their own tenant's rules and alerts, and can't create a rule. No endpoint writes to `alerts`; only the evaluator does.

### Evaluation
- A ticker in the `serve` process (ADR 0001) evaluates every 60 seconds. Each rule becomes one grouped `COUNT(*) ... HAVING COUNT(*) >= threshold` over its window. Rows whose `group_by` column is null are left out, so failed logins with no source address don't fire together as one unknown IP.
- Evaluation loops over tenants and runs each rule in its own transaction through the tenant-aware helper (ADR 0003), scoped to the rule's tenant, so RLS applies to the evaluator exactly as it does to a request. The evaluator does not run as an admin.
- As search does, the query also names the tenant, because Postgres can't use an index for the RLS condition. `events_tenant_ts_idx` then serves the window, and `events_tenant_type_ts_idx` serves a rule that filters on `event_type`.
- Each rule's query has a timeout. A rule that fails is logged and skipped, so one bad rule can't hold up the rest.
- Each window ends on the last whole minute at least 30 seconds in the past. Events arriving through Vector's disk buffer (ADR 0004) can land slightly late, and a window that ends at `now()` could be evaluated before the last events of a burst arrive. Because windows end on a minute boundary, every evaluation within the same minute covers the same window.
- A firing is unique on (rule, group key, window start) and inserted with `ON CONFLICT DO NOTHING`. If the same window is evaluated twice, after a restart or by a second replica at the same moment, it is still recorded only once. The cooldown alone wouldn't stop that race, since both evaluations would read before either writes.
- Successive ticks cover overlapping windows with different starts, so the same burst would match again on each of them. A per-rule cooldown suppresses repeat firings for the same group key until it elapses. It defaults to the window length: one burst fires once, and an attack that keeps going fires again once per window.

### Delivery
- The alert page in the UI reads `alerts`, and that is the channel the requirement asks for.
- A rule may also name a webhook URL. The evaluator POSTs a firing to it with a short timeout, and only after the insert commits. A firing the unique key dropped sends nothing, so it is never delivered twice. Delivery is best-effort: a failure is logged and not retried, and the firing stays on the alert page either way.

## Consequences
- Rules are rows, so adding one during the demo is an API call rather than a deploy.
- Detection lags by up to two and a half minutes: up to a minute to the next minute boundary, the 30-second lag, and up to a minute to the next tick. That is well inside what the requirement asks for, and it is the cost of not holding rule state in memory.
- Windows are evaluated once a minute, not continuously. A burst that takes more than four of a five-minute rule's minutes may not fit inside any evaluated window, and is missed. Widening each evaluated window by a minute would catch it, at the cost of sometimes firing on a burst slightly longer than the rule says.
- Events that arrive more than a window late are stored but never alerted on. The usual cause is Vector replaying its buffer after a backend outage.
- A rule watches one tenant, so watching every tenant for failed logins takes one row per tenant. A single rule covering every tenant would need the evaluator to run with the admin scope, which this decision rules out.
- Evaluation runs one query per rule every tick, so its cost grows with the number of rules. That is fine at demo size. Rules of the same shape in different tenants could share one query grouped by tenant, but that query would have to run with the admin scope.
- Alerting is a background job in the API process, as ADR 0001 intends for retention and partition maintenance. ADR 0001 already notes that running several replicas needs a lock before any of these jobs are safe. The unique key keeps a replica race from doubling a firing, but that doesn't replace the lock.
- The rule shape is deliberately narrow. Anything correlating two different event types, or comparing against a baseline, does not fit and is out of scope.
- Rules can be created and read, not changed or removed. The enforcer has no update or delete action yet, and `loghub_app` has no grant for either. Both are small additions once a rule needs switching off.
- The webhook makes the backend send a request to any URL an admin names, including addresses on the Compose network and cloud metadata endpoints. Only admins can set one. Letting a tenant set its own would need an allow-list first.
- `alert_rules` and `alerts` arrive in a new migration with their RLS policies and grants. The spec gains the rule and alert endpoints (ADR 0007), and `authz` gains a resource for each.
