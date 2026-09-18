#!/usr/bin/env python3
"""Checks a running loghub against the acceptance criteria.

  tests/acceptance.py                                     https://$SITE_ADDRESS from .env
  tests/acceptance.py --url https://loghub.example.com    a SaaS deployment, from anywhere
  tests/acceptance.py -k                                  an appliance whose CA isn't trusted

Sends a few events tagged with this run's ID, through syslog, POST /ingest and
a file upload, and checks what each way in stored, what search and the
dashboard's counts return for them, that each viewer sees only their own
tenant, and that the sample alert rule fires. tests/README.md maps each check
to docs/requirements.md §10.

Reads ADMIN_EMAIL, ADMIN_PASSWORD, VIEWER_PASSWORD and INGEST_TOKEN from the
environment or the checkout's .env. Exits with 1 if any check fails.
"""

import argparse
import collections
import http.client
import ipaddress
import json
import os
import random
import socket
import ssl
import sys
import time
import urllib.error
import urllib.parse
import urllib.request
from pathlib import Path

PROG = os.path.basename(sys.argv[0])
ROOT = Path(__file__).resolve().parent.parent
ENV_FILE = ROOT / ".env"
SAMPLES = ROOT / "samples"
# Where `make ca` saves the root certificate Caddy signs localhost with.
LOCAL_CA = ROOT / "loghub-root-ca.crt"
TIMEOUT = 30
SYSLOG_PORT = 514
# The acceptance criterion: a syslog message appears within one minute.
SYSLOG_DEADLINE = 60
# Rules are evaluated every minute, over a window that ends at least 30
# seconds in the past, so an alert takes up to about two and a half minutes.
ALERT_DEADLINE = 180
VIEWERS = {"demoA": "viewer@demoa.local", "demoB": "viewer@demob.local"}
# Addresses reserved for benchmarking, RFC 2544. Each run's failed logins come
# from one of them, so an earlier run's alert cooldown can't hide this run's.
ATTACKERS = ipaddress.ip_network("198.18.0.0/15")


class Fail(Exception):
    """A check found something the requirements don't allow."""


class Unreachable(Exception):
    """A request got no HTTP response, so the checks after it can't pass either."""


class API:
    def __init__(self, base: str, ctx: ssl.SSLContext):
        self.base = base
        self.ctx = ctx

    def call(self, method: str, path: str, token=None, params=None, body=None, content_type="application/json"):
        """Returns the status and the decoded JSON body, or None for a body that isn't JSON."""
        url = self.base + path
        if params:
            url += "?" + urllib.parse.urlencode(params, doseq=True)
        headers = {"Accept": "application/json"}
        if token:
            headers["Authorization"] = "Bearer " + token
        data = None
        if body is not None:
            data = body if isinstance(body, bytes) else json.dumps(body).encode()
            headers["Content-Type"] = content_type
        req = urllib.request.Request(url, data=data, method=method, headers=headers)
        try:
            with urllib.request.urlopen(req, timeout=TIMEOUT, context=self.ctx) as resp:
                status, raw = resp.status, resp.read()
        except urllib.error.HTTPError as e:
            status, raw = e.code, e.read()
        except urllib.error.URLError as e:
            hint = ""
            if isinstance(e.reason, ssl.SSLCertVerificationError):
                hint = "; on an appliance, `make ca` saves Caddy's CA where this script trusts it, or pass -k"
            raise Unreachable(f"cannot reach {url}: {e.reason}{hint}") from e
        except OSError as e:
            raise Unreachable(f"{method} {url} failed: {e}") from e
        try:
            return status, json.loads(raw)
        except ValueError:
            return status, None

    def get(self, path: str, token: str, params=None):
        status, doc = self.call("GET", path, token, params)
        if status != 200:
            raise Fail(f"GET {path} answered {describe(status, doc)}")
        return doc


class Run:
    def __init__(self, api: API, verified: bool, ingest_token: str):
        self.api = api
        self.verified = verified
        self.ingest_token = ingest_token
        self.id = time.strftime("acceptance-%Y%m%d-%H%M%S-") + f"{random.getrandbits(16):04x}"
        self.host = urllib.parse.urlsplit(api.base).hostname
        self.tokens = {}
        # Events stored with the run's tag, by the stores' own counts.
        self.stored = 0
        self.attacker = str(ATTACKERS[random.randrange(1, ATTACKERS.num_addresses - 1)])
        self.threshold = 0
        self.alert_sent = None

    # HTTPS: the SaaS deployment is reachable over HTTPS.
    def https(self) -> str:
        status, doc = self.api.call("GET", "/api/healthz")
        if status != 200 or not isinstance(doc, dict) or doc.get("status") != "ok":
            raise Fail(f"/api/healthz answered {describe(status, doc)}")
        cert = "a trusted certificate" if self.verified else "an unchecked certificate (-k)"
        if urllib.parse.urlsplit(self.api.base).port:
            return f"/api/healthz is ok over {cert}"

        # Port 80 only redirects.
        conn = http.client.HTTPConnection(self.host, 80, timeout=TIMEOUT)
        try:
            conn.request("HEAD", "/")
            resp = conn.getresponse()
        except OSError as e:
            raise Fail(f"/api/healthz is ok over {cert}, but http://{self.host} didn't answer: {e}")
        finally:
            conn.close()
        location = resp.getheader("Location") or ""
        if resp.status not in (301, 302, 307, 308) or not location.startswith("https://"):
            raise Fail(f"http://{self.host} answered {resp.status} {location}, not a redirect to HTTPS")
        return f"/api/healthz is ok over {cert}, and http:// redirects to https:// ({resp.status})"

    def sign_in(self, admin_email: str, admin_password: str, viewer_password: str) -> str:
        self.tokens["admin"] = self.login(admin_email, admin_password, "admin", None)
        for tenant, email in VIEWERS.items():
            self.tokens[tenant] = self.login(email, viewer_password, "viewer", tenant)
        return f"{admin_email} as admin, and {' and '.join(VIEWERS.values())} as their tenants' viewers"

    def login(self, email: str, password: str, role: str, tenant) -> str:
        status, doc = self.api.call("POST", "/api/v1/auth/login", body={"email": email, "password": password})
        if status != 200:
            raise Fail(f"{email} can't sign in: {describe(status, doc)}")
        if doc.get("role") != role or doc.get("tenant") != tenant:
            raise Fail(f"{email} signed in as {doc.get('role')} of {doc.get('tenant')}, not {role} of {tenant}")
        return doc["token"]

    # HTTP API ingestion: POST /ingest with the sample JSON succeeds, and the
    # record can be found by search.
    def http_ingest(self) -> str:
        self.send("/ingest", [sample("json/api.json")])
        events = self.search(self.tokens["admin"], {"tag": self.id, "source": "api"})
        if len(events) != 1:
            raise Fail(f"POST /ingest stored the event, but search found {len(events)}")
        expect(events[0], "api", tenant="demoA", user="alice", src_ip="203.0.113.7", action="login", tag="auth_failure")
        return "POST /ingest stored samples/json/api.json, and search finds it as alice's failed login from 203.0.113.7"

    # File-based sources: sample AWS, M365 and AD files are uploaded, and the
    # data is normalized.
    def file_upload(self) -> str:
        files = {
            "json/aws_cloudtrail.json": dict(tenant="demoB", vendor="aws", product="cloudtrail", event_type="CreateUser", action="create"),
            "json/m365_audit.json": dict(tenant="demoB", vendor="microsoft", product="m365", action="login", tag="auth_success"),
            "json/ad_4625.json": dict(tenant="demoA", vendor="microsoft", product="windows", action="login", event_subtype="network", tag="auth_failure"),
        }
        records = [sample(f) for f in files]
        self.send("/api/v1/ingest/file", records)
        for (name, want), record in zip(files.items(), records):
            events = self.search(self.tokens["admin"], {"tag": self.id, "source": record["source"]})
            if len(events) != 1:
                raise Fail(f"the upload stored samples/{name}, but search found {len(events)} {record['source']} events")
            expect(events[0], record["source"], **want)
        return "POST /api/v1/ingest/file stored the AWS, M365 and AD samples, each with its vendor, product and action"

    # Alerting, part one: the sample rule exists, and enough failed logins to
    # fire it are on their way. The evaluator takes a while, so the other
    # checks run before part two looks for the alert.
    def alert_rule(self) -> str:
        rule = sample("alert_rule.json")
        admin = self.tokens["admin"]
        rules = self.api.get("/api/v1/alert-rules", admin, {"tenant": rule["tenant"]})["items"]
        found = next((r for r in rules if same_rule(r, rule)), None)
        if found:
            how = f"rule {found['id']} matches samples/alert_rule.json"
        else:
            status, doc = self.api.call("POST", "/api/v1/alert-rules", admin, body=rule)
            if status != 201:
                raise Fail(f"creating samples/alert_rule.json answered {describe(status, doc)}")
            how = f"created rule {doc['id']} from samples/alert_rule.json"

        login = sample("json/ad_4625.json")
        login["ip"] = self.attacker
        for _ in range(rule["threshold"]):
            self.send("/api/v1/ingest", [login])
        self.threshold = rule["threshold"]
        self.alert_sent = time.monotonic()
        return f"{how}; sent {rule['threshold']} failed logins from {self.attacker} to {rule['tenant']}"

    # Syslog ingestion: a sample syslog message appears within one minute.
    def syslog(self) -> str:
        lines = {}
        errors = []
        for proto, send in (("UDP", send_udp), ("TCP", send_tcp)):
            marker = f"{self.id}-{proto.lower()}"
            line = (
                f"<134>Aug 20 12:44:56 fw-acceptance vendor=demo product=ngfw action=deny "
                f"src=192.0.2.10 dst=198.51.100.53 spt=5353 dpt=53 proto=udp msg={marker}"
            )
            try:
                send(self.host, line)
                lines[proto] = marker
            except OSError as e:
                errors.append(f"can't send over {proto} to {self.host}:{SYSLOG_PORT}: {e}")

        start = time.monotonic()
        found = {}
        while lines.keys() - found.keys():
            for proto in lines.keys() - found.keys():
                events = self.search(self.tokens["admin"], {"q": lines[proto]})
                if events:
                    found[proto] = (events[0], time.monotonic() - start)
            if time.monotonic() - start >= SYSLOG_DEADLINE:
                break
            time.sleep(2)

        for proto in sorted(lines.keys() - found.keys()):
            errors.append(f"the line sent over {proto} wasn't found within {SYSLOG_DEADLINE} s")
        for event, _ in found.values():
            expect(event, "firewall", src_ip="192.0.2.10", action="deny", event_type="traffic")
        if errors:
            raise Fail("; ".join(errors))
        took = max(t for _, t in found.values())
        tenant = next(iter(found.values()))[0]["tenant"]
        return f"lines sent over UDP and TCP to port {SYSLOG_PORT} were stored in {tenant} as firewall events, found within {took:.0f} s"

    # RBAC: a viewer can see only their own tenant.
    def rbac(self) -> str:
        everything = self.search(self.tokens["admin"], {"tag": self.id})
        per_tenant = collections.Counter(e["tenant"] for e in everything)
        for tenant in VIEWERS:
            if not per_tenant[tenant]:
                raise Fail(f"this run stored nothing in {tenant}, so there is nothing to compare")

        for tenant, other in zip(VIEWERS, reversed(VIEWERS)):
            token = self.tokens[tenant]
            seen = self.search(token, {"tag": self.id})
            strangers = sorted({e["tenant"] for e in seen} - {tenant})
            if strangers:
                raise Fail(f"{tenant}'s viewer sees events from {', '.join(strangers)}")
            if len(seen) != per_tenant[tenant]:
                raise Fail(f"{tenant}'s viewer sees {len(seen)} of this run's {per_tenant[tenant]} events in {tenant}")
            status, doc = self.api.call("GET", "/api/v1/events", token, {"tenant": other})
            if status != 403 or code(doc) != "tenant_not_permitted":
                raise Fail(f"{tenant}'s viewer asking for {other} got {describe(status, doc)}, not 403 tenant_not_permitted")

        # Only the ingest key and admins may send events, and nothing is read without a token.
        event = {"tenant": "demoA", "source": "api", "event_type": "acceptance_viewer_write", "_tags": [self.id]}
        status, doc = self.api.call("POST", "/api/v1/ingest", self.tokens["demoA"], body=event)
        if status != 403:
            raise Fail(f"a viewer sending an event got {describe(status, doc)}, not 403")
        status, doc = self.api.call("GET", "/api/v1/events")
        if status != 401:
            raise Fail(f"a search without a token got {describe(status, doc)}, not 401")

        a, b = (per_tenant[t] for t in VIEWERS)
        return (
            f"demoA's viewer sees only this run's {a} demoA events and demoB's viewer only its {b}; "
            "each gets 403 for the other tenant, a viewer can't send events, and no token gets 401"
        )

    # Dashboard: Top N, timeline, tenant filter, source filter and time filter
    # all work. Each count must agree with the search it filters like.
    def dashboard(self) -> str:
        admin = self.tokens["admin"]
        events = self.search(admin, {"tag": self.id})
        if len(events) != self.stored:
            raise Fail(f"search found {len(events)} of the {self.stored} events this run stored")
        first = self.search(admin, {"tag": self.id, "order": "asc", "limit": 1})[0]["@timestamp"]

        cases = [
            ("every tenant", {}, events),
            ("tenant demoB", {"tenant": "demoB"}, [e for e in events if e["tenant"] == "demoB"]),
            ("source ad", {"source": "ad"}, [e for e in events if e["source"] == "ad"]),
            ("from the first event", {"from": first}, events),
            ("before the first event", {"to": first}, []),
        ]
        for name, params, subset in cases:
            params = {"tag": self.id, **params}
            top = self.api.get("/api/v1/events/top", admin, {"field": "src_ip", **params})["items"]
            want = collections.Counter(e["src_ip"] for e in subset if e.get("src_ip"))
            got = {i["value"]: i["count"] for i in top}
            if got != dict(want):
                raise Fail(f"top source IPs for {name} are {got}, but search finds {dict(want)}")
            counts = [i["count"] for i in top]
            if counts != sorted(counts, reverse=True):
                raise Fail(f"top source IPs for {name} aren't most frequent first: {counts}")

            buckets = self.api.get("/api/v1/events/timeline", admin, params)["buckets"]
            total = sum(b["count"] for b in buckets)
            if total != len(subset):
                raise Fail(f"the timeline for {name} counts {total} events, but search finds {len(subset)}")

        leader = self.api.get("/api/v1/events/top", admin, {"field": "src_ip", "tag": self.id, "limit": 1})["items"]
        lead = f"; the top source IP is {leader[0]['value']} with {leader[0]['count']}" if leader else ""
        return f"top values and the timeline agree with search for every tenant, a tenant, a source and a time range{lead}"

    # Alerting, part two: a notification is observed in the UI, through the
    # API the Alerts page reads.
    def alerting(self) -> str:
        if self.alert_sent is None:
            raise Fail("the failed logins weren't sent")
        viewer = self.tokens["demoA"]
        print(f"      waiting up to {ALERT_DEADLINE} s for the evaluator", flush=True)
        while True:
            alerts = self.api.get("/api/v1/alerts", viewer, {"limit": 100})["items"]
            mine = [a for a in alerts if a["group_key"] == self.attacker]
            took = time.monotonic() - self.alert_sent
            if mine:
                break
            if took >= ALERT_DEADLINE:
                raise Fail(f"no alert for {self.attacker} within {ALERT_DEADLINE} s of the failed logins")
            time.sleep(5)

        alert = mine[0]
        if alert["count"] < self.threshold:
            raise Fail(f"the alert for {self.attacker} counted {alert['count']} events, fewer than the rule's {self.threshold}")
        others = self.api.get("/api/v1/alerts", self.tokens["demoB"], {"limit": 100})["items"]
        if any(a["group_key"] == self.attacker for a in others):
            raise Fail("demoB's viewer sees demoA's alert")
        return (
            f"{alert['rule_name']!r} raised an alert for {self.attacker} ({alert['count']} events) "
            f"{took:.0f} s after the failed logins; demoA's viewer sees it, and demoB's doesn't"
        )

    def send(self, path: str, records: list[dict]) -> None:
        """Posts records, tagged with the run, with the ingest key."""
        tagged = [dict(r, _tags=list(r.get("_tags") or []) + [self.id]) for r in records]
        if path.endswith("/file"):
            body = b"".join(json.dumps(r).encode() + b"\n" for r in tagged)
            content_type = "application/x-ndjson"
        else:
            (record,) = tagged
            body, content_type = json.dumps(record).encode(), "application/json"
        status, doc = self.api.call("POST", path, self.ingest_token, body=body, content_type=content_type)
        if status != 200:
            raise Fail(f"POST {path} answered {describe(status, doc)}")
        self.stored += doc["accepted"]
        if doc["rejected"]:
            reasons = "; ".join(f"{e['code']}: {e['message']}" for e in doc.get("errors") or [])
            raise Fail(f"POST {path} rejected {doc['rejected']} of {len(tagged)}: {reasons}")

    def search(self, token: str, params: dict) -> list[dict]:
        return self.api.get("/api/v1/events", token, {"limit": 1000, **params})["items"]


def sample(name: str) -> dict:
    return json.loads((SAMPLES / name).read_text())


def expect(event: dict, source: str, tag=None, **fields) -> None:
    """Fails unless event has source, every field given, and tag among its tags."""
    want = {"source": source, **fields}
    wrong = [f"{k} is {event.get(k)!r}, not {v!r}" for k, v in want.items() if event.get(k) != v]
    if tag and tag not in event["_tags"]:
        wrong.append(f"tags {event['_tags']} lack {tag}")
    if wrong:
        raise Fail(f"the {source} event {event.get('id')} is stored wrongly: " + "; ".join(wrong))


def same_rule(rule: dict, want: dict) -> bool:
    """Reports whether rule counts what want does, whatever its name, cooldown or webhook."""
    fields = ("tenant", "group_by", "threshold", "window_minutes", "source", "event_type", "action", "severity_min")
    return all(rule.get(f) == want.get(f) for f in fields) and sorted(rule["tags"]) == sorted(want.get("tags", []))


def send_udp(host: str, line: str) -> None:
    # IPv4 first: a cloud VM's firewall rules often cover only that.
    infos = socket.getaddrinfo(host, SYSLOG_PORT, type=socket.SOCK_DGRAM)
    family, kind, proto, _, addr = min(infos, key=lambda i: i[0] != socket.AF_INET)
    with socket.socket(family, kind, proto) as s:
        s.sendto(line.encode() + b"\n", addr)


def send_tcp(host: str, line: str) -> None:
    with socket.create_connection((host, SYSLOG_PORT), timeout=10) as s:
        s.sendall(line.encode() + b"\n")


def code(doc) -> str | None:
    return doc.get("code") if isinstance(doc, dict) else None


def describe(status: int, doc) -> str:
    """Describes a response, from the error envelope if it has one."""
    if isinstance(doc, dict) and "code" in doc:
        return f"HTTP {status} {doc['code']}: {doc.get('message', '')}"
    return f"HTTP {status}"


def env_file_value(key: str) -> str | None:
    """Returns key's value in the checkout's .env, if it has one."""
    try:
        lines = ENV_FILE.read_text().splitlines()
    except OSError:
        return None
    for line in lines:
        name, sep, value = line.partition("=")
        if sep and name.strip() == key:
            return value.strip() or None
    return None


def setting(key: str, default=None) -> str | None:
    return os.environ.get(key) or env_file_value(key) or default


def parse_args() -> argparse.Namespace:
    site = setting("SITE_ADDRESS", "localhost")
    p = argparse.ArgumentParser(
        description="Checks a running loghub against the acceptance criteria in docs/requirements.md §10. "
        "Leaves a few events tagged with the run in demoA and demoB, and creates the sample alert rule "
        "if demoA has none like it. Exits with 1 if any check fails.",
    )
    p.add_argument(
        "--url",
        default=os.environ.get("LOGHUB_URL") or f"https://{site}",
        help=f"loghub's base URL; syslog goes to port {SYSLOG_PORT} on its host "
        f"(default: $LOGHUB_URL, or https:// and SITE_ADDRESS from .env: https://{site})",
    )
    p.add_argument(
        "-k",
        "--insecure",
        action="store_true",
        help=f"don't verify the TLS certificate. Without it, the system's CAs are trusted, and {LOCAL_CA.name} "
        "too if `make ca` saved it.",
    )
    return p.parse_args()


def main() -> int:
    args = parse_args()
    if not args.url.startswith("https://"):
        sys.exit(f"{PROG}: --url must start with https://, since serving HTTPS is one of the checks")

    creds = {k: setting(k) for k in ("ADMIN_PASSWORD", "VIEWER_PASSWORD", "INGEST_TOKEN")}
    missing = [k for k, v in creds.items() if not v]
    if missing:
        sys.exit(f"{PROG}: set {', '.join(missing)} in the environment or in {ENV_FILE}")

    ctx = ssl.create_default_context()
    if args.insecure:
        ctx.check_hostname = False
        ctx.verify_mode = ssl.CERT_NONE
    elif LOCAL_CA.exists():
        ctx.load_verify_locations(LOCAL_CA)

    run = Run(API(args.url.rstrip("/"), ctx), not args.insecure, creds["INGEST_TOKEN"])
    admin_email = setting("ADMIN_EMAIL", "admin@loghub.local")
    print(f"Checking {run.api.base}. This run's events are tagged {run.id}.", flush=True)

    checks = [
        ("HTTPS", run.https),
        ("Sign-in", lambda: run.sign_in(admin_email, creds["ADMIN_PASSWORD"], creds["VIEWER_PASSWORD"])),
        ("HTTP API ingestion", run.http_ingest),
        ("File upload", run.file_upload),
        ("Alert rule", run.alert_rule),
        ("Syslog ingestion", run.syslog),
        ("RBAC", run.rbac),
        ("Dashboard", run.dashboard),
        ("Alerting", run.alerting),
    ]
    ran = failed = 0
    for name, check in checks:
        ran += 1
        try:
            detail = check()
        except Fail as e:
            failed += 1
            print(f"FAIL  {name}: {e}", flush=True)
            if name == "Sign-in":
                break
        except Unreachable as e:
            failed += 1
            print(f"FAIL  {name}: {e}", flush=True)
            break
        else:
            print(f"ok    {name}: {detail}", flush=True)

    skipped = len(checks) - ran
    print(f"\n{ran - failed} passed, {failed} failed" + (f", {skipped} not run" if skipped else ""))
    return 1 if failed else 0


if __name__ == "__main__":
    sys.exit(main())
