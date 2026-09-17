#!/usr/bin/env python3
"""Posts JSON events to loghub's HTTP ingest API.

  samples/post_logs.py                            samples/json/*.json, one request per event
  samples/post_logs.py --tag demo my.ndjson       a file's events, each tagged demo
  samples/post_logs.py --file --tenant demoB export.ndjson
                                                  a whole file in one request
  jq -c . samples/json/api.json | samples/post_logs.py -

Requests carry the ingest key, INGEST_TOKEN, from the checkout's .env unless
--token or the environment gives another credential.
"""

import argparse
import json
import os
import ssl
import sys
import urllib.error
import urllib.parse
import urllib.request
from dataclasses import dataclass
from pathlib import Path

PROG = os.path.basename(sys.argv[0])
DEFAULT_URL = "http://127.0.0.1:8080"
TIMEOUT = 30
ENV_FILE = Path(__file__).resolve().parent.parent / ".env"


@dataclass
class Record:
    where: str  # file, file:line or file[index], for messages
    body: bytes


class Unreachable(Exception):
    """The request got no HTTP response."""


class Refused(Exception):
    """The server refused the credential, so every other request would fail too."""


def main() -> int:
    args = parse_args()
    base = args.url.rstrip("/")
    ctx = ssl.create_default_context()
    if args.insecure:
        ctx.check_hostname = False
        ctx.verify_mode = ssl.CERT_NONE

    # Read everything first, so a bad file stops the run before anything is sent.
    inputs = [read(name, args.tag) for name in args.files]
    if args.file:
        params = {"tenant": args.tenant, "source": args.source}
        query = urllib.parse.urlencode({k: v for k, v in params.items() if v})
        endpoint = base + "/api/v1/ingest/file" + ("?" + query if query else "")
        content_type = "application/x-ndjson"
        requests = [
            (label, records, b"".join(r.body + b"\n" for r in records))
            for label, records in inputs
            if records
        ]
    else:
        endpoint = base + "/api/v1/ingest"
        content_type = "application/json"
        requests = [(r.where, [r], r.body) for _, records in inputs for r in records]

    sent = accepted = rejected = 0
    try:
        for label, records, body in requests:
            a, r = send(endpoint, content_type, body, label, records, args.token, ctx)
            sent += len(records)
            accepted += a
            rejected += r
    except (Unreachable, Refused) as e:
        sys.exit(f"{PROG}: {e}")
    print(f"sent {sent} event(s) to {endpoint}: {accepted} accepted, {rejected} rejected")
    return 1 if rejected else 0


def parse_args() -> argparse.Namespace:
    p = argparse.ArgumentParser(
        description="Posts each event in each FILE (default: samples/json/*.json) to "
        "loghub, one request per event. A .json file holds one event or a JSON array "
        "of them; any other file holds one event per line (NDJSON). - reads standard "
        "input. Exits with 1 if any event was not stored.",
    )
    p.add_argument("files", nargs="*", metavar="FILE")
    p.add_argument(
        "--url",
        default=os.environ.get("LOGHUB_URL", DEFAULT_URL),
        help=f"loghub's base URL (default: $LOGHUB_URL, or {DEFAULT_URL})",
    )
    p.add_argument(
        "--token",
        default=os.environ.get("LOGHUB_TOKEN") or os.environ.get("INGEST_TOKEN"),
        help="the credential to send: the ingest key, or an admin's token "
        f"(default: $LOGHUB_TOKEN, $INGEST_TOKEN, or INGEST_TOKEN in {ENV_FILE})",
    )
    p.add_argument(
        "--file",
        action="store_true",
        help="send each FILE whole, as a vendor export, to /api/v1/ingest/file",
    )
    p.add_argument("--tenant", help="with --file, the tenant of events that name none")
    p.add_argument("--source", help="with --file, the source of events that name none")
    p.add_argument(
        "--tag",
        action="append",
        default=[],
        help="add TAG to every event's _tags; may be repeated",
    )
    p.add_argument(
        "-k",
        "--insecure",
        action="store_true",
        help="don't verify the TLS certificate, such as a self-signed one",
    )
    args = p.parse_args()
    if (args.tenant or args.source) and not args.file:
        p.error("--tenant and --source need --file")
    if not args.token:
        args.token = env_file_value(ENV_FILE, "INGEST_TOKEN")
    if not args.token:
        p.error(f"no credential: pass --token, set LOGHUB_TOKEN, or add INGEST_TOKEN to {ENV_FILE}")
    if not args.files:
        samples = Path(sys.argv[0]).parent / "json"
        args.files = sorted(str(f) for f in samples.glob("*.json"))
        if not args.files:
            p.error(f"no FILE given, and {samples} has no .json files")
    return args


def env_file_value(path: Path, key: str) -> str | None:
    """Returns key's value in a KEY=VALUE file such as .env, if it has one."""
    try:
        lines = path.read_text().splitlines()
    except OSError:
        return None
    for line in lines:
        name, sep, value = line.partition("=")
        if sep and name.strip() == key:
            return value.strip() or None
    return None


def read(name: str, tags: list[str]) -> tuple[str, list[Record]]:
    """Returns a label for name and the events it holds."""
    label = "<stdin>" if name == "-" else name
    try:
        data = sys.stdin.buffer.read() if name == "-" else Path(name).read_bytes()
    except OSError as e:
        sys.exit(f"{PROG}: {e}")

    if name == "-" or name.endswith(".json"):
        try:
            doc = json.loads(data)
        except ValueError as e:
            # Standard input may be NDJSON rather than one document.
            if name != "-":
                sys.exit(f"{PROG}: {name}: not valid JSON ({e}); name it .ndjson for one event per line")
        else:
            if isinstance(doc, list):
                return label, [Record(f"{label}[{i}]", encode(d, tags)) for i, d in enumerate(doc)]
            return label, [Record(label, encode(doc, tags))]

    # One event per line, numbered as the server numbers them. A line is sent
    # as it is unless it needs tagging, so a malformed one reaches the server
    # and is rejected there.
    records = []
    for n, line in enumerate(data.split(b"\n"), 1):
        line = line.strip()
        if line:
            records.append(Record(f"{label}:{n}", retag(line, tags)))
    return label, records


def encode(doc, tags: list[str]) -> bytes:
    """Returns doc as one line of JSON, with tags added to its _tags."""
    if tags and isinstance(doc, dict):
        have = doc.get("_tags")
        if have is None:
            have = []
        # Any other _tags is left for the normalizer to flag.
        if isinstance(have, list):
            doc["_tags"] = have + tags
    return json.dumps(doc, separators=(",", ":")).encode()


def retag(line: bytes, tags: list[str]) -> bytes:
    if not tags:
        return line
    try:
        return encode(json.loads(line), tags)
    except ValueError:
        return line


def send(url: str, content_type: str, body: bytes, label: str, records: list[Record], token: str, ctx) -> tuple[int, int]:
    """Posts body, reports what was not stored on stderr, and returns how many
    of records were accepted and rejected."""
    status, headers, raw = post(url, content_type, body, token, ctx)
    if status in (401, 403):
        hint = "; send the ingest key (INGEST_TOKEN in .env) or an admin's token with --token"
        raise Refused(f"{label}: not stored: {failure(status, headers, raw)}{hint}")
    if status == 200:
        try:
            result = json.loads(raw)
            accepted, rejected = result["accepted"], result["rejected"]
        except (ValueError, TypeError, KeyError):
            status = None
    if status != 200:
        print(f"{label}: not stored: {failure(status, headers, raw)}", file=sys.stderr)
        return 0, len(records)

    # errors[] indexes the lines of body, which are records in order.
    errors = result.get("errors") or []
    for e in errors:
        i = e["index"]
        where = records[i].where if 0 <= i < len(records) else f"{label}, line {i + 1} sent"
        print(f"{where}: rejected: {e['code']}: {e['message']}", file=sys.stderr)
    if rejected > len(errors):
        print(f"{label}: {rejected - len(errors)} more rejected, not itemized by the server", file=sys.stderr)
    return accepted, rejected


def post(url: str, content_type: str, body: bytes, token: str, ctx):
    """Returns the status, headers and body of the response."""
    req = urllib.request.Request(
        url,
        data=body,
        method="POST",
        headers={
            "Content-Type": content_type,
            "Accept": "application/json",
            "Authorization": "Bearer " + token,
        },
    )
    try:
        with urllib.request.urlopen(req, timeout=TIMEOUT, context=ctx) as resp:
            return resp.status, resp.headers, resp.read()
    except urllib.error.HTTPError as e:
        return e.code, e.headers, e.read()
    except urllib.error.URLError as e:
        hint = ""
        if isinstance(e.reason, ssl.SSLCertVerificationError):
            hint = "; pass -k to accept a self-signed certificate"
        elif isinstance(e.reason, ConnectionRefusedError):
            hint = "; is loghub running? `make dev-up` serves the API on 127.0.0.1:8080"
        raise Unreachable(f"cannot reach {url}: {e.reason}{hint}") from e
    except OSError as e:
        raise Unreachable(f"POST {url} failed: {e}") from e


def failure(status, headers, raw: bytes) -> str:
    """Describes an unsuccessful response, from the error envelope if it has one."""
    if status is None:
        return "unexpected response: " + first_line(raw)
    if 300 <= status < 400:
        # urllib does not follow a redirect for a POST.
        return f"HTTP {status} redirect to {headers.get('Location')}; check --url"
    try:
        env = json.loads(raw)
        text = f"{env['code']}: {env['message']}"
        details = [f"{fe['field']}: {fe['message']}" for fe in env.get("errors") or []]
        request_id = env.get("request_id")
    except (ValueError, TypeError, KeyError, AttributeError):
        # Not the envelope, such as the plain-text 404 for an unrouted path.
        return f"HTTP {status}: {first_line(raw)}"
    if details:
        text += " (" + "; ".join(details) + ")"
    if request_id:
        text += f" [request {request_id}]"
    return f"HTTP {status} {text}"


def first_line(raw: bytes) -> str:
    return raw.decode(errors="replace").strip().split("\n", 1)[0] or "no detail"


if __name__ == "__main__":
    sys.exit(main())
