-- name: SetTenantContext :exec
-- Transaction-local settings read by the RLS policy on events; they reset at commit or rollback.
SELECT
  set_config('app.tenant_id', @tenant_id::text, true),
  set_config('app.is_admin', CASE WHEN @is_admin::boolean THEN 'on' ELSE 'off' END, true);

-- name: MaintainPartitions :exec
SELECT loghub_maintain_partitions(@retention_days::integer);

