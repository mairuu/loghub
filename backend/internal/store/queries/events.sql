-- name: InsertEvents :batchexec
-- The parameters are in NewEventParams order, which store/events.go relies on
-- to convert one struct to the other.
INSERT INTO events (
  ts, tenant_id, source, vendor, product, event_type, event_subtype, severity,
  action, src_ip, src_port, dst_ip, dst_port, protocol, user_name, host,
  process, url, http_method, status_code, rule_name, rule_id,
  cloud_account_id, cloud_region, cloud_service, raw, tags
) VALUES (
  @ts, @tenant_id, @source, @vendor, @product, @event_type, @event_subtype, @severity,
  @action, @src_ip, @src_port, @dst_ip, @dst_port, @protocol, @user_name, @host,
  @process, @url, @http_method, @status_code, @rule_name, @rule_id,
  @cloud_account_id, @cloud_region, @cloud_service, @raw, @tags
);
