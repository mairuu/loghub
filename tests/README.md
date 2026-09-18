# Tests

The unit and integration tests sit next to the code they test, as Go and Vitest expect. This folder holds the acceptance checks, which run against a deployed loghub.

## Acceptance checks

`tests/acceptance.py` checks a running loghub, an appliance or a SaaS deployment, against the acceptance criteria in [requirements §10](../docs/requirements.md#10-acceptance-criteria). It needs only Python 3.10 or later.

On the appliance, after `make up`:

```sh
make ca           # once, so that the script trusts Caddy's certificate for localhost
make acceptance
```

From any other machine, in a clone where the four credential lines from the operator are saved as `.env` ([setup_saas.md](../docs/setup_saas.md#what-reviewers-can-do-from-their-own-machines)):

```sh
tests/acceptance.py --url https://loghub.example.com
```

It prints one line per check and exits with 1 if any fails. A run takes one to three minutes, most of it spent waiting for the alert evaluator:

```text
Checking https://loghub.example.com. This run's events are tagged acceptance-20260918-173852-ab08.
ok    HTTPS: /api/healthz is ok over a trusted certificate, and http:// redirects to https:// (308)
ok    Sign-in: admin@loghub.local as admin, and viewer@demoa.local and viewer@demob.local as their tenants' viewers
...
9 passed, 0 failed
```

| Check | §10 criterion | What it does |
|---|---|---|
| HTTPS | SaaS | `GET /api/healthz` over a verified certificate, and a redirect from `http://` to `https://` |
| Sign-in | RBAC | signs in as the admin and as both viewers |
| HTTP API ingestion | HTTP API ingestion | `POST /ingest` with `samples/json/api.json`, then finds it by search, normalized as a failed login |
| File upload | File-based sources | uploads the AWS, M365 and AD samples to `POST /api/v1/ingest/file`, and checks each one's vendor, product and action |
| Alert rule | Alerting | finds a rule in demoA that counts what `samples/alert_rule.json` does, or creates one from it, and sends five failed logins from an address no earlier run used |
| Syslog ingestion | Syslog ingestion | sends a firewall line to port 514 over UDP and another over TCP, and searches for each for up to a minute |
| RBAC | RBAC | each viewer sees only their own tenant's events and gets 403 for the other tenant, a viewer can't send events, and a search without a token gets 401 |
| Dashboard | Dashboard | top source IPs and the timeline agree with search for every tenant, one tenant, one source and a time range |
| Alerting | Alerting | the alert for that address appears for demoA's viewer within three minutes, and not for demoB's |

Each run leaves eleven events in demoA and demoB and raises one alert. The events carry the run's ID as a tag, which the Search page can filter on under *More filters*, or, for the two syslog lines, in the message. Retention deletes the events after seven days. The first run on a deployment also creates the sample alert rule if demoA has none like it, and rules can't be removed through the API.

The checks don't cover appliance startup, which is `make up` and comes first, or webhook delivery. They check the API that the UI's pages read, not the pages themselves.

## Unit and integration tests

| Command | Runs |
|---|---|
| `make test` | the Go tests except those that need a database, and the UI's Vitest tests |
| `make test-db` | the Go tests including the database ones, against the dev Postgres as `loghub_app`, so row-level security applies |
| `make check-vector` | the collector's configuration tests in `ingest/vector.test.yaml` |

These are good examples to start with:

- **Normalization:** `TestNormalizeSamples` in `backend/internal/ingest/normalize_test.go` normalizes every file in `samples/` and compares it with the event expected.
- **Tenant isolation:** `TestTenantIsolation` in `backend/internal/store/events_test.go` stores events for two tenants, then reads them back through row-level security as one tenant, as the admin and with no context at all, which sees nothing.
- **Authorization:** `TestCan` and `TestScopes` in `backend/internal/authz/authz_test.go` check the policy set's decisions.
- **End to end:** `TestSamplesEndToEnd`, `TestAlertingEndToEnd` and `TestRBACEndToEnd` in `backend/internal/api/e2e_test.go` are in-process versions of the acceptance checks, run through the HTTP API against a real database. The alerting one also receives the webhook.
- **UI:** `frontend/src/pages/*.test.tsx` render each page against a fake API.
