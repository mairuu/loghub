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

`make up` creates `.env` from `.env.example` with random secrets, including the token signing secret `AUTH_SECRET` and the ingest key `INGEST_TOKEN` (it never overwrites an existing `.env`), builds the images, runs the migrations, seeds the demo accounts and waits until every service is healthy. Seeding is a one-shot `seed` service the backend waits on, so it needs no separate step; `docker compose run --rm seed` re-runs it. These are the accounts it creates:

| Account | Role | Tenant | Password |
|---|---|---|---|
| `admin@loghub.local` | admin | all | `ADMIN_PASSWORD` in `.env` |
| `viewer@demoa.local` | viewer | demoA | `VIEWER_PASSWORD` in `.env` |
| `viewer@demob.local` | viewer | demoB | `VIEWER_PASSWORD` in `.env` |

An admin reads every tenant and may send events. A viewer reads only their own tenant.

Check that it's running:

```sh
make ps                                  # services healthy; migrate exited with 0
curl -k https://localhost/api/healthz
```

### Upgrading an older checkout

An `.env` written before sign-in existed lacks `AUTH_SECRET` and `INGEST_TOKEN`, and `make up` stops with `required variable ... is missing a value`. Add both by hand, then run `make up` again:

```sh
echo "AUTH_SECRET=$(openssl rand -hex 24)" >> .env
echo "INGEST_TOKEN=$(openssl rand -hex 24)" >> .env
```

Each must be at least 32 characters, or the backend refuses to start. Changing `AUTH_SECRET` signs everyone out. Changing `INGEST_TOKEN` takes effect once the backend and Vector are recreated with `make up`.

## Sign in

The API takes a bearer token, which `POST /api/v1/auth/login` returns and which lasts 12 hours:

```sh
set -a; . ./.env; set +a
TOKEN=$(curl -sk https://localhost/api/v1/auth/login -H 'Content-Type: application/json' \
  -d "{\"email\":\"viewer@demoa.local\",\"password\":\"$VIEWER_PASSWORD\"}" | jq -r .token)
curl -sk https://localhost/api/v1/events -H "Authorization: Bearer $TOKEN" | jq '.items[].tenant'   # only demoA
curl -sk 'https://localhost/api/v1/events?tenant=demoB' -H "Authorization: Bearer $TOKEN"          # 403 tenant_not_permitted
```

A request with no token is refused with 401 `authentication_required`, and one with an expired or malformed token with 401 `invalid_token`. The API reference at `https://localhost/api/docs` and the Postman collection in [`docs/postman_collection.json`](postman_collection.json) cover every endpoint. In Postman, set the collection's `password` variable, and `email` if you want a viewer instead of the admin, then send **Auth → Sign in**: it stores the token in `bearerToken`, which the other requests send.

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

To send JSON events over HTTP instead, use the ingest key, `INGEST_TOKEN` in `.env`. It may send events for any tenant and do nothing else. An admin's token works too, and a viewer's doesn't. `samples/post_logs.py` reads the key from `.env` itself:

```sh
samples/post_logs.py --url https://localhost -k                     # samples/json/*.json, one request each
curl -sk https://localhost/api/v1/ingest -H "Authorization: Bearer $INGEST_TOKEN" \
  -H 'Content-Type: application/json' -d '{"tenant":"demoA","source":"api","event_type":"hello"}'
```
