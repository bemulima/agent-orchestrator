ifneq (,$(wildcard .env))
include .env
export
endif

.DEFAULT_GOAL := help

COMPOSE ?= docker compose
ORCHESTRATOR_CLI := $(COMPOSE) exec -T orchestrator /app/course-dev-orchestrator
DB_CONTAINER ?= postgres
DB_NAME ?= course_dev_orchestrator
DB_USER ?= postgres
POSTGRES_PORT ?= 5434
HTTP_PORT ?= 8080
TEMPORAL_UI_PORT ?= 8233
DATABASE_URL ?= postgres://$(DB_USER):$(or $(POSTGRES_PASSWORD),postgres)@localhost:$(POSTGRES_PORT)/$(DB_NAME)?sslmode=disable
CODEX_HOST_AUTH_FILE ?= $(HOME)/.codex/auth.json
GO_ENV := XDG_CACHE_HOME=$(CURDIR)/.cache GOCACHE=$(CURDIR)/.cache/go-build GOMODCACHE=$(CURDIR)/.cache/gomod GOBIN=$(CURDIR)/.cache/bin
GO_FILES := $$(find . -type f -name '*.go' -not -path './.cache/*')
COMMAND_PATH ?= /usr/local/go/bin:/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin
ifeq ($(origin PATH),command line)
PROJECT_PATH_FROM_PATH := $(PATH)
override PATH := $(COMMAND_PATH)
endif
CONNECT_PATH := $(or $(PROJECT_PATH),$(PROJECT_PATH_FROM_PATH))

.PHONY: help bootstrap up down restart ps logs migrate migrate-down migrate-status temporal-ui serve worker workflow-probe telegram config-check codex-auth-sync codex-auth-status project-connect project-list project-show project-scan project-report project-onboard project-enrich project-diff project-approve project-reject project-apply topology contracts contract-drift dependencies consumers plan plan-show plan-comment plan-issues plan-submit plan-approve plan-reject plan-publish-issues plan-run run-status run-pause run-resume run-cancel task-show task-log task-retry task-cancel task-pr-prepare task-pr-publish gitlab-sync gitlab-links fmt fmt-check lint test test-unit test-integration mvp-rehearsal runner-test verify compose-check

help: ## Show available targets
	@echo "Available targets:"
	@awk 'BEGIN {FS = ":.*## "}; /^[a-zA-Z0-9_.-]+:.*## / {printf "  %-22s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

bootstrap: ## Prepare local configuration and download Go dependencies
	@if [ ! -f .env ]; then cp .env.dist .env; echo "Created .env from .env.dist"; fi
	@mkdir -p .cache/go-build .cache/gomod .cache/bin
	$(GO_ENV) go mod download
	cd runner && npm ci

up: ## Start PostgreSQL, Temporal, API, worker, and Temporal UI
	$(COMPOSE) up -d --build
	@if [ -f "$(CODEX_HOST_AUTH_FILE)" ]; then \
		COMPOSE_COMMAND="$(COMPOSE)" ./scripts/sync-codex-auth.sh "$(CODEX_HOST_AUTH_FILE)"; \
	else \
		echo "Codex CLI login is not configured; run 'codex login' and 'make codex-auth-sync' before executing plans"; \
	fi

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

telegram: ## Run Telegram long polling, or configure the webhook when TELEGRAM_WEBHOOK_URL is set
	$(GO_ENV) go run ./cmd/course-dev-orchestrator telegram

config-check: ## Validate environment and print a secret-free summary
	$(GO_ENV) go run ./cmd/course-dev-orchestrator config-check

codex-auth-sync: ## Copy the existing local codex-cli ChatGPT login into the worker volume
	COMPOSE_COMMAND="$(COMPOSE)" ./scripts/sync-codex-auth.sh "$(CODEX_HOST_AUTH_FILE)"

codex-auth-status: ## Check codex-cli login inside the worker
	$(COMPOSE) exec -T worker /bin/sh -c 'set -eu; codex_bin=$$(find /app/runner/node_modules/@openai -type f -path "*/vendor/*/bin/codex" | head -n 1); test -n "$$codex_bin"; "$$codex_bin" login status'

project-connect: ## Connect and scan a project (PATH=/projects/... or GIT_URL=..., optional ROLE=...)
	@if [ -n "$(CONNECT_PATH)" ] && [ -n "$(GIT_URL)" ]; then echo "Set only PATH/PROJECT_PATH or GIT_URL"; exit 2; fi
	@if [ -z "$(CONNECT_PATH)" ] && [ -z "$(GIT_URL)" ]; then echo "Set PATH/PROJECT_PATH or GIT_URL"; exit 2; fi
	$(ORCHESTRATOR_CLI) project-connect $(if $(CONNECT_PATH),--path "$(CONNECT_PATH)",--git-url "$(GIT_URL)") --role "$(or $(ROLE),service)"

project-list: ## List connected projects
	$(ORCHESTRATOR_CLI) project-list

project-show: ## Show a project by SERVICE=id-or-name
	@test -n "$(SERVICE)" || (echo "Set SERVICE=id-or-name"; exit 2)
	$(ORCHESTRATOR_CLI) project-show --service "$(SERVICE)"

project-scan: ## Run read-only discovery for SERVICE=id-or-name
	@test -n "$(SERVICE)" || (echo "Set SERVICE=id-or-name"; exit 2)
	$(ORCHESTRATOR_CLI) project-scan --service "$(SERVICE)"

project-report: ## Show latest discovery report for SERVICE=id-or-name
	@test -n "$(SERVICE)" || (echo "Set SERVICE=id-or-name"; exit 2)
	$(ORCHESTRATOR_CLI) project-report --service "$(SERVICE)"

project-onboard: ## Prepare onboarding proposal for SERVICE=id-or-name (optional DRY_RUN=true)
	@test -n "$(SERVICE)" || (echo "Set SERVICE=id-or-name"; exit 2)
	$(ORCHESTRATOR_CLI) project-onboard --service "$(SERVICE)" $(if $(filter true 1 yes,$(DRY_RUN)),--dry-run,)

project-enrich: ## Prepare a read-only Codex semantic onboarding proposal for SERVICE=id-or-name
	@test -n "$(SERVICE)" || (echo "Set SERVICE=id-or-name"; exit 2)
	$(ORCHESTRATOR_CLI) project-enrich --service "$(SERVICE)"

project-diff: ## Print proposal diff for RUN_ID=uuid
	@test -n "$(RUN_ID)" || (echo "Set RUN_ID=uuid"; exit 2)
	$(ORCHESTRATOR_CLI) project-diff --run-id "$(RUN_ID)"

project-approve: ## Approve RUN_ID=uuid (optional ACTOR=... COMMENT=...)
	@test -n "$(RUN_ID)" || (echo "Set RUN_ID=uuid"; exit 2)
	$(ORCHESTRATOR_CLI) project-approve --run-id "$(RUN_ID)" --actor "$(or $(ACTOR),owner)" $(if $(COMMENT),--comment "$(COMMENT)",)

project-reject: ## Reject RUN_ID=uuid (optional ACTOR=... COMMENT=...)
	@test -n "$(RUN_ID)" || (echo "Set RUN_ID=uuid"; exit 2)
	$(ORCHESTRATOR_CLI) project-reject --run-id "$(RUN_ID)" --actor "$(or $(ACTOR),owner)" $(if $(COMMENT),--comment "$(COMMENT)",)

project-apply: ## Apply approved RUN_ID=uuid in an isolated worktree (optional DRY_RUN=true)
	@test -n "$(RUN_ID)" || (echo "Set RUN_ID=uuid"; exit 2)
	$(ORCHESTRATOR_CLI) project-apply --run-id "$(RUN_ID)" $(if $(filter true 1 yes,$(DRY_RUN)),--dry-run,)

topology: ## Rebuild and print the materialized service topology
	$(GO_ENV) go run ./cmd/course-dev-orchestrator topology

contracts: ## List discovered contracts from the current topology
	$(GO_ENV) go run ./cmd/course-dev-orchestrator contracts

contract-drift: ## List producer/consumer contract drift
	$(GO_ENV) go run ./cmd/course-dev-orchestrator contract-drift

dependencies: ## Show dependencies and impact for SERVICE=id-or-name
	@test -n "$(SERVICE)" || (echo "Set SERVICE=id-or-name"; exit 2)
	$(GO_ENV) go run ./cmd/course-dev-orchestrator dependencies --service "$(SERVICE)"

consumers: ## Show direct and transitive consumers for SERVICE=id-or-name
	@test -n "$(SERVICE)" || (echo "Set SERVICE=id-or-name"; exit 2)
	$(GO_ENV) go run ./cmd/course-dev-orchestrator consumers --service "$(SERVICE)"

plan: ## Create discussion plan from FILE (optional PROJECT_IDS and SOURCE_ISSUES=github:id:number)
	@test -n "$(FILE)" || (echo "Set FILE=path-to-command.md"; exit 2)
	$(GO_ENV) go run ./cmd/course-dev-orchestrator plan --file "$(FILE)" $(if $(PROJECT_IDS),--project-ids "$(PROJECT_IDS)",) $(if $(SOURCE_ISSUES),--source-issues "$(SOURCE_ISSUES)",)

plan-show: ## Show PLAN_ID=uuid with tasks and dependencies
	@test -n "$(PLAN_ID)" || (echo "Set PLAN_ID=uuid"; exit 2)
	$(GO_ENV) go run ./cmd/course-dev-orchestrator plan-show --plan-id "$(PLAN_ID)"

plan-comment: ## Add COMMENT to discussion of PLAN_ID=uuid
	@test -n "$(PLAN_ID)" || (echo "Set PLAN_ID=uuid"; exit 2)
	@test -n "$(COMMENT)" || (echo "Set COMMENT=text"; exit 2)
	$(GO_ENV) go run ./cmd/course-dev-orchestrator plan-comment --plan-id "$(PLAN_ID)" --actor "$(or $(ACTOR),owner)" --comment "$(COMMENT)"

plan-issues: ## Ask issue-manage-agent to prepare Russian issue proposals for PLAN_ID=uuid
	@test -n "$(PLAN_ID)" || (echo "Set PLAN_ID=uuid"; exit 2)
	$(GO_ENV) go run ./cmd/course-dev-orchestrator plan-issues --plan-id "$(PLAN_ID)"

plan-submit: ## Freeze PLAN_ID=uuid issue-backed version for approval
	@test -n "$(PLAN_ID)" || (echo "Set PLAN_ID=uuid"; exit 2)
	$(GO_ENV) go run ./cmd/course-dev-orchestrator plan-submit --plan-id "$(PLAN_ID)" --actor "$(or $(ACTOR),owner)" $(if $(COMMENT),--comment "$(COMMENT)",)

plan-approve: ## Approve exact PLAN_ID=uuid FINGERPRINT=sha256:... (optional ACTOR/COMMENT)
	@test -n "$(PLAN_ID)" || (echo "Set PLAN_ID=uuid"; exit 2)
	@test -n "$(FINGERPRINT)" || (echo "Set FINGERPRINT=sha256:..."; exit 2)
	$(GO_ENV) go run ./cmd/course-dev-orchestrator plan-approve --plan-id "$(PLAN_ID)" --fingerprint "$(FINGERPRINT)" --actor "$(or $(ACTOR),owner)" $(if $(COMMENT),--comment "$(COMMENT)",)

plan-reject: ## Reject PLAN_ID=uuid (optional ACTOR=... COMMENT=...)
	@test -n "$(PLAN_ID)" || (echo "Set PLAN_ID=uuid"; exit 2)
	$(GO_ENV) go run ./cmd/course-dev-orchestrator plan-reject --plan-id "$(PLAN_ID)" --actor "$(or $(ACTOR),owner)" $(if $(COMMENT),--comment "$(COMMENT)",)

plan-publish-issues: ## Publish approved PLAN_ID issues (preview while GITHUB_DRY_RUN=true)
	@test -n "$(PLAN_ID)" || (echo "Set PLAN_ID=uuid"; exit 2)
	$(GO_ENV) go run ./cmd/course-dev-orchestrator plan-publish-issues --plan-id "$(PLAN_ID)"

plan-run: ## Start or reuse the Temporal workflow for PLAN_ID=uuid
	@test -n "$(PLAN_ID)" || (echo "Set PLAN_ID=uuid"; exit 2)
	$(GO_ENV) go run ./cmd/course-dev-orchestrator plan-run --plan-id "$(PLAN_ID)"

run-status: ## Show RUN_ID=uuid
	@test -n "$(RUN_ID)" || (echo "Set RUN_ID=uuid"; exit 2)
	$(GO_ENV) go run ./cmd/course-dev-orchestrator run-status --run-id "$(RUN_ID)"

run-pause: ## Pause new task dispatch for RUN_ID=uuid
	@test -n "$(RUN_ID)" || (echo "Set RUN_ID=uuid"; exit 2)
	$(GO_ENV) go run ./cmd/course-dev-orchestrator run-pause --run-id "$(RUN_ID)"

run-resume: ## Resume task dispatch for RUN_ID=uuid
	@test -n "$(RUN_ID)" || (echo "Set RUN_ID=uuid"; exit 2)
	$(GO_ENV) go run ./cmd/course-dev-orchestrator run-resume --run-id "$(RUN_ID)"

run-cancel: ## Cancel RUN_ID=uuid and unfinished tasks
	@test -n "$(RUN_ID)" || (echo "Set RUN_ID=uuid"; exit 2)
	$(GO_ENV) go run ./cmd/course-dev-orchestrator run-cancel --run-id "$(RUN_ID)"

task-show: ## Show TASK_ID=uuid
	@test -n "$(TASK_ID)" || (echo "Set TASK_ID=uuid"; exit 2)
	$(GO_ENV) go run ./cmd/course-dev-orchestrator task-show --task-id "$(TASK_ID)"

task-pr-prepare: ## Ask PR manager to prepare a Russian draft PR proposal for TASK_ID=uuid
	@test -n "$(TASK_ID)" || (echo "Set TASK_ID=uuid"; exit 2)
	$(GO_ENV) go run ./cmd/course-dev-orchestrator task-pr-prepare --task-id "$(TASK_ID)"

task-pr-publish: ## Publish WORK_ITEM_ID=uuid (preview while GITHUB_DRY_RUN=true)
	@test -n "$(WORK_ITEM_ID)" || (echo "Set WORK_ITEM_ID=uuid"; exit 2)
	$(GO_ENV) go run ./cmd/course-dev-orchestrator task-pr-publish --work-item-id "$(WORK_ITEM_ID)"

task-log: ## Show attempts and artifacts for TASK_ID=uuid
	@test -n "$(TASK_ID)" || (echo "Set TASK_ID=uuid"; exit 2)
	$(GO_ENV) go run ./cmd/course-dev-orchestrator task-log --task-id "$(TASK_ID)"

task-retry: ## Retry blocked or changes-requested TASK_ID=uuid
	@test -n "$(TASK_ID)" || (echo "Set TASK_ID=uuid"; exit 2)
	$(GO_ENV) go run ./cmd/course-dev-orchestrator task-retry --task-id "$(TASK_ID)"

task-cancel: ## Signal cancellation for TASK_ID=uuid
	@test -n "$(TASK_ID)" || (echo "Set TASK_ID=uuid"; exit 2)
	$(GO_ENV) go run ./cmd/course-dev-orchestrator task-cancel --task-id "$(TASK_ID)"

gitlab-sync: ## Preview legacy GitLab synchronization (real writes are disabled)
	@test -n "$(PLAN_ID)" || (echo "Set PLAN_ID=uuid"; exit 2)
	$(GO_ENV) go run ./cmd/course-dev-orchestrator gitlab-sync --plan-id "$(PLAN_ID)"

gitlab-links: ## Show persisted GitLab links for PLAN_ID=uuid
	@test -n "$(PLAN_ID)" || (echo "Set PLAN_ID=uuid"; exit 2)
	$(GO_ENV) go run ./cmd/course-dev-orchestrator gitlab-links --plan-id "$(PLAN_ID)"

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

mvp-rehearsal: ## Run the disposable full-lifecycle MVP rehearsal (requires an empty project database)
	DATABASE_URL="$${INTEGRATION_DATABASE_URL:-postgres://$(DB_USER):$${POSTGRES_PASSWORD:-postgres}@localhost:$(POSTGRES_PORT)/$(DB_NAME)?sslmode=disable}" \
	TEMPORAL_HOST_PORT="$${TEMPORAL_HOST_PORT:-localhost:7233}" \
	TEMPORAL_NAMESPACE="$${TEMPORAL_NAMESPACE:-default}" \
	MVP_RESTART_COMPOSE=true \
	MVP_COMPOSE_FILE="$(abspath docker-compose.yml)" \
	$(GO_ENV) go test -count=1 -timeout=7m -tags=mvp ./test/mvp/...

runner-test: ## Build and test the pinned Codex SDK runner
	cd runner && npm test

compose-check: ## Validate Docker Compose configuration
	$(COMPOSE) config --quiet

verify: fmt-check lint test-unit runner-test compose-check ## Run all non-destructive checks
