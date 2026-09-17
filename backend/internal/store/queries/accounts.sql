-- name: InsertTenantIfMissing :exec
INSERT INTO tenants (id, name)
VALUES (@id, @name)
ON CONFLICT (id) DO NOTHING;

-- name: ListTenantIDs :many
SELECT id FROM tenants ORDER BY id;

-- name: ListTenants :many
-- Every tenant, or only the one named. tenants has no row-level security, so
-- the caller's reach is applied here.
SELECT id, name
FROM tenants
WHERE sqlc.narg(id)::text IS NULL OR id = sqlc.narg(id)
ORDER BY id;

-- name: FilterTenantIDs :many
-- The subset of ids that are existing tenants.
SELECT id FROM tenants WHERE id = ANY(@ids::text[]);

-- name: InsertUserIfMissing :execrows
INSERT INTO users (email, password_hash, role, tenant_id)
VALUES (lower(@email), @password_hash, @role, sqlc.narg(tenant_id))
ON CONFLICT (email) DO NOTHING;

-- name: GetUserByEmail :one
-- users has no row-level security: it is read to find out who is calling,
-- before there is a tenant to scope by.
SELECT id, password_hash, role, tenant_id
FROM users
WHERE email = lower(@email);
