# 0006. React + Vite + TypeScript frontend

- Status: Accepted
- Date: 2026-09-16

## Context
The UI needs a login page, a dashboard (Top-N charts, a timeline, and tenant/source/time filters), a search table and an alert page. One person builds it quickly, and reviewers need to be able to read it.

## Decision
React 19 with TypeScript, built by Vite 8.

- Routing with react-router, server state with TanStack Query, charts with Recharts, styling with Tailwind CSS 4.
- API calls go through the typed openapi-fetch client generated from the spec (ADR 0007).
- oxlint and Vitest, as the create-vite template sets up, and Prettier for formatting, with its Tailwind plugin to keep class lists in one order. TypeScript stays on the template's ~6.0 until the toolchain supports 7.
- openapi-typescript (ADR 0007) still requires TypeScript 5, so it isn't a dependency of the frontend. `make gen-api` runs a pinned version with npx, as it runs the other spec tools.
- The build is static files served by Caddy (ADR 0005); no Node process runs in production.

Alternatives: Vue would take similar effort. Grafana or OpenSearch Dashboards would give charts for free, but the alert page, RBAC and tenant filters would become configuration of someone else's tool rather than code in this repo.

## Consequences
- A large ecosystem covers every chart and table need.
- No server-side rendering, which is fine for an authenticated internal tool.
- TanStack Query polling shows new events and alerts within the one-minute acceptance window without WebSockets.
