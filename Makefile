ifneq (,$(wildcard .env))
include .env
export
endif

.DEFAULT_GOAL := help

COMPOSE ?= docker compose
DB_CONTAINER ?= postgres
DB_NAME ?= course_dev_orchestrator
DB_USER ?= postgres
POSTGRES_PORT ?= 5434
HTTP_PORT ?= 8080
TEMPORAL_UI_PORT ?= 8233
GO_ENV := XDG_CACHE_HOME=$(CURDIR)/.cache GOCACHE=$(CURDIR)/.cache/go-build GOMODCACHE=$(CURDIR)/.cache/gomod GOBIN=$(CURDIR)/.cache/bin
GO_FILES := $$(find . -type f -name '*.go' -not -path './.cache/*')
COMMAND_PATH ?= /usr/local/go/bin:/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin
ifeq ($(origin PATH),command line)
PROJECT_PATH_FROM_PATH := $(PATH)
override PATH := $(COMMAND_PATH)
endif
CONNECT_PATH := $(or $(PROJECT_PATH),$(PROJECT_PATH_FROM_PATH))

.PHONY: help bootstrap up down restart ps logs migrate migrate-down migrate-status temporal-ui serve worker workflow-probe config-check project-connect project-list project-show project-scan project-report fmt fmt-check lint test test-unit test-integration verify compose-check

help: ## Show available targets
	@echo "Available targets:"
	@awk 'BEGIN {FS = ":.*## "}; /^[a-zA-Z0-9_.-]+:.*## / {printf "  %-22s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

bootstrap: ## Prepare local configuration and download Go dependencies
	@if [ ! -f .env ]; then cp .env.dist .env; echo "Created .env from .env.dist"; fi
	@mkdir -p .cache/go-build .cache/gomod .cache/bin
	$(GO_ENV) go mod download

up: ## Start PostgreSQL, Temporal, API, worker, and Temporal UI
	$(COMPOSE) up -d --build

down: ## Stop the local stack without deleting durable volumes
	$(COMPOSE) down

restart: ## Restart the local stack
	$(COMPOSE) restart

ps: ## Show local stack status
	$(COMPOSE) ps

logs: ## Follow local stack logs
	$(COMPOSE) logs -f --tail=200

migrate: ## Apply pending PostgreSQL migrations transactionally
	COMPOSE_COMMAND="$(COMPOSE)" DB_CONTAINER=$(DB_CONTAINER) DB_USER=$(DB_USER) DB_NAME=$(DB_NAME) ./scripts/migrate.sh

migrate-down: ## Roll back the most recently applied migration
	COMPOSE_COMMAND="$(COMPOSE)" DB_CONTAINER=$(DB_CONTAINER) DB_USER=$(DB_USER) DB_NAME=$(DB_NAME) ./scripts/migrate-down.sh

migrate-status: ## Show applied PostgreSQL migrations
	$(COMPOSE) exec -T $(DB_CONTAINER) psql -U $(DB_USER) -d $(DB_NAME) -c "SELECT version, applied_at FROM schema_migrations ORDER BY applied_at, version;"

temporal-ui: ## Print the Temporal UI address
	@echo "Temporal UI: http://localhost:$(TEMPORAL_UI_PORT)"

serve: ## Run the HTTP API locally
	$(GO_ENV) go run ./cmd/course-dev-orchestrator serve

worker: ## Run the Temporal worker locally
	$(GO_ENV) go run ./cmd/course-dev-orchestrator worker

workflow-probe: ## Execute the system probe workflow through Temporal
	$(GO_ENV) go run ./cmd/course-dev-orchestrator workflow-probe

config-check: ## Validate environment and print a secret-free summary
	$(GO_ENV) go run ./cmd/course-dev-orchestrator config-check

project-connect: ## Connect and scan a project (PATH=.../PROJECT_PATH=... or GIT_URL=..., optional ROLE=...)
	@if [ -n "$(CONNECT_PATH)" ] && [ -n "$(GIT_URL)" ]; then echo "Set only PATH/PROJECT_PATH or GIT_URL"; exit 2; fi
	@if [ -z "$(CONNECT_PATH)" ] && [ -z "$(GIT_URL)" ]; then echo "Set PATH/PROJECT_PATH or GIT_URL"; exit 2; fi
	$(GO_ENV) go run ./cmd/course-dev-orchestrator project-connect $(if $(CONNECT_PATH),--path "$(CONNECT_PATH)",--git-url "$(GIT_URL)") --role "$(or $(ROLE),service)"

project-list: ## List connected projects
	$(GO_ENV) go run ./cmd/course-dev-orchestrator project-list

project-show: ## Show a project by SERVICE=id-or-name
	@test -n "$(SERVICE)" || (echo "Set SERVICE=id-or-name"; exit 2)
	$(GO_ENV) go run ./cmd/course-dev-orchestrator project-show --service "$(SERVICE)"

project-scan: ## Run read-only discovery for SERVICE=id-or-name
	@test -n "$(SERVICE)" || (echo "Set SERVICE=id-or-name"; exit 2)
	$(GO_ENV) go run ./cmd/course-dev-orchestrator project-scan --service "$(SERVICE)"

project-report: ## Show latest discovery report for SERVICE=id-or-name
	@test -n "$(SERVICE)" || (echo "Set SERVICE=id-or-name"; exit 2)
	$(GO_ENV) go run ./cmd/course-dev-orchestrator project-report --service "$(SERVICE)"

fmt: ## Format Go source files
	@gofmt -w $(GO_FILES)

fmt-check: ## Check Go formatting without changing files
	@files=$$(gofmt -l $(GO_FILES)); if [ -n "$$files" ]; then echo "Unformatted files:"; echo "$$files"; exit 1; fi

lint: ## Run Go static analysis
	$(GO_ENV) go vet ./...

test: test-unit ## Run the default test suite

test-unit: ## Run unit and Temporal workflow tests
	$(GO_ENV) go test ./...

test-integration: ## Run PostgreSQL integration tests against the local stack
	DATABASE_URL="$${INTEGRATION_DATABASE_URL:-postgres://$(DB_USER):$${POSTGRES_PASSWORD:-postgres}@localhost:$(POSTGRES_PORT)/$(DB_NAME)?sslmode=disable}" $(GO_ENV) go test -count=1 -tags=integration ./test/integration/...

compose-check: ## Validate Docker Compose configuration
	$(COMPOSE) config --quiet

verify: fmt-check lint test-unit compose-check ## Run all Stage 1 non-destructive checks
