# Log Management System --- Requirements

## 1. Assignment Overview

Develop a demo **log management system** that:

-   Supports multiple data sources.
-   Can be deployed as a **hardware appliance** on a single machine/VM.
-   Can be deployed as **SaaS/Cloud** with a publicly accessible URL for
    external testing.

### Constraints

-   **Work format:** Individual
-   **Technology:** Candidate may choose the technology stack.
-   Open-source and cloud-native technologies are recommended.
-   **Submission/demo:** Within 10 days after viewing the assignment.
    The student is responsible for scheduling the demo.

------------------------------------------------------------------------

## 2. Objectives

The system should demonstrate:

1.  Full-stack development capabilities:
    -   Architecture design
    -   Backend/API
    -   Frontend/UI
    -   Data pipelines
    -   DevOps/deployment
2.  Ability to handle event/log data from multiple sources:
    -   Ingestion
    -   Normalization
    -   Indexing
    -   Search
    -   Visualization
    -   Alerting
3.  Understanding of security:
    -   Authentication (AuthN)
    -   Authorization (AuthZ)
    -   TLS
    -   Multi-tenant customer data isolation

------------------------------------------------------------------------

## 3. Functional Requirements

### 3.1 Data Sources

The system MUST support **at least four real, working data sources**.

The remaining sources MAY use sample data or simulators.

Supported source categories include:

  -----------------------------------------------------------------------
  Source                              Example
  ----------------------------------- -----------------------------------
  Firewall / Network                  Syslog UDP/TCP 514 or HTTP ingest

  API                                 JSON via REST/HTTP POST

  CrowdStrike                         Synthetic JSON/CSV is permitted

  AWS                                 CloudTrail / ALB / NLB; sample
                                      files permitted

  Microsoft 365                       Unified Audit Log; sample JSON
                                      permitted

  Microsoft AD / Windows Security     Event IDs 4624/4625; samples
                                      permitted
  -----------------------------------------------------------------------

#### Data-source requirement

At least four sources MUST actually be capable of sending data into the
system.

For example:

-   Syslog
-   HTTP API
-   JSON batch file
-   Simulator script

All ingested sources MUST be normalized into a centralized schema before
storage.

------------------------------------------------------------------------

### 3.2 Ingestion

The system MUST:

-   Accept multiple log formats.
-   Support:
    -   Syslog
    -   HTTP JSON
    -   File batch
-   Support at least **two protocols**.

------------------------------------------------------------------------

### 3.3 Normalization

Every supported log type MUST be converted into a common normalized
schema before storage.

The schema MAY be extended or adjusted, but it MUST cover the key fields
required for search.

------------------------------------------------------------------------

### 3.4 Storage and Query

The system MUST:

-   Store normalized log data in a searchable datastore.
-   Support searching/filtering the stored events.

Possible technologies include:

-   OpenSearch
-   ClickHouse
-   PostgreSQL + GIN
-   Elasticsearch

------------------------------------------------------------------------

### 3.5 Dashboard

The UI MUST provide:

-   Top IP
-   Top User
-   Top Event Type
-   Timeline visualization
-   Time-range filtering
-   Tenant filtering

------------------------------------------------------------------------

### 3.6 Alerting

The system MUST provide at least **one alert rule**.

Example:

> Repeated failed logins from the same IP within five minutes.

When an alert condition is triggered, the system MUST expose the alert
through at least one of:

-   Alert page in the UI
-   Webhook
-   Email

------------------------------------------------------------------------

### 3.7 Authentication and Authorization

The system MUST:

-   Provide authentication.
-   Provide authorization.
-   Support at least two roles:
    -   `Admin`
    -   `Viewer`
-   Support tenant data isolation.

Tenant identity MAY be supplied through:

-   Request parameter
-   Request header
-   Authentication claim

A Viewer MUST only be able to see data belonging to their own tenant.

------------------------------------------------------------------------

### 3.8 Deployment

The system MUST support two deployment modes.

#### Appliance Mode

The application MUST be runnable on:

-   A single physical machine, or
-   A single VM

Docker Compose is recommended.

#### SaaS / Cloud Mode

The application MUST be deployed to a public cloud environment, such as:

-   VM
-   Container platform

The deployment MUST provide a URL accessible to reviewers.

------------------------------------------------------------------------

### 3.9 TLS

HTTPS MUST be enabled at least for the SaaS deployment.

A self-signed certificate is acceptable if the setup is clearly
documented.

------------------------------------------------------------------------

### 3.10 Data Retention

The system MUST retain data for at least **seven days**.

Possible implementation strategies include:

-   Deletion policies
-   Rollover
-   Partitioning

------------------------------------------------------------------------

## 4. Common Event Schema

The normalized event schema SHOULD cover the following fields:

  Field                Description
  -------------------- -----------------------------------
  `@timestamp`         Event timestamp in RFC3339 format
  `tenant`             Tenant identifier
  `source`             Source category
  `vendor`             Vendor
  `product`            Product
  `event_type`         Event type
  `event_subtype`      Event subtype
  `severity`           Severity from 0--10
  `action`             Event action
  `src_ip`             Source IP
  `src_port`           Source port
  `dst_ip`             Destination IP
  `dst_port`           Destination port
  `protocol`           Network protocol
  `user`               User associated with the event
  `host`               Host associated with the event
  `process`            Process associated with the event
  `url`                URL
  `http_method`        HTTP method
  `status_code`        HTTP status code
  `rule_name`          Rule name
  `rule_id`            Rule identifier
  `cloud.account_id`   Cloud account identifier
  `cloud.region`       Cloud region
  `cloud.service`      Cloud service
  `raw`                Original event/message
  `_tags`              Array of tags

### Allowed `source` examples

-   `firewall`
-   `crowdstrike`
-   `aws`
-   `m365`
-   `ad`
-   `api`
-   `network`

### Allowed `action` examples

-   `allow`
-   `deny`
-   `create`
-   `delete`
-   `login`
-   `logout`
-   `alert`

------------------------------------------------------------------------

## 5. Sample Input Data

The following examples MAY be used as seed data or submitted through the
API.

### 5.1 Firewall / Syslog

``` text
<134>Aug 20 12:44:56 fw01 vendor=demo product=ngfw action=deny src=10.0.1.10 dst=8.8.8.8 spt=5353 dpt=53 proto=udp msg=DNS blocked policy=Block-DNS
```

### 5.2 Network Router Syslog

``` text
<190>Aug 20 13:01:02 r1 if=ge-0/0/1 event=link-down mac=aa:bb:cc:dd:ee:ff reason=carrier-loss
```

### 5.3 HTTP API

`POST /ingest`

``` json
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

``` json
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

``` json
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

``` json
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

``` json
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

------------------------------------------------------------------------

## 6. Recommended Architecture

The following components are recommended but MAY be replaced.

### Collector / Ingest

Possible implementations:

-   Vector
-   Fluent Bit
-   Logstash
-   Custom Node.js implementation
-   Custom Go implementation
-   Custom Python implementation

### Storage / Index

Possible implementations:

-   OpenSearch
-   ClickHouse
-   PostgreSQL + JSONB/GIN
-   Elasticsearch

### Backend API

Possible implementations:

-   FastAPI
-   Express
-   Go Fiber
-   NestJS

The backend should include:

-   Authentication
-   Ingest endpoint

### UI / Dashboard

Possible implementations:

-   React + chart library
-   Vue + chart library
-   OpenSearch Dashboards
-   Grafana

### Packaging / Deployment

-   Docker Compose for Appliance mode
-   Cloud VM/container for SaaS mode

------------------------------------------------------------------------

## 7. Minimum Appliance Environment

The appliance should support at least:

-   Ubuntu 22.04+
-   4 vCPU
-   8 GB RAM
-   40 GB disk

Required ports may include:

-   `80`
-   `443`
-   `514`

------------------------------------------------------------------------

## 8. Repository and Deliverables

The Git repository MUST contain:

``` text
/docs/
  architecture.md
  setup_appliance.md
  setup_saas.md

docker-compose.yml
# and/or Helm chart

.env.example
Makefile
# or run.sh

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

#### `/docs/architecture.md`

Must contain:

-   Architecture diagram
-   Architecture explanation
-   Data flow
-   Tenant model

#### `/docs/setup_appliance.md`

Must contain detailed Appliance installation/setup instructions.

#### `/docs/setup_saas.md`

Must contain detailed SaaS installation/deployment instructions.

### Required scripts/configuration

The repository MUST provide:

-   Docker Compose and/or Helm configuration
-   Initialization/seed scripts
-   `.env.example`
-   Makefile or `run.sh`
-   Sample log files
-   Sample sender scripts

### Tests

The repository MUST contain at least **2--3 example test cases**.

------------------------------------------------------------------------

## 9. Demo Requirements

### Demo Video

Provide a **30-minute demo video** covering:

1.  Architecture
2.  Ingestion
3.  Search
4.  Dashboard
5.  Alerting

### SaaS Demo

Provide:

-   A publicly accessible SaaS demo URL

### Appliance Demo

Provide either:

-   An OVA file, or
-   Instructions for spinning up the appliance

### API Collection

Provide a:

-   Postman collection, or
-   Insomnia collection

for ingest/search APIs, where applicable.

------------------------------------------------------------------------

## 10. Acceptance Criteria

The implementation is considered acceptable when all of the following
can be demonstrated.

### Appliance Startup

-   The system starts in Appliance mode by following the documentation.
-   Startup requires one command or only a few steps.

### Syslog Ingestion

-   Send a sample Syslog message using tools such as `logger` or `nc`.
-   The message appears in the UI within one minute.

### HTTP API Ingestion

-   Call `POST /ingest` with the sample JSON.
-   Successfully search for the resulting record.

### File-based Sources

-   Upload or point the system to sample AWS/M365/AD files.
-   Verify that the data is normalized.

### Dashboard

Verify that the following work:

-   Top N
-   Timeline
-   Tenant filter
-   Source filter
-   Time filter

### Alerting

-   Create a sample alert rule.
-   Observe a notification in:
    -   UI, or
    -   Email, or
    -   Webhook

### RBAC

-   Verify that a Viewer can see only their own tenant.

### SaaS

-   Access the SaaS deployment over HTTPS.

------------------------------------------------------------------------

## 11. Nice-to-Have Requirements

The following features are optional:

-   True multi-tenancy using separate indexes/tables
-   Field-level or tenant-level RBAC
-   Ingestion-time enrichment:
    -   Reverse DNS
    -   GeoIP
-   CI/CD:
    -   GitHub Actions
    -   Similar CI/CD system
-   Infrastructure as Code:
    -   Terraform
    -   Helm
-   Basic unit tests
-   Basic integration tests

------------------------------------------------------------------------

## 12. Evaluation Criteria

  --------------------------------------------------------------------------
  Category              Evaluation                                    Points
  --------------------- ----------------------- ----------------------------
  Architecture &        Clarity, design, and                              15
  Documentation         rationale for           
                        technology choices      

  Ingestion             Multiple                                          20
                        sources/protocols,      
                        stability, and          
                        simulation              

  Normalization /       Appropriate schema and                            10
  Schema                mapping design          

  Storage & Query       Fast/accurate search                              10
                        and good                
                        indexing/partitioning   

  Dashboard / UI        Ease of use, required                             10
                        charts/tables/filters   

  Alerting              At least one rule with                            10
                        successful notification 

  Security              AuthN/AuthZ, RBAC, and                            10
                        TLS                     

  Deployment            Working Appliance +                               10
                        SaaS deployments        

  Tests & Developer     Test examples,                                     5
  Experience            scripts/Makefile,       
                        `.env.example`          

  **Total**                                                          **100**
  --------------------------------------------------------------------------

### Bonus

Maximum bonus: **+10 points**

Possible bonus areas:

-   True multi-tenancy
-   Enrichment
-   CI/CD
-   Infrastructure as Code
-   Observability:
    -   Metrics
    -   Traces
-   Hardening

### Passing Score

-   **Minimum passing score:** 60/100
-   **Strong Hire consideration:** 85/100 or higher, with the ability to
    clearly explain the design rationale.

------------------------------------------------------------------------

## 13. Requirement Priority Summary

### MUST HAVE

-   [ ] At least 4 working data sources
-   [ ] Multiple log formats
-   [ ] At least 2 protocols
-   [ ] Centralized normalization schema
-   [ ] Searchable storage
-   [ ] Dashboard
-   [ ] Top IP/User/Event Type
-   [ ] Timeline
-   [ ] Time and tenant filters
-   [ ] At least 1 alert rule
-   [ ] Alert notification
-   [ ] Authentication
-   [ ] Admin/Viewer roles
-   [ ] Tenant isolation
-   [ ] Appliance deployment
-   [ ] SaaS/Cloud deployment
-   [ ] Public SaaS URL
-   [ ] HTTPS for SaaS
-   [ ] At least 7-day data retention
-   [ ] Architecture documentation
-   [ ] Appliance setup documentation
-   [ ] SaaS setup documentation
-   [ ] Docker Compose and/or Helm
-   [ ] Sample data and sender scripts
-   [ ] Backend/frontend/ingest source code
-   [ ] 2--3 example tests
-   [ ] 30-minute demo video
-   [ ] API collection where applicable

### NICE TO HAVE

-   [ ] Separate indexes/tables per tenant
-   [ ] Field/tenant-level RBAC
-   [ ] Reverse DNS
-   [ ] GeoIP
-   [ ] CI/CD
-   [ ] Terraform
-   [ ] Helm
-   [ ] Unit tests
-   [ ] Integration tests
-   [ ] Metrics
-   [ ] Traces
-   [ ] Hardening
