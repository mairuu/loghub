# SaaS setup

Run loghub on a cloud VM at a public HTTPS address that reviewers can reach. It's the same Compose stack as the [appliance](setup_appliance.md), and the one difference is `SITE_ADDRESS` in `.env`. Set to a public DNS name, it makes Caddy get a certificate from Let's Encrypt instead of from its own CA, so browsers trust it with no setup. [ADR 0011](adr/0011-deployment-compose.md) explains why.

The examples use `loghub.example.com`; use your own name instead.

## Requirements

- A VM from any cloud provider, with Ubuntu 22.04 or later. 2 vCPU, 8 GB RAM and a 40 GB disk are enough: on that size, the first `make up` takes about two minutes.
- A public IPv4 address. For an A record in your own domain, make it static, so that the record still points at the VM after it is stopped and started. A DNS name from the provider follows the address by itself.
- A DNS name for that address, as the next section describes.
- The tools the [appliance needs](setup_appliance.md#requirements), installed on the VM the same way.

### Firewall

Open these ports in the provider's firewall, for example an Azure network security group or an AWS security group:

| Port | From | For |
|---|---|---|
| 22/tcp | your own address | SSH |
| 80/tcp | anywhere | Let's Encrypt's checks, and redirects to HTTPS |
| 443/tcp | anywhere | the UI and the API |
| 514/udp, 514/tcp | anywhere, or only the senders you expect | syslog |

Use the provider's firewall, not ufw on the VM: Docker's published ports bypass ufw, so its rules don't apply to them. Syslog has no authentication, so anyone who can reach 514 can add events to the tenant `SYSLOG_DEFAULT_TENANT`. Leave it open to anywhere only if reviewers are to send syslog from their own machines.

### DNS name

Create an A record for a domain you control, such as `loghub.example.com`, pointing at the VM's address. Don't add an AAAA record unless that IPv6 address reaches the VM too, because Let's Encrypt tries IPv6 first. Check the record from the VM:

```sh
getent hosts loghub.example.com   # prints the VM's public address
```

Without a domain, you can use a name under a shared one, such as the DNS name a provider gives the VM (on Azure, `<label>.<region>.cloudapp.azure.com`) or `203-0-113-10.sslip.io` for the address 203.0.113.10. These names share Let's Encrypt's weekly certificate limit with every other user of the parent domain, so issuance can fail with a rate-limit error that only waiting fixes.

## Install

On the VM:

```sh
git clone https://github.com/mairuu/loghub.git
cd loghub
make env
sed -i 's/^SITE_ADDRESS=.*/SITE_ADDRESS=loghub.example.com/' .env
make up
```

`make env` writes `.env` with random secrets and passwords, as on an appliance. Keep the generated passwords rather than choosing easier ones, since this site is on the internet. Sign-in allows each address 10 attempts a minute, which slows guessing but can't stop someone with many addresses. `make up` builds the images, runs the migrations, seeds the demo accounts and waits until every service is healthy.

Caddy requests its certificate as it starts, and `make up` doesn't wait for it, so check that separately:

```sh
make ps                                                     # every service healthy
docker compose logs caddy | grep 'certificate obtained'     # names loghub.example.com
curl https://loghub.example.com/api/healthz                 # {"status":"ok"}, with no -k
curl -sI http://loghub.example.com | head -1                # 308: plain HTTP redirects to HTTPS
```

If there is no certificate, see [Troubleshooting](#troubleshooting).

## Give reviewers access

Reviewers need the URL, `https://loghub.example.com`, and the credentials in `.env` on the VM:

```sh
grep -E '^(ADMIN_EMAIL|ADMIN_PASSWORD|VIEWER_PASSWORD|INGEST_TOKEN)=' .env
```

Send those four lines privately, never in the repository or an issue. These are the accounts they sign in with:

| Account | Role | Tenant | Password |
|---|---|---|---|
| `admin@loghub.local` | admin | all | `ADMIN_PASSWORD` |
| `viewer@demoa.local` | viewer | demoA | `VIEWER_PASSWORD` |
| `viewer@demob.local` | viewer | demoB | `VIEWER_PASSWORD` |
| `viewer@democ.local` | viewer | demoC | `VIEWER_PASSWORD` |

With the `monitoring` profile running, the admin email and `ADMIN_PASSWORD` also sign in to Grafana at `https://loghub.example.com/grafana/`, which shows what the system is doing: events stored per tenant and source, rejections, sign-ins by outcome, alerts, API latency and background jobs.

### What reviewers can do from their own machines

The UI is at `https://loghub.example.com`, and the API reference is at `https://loghub.example.com/api/docs`. In the Postman collection, set `baseUrl` to the URL.

The steps in the appliance guide from [Use the UI](setup_appliance.md#use-the-ui) onwards work against the SaaS deployment, with three changes:

- Use `https://loghub.example.com` for `https://localhost`, and drop `-k`, because the certificate is publicly trusted.
- In their own clone of the repository, save the four lines the operator sent as `.env`, where the commands and `samples/post_logs.py` read them. Don't run the stack from that clone, since its `.env` lacks the other settings.
- `inbox/`, `make send-samples` and `make logs` work only on the VM. Upload files on the UI's **Upload** page, or with `samples/post_logs.py --file`, instead.

For example, to send the samples from elsewhere:

```sh
samples/send_syslog.sh --host loghub.example.com                                            # syslog, UDP
samples/send_syslog.sh --tcp --host loghub.example.com samples/syslog/network.log           # syslog, TCP
logger -n loghub.example.com -P 514 -t myapp "user=alice action=deny"                       # syslog, UDP
samples/post_logs.py --url https://loghub.example.com                                       # samples/json over HTTPS
samples/post_logs.py --url https://loghub.example.com --file samples/json/m365_audit.json   # one file, as an upload
```

demoC is there to try things in. Neither the simulator nor the acceptance checks write to it, so it holds only what reviewers send it. Send events and add alert rules to it as the admin, on the **Upload** and **Alerts** pages, or with the ingest key. Then sign in as `viewer@democ.local` to see it as its viewer does. The JSON samples name demoA or demoB, so re-address them first:

```sh
jq -c '.tenant = "demoC"' samples/json/*.json | samples/post_logs.py --url https://loghub.example.com -
```

Syslog can't reach demoC, because every syslog line is stored under `SYSLOG_DEFAULT_TENANT`, which is demoA.

To run every acceptance check at once, from the same clone, use [`tests/acceptance.py`](../tests/README.md). It is also a quick way for the operator to check a new install. It takes one to three minutes, and leaves a few events tagged with the run:

```sh
tests/acceptance.py --url https://loghub.example.com
```

## Keep demo traffic flowing

A new install has no events. On the VM, fill the last day once, then keep the simulation running with the stack ([Simulate two companies](setup_appliance.md#simulate-two-companies)), along with Prometheus and Grafana to chart it ([Watch the metrics](setup_appliance.md#watch-the-metrics)):

```sh
samples/simulate.py --backfill 24h --for 0
echo COMPOSE_PROFILES=demo,monitoring >> .env
make up
make logs s=simulator                          # each incident as it starts
```

Grafana is then at `https://loghub.example.com/grafana/`. Prometheus is on the VM's loopback only; `ssh -L 9090:127.0.0.1:9090` with the VM's address reaches it from your machine at `http://127.0.0.1:9090`.

Brute-force incidents raise alerts only in a tenant that has a failed-login rule, such as the one `tests/acceptance.py` creates in demoA. The acceptance checks look only at events tagged with their own run, so the simulation doesn't disturb them.

## Run it

- **Upgrade:** `git pull`, then `make up`. The running services keep serving while the new images build, and then only what changed is restarted, which takes a few seconds. Migrations and seeding run again and leave existing data alone.
- **Change the name,** say from the provider's to your own domain: point the new name at the VM, set `SITE_ADDRESS` to it in `.env`, then run `make up`. Caddy gets a certificate for the new name and stops answering to the old one, so send reviewers the new URL.
- **Reboot:** Docker starts at boot and restarts every long-running service, so the site comes back on its own.
- **Logs:** `make logs s=backend`, or `s=caddy` for certificates. [`ingest/README.md`](../ingest/README.md) says how to tell whether events arrived.
- **Stop:** `make down` stops the stack and keeps the data. `make reset` also deletes the data and the certificate. Let's Encrypt issues at most five certificates a week for the same name, so avoid resetting repeatedly.
- **Tear down:** delete the DNS record, and then the VM. A record left behind would point at an address the provider may give to someone else.

## Troubleshooting

- **No certificate:** read `docker compose logs caddy`. The usual causes are a DNS record that doesn't point at the VM yet, or an AAAA record that doesn't reach it, port 80 or 443 closed in the provider's firewall, or a rate limit on a shared domain. Caddy keeps retrying on its own. Once the cause is fixed, `docker compose restart caddy` retries straight away.
- **A certificate warning or an empty page:** Caddy answers only to the name in `SITE_ADDRESS`, so use exactly that name, not the VM's IP address or another name for it.
