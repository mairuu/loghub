# 0003. Tenant isolation with Row-Level Security

- Status: Accepted
- Date: 2026-09-14

## Context
A Viewer must only see their own tenant's data, and an Admin sees every tenant. If isolation depends on every query remembering `WHERE tenant_id = $1`, it fails silently the first time someone forgets.

## Decision
- All tenants share the tables, and each row carries a `tenant_id`.
- `events` has Row-Level Security. A row is visible, and may be written, when `tenant_id = current_setting('app.tenant_id', true)` or `current_setting('app.is_admin', true) = 'on'`.
- The backend connects as `loghub_app`, which doesn't own the tables, so RLS always applies. `loghub migrate` creates the role and keeps its password in sync with `APP_DB_PASSWORD`. Only `migrate` and `seed` connect as the owner role.
- Every request runs in a transaction that sets `app.tenant_id` and `app.is_admin` from the verified JWT claims with `set_config(..., true)`, which is transaction-local. A Viewer's tenant never comes from query parameters.
- Ingest sets the tenant for each group of events before inserting, so `WITH CHECK` rejects rows for any other tenant. In the skeleton the tenant comes from the event body; per-tenant ingest API keys replace that in the auth milestone.

## Consequences
- A forgotten filter returns no rows instead of another tenant's data. This is a second layer beneath the API checks.
- Transaction-local settings reset at commit or rollback, so a pooled connection can't carry one tenant's context into the next request.
- All database access has to go through the tenant-aware transaction helper. A query outside it sees nothing, so it fails closed.
- A schema or database per tenant would isolate more strongly and allow per-tenant retention. Moving there later is contained, because all access already goes through one helper.