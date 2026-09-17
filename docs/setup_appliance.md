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

Then open <https://localhost> and sign in with one of the accounts. The browser warns about the certificate until you trust it, as the next section describes.

### Trust the certificate

Caddy serves the appliance over HTTPS with a certificate from its own CA. Until a browser trusts that CA, it shows a warning you can click through, and `curl` needs `-k`. To trust it, save its root certificate to `loghub-root-ca.crt`:

```sh
make ca
```

Then add it where it is needed:

- **Firefox:** Settings → Privacy & Security → Certificates → View Certificates → Authorities → Import, and tick *Trust this CA to identify websites*.
- **Chrome and Edge:** Settings → Privacy and security → Security → Manage certificates, and import it as a trusted authority.
- **Ubuntu, for curl and Python:** `sudo cp loghub-root-ca.crt /usr/local/share/ca-certificates/ && sudo update-ca-certificates`
- **macOS:** `sudo security add-trusted-cert -d -r trustRoot -k /Library/Keychains/System.keychain loghub-root-ca.crt`
- **Windows,** in an administrator prompt: `certutil -addstore -f Root loghub-root-ca.crt`

The CA lives in the `caddy_data` volume, so it stays the same across restarts and upgrades. `make reset` deletes it, and the next `make up` makes a new one to trust.

### Reach it from another machine

Caddy answers only to the one name or address in `SITE_ADDRESS` in `.env`, which is `localhost`, and gives any other name an empty page. To use the appliance from elsewhere, set it to the address the other machines use, then run `make up` again:

```sh
grep -q '^SITE_ADDRESS=' .env || echo 'SITE_ADDRESS=' >> .env   # an .env from before the UI lacks it
sed -i 's/^SITE_ADDRESS=.*/SITE_ADDRESS=192.168.1.50/' .env
make up
```

Use that address on the appliance itself too, including in the commands below, which use `localhost`. Caddy's CA signs certificates for private addresses as it does for `localhost`. A public domain name gets a certificate from Let's Encrypt instead, which is how [`setup_saas.md`](setup_saas.md) deploys.

## Use the UI

- **Dashboard:** events over time and the most frequent source IPs, users, event types and hosts, for a time range, a tenant and any sources you choose. Select a bar or a value to search for its events. It refreshes every 30 seconds.
- **Search:** the events that match, newest first. Search the original events for text, or open *More filters* for event type, action, user, host, source IP, severity and tags. Select a row to see the whole normalized event beside the original, and add any of its values to the search. The first page refreshes every 15 seconds.
- **Alerts:** the alerts raised, with a link to the events each one counted, and the rules. The page refreshes every 15 seconds, and the navigation marks alerts raised since you last looked.
- **Upload,** for admins: send a vendor export, such as the files in `samples/json`. The page reports what was stored and why anything was rejected.

The dashboard and search keep their filters in the address, so a link or a reload shows the same thing. A viewer sees their own tenant's name where an admin chooses a tenant, and a link naming another tenant says it can't be shown. *API docs* in the header opens the API reference.

A session lasts 12 hours, or until the tab closes or you sign out.

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

This sends the syslog samples to port 514, over UDP and TCP, and drops the JSON samples into `inbox/`. They are searchable within a few seconds, and on the dashboard and search pages within their next refresh. To send your own:

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

## Count events

The dashboard's charts come from two endpoints, which take the same filters as search and count only what the caller may read. With `TOKEN` from Sign in:

```sh
curl -sk 'https://localhost/api/v1/events/top?field=src_ip' -H "Authorization: Bearer $TOKEN" | jq '.items'
curl -sk 'https://localhost/api/v1/events/timeline?interval=1h' -H "Authorization: Bearer $TOKEN" | jq '.buckets[] | select(.count > 0)'
```

`field` may be `src_ip`, `dst_ip`, `user`, `host` or `event_type`. Like search, both cover the last 24 hours unless `from` and `to` say otherwise. Without `interval`, the timeline picks the shortest one that divides the window into at most 200 parts.

## Raise an alert

Alert rules count matching events over a time window, and only an admin can create one. [`samples/alert_rule.json`](../samples/alert_rule.json) raises an alert when five failed logins come from one address in demoA within five minutes.

In the UI, sign in as the admin, open **Alerts**, choose **New rule**, then **Fill in the failed-login example**, pick demoA and select **Add rule**. With the API:

```sh
set -a; . ./.env; set +a
ADMIN=$(curl -sk https://localhost/api/v1/auth/login -H 'Content-Type: application/json' \
  -d "{\"email\":\"$ADMIN_EMAIL\",\"password\":\"$ADMIN_PASSWORD\"}" | jq -r .token)
curl -sk https://localhost/api/v1/alert-rules -H "Authorization: Bearer $ADMIN" \
  -H 'Content-Type: application/json' -d @samples/alert_rule.json
```

To have each alert POSTed to a webhook as well, add its URL to the rule. For a quick test, get a URL from a service such as webhook.site:

```sh
jq '. + {webhook_url: "https://webhook.site/<your-id>"}' samples/alert_rule.json |
  curl -sk https://localhost/api/v1/alert-rules -H "Authorization: Bearer $ADMIN" \
    -H 'Content-Type: application/json' -d @-
```

Rules can't be changed or removed through the API, so each of these commands adds another rule, and every rule raises its own alert.

Then send five failed logins. The sample's own time is from 2025, so each is stored with the time it arrives:

```sh
for i in 1 2 3 4 5; do jq -c . samples/json/ad_4625.json; done | samples/post_logs.py --url https://localhost -k -
```

Rules are evaluated once a minute, over a window that ends at least 30 seconds in the past, so the alert appears within about two and a half minutes. It shows on the **Alerts** page and the dashboard for the admin and demoA's viewer, and not for demoB's viewer. With the API:

```sh
curl -sk https://localhost/api/v1/alerts -H "Authorization: Bearer $TOKEN" | jq   # TOKEN from Sign in, as viewer@demoa.local
make logs s=backend                                                              # "alert raised", then "webhook delivered"
```

After an alert, the same address stays quiet for the rule's cooldown, five minutes here, however many failed logins follow.
