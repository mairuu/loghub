# 0007. Spec-first OpenAPI with code generation

- Status: Accepted
- Date: 2026-09-16

## Context
The Go backend and React frontend must agree on request and response shapes for around 15 endpoints (ingest, search, dashboard, auth, alerts). Hand-maintained types on both sides drift. The deliverables include a Postman or Insomnia collection, and reviewers benefit from browsable API docs.

## Decision
`api/openapi.yaml` (OpenAPI 3.0.3) is written first, and code is generated from it.

- **Go:** oapi-codegen v2 generates request/response models and a `ServerInterface` for the `std-http-server` target, into `internal/api/gen`. It matches the stdlib `http.ServeMux` routing of ADR 0001: the generated registration function wires each operation to a handler method, so an endpoint added to the spec won't compile until a handler exists for it.
- **TypeScript:** openapi-typescript generates `src/api/schema.d.ts`, which the openapi-fetch client of ADR 0006 consumes. Paths, path/query parameters and bodies are typed at each call site, with no hand-written request types.
- Auth is declared in the spec as `securitySchemes`: a bearer JWT for the UI and API, and the separate bearer token Vector uses for `/api/v1/ingest/batch` (ADR 0004). Endpoints carry the scheme they require, so the docs and the collection show it.
- The backend serves the spec at `/api/openapi.json` and a Scalar reference page at `/api/docs`.
- `make postman` converts the spec into `docs/postman_collection.json`.
- Generated code is committed. `make gen-api` regenerates it, and `make lint` fails on drift, alongside the same check for sqlc (ADR 0008).

Not adopted, because each adds friction or runtime cost for little gain here: strict-server mode, whose extra indirection buys little over handlers that already return typed responses; request-validation middleware, since the spec's types plus handler checks already cover it; and a generated Go client, which nothing in this repo consumes.

## Consequences
- An API change starts in YAML, then `make gen-api`. The Go and TypeScript compilers point at every handler and frontend call that needs updating.
- The Postman collection and API docs come for free and can't go stale.
- YAML is verbose. Shared shapes live in `components` to limit repetition.
- Generation only guarantees shapes. Business-rule validation stays in the Go handlers, and so does anything the spec can't express, such as tenant checks.
- The spec stays on 3.0.3, the version oapi-codegen fully supports.
