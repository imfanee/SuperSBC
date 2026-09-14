SHELL := /bin/bash
export PATH := $(PATH):/usr/local/go/bin:$(HOME)/go/bin
COMPOSE ?= docker compose
GO ?= go
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

.PHONY: help up down logs ps build test test-unit test-integration lint lint-go lint-lua lint-web lint-emdash seed e2e reconcile fmt web-build openapi clean rollback

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}'

up: .env ## Build and start the whole stack
	SBC_VERSION=$(VERSION) $(COMPOSE) up -d --build
	@echo "Admin API: http://localhost:$${SBC_ADMIN_PORT:-8080}  Web UI: http://localhost:$${SBC_WEB_PORT:-3000}"

down: ## Stop the stack (keeps data)
	$(COMPOSE) --profile e2e down --remove-orphans

clean: ## Stop the stack and delete all data volumes
	$(COMPOSE) --profile e2e down -v --remove-orphans

logs: ## Tail all logs
	$(COMPOSE) logs -f --tail=200

ps: ## Show service status
	$(COMPOSE) ps

.env:
	cp .env.example .env

build: ## Compile the Go binary locally
	$(GO) build -ldflags "-X main.version=$(VERSION)" -o bin/sbc-api ./cmd/sbc-api

test: test-unit ## Run unit tests (fast, no external services)

test-unit: ## Unit tests
	$(GO) test -race -count=1 ./...

test-integration: ## Integration tests against compose Postgres and Redis
	$(COMPOSE) up -d postgres redis
	SBC_TEST_DATABASE_URL=postgres://opensbc:$${POSTGRES_PASSWORD:-opensbc}@127.0.0.1:$$($(COMPOSE) port postgres 5432 | cut -d: -f2)/opensbc?sslmode=disable \
	SBC_TEST_REDIS_URL=redis://127.0.0.1:$$($(COMPOSE) port redis 6379 | cut -d: -f2)/1 \
	$(GO) test -race -count=1 -tags integration ./internal/... -run 'Integration'

lint: lint-go lint-lua lint-emdash ## All linters

lint-go: ## golangci-lint
	golangci-lint run ./...

lint-lua: ## luacheck
	luacheck freeswitch/scripts

lint-web: ## eslint + prettier + tsc
	cd web && npm run lint && npm run typecheck

lint-emdash: ## D-41: no em-dash anywhere in the tree
	@if grep -rIl --exclude-dir=node_modules --exclude-dir=.git --exclude-dir=dist --exclude=Makefile $$'\xe2\x80\x94' . ; then echo "em-dash found (D-41)"; exit 1; else echo "no em-dash"; fi

fmt: ## gofmt + prettier
	gofmt -w ./cmd ./internal ./tests
	cd web && npx prettier --write src >/dev/null 2>&1 || true

seed: ## Load the demo data set (idempotent)
	$(COMPOSE) run --rm --no-deps api seed

reconcile: ## Prove ledger and account invariants
	$(COMPOSE) run --rm --no-deps api reconcile

e2e: ## End-to-end SIP tests with sipp mock customers and carriers
	SBC_VERSION=$(VERSION) $(COMPOSE) --profile e2e up -d --build
	$(COMPOSE) run --rm --no-deps api seed
	$(GO) test -count=1 -tags e2e -v -timeout 20m ./tests/e2e/...

rollback: ## Roll back the latest database migration
	$(COMPOSE) run --rm --no-deps api migrate-down

openapi: ## Regenerate OpenAPI 3.1 document from handler annotations
	swag init --v3.1 -g cmd/sbc-api/main.go -o internal/httpapi/admin/openapi --outputTypes json,yaml
