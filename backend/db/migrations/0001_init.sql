-- +goose Up
-- The loghub_app role is created by `loghub migrate` before migrations run.

CREATE TABLE tenants (
  id         text PRIMARY KEY CHECK (id ~ '^[A-Za-z0-9_-]{1,64}$'),
  name       text NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE users (
  id            bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  email         text NOT NULL UNIQUE,
  password_hash text NOT NULL,
  role          text NOT NULL CHECK (role IN ('admin', 'viewer')),
  tenant_id     text REFERENCES tenants (id),
  created_at    timestamptz NOT NULL DEFAULT now(),
  -- Admins see every tenant; a viewer belongs to exactly one.
  CHECK ((role = 'admin') = (tenant_id IS NULL))
);

-- The common schema (assignment section 3). See docs/adr/0002-postgres-event-store.md.
CREATE TABLE events (
  id               bigint GENERATED ALWAYS AS IDENTITY,
  ts               timestamptz NOT NULL,
  received_at      timestamptz NOT NULL DEFAULT now(),
  tenant_id        text NOT NULL REFERENCES tenants (id),
  source           text NOT NULL CHECK (source IN ('firewall', 'network', 'crowdstrike', 'aws', 'm365', 'ad', 'api')),
  vendor           text,
  product          text,
  event_type       text,
  event_subtype    text,
  severity         smallint CHECK (severity BETWEEN 0 AND 10),
  action           text,
  src_ip           inet,
  src_port         integer CHECK (src_port BETWEEN 0 AND 65535),
  dst_ip           inet,
  dst_port         integer CHECK (dst_port BETWEEN 0 AND 65535),
  protocol         text,
  user_name        text,
  host             text,
  process          text,
  url              text,
  http_method      text,
  status_code      integer,
  rule_name        text,
  rule_id          text,
  cloud_account_id text,
  cloud_region     text,
  cloud_service    text,
  raw              jsonb NOT NULL,
  tags             text[] NOT NULL DEFAULT '{}',
  PRIMARY KEY (id, ts)
) PARTITION BY RANGE (ts);

-- Catches timestamps outside the daily partitions that exist.
CREATE TABLE events_default PARTITION OF events DEFAULT;

CREATE INDEX events_tenant_ts_idx      ON events (tenant_id, ts DESC);
CREATE INDEX events_tenant_type_ts_idx ON events (tenant_id, event_type, ts);
CREATE INDEX events_src_ip_idx         ON events (src_ip);
CREATE INDEX events_user_name_idx      ON events (user_name);
CREATE INDEX events_raw_idx            ON events USING gin (raw jsonb_path_ops);
CREATE INDEX events_tags_idx           ON events USING gin (tags);

-- See docs/adr/0003-tenant-isolation-rls.md. Settings are transaction-local, set per request.
ALTER TABLE events ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON events
  USING (tenant_id = current_setting('app.tenant_id', true) OR current_setting('app.is_admin', true) = 'on')
  WITH CHECK (tenant_id = current_setting('app.tenant_id', true) OR current_setting('app.is_admin', true) = 'on');

-- Creates daily partitions from the retention cutoff to two days ahead, drops partitions
-- older than the cutoff, and purges expired rows from the DEFAULT partition.
-- +goose StatementBegin
CREATE FUNCTION loghub_maintain_partitions(retention_days integer)
RETURNS void
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = public, pg_temp
SET TimeZone = 'UTC'
AS $$
DECLARE
  cutoff    date := current_date - retention_days;
  day       date;
  part_name text;
BEGIN
  IF retention_days IS NULL OR retention_days < 1 THEN
    RAISE EXCEPTION 'retention_days must be at least 1, got %', retention_days;
  END IF;

  FOR day IN SELECT d::date FROM generate_series(cutoff, current_date + 2, interval '1 day') AS d LOOP
    part_name := 'events_' || to_char(day, 'YYYYMMDD');
    CONTINUE WHEN to_regclass(part_name) IS NOT NULL;

    -- A partition can't be created while DEFAULT holds rows in its range,
    -- so move those rows out and back in around the CREATE. The CREATE locks
    -- events exclusively anyway; locking before the DELETE keeps concurrent
    -- inserts from landing in DEFAULT in between.
    LOCK TABLE events IN ACCESS EXCLUSIVE MODE;
    CREATE TEMP TABLE moving ON COMMIT DROP AS
      WITH moved AS (DELETE FROM events_default WHERE ts >= day AND ts < day + 1 RETURNING *)
      SELECT * FROM moved;
    EXECUTE format('CREATE TABLE %I PARTITION OF events FOR VALUES FROM (%L) TO (%L)', part_name, day, day + 1);
    INSERT INTO events OVERRIDING SYSTEM VALUE SELECT * FROM moving;
    DROP TABLE moving;
  END LOOP;

  FOR part_name IN
    SELECT c.relname
    FROM pg_inherits i
    JOIN pg_class c ON c.oid = i.inhrelid
    WHERE i.inhparent = 'events'::regclass
      AND c.relname ~ '^events_[0-9]{8}$'
      AND to_date(substring(c.relname FROM 8), 'YYYYMMDD') < cutoff
  LOOP
    EXECUTE format('DROP TABLE %I', part_name);
  END LOOP;

  DELETE FROM events_default WHERE ts < cutoff;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
BEGIN
  EXECUTE format('GRANT CONNECT ON DATABASE %I TO loghub_app', current_database());
END
$$;
-- +goose StatementEnd
GRANT USAGE ON SCHEMA public TO loghub_app;
REVOKE ALL ON FUNCTION loghub_maintain_partitions(integer) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION loghub_maintain_partitions(integer) TO loghub_app;
GRANT SELECT, INSERT ON events TO loghub_app;
GRANT SELECT ON tenants, users TO loghub_app;

-- Initialize partitions for the current date and the next two days.
SELECT loghub_maintain_partitions(7);

-- +goose Down
DROP FUNCTION IF EXISTS loghub_maintain_partitions(integer);
DROP TABLE IF EXISTS events;
DROP TABLE IF EXISTS users;
DROP TABLE IF EXISTS tenants;
