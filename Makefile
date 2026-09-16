# loghub: common tasks. Run `make help` for more information.

COMPOSE     := docker compose
DEV_COMPOSE := docker compose -f docker-compose.yml -f docker-compose.dev.yml
SQLC_IMAGE  := sqlc/sqlc:1.31.1

-include .env
export

DEV_DATABASE_URL         = postgres://loghub_app:$(APP_DB_PASSWORD)@127.0.0.1:5432/$(POSTGRES_DB)?sslmode=disable
DEV_MIGRATE_DATABASE_URL = postgres://$(POSTGRES_USER):$(POSTGRES_PASSWORD)@127.0.0.1:5432/$(POSTGRES_DB)?sslmode=disable

.PHONY: help env up down ps logs reset seed dev-up dev-deps dev-api gen-sql check-sql lint

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

gen-sql: ## Regenerate the sqlc code in backend/internal/store/gen
	docker run --rm -u "$$(id -u):$$(id -g)" -v "$(CURDIR)/backend:/src" -w /src $(SQLC_IMAGE) generate

# Regenerating leaves the tree untouched when the committed code is current.
check-sql: gen-sql ## Fail if the committed sqlc code is stale
	@git diff --quiet -- backend/internal/store/gen \
	  && [ -z "$$(git ls-files --others --exclude-standard -- backend/internal/store/gen)" ] \
	  || { echo "sqlc output is stale; stage what 'make gen-sql' produced:"; \
	       git status --short -- backend/internal/store/gen; exit 1; }

lint: check-sql ## gofmt, go vet, sqlc drift
	@out=$$(gofmt -l backend); if [ -n "$$out" ]; then echo "Files need gofmt:"; echo "$$out"; exit 1; fi
	cd backend && go vet ./...
