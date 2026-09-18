# 0012. Prometheus metrics, with Grafana in an optional profile

- Status: Accepted
- Date: 2026-09-19

## Context
Until now, the backend's JSON log was the only way to tell what loghub was doing: how many events arrive, from which tenant and source, how many are rejected, whether anyone is guessing passwords, whether the alert evaluator and partition job are keeping up. The log says each of these once, per request or per run. Seeing a rate or a trend means reading it all back. §11 lists observability (metrics, traces) as a bonus.

The operator needs to see these numbers, and so does a reviewer on the SaaS deployment. The public internet doesn't need them: per-tenant volumes and sign-in failures say more about a deployment than the UI shows to a viewer.

## Decision

### Collecting
- The backend exposes Prometheus metrics with `github.com/prometheus/client_golang`, the standard Go client. It includes the Go runtime, process and build-info collectors, so those need no code of our own.
- Metrics are counted where things happen, in memory:
  - API requests by route and status in the middleware that logs them;
  - sign-in outcomes in the sign-in handler;
  - stored events by tenant and source, and rejected records by code, once a batch is stored;
  - alerts raised and webhook deliveries in the evaluator;
  - runs, durations and the last success of each background job.

  The connection pool is read when Prometheus scrapes. No metric queries the database, so a slow database can't slow a scrape.
- Every label has a closed set of values. A route is a registered ServeMux pattern, and anything else is `unmatched`. A tenant is one the insert accepted. A source is one the normalizer knows, and a code is one of the API's. A client can't create a series by sending an odd path, method or tenant.
- Each package that records metrics owns them and registers them with the `prometheus.Registerer` in its config. `serve` passes one registry to all of them, and tests pass their own.
- Vector's `internal_metrics` source feeds a `prometheus_exporter` sink, so the collector's inputs and disk buffer are measured too.

### Serving
- `serve` has a second listener, `METRICS_ADDR` (`:9090`), that serves only `/metrics`. Compose publishes no port for it and Caddy doesn't route to it, so it is reachable only on the Compose network and needs no credential. Vector's exporter on 9598 is the same.
- Prometheus and Grafana run in a Compose profile, `monitoring`, like the simulator's `demo`. `COMPOSE_PROFILES=monitoring` in `.env` starts them with the stack. Prometheus scrapes every 15 seconds and keeps 7 days, the same as the events. It is published on `127.0.0.1:9090` only, for an SSH tunnel.
- Caddy routes `/grafana/` to Grafana, which has its own login: the loghub admin's email and `ADMIN_PASSWORD`. Anonymous access and sign-up are off. Reviewers use the credentials the operator already sends them.
- The dashboard and data source are provisioned from `monitoring/grafana`, and the dashboard is read-only in Grafana. It changes in the repository, like everything else.

Alternatives: writing the text exposition format by hand would avoid the dependency, but it would also mean writing the histograms and runtime metrics. Serving `/metrics` on the API port behind the admin token would reach reviewers without Grafana, but Prometheus would then need a token that expires every 12 hours. Pushing to a hosted service would need an account and an outbound path that an appliance may not have.

## Consequences
- New dependency: `github.com/prometheus/client_golang`, which brings `prometheus/common`, `prometheus/procfs`, `client_model` and `protobuf` with it.
- The counters live in the backend's memory, so a restart sets them to zero. Prometheus's `rate()` and `increase()` handle resets, and totals such as "events in the last 24 hours" come from them rather than from the table.
- The metrics describe one backend. A second replica would need its own scrape target, which Prometheus sums across without change.
- Grafana stores the admin password on its first start. Changing `ADMIN_PASSWORD` later changes loghub's accounts only after a reseed, and Grafana's only with `grafana cli admin reset-admin-password`. `make reset` deletes both.
- Without the profile, `/grafana/` answers 502, and nothing else changes.
- Prometheus and Grafana add about 300 MB of memory to the VM.
- The CI acceptance job checks the backend's and Vector's metrics from inside the stack, without starting Prometheus or Grafana, so it pulls no extra images.
- Traces are not covered. Every request already has an ID in its log line, which is what a trace ID would add first.
