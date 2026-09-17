# 0004. Vector as transport only; normalization in Go

- Status: Accepted
- Date: 2026-09-16

## Context
Syslog over UDP/TCP 514 and file drops are required inputs. A hand-written listener has to handle TCP framing, backpressure, retries while the backend is down, and file tailing with checkpoints. The normalization rules for seven source types are what reviewers will look at most, and they need tests.

## Decision
- Vector 0.58 receives syslog with `socket` sources (UDP and TCP on 5514 in the container, published as 514 on the host) and reads `inbox/*.ndjson`, one JSON event per line, with a `file` source. The inbox is a drop folder: each file is deleted once its lines are in the buffer.
- VRL only wraps each syslog line with metadata (`received_at`, `peer_ip`, `input`), and inbox lines pass through byte for byte. It doesn't parse.
- Vector sends batches as NDJSON to `POST /api/v1/ingest/batch` with a bearer token. A disk buffer keeps events through backend restarts.
- The HTTP API goes straight to the backend, not through Vector: single events to `POST /api/v1/ingest`, whole files such as vendor exports to `POST /api/v1/ingest/file`.

## Consequences
- Buffering, retries, TCP framing and file checkpoints come from a proven tool instead of new code.
- Normalization lives in one place and one language, handles events from Vector and the API the same way, and is tested without running Vector.
- It adds one container and one config file to operate and explain.
- Secrets reach Vector as files through its `directory` secret backend. Vector 0.58 no longer expands environment variables in config files by default, and the opt-in flag is marked dangerous.
- Vector's `syslog` source could parse the RFC 3164/5424 header, but sample 4.1's `key=value` body needs custom parsing anyway. Forwarding raw lines keeps all parsing together.
- In the skeleton every syslog sender gets `SYSLOG_DEFAULT_TENANT`, which VRL reads with `get_env_var`. Mapping sender IPs to tenants is a later change in Go.
- Vector's HTTP sink retries 408, 429 and any 5xx without limit, and drops the batch on any other status. The batch endpoint therefore reports bad records inside a 200, and logs them, since Vector never reads the response.
- The `file` source is built for tailing logs, and tells files apart by a checksum of the first line. Its checkpoints would skip a dropped file that repeats one already read, and start a different file with the same first line part-way through, so they are ignored. Every dropped file is read in full, but a file still in the inbox when Vector restarts is read again.
- While two identical files sit in the inbox, Vector 0.58 holds back its most recent event until the next one arrives. Fingerprinting by inode instead avoids that but silently skips the start of a new file that reuses a deleted file's inode, which is worse.
- `make check-vector` validates the config and runs VRL unit tests in `ingest/vector.test.yaml`.
