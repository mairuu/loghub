-- name: InsertAlertRule :one
INSERT INTO alert_rules (
  tenant_id, name, source, event_type, action, severity_min, tags,
  group_by, threshold, window_minutes, cooldown_minutes, webhook_url
) VALUES (
  @tenant_id, @name, sqlc.narg(source), sqlc.narg(event_type), sqlc.narg(action),
  sqlc.narg(severity_min), @tags, @group_by, @threshold, @window_minutes,
  @cooldown_minutes, sqlc.narg(webhook_url)
)
RETURNING *;

-- name: ListAlertRules :many
SELECT * FROM alert_rules ORDER BY id;

-- name: ListTenantAlertRules :many
-- The tenant is named even though row-level security already limits a
-- viewer to it: an admin asks for one tenant too.
SELECT * FROM alert_rules WHERE tenant_id = @tenant_id ORDER BY id;

-- name: ListAlerts :many
SELECT a.id, a.rule_id, r.name AS rule_name, r.group_by, a.tenant_id,
       a.group_key, a.window_start, a.window_end, a.matched, a.created_at
FROM alerts a
JOIN alert_rules r ON r.id = a.rule_id
ORDER BY a.id DESC
LIMIT @max_rows;

-- name: ListTenantAlerts :many
-- Names the tenant so Postgres can use alerts_tenant_id_idx, which the
-- row-level security condition can't.
SELECT a.id, a.rule_id, r.name AS rule_name, r.group_by, a.tenant_id,
       a.group_key, a.window_start, a.window_end, a.matched, a.created_at
FROM alerts a
JOIN alert_rules r ON r.id = a.rule_id
WHERE a.tenant_id = @tenant_id
ORDER BY a.id DESC
LIMIT @max_rows;
