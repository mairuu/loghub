# Log Management System — Requirements

## 1. Assignment Overview

Develop a demo **log management system** that:

- Supports multiple data sources.
- Can be deployed as a **hardware appliance** on a single machine/VM.
- Can be deployed as **SaaS/Cloud** with a publicly accessible URL for external testing.

### Constraints

- **Work format:** Individual.
- **Technology:** Candidate may choose the technology stack. Open-source and cloud-native technologies are recommended.
- **Submission/demo:** Within 10 days after viewing the assignment. The student is responsible for scheduling the demo.

---

## 2. Objectives

The system should demonstrate:

1. Full-stack development capabilities:
   - Architecture design
   - Backend/API
   - Frontend/UI
   - Data pipelines
   - DevOps/deployment
2. Ability to handle event/log data from multiple sources:
   - Ingestion
   - Normalization
   - Indexing
   - Search
   - Visualization
   - Alerting
3. Understanding of security:
   - Authentication (AuthN)
   - Authorization (AuthZ)
   - TLS
   - Multi-tenant customer data isolation

---

## 3. Functional Requirements

### 3.1 Data Sources

The system MUST support **at least four real, working data sources**. The remaining sources MAY use sample data or simulators.

Supported source categories include:

| Source | Example |
|---|---|
| Firewall / Network | Syslog UDP/TCP 514 or HTTP ingest |
| API | JSON via REST/HTTP POST |
| CrowdStrike | Synthetic JSON/CSV is permitted |
| AWS | CloudTrail / ALB / NLB; sample files permitted |
| Microsoft 365 | Unified Audit Log; sample JSON permitted |
| Microsoft AD / Windows Security | Event IDs 4624/4625; samples permitted |

#### Data-source requirement

At least four sources MUST actually be capable of sending data into the system, for example:

- Syslog
- HTTP API
- JSON batch file
- Simulator script

All ingested sources MUST be normalized into a centralized schema before storage.

### 3.2 Ingestion

The system MUST:

- Accept multiple log formats.
- Support:
  - Syslog
  - HTTP JSON
  - File batch
- Support at least **two protocols**.

### 3.3 Normalization

Every supported log type MUST be converted into a common normalized schema before storage.

The schema MAY be extended or adjusted, but it MUST cover the key fields required for search.

### 3.4 Storage and Query

The system MUST:

- Store normalized log data in a searchable datastore.
- Support searching/filtering the stored events.

Possible technologies include:

- OpenSearch
- ClickHouse
- PostgreSQL + GIN
- Elasticsearch

### 3.5 Dashboard

The UI MUST provide:

- Top IP
- Top User
- Top Event Type
- Timeline visualization
- Time-range filtering
- Tenant filtering

### 3.6 Alerting

The system MUST provide at least **one alert rule**, for example:

> Repeated failed logins from the same IP within five minutes.

When an alert condition is triggered, the system MUST expose the alert through at least one of:

- Alert page in the UI
- Webhook
- Email

### 3.7 Authentication and Authorization

The system MUST:

- Provide authentication.
- Provide authorization.
- Support at least two roles:
  - `Admin`
  - `Viewer`
- Support tenant data isolation.

Tenant identity MAY be supplied through:

- Request parameter
- Request header
- Authentication claim

A Viewer MUST only be able to see data belonging to their own tenant.

### 3.8 Deployment

The system MUST support two deployment modes.

#### Appliance Mode

The application MUST be runnable on either:

- A single physical machine, or
- A single VM

Docker Compose is recommended.

#### SaaS / Cloud Mode

The application MUST be deployed to a public cloud environment, such as:

- VM
- Container platform

The deployment MUST provide a URL accessible to reviewers.

### 3.9 TLS

HTTPS MUST be enabled at least for the SaaS deployment.

A self-signed certificate is acceptable if the setup is clearly documented.

### 3.10 Data Retention

The system MUST retain data for at least **seven days**.

Possible implementation strategies include:

- Deletion policies
- Rollover
- Partitioning

---

## 4. Common Event Schema

The normalized event schema SHOULD cover the following fields:

| Field | Description |
|---|---|
| `@timestamp` | Event timestamp in RFC 3339 format |
| `tenant` | Tenant identifier |
| `source` | Source category |
| `vendor` | Vendor |
| `product` | Product |
| `event_type` | Event type |
| `event_subtype` | Event subtype |
| `severity` | Severity from 0–10 |
| `action` | Event action |
| `src_ip` | Source IP |
| `src_port` | Source port |
| `dst_ip` | Destination IP |
| `dst_port` | Destination port |
| `protocol` | Network protocol |
| `user` | User associated with the event |
| `host` | Host associated with the event |
| `process` | Process associated with the event |
| `url` | URL |
| `http_method` | HTTP method |
| `status_code` | HTTP status code |
| `rule_name` | Rule name |
| `rule_id` | Rule identifier |
| `cloud.account_id` | Cloud account identifier |
| `cloud.region` | Cloud region |
| `cloud.service` | Cloud service |
| `raw` | Original event/message |
| `_tags` | Array of tags |

**Allowed `source` examples:** `firewall`, `crowdstrike`, `aws`, `m365`, `ad`, `api`, `network`.

**Allowed `action` examples:** `allow`, `deny`, `create`, `delete`, `login`, `logout`, `alert`.

---

## 5. Sample Input Data

The following examples MAY be used as seed data or submitted through the API.

### 5.1 Firewall / Syslog

```text
<134>Aug 20 12:44:56 fw01 vendor=demo product=ngfw action=deny src=10.0.1.10 dst=8.8.8.8 spt=5353 dpt=53 proto=udp msg=DNS blocked policy=Block-DNS
```

### 5.2 Network Router Syslog

```text
<190>Aug 20 13:01:02 r1 if=ge-0/0/1 event=link-down mac=aa:bb:cc:dd:ee:ff reason=carrier-loss
```

### 5.3 HTTP API

`POST /ingest`

```json
{
  "tenant": "demoA",
  "source": "api",
  "event_type": "app_login_failed",
  "user": "alice",
  "ip": "203.0.113.7",
  "reason": "wrong_password",
  "@timestamp": "2025-08-20T07:20:00Z"
}
```

### 5.4 CrowdStrike

```json
{
  "tenant": "demoA",
  "source": "crowdstrike",
  "event_type": "malware_detected",
  "host": "WIN10-01",
  "process": "powershell.exe",
  "severity": 8,
  "sha256": "abc...",
  "action": "quarantine",
  "@timestamp": "2025-08-20T08:00:00Z"
}
```

### 5.5 AWS CloudTrail

```json
{
  "tenant": "demoB",
  "source": "aws",
  "cloud": {
    "service": "iam",
    "account_id": "123456789012",
    "region": "ap-southeast-1"
  },
  "event_type": "CreateUser",
  "user": "admin",
  "@timestamp": "2025-08-20T09:10:00Z",
  "raw": {
    "eventName": "CreateUser",
    "requestParameters": {
      "userName": "temp-user"
    }
  }
}
```

### 5.6 Microsoft 365 Audit

```json
{
  "tenant": "demoB",
  "source": "m365",
  "event_type": "UserLoggedIn",
  "user": "bob@demo.local",
  "ip": "198.51.100.23",
  "status": "Success",
  "workload": "Exchange",
  "@timestamp": "2025-08-20T10:05:00Z"
}
```

### 5.7 Microsoft AD / Windows Security

Example: Event ID `4625`

```json
{
  "tenant": "demoA",
  "source": "ad",
  "event_id": 4625,
  "event_type": "LogonFailed",
  "user": "demo\\eve",
  "host": "DC01",
  "ip": "203.0.113.77",
  "logon_type": 3,
  "@timestamp": "2025-08-20T11:11:11Z"
}
```

---

## 6. Recommended Architecture

The following components are recommended but MAY be replaced.

| Layer | Possible implementations |
|---|---|
| Collector / Ingest | Vector, Fluent Bit, Logstash, or a custom Node.js / Go / Python implementation |
| Storage / Index | OpenSearch, ClickHouse, PostgreSQL + JSONB/GIN, Elasticsearch |
| Backend API | FastAPI, Express, Go Fiber, NestJS — should include authentication and an ingest endpoint |
| UI / Dashboard | React or Vue with a chart library, OpenSearch Dashboards, Grafana |
| Packaging / Deployment | Docker Compose for Appliance mode; cloud VM/container for SaaS mode |

---

## 7. Minimum Appliance Environment

The appliance should support at least:

- Ubuntu 22.04+
- 4 vCPU
- 8 GB RAM
- 40 GB disk

Required ports may include `80`, `443` and `514`.

---

## 8. Repository and Deliverables

The Git repository MUST contain:

```text
/docs/
  architecture.md
  setup_appliance.md
  setup_saas.md

docker-compose.yml       # and/or Helm chart
.env.example
Makefile                 # or run.sh

/samples/
  sample log files
  sender scripts
  send_syslog.sh
  post_logs.py

/backend/
/frontend/
/ingest/

/tests/
  example test cases
```

### Required documentation

- **`/docs/architecture.md`** — architecture diagram, architecture explanation, data flow, tenant model.
- **`/docs/setup_appliance.md`** — detailed Appliance installation/setup instructions.
- **`/docs/setup_saas.md`** — detailed SaaS installation/deployment instructions.

### Required scripts/configuration

The repository MUST provide:

- Docker Compose and/or Helm configuration
- Initialization/seed scripts
- `.env.example`
- Makefile or `run.sh`
- Sample log files
- Sample sender scripts

### Tests

The repository MUST contain at least **2–3 example test cases**.

---

## 9. Demo Requirements

- **Demo video:** a **30-minute** video covering:
  1. Architecture
  2. Ingestion
  3. Search
  4. Dashboard
  5. Alerting
- **SaaS demo:** a publicly accessible SaaS demo URL.
- **Appliance demo:** either an OVA file or instructions for spinning up the appliance.
- **API collection:** a Postman or Insomnia collection for the ingest/search APIs, where applicable.

---

## 10. Acceptance Criteria

The implementation is considered acceptable when all of the following can be demonstrated.

| Area | Demonstration |
|---|---|
| Appliance startup | The system starts in Appliance mode by following the documentation, with one command or only a few steps. |
| Syslog ingestion | A sample syslog message sent with `logger` or `nc` appears in the UI within one minute. |
| HTTP API ingestion | `POST /ingest` with the sample JSON succeeds, and the record can be found by search. |
| File-based sources | Sample AWS/M365/AD files are uploaded or pointed to, and the data is normalized. |
| Dashboard | Top N, timeline, tenant filter, source filter and time filter all work. |
| Alerting | A sample alert rule is created, and a notification is observed in the UI, email or a webhook. |
| RBAC | A Viewer can see only their own tenant. |
| SaaS | The SaaS deployment is reachable over HTTPS. |

---

## 11. Nice-to-Have Requirements

The following features are optional:

- True multi-tenancy using separate indexes/tables
- Field-level or tenant-level RBAC
- Ingestion-time enrichment: reverse DNS, GeoIP
- CI/CD: GitHub Actions or a similar system
- Infrastructure as Code: Terraform, Helm
- Basic unit tests
- Basic integration tests

---

## 12. Evaluation Criteria

| Category | Evaluation | Points |
|---|---|---:|
| Architecture & Documentation | Clarity, design, and rationale for technology choices | 15 |
| Ingestion | Multiple sources/protocols, stability, and simulation | 20 |
| Normalization / Schema | Appropriate schema and mapping design | 10 |
| Storage & Query | Fast/accurate search and good indexing/partitioning | 10 |
| Dashboard / UI | Ease of use, required charts/tables/filters | 10 |
| Alerting | At least one rule with successful notification | 10 |
| Security | AuthN/AuthZ, RBAC, and TLS | 10 |
| Deployment | Working Appliance + SaaS deployments | 10 |
| Tests & Developer Experience | Test examples, scripts/Makefile, `.env.example` | 5 |
| **Total** | | **100** |

### Bonus

Up to **+10 points** for: true multi-tenancy, enrichment, CI/CD, Infrastructure as Code, observability (metrics, traces), hardening.

### Passing Score

- **Minimum passing score:** 60/100.
- **Strong Hire consideration:** 85/100 or higher, with the ability to clearly explain the design rationale.

---

## 13. Requirement Status

Status as of 2026-09-18.

### Must have

- [x] At least 4 working data sources — syslog (UDP/TCP 514), `POST /ingest`, NDJSON files dropped in `inbox/`, and file upload in the UI (`POST /api/v1/ingest/file`); `samples/` covers firewall, network, api, crowdstrike, aws, m365 and ad
- [x] Multiple log formats — syslog RFC 3164/5424 with key=value bodies, JSON, NDJSON
- [x] At least 2 protocols — syslog over UDP/TCP, HTTPS
- [x] Centralized normalization schema — `backend/internal/ingest`
- [x] Searchable storage — PostgreSQL 18 with daily partitions; `GET /api/v1/events` and the Search page
- [x] Dashboard — `frontend/src/pages/DashboardPage.tsx`
- [x] Top IP/User/Event Type — plus top hosts
- [x] Timeline
- [x] Time and tenant filters — source filter too
- [x] At least 1 alert rule — failed logins per `src_ip` in 5 minutes (`samples/alert_rule.json`)
- [x] Alert notification — Alerts page in the UI and webhook delivery
- [x] Authentication — JWT sign-in, plus an ingest token for the collector
- [x] Admin/Viewer roles
- [x] Tenant isolation — policies plus PostgreSQL row-level security
- [x] Appliance deployment — `make up` (Docker Compose)
- [ ] SaaS/Cloud deployment — the same Compose stack is meant to run on a cloud VM (ADR 0011), but nothing is deployed yet
- [ ] Public SaaS URL
- [ ] HTTPS for SaaS — Caddy gets Let's Encrypt certificates for a public `SITE_ADDRESS`; not yet shown on a live deployment
- [x] At least 7-day data retention — daily partitions dropped after 7 days, run hourly
- [ ] Architecture documentation — diagram, components and data flow are done; there is no dedicated tenant model section yet
- [x] Appliance setup documentation — `docs/setup_appliance.md`
- [ ] SaaS setup documentation — `docs/setup_saas.md` is a placeholder
- [x] Docker Compose and/or Helm — Compose only
- [x] Sample data and sender scripts — `samples/send_syslog.sh`, `samples/post_logs.py`, `make send-samples`
- [x] Initialization/seed scripts, `.env.example`, Makefile — `loghub seed`, `make env`
- [x] Backend/frontend/ingest source code — `backend/`, `frontend/`, `ingest/`
- [x] 2–3 example tests — Go and Vitest tests sit next to the code rather than in `/tests/`
- [ ] 30-minute demo video
- [x] API collection — `docs/postman_collection.json`, generated from `api/openapi.yaml`

### Nice to have

- [ ] Separate indexes/tables per tenant — one partitioned table shared by all tenants, isolated with row-level security (ADR 0003)
- [x] Field/tenant-level RBAC — tenant-scoped policies; viewers don't see rules' webhook URLs
- [ ] Reverse DNS
- [ ] GeoIP
- [ ] CI/CD — no pipeline; `make lint` and `make test` run locally
- [ ] Terraform
- [ ] Helm — ruled out in ADR 0011
- [x] Unit tests
- [x] Integration tests — database tests (`make test-db`), API end-to-end tests, Vector config tests (`make check-vector`)
- [ ] Metrics
- [ ] Traces
- [x] Hardening — least-privilege database role, secrets as files for Vector, Caddy with CSP/HSTS headers and dropped capabilities, constant-time token comparison
