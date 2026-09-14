# Decisions and assumptions

Every choice the specification left open, and the reason it was made. Numbered so they can be referenced from code comments (`D-07`).

## Platform

* **D-01 FreeSWITCH image: `safarov/freeswitch:1.10.12`.** The official SignalWire images require a personal access token to pull, which breaks "docker compose up on a clean machine". The safarov image is public, Alpine based, built from the 1.10.12 release, and ships `mod_lua`, `mod_curl`, `mod_json_cdr`, `mod_xml_curl`, `mod_event_socket`, `mod_sofia`, `mod_opus`, `mod_g729` (the FreeSWITCH pass-through G.729; transcoding G.729 needs a licensed codec, see D-30).
* **D-02 Lua to Go transport: `mod_curl` via `freeswitch.API():executeString("curl ...")`.** The image has no `lua-cjson` or `luasocket`. `mod_curl` is loaded, supports `post`, `content-type`, `timeout` and `connect-timeout` in seconds, and returns the response body. JSON is handled by a vendored copy of `rxi/json.lua` (MIT), stored as `freeswitch/scripts/sbc/json.lua`. Verified inside the container in M1.
* **D-03 Lua VM is Lua 5.2** (bundled with FreeSWITCH 1.10). `luacheck` runs with `std = "lua52"` plus the FreeSWITCH globals.
* **D-04 Gateway and ACL configuration are rendered to files by Go, not served by `mod_xml_curl`.** Reasons: FreeSWITCH boots and keeps working when the API is down, the rendered XML is visible on disk for debugging, and `sofia profile external-egress rescan` plus `reloadacl` are documented operations. The files live on a shared Docker volume mounted at `/etc/freeswitch/sip_profiles/external-egress/` and `/etc/freeswitch/autoload_configs/acl.conf.xml`. `mod_xml_curl` is kept loaded but unused so the roadmap item is a config change, not a rebuild.
* **D-05 Docker networking: one bridge network with static IPs** (`172.28.0.0/24`). This makes the e2e tests deterministic (customer containers have known source IPs). For production, `deploy/docker-compose.host.yml` switches FreeSWITCH to `network_mode: host`; documented in OPERATIONS.md.
* **D-06 Ports.** Ingress profile 5060 (customers), egress profile 5080 (carriers), RTP 16384 to 32768 (each call uses two RTP sessions, so the range bounds concurrent calls at about 4000), Admin API 8080, internal API 8081 (docker network only), ESL 8021 (docker network only), Web UI 3000.

## Go

* **D-07 Router: `chi`.** It is stdlib `net/http` compatible, so handlers are plain functions, middleware composes cleanly, and it has no framework lock-in. gin would have forced its own context type into every handler.
* **D-08 SQL: `pgx/v5` directly with hand-written queries in `internal/store`.** `sqlc` was considered; it adds a code generation step and does not model the dynamic filters that CDR search and reports need. All queries are parameterised. Integration tests run against a real Postgres.
* **D-09 Migrations: `goose` embedded in the binary**, run automatically at API start (`SBC_AUTO_MIGRATE=true` by default; set false in multi-node production and run `sbc-api migrate` once).
* **D-10 Money: `shopspring/decimal`** in Go, `NUMERIC(18,6)` in Postgres, strings in JSON. Rounding is half-up to 6 decimal places: `decimal.Round(6)` rounds half away from zero, which is half-up for positive amounts, and negative amounts are only ever produced by negating a rounded positive.
* **D-11 Redis client: `redis/go-redis/v9`.**
* **D-12 ESL client: in-house minimal implementation (`internal/esl`).** The protocol is simple (auth, `api`, `bgapi`, `event plain`). The third-party libraries are either unmaintained or drag in more than needed. It supports reconnect with backoff.
* **D-13 Logging: `log/slog` JSON** to stdout. Every log line inside a call carries `call_uuid`. Lua logs through `freeswitch.consoleLog` with a `[sbc uuid=...]` prefix, and FreeSWITCH is configured to log JSON-ish key=value so `docs/OPERATIONS.md` can show one `grep` that stitches all three.
* **D-14 OpenAPI 3.1: `swaggo/swag` v2 with `--v3.1`**, generated from handler annotations at build time into `docs/API.md` and served at `/api/docs`.
* **D-15 Auth: argon2id (`golang.org/x/crypto/argon2`), JWT (`golang-jwt/jwt/v5`) in httpOnly cookies, CSRF double-submit cookie, TOTP via `pquerna/otp`.**
* **D-16 The `setup` endpoint.** Lua makes one `POST /internal/v1/call/setup` that performs authorise, normalise, sell rate, balance check, reserve, route and buy rate lookups. Each is a separate Go function with the semantics of Section 3, and each also has its own endpoint for tooling and tests.
* **D-17 Idempotency of billing** is guarded by `cdrs.billed_at` under `SELECT ... FOR UPDATE`, not by a separate table.

## Billing semantics

* **D-18 Reservation amount** = `Amount(BilledSeconds(reserve_minutes*60, rate...), rate)` i.e. 5 minutes rated with the same increment rules, plus connect fee. For a 60/60 rate at 0.02 this is 0.10.
* **D-19 Available balance must be `> 0` and `>= reserve_amount`.** Both conditions are checked; with a positive reservation the second implies the first, but the first is kept explicit so a rate of 0.000000 per minute (free destination) still requires a non-negative account.
* **D-20 `max_call_seconds = floor(available_after_reservation_plus_reservation / rate_per_second)`**, capped by `billing.max_call_duration` (4 h). The reservation itself is part of what the customer may spend, so `available + reserve_amount` is the spendable amount. If `rate_per_min` is 0, `max_call_seconds` is the cap.
* **D-21 Overrun edge.** The true-up may exceed `max_call_seconds` money by at most one `subsequent_increment` (because `sched_hangup` fires at second granularity and rounding goes up). This can push `balance + allowed_credit` below zero by at most one increment. Documented in BILLING.md; not "fixed" because the alternative (hanging up one increment early) short-changes the customer.
* **D-22 Carrier accounts are liabilities.** `balance` on a carrier account represents prepaid deposit with the supplier (positive) or what we owe (negative). `cost` entries decrement it; `topup` records a payment to the supplier.
* **D-23 Failed attempts cost 0.** `carriers.charge_failed_attempts` exists as a column, defaults false, and is not yet honoured (roadmap).
* **D-24 Currency.** Everything defaults to USD. `fx_rates` and conversion at rating time arrive in M4; until then, a rate group, its customers and carriers are assumed to share a currency and the API refuses to attach a rate group with a different currency.

## Call handling

* **D-25 Source address** is taken from `sip_network_ip` / `sip_network_port` (the UDP/TCP peer), never from Via or Contact.
* **D-26 Number normalisation** (Section 3 Step 2) in order: strip `tech_prefix` if the number starts with it; drop everything except digits and a leading `+`; `+CC...` becomes `CC...`; `00CC...` becomes `CC...` (international prefix configurable per customer later, default `00`); `011` is NOT treated as international by default (US customers set `intl_prefix=011` on the customer in a later milestone, see ROADMAP); if the result starts with `0` and `default_country_code` is set, the leading `0` is dropped and the country code prepended; a number without a leading 0 and shorter than 7 digits is malformed; numbers longer than 15 digits are malformed.
* **D-27 Reason phrases** are sent with the `respond` dialplan application before the channel is answered, so the exact text from Section 3 reaches the customer.
* **D-28 Relaying carrier codes.** After a failed bridge the a-leg reads `last_bridge_proto_specific_hangup_cause` (`sip:503`) and responds with that code and the standard reason phrase for it. If the b-leg failed without a SIP code (timeout, gateway down) the FreeSWITCH cause is mapped through the standard FreeSWITCH cause to SIP code table.
* **D-29 PDD** is measured in Lua from the moment the bridge is started to the a-leg's `progress_uepoch` (first 18x relayed) or `answer_uepoch`, whichever comes first, per attempt. If an earlier attempt already produced early media the later attempt's PDD is measured to answer only. Good enough for reports; exact per-leg timing is a roadmap item using `uuid_dump` on the b-leg.
* **D-30 Codecs.** Default allowed list `PCMA,PCMU,OPUS,G722`. `G729` is accepted in lists; with the stock `mod_g729` FreeSWITCH can pass it through but not transcode it. Documented in OPERATIONS.md.
* **D-31 Concurrency limits** use Redis `INCR` with a key per customer that is decremented on hangup and reconciled every minute from `active_calls`. CPS uses a fixed one-second window counter (`cps:<customer>:<unix_second>`, TTL 2 s), which is a token bucket with refill equal to capacity every second: simple, and what carriers mean by "CPS".
* **D-32 Circuit breaker** state is kept in Redis so two API nodes agree. Thresholds: 20 consecutive carrier faults, or ASR < 10 % over the last 5 minutes with at least 50 attempts, degrade for 60 s.
* **D-33 Session timers**: `enable-timer=true`, `session-timeout=1800`, `minimum-session-expires=120` on both profiles.
* **D-34 RTP timeout**: `rtp-timeout-sec=300`, `rtp-hold-timeout-sec=1800`.
* **D-35 Max call duration** default 4 h, enforced twice: `sched_hangup` (FreeSWITCH) and the reservation TTL (reconciliation).
* **D-36 CDR row is created at setup**, before dialling, with `disposition = 'pending'`, so a crash mid-call still leaves a row for reconciliation. `pending` is an internal state; the API never reports it to reports (they filter it out) and the reconciliation worker resolves it.
* **D-37 `sbc_node`** is the hostname of the API instance that billed the call.

## Testing

* **D-38 Unit tests** for every pure package (`rating`, `numbering`, `prefix`, `failover`). **Integration tests** (`//go:build integration`) need `SBC_TEST_DATABASE_URL` and `SBC_TEST_REDIS_URL`, provided by `make test-integration` through compose. **E2E tests** are Go tests (`tests/e2e`, build tag `e2e`) that drive `sipp` containers with `docker compose run` and assert against the Admin API and Postgres.
* **D-39 Mock carriers are sipp UAS containers** with three scenarios: `uas_answer.xml` (180, 200, waits for BYE), `uas_503.xml`, `uas_404.xml`. Routes in the seed data point different prefixes at different combinations so every e2e scenario in M1 is a plain call to a known number. sipp UAS scenarios do not answer out-of-dialog OPTIONS (they abort the pseudo-call), so the demo carriers are seeded with `sip_options_ping = false`; real carriers keep the default `true`. Gateway DOWN handling is exercised by pointing a carrier at an unreachable host in the M2 tests.
* **D-40 Seed data** is loaded by `sbc-api seed` (idempotent, keyed by name): customers `acme` (172.28.0.101, balance 10.00) and `beta` (172.28.0.102, balance 0.00), carriers `carrier-answer`, `carrier-503`, `carrier-404`, selling deck `retail-usd`, buying decks per carrier, route group `default`.

## Style

* **D-43 ANI is the From user.** Customer supplied `P-Asserted-Identity` and `Remote-Party-ID` headers are ignored for caller identification and never forwarded (FreeSWITCH would otherwise prefer them as caller id). A per-customer "trust PAI" flag is on the roadmap together with Privacy handling.

* **D-44 Blocked destinations answer `403 Destination blocked`** (disposition `rejected_route`, step `blocklist`). The specification does not name a code for block lists; 403 is what carriers conventionally send for barred destinations. The per-customer list is checked before the global blacklist; `customers.blocked_prefixes_enabled=false` exempts a customer from both.
* **D-45 Multi-currency.** Every account has a currency; every rate deck has a currency. At setup the selling rate is converted from the deck currency to the customer's account currency with the latest `fx_rates` row (inverse pairs are used when only the reverse direction exists) and the multiplier is stored in `cdrs.sell_fx` so billing reproduces exactly the same price. The buying rate is converted at billing time into the carrier's account currency (`cdrs.buy_fx`); a rate that changed between setup and hangup therefore costs at the hangup rate, which is the normal wholesale convention. A missing exchange rate rejects the call with `404 No rate for destination` (sell side) or costs at 1:1 with an error log (buy side). `margin` is only meaningful when both currencies match; reports show the currencies next to the money.

* **D-46 Web stack versions.** React 18.3 as specified (the Vite template defaults to 19; both work with the code), Vite 7, TypeScript 5.9, Tailwind CSS 4 with the `@tailwindcss/vite` plugin, shadcn/ui components written into `web/src/components/ui` by hand on Radix primitives (the shadcn CLI needs interactive prompts and vendors more than we use), TanStack Query 5, TanStack Table 8, React Router 7, react-hook-form 7 with zod 4, Recharts 3, sonner, next-themes. The build stage uses `node:22-bookworm-slim` because the npm lockfile generated on glibc omits the musl rollup binary. Money is displayed at 4 decimals (`lib/utils.money`), never rounded anywhere else.
* **D-47 Password reset without email.** OpenSBC sends no email. An admin generates a one-time reset link (`POST /users/{id}/reset-token`, valid one hour) and hands it to the user; the forgot-password page explains this.

* **D-48 Reconciliation sweep.** With FreeSWITCH reachable, any reservation older than `orphan_timeout` whose channel is absent from `show channels` is released, not only those past `max_call_duration + orphan_timeout`; the channel list is authoritative, and this repairs an API restart that lost hangup events within minutes. Without FreeSWITCH the specification's timing applies.
* **D-49 Hourly roll-ups feed the dashboard only.** `cdr_hourly_stats` is recomputed every minute for the current and previous hour and for any hour whose CDRs changed; the dashboard's hourly profile reads it. All other reports query `cdrs` directly so any filter combination works; the roll-up is the fast path, not a second source of truth.
* **D-50 SIP trace scope.** Sofia traces a whole profile, not one address; the UI enables tracing on the ingress profile for N minutes (auto-off) and filters the captured messages by address when displaying them. The trace is captured in `freeswitch.log` because the logfile profile maps the `console` level.

* **D-41 No em-dash character anywhere.** Enforced by `make lint` (`grep` over the tree).
* **D-42 Conventional commits**, one per milestone, plus intermediate commits when a milestone is large.
