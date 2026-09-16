# 0005. Caddy at the edge for TLS and SPA hosting

- Status: Accepted
- Date: 2026-09-16

## Context
HTTPS is required in SaaS mode, and a self-signed certificate is acceptable if documented. The appliance runs on `localhost` or a LAN IP with no public DNS; the SaaS deployment has a domain. The SPA needs static hosting, and the API should share its origin to avoid CORS.

## Decision
Caddy 2 is the only public HTTP entry point (ports 80 and 443).

- The site address comes from `SITE_ADDRESS`. Caddy's automatic HTTPS uses its internal CA for `localhost` and private IPs, and Let's Encrypt for public domains, so one Caddyfile serves both modes.
- It serves the built SPA with fallback to `index.html`, proxies `/api/*` to the backend, and rewrites `/ingest` to `/api/v1/ingest`.
- HTTP redirects to HTTPS. Certificates and the internal CA persist in the `caddy_data` volume.
- Syslog (514) is published by Vector directly, since it isn't HTTP.

## Consequences
- No certbot, cron job or certificate scripts.
- UI and API share one origin, so there is no CORS configuration.
- On the appliance, browsers warn until the user trusts Caddy's root CA. `make ca` exports it, and `docs/setup_appliance.md` explains the steps.
- Let's Encrypt needs DNS pointing at the VM and ports 80/443 reachable.
- Syslog on 514 is unencrypted, as the assignment allows. Syslog over TLS (6514) would be a hardening step.
