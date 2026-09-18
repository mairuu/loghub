# loghub: common tasks. Run `make help` for more information.

COMPOSE     := docker compose
DEV_COMPOSE := docker compose -f docker-compose.yml -f docker-compose.dev.yml
SQLC_IMAGE  := sqlc/sqlc:1.31.1
# Keep in step with docker-compose.yml.
VECTOR_IMAGE := timberio/vector:0.58.0-alpine
# Keep in step with frontend/Dockerfile.
CADDY_IMAGE := caddy:2.11.4-alpine
NODE_IMAGE  := node:24-alpine
OAPI_SPEC   := api/openapi.yaml
P2C_VERSION := 5.0.0
REDOCLY_VER := 1.34.2
API_GEN_OUT := backend/internal/api/gen
# openapi-typescript runs outside the frontend: it still wants TypeScript 5.
OAPI_TS_VER := 7.13.0
WEB_API_OUT := frontend/src/api/schema.d.ts
# npm writes this on every install, so it is newer than the lockfile once
# the lockfile's dependencies are in place.
WEB_DEPS    := frontend/node_modules/.package-lock.json

# npx needs a writable HOME once we drop to the caller's uid.
NPX := docker run --rm -u "$$(id -u):$$(id -g)" -e HOME=/tmp \
         -v "$(CURDIR):/work" -w /work $(NODE_IMAGE) npx -y

-include .env
export

DEV_DATABASE_URL         = postgres://loghub_app:$(APP_DB_PASSWORD)@127.0.0.1:5432/$(POSTGRES_DB)?sslmode=disable
DEV_MIGRATE_DATABASE_URL = postgres://$(POSTGRES_USER):$(POSTGRES_PASSWORD)@127.0.0.1:5432/$(POSTGRES_DB)?sslmode=disable

.PHONY: help env up down ps logs reset ca seed dev-up dev-deps dev-api dev-web web-deps send-samples \
        simulate acceptance test test-web test-db gen-sql check-sql gen-api check-api lint-api check-vector check-caddy \
        lint-web postman lint

help: ## Show this help
	@awk 'BEGIN {FS = ":.*## "} /^[a-z-]+:.*## / {printf "  \033[36m%-13s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

env: ## Create .env from .env.example with generated secrets (never overwrites)
	@if [ -f .env ]; then echo ".env already exists; leaving it alone"; exit 0; fi; \
	while IFS= read -r line || [ -n "$$line" ]; do \
	  case "$$line" in \
	    *=change-me) echo "$${line%change-me}$$(openssl rand -hex 24)" ;; \
	    *) echo "$$line" ;; \
	  esac; \
	done < .env.example > .env; \
	echo "Wrote .env with generated secrets"

up: env ## Build and start the appliance, then wait until it's healthy
	$(COMPOSE) up -d --build --wait

# Every profile is named, so that the simulator, Prometheus and Grafana stop
# too, even once COMPOSE_PROFILES no longer names theirs.
down: ## Stop the stack (keeps data)
	$(COMPOSE) --profile '*' down

ps: ## Show service status
	$(COMPOSE) ps

logs: ## Follow logs; pick a service with s=backend
	$(COMPOSE) logs -f $(s)

ca: ## Save the root certificate Caddy signs localhost with to loghub-root-ca.crt, for browsers to trust
	$(COMPOSE) cp caddy:/data/caddy/pki/authorities/local/root.crt loghub-root-ca.crt
	@echo "Wrote loghub-root-ca.crt; docs/setup_appliance.md says how to trust it"

reset: ## Stop the stack and DELETE all volumes
	@read -r -p "Delete all loghub volumes? [y/N] " ans && [ "$$ans" = y ]
	$(COMPOSE) --profile '*' down -v

seed: ## Create demo tenants, the admin and one viewer per tenant
	cd backend && MIGRATE_DATABASE_URL='$(DEV_MIGRATE_DATABASE_URL)' go run ./cmd/loghub seed

dev-up: env ## Build and start the stack with dev ports (API on 127.0.0.1:8080)
	$(DEV_COMPOSE) up -d --build --wait

dev-deps: env ## Start only Postgres
	$(DEV_COMPOSE) up -d --wait postgres

dev-api: ## Run the API on the host
	cd backend && MIGRATE_DATABASE_URL='$(DEV_MIGRATE_DATABASE_URL)' go run ./cmd/loghub migrate
	cd backend && DATABASE_URL='$(DEV_DATABASE_URL)' go run ./cmd/loghub serve

# The frontend targets run Node on the host, as the Go ones run Go.
$(WEB_DEPS): frontend/package.json frontend/package-lock.json
	cd frontend && npm ci --no-audit --no-fund

web-deps: $(WEB_DEPS) ## Install the frontend's dependencies (Node 24 or later)

dev-web: $(WEB_DEPS) ## Serve the UI on http://localhost:5173, against the API of dev-up or dev-api
	cd frontend && npm run dev

# The firewall lines go over UDP and the router lines over TCP, so both
# listeners are used. The JSON records are tagged with the run, which also
# keeps two runs' files from looking identical to Vector, and are written under
# a temporary name first so Vector never reads a half-written file.
send-samples: ## Send samples/ through the collector: syslog to port 514, JSON via inbox/
	@samples/send_syslog.sh --udp samples/syslog/firewall.log
	@samples/send_syslog.sh --tcp samples/syslog/network.log
	@run=samples-$$(date +%Y%m%d-%H%M%S); \
	  jq -c --arg run "$$run" '._tags = ((._tags // []) + [$$run])' samples/json/*.json > inbox/.$$run.tmp && \
	  mv inbox/.$$run.tmp inbox/$$run.ndjson && \
	  echo "Sent samples/syslog to port 514, and samples/json to inbox/ tagged $$run"

simulate: ## Send made-up traffic until interrupted; backfill=24h fills the last day first, url=https://... sends to another stack
	samples/simulate.py $(if $(backfill),--backfill '$(backfill)') $(if $(url),--url '$(url)')

# Leaves a few tagged events, and creates the sample alert rule if demoA has
# none like it; tests/README.md has the details.
acceptance: ## Check the running stack against the acceptance criteria; another one with url=https://...
	tests/acceptance.py $(if $(url),--url '$(url)')

test: test-web ## Run the Go and frontend tests; database tests are skipped
	cd backend && go test ./...

test-web: $(WEB_DEPS) ## Run the frontend tests
	cd frontend && npm test

# Each test package creates and drops its own scratch database, so the dev
# data is untouched.
test-db: dev-deps ## Run the Go tests, database tests included, against the dev Postgres
	@echo "go test ./... with TEST_DATABASE_URL set to the dev Postgres"
	@cd backend && TEST_DATABASE_URL='$(DEV_MIGRATE_DATABASE_URL)' go test ./...

gen-sql: ## Regenerate the sqlc code in backend/internal/store/gen
	docker run --rm -u "$$(id -u):$$(id -g)" -v "$(CURDIR)/backend:/src" -w /src $(SQLC_IMAGE) generate

# Regenerating leaves the tree untouched when the committed code is current.
check-sql: gen-sql ## Fail if the committed sqlc code is stale
	@git diff --quiet -- backend/internal/store/gen \
	  && [ -z "$$(git ls-files --others --exclude-standard -- backend/internal/store/gen)" ] \
	  || { echo "sqlc output is stale; stage what 'make gen-sql' produced:"; \
	       git status --short -- backend/internal/store/gen; exit 1; }

gen-api: ## Regenerate the API code in backend/internal/api/gen and frontend/src/api
	cd backend && go tool oapi-codegen -config oapi-codegen.models.yml ../$(OAPI_SPEC)
	cd backend && go tool oapi-codegen -config oapi-codegen.server.yml ../$(OAPI_SPEC)
	$(NPX) openapi-typescript@$(OAPI_TS_VER) $(OAPI_SPEC) -o $(WEB_API_OUT)

# Regenerating leaves the tree untouched when the committed code is current.
check-api: gen-api ## Fail if the committed API code is stale
	@git diff --quiet -- $(API_GEN_OUT) $(WEB_API_OUT) \
	  && [ -z "$$(git ls-files --others --exclude-standard -- $(API_GEN_OUT) $(WEB_API_OUT))" ] \
	  || { echo "openapi output is stale; stage what 'make gen-api' produced:"; \
	       git status --short -- $(API_GEN_OUT) $(WEB_API_OUT); exit 1; }

# Not part of `lint`: the converter stamps a random info._postman_id on every
# run, so the output can never be drift-checked. Regenerate before a release.
# docs/postman.jq adds the credential variables and the sign-in script.
postman: ## Convert the spec into docs/postman_collection.json
	$(NPX) openapi-to-postmanv2@$(P2C_VERSION) -s $(OAPI_SPEC) \
	  -o docs/postman_collection.json -p \
	  -O folderStrategy=Tags,requestParametersResolution=Example
	jq --indent 4 -f docs/postman.jq docs/postman_collection.json > docs/postman_collection.json.tmp
	mv docs/postman_collection.json.tmp docs/postman_collection.json

lint-api: ## Validate api/openapi.yaml itself
	$(NPX) @redocly/cli@$(REDOCLY_VER) lint $(OAPI_SPEC)

# The tests resolve every SECRET[], so they get a placeholder token.
VECTOR_CHECK := docker run --rm -e SYSLOG_DEFAULT_TENANT=t_test \
  -v "$(CURDIR)/ingest:/etc/vector:ro" \
  -v "$(CURDIR)/ingest/testdata/secrets:/run/secrets:ro" $(VECTOR_IMAGE)

check-vector: ## Validate ingest/vector.yaml and run its unit tests
	$(VECTOR_CHECK) validate --no-environment /etc/vector/vector.yaml
	$(VECTOR_CHECK) test /etc/vector/vector.yaml /etc/vector/vector.test.yaml

check-caddy: ## Check frontend/Caddyfile's formatting and validate it
	docker run --rm -e SITE_ADDRESS=localhost -v "$(CURDIR)/frontend/Caddyfile:/etc/caddy/Caddyfile:ro" $(CADDY_IMAGE) \
	  sh -c 'caddy fmt --diff /etc/caddy/Caddyfile && caddy validate --config /etc/caddy/Caddyfile --adapter caddyfile'

lint-web: $(WEB_DEPS) ## Type-check, lint and format-check the frontend
	cd frontend && npm run typecheck && npm run lint

lint: check-sql check-api lint-api check-vector check-caddy lint-web ## gofmt, go vet, generated code drift, spec lint, collector and edge config, frontend
	@out=$$(gofmt -l backend); if [ -n "$$out" ]; then echo "Files need gofmt:"; echo "$$out"; exit 1; fi
	cd backend && go vet ./...
