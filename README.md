# OpenSBC

A deliberately simple, production-minded Session Border Controller with prepaid wholesale billing.

* **FreeSWITCH** does SIP, RTP anchoring, transcoding and topology hiding.
* **Lua** (inside FreeSWITCH) runs the per-call state machine and asks the control plane what to do.
* **Go** (`sbc-api`) authorises by source IP, rates, reserves money, routes with ordered failover, bills on hangup, and serves the admin REST API.
* **PostgreSQL** is the system of record, **Redis** the hot cache.
* **React + TypeScript** admin UI.

Read [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) for the design and [docs/DECISIONS.md](docs/DECISIONS.md) for every assumption.

## Quick start

Requirements: Docker 26+ with Compose v2, GNU make, and for development Go 1.26, Node 22, `golangci-lint`, `luacheck`.

```bash
cp .env.example .env        # then change the three change-me secrets
make up                     # postgres, redis, sbc-api, freeswitch, web
make seed                   # demo customers, carriers, rate decks and routes
curl -s localhost:8080/readyz | jq
```

* Admin API: http://localhost:8080/api/v1 (OpenAPI at `/api/docs`)
* Monitoring: `docker compose --profile monitoring up -d` then Grafana at http://localhost:3001 (admin / admin)
* Web UI: http://localhost:3000 (login `admin@example.com` / the `SBC_BOOTSTRAP_ADMIN_PASSWORD` you set)
* SIP ingress: `172.28.0.10:5060` inside the compose network (see docs/OPERATIONS.md for exposing it to real customers)

## Everything with one command

| Command | What it does |
|---------|--------------|
| `make up` | build images and start the stack |
| `make seed` | load the demo data set (idempotent) |
| `make test` | Go unit tests |
| `make test-integration` | Go tests against a real Postgres and Redis |
| `make e2e` | sipp mock customers and carriers place calls through FreeSWITCH and assert CDRs and balances |
| `make e2e-ui` | Playwright drives the web UI: login, customer and IP, rate deck import, route, simulator, CDRs |
| `make lint` | golangci-lint, luacheck, em-dash check |
| `make reconcile` | proves that balances equal the ledger and reservations equal open calls |
| `make replay-cdrs` | re-posts CDRs spooled by mod_json_cdr while the API was down |
| `make openapi` | regenerates the OpenAPI document and docs/API.md |
| `make logs` | follow all logs |
| `make down` / `make clean` | stop, or stop and delete data |

## Documentation

| Document | Content |
|----------|---------|
| [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) | components, call flow, data model, failure modes |
| [docs/DECISIONS.md](docs/DECISIONS.md) | every assumption, numbered |
| [docs/CALL_FLOW.md](docs/CALL_FLOW.md) | the ingress pipeline step by step, SIP codes and reason phrases |
| [docs/BILLING.md](docs/BILLING.md) | rating maths, reservation, true-up, ledger, edge cases |
| [docs/API.md](docs/API.md) | admin and internal API reference (OpenAPI 3.1) |
| [docs/OPERATIONS.md](docs/OPERATIONS.md) | day-two operations: add a customer, a carrier, a rate deck; logs; rollback; backups |
| [docs/ROADMAP.md](docs/ROADMAP.md) | features not yet built and the FreeSWITCH mechanism for each |
| [docs/PERFORMANCE.md](docs/PERFORMANCE.md) | load test results (25 CPS sustained on a 4 vCPU VM) and the defects the test found |
| [docs/SECURITY.md](docs/SECURITY.md) | OWASP ASVS level 1 walkthrough |

## Milestones

| | Status |
|---|---|
| M0 Foundation | done |
| M1 Core call pipeline and billing | done |
| M2 SBC hardening | done |
| M3 Media, capacity and metrics | done |
| M4 Admin API completeness | done |
| M5 Web UI | done |
| M6 Operations | done |

## Licence

MIT. `freeswitch/scripts/sbc/json.lua` is rxi/json.lua, MIT.
