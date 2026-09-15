# Operations

Day-two runbook for OpenSBC. The commands use the dev stack (`docker compose`, `make ...`); for the live stack substitute `make live-...` targets or `docker compose -p opensbc-live --env-file .env.live -f deploy/live/docker-compose.live.yml` (see [GO_LIVE.md](GO_LIVE.md)).

## 1. Start, stop, upgrade

```bash
make up            # build and start postgres, redis, api, freeswitch, web
make ps            # service status (all should be healthy)
make logs          # follow everything
make down          # stop, keep data
make clean         # stop and delete the data volumes (destructive)
```

Upgrade: `git pull && make up`. Migrations run automatically at API start (`SBC_AUTO_MIGRATE=true`). For a multi-node deployment set it to `false` and run `docker compose exec api /app/sbc-api migrate` once before starting the new API images.

Rollback: `make rollback` runs `sbc-api migrate-down` (one migration); then start the previous image tag with `SBC_VERSION=<tag> make up`. Because gateway and ACL XML are rendered from the database at every API start, a rolled back API re-renders a matching FreeSWITCH configuration.

Optional profiles:

```bash
docker compose --profile monitoring up -d   # prometheus :9090 (loopback) and grafana :3001 (admin / GRAFANA_ADMIN_PASSWORD)
docker compose --profile backup up -d       # nightly pg_dump into ./backups
docker compose --profile e2e up -d          # sipp mock carriers for tests
```

## 2. Exposing SIP to real customers

The dev compose network is a private bridge (172.28.0.0/24): fine for the lab and for the e2e suite, wrong for real traffic because FreeSWITCH advertises its container address in SDP. The live stack (`deploy/live`, see [GO_LIVE.md](GO_LIVE.md)) runs FreeSWITCH on the host network with `SBC_FS_NODE_IP` set to the public address (`SBC_FS_EXT_IP` when behind 1:1 NAT), ESL bound to the docker bridge gateway so only containers reach it, the internal API on loopback, and `make live-firewall` opens only ssh, 8080, 8443, SIP 5060, RTP and 5080 from the carrier allow-list.

## 3. Add a customer

UI: Customers, New customer. API (`API=http://127.0.0.1:18080/api/v1` on dev, `API=https://<host>:8443/api/v1` with `curl -k` on live):

```bash
# log in and keep the cookies
curl -c cj -H 'Content-Type: application/json' -d '{"email":"admin@example.com","password":"..."}' $API/auth/login
CSRF=$(grep sbc_csrf cj | awk '{print $7}')
H=(-b cj -H "X-CSRF-Token: $CSRF" -H 'Content-Type: application/json')

# customer with selling deck and route group
curl "${H[@]}" -d '{"name":"newco","rate_group_id":"<retail deck id>","route_group_id":"<route group id>","max_concurrent_calls":30,"max_cps":5}' $API/customers
# authorise its address (single IP or CIDR) and put money on the account
curl "${H[@]}" -d '{"ip_cidr":"203.0.113.10/32"}' $API/customers/<id>/ips
curl "${H[@]}" -d '{"amount":"100.00","description":"first payment"}' $API/customers/<id>/account/topup
```

What happens underneath: the IP cache is flushed on every API node (Redis `customer_ips:changed`), the FreeSWITCH ACL is re-rendered and `reloadacl` runs. In `SBC_ACL_MODE=dialplan` (default) unknown addresses still reach the Lua pipeline and get `403 IP not authorized`; in `strict` mode Sofia answers a bare `403 Forbidden` before the dialplan.

Check with the simulator before the customer sends traffic: Routing simulator, or `GET /api/v1/routing/simulate?customer_id=<id>&number=<as dialled>`.

## 4. Add a carrier

UI: Carriers, New carrier. API: `POST /api/v1/carriers` with `name` (lowercase, becomes the FreeSWITCH gateway name), `gateway_host`, `gateway_port`, `transport`, `rate_group_id` (buying deck), optional `auth_username`/`auth_password`, `dni_prefix`, `ani_prefix`, `strip_digits`, `allowed_codecs`, `max_concurrent_calls`, `max_cps`, `failover_sip_codes`.

The API renders `/etc/freeswitch/sip_profiles/external-egress/<name>.xml` on the shared volume and runs `sofia profile external-egress rescan` (changed gateways are killed with `killgw` first). Verify:

```bash
docker compose exec freeswitch fs_cli -p "$SBC_ESL_PASSWORD" -x 'sofia status'          # gateway listed, state NOREG/REGED (UP) or DOWN
curl -b cj $API/carriers/<id>/status                                     # ping state, breaker, live channels
```

With `sip_options_ping=true` (default) a gateway that fails OPTIONS is marked DOWN by FreeSWITCH within about a minute and routing skips it; when it answers again it returns to UP automatically.

Then add the carrier to routes: Route groups, open the route, Add carrier, drag into position, Save. Or `PUT /api/v1/routes/<id>/carriers` with the ordered list.

## 5. Load a rate deck

UI: Rate groups, New rate group, then Import CSV: pick the file, Validate (a dry run that reports invalid rows with line numbers), then Import. Tick "Replace the whole deck" to swap a deck atomically. API:

```bash
curl "${H[@]:0:3}" -F file=@rates.csv '$API/rate-groups/<id>/rates/import?dry_run=true'   # preview
curl "${H[@]:0:3}" -F file=@rates.csv '$API/rate-groups/<id>/rates/import?replace=true'   # import
curl -b cj '$API/rate-groups/<id>/rates/test?number=447700900123'                          # longest prefix check
curl -b cj '$API/rate-groups/<id>/rates/export' > deck.csv
```

CSV columns: `prefix,destination,rate_per_min,connect_fee,initial_increment,subsequent_increment,min_duration,effective_from,effective_to,enabled`. Only `prefix` and `rate_per_min` (or `rate`) are required; unknown columns are ignored. Rates with a future `effective_from` are loaded now and used from that moment (the trie is rebuilt every five minutes and on every change).

Currency: a deck has a currency; a customer or carrier account has a currency; when they differ an exchange rate must exist (`POST /api/v1/fx-rates`), otherwise the API refuses to attach the deck.

## 6. Reading logs

Every component logs JSON (Go) or `key=value` (Lua) with the a-leg UUID:

```bash
UUID=<call uuid from the CDR>
docker compose logs api | grep $UUID                                  # setup decision, attempts, billing
docker compose exec freeswitch grep $UUID /var/log/freeswitch/freeswitch.log   # [sbc uuid=...] Lua lines and Sofia lines
```

The UI stitches all three on the call trace page (CDRs, click a row, "trace"): sbc-api lines are kept 24 h in Redis, FreeSWITCH lines are read from the log volume mounted read-only into the API container.

Useful searches:

| Question | Command |
|----------|---------|
| why was a call rejected | `docker compose logs api \| grep '"call rejected"'` |
| which carrier answered | `SELECT carrier_id, attempts FROM cdrs WHERE call_uuid = '...'` |
| calls that overran their money | `SELECT * FROM accounts WHERE balance + allowed_credit < 0` |
| orphan reservations released | `docker compose logs api \| grep orphaned` |
| gateway state changes | `docker compose logs api \| grep 'gateway state changed'` |

SIP trace: Customers, Trace tab, Enable trace (N minutes, ingress profile), place the call, Show messages (filtered to that customer's address). The trace goes to `freeswitch.log` at `console` level; disable it when done (it is automatically disabled after the chosen minutes). For a permanent capture pipeline use HOMER: set `capture-server` in `sofia.conf.xml` and `sip-capture=yes` on the profiles (see ROADMAP).

## 7. Money operations

* Top up: Customers, Account tab, or `POST /customers/{id}/account/topup {"amount":"50.00"}`. Ledger type `topup`.
* Correction: `POST /customers/{id}/account/adjust {"amount":"-3.10","description":"disputed call"}`. Ledger type `adjustment`, signed.
* Credit limit: `PUT /customers/{id}/account/credit {"allowed_credit":"200"}`.
* Carrier payments: `POST /carriers/{id}/account/topup` records what you paid the supplier; `cost` entries decrement it per call.
* Statement: Reports, Statement, or `GET /reports/statement?owner_type=customer&owner_id=...&from=...&to=...`.
* Invariants: `make reconcile` must print `0 mismatches`. Run it after any manual SQL. If it fails, do not "fix" the account row; find the missing or extra ledger entry (`SELECT * FROM ledger_entries WHERE account_id = ... ORDER BY id`).

Only `admin` users can move money; `operator` can change routing and configuration; `viewer` is read only.

## 8. Backups and restore

Enable the nightly dump: `docker compose --profile backup up -d` (02:30 UTC, `pg_dump -Fc`, 14 days retained in `./backups`). Manual: `docker compose exec postgres pg_dump -Fc -U opensbc opensbc > opensbc-$(date +%F).dump`.

Restore on a fresh stack:

```bash
make up                                   # start; the API creates an empty schema
docker compose stop api
docker compose exec -T postgres dropdb -U opensbc opensbc && docker compose exec -T postgres createdb -U opensbc opensbc
docker compose exec -T postgres pg_restore -U opensbc -d opensbc --no-owner < opensbc-2026-09-14.dump
docker compose start api                  # renders gateways and ACLs from the restored data
```

Redis holds only caches, counters and reservation mirrors; it needs no backup. After a restore run `make reconcile` and check `active_calls` is empty (`sbc-api reconcile` releases nothing; the reconciliation worker releases stale reservations after `max_call_duration + orphan_timeout`).

## 9. FreeSWITCH maintenance

```bash
docker compose exec freeswitch fs_cli -p "$SBC_ESL_PASSWORD"          # interactive console
fs_cli> sofia status                                                    # profiles and gateways
fs_cli> sofia profile external-egress rescan                            # pick up rendered gateway files
fs_cli> reloadacl                                                       # after editing acl files by hand (do not)
fs_cli> show channels                                                   # live channels
fs_cli> uuid_kill <uuid> MANAGER_REQUEST                                # hang up one call (the UI does this)
fs_cli> sofia global siptrace on                                        # trace everything to the log
```

Codecs: the profiles negotiate `SBC_FS_INGRESS_CODECS` / `SBC_FS_EGRESS_CODECS` (default `PCMA,PCMU,OPUS,G722`); each customer and carrier narrows the list. G.729 pass-through works with the stock `mod_g729`; transcoding to or from G.729 needs a licensed codec module.

Capacity: `SBC_FS_MAX_SESSIONS` and `SBC_FS_SESSIONS_PER_SECOND` count both legs (a call is two sessions). `SBC_ROUTING_GLOBAL_MAX_CHANNELS` and `SBC_ROUTING_GLOBAL_MAX_CPS` cap admitted calls in the API.

## 10. Monitoring

* `/readyz` on the API: 200 only when Postgres, Redis, ESL and both Sofia profiles are fine. Use it for load balancer health checks.
* `/metrics`: Prometheus. The Grafana dashboard (`deploy/grafana/dashboards/opensbc.json`) shows live calls, CPS, ASR and PDD per carrier, rejections by step, gateway and breaker state, revenue and cost, Lua to API latency.
* Alerts worth having: `sbc_gateway_up == 0` for more than 2 minutes, `sbc_carrier_degraded == 1`, `rate(sbc_call_rejections_total{step="authorize"}[5m])` spikes (scanner or misconfigured customer), `rate(sbc_reservation_failures_total[5m])` (customers out of money), `histogram_quantile(0.99, rate(sbc_internal_api_duration_seconds_bucket[5m])) > 0.5` (the 2 s Lua timeout is approaching).

## 11. Security checklist (OWASP ASVS level 1 walkthrough)

| Area | Status |
|------|--------|
| Authentication | argon2id passwords, 10 character minimum, optional TOTP, login rate limit (10 per email, 100 per IP, 15 minutes), sessions revoked on password change |
| Session management | JWT access token 15 minutes in an httpOnly, SameSite=Strict cookie; refresh tokens hashed in the database, rotated on use, 7 days; logout revokes |
| Access control | roles enforced server side (`requireRole`); money endpoints admin only; a user cannot demote or delete themselves |
| CSRF | double-submit token (`sbc_csrf` cookie, `X-CSRF-Token` header) on every state changing cookie request; API keys are exempt (no cookie) |
| Input validation | go-playground/validator on every body, zod on every form, prefixes digits only, amounts decimal strings, CIDR parsing by Postgres |
| Injection | every SQL statement parameterised (pgx), no string interpolation of user input; `group_by` and `sort` come from allow-lists |
| Secrets | none in the repository; `.env` is gitignored; carrier SIP passwords are never returned by the API; API keys and reset tokens are stored hashed and shown once |
| Transport | terminate TLS in front of nginx (not included); set `SBC_AUTH_COOKIE_SECURE=true` behind HTTPS; SIP TLS/SRTP per profile is on the roadmap |
| Headers | CSP (`default-src 'self'`, no inline scripts), `X-Frame-Options: DENY`, `X-Content-Type-Options: nosniff`, `Referrer-Policy`, `Cache-Control: no-store` on the API |
| Logging | every admin action in `audit_log` with actor and source address; no passwords or tokens in logs |
| Availability | fail closed on API or Redis outage (503 SBC internal error, never free calls); per customer CPS and channel limits; global caps; FreeSWITCH session rate limit |
| Data | soft delete keeps CDR and ledger history; backups with pg_dump; `make reconcile` proves ledger integrity |
| Known gaps | no account lockout notification, no email delivery, no SIP TLS by default, admin session fixation relies on cookie rotation only, no WAF; see ROADMAP |
