# SuperSBC Admin API

Generated from handler annotations by `make openapi` (swag v2, OpenAPI 3.1). The live document is served at `/api/docs` (Swagger UI) and `/api/docs/openapi.json`.

Base path: `/api/v1`. Authentication: `POST /auth/login` sets `sbc_access` (JWT, 15 min), `sbc_refresh` (7 days, path `/api/v1/auth`) and `sbc_csrf` cookies; state changing requests must send `X-CSRF-Token` equal to the `sbc_csrf` cookie. API keys: `Authorization: Bearer sbc_...` (no CSRF). Roles: `viewer` read only, `operator` configuration and routing, `admin` everything including money and users.

Money is always a decimal string (`"0.020000"`). Lists accept `page`, `per_page`, `sort` (prefix `-` for descending) and return `{items, total, page, per_page}`.

## Internal call control API (port 8081, header `X-SBC-Secret`)

| Method | Path | Purpose |
|---|---|---|
| `POST` | `/internal/v1/call/setup` | Steps 1 to 7 of the pipeline in one call: authorise, normalise, sell rate, balance, reserve, route, buy rates |
| `POST` | `/internal/v1/call/authorize` | Step 1 only (probe, holds no slot) |
| `POST` | `/internal/v1/call/rate` | Step 3 only |
| `POST` | `/internal/v1/call/route` | Steps 6 and 7 only |
| `POST` | `/internal/v1/call/attempt/begin` | Carrier capacity slot before a bridge attempt |
| `POST` | `/internal/v1/call/attempt` | Report and classify one bridge attempt |
| `POST` | `/internal/v1/call/release` | Release a reservation for a call that will not be dialled |
| `POST` | `/internal/v1/cdr` | mod_json_cdr receiver (basic auth, password = secret) |

## Auth

| Method | Path | Summary |
|---|---|---|
| `DELETE` | `/auth/api-keys/{id}` | Revoke an API key |
| `GET` | `/auth/api-keys` | List API keys (own keys; admins see all) |
| `GET` | `/auth/me` | Current principal |
| `GET` | `/auth/options` | Public login options: whether password reset by e-mail is available (unauthenticated) |
| `POST` | `/auth/api-keys` | Create an API key (the plaintext is returned once) |
| `POST` | `/auth/forgot` | E-mail a one-time password reset link to an account (unauthenticated; always 202 so addresses cannot be enumerated) |
| `POST` | `/auth/login` | Log in with email and password (and TOTP code when enabled) |
| `POST` | `/auth/logout` | Log out (revokes the refresh session and clears cookies) |
| `POST` | `/auth/password` | Change own password (revokes other sessions) |
| `POST` | `/auth/refresh` | Rotate the refresh token and issue a new access token |
| `POST` | `/auth/reset` | Set a new password with a one-time reset token (unauthenticated) |
| `POST` | `/auth/totp/confirm` | Confirm a TOTP code and enable 2FA |
| `POST` | `/auth/totp/disable` | Disable 2FA (requires a valid code) |
| `POST` | `/auth/totp/setup` | Generate a TOTP secret (not enabled until confirmed) |

## Calls

| Method | Path | Summary |
|---|---|---|
| `DELETE` | `/calls/active/{id}` | Hang up a call in progress (uuid_kill) |
| `GET` | `/calls/active` | Calls in progress (from active_calls, enriched from FreeSWITCH when connected) |

## Carriers

| Method | Path | Summary |
|---|---|---|
| `DELETE` | `/carriers/{id}` | Delete a carrier (soft delete, removed from routes) |
| `GET` | `/carriers/status` | Gateway state of every carrier (dashboard health cards) |
| `GET` | `/carriers/{id}/account/ledger` | Ledger entries of a carrier account |
| `GET` | `/carriers/{id}/account` | Account of a carrier |
| `GET` | `/carriers/{id}/status` | Gateway ping state, circuit breaker and live channel count of a carrier |
| `GET` | `/carriers/{id}` | Get a carrier with account, gateway state and breaker statistics |
| `GET` | `/carriers` | List carriers with account and gateway state<br>Query: `search`, `status`, `page`, `per_page` |
| `POST` | `/carriers/{id}/account/adjust` | Adjust a carrier balance (signed) |
| `POST` | `/carriers/{id}/account/topup` | Record a payment to the carrier (ledger type topup) |
| `POST` | `/carriers` | Create a carrier (renders its FreeSWITCH gateway) |
| `PUT` | `/carriers/{id}` | Update a carrier (re-renders its gateway) |

## Cdrs

| Method | Path | Summary |
|---|---|---|
| `GET` | `/cdrs/export` | Stream CDRs as CSV with the same filters as the list; view=customer (no carrier, routing or cost columns), carrier (no customer, routing or selling columns) or full<br>Query: `view` |
| `GET` | `/cdrs/{id}/sip.pcap` | Download the captured SIP of a call as a pcap file (leg=customer|carrier|all)<br>Query: `leg` |
| `GET` | `/cdrs/{id}/sip` | Every captured SIP message of a call (customer and carrier legs, headers and bodies) from the HOMER capture store (D-71) |
| `GET` | `/cdrs/{id}/trace` | Per-call trace: CDR, ledger entries, sbc-api log lines and FreeSWITCH log lines (Lua and Sofia) for one call uuid |
| `GET` | `/cdrs/{id}` | One CDR with attempts and RTP statistics |
| `GET` | `/cdrs` | Search CDRs<br>Query: `from`, `to`, `customer_id`, `carrier_id`, `prefix`, `caller`, `disposition`, `sip_code`, `min_billsec`, `max_billsec`, `src_ip`, `negative_margin`, `page`, `per_page`, `sort` |

## Customers

| Method | Path | Summary |
|---|---|---|
| `DELETE` | `/customers/{id}/ips/{ipId}` | Remove an authorised address |
| `DELETE` | `/customers/{id}` | Delete a customer (soft delete, IPs removed, history kept) |
| `GET` | `/customers/{id}/account/ledger` | Ledger entries of a customer account<br>Query: `page`, `per_page` |
| `GET` | `/customers/{id}/account` | Account of a customer |
| `GET` | `/customers/{id}/blocked-prefixes` | Blocked prefixes of a customer |
| `GET` | `/customers/{id}/ips` | List a customer's authorised addresses |
| `GET` | `/customers/{id}` | Get a customer with account, IPs and group names |
| `GET` | `/customers` | List customers with account summary<br>Query: `search`, `status`, `page`, `per_page`, `sort` |
| `POST` | `/customers/{id}/account/adjust` | Adjust a customer balance (signed decimal, ledger type adjustment) |
| `POST` | `/customers/{id}/account/topup` | Add money to a customer account (ledger type topup) |
| `POST` | `/customers/{id}/blocked-prefixes` | Block a prefix for one customer |
| `POST` | `/customers/{id}/ips` | Authorise an address (single IP or CIDR) for a customer |
| `POST` | `/customers` | Create a customer (and its account) |
| `PUT` | `/customers/{id}/account/credit` | Set the credit limit of a customer |
| `PUT` | `/customers/{id}` | Update a customer |

## Invoices

| Method | Path | Summary |
|---|---|---|
| `GET` | `/invoice-ledger` | Invoice ledger of a customer or carrier: invoices and payments with allocations, totals of invoiced, received, outstanding (D-72)<br>Query: `owner_type` (required), `owner_id` (required) |
| `GET` | `/invoices/{id}.pdf` | Download one invoice as PDF |
| `GET` | `/invoices/{id}/allocations` | Payments applied to one invoice |
| `GET` | `/invoices/{id}` | Get one invoice with its usage lines |
| `GET` | `/invoices` | List invoices (newest first), optionally of one customer or carrier<br>Query: `owner_type`, `owner_id`, `limit` |
| `POST` | `/invoice-payments` | Record a payment against invoices (full, partial or several), optionally also topping up the prepaid account |
| `POST` | `/invoices/generate` | Generate the invoice of a customer or carrier for a month (period=YYYY-MM) or a custom range (from, to); idempotent, 200 when it already exists, 409 when another invoice overlaps |

## Rates

| Method | Path | Summary |
|---|---|---|
| `DELETE` | `/rate-groups/{id}/rates/{rateId}` | Delete a rate row |
| `DELETE` | `/rate-groups/{id}` | Delete an unused rate group |
| `GET` | `/fx-rates` | Current exchange rates |
| `GET` | `/rate-groups/{id}/rates/export` | Export a rate deck as CSV |
| `GET` | `/rate-groups/{id}/rates/test` | Longest-prefix match of a number in a rate group (trie and SQL must agree)<br>Query: `number` (required) |
| `GET` | `/rate-groups/{id}/rates` | List rates of a group<br>Query: `search`, `enabled` |
| `GET` | `/rate-groups/{id}` | Get a rate group |
| `GET` | `/rate-groups` | List rate groups (rate decks)<br>Query: `search` |
| `POST` | `/fx-rates` | Add an exchange rate (1 base = rate quote) |
| `POST` | `/rate-groups/{id}/rates/bulk-delete` | Delete several rate rows |
| `POST` | `/rate-groups/{id}/rates/import` | Import a CSV rate deck (multipart file or raw text/csv body); ?dry_run=true validates only; ?replace=true replaces the deck<br>Query: `dry_run`, `replace` |
| `POST` | `/rate-groups/{id}/rates` | Add a rate row |
| `POST` | `/rate-groups` | Create a rate group |
| `PUT` | `/rate-groups/{id}/rates/{rateId}` | Update a rate row |
| `PUT` | `/rate-groups/{id}` | Update a rate group |

## Reports

| Method | Path | Summary |
|---|---|---|
| `GET` | `/reports/peaks` | Peak concurrent calls and peak CPS per customer, carrier or system<br>Query: `from`, `to`, `group_by` |
| `GET` | `/reports/quality` | Quality report per group and day: ASR, ACD, PDD, MOS, loss, jitter, short call ratio, false answer indicator<br>Query: `from`, `to`, `group_by` |
| `GET` | `/reports/statement` | Balance statement of an account for a period (opening, movements, closing)<br>Query: `owner_type` (required), `owner_id` (required), `from`, `to`, `lines` |
| `GET` | `/reports/summary` | Dashboard: today's KPIs, hourly profile, top destinations, rejections, live calls, low balance customers<br>Query: `from`, `to` |
| `GET` | `/reports/traffic` | Traffic report: attempts, ASR, ACD, NER, minutes, revenue, cost, margin, PDD grouped by any dimension; ?format=csv exports<br>Query: `from`, `to`, `group_by`, `order_by`, `limit`, `customer_id`, `carrier_id`, `format` |

## Routing

| Method | Path | Summary |
|---|---|---|
| `DELETE` | `/blocked-prefixes/{blockId}` | Remove a blocked prefix |
| `DELETE` | `/header-rules/{ruleId}` | Delete a header rule |
| `DELETE` | `/route-groups/{id}` | Delete an unused route group |
| `DELETE` | `/routes/{id}` | Delete a route |
| `GET` | `/blocked-prefixes` | Global blacklist of prefixes |
| `GET` | `/customers/{id}/header-rules` | Header manipulation rules of a customer or carrier |
| `GET` | `/route-groups/{id}/routes` | Routes of a group with their ordered carriers<br>Query: `search` |
| `GET` | `/route-groups/{id}` | Get a route group |
| `GET` | `/route-groups` | List route groups |
| `GET` | `/routes/{id}/preview` | Buy rate and margin per carrier of a route for a sample number<br>Query: `number` (required), `sell_rate_group_id` |
| `GET` | `/routes/{id}` | Get a route with its carriers |
| `GET` | `/routing/simulate` | Routing simulator: the whole setup decision for a customer and number without dialling<br>Query: `customer_id` (required), `number` (required), `caller` |
| `POST` | `/blocked-prefixes` | Block a prefix for every customer |
| `POST` | `/customers/{id}/header-rules` | Add a header rule (egress: INVITE to the carrier; response: responses to the customer). Values may use {caller} {called} {customer} {carrier} {call_uuid} {node_ip} |
| `POST` | `/route-groups/{id}/routes` | Create a route (optionally with its carriers) |
| `POST` | `/route-groups` | Create a route group |
| `PUT` | `/header-rules/{ruleId}` | Update a header rule |
| `PUT` | `/route-groups/{id}` | Update a route group (name, description, LCR mode) |
| `PUT` | `/routes/{id}/carriers` | Replace the ordered carrier list of a route (reorder endpoint) |
| `PUT` | `/routes/{id}` | Update a route (and replace its carriers when given) |

## System

| Method | Path | Summary |
|---|---|---|
| `DELETE` | `/system/banned-ips/{ip}` | Lift a ban |
| `DELETE` | `/system/siptrace` | Disable SIP tracing |
| `GET` | `/audit-log` | Audit log<br>Query: `entity_type`, `entity_id`, `actor` |
| `GET` | `/system/banned-ips` | Active source address bans (automatic scanner bans and manual ones) |
| `GET` | `/system/failover-rules` | The failure classification table (read only) |
| `GET` | `/system/gateways` | Sofia gateway states from the OPTIONS ping poller |
| `GET` | `/system/notifications` | Recent notifications (low balance etc.) |
| `GET` | `/system/settings` | System settings rows (free-form key/value, e.g. UI preferences, sip trace state) |
| `GET` | `/system/siptrace/messages` | Traced SIP messages involving one address (customer or carrier IP) from the FreeSWITCH log<br>Query: `ip` (required), `limit` |
| `GET` | `/system/siptrace` | SIP trace toggle state |
| `GET` | `/system/status` | Readiness of every dependency plus configuration summary |
| `GET` | `/system/version` | Build version (unauthenticated) |
| `POST` | `/system/banned-ips` | Ban a source address (FreeSWITCH ACL deny, reloaded immediately) |
| `POST` | `/system/profiles/{name}/restart` | Restart a Sofia profile so it re-reads its configuration (drops the calls on that profile; use at a quiet time) |
| `POST` | `/system/siptrace` | Enable Sofia SIP tracing for N minutes (global or one profile); messages land in the FreeSWITCH log |
| `PUT` | `/system/settings` | Upsert settings rows |

## Users

| Method | Path | Summary |
|---|---|---|
| `DELETE` | `/users/{id}` | Delete a user |
| `GET` | `/users` | List users |
| `POST` | `/users/{id}/password` | Set a user's password (admin), revoking their sessions |
| `POST` | `/users/{id}/reset-token` | Generate a one-time password reset link for a user (valid one hour) |
| `POST` | `/users` | Create a user |
| `PUT` | `/users/{id}` | Update role and status of a user |

