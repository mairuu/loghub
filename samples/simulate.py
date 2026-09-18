#!/usr/bin/env python3
"""Sends two made-up companies' security logs to loghub as they happen, so
that the dashboard, search and alerts have something to show.

  samples/simulate.py                                   until interrupted
  samples/simulate.py --backfill 24h                    the last 24 hours at once, then live
  samples/simulate.py --backfill 24h --for 0            only the last 24 hours
  samples/simulate.py --incident brute-force:demoA --for 0
                                                        one password-guessing burst in demoA, now
  samples/simulate.py --url https://loghub.example.com  a SaaS deployment, from anywhere

demoA runs Windows and an in-house portal behind a firewall and a router,
which send syslog to port 514, over UDP and TCP. demoB runs on Microsoft 365
and AWS, sells an online product, and its firewall ships its lines over
HTTP. Both have endpoint agents. Everything but the syslog, and everything
backfilled, is posted to /api/v1/ingest/batch with the ingest key.

Ordinary traffic averages one event a second, busiest in the afternoon, and
every 20 minutes or so an incident plays out on top of it:

  brute-force  6 to 14 failed logins from one address within three minutes,
               which the sample alert rule catches, sometimes followed by a
               login that works
  port-scan    one address probing 20 to 50 ports on a company's firewall
  malware      an endpoint detection, then the workstation calling out to its
               controller and being blocked by the firewall

Public addresses are from the documentation ranges of RFC 5737: the
companies' own in 198.51.100.0/24, attackers' in 203.0.113.0/24, and
everyone else's in 192.0.2.0/24.

Reads INGEST_TOKEN and SYSLOG_DEFAULT_TENANT from the environment or the
checkout's .env. Exits with 1 if loghub rejected any event, or some couldn't
be delivered.
"""

import argparse
import heapq
import itertools
import json
import math
import os
import random
import re
import signal
import socket
import ssl
import sys
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid
from dataclasses import dataclass
from datetime import datetime, timezone
from pathlib import Path

PROG = os.path.basename(sys.argv[0])
ROOT = Path(__file__).resolve().parent.parent
ENV_FILE = ROOT / ".env"
# Where `make ca` saves the root certificate Caddy signs localhost with.
LOCAL_CA = ROOT / "loghub-root-ca.crt"
TIMEOUT = 30
SYSLOG_PORT = 514
# Records per request, and how often live records are posted.
BATCH = 1000
FLUSH_EVERY = 2
# Live records kept for a retry while loghub can't be reached.
MAX_PENDING = 50_000
# Retention is seven days, and older events would be stored as arriving now.
MAX_BACKFILL = 6 * 86400
INCIDENTS = ("brute-force", "port-scan", "malware")

# Syslog facilities, as the PRI's facility * 8.
LOCAL0, LOCAL7 = 16 * 8, 23 * 8
RESOLVER, OTHER_RESOLVER, NTP = "192.0.2.53", "192.0.2.153", "192.0.2.250"
ISP_PEER = "198.51.100.1"
CI_RUNNER = "192.0.2.200"
AWS_ACCOUNT, AWS_REGION = "210987654321", "ap-southeast-1"
SCANNED_PORTS = [
    21, 22, 23, 25, 53, 80, 81, 110, 111, 135, 139, 143, 389, 443, 445, 465, 587, 631, 873, 993, 995,
    1080, 1433, 1521, 1723, 2049, 2375, 3000, 3306, 3389, 5000, 5060, 5432, 5601, 5900, 5985,
    6379, 7001, 8000, 8008, 8080, 8081, 8443, 8888, 9000, 9090, 9200, 10000, 11211, 27017,
]


class Unreachable(Exception):
    """A request got no HTTP response."""


class Refused(Exception):
    """loghub refused the ingest key, so every other request would fail too."""


class Stop(Exception):
    """The process was asked to stop."""


@dataclass
class Event:
    t: float  # Unix time
    tenant: str
    via: str  # udp, tcp or http
    body: str | dict  # a syslog line, or a JSON record


@dataclass
class Person:
    name: str
    host: str  # their workstation or laptop
    ip: str  # its address inside the company
    home: str  # their address when working from home


@dataclass
class Company:
    tenant: str
    people: list[Person]
    wan: str  # the firewall's public address
    firewall: str
    syslog: bool  # the firewall and router send to port 514, not over HTTP

    def user(self, p: Person) -> str:
        """Returns p as the company's directory names them."""
        return f"{p.name}@demob.local" if self.tenant == "demoB" else f"demoa\\{p.name}"

    def device(self, t: float, line: str, tcp=False) -> Event:
        """Returns a syslog line from the company's firewall or router."""
        via = ("tcp" if tcp else "udp") if self.syslog else "http"
        return Event(t, self.tenant, via, line)


class World:
    """The two companies, and what their systems log."""

    def __init__(self, rng: random.Random, syslog_tenant: str):
        self.rng = rng
        a_names = ["alice", "bob", "carol", "dave", "erin", "frank", "grace", "heidi"]
        b_names = ["ivan", "judy", "ken", "liam", "mia", "noah"]
        self.a = Company(
            "demoA",
            [Person(n, f"WS-A{i + 1:02}", f"10.10.1.{11 + i}", f"192.0.2.{101 + i}") for i, n in enumerate(a_names)],
            "198.51.100.10",
            "fw01",
            syslog_tenant == "demoA",
        )
        self.b = Company(
            "demoB",
            [Person(n, f"LT-B{i + 1:02}", f"10.20.1.{21 + i}", f"192.0.2.{121 + i}") for i, n in enumerate(b_names)],
            "198.51.100.20",
            "fw-b1",
            syslog_tenant == "demoB",
        )
        self.customers = [(f"customer{n:03}@example.com", f"192.0.2.{60 + n}") for n in range(40)]
        a, b = self.a, self.b
        self.mix = [
            (26, self.browse, a), (8, self.dns, a), (3, self.probe, a), (1, self.vpn, a), (1.2, self.router, a),
            (12, self.logon, a), (5, self.logoff, a), (0.3, self.mistyped, a), (5, self.portal, a),
            (0.3, self.endpoint, a),
            (14, self.browse, b), (5, self.dns, b), (2, self.probe, b),
            (7, self.m365_login, b), (0.15, self.m365_failed, b), (8, self.m365_file, b), (5, self.m365_mail, b),
            (12, self.aws_activity, b), (1, self.aws_console, b), (0.3, self.aws_change, b),
            (10, self.product, b), (0.3, self.endpoint, b),
        ]
        self.weights = [w for w, _, _ in self.mix]

    def background(self, t: float) -> list[Event]:
        _, make, company = self.rng.choices(self.mix, self.weights)[0]
        return make(company, t)

    def incident(self, kind: str, t: float, tenant=None) -> tuple[list[Event], str]:
        company = {"demoA": self.a, "demoB": self.b}.get(tenant) or self.rng.choice([self.a, self.b])
        return {"brute-force": self.brute_force, "port-scan": self.port_scan, "malware": self.malware}[kind](company, t)

    # --- helpers ---

    def person(self, c: Company) -> Person:
        return self.rng.choice(c.people)

    def hostile(self) -> str:
        return f"203.0.113.{self.rng.randint(1, 254)}"

    def record(self, c: Company, source: str, t: float, **fields) -> Event:
        return Event(t, c.tenant, "http", {"tenant": c.tenant, "source": source, **fields, "@timestamp": iso(t)})

    def fw(self, c: Company, t: float, action: str, src: str, dst: str, dpt: int, proto="tcp", policy="", msg=None, sev=6):
        spt = self.rng.randint(49152, 65535)
        kv = f"vendor=demo product=ngfw action={action} src={src} dst={dst} spt={spt} dpt={dpt} proto={proto}"
        if msg:
            kv += f" msg={msg}"
        return c.device(t, f"<{LOCAL0 + sev}>{rfc3164(t)} {c.firewall} {kv} policy={policy}")

    # --- firewalls and routers ---

    def browse(self, c: Company, t: float) -> list[Event]:
        p = self.person(c)
        port = 443 if self.rng.random() < 0.9 else 80
        return [self.fw(c, t, "allow", p.ip, f"192.0.2.{self.rng.randint(1, 49)}", port, policy="Allow-Web")]

    def dns(self, c: Company, t: float) -> list[Event]:
        p = self.person(c)
        if self.rng.random() < 0.1:
            return [self.fw(c, t, "deny", p.ip, OTHER_RESOLVER, 53, "udp", "Block-DNS", msg="DNS blocked", sev=4)]
        return [self.fw(c, t, "allow", p.ip, RESOLVER, 53, "udp", "Allow-DNS")]

    def probe(self, c: Company, t: float) -> list[Event]:
        """The internet's background scanning, dropped at the firewall."""
        port = self.rng.choice([22, 23, 445, 1433, 3389, 5900, 8080])
        return [self.fw(c, t, "drop", self.hostile(), c.wan, port, policy="Block-Inbound", sev=4)]

    def vpn(self, c: Company, t: float) -> list[Event]:
        p = self.person(c)
        return [self.fw(c, t, "accept", p.home, c.wan, 51820, "udp", "Allow-VPN")]

    def router(self, c: Company, t: float) -> list[Event]:
        def line(t: float, sev: int, app: str, body: str) -> Event:
            return c.device(t, f"<{LOCAL7 + sev}>1 {iso(t)} r1 {app} - - - {body}", tcp=True)

        r = self.rng.random()
        if r < 0.5:
            port = f"ge-0/0/{self.rng.randint(0, 7)}"
            return [
                line(t, 3, "kernel", f"if={port} event=link-down reason=carrier-loss"),
                line(t + self.rng.uniform(3, 40), 5, "kernel", f"if={port} event=link-up"),
            ]
        if r < 0.7:
            return [line(t, 5, "mgd", "event=config-commit user=netadmin")]
        if r < 0.85:
            return [
                line(t, 4, "rpd", f"peer={ISP_PEER} event=bgp-neighbor-down reason=hold-timer-expired"),
                line(t + self.rng.uniform(20, 90), 5, "rpd", f"peer={ISP_PEER} event=bgp-neighbor-up"),
            ]
        return [line(t, 6, "ntpd", f"server={NTP} event=ntp-sync")]

    # --- demoA: Windows, and the in-house portal ---

    def win(self, c: Company, t: float, event_id: int, event_type: str, user: str, host: str, ip: str, **extra) -> Event:
        return self.record(c, "ad", t, event_id=event_id, event_type=event_type, user=user, host=host, ip=ip, **extra)

    def logon(self, c: Company, t: float) -> list[Event]:
        p = self.person(c)
        r = self.rng.random()
        if r < 0.7:
            return [self.win(c, t, 4624, "LogonSuccess", c.user(p), "DC01", p.ip, logon_type=3)]
        if r < 0.95:
            return [self.win(c, t, 4624, "LogonSuccess", c.user(p), p.host, p.ip, logon_type=2 if r < 0.85 else 7)]
        return [self.win(c, t, 4624, "LogonSuccess", "demoa\\svc-backup", "FS01", "10.10.0.20", logon_type=5)]

    def logoff(self, c: Company, t: float) -> list[Event]:
        p = self.person(c)
        return [self.win(c, t, 4634, "Logoff", c.user(p), "DC01", p.ip, logon_type=3)]

    def mistyped(self, c: Company, t: float) -> list[Event]:
        p = self.person(c)
        return [self.win(c, t, 4625, "LogonFailed", c.user(p), p.host, p.ip, logon_type=2, status="0xc000006a")]

    def app(self, c: Company, t: float, host: str, event_type: str, user: str, ip: str, method: str, url: str,
            status: int, **extra) -> Event:
        return self.record(c, "api", t, event_type=event_type, user=user, ip=ip, host=host, http_method=method, url=url,
                           status_code=status, **extra)

    def portal(self, c: Company, t: float) -> list[Event]:
        p = self.person(c)
        r = self.rng.random()
        if r < 0.45:
            return [self.app(c, t, "portal01", "app_login", p.name, p.ip, "POST", "/login", 200)]
        if r < 0.8:
            return [self.app(c, t, "portal01", "app_logout", p.name, p.ip, "POST", "/logout", 204)]
        if r < 0.97:
            report = f"/reports/{self.rng.randint(1000, 1999)}/export"
            return [self.app(c, t, "portal01", "report_exported", p.name, p.ip, "GET", report, 200)]
        return [self.app(c, t, "portal01", "app_login_failed", p.name, p.ip, "POST", "/login", 401, reason="wrong_password")]

    # --- both: endpoint agents ---

    def endpoint(self, c: Company, t: float) -> list[Event]:
        p = self.person(c)
        event_type, severity, action, process = self.rng.choice([
            ("pup_detected", 3, "quarantine", "bundleware_setup.exe"),
            ("suspicious_script", 4, "alert", "wscript.exe"),
            ("process_blocked", 5, "block", "psexec.exe"),
        ])
        return [self.record(c, "crowdstrike", t, event_type=event_type, host=p.host, user=c.user(p), process=process,
                            severity=severity, sha256=self.rng.randbytes(32).hex(), action=action)]

    # --- demoB: Microsoft 365, AWS, and the product ---

    def where(self, c: Company, p: Person) -> str:
        """Returns where p works from: the office, or home."""
        return c.wan if self.rng.random() < 0.7 else p.home

    def m365(self, c: Company, t: float, event_type: str, user: str, ip: str, workload: str, **extra) -> Event:
        return self.record(c, "m365", t, event_type=event_type, user=user, ip=ip, workload=workload, **extra)

    def m365_login(self, c: Company, t: float) -> list[Event]:
        p = self.person(c)
        return [self.m365(c, t, "UserLoggedIn", c.user(p), self.where(c, p), "AzureActiveDirectory", status="Success")]

    def m365_failed(self, c: Company, t: float) -> list[Event]:
        p = self.person(c)
        return [self.m365(c, t, "UserLoginFailed", c.user(p), p.home, "AzureActiveDirectory", status="Failed",
                          logon_error="InvalidUserNameOrPassword")]

    def m365_file(self, c: Company, t: float) -> list[Event]:
        p = self.person(c)
        op = self.rng.choice(["FileAccessed", "FileAccessed", "FileModified", "FileDownloaded", "FileUploaded"])
        doc = self.rng.choice(["roadmap-2026.xlsx", "pricing.docx", "architecture.pptx", "payroll-09.xlsx", "notes.md"])
        return [self.m365(c, t, op, c.user(p), self.where(c, p), "SharePoint",
                          object=f"/sites/demob/Shared Documents/{doc}")]

    def m365_mail(self, c: Company, t: float) -> list[Event]:
        p = self.person(c)
        op = self.rng.choice(["MailItemsAccessed", "MailItemsAccessed", "Send"])
        return [self.m365(c, t, op, c.user(p), self.where(c, p), "Exchange")]

    def aws(self, c: Company, t: float, user: str, ip: str, service: str, name: str, params=None, response=None,
            role=False, agent="aws-cli/2.27.50") -> Event:
        """Returns a CloudTrail record. loghub reads its event type, and for a
        ConsoleLogin its outcome, from raw."""
        if role:
            identity = {"type": "AssumedRole", "arn": f"arn:aws:sts::{AWS_ACCOUNT}:assumed-role/{user}/ci"}
        else:
            identity = {"type": "IAMUser", "arn": f"arn:aws:iam::{AWS_ACCOUNT}:user/{user}", "userName": user}
        raw = {
            "eventVersion": "1.11",
            "eventTime": iso(t, millis=False),
            "eventSource": f"{service}.amazonaws.com",
            "eventName": name,
            "awsRegion": AWS_REGION,
            "sourceIPAddress": ip,
            "userAgent": agent,
            "userIdentity": {**identity, "accountId": AWS_ACCOUNT},
            "requestParameters": params,
            "responseElements": response,
            "eventID": str(uuid.UUID(int=self.rng.getrandbits(128), version=4)),
        }
        cloud = {"service": service, "account_id": AWS_ACCOUNT, "region": AWS_REGION}
        return self.record(c, "aws", t, cloud=cloud, user=user, ip=ip, raw=raw)

    def aws_activity(self, c: Company, t: float) -> list[Event]:
        build = f"builds/app-1.{self.rng.randint(0, 9)}.{self.rng.randint(0, 30)}.tar.gz"
        if self.rng.random() < 0.55:
            service, name, params = self.rng.choice([
                ("s3", "PutObject", {"bucketName": "demob-artifacts", "key": build}),
                ("s3", "GetObject", {"bucketName": "demob-artifacts", "key": build}),
                ("sts", "AssumeRole", {"roleArn": f"arn:aws:iam::{AWS_ACCOUNT}:role/deploy", "roleSessionName": "ci"}),
                ("ecs", "UpdateService", {"cluster": "prod", "service": "api"}),
            ])
            return [self.aws(c, t, "deploy", CI_RUNNER, service, name, params, role=True, agent="Boto3/1.40.20")]
        p = self.person(c)
        service, name, params = self.rng.choice([
            ("ec2", "DescribeInstances", None),
            ("s3", "ListBuckets", None),
            ("s3", "GetObject", {"bucketName": "demob-reports", "key": f"daily/{iso(t)[:10]}.csv"}),
            ("logs", "FilterLogEvents", {"logGroupName": "/ecs/api"}),
            ("cloudwatch", "GetMetricData", None),
        ])
        return [self.aws(c, t, p.name, self.where(c, p), service, name, params)]

    def aws_console(self, c: Company, t: float) -> list[Event]:
        p = self.person(c)
        failed = self.rng.random() < 0.05
        return [self.aws(c, t, p.name, p.home if failed else self.where(c, p), "signin", "ConsoleLogin",
                         response={"ConsoleLogin": "Failure" if failed else "Success"}, agent="Mozilla/5.0")]

    def aws_change(self, c: Company, t: float) -> list[Event]:
        p = self.person(c)
        service, name, params = self.rng.choice([
            ("iam", "CreateAccessKey", {"userName": p.name}),
            ("iam", "CreateUser", {"userName": f"contractor-{self.rng.randint(1, 9)}"}),
            ("iam", "DeleteUser", {"userName": f"contractor-{self.rng.randint(1, 9)}"}),
            ("iam", "AttachUserPolicy", {"userName": p.name, "policyArn": "arn:aws:iam::aws:policy/ReadOnlyAccess"}),
            ("ec2", "RunInstances", {"instanceType": "t3.medium", "minCount": 1, "maxCount": 1}),
            ("ec2", "TerminateInstances", {"instancesSet": {"items": [{"instanceId": f"i-{self.rng.randbytes(8).hex()}"}]}}),
        ])
        return [self.aws(c, t, p.name, self.where(c, p), service, name, params)]

    def product(self, c: Company, t: float) -> list[Event]:
        email, ip = self.rng.choice(self.customers)
        r = self.rng.random()
        if r < 0.5:
            return [self.app(c, t, "api-b1", "app_login", email, ip, "POST", "/v1/sessions", 201)]
        if r < 0.85:
            return [self.app(c, t, "api-b1", "app_logout", email, ip, "DELETE", "/v1/sessions/current", 204)]
        if r < 0.95:
            return [self.app(c, t, "api-b1", "password_reset_requested", email, ip, "POST", "/v1/password-resets", 202)]
        if r < 0.97:
            return [self.app(c, t, "api-b1", "api_token_created", email, ip, "POST", "/v1/tokens", 201, action="create")]
        return [self.app(c, t, "api-b1", "app_login_failed", email, ip, "POST", "/v1/sessions", 401,
                         reason="wrong_password")]

    # --- incidents ---

    def brute_force(self, c: Company, t: float) -> tuple[list[Event], str]:
        attacker = self.hostile()
        span = self.rng.uniform(60, 180)
        times = sorted(t + self.rng.uniform(0, span) for _ in range(self.rng.randint(6, 14)))
        end = times[-1]
        works = self.rng.random() < 0.3
        people = c.people
        victim = self.rng.choice(people)

        if c is self.a and self.rng.random() < 0.5:
            # One account guessed at on the domain controller, until it locks.
            user = c.user(victim)
            events = [
                self.win(c, ti, 4625, "LogonFailed", user, "DC01", attacker, logon_type=3,
                         status="0xc000006a" if i < 5 else "0xc0000234")
                for i, ti in enumerate(times)
            ]
            events.append(self.win(c, times[4] + 0.5, 4740, "AccountLockedOut", user, "DC01", attacker))
            return events, f"{len(times)} failed logins as {user} on DC01 from {attacker}, which lock the account"

        if c is self.a:
            names = [p.name for p in people] + ["admin", "administrator", "test", "root"]
            events = [
                self.app(c, ti, "portal01", "app_login_failed", self.rng.choice(names), attacker, "POST", "/login", 401,
                         reason="wrong_password")
                for ti in times
            ]
            what = f"{len(times)} failed logins to the portal from {attacker}"
            if works:
                later = end + self.rng.uniform(5, 30)
                report = f"/reports/{self.rng.randint(1000, 1999)}/export"
                events += [
                    self.app(c, later, "portal01", "app_login", victim.name, attacker, "POST", "/login", 200),
                    self.app(c, later + self.rng.uniform(20, 90), "portal01", "report_exported", victim.name, attacker,
                             "GET", report, 200),
                ]
                what += f", then {victim.name}'s password works and a report is exported"
            return events, what

        if self.rng.random() < 0.5:
            # Password spraying against Microsoft 365.
            users = [c.user(p) for p in people] + ["admin@demob.local"]
            events = [self.m365(c, ti, "UserLoginFailed", self.rng.choice(users), attacker, "AzureActiveDirectory",
                                status="Failed", logon_error="InvalidUserNameOrPassword") for ti in times]
            what = f"{len(times)} failed Microsoft 365 logins from {attacker}"
            if works:
                later = end + self.rng.uniform(5, 30)
                user = c.user(victim)
                events += [
                    self.m365(c, later, "UserLoggedIn", user, attacker, "AzureActiveDirectory", status="Success"),
                    self.m365(c, later + self.rng.uniform(30, 90), "New-InboxRule", user, attacker, "Exchange",
                              parameters="ForwardTo=collector@203.0.113.66"),
                    self.m365(c, later + self.rng.uniform(60, 150), "MailItemsAccessed", user, attacker, "Exchange"),
                ]
                what += f", then {user} signs in and forwards their mail"
            return events, what

        # Credential stuffing against the product.
        emails = [e for e, _ in self.customers] + [f"user{self.rng.randint(1, 9999)}@example.net" for _ in range(20)]
        events = [
            self.app(c, ti, "api-b1", "app_login_failed", self.rng.choice(emails), attacker, "POST", "/v1/sessions", 401,
                     reason="wrong_password")
            for ti in times
        ]
        what = f"{len(times)} failed logins to the product from {attacker}"
        if works:
            email = self.rng.choice(self.customers)[0]
            later = end + self.rng.uniform(5, 30)
            events += [
                self.app(c, later, "api-b1", "app_login", email, attacker, "POST", "/v1/sessions", 201),
                self.app(c, later + self.rng.uniform(10, 60), "api-b1", "api_token_created", email, attacker, "POST",
                         "/v1/tokens", 201, action="create"),
            ]
            what += f", then {email}'s password works and an API token is created"
        return events, what

    def port_scan(self, c: Company, t: float) -> tuple[list[Event], str]:
        scanner = self.hostile()
        ports = self.rng.sample(SCANNED_PORTS, self.rng.randint(20, 50))
        span = self.rng.uniform(15, 60)
        events = [
            self.fw(c, t + span * i / len(ports), "deny", scanner, c.wan, port, policy="Block-Inbound", sev=4)
            for i, port in enumerate(ports)
        ]
        return events, f"{scanner} probes {len(ports)} ports on {c.wan} in {span:.0f} s"

    def malware(self, c: Company, t: float) -> tuple[list[Event], str]:
        p = self.person(c)
        user = c.user(p)
        c2 = self.hostile()
        document = f"invoice_{self.rng.randint(1000, 9999)}.docm"
        events = [
            self.record(c, "crowdstrike", t, event_type="suspicious_process", host=p.host, user=user,
                        process="powershell.exe", parent_process="WINWORD.EXE", severity=6, action="alert",
                        command_line="powershell.exe -nop -w hidden -enc SQBFAFgA..."),
            self.record(c, "crowdstrike", t + self.rng.uniform(5, 20), event_type="malware_detected", host=p.host, user=user,
                        process="powershell.exe", file=document, severity=9, sha256=self.rng.randbytes(32).hex(),
                        action="quarantine"),
        ]
        tries = self.rng.randint(3, 6)
        at = t + self.rng.uniform(20, 40)
        for _ in range(tries):
            events.append(self.fw(c, at, "deny", p.ip, c2, 443, policy="Block-Threat-Intel", msg="known C2", sev=4))
            at += self.rng.uniform(10, 30)
        what = (f"{document} starts powershell.exe on {p.host}, which is quarantined, "
                f"then {tries} call-outs to {c2} are blocked")
        return events, what


class Simulation:
    """Orders the world's events in time, from start on."""

    def __init__(self, world: World, rng: random.Random, rate: float, incident_every: float, start: float):
        self.world = world
        self.rng = rng
        self.rate = rate
        self.incident_every = incident_every
        self.pending = []  # (t, n, Event)
        self.order = itertools.count()
        self.incidents = 0
        self.background_at = start + self.gap(start) if rate else math.inf
        self.incident_at = start + rng.uniform(0.2, 1) * incident_every if incident_every else math.inf
        self.on_incident = lambda kind, tenant, t, what: None

    def gap(self, t: float) -> float:
        return self.rng.expovariate(self.rate * busy(t))

    def schedule(self, events: list[Event]) -> None:
        for ev in events:
            heapq.heappush(self.pending, (ev.t, next(self.order), ev))

    def incident(self, kind: str, t: float, tenant=None) -> None:
        events, what = self.world.incident(kind, t, tenant)
        self.incidents += 1
        self.schedule(events)
        self.on_incident(kind, events[0].tenant, t, what)

    def stop(self) -> None:
        """Starts no more ordinary events or incidents. Incidents under way play out."""
        self.background_at = self.incident_at = math.inf

    def next_at(self) -> float:
        return min(self.background_at, self.incident_at, self.pending[0][0] if self.pending else math.inf)

    def advance(self, until: float) -> list[Event]:
        """Returns every event due by until, in time order."""
        due = []
        while (t := self.next_at()) <= until:
            if self.pending and self.pending[0][0] == t:
                due.append(heapq.heappop(self.pending)[2])
            elif t == self.background_at:
                self.schedule(self.world.background(t))
                self.background_at = t + self.gap(t)
            else:
                self.incident(self.rng.choice(INCIDENTS), t)
                self.incident_at = t + self.rng.uniform(0.5, 1.5) * self.incident_every
        return due


class Sender:
    """Sends events over syslog as they come, and posts JSON records in batches."""

    def __init__(self, base: str, ctx: ssl.SSLContext, token: str, syslog: tuple[str, int]):
        self.url = base + "/api/v1/ingest/batch"
        self.ctx = ctx
        self.token = token
        self.syslog = syslog
        self.udp = None
        self.udp_at = 0.0
        self.tcp = None
        self.tcp_retry_at = 0.0
        self.batch: list[bytes] = []
        self.flushed_at = time.monotonic()
        self.sent = {"udp": 0, "tcp": 0, "http": 0}
        self.rejected = 0
        self.warned: dict[str, float] = {}

    def send(self, ev: Event, backfill=False) -> None:
        if ev.via == "http" or backfill:
            record = ev.body
            if isinstance(record, str):
                # A syslog line over HTTP, as the collector would wrap it.
                record = {"tenant": ev.tenant, "message": record, "@timestamp": iso(ev.t)}
            self.batch.append(json.dumps(record, separators=(",", ":")).encode())
            if len(self.batch) >= BATCH:
                self.flush(strict=backfill)
        elif ev.via == "udp":
            self.send_udp(ev.body)
        else:
            self.send_tcp(ev.body)

    def send_udp(self, line: str) -> None:
        try:
            # Looked up again every minute: in Compose, a recreated collector
            # has a new address.
            if self.udp is None or time.monotonic() - self.udp_at >= 60:
                # IPv4 first: a cloud VM's firewall rules often cover only that.
                infos = socket.getaddrinfo(*self.syslog, type=socket.SOCK_DGRAM)
                family, kind, proto, _, addr = min(infos, key=lambda i: i[0] != socket.AF_INET)
                if self.udp:
                    self.udp[0].close()
                self.udp = (socket.socket(family, kind, proto), addr)
                self.udp_at = time.monotonic()
            sock, addr = self.udp
            sock.sendto(line.encode() + b"\n", addr)
            self.sent["udp"] += 1
        except OSError as e:
            self.warn("udp", f"can't send syslog over UDP to {self.syslog[0]}:{self.syslog[1]}: {e}")

    def send_tcp(self, line: str) -> None:
        data = line.encode() + b"\n"
        for _ in range(2):
            if self.tcp is None:
                if time.monotonic() < self.tcp_retry_at:
                    return
                try:
                    self.tcp = socket.create_connection(self.syslog, timeout=10)
                except OSError as e:
                    self.tcp_retry_at = time.monotonic() + 5
                    self.warn("tcp", f"can't connect over TCP to {self.syslog[0]}:{self.syslog[1]}: {e}")
                    return
            try:
                self.tcp.sendall(data)
                self.sent["tcp"] += 1
                return
            except OSError:
                # The collector restarted, or closed an idle connection: reconnect once.
                self.tcp.close()
                self.tcp = None

    def tick(self) -> None:
        if self.batch and time.monotonic() - self.flushed_at >= FLUSH_EVERY:
            self.flush()

    def flush(self, strict=False) -> None:
        """Posts the batch. Unless strict, a batch that can't be delivered is
        kept for the next flush, up to MAX_PENDING records."""
        self.flushed_at = time.monotonic()
        while self.batch:
            chunk = self.batch[:BATCH]
            try:
                self.post(chunk)
            except Unreachable as e:
                if strict:
                    raise
                self.warn("http", f"{e}; retrying, with {len(self.batch)} records waiting")
                if len(self.batch) > MAX_PENDING:
                    del self.batch[: len(self.batch) - MAX_PENDING]
                return
            del self.batch[:BATCH]

    def post(self, chunk: list[bytes]) -> None:
        req = urllib.request.Request(
            self.url,
            data=b"\n".join(chunk) + b"\n",
            method="POST",
            headers={"Content-Type": "application/x-ndjson", "Authorization": "Bearer " + self.token},
        )
        try:
            with urllib.request.urlopen(req, timeout=TIMEOUT, context=self.ctx) as resp:
                result = json.loads(resp.read())
        except urllib.error.HTTPError as e:
            status, detail = e.code, error_detail(e.read())
            if status in (401, 403):
                raise Refused(f"loghub refused the ingest key: HTTP {status} {detail}; check INGEST_TOKEN") from e
            if status >= 500 or status in (408, 429):
                raise Unreachable(f"POST {self.url}: HTTP {status} {detail}") from e
            # Anything else is a bug here, and sending the batch again won't help.
            self.warn("http-" + str(status), f"POST {self.url}: HTTP {status} {detail}; {len(chunk)} records dropped")
            return
        except urllib.error.URLError as e:
            hint = ""
            if isinstance(e.reason, ssl.SSLCertVerificationError):
                hint = "; run `make ca` for an appliance's certificate, or pass -k"
            raise Unreachable(f"can't reach {self.url}: {e.reason}{hint}") from e
        except (OSError, ValueError) as e:
            raise Unreachable(f"POST {self.url} failed: {e}") from e

        self.sent["http"] += result.get("accepted", 0)
        rejected = result.get("rejected", 0)
        if rejected:
            self.rejected += rejected
            first = (result.get("errors") or [{}])[0]
            self.warn("rejected", f"loghub rejected {rejected} of {len(chunk)} records, the first with "
                                  f"{first.get('code')}: {first.get('message')}")

    def warn(self, key: str, message: str) -> None:
        """Prints message, but the same kind of warning at most once a minute."""
        now = time.monotonic()
        if now - self.warned.get(key, -math.inf) >= 60:
            self.warned[key] = now
            print(f"{clock()} warning: {message}", file=sys.stderr, flush=True)

    def total(self) -> int:
        return sum(self.sent.values())

    def summary(self) -> str:
        s = self.sent
        text = f"{self.total()} events: {s['udp']} over syslog UDP, {s['tcp']} over syslog TCP, {s['http']} over HTTP"
        if self.rejected:
            text += f"; {self.rejected} rejected"
        if self.batch:
            text += f"; {len(self.batch)} not delivered"
        return text

    def close(self) -> None:
        for sock in (self.tcp, self.udp[0] if self.udp else None):
            if sock:
                sock.close()


def busy(t: float) -> float:
    """How busy the companies are at t, from 0.4 at 2 a.m. to 1.6 at 2 p.m.
    local time, averaging 1 over a day."""
    lt = time.localtime(t)
    hour = lt.tm_hour + lt.tm_min / 60
    return 1 + 0.6 * math.cos(2 * math.pi * (hour - 14) / 24)


def iso(t: float, millis=True) -> str:
    d = datetime.fromtimestamp(t, timezone.utc)
    return d.isoformat(timespec="milliseconds" if millis else "seconds").replace("+00:00", "Z")


def rfc3164(t: float) -> str:
    d = datetime.fromtimestamp(t, timezone.utc)
    return f"{d:%b} {d.day:2} {d:%H:%M:%S}"


def clock() -> str:
    return time.strftime("%H:%M:%S")


def span(seconds: float) -> str:
    """Returns seconds as a duration such as 90s, 20m or 24h."""
    for unit, size in (("d", 86400), ("h", 3600), ("m", 60)):
        if seconds >= size and seconds % size == 0:
            return f"{seconds / size:g}{unit}"
    return f"{seconds:g}s"


def error_detail(raw: bytes) -> str:
    try:
        doc = json.loads(raw)
        return f"{doc['code']}: {doc['message']}"
    except (ValueError, TypeError, KeyError):
        return raw.decode(errors="replace").strip().split("\n", 1)[0]


def backfill(sim: Simulation, out: Sender, start: float, until: float) -> None:
    """Posts everything from start to until at once, syslog lines included."""
    began = time.monotonic()
    report_at = 50_000
    t = start
    while t < until:
        t = min(t + 600, until)
        for ev in sim.advance(t):
            out.send(ev, backfill=True)
        if out.sent["http"] >= report_at:
            print(f"{clock()} backfilled {out.sent['http']} events, up to {iso(t, millis=False)}", flush=True)
            report_at += 50_000
    out.flush(strict=True)
    print(f"{clock()} backfilled {out.sent['http']} events and {sim.incidents} incidents in "
          f"{time.monotonic() - began:.0f} s", flush=True)


def live(sim: Simulation, out: Sender, until: float) -> None:
    report_at = time.monotonic() + 60
    reported = out.total()
    while True:
        now = time.time()
        if now >= until:
            sim.stop()
        for ev in sim.advance(now):
            out.send(ev)
        out.tick()
        if time.monotonic() >= report_at:
            print(f"{clock()} {out.total() - reported} events in the last minute", flush=True)
            report_at += 60
            reported = out.total()
        next_at = sim.next_at()
        if next_at == math.inf:
            break
        time.sleep(max(0.0, min(next_at - now, FLUSH_EVERY)))
    out.flush()


DURATION = re.compile(r"(\d+(?:\.\d+)?)([smhd]?)")


def duration(text: str) -> float:
    m = DURATION.fullmatch(text.strip())
    if not m:
        raise argparse.ArgumentTypeError(f"{text!r} is not a duration such as 90s, 20m, 24h or 2d")
    return float(m[1]) * {"": 1, "s": 1, "m": 60, "h": 3600, "d": 86400}[m[2]]


def incident(text: str) -> tuple[str, str | None]:
    kind, _, tenant = text.partition(":")
    if kind not in INCIDENTS:
        raise argparse.ArgumentTypeError(f"{kind!r} is not one of {', '.join(INCIDENTS)}")
    if tenant not in ("", "demoA", "demoB"):
        raise argparse.ArgumentTypeError(f"{tenant!r} is not demoA or demoB")
    return kind, tenant or None


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
        description="Sends two made-up companies' security logs to loghub as they happen, until interrupted: "
        "demoA's firewall and router over syslog, and the rest over HTTP, with an incident every so often.",
    )
    p.add_argument(
        "--url",
        default=os.environ.get("LOGHUB_URL") or f"https://{site}",
        help=f"loghub's base URL (default: $LOGHUB_URL, or https:// and SITE_ADDRESS from .env: https://{site})",
    )
    p.add_argument(
        "--syslog",
        metavar="HOST[:PORT]",
        help=f"where to send syslog (default: the URL's host, port {SYSLOG_PORT})",
    )
    p.add_argument("--rate", type=float, default=1.0, help="ordinary events a second, on average (default: 1)")
    p.add_argument(
        "--incident-every",
        type=duration,
        default=20 * 60,
        metavar="DURATION",
        help="the average time between incidents, such as 10m, or 0 for none (default: 20m)",
    )
    p.add_argument(
        "--incident",
        action="append",
        type=incident,
        default=[],
        metavar="KIND[:TENANT]",
        help=f"play out an incident now, as well as the ones that come on their own: {', '.join(INCIDENTS)}, "
        "in demoA or demoB, or either if no tenant is given; may be repeated",
    )
    p.add_argument(
        "--backfill",
        type=duration,
        default=0,
        metavar="DURATION",
        help="first send what would have happened over this long up to now, such as 24h, at most 6d. "
        "No alerts are raised for it, since rules only look at the last few minutes.",
    )
    p.add_argument(
        "--for",
        dest="run_for",
        type=duration,
        default=math.inf,
        metavar="DURATION",
        help="how long to go on sending live, such as 30m, or 0 to stop after --backfill and --incident "
        "(default: until interrupted)",
    )
    p.add_argument("--seed", type=int, help="a seed for the random choices, to repeat a run")
    p.add_argument(
        "-k",
        "--insecure",
        action="store_true",
        help=f"don't verify the TLS certificate. Without it, the system's CAs are trusted, and {LOCAL_CA.name} "
        "too if `make ca` saved it.",
    )
    args = p.parse_args()

    url = urllib.parse.urlsplit(args.url)
    if url.scheme not in ("http", "https") or not url.hostname:
        p.error(f"--url {args.url!r} is not an http:// or https:// URL")
    args.url = args.url.rstrip("/")
    host, port = url.hostname, SYSLOG_PORT
    if args.syslog:
        m = re.fullmatch(r"\[([^]]+)\](?::(\d+))?|([^:]+)(?::(\d+))?", args.syslog)
        if not m:
            p.error(f"--syslog {args.syslog!r} is not HOST or HOST:PORT")
        host, port = m[1] or m[3], int(m[2] or m[4] or SYSLOG_PORT)
    args.syslog = (host, port)

    if args.rate <= 0:
        p.error("--rate must be more than 0")
    if args.backfill > MAX_BACKFILL:
        p.error("--backfill may be at most 6d: the events would be older than the seven days loghub keeps")
    if args.run_for == 0 and not args.backfill and not args.incident:
        p.error("--for 0 sends nothing without --backfill or --incident")
    args.token = setting("INGEST_TOKEN")
    if not args.token:
        p.error(f"set INGEST_TOKEN in the environment or in {ENV_FILE}")
    return args


def main() -> int:
    args = parse_args()

    ctx = ssl.create_default_context()
    if args.insecure:
        ctx.check_hostname = False
        ctx.verify_mode = ssl.CERT_NONE
    elif LOCAL_CA.exists():
        ctx.load_verify_locations(LOCAL_CA)

    def stop(signum, frame):
        raise Stop()

    # docker stop sends SIGTERM; stopping as for ^C posts what is waiting.
    signal.signal(signal.SIGTERM, stop)

    rng = random.Random(args.seed)
    syslog_tenant = setting("SYSLOG_DEFAULT_TENANT", "demoA")
    world = World(rng, syslog_tenant)
    out = Sender(args.url, ctx, args.token, args.syslog)
    now = time.time()
    sim = Simulation(world, rng, args.rate, args.incident_every, now - args.backfill)

    host, port = args.syslog
    if world.a.syslog or world.b.syslog:
        print(f"Sending to {args.url}, and {syslog_tenant}'s syslog to {host}:{port}.", flush=True)
    else:
        print(f"Sending to {args.url}. SYSLOG_DEFAULT_TENANT is {syslog_tenant}, so no syslog is sent.", flush=True)
    try:
        if args.backfill:
            print(f"{clock()} backfilling the last {span(args.backfill)}", flush=True)
            backfill(sim, out, now - args.backfill, now)
        sim.on_incident = lambda kind, tenant, t, what: print(f"{clock()} {kind} in {tenant}: {what}", flush=True)
        for kind, tenant in args.incident:
            sim.incident(kind, time.time(), tenant)
        # With --for 0, an incident the backfill left under way isn't finished.
        if args.run_for or args.incident:
            live(sim, out, time.time() + args.run_for)
    except (KeyboardInterrupt, Stop):
        try:
            out.flush()
        except (KeyboardInterrupt, Stop, Refused):
            pass
    except (Unreachable, Refused) as e:
        sys.exit(f"{PROG}: {e}")
    finally:
        out.close()
    print(f"{clock()} sent {out.summary()}; {sim.incidents} incidents", flush=True)
    return 1 if out.rejected or out.batch else 0


if __name__ == "__main__":
    sys.exit(main())
