# Troubleshooting

Symptoms first, then what to check. Commands assume the live stack (`make live-*`, container names `supersbc-live-*`); for development drop the `live-` prefix and use `docker compose`.

## 1. First three things

1. `curl -sk https://127.0.0.1:8443/readyz | jq` : every check must be `ok`. A failing check names the broken dependency.
2. The CDR of the call (CDRs page, filter by number or Call-ID): the disposition, SIP code and reject reason say which step refused it. No CDR means the INVITE never reached the Lua pipeline (network, firewall, ban, or FreeSWITCH itself refused).
3. `make live-logs` (or `docker logs supersbc-live-api-1`) filtered by the call UUID.

## 2. SIP responses the SBC sends and why

| Response | Produced by | Cause and fix |
|---|---|---|
| `403 IP not authorized` | API step authenticate | source address not on any customer; add it (Customer > IP addresses), or the customer sends from a different address/port than expected. Twenty of these ban the address. |
| `403 Forbidden` (bare) | FreeSWITCH ACL | address banned (System > Banned IPs) or `SBC_ACL_MODE=strict` and the address is unknown |
| `403 Customer suspended` | authenticate | customer status is not active |
| `403 TLS required` | policy | customer has `require_tls` and called over UDP/TCP |
| `488 SRTP required` | policy | customer has `srtp_mode=mandatory` and offered plain RTP |
| `428 Use Identity Header`, `436 Bad Identity Info`, `438 Invalid Identity Header` | STIR/SHAKEN | customer has `stir_mode=require`; missing header, certificate not fetchable, or invalid/stale/mismatched token (CDR `stir_status`) |
| `480 Concurrent call limit` | admission | customer at `max_concurrent_calls` (or the global cap) |
| `503 CPS limit` | admission | customer at `max_cps` (or the global cap) |
| `484 Address Incomplete` | normalise | number could not be turned into E.164 (check tech prefix, country code, international prefix) |
| `403 Destination blocked` | block lists | prefix in the global blacklist or the customer's blocked prefixes |
| `404 No rate for destination` | rating | selling deck has no prefix for the number (import the rate) |
| `402 Not enough funds` | balance | `balance + allowed credit - reserved` is below five minutes at the selling rate |
| `503 No route` | routing | no route for the prefix, or every carrier skipped (simulator shows `skipped` reasons: `gateway_down`, `carrier_capacity`, `skipped_no_rate`, `outside_window`, `negative_margin_blocked`, `disabled`) |
| `503 All carriers failed` | bridge loop | every carrier answered with a failover code or timed out; the attempts timeline in the CDR shows each response |
| carrier's own code (`486`, `404`, ...) | bridge loop | a non failover response passed through from the last carrier |
| `503 SBC internal error` | Lua | the API did not answer within 2 s or answered garbage: check `readyz`, API logs, Postgres |
| `487 Request Terminated` | FreeSWITCH | the customer cancelled, or an operator hung up during ringing |

## 3. No CDR, customer says the call fails

* Is the INVITE arriving? `fs_cli -x "sofia global siptrace on"` for a minute (or the SIP trace in the UI), or `tcpdump -ni any port 5060`.
* Firewall: `nft list chain inet sbc input` counters; is the customer's address in the `customers` set (`nft list set inet sbc customers`; it is filled from Customer > IP addresses by sbc-fwsync, `systemctl status supersbc-fwsync`)? Is it banned (`nft list set inet sbc banned`)? Is the customer sending to the right port (5060 UDP/TCP, 5061 TLS)?
* FreeSWITCH refusing at its own limits: `fs_cli -x "status"` shows sessions and the per second cap; `503 Maximum Calls In Progress` in the trace means `SBC_FS_MAX_SESSIONS` or `_SESSIONS_PER_SECOND`.
* Profile not running: `fs_cli -x "sofia status"` must show both profiles RUNNING (and their TLS lines).

## 4. Calls connect but

| Symptom | Check |
|---|---|
| No audio or one way audio | the RTP range 16384 to 32768 must be open on the host firewall and, behind NAT, `SBC_FS_EXT_IP` must be the public address; `media_mode=bypass` requires customer and carrier to reach each other directly; the CDR's RTP statistics show which leg had no packets |
| Audio drops after 5 minutes | `SBC_MEDIA_TIMEOUT_SEC` (300): RTP stopped arriving; usually a NAT binding expired on the customer side |
| Bad quality | CDR RTP stats: jitter and loss per leg; Reports > Quality per carrier; transcoding (codec_in different from codec_out) costs CPU, check load |
| DTMF not working | customer and carrier `dtmf_mode`: rfc2833 needs payload 101 negotiated; `inband` forces tone detection and generation (CPU) |
| Wrong caller number at the carrier | `trust_pai` on the customer, `ani_prefix` on the carrier, privacy handling (`privacy_mode`) |
| Carrier rejects TLS | `carriers.transport=tls` needs the carrier to trust our certificate (self signed by default); check with `openssl s_client -connect carrier:5081` |

## 5. Money

| Symptom | Check |
|---|---|
| `make live-reconcile` reports mismatches | never expected; stop and look: the report lists the account and the difference. Causes so far: none in production; a restore from backup taken between a reservation and its release would show one until the reconciler releases it |
| Reserved amount stays after the call | the hangup event was lost (API restart): the reconciler releases it within `SBC_BILLING_ORPHAN_TIMEOUT` when the channel is gone; `make live-replay-cdrs` re-posts spooled CDRs |
| Call not billed (`billed_at` null) | same as above; check `docker logs supersbc-live-api-1 \| grep billed` for errors |
| Wrong price | the CDR shows the rate id, billed seconds and increments used; check `effective_from` of the deck rows and the exchange rate stored in the CDR |
| Customer reaches negative balance | `allowed_credit` set, or several concurrent calls each reserved five minutes and ran longer than reserved (by design the reservation only covers the first five minutes; set `max_concurrent_calls` for small prepaid customers) |

## 6. The API or the UI

| Symptom | Check |
|---|---|
| UI shows "network error" or 502 | `docker logs supersbc-live-web-1` (nginx) and `supersbc-live-api-1`; `readyz` |
| Reset e-mail never arrives | `SBC_SMTP_HOST` set? `docker logs supersbc-live-api-1 \| grep "reset e-mail"` shows the SMTP error (auth, TLS, relay refused); the forgot page still returns 202 by design; ten requests per address in 15 minutes are throttled (429) |
| Login fails with correct password | account locked after failures (wait or admin reset), clock skew above a minute breaks TOTP, cookies blocked (Secure cookies over plain http: use https) |
| 403 on every write | CSRF token missing (browser extensions, stale tab: reload), or role is `viewer` |
| API key rejected | header must be `Authorization: Bearer sbc_...`; key disabled or expired |
| Changes not applied to calls | tables are cached and invalidated through Redis; if Redis was restarted, restart the API; gateway files need `sofia profile external-egress rescan` (automatic) |
| `readyz` says `esl` not ok | FreeSWITCH down or the ESL password differs between `.env.live` and the running container: `make live-up` |

## 7. FreeSWITCH

| Symptom | Check |
|---|---|
| Container restarts in a loop | `docker logs supersbc-live-freeswitch-1`: usually a certificate problem (`agent.pem` missing or unreadable), a port already in use on the host, or a syntax error in a rendered file |
| Gateway `DOWN` | OPTIONS ping unanswered: carrier address/port, firewall `carriers` set (their responses come from their address), or the carrier does not answer OPTIONS (turn pinging off for that carrier) |
| Gateway `FAIL_WAIT` / `TRYING` | registration failing: credentials, realm |
| Profile restart needed | after changing TLS files or the client certificate subject list: System > restart profile (drops calls on that profile) |
| Disk full of logs | the `fslogs` volume rotates by size; lower `SBC_FS_LOG_LEVEL` to `warning` in production |

## 8. Docker and the box

| Symptom | Check |
|---|---|
| `make live-up` fails with "pool overlaps" | a network from an old project name still exists: `docker network ls`, remove the stale one |
| Containers use a stale image | the image tag follows `SBC_VERSION` (git version); always start through `make`, not bare `docker compose up` |
| Postgres will not start after a crash | `docker logs supersbc-live-postgres-1`; it recovers from WAL by itself; disk full is the usual cause |
| High load | `docker stats`; FreeSWITCH transcoding and sipp load generators are the usual consumers; see PERFORMANCE.md for sizing |
| Time | NTP must run: TOTP, STIR freshness, invoices and CDR times depend on it |

## 9. Getting a SIP trace to the carrier or the customer

1. System > SIP trace: enable on the customer profile for 5 minutes, place the call, open the CDR > Trace.
2. Or HOMER: `make homer-up`, `SBC_FS_HEP_SERVER` set, search by Call-ID at http://127.0.0.1:19080.
3. Or `fs_cli -x "sofia global siptrace on"` and read `docker logs supersbc-live-freeswitch-1`; turn it off afterwards.

The carrier leg carries `X-SBC-Call: <call uuid>` so a carrier can quote it back to you; it is the CDR's `call_uuid`.
