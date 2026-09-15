# Call flow

This document follows one INVITE from a customer through OpenSBC, step by step, naming the code that runs at each point. Section numbers refer to the original specification.

## Where things run

| Layer | File | Role |
|-------|------|------|
| FreeSWITCH dialplan | `freeswitch/conf/dialplan/public.xml` | every INVITE on `external-ingress` runs `lua sbc_inbound.lua` |
| Lua entry | `freeswitch/scripts/sbc_inbound.lua` | `pcall` wrapper: any Lua error becomes `503 SBC internal error` |
| Lua state machine | `freeswitch/scripts/sbc/pipeline.lua` | one `setup` call, then the bridge loop, then the final response |
| Lua HTTP | `freeswitch/scripts/sbc/http.lua`, `sbc/json.lua` | `mod_curl` with a 2 s hard timeout, JSON encoded for the `curl` API argument parser |
| Go pipeline | `internal/callcontrol/pipeline.go` | `Authorize`, `Normalize`, `SellRateFor`, `Reserve`, `Route`, `RecordAttempt`, `ReleaseReservation` |
| Go billing | `internal/billing/billing.go` | `Bill`, triggered by ESL and by `mod_json_cdr`, idempotent |

## Step 0: the INVITE reaches the dialplan

`external-ingress` has `auth-calls=false` and `apply-inbound-acl=customers`. In the default `SBC_ACL_MODE=dialplan` the rendered `customers` ACL allows everything so the pipeline can answer with the exact reason phrase. In `strict` mode unknown addresses are dropped by Sofia before the dialplan and never produce a CDR.

Lua reads:

* `sip_network_ip` / `sip_network_port`: the real UDP/TCP peer (D-25)
* `sip_via_protocol`: transport
* `caller_id_number`, `destination_number`, `sip_call_id`, `ep_codec_string`

and posts them to `POST /internal/v1/call/setup` with header `X-SBC-Secret`.

## Steps 1 to 7: `setup` (one round trip, D-16)

`Pipeline.Setup` runs the steps in order and stops at the first rejection. Each rejection writes a final CDR row (`billed_at` set, no money moved) and returns `{"action":"reject","reject":{"code":403,"reason":"IP not authorized"}}`.

| Step | Function | Rejection |
|------|----------|-----------|
| 1 authenticate | `Authorize`: Redis `ip:` cache, then `customer_ips WHERE ip_cidr >>= $1` (most specific CIDR wins, port and transport filters) | `403 IP not authorized`, `403 Customer suspended` |
| 1 policy | `require_tls` and `srtp_mode` of the customer against the INVITE transport and SDP (D-52) | `403 TLS required`, `488 SRTP required` |
| 1 admission | `cache.Admission.Admit`: `INCR cc:customer:<id>` and `INCR cps:customer:<id>:<second>`; global caps if configured | `480 Concurrent call limit`, `503 CPS limit` |
| 2 normalise | `numbering.Normalize` (D-26) | `484 Address Incomplete` |
| 3 sell rate | `tables.MatchRate` (trie, longest prefix) | `404 No rate for destination` |
| 4 balance | `available = balance + allowed_credit - reserved`; must be `> 0` and `>= reserve_amount` | `402 Not enough funds` |
| 5 reserve | one transaction: `UPDATE accounts ... WHERE available >= $amt RETURNING`, `INSERT ledger_entries(reserve)`, `INSERT cdrs(disposition='pending')`, `INSERT active_calls`; then Redis `resv:<uuid>` with TTL | `402 Not enough funds` (race lost) |
| 6 route | `tables.MatchRoute` then the ordered `route_carriers` | `503 No route` (reservation released) |
| 7 buy rates | per carrier `tables.MatchRate` in the carrier's deck; no rate: skipped; DOWN gateway: skipped; degraded: moved last; negative margin flagged (or blocked with `SBC_ROUTING_BLOCK_NEGATIVE_MARGIN=true`) | `503 No route` when nothing is dialable |

The response for a dialable call:

```json
{
  "action": "dial",
  "called": "447700900123",
  "caller": "15550001111",
  "sell": {"rate_id": "...", "rate_per_min": "0.02", "connect_fee": "0", "initial_increment": 60, "subsequent_increment": 60},
  "reserved_amount": "0.1",
  "max_call_seconds": 14400,
  "carriers": [
    {"seq": 1, "name": "carrier-503", "dial_string": "{sip_h_X-SBC-Call=...,origination_caller_id_number=15550001111,absolute_codec_string=^^:PCMA:PCMU,ignore_early_media=false,originate_timeout=60,progress_timeout=8,sip_copy_custom_headers=false,sip_cid_type=none,...}sofia/gateway/carrier-503/447700900123", "buy_rate_id": "...", "buy_rate_per_min": "0.014"},
    {"seq": 2, "name": "carrier-answer", "dial_string": "...sofia/gateway/carrier-answer/447700900123", "buy_rate_per_min": "0.015"}
  ],
  "vars": {"sbc_customer_id": "...", "sbc_sell_rate_id": "...", "sbc_reserved": "0.100000", "sbc_max_call_seconds": "14400", "sbc_route_id": "..."}
}
```

Lua copies `vars` onto the a-leg so they appear in every CDR source. Section 7 policies ride the same response: `vars` also carries `rtp_secure_media` and the a-leg DTMF variables, `apps` lists dialplan applications to run before the bridge (`start_dtmf` for inband customers), each carrier has `media_mode` (Lua sets `bypass_media` or `proxy_media` per attempt) and `passthrough` headers copied from the a-leg, and the dial string already contains the carrier's DTMF, SRTP, privacy (`origination_privacy`, `sip_h_Privacy`, `sip_h_P-Asserted-Identity`) and header rule variables (`sip_h_...`). Response header rules arrive as `sip_rh_...` variables in `vars`.

Before any of this, an INVITE from a banned address (D-58) is answered `403 Forbidden` by Sofia's ACL and never reaches the dialplan.

## Step 8: the bridge loop (Lua)

Before the loop Lua sets on the a-leg:

* `continue_on_fail=true`: a failed bridge returns to the script instead of hanging up
* `hangup_after_bridge=true`: after an answered bridge ends, the a-leg is torn down by FreeSWITCH
* `execute_on_answer=sched_hangup +<max_call_seconds> ALLOTTED_TIMEOUT`: the customer can never talk past their money (Section 3 Step 5)

For each carrier in order:

1. Set `sbc_carrier_id`, `sbc_carrier_name`, `sbc_attempt_seq` on the a-leg, record `started` (`strmicroepoch`).
2. `bridge <dial_string>`. The dial string carries `originate_timeout` (60 s, no answer) and `progress_timeout` (8 s, no 18x), `absolute_codec_string` (customer and carrier codec intersection), `sip_h_X-SBC-Call`, `sip_copy_custom_headers=false` and `sip_cid_type=none` (topology and header hygiene).
3. When `bridge` returns, read `originate_disposition` (`SUCCESS` means answered), `last_bridge_hangup_cause`, `last_bridge_proto_specific_hangup_cause` (`sip:503`), and `uuid_dump` channel times for per-attempt PDD (D-29).
4. `POST /internal/v1/call/attempt`. Go classifies it (`internal/failover`), appends it to `cdrs.attempts`, and answers `{"classification":"carrier_fault","continue":true,"relay_code":503,"relay_reason":"Service Unavailable"}`.
5. Answered: stop (FreeSWITCH already handles the rest). Customer gone (`ORIGINATOR_CANCEL` or `session:ready()` false): stop. `continue=false` (number fault): stop and relay. Otherwise: next carrier.

After the loop, if nothing answered and the customer is still there, Lua sends `respond <relay_code> <relay_reason>`: the last carrier's code for carrier faults (`503 Service Unavailable`), the relayed code for number faults (`404 Not Found`, `486 Busy Here`), or `503 All carriers failed` when nothing was dialled.

### Failure classification (Section 6)

`failover.yaml` (built-in defaults in `internal/failover`) decides per SIP code and FreeSWITCH cause. `carriers.failover_sip_codes` overrides the carrier-fault list for one carrier. `NO_ANSWER` after less than `min_ring_seconds_for_no_answer` (10 s) of ringing is treated as a carrier fault (the carrier gave up early). Unlisted 5xx: carrier fault. Unlisted 4xx/6xx: number fault.

## Step 9: hangup and billing

Two independent triggers reach `billing.Engine.Bill`:

1. The ESL consumer (`cmd/sbc-api/wiring.go`) subscribed to `CHANNEL_HANGUP_COMPLETE`, filtered to `Call-Direction=inbound` on `external-ingress`.
2. `mod_json_cdr` posting the a-leg CDR to `POST /internal/v1/cdr` with basic auth (retries 3 times, then spools to `/var/log/freeswitch/json_cdr_failed`).

Both build the same `billing.HangupInfo` from channel variables (`billing.FromVariables`), so whichever arrives first bills the call and the second is a no-op (`cdrs.billed_at` under `SELECT ... FOR UPDATE`, D-17). The reconciliation worker is the third, last-resort trigger for calls whose reservation outlived `max_call_duration + orphan_timeout`.

See [BILLING.md](BILLING.md) for the money movements.

## Observability

Every log line of one call carries the a-leg UUID:

```bash
UUID=cb78a48c-8faf-4e2b-bba6-27e02399a008
docker compose logs api | grep $UUID                       # Go: setup, attempts, billed
docker compose exec freeswitch grep $UUID /var/log/freeswitch/freeswitch.log   # Lua ([sbc uuid=...]) and Sofia
```

## Sequence diagram

See [ARCHITECTURE.md](ARCHITECTURE.md#2-call-flow-successful-call-with-one-failover).
