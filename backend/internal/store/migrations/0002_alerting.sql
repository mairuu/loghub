-- +goose Up
-- Threshold alert rules and their firings. See docs/adr/0010-alerting.md.

CREATE TABLE alert_rules (
  id               bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  tenant_id        text NOT NULL REFERENCES tenants (id),
  name             text NOT NULL CHECK (name <> ''),
  -- The filter: an event must match every one that is set.
  source           text CHECK (source IN ('firewall', 'network', 'crowdstrike', 'aws', 'm365', 'ad', 'api')),
  event_type       text,
  action           text,
  severity_min     smallint CHECK (severity_min BETWEEN 0 AND 10),
  tags             text[] NOT NULL DEFAULT '{}',
  -- The API's names; internal/store maps each to its column.
  group_by         text NOT NULL CHECK (group_by IN ('src_ip', 'dst_ip', 'user', 'host')),
  threshold        integer NOT NULL CHECK (threshold >= 1),
  window_minutes   integer NOT NULL CHECK (window_minutes BETWEEN 1 AND 1440),
  cooldown_minutes integer NOT NULL CHECK (cooldown_minutes BETWEEN 0 AND 10080),
  webhook_url      text,
  created_at       timestamptz NOT NULL DEFAULT now(),
  -- The target of alerts' foreign key, which keeps a firing in its rule's tenant.
  UNIQUE (id, tenant_id)
);

CREATE TABLE alerts (
  id           bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  rule_id      bigint NOT NULL,
  tenant_id    text NOT NULL,
  group_key    text NOT NULL,
  window_start timestamptz NOT NULL,
  window_end   timestamptz NOT NULL,
  matched      bigint NOT NULL,
  created_at   timestamptz NOT NULL DEFAULT now(),
  FOREIGN KEY (rule_id, tenant_id) REFERENCES alert_rules (id, tenant_id),
  -- Evaluating the same window twice can't record a firing twice. Also serves
  -- the cooldown lookup.
  UNIQUE (rule_id, group_key, window_start)
);

CREATE INDEX alerts_tenant_id_idx ON alerts (tenant_id, id DESC);

-- The same isolation as events (ADR 0003, ADR 0009).
ALTER TABLE alert_rules ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON alert_rules
  USING (tenant_id = current_setting('app.tenant_id', true) OR current_setting('app.is_admin', true) = 'on')
  WITH CHECK (tenant_id = current_setting('app.tenant_id', true) OR current_setting('app.is_admin', true) = 'on');

ALTER TABLE alerts ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON alerts
  USING (tenant_id = current_setting('app.tenant_id', true) OR current_setting('app.is_admin', true) = 'on')
  WITH CHECK (tenant_id = current_setting('app.tenant_id', true) OR current_setting('app.is_admin', true) = 'on');

-- Rules can be created and read, not changed (ADR 0010), and only the
-- evaluator writes alerts.
GRANT SELECT, INSERT ON alert_rules, alerts TO loghub_app;

-- +goose Down
DROP TABLE IF EXISTS alerts;
DROP TABLE IF EXISTS alert_rules;
