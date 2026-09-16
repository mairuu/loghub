# 0001. Go single-binary backend

- Status: Accepted
- Date: 2026-09-14

## Context
The backend takes ingest traffic, serves the search, dashboard and alert APIs, run background jobs for retention and alert evaluation.

## Decision
Write the backend in Go as one binary `loghub` with the subcommands `serve`, `migrate`, `seed`, `healthcheck`

- HTTP routing use stdlib `net/http` and `http.ServeMux`
- Postgres access uses pgx v5; logs use `log/slog` (JSON).
- Background jobs are run in goroutines in the `serve` command

Alternatives: Node/TS would share the type with the frontend but would use more memory. Python/FastAPI is the fastest to write but the slowest to run. Rust is fast but hard to write and maintain. *author is familiar with Go.*

## Consequences
- A static binary on a distroless image gives a small image size, a small attack surface, and a simple deployment.
- The same image runs migrations, seeding, so Compose needs no extra tooling.
- Background jobs inside the API process are fine for one instance. Running several API replicas will require a lock so jobs are not run multiple times.