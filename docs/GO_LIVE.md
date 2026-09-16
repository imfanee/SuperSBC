# Go-live: the two environments on one box

| | dev | live ("mini-live") |
|---|---|---|
| compose project | `supersbc` (`docker-compose.yml`, `.env`) | `supersbc-live` (`deploy/live/docker-compose.live.yml`, `.env.live`) |
| network | private bridge 172.28.0.0/24, sipp mock carriers and customers | private bridge 172.29.0.0/24 for postgres, redis, api, web; **FreeSWITCH on the host network** |
| SIP | 172.28.0.10:5060 inside the bridge only | `<public ip>:5060` customers, `:5080` carriers, RTP 16384 to 32768 on the host |
| web / API | http://127.0.0.1:13000 and http://127.0.0.1:18080 (loopback only) | https://`<public ip>`:8443 (8080 redirects to it) |
| other host ports | 15432 postgres, 16379 redis, 13001 grafana, 19090 prometheus, all loopback | 25432, 26379, 28080 (admin API), 8081 (internal API for FreeSWITCH), 29090, all loopback; ESL on 172.29.0.1:8021 |
| data | seed data, wiped by `make clean` | real customers only, nightly backups in `backups/live` |
| ACL mode | `dialplan` (403 text for unknown IPs) | `strict` (Sofia drops unknown IPs before the dialplan) |
| commands | `make up / seed / e2e / e2e-ui / down` | `make live-init / live-up / live-ps / live-logs / live-reconcile / live-backup / live-down` |

Both stacks build from the same source tree; `make up` and `make live-up` each rebuild their own images, so a change is tested on dev first and then rolled to live with `make live-up`.

## What was done on this box (2026-09-15)

1. Dev stack moved to loopback-only ports (it was publishing 8080 and 3000 on the public address).
2. `make live-init` generated `.env.live` (random secrets, `SBC_FS_NODE_IP=51.38.218.42`, strict ACL, secure cookies, global caps 20 CPS / 200 channels, FreeSWITCH 400 sessions) and a self-signed certificate for the IP in `deploy/live/tls/`.
3. `make live-up` started the live stack; `/readyz` reports every dependency healthy; FreeSWITCH listens on 51.38.218.42:5060 and :5080, ESL on 172.29.0.1:8021 (docker bridge gateway, unreachable from outside).
4. A smoke call was placed through the live path (dev sipp container to the public address, routed to the dev mock carrier): answered, billed, ledger reconciled. The smoke objects were deleted afterwards; live contains no customers.
5. Host firewall applied and persisted (`make live-firewall`, `/etc/nftables.conf`): ssh 22, 8080, 8443, SIP 5060 udp/tcp, RTP udp 16384 to 32768 open to everyone; SIP 5080 only from the `carriers` set (empty until you add your carrier addresses); everything else dropped, docker bridges trusted.

The bootstrap admin password was printed once by `make live-init`; it is also in `.env.live` (`SBC_BOOTSTRAP_ADMIN_PASSWORD`) until you change it in the UI (Account and security), which you should do at first login.

## Onboarding the pilot customer

1. Log in at https://51.38.218.42:8443 (accept the self-signed certificate, or install a real one: put `server.crt` and `server.key` in `deploy/live/tls/` and `make live-up`).
2. Rate groups: create the selling deck (CSV import) and the buying deck of your carrier.
3. Carriers: create the carrier (gateway host, port, transport, buying deck, auth if any, `sip_options_ping` on). Then open 5080 for it:
   The firewall opens 5080 for the gateway host automatically (sbc-fwsync); add "Extra signalling sources" if the carrier uses more addresses. Check: `nft list set inet sbc carriers`.
   Check Carriers, gateway state UP after a minute.
4. Route groups: create a group, add a route per prefix with the ordered carriers.
5. Customers: create the pilot customer with selling deck, route group, sensible limits (for example 10 concurrent calls, 2 CPS), add their source IP, top up the account.
6. Routing simulator: enter the customer and a number as they would dial it; expect `200 OK (dial <carrier> first)`.
7. Ask the customer for a test call; watch Live calls, then the CDR (click the row for attempts and RTP statistics) and the ledger on the customer's Account tab.
8. `make live-monitoring` for Grafana at https://51.38.218.42:8443/grafana/ (admin / `GRAFANA_ADMIN_PASSWORD` from `.env.live`); enable `docker compose ... --profile backup up -d` or `make live-backup` before the pilot starts.

## Daily checks during the pilot

* `make live-ps` (all healthy), `curl -sk https://127.0.0.1:8443/readyz`.
* `make live-reconcile` must print `0 mismatches`.
* Dashboard: ASR per carrier, rejections by step, low balance customers.
* `make live-logs | grep -E 'WARN|ERROR'` for gateway state changes, orphaned reservations, breaker trips.
* If the API was down for a while: `make live-replay-cdrs` re-posts spooled CDRs.

## Limits of this setup (be explicit with the pilot)

* One box, no HA: a reboot drops calls in progress (reservations are released by the reconciler within `orphan_timeout`).
* Both environments share 4 vCPUs and 3.8 GiB: keep dev idle during pilot hours; do not run `tests/load/run.sh` while pilot traffic flows. Live is capped at 20 CPS / 200 channels (`.env.live`).
* Self-signed certificate: browsers warn until you install a real certificate for a hostname.
* No SIP TLS/SRTP yet (see ROADMAP).

## Added after the pilot roll-out

* `make live-firewall` installs the SIP rate limits and the `customers`, `carriers` and `banned` sets; `make live-fwsync` installs the service that keeps them equal to the database (D-69).
* SIP TLS listens on 5061 (customers) and 5081 (carriers, allow-listed); client certificate verification is off (`SBC_FS_TLS_VERIFY_POLICY=none`) until you have customer certificates (D-67).
* HEP capture is on: live FreeSWITCH mirrors SIP to heplify-server on the live bridge address (`SBC_FS_HEP_SERVER=udp:172.29.0.1:9060;hep=3;capture_id=2`); `make homer-up` runs HOMER, UI at http://127.0.0.1:19080 over an ssh tunnel (admin / sipcapture, change it). Dev traffic is capture_id 1, live is 2 (D-65).
* Invoices, STIR/SHAKEN verification and routing windows are configured per customer, carrier and route in the UI; nothing to enable on the box.
* A second box can be added following docs/HA.md.
