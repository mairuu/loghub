# ingest

The collector: [Vector](https://vector.dev) 0.58, configured by [`vector.yaml`](vector.yaml). It is transport only ([ADR 0004](../docs/adr/0004-vector-transport-only.md)):

- It receives syslog on port 514, over UDP and TCP.
- It reads NDJSON files dropped into [`inbox/`](../inbox), then deletes them.
- It keeps everything in a disk buffer and forwards it in batches to `POST /api/v1/ingest/batch`.

Vector doesn't parse anything. Each syslog line is wrapped as `{"tenant", "message", "received_at", "peer_ip", "input"}`, and each inbox line is forwarded byte for byte. The backend normalizes both, exactly as it does for its HTTP endpoints.

## Sending syslog

```sh
samples/send_syslog.sh                              # samples/syslog/*.log, over UDP
samples/send_syslog.sh --tcp samples/syslog/network.log
echo '<134>Aug 20 12:44:56 fw01 action=deny src=10.0.1.10' | samples/send_syslog.sh -
logger -n 127.0.0.1 -P 514 -t myapp "user=alice action=deny"   # RFC 5424; add -T for TCP
```

- **Tenant:** every sender's events are stored under `SYSLOG_DEFAULT_TENANT` from `.env` (default `demoA`). The tenant must exist, or each line is rejected with `unknown_tenant`.
- **UDP:** one message per datagram. A trailing line ending is dropped.
- **TCP:** one message per line, or octet-counted as in RFC 6587. A message may be at most 64 KiB.
- **Sender address:** the backend uses it as the event's `host` when the syslog header has none. Vector sees the address the packet arrives from, so a sender on the Docker host itself shows up as the Docker bridge gateway, such as `172.17.0.1`, not `127.0.0.1`.

## Dropping files

An inbox file holds one JSON event per line, as in the samples, and its name ends in `.ndjson`. Each line may be at most 1 MiB. Records must carry their own `tenant` and `source`; unlike `POST /api/v1/ingest/file`, the inbox has no defaults.

```sh
jq -c . samples/json/aws_cloudtrail.json > inbox/.aws.tmp && mv inbox/.aws.tmp inbox/aws.ndjson
make send-samples                                   # the syslog and JSON samples together
```

- **Move complete files in.** Write the file under a name that doesn't end in `.ndjson`, then rename it, as above. Vector deletes a file 10 to 20 seconds after it stops growing, so a slow writer can lose the end of its file.
- **Every file is read in full,** even one identical to a file read before. If Vector restarts while a file is still in the inbox, that file is read again from the start, and its events may be stored twice.
- **Don't drop identical files together.** While two files with the same content sit in the inbox at once, Vector 0.58 holds back the most recent event it received, from any source, until another one arrives. `make send-samples` avoids this by tagging each run's records, which also makes them searchable with `?tag=samples-…`.

## Checking delivery

With `make dev-up`, the API is at `http://127.0.0.1:8080`:

```sh
curl -s 'http://127.0.0.1:8080/api/v1/events?source=firewall&limit=5' | jq
docker compose logs backend | grep 'records rejected'
docker compose logs vector
```

Vector never reads the backend's response, so a record the backend rejects appears only in the backend log. Each `records rejected` line gives the request's counts by code and its first rejection.

In the Vector log:

- **`Retrying after error` or `Retrying after response`:** the backend is down or answered 5xx, 408 or 429. The batch is retried until it succeeds.
- **`Events dropped`:** the backend answered with any other status, and the batch is gone. The backend only does that when a whole request is unusable, so this points to a configuration problem.
- **`Source has acknowledgements enabled by a sink` at startup:** expected. The socket sources can't acknowledge.

## Delivery

- **Backend unavailable:** events wait in the disk buffer (the `vector_data` volume, 256 MiB), which survives Vector restarts.
- **Buffer full:** Vector stops reading. TCP senders slow down, UDP datagrams are lost, and inbox files wait.
- **Inbox files:** a file is deleted only once every line is in the buffer.
- **Authentication:** there is none yet. The batch endpoint takes no token until authentication lands ([ADR 0009](../docs/adr/0009-auth-jwt-rbac.md)). The token will then reach Vector through the `compose` secret backend.

## Changing the configuration

```sh
make check-vector                                   # validates vector.yaml and runs vector.test.yaml
docker compose up -d --force-recreate vector
```

Vector 0.58 doesn't expand `${VARIABLES}` in config files. A plain setting can be read in VRL with `get_env_var`, as the syslog tenant is. A secret belongs in a file under `/run/secrets`, referenced as `SECRET[compose.<name>]`.
