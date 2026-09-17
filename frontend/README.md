# frontend

The UI and the edge that serves it: a React single-page app ([ADR 0006](../docs/adr/0006-react-vite-frontend.md)), built into a [Caddy](https://caddyserver.com) 2.11 image configured by [`Caddyfile`](Caddyfile) ([ADR 0005](../docs/adr/0005-caddy-edge.md)). [`docs/architecture.md`](../docs/architecture.md#edge-and-ui) describes both, and [`docs/setup_appliance.md`](../docs/setup_appliance.md#use-the-ui) describes the pages.

## Developing

The targets below run Node 24 or later on the host, as the backend's run Go.

```sh
make dev-up      # the whole stack, with the API on 127.0.0.1:8080
make dev-web     # this UI on http://localhost:5173, with hot reload and /api passed to 127.0.0.1:8080
make test-web    # the tests
make lint-web    # type-check, oxlint and Prettier
```

`cd frontend && npm run format` fixes formatting. The UI served by `make dev-up` on https://localhost is the one built into the image, so it changes only when `make dev-up` rebuilds it.

## Layout

| Path | What |
|---|---|
| `src/api/schema.d.ts` | Types generated from [`api/openapi.yaml`](../api/openapi.yaml) by `make gen-api`. Don't edit it. |
| `src/api/client.ts` | The typed API client. It sends the session's token and ends the session on a 401. |
| `src/api/queries.ts` | A hook per endpoint, with how often each refreshes. |
| `src/lib/filters.ts` | The filters the dashboard and search share, and how they are kept in the URL. |
| `src/pages/` | One file per route, each loaded when first visited. |
| `src/test/` | The fake API and render helper the tests use. |

## Conventions

- **API shapes** come from the spec. A change starts in `api/openapi.yaml`, then `make gen-api`, and the compiler points at every call to update. Enums have no runtime form in the generated types, so [`src/api/enums.ts`](src/api/enums.ts) lists their values, with a compile-time check that each list is complete.
- **Filters** are read from and written to the URL, under the API's parameter names, never held only in component state.
- **Times** are sent in UTC and shown in the browser's zone. The tests run in Asia/Bangkok so that a time shown in the wrong zone fails.
- **Tests** render the whole app at a URL with [`renderApp`](src/test/render.tsx) and answer its requests with [`mockApi`](src/test/api.ts). They find elements by role and name, as a screen reader would.
