# loghub

[![CI](https://github.com/mairuu/loghub/actions/workflows/ci.yml/badge.svg)](https://github.com/mairuu/loghub/actions/workflows/ci.yml)

A demo multi-tenant log management system. It collects security logs over syslog, HTTPS and files, normalizes them to one schema, and lets each tenant search them, chart them and raise alerts on them. The same Compose stack runs as an appliance on one machine and as a SaaS deployment on a cloud VM.

- **Ingestion:** Syslog over UDP and TCP on port 514, `POST /ingest` and a batch endpoint over HTTPS, file uploads in the UI, and NDJSON files dropped into `inbox/`.
- **Normalization:** RFC 3164 and RFC 5424 syslog with key=value bodies, and JSON from AWS CloudTrail, Microsoft 365, Active Directory, CrowdStrike and custom apps, all mapped to one event schema. The original is always kept.
- **Storage and search:** PostgreSQL 18 with daily partitions and 7-day retention. Search by text or by any normalized field.
- **Dashboard:** A timeline and the top source IPs, users, event types and hosts, filtered by time, tenant and source.
- **Alerting:** Threshold rules evaluated every minute. Alerts appear in the UI and are sent to webhooks.
- **Security:** HTTPS at the edge, JWT sign-in, admin and viewer roles, and tenant isolation enforced by Postgres row-level security as well as by the API.
- **Operations:** Prometheus metrics with a Grafana dashboard, CI on every push, and acceptance checks that run against any deployment.

## Live demo

The SaaS deployment is at <https://loghub.mairuu.online>, and its API reference is at <https://loghub.mairuu.online/api/docs>. Reviewers get the sign-in credentials privately. [What reviewers can do from their own machines](docs/setup_saas.md#what-reviewers-can-do-from-their-own-machines) lists what works from outside.

## Quick start

You need Linux with Docker Engine and the Compose plugin, plus `make`, `git`, `python3`, `openssl`, `jq` and `nc`. Ports 80, 443 and 514 must be free. [Appliance setup](docs/setup_appliance.md#requirements) says how to install them on Ubuntu.

```sh
git clone https://github.com/mairuu/loghub.git
cd loghub
make up                      # write .env with random secrets, build, start, and wait until healthy
make ca                      # save Caddy's root certificate as loghub-root-ca.crt, which the scripts below trust
make acceptance              # check the stack against the acceptance criteria, and create the sample alert rule
make simulate backfill=24h   # a day of history from two made-up companies, then live traffic until ^C
```

Open <https://localhost>. The browser warns about the certificate until you [trust `loghub-root-ca.crt`](docs/setup_appliance.md#trust-the-certificate). `make up` generated the passwords into `.env`:

```sh
grep -E '^(ADMIN|VIEWER)_PASSWORD=' .env
```

| Account | Role | Sees | Password |
|---|---|---|---|
| `admin@loghub.local` | admin | every tenant | `ADMIN_PASSWORD` |
| `viewer@demoa.local` | viewer | demoA | `VIEWER_PASSWORD` |
| `viewer@demob.local` | viewer | demoB | `VIEWER_PASSWORD` |
| `viewer@democ.local` | viewer | demoC | `VIEWER_PASSWORD` |

demoA and demoB get the simulated traffic and the acceptance checks' events. demoC gets neither, so it holds only what you send it.

[Appliance setup](docs/setup_appliance.md) walks through each feature, with the API calls.

## Common tasks

```sh
make up                      # start the stack; run it again after a git pull or an .env change
make ps                      # each service's status
make logs s=backend          # follow one service's logs, or every service's without s=
make send-samples            # send samples/ through the collector, over syslog and inbox/
make simulate                # made-up traffic until ^C; backfill=24h fills the last day first
make acceptance              # check the stack against the acceptance criteria
make down                    # stop the stack and keep the data
make reset                   # stop the stack and delete every volume, the CA included, after asking
```

Optional services start with the stack once `.env` names their profile, as in `COMPOSE_PROFILES=demo,monitoring`, and `make up` has run. `demo` keeps the simulator running, and `monitoring` adds Prometheus and Grafana at <https://localhost/grafana/> (sign in with the admin account).

`make simulate` and `make acceptance` take `url=https://...` to run against another deployment, such as the live demo. [setup_saas.md](docs/setup_saas.md#what-reviewers-can-do-from-their-own-machines) says what that needs.

`make help` lists every target.

## Project structure

| Path | What |
|---|---|
| `docker-compose.yml`, `Makefile`, `.env.example` | The stack, the common tasks (`make help`), and every setting with what it does. |
| [`api/`](api) | `openapi.yaml`, the API contract. The Go server interface and the frontend's types are generated from it. |
| [`backend/`](backend) | The Go service. `cmd/loghub` builds one binary with `serve`, `migrate`, `seed` and `healthcheck` commands. |
| [`backend/internal/ingest/`](backend/internal/ingest) | The normalizer: syslog and JSON parsing, and the mappings for each source. |
| [`backend/internal/store/`](backend/internal/store) | Postgres: migrations, sqlc queries, search, and tenant-scoped transactions. |
| [`backend/internal/api/`](backend/internal/api) | The HTTP handlers. Sign-in is in `auth/`, and the RBAC policies are in `authz/`. |
| [`backend/internal/alerting/`](backend/internal/alerting) | The alert rule evaluator and webhook delivery. |
| [`frontend/`](frontend) | The React UI, built into the Caddy image that terminates TLS and passes `/api` to the backend. |
| [`ingest/`](ingest) | The Vector collector's config: syslog on port 514, and files from `inbox/`. |
| [`inbox/`](inbox) | Drop `.ndjson` files here for the collector to read. |
| [`monitoring/`](monitoring) | Prometheus and Grafana config for the `monitoring` profile. |
| [`samples/`](samples) | Sample logs, the sender scripts, and the two-company simulator. |
| [`tests/`](tests) | The acceptance checks. Unit and integration tests sit next to the code they test. |
| [`docs/`](docs) | Architecture, setup guides, ADRs, the requirements, and the Postman collection. |

## Development

The Go targets need Go 1.26 on the host, and the frontend targets need Node 24.

```sh
make dev-up     # the stack, with Postgres on 127.0.0.1:5432 and the API on 127.0.0.1:8080
make dev-deps   # or only Postgres, for `make dev-api` to run the API on the host
make dev-web    # the UI on http://localhost:5173, with hot reload
make test       # the Go and frontend tests, without the database tests
make test-db    # the Go tests, database tests included, against the dev Postgres
make lint       # gofmt, go vet, generated-code drift, the spec, the Vector and Caddy config, and the frontend
```

The Go server interface and the frontend's types are generated from `api/openapi.yaml`, and the query code from the SQL in `backend/internal/store/queries/`. After changing either, run `make gen-api` or `make gen-sql` and commit what it generates. `make lint` fails while the generated code is stale.

CI runs `make lint`, `make test-db` and `make test-web`. It then starts a fresh stack, runs the acceptance checks against it, and checks that the backend stored everything the simulator sent ([tests/README.md](tests/README.md)).

## Docs

- [Architecture](docs/architecture.md): Components, data flow, normalization, the tenant model, auth, alerting and metrics
- [Architecture decision records](docs/adr/README.md): Why each technology was chosen
- [Appliance setup](docs/setup_appliance.md)
- [SaaS setup](docs/setup_saas.md)
- [Collector: syslog and the inbox](ingest/README.md)
- [UI and edge](frontend/README.md)
- [Tests and acceptance checks](tests/README.md)
- API: the [OpenAPI spec](api/openapi.yaml), served as a reference at `/api/docs`, and a [Postman collection](docs/postman_collection.json)
- [Requirements](docs/requirements.md), with [what is done](docs/requirements.md#13-requirement-status)
