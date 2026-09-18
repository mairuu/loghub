# 0011. One Compose file for both deployment modes

- Status: Accepted
- Date: 2026-09-18

## Context
Requirement 3.8 needs the same system to run as an appliance on a single machine or VM and as a SaaS deployment on a public cloud, at a URL reviewers can reach, and requirement 3.9 needs HTTPS for the latter. The acceptance criteria say appliance startup is one command or a few steps. The two modes differ in almost nothing: the appliance answers on `localhost` or a LAN address with a certificate from Caddy's own CA, the SaaS deployment answers on a public DNS name with one from Let's Encrypt. ADR 0005 already made that difference a single `SITE_ADDRESS` value.

Maintaining two Compose files, or a Compose file and a Helm chart, would mean the mode a reviewer tries is not the mode that was tested. A managed container platform would split the stack further: Cloud Run, for one, accepts only HTTP, so syslog over UDP would need another way in, and Postgres would become a managed service with setup of its own.

## Decision

### The stack
- One `docker-compose.yml` describes both modes. `.env` is the only thing that differs, and `docker-compose.dev.yml` is an override for development that publishes Postgres on 5432 and the backend on 8080, both on `127.0.0.1` — ports neither deployment mode exposes.
- `make up` is the single command. It generates `.env` from `.env.example` with random secrets if none exists, builds the images, and waits with `--wait` until every service is healthy.
- Startup order is expressed as Compose conditions rather than as retries in the application: Postgres healthy → `migrate` completed → `seed` completed → `backend` healthy → Caddy and Vector. `migrate` and `seed` are one-shot services running the same image as the backend (ADR 0001), so no extra tooling is needed in the stack.
- Only `migrate` and `seed` connect as the owner role. The backend connects as `loghub_app` (ADR 0003). The Compose file carries this split as an anchor, so it is visible in one place.
- Published ports are Caddy's 80 and 443 and Vector's 514 over UDP and TCP (ADR 0004, ADR 0005), matching the port list in requirement §7. Nothing else is reachable from outside the Compose network.
- Images are built from source on the host rather than pulled from a registry. A clone and one command is the whole install, with no account or registry to configure.
- State lives in named volumes: `postgres_data`, `caddy_data` for certificates and the internal CA, `caddy_config`, and `vector_data` for Vector's disk buffer. `make reset` deletes them and asks first.
- Every long-running service restarts unless it was stopped, so the stack comes back with Docker after a reboot. Compose's start order doesn't apply then: the backend exits if it can't reach Postgres, and restarting is how it waits.

### SaaS mode
- SaaS mode is the same stack on one cloud VM, from any provider, with `SITE_ADDRESS` set to a public DNS name that points at the VM. That name is what switches Caddy from its internal CA to Let's Encrypt, so HTTPS needs no step of its own.
- The provider's firewall decides what is reachable, not a firewall on the VM: Docker's published ports bypass ufw. It admits 80 and 443 from anywhere, since Let's Encrypt validates over them, 514 from anywhere so reviewers can send syslog, and SSH from the operator only.
- Syslog has no authentication (ADR 0005), so on a public address anyone can write events into `SYSLOG_DEFAULT_TENANT`. This is accepted for the demo: that tenant holds demo data, and retention drops it after seven days. The firewall rule for 514 is where to narrow it.
- Secrets are generated on the VM by `make up`, as on an appliance. The operator sends reviewers the URL and the demo accounts' credentials directly, and they are never committed.
- Deploying and upgrading is `git pull` and `make up` over SSH. There is no deploy pipeline, no registry and no infrastructure code.
- No OVA is built. The appliance demo is the instructions in `docs/setup_appliance.md`, which requirement §9 allows. An OVA would be an export of a VM that has already been through `make up`, not a separate build.

## Consequences
- What a reviewer runs on the appliance is what runs in the cloud, down to the image. The VM needs nothing an appliance doesn't, so any provider works.
- Building on each host costs a few minutes per install and requires the build toolchain in the image layers. Pulling prebuilt images would be faster and is the change to make if installs become frequent.
- Secrets are generated per install and never leave the machine. There is no shared default password, and `make env` never overwrites an existing `.env`.
- Let's Encrypt needs the DNS name to point at the VM, and ports 80 and 443 to be open, before Caddy can get a certificate. Until then Caddy keeps retrying, and `make up` still reports every service healthy, because no healthcheck covers the certificate. `make reset` deletes the certificate with everything else, and Let's Encrypt issues at most five a week for the same name.
- One machine, one of everything: no HA, an upgrade is a short restart, and nothing is backed up. Acceptable for a demo, and the reason ADR 0001's note about background jobs needing a lock has not yet had to be acted on.
- Compose has no notion of rollback. Recovering from a bad build means checking out the previous commit and running `make up` again.
- Rebuilding the VM is a manual checklist in `docs/setup_saas.md`. Terraform for the VM, firewall and DNS record, and a pipeline that runs `make up` on it, are the changes to make if that happens often.
