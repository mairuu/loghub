# Appliance setup

Run all of loghub on one machine or VM with Docker Compose.

## Requirements

- Linux with Docker Engine and the Compose plugin (Ubuntu 22.04+ recommended), plus `make`, `git`, `python3`, `openssl` and `jq`
- 4 vCPU, 8 GB RAM, 40 GB disk
- Free ports: 80 and 443 (UI and API), 514/udp and 514/tcp (syslog)

On a fresh Ubuntu machine:

```sh
sudo apt-get update && sudo apt-get install -y make git python3 openssl jq netcat-openbsd
curl -fsSL https://get.docker.com | sh
sudo usermod -aG docker "$USER"   # then log out and back in
```

## Install

```sh
git clone https://github.com/mairuu/loghub.git
cd loghub
make up
```

`make up` creates `.env` from `.env.example` with random secrets (it never overwrites an existing `.env`), builds the images, runs the migrations, seeds the demo accounts and waits until every service is healthy. Seeding is a one-shot `seed` service the backend waits on, so it needs no separate step; `docker compose run --rm seed` re-runs it. These are the accounts it creates:

| Account | Role | Tenant | Password |
|---|---|---|---|
| `admin@loghub.local` | admin | all | `ADMIN_PASSWORD` in `.env` |
| `viewer@demoa.local` | viewer | demoA | `VIEWER_PASSWORD` in `.env` |
| `viewer@demob.local` | viewer | demoB | `VIEWER_PASSWORD` in `.env` |

Sign-in arrives with the auth milestone; the accounts already exist.

Check that it's running:

```sh
make ps                                  # services healthy; migrate exited with 0
curl -k https://localhost/api/healthz
```

## Send some events

```sh
make send-samples
```

This sends the syslog samples to port 514, over UDP and TCP, and drops the JSON samples into `inbox/`. They are searchable within a few seconds. To send your own:

```sh
logger -n 127.0.0.1 -P 514 -t myapp "user=alice action=deny"        # syslog, UDP
jq -c . samples/json/m365_audit.json > inbox/.m365.tmp && mv inbox/.m365.tmp inbox/m365.ndjson
```

Syslog is stored under the tenant `SYSLOG_DEFAULT_TENANT` in `.env`, and each inbox file is deleted once it has been read. [`ingest/README.md`](../ingest/README.md) covers the collector in full, including how to tell whether events arrived.
