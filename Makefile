SHELL := /bin/bash
export PATH := $(PATH):/usr/local/go/bin:$(HOME)/go/bin
COMPOSE ?= docker compose
GO ?= go
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

.PHONY: help up down logs ps build test test-unit test-integration lint lint-go lint-lua lint-web lint-emdash seed e2e e2e-ui reconcile replay-cdrs fmt web-build openapi clean rollback live-init live-up live-down live-ps live-logs live-reconcile live-replay-cdrs live-backup live-monitoring live-firewall homer-up homer-down live-ban-sync live-ban-timer

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}'

up: .env ## Build and start the whole stack
	SBC_VERSION=$(VERSION) $(COMPOSE) up -d --build
	@echo "Dev admin API: http://127.0.0.1:$${SBC_ADMIN_PORT:-18080}  Dev web UI: http://127.0.0.1:$${SBC_WEB_PORT:-13000}"

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
	SBC_TEST_DATABASE_URL=postgres://supersbc:$${POSTGRES_PASSWORD:-supersbc}@127.0.0.1:$${SBC_PG_PORT:-15432}/supersbc_test?sslmode=disable \
	SBC_TEST_REDIS_URL=redis://127.0.0.1:$${SBC_REDIS_PORT:-16379}/1 \
	$(GO) test -race -count=1 -tags integration ./internal/... -run 'Integration'

lint: lint-go lint-lua lint-web lint-emdash ## All linters

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
	$(COMPOSE) exec api /app/sbc-api seed

reconcile: ## Prove ledger and account invariants
	$(COMPOSE) exec api /app/sbc-api reconcile

e2e: ## End-to-end SIP tests with sipp mock customers and carriers
	SBC_VERSION=$(VERSION) $(COMPOSE) --profile e2e up -d --build
	$(COMPOSE) exec api /app/sbc-api seed
	SBC_VERSION=$(VERSION) $(GO) test -count=1 -tags e2e -v -timeout 30m ./tests/e2e/...

e2e-ui: ## Playwright tests against the running web UI (needs make up and make seed)
	cd web && npx playwright install chromium >/dev/null 2>&1 || true
	cd web && SBC_WEB_URL=http://127.0.0.1:$${SBC_WEB_PORT:-13000} npx playwright test

replay-cdrs: ## Re-post CDRs spooled by mod_json_cdr while the API was down
	$(COMPOSE) exec freeswitch sh /etc/freeswitch/replay_json_cdr.sh

# ---- live environment (deploy/live) ----
LIVE := docker compose -p supersbc-live --env-file .env.live -f deploy/live/docker-compose.live.yml

live-init: ## One-off: generate .env.live (random secrets) and a self-signed certificate
	deploy/live/init.sh $(NODE_IP)

live-up: ## Build and start the live stack (FreeSWITCH on the host network, web on 8080/8443)
	@test -f .env.live || (echo "run make live-init first"; exit 1)
	$(LIVE) up -d --build
	@echo "Live UI: https://$$(grep ^SBC_FS_NODE_IP .env.live | cut -d= -f2):8443"

live-down: ## Stop the live stack (keeps data)
	$(LIVE) --profile monitoring --profile backup down --remove-orphans

live-ps: ## Live service status
	$(LIVE) ps

live-logs: ## Follow live logs
	$(LIVE) logs -f --tail=200

live-reconcile: ## Ledger invariants on live
	$(LIVE) exec api /app/sbc-api reconcile

live-replay-cdrs: ## Re-post spooled CDRs on live
	$(LIVE) exec freeswitch sh /etc/freeswitch/replay_json_cdr.sh

live-backup: ## Immediate pg_dump of the live database into backups/live
	mkdir -p backups/live && $(LIVE) exec -T postgres pg_dump -Fc -U supersbc supersbc > backups/live/supersbc-$$(date -u +%Y%m%d-%H%M%S).dump && ls -la backups/live | tail -1

homer-up: ## Start HOMER (heplify-server on 172.28.0.1:9060, webapp on 127.0.0.1:19080, admin/sipcapture)
	$(COMPOSE) --profile homer up -d

homer-down: ## Stop HOMER (only its services; the SBC keeps running)
	$(COMPOSE) --profile homer rm -sf homer-webapp heplify-server homer-db

live-monitoring: ## Start prometheus and grafana for live (grafana at https://host:8443/grafana/)
	$(LIVE) --profile monitoring up -d

live-ban-sync: ## Mirror the SBC ban list into the nftables "banned" set once (install the systemd timer for continuous sync)
	deploy/live/ban-sync.sh

live-ban-timer: ## Install and start the systemd timer that runs live-ban-sync every 30 seconds
	install -m 644 deploy/live/supersbc-ban-sync.service deploy/live/supersbc-ban-sync.timer /etc/systemd/system/
	systemctl daemon-reload && systemctl enable --now supersbc-ban-sync.timer

live-firewall: ## Apply and persist the nftables ruleset in deploy/live/nftables.conf
	nft -f deploy/live/nftables.conf && cp deploy/live/nftables.conf /etc/nftables.conf && systemctl enable --now nftables >/dev/null 2>&1; nft list chain inet sbc input | head -30

rollback: ## Roll back the latest database migration
	$(COMPOSE) exec api /app/sbc-api migrate-down

openapi: ## Regenerate the OpenAPI 3.1 document (swag v2) and docs/API.md
	swag init --v3.1 -g main.go -d ./cmd/sbc-api,./internal/httpapi/admin -o internal/httpapi/admin/openapi --outputTypes json --parseDependency --parseInternal >/dev/null 2>&1
	$(GO) run ./cmd/apidoc internal/httpapi/admin/openapi/swagger.json docs/API.md
