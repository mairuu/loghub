# loghub: common tasks. Run `make help` for more information.

COMPOSE     := docker compose
DEV_COMPOSE := docker compose -f docker-compose.yml -f docker-compose.dev.yml
SQLC_IMAGE  := sqlc/sqlc:1.31.1
# Keep in step with docker-compose.yml.
VECTOR_IMAGE := timberio/vector:0.58.0-alpine
NODE_IMAGE  := node:24-alpine
OAPI_SPEC   := api/openapi.yaml
P2C_VERSION := 5.0.0
REDOCLY_VER := 1.34.2
API_GEN_OUT := backend/internal/api/gen

# npx needs a writable HOME once we drop to the caller's uid.
NPX := docker run --rm -u "$$(id -u):$$(id -g)" -e HOME=/tmp \
         -v "$(CURDIR):/work" -w /work $(NODE_IMAGE) npx -y

-include .env
export

DEV_DATABASE_URL         = postgres://loghub_app:$(APP_DB_PASSWORD)@127.0.0.1:5432/$(POSTGRES_DB)?sslmode=disable
DEV_MIGRATE_DATABASE_URL = postgres://$(POSTGRES_USER):$(POSTGRES_PASSWORD)@127.0.0.1:5432/$(POSTGRES_DB)?sslmode=disable

.PHONY: help env up down ps logs reset seed dev-up dev-deps dev-api send-samples \
        test test-db gen-sql check-sql gen-api check-api lint-api check-vector postman lint

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

down: ## Stop the stack (keeps data)
	$(COMPOSE) down

ps: ## Show service status
	$(COMPOSE) ps

logs: ## Follow logs; pick a service with s=backend
	$(COMPOSE) logs -f $(s)

reset: ## Stop the stack and DELETE all volumes
	@read -r -p "Delete all loghub volumes? [y/N] " ans && [ "$$ans" = y ]
	$(COMPOSE) down -v

seed: ## Create demo tenants, the admin and one viewer per tenant
	cd backend && MIGRATE_DATABASE_URL='$(DEV_MIGRATE_DATABASE_URL)' go run ./cmd/loghub seed

dev-up: env ## Build and start the stack with dev ports (API on 127.0.0.1:8080)
	$(DEV_COMPOSE) up -d --build --wait

dev-deps: env ## Start only Postgres
	$(DEV_COMPOSE) up -d --wait postgres

dev-api: ## Run the API on the host
	cd backend && MIGRATE_DATABASE_URL='$(DEV_MIGRATE_DATABASE_URL)' go run ./cmd/loghub migrate
	cd backend && DATABASE_URL='$(DEV_DATABASE_URL)' go run ./cmd/loghub serve

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

test: ## Run the Go tests; database tests are skipped
	cd backend && go test ./...

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

gen-api: ## Regenerate the API code in backend/internal/api/gen
	cd backend && go tool oapi-codegen -config oapi-codegen.models.yml ../$(OAPI_SPEC)
	cd backend && go tool oapi-codegen -config oapi-codegen.server.yml ../$(OAPI_SPEC)

# Regenerating leaves the tree untouched when the committed code is current.
check-api: gen-api ## Fail if the committed API code is stale
	@git diff --quiet -- $(API_GEN_OUT) \
	  && [ -z "$$(git ls-files --others --exclude-standard -- $(API_GEN_OUT))" ] \
	  || { echo "openapi output is stale; stage what 'make gen-api' produced:"; \
	       git status --short -- $(API_GEN_OUT); exit 1; }

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

lint: check-sql check-api lint-api check-vector ## gofmt, go vet, sqlc drift, api drift, spec lint, collector config
	@out=$$(gofmt -l backend); if [ -n "$$out" ]; then echo "Files need gofmt:"; echo "$$out"; exit 1; fi
	cd backend && go vet ./...
