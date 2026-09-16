# 0011. One Compose file for both deployment modes

- Status: Draft
- Date: 2026-09-16

## Context
Requirement 3.8 needs the same system to run as an appliance on a single machine or VM and as a publicly reachable SaaS deployment, and the acceptance criteria say appliance startup is one command or a few steps. The two modes differ in almost nothing: the appliance answers on `localhost` or a LAN address with a self-signed certificate, the SaaS deployment answers on a domain with a real one. ADR 0005 already made that difference a single `SITE_ADDRESS` value.

Maintaining two Compose files, or a Compose file and a Helm chart, would mean the mode a reviewer tries is not the mode that was tested.

## Decision
- One `docker-compose.yml` describes both modes. `.env` is the only thing that differs, and `docker-compose.dev.yml` is an override for development that publishes Postgres on 5432 and the backend on 8080 — ports neither deployment mode exposes.
- `make up` is the single command. It generates `.env` from `.env.example` with random secrets if none exists, builds the images, and waits with `--wait` until every service is healthy.
- Startup order is expressed as Compose conditions rather than as retries in the application: Postgres healthy → `migrate` completed → `seed` completed → `backend` starts. `migrate` and `seed` are one-shot services running the same image as the backend (ADR 0001), so no extra tooling is needed in the stack.
- Only `migrate` and `seed` connect as the owner role. The backend connects as `loghub_app` (ADR 0003). The Compose file carries this split as an anchor, so it is visible in one place.
- Published ports are Caddy's 80 and 443 and Vector's 514 over UDP and TCP (ADR 0004, ADR 0005), matching the port list in requirement §7. Nothing else is reachable from outside the Compose network.
- Images are built from source on the host rather than pulled from a registry. A clone and one command is the whole install, with no account or registry to configure.
- State lives in named volumes: `postgres_data`, `caddy_data` for certificates and the internal CA, and Vector's disk buffer. `make reset` deletes them and asks first.
- SaaS mode is the same stack on a cloud VM with `SITE_ADDRESS` set to the domain, which is what switches Caddy from its internal CA to Let's Encrypt. An OVA is an export of an appliance that has already been through this, not a separate build.

## Consequences
- What a reviewer runs on the appliance is what runs in the cloud, down to the image.
- Building on each host costs a few minutes per install and requires the build toolchain in the image layers. Pulling prebuilt images would be faster and is the change to make if installs become frequent.
- Secrets are generated per install and never leave the machine. There is no shared default password, and `make env` never overwrites an existing `.env`.
- One machine, one of everything: no HA, and an upgrade is a short restart. Acceptable for a demo, and the reason ADR 0001's note about background jobs needing a lock has not yet had to be acted on.
- Compose has no notion of rollback. Recovering from a bad build means checking out the previous commit and running `make up` again.
