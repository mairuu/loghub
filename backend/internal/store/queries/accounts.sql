-- name: InsertTenantIfMissing :exec
INSERT INTO tenants (id, name)
VALUES (@id, @name)
ON CONFLICT (id) DO NOTHING;

-- name: ListTenantIDs :many
SELECT id FROM tenants ORDER BY id;

-- name: FilterTenantIDs :many
-- The subset of ids that are existing tenants.
SELECT id FROM tenants WHERE id = ANY(@ids::text[]);

-- name: InsertUserIfMissing :execrows
INSERT INTO users (email, password_hash, role, tenant_id)
VALUES (lower(@email), @password_hash, @role, sqlc.narg(tenant_id))
ON CONFLICT (email) DO NOTHING;
