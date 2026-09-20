# Configuration reference

SuperSBC is configured with environment variables read by `sbc-api` and by the FreeSWITCH configuration pre-processor. Development uses `.env`, live uses `.env.live` (both git-ignored, `.env.example` documents every key). A change needs a container restart (`make up` / `make live-up`); FreeSWITCH variables need a FreeSWITCH restart, which drops calls in progress.

Durations use Go syntax (`30s`, `5m`, `4h`). Booleans are `true`/`false`.

## Identity and listeners

| Variable | Default | Meaning |
|---|---|---|
| `SBC_ENV` | `dev` | `dev` or `prod`; prod hides debug output |
| `SBC_NODE_NAME` | hostname | node name in logs, CDRs (`sbc_node`) and reservations; must be unique per node in an HA pair |
| `SBC_LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error`; logs are JSON with `call_uuid` |
| `SBC_ADMIN_LISTEN` | `:8080` | admin REST API listener (behind nginx on live) |
| `SBC_INTERNAL_LISTEN` | `:8081` | call control API used by FreeSWITCH only |
| `SBC_INTERNAL_SECRET` | required | shared secret FreeSWITCH sends in `X-SBC-Secret` |
| `SBC_OPENAPI_FILE` | embedded | override the OpenAPI document served at `/api/docs` |

## Data stores

| Variable | Default | Meaning |
|---|---|---|
| `SBC_DATABASE_URL` | required | Postgres URL; several hosts with `target_session_attrs=read-write` for HA |
| `SBC_AUTO_MIGRATE` | `true` | apply embedded migrations at start |
| `SBC_REDIS_URL` | `redis://redis:6379/0` | Redis URL (password and db number also used with Sentinel) |
| `SBC_REDIS_SENTINEL_ADDRS` | empty | comma separated sentinels; when set the master is discovered through Sentinel |
| `SBC_REDIS_SENTINEL_MASTER` | `sbcmaster` | Sentinel master name |

## FreeSWITCH link

| Variable | Default | Meaning |
|---|---|---|
| `SBC_ESL_HOST`, `SBC_ESL_PORT` | `freeswitch`, `8021` | event socket address |
| `SBC_ESL_PASSWORD` | required | event socket password (also read by FreeSWITCH) |
| `SBC_FS_CONFIG_DIR` | `/fsconfig` | where the API renders gateways, ACLs and the TLS subject list |
| `SBC_FS_NODE_IP` | auto | address FreeSWITCH binds and the API uses in headers; the public address on live |
| `SBC_FS_EXT_IP` | node ip | address advertised in Contact and SDP when behind 1:1 NAT |
| `SBC_FS_ESL_LISTEN_IP` | `0.0.0.0` | where the event socket listens (the docker bridge gateway on live) |
| `SBC_FS_LOG_FILE` | `/var/log/freeswitch/freeswitch.log` | for the SIP trace viewer |
| `SBC_ACL_MODE` | `dialplan` | `dialplan`: every INVITE reaches Lua and unknown addresses get `403 IP not authorized` (counted for bans); `strict`: FreeSWITCH's ACL drops them first |

## FreeSWITCH profiles (read by vars.xml)

| Variable | Default | Meaning |
|---|---|---|
| `SBC_FS_INGRESS_PORT`, `SBC_FS_EGRESS_PORT` | `5060`, `5080` | customer and carrier SIP ports |
| `SBC_FS_TLS` | `true` | enable TLS listeners |
| `SBC_FS_INGRESS_TLS_PORT`, `SBC_FS_EGRESS_TLS_PORT` | `5061`, `5081` | TLS ports |
| `SBC_FS_TLS_VERIFY_POLICY` | `none` | `none`, `in` (client certificates must chain to cafile.pem), `subjects_in` (plus a subject listed on a customer), `out`, `all` |
| `SBC_FS_TLS_VERIFY_DEPTH` | `2` | chain depth |
| `SBC_FS_TLS_VERSION` | `tlsv1.2,tlsv1.3` | accepted TLS versions |
| `SBC_FS_INGRESS_100REL`, `SBC_FS_EGRESS_100REL` | `false` | offer PRACK (RFC 3262) per profile |
| `SBC_FS_RTP_START`, `SBC_FS_RTP_END` | `16384`, `32768` | RTP port range (open it in the firewall) |
| `SBC_FS_MAX_SESSIONS` | `1000` | FreeSWITCH session cap (two per call) |
| `SBC_FS_SESSIONS_PER_SECOND` | `100` | FreeSWITCH setup rate cap (two per call) |
| `SBC_FS_INGRESS_CODECS`, `SBC_FS_EGRESS_CODECS` | `PCMA,PCMU,OPUS,G722` | codec preference per profile |
| `SBC_FS_USER_AGENT` | `SuperSBC` | User-Agent / Server header |
| `SBC_FS_LOG_LEVEL` | `info` | FreeSWITCH log level |
| `SBC_FS_HEP_SERVER` | empty | HEPv3 capture target `udp:host:9060;hep=3;capture_id=N`; empty disables (live: `udp:172.29.0.1:9060;hep=3;capture_id=2`) |
| `SBC_HEP_DATABASE_URL` | empty | HOMER capture database read by the CDR "SIP trace" viewer; empty disables the viewer (D-71) |
| `HOMER_DB_PASSWORD`, `HOMER_KEEP_DAYS` | required (live), `30` | capture database password and retention |

## Billing

| Variable | Default | Meaning |
|---|---|---|
| `SBC_BILLING_CURRENCY` | `USD` | default currency of new accounts and decks |
| `SBC_BILLING_RESERVE_MINUTES` | `5` | minutes reserved at call setup |
| `SBC_BILLING_MAX_CALL_DURATION` | `4h` | hard limit per call (FreeSWITCH hangs up) |
| `SBC_BILLING_ORPHAN_TIMEOUT` | `10m` | age after which a reservation without a channel is released |
| `SBC_BILLING_LOW_BALANCE_THRESHOLD` | `0` | notification threshold; `0` disables |

## Routing and media

| Variable | Default | Meaning |
|---|---|---|
| `SBC_ROUTING_ORIGINATE_TIMEOUT` | `60` | seconds to wait for a carrier answer |
| `SBC_ROUTING_PROGRESS_TIMEOUT` | `8` | seconds to wait for the first provisional response before trying the next carrier |
| `SBC_ROUTING_BLOCK_NEGATIVE_MARGIN` | `false` | skip carriers whose buy rate exceeds the sell rate |
| `SBC_ROUTING_GLOBAL_MAX_CPS`, `SBC_ROUTING_GLOBAL_MAX_CHANNELS` | `0` | system wide caps (`0` = none) |
| `SBC_MEDIA_TIMEOUT_SEC` | `300` | hang up after this long without RTP |
| `SBC_MEDIA_HOLD_TIMEOUT_SEC` | `1800` | same while on hold |

## Quality based routing

| Variable | Default | Meaning |
|---|---|---|
| `SBC_QUALITY_MIN_SAMPLES` | `20` | attempts a carrier needs on a prefix before its own score is used (else carrier wide, else neutral) |
| `SBC_QUALITY_WINDOW_HOURS` | `24` | hours of history in the score (exponential decay, newest hour weighs most) |
| `SBC_QUALITY_REFRESH` | `30s` | how often scores are recomputed from Redis into memory |

## Failover and carrier health

| Variable | Default | Meaning |
|---|---|---|
| `SBC_FAILOVER_RULES_FILE` | `/app/failover.yaml` | SIP code classification rules |
| `SBC_FAILOVER_MIN_RING_SECONDS_FOR_NO_ANSWER` | `10` | shorter ringing before a timeout counts as a carrier fault |
| `SBC_FAILOVER_BREAKER_CONSECUTIVE_FAULTS` | `20` | consecutive faults that degrade a carrier |
| `SBC_FAILOVER_BREAKER_ASR_THRESHOLD_PERCENT` | `10` | ASR below this (with enough samples) degrades a carrier |
| `SBC_FAILOVER_BREAKER_ASR_MIN_SAMPLES` | `50` | samples needed for the ASR rule |
| `SBC_FAILOVER_BREAKER_DEGRADED_SECONDS` | `60` | how long a carrier stays degraded (tried last) |
| `SBC_FAILOVER_GATEWAY_PING_INTERVAL_SECONDS` | `10` | OPTIONS ping interval for carriers with pinging on |

## Security

| Variable | Default | Meaning |
|---|---|---|
| `SBC_JWT_SECRET` | required | signs session tokens |
| `SBC_AUTH_ACCESS_TTL`, `SBC_AUTH_REFRESH_TTL` | `15m`, `168h` | session token lifetimes |
| `SBC_AUTH_COOKIE_SECURE` | `false` | set `true` behind HTTPS (live) |
| `SBC_BOOTSTRAP_ADMIN_EMAIL`, `SBC_BOOTSTRAP_ADMIN_PASSWORD` | `admin@example.com`, empty | first admin, created when no user exists |
| `SBC_BAN_THRESHOLD`, `SBC_BAN_WINDOW`, `SBC_BAN_DURATION` | `20`, `5m`, `1h` | scanner protection; threshold `0` disables |
| `SBC_FIREWALL_EXPORT_FILE` | empty | JSON export of customer and carrier allow-lists and bans for `sbc-fwsync` (`/state/firewall.json` on live; D-69) |
| `SBC_STIR_MAX_AGE` | `60s` | PASSporT freshness window |
| `SBC_STIR_CA_FILE` | empty | PEM bundle of trusted STI-CA roots; empty skips chain validation |
| `SBC_STIR_ALLOW_HTTP` | `false` | accept `http://` x5u URLs (labs only) |
| `SBC_STIR_FORWARD` | `true` | forward a verified Identity header to the carrier |

## E-mail (password reset links)

| Variable | Default | Meaning |
|---|---|---|
| `SBC_SMTP_HOST` | empty | outgoing SMTP server; empty disables e-mail (the forgot page then points at the admin generated link) |
| `SBC_SMTP_PORT` | `587` | 587 STARTTLS, 465 implicit TLS, 25 clear |
| `SBC_SMTP_USERNAME`, `SBC_SMTP_PASSWORD` | empty | SMTP AUTH PLAIN credentials (an app password for Gmail or Outlook) |
| `SBC_SMTP_FROM` | `SuperSBC <noreply@<host>>` | sender |
| `SBC_SMTP_TLS` | by port | `starttls`, `tls` or `none` |
| `SBC_PUBLIC_URL` | request host | base URL of the UI used in e-mailed links, e.g. `https://sbc.example.com:8443` |

## Invoices

| Variable | Default | Meaning |
|---|---|---|
| `SBC_INVOICE_OPERATOR_NAME` | `SuperSBC` | issuer name on the PDF |
| `SBC_INVOICE_OPERATOR_ADDRESS` | empty | issuer address, lines separated by `\|` |
| `SBC_INVOICE_FOOTER` | empty | footer text |

## Compose only (ports and third party services)

| Variable | Default (dev / live) | Meaning |
|---|---|---|
| `POSTGRES_PASSWORD` | required | database password (must match `SBC_DATABASE_URL`) |
| `SBC_WEB_PORT` | `13000` / `8080` | web UI port; live also uses `SBC_HTTPS_PORT` `8443` |
| `SBC_ADMIN_PORT`, `SBC_INTERNAL_PORT` | `18080`, `18081` / `28080`, `8081` | loopback published API ports |
| `SBC_PG_PORT`, `SBC_REDIS_PORT` | `15432`, `16379` / `25432`, `26379` | loopback published stores |
| `SBC_PROMETHEUS_PORT`, `SBC_GRAFANA_PORT` | `19090`, `13001` / `29090` | monitoring |
| `GRAFANA_ADMIN_PASSWORD` | `admin` | Grafana admin |
| `BACKUP_KEEP_DAYS` | `30` | nightly dump retention |
| `SBC_HEP_BIND`, `SBC_HEP_BIND_LIVE` | `172.28.0.1`, `172.29.0.1` | addresses heplify-server binds |
| `SBC_HOMER_PORT` | `19080` | HOMER UI |
| `HOMER_DB_PASSWORD`, `HOMER_KEEP_DAYS` | `homer`, `7` | HOMER database |
| `SBC_VERSION` | git describe / `live` | image tag; set by the Makefile |

## Files

| Path | Purpose |
|---|---|
| `failover.yaml` | SIP response classification and failover rules |
| `freeswitch/conf` | FreeSWITCH configuration (mounted read only) |
| `freeswitch/scripts/sbc` | the Lua call pipeline |
| `freeswitch/tls`, `deploy/live/tls` | SIP and web TLS material (`agent.pem`, `cafile.pem`, `server.crt`, `server.key`) |
| `deploy/live/nftables.conf` | host firewall ruleset |
| `deploy/live/state/firewall.json`, `firewall-sets.nft` | firewall export written by the API and the set contents kept by sbc-fwsync for boot |
| `deploy/grafana`, `deploy/prometheus` | dashboards and scrape config |
| `deploy/ha` | keepalived, Postgres replica and Redis Sentinel kit |
