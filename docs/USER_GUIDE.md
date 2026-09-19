# SuperSBC user guide

For the people who operate the SBC day to day through the web UI: onboarding customers and carriers, loading rate decks, building routes, watching traffic and money, and answering "why did this call fail". Everything in the UI is also available through the REST API ([API.md](API.md)); the UI never does anything the API cannot.

Roles: **viewer** can read everything, **operator** can also change customers, carriers, rates, routes, money and bans, **admin** can additionally manage users, API keys, settings and FreeSWITCH profiles.

## 1. Signing in

Open the UI (`https://<your sbc>:8443` on a live box, `http://127.0.0.1:13000` in development) and sign in with your e-mail and password. If two factor authentication is enabled on your account you are asked for the six digit code from your authenticator app.

Forgot your password: on the sign in page choose "Forgot your password?" and enter your e-mail; a one time reset link (valid one hour) is sent when the SBC has an outgoing mail server configured. Otherwise ask an admin to generate the link for you (System > Users > Reset link).

Your own account (top right > Account): change password, enable two factor authentication (scan the QR code, confirm with a code), see your active sessions.

## 2. The dashboard

The home page shows the last 24 hours: calls per hour (answered and failed), current concurrent calls and calls per second, answer seizure ratio (ASR), average call duration (ACD), post dial delay (PDD), revenue, cost and margin, the carriers by traffic, low balance customers, gateways that are down and degraded carriers. All numbers come from the CDRs; the hourly profile is refreshed every minute.

## 3. Customers

A customer is a wholesale trunk identified by its **source IP addresses** (no registration, no passwords). A customer has a prepaid **account** (balance, allowed credit), a **selling rate deck**, a **route group**, capacity limits and policies.

### 3.1 Create a customer

Customers > New customer:

| Field | Meaning |
|---|---|
| Name | unique, letters, digits, `-` and `_` |
| Status | `active`, `suspended` (calls rejected with `403 Customer suspended`), `disabled` |
| Selling rate deck | the rate group used to price this customer's calls (required to carry traffic) |
| Route group | which carriers its calls go to (required) |
| Max concurrent calls, max CPS | 0 = unlimited; exceeding gives `480 Concurrent call limit` or `503 CPS limit` |
| Allowed codecs | what the customer may negotiate (PCMA, PCMU, OPUS, G722, G729) |
| Tech prefix | digits the customer prepends to the called number; stripped before rating |
| Default country code, international prefix | how national numbers and `00`/`011` style numbers are turned into E.164 (see [CALL_FLOW.md](CALL_FLOW.md), D-26) |
| Trust P-Asserted-Identity | take the caller number from PAI instead of From |
| Apply block lists | whether the global and per customer blocked prefixes apply |
| Media | `anchor` (relay and transcode), `proxy` (relay without transcoding), `bypass` (media direct between customer and carrier when the carrier allows it too) |
| DTMF | `rfc2833`, `info`, `inband` |
| SRTP | `off`, `optional`, `mandatory` (plain offers get `488 SRTP required`) |
| STIR/SHAKEN | `ignore`, `verify` (record the attestation), `require` (reject unverified calls with 428/436/438) |
| TLS client certificate subject | only when the box verifies client certificates (admin guide) |
| Require SIP TLS | UDP/TCP calls get `403 TLS required` |
| Account currency | the currency of the prepaid account (only at creation) |

### 3.2 Authorise its addresses

Customer > IP addresses: add each address or CIDR block the customer sends from, optionally restricted to a source port and transport (`udp`, `tcp`, `tls`). The most specific block wins when two customers overlap (they should not). Changes apply within a second; in strict ACL mode FreeSWITCH also reloads its access list.

On a live box the host firewall only accepts SIP from listed addresses (the list is synced automatically), so an unknown source is dropped silently; where the firewall is not in place the SBC rejects it with `403 IP not authorized` and bans the address after twenty attempts in five minutes (System > Banned IPs).

### 3.3 Put money on the account

Customer > Account: **Top up** adds money (a payment received), **Adjust** corrects the balance up or down with a reason, **Allowed credit** lets the customer go negative up to that amount (postpaid behaviour). Every movement is a ledger entry you can see in the same tab and in Reports > Statement. Money is never edited directly; only ledger entries change balances.

A call is admitted when `balance + allowed credit - reserved` is enough for the first five minutes at the selling rate; the reservation is released and the real price charged when the call ends. The customer receives `402 Not enough funds` otherwise.

### 3.4 Blocked prefixes, header rules, invoices

* **Blocked prefixes**: destinations this customer may not call (`403 Destination blocked`). The global list is in System > Global blacklist.
* **Header rules**: shape the SIP sent to carriers (add, pass through or remove a header) and the responses sent to the customer. See section 5.3.
* **Invoices**: monthly prepaid usage statements as PDF, generated automatically after each month and on demand for any month. Numbering `INV-YYYYMM-nnnnnn`.
* **CDRs** and **Trace**: the customer's recent calls and a live SIP trace filtered to its addresses.

### 3.5 Suspend or delete

Set status `suspended` to stop traffic while keeping everything. Delete removes the customer and its addresses; CDRs, ledger and invoices are kept for accounting.

## 4. Carriers

A carrier is a supplier reached through a FreeSWITCH gateway. It has a **buying rate deck**, a gateway address and transport, capacity limits, and policies.

### 4.1 Create a carrier

Carriers > New carrier:

| Field | Meaning |
|---|---|
| Name, status | as for customers; a carrier with status other than `active` is skipped in routing |
| Buying rate deck | the carrier's price list (a carrier without a rate for the destination is skipped) |
| Gateway host, port, transport | where INVITEs go (`udp`, `tcp`, `tls`); optional registration with username/password/realm for carriers that require it |
| Tech prefix, ANI prefix | digits prepended to the called and calling number toward this carrier |
| Allowed codecs | what to offer the carrier (intersection with the customer's list; transcoding when nothing overlaps) |
| Max concurrent calls, max CPS | carrier capacity; a full carrier is skipped, not queued |
| Failover SIP codes | responses that move the call to the next carrier (defaults come from the failover rules) |
| OPTIONS ping | keep-alive; a gateway that stops answering is marked DOWN and skipped |
| Charge failed attempts | bill the buy rate's connect fee for unanswered attempts (some carriers charge these) |
| Ignore early media | do not pass ringback/announcements from this carrier |
| Media, DTMF, SRTP | as for customers, on the carrier leg |
| Privacy calls | what a carrier receives when the customer asked for privacy: `anonymize` (From anonymous), `pass` (real identity with PAI and `Privacy: id`), `ignore` |

The carrier's account records what you owe it (`cost` entries). Top-ups there represent payments made to the carrier.

### 4.2 Check the gateway

Carrier > Gateway shows the FreeSWITCH gateway state (`UP`, `DOWN`, `NOREG`, `REGED`), the last ping and the circuit breaker state (`degraded` after too many consecutive faults or a low ASR; a degraded carrier is tried last for a minute).

The gateway host is allow-listed in the host firewall automatically; if the carrier sends signalling from other addresses (a cluster), list them in "Extra signalling sources" so its OPTIONS pings and inbound calls are accepted too.

## 5. Rates, routes and header rules

### 5.1 Rate decks

Rate decks (rate groups) hold prices per prefix in one currency. A selling deck is assigned to customers, a buying deck to carriers.

Rate decks > New, then **Import CSV**. The header row must contain `prefix` and `rate_per_min` (or `rate`); optional columns: `destination`, `connect_fee`, `initial_increment`, `subsequent_increment`, `min_duration`, `effective_from`, `effective_to`, `enabled`. Example:

```
prefix,destination,rate_per_min,connect_fee,initial_increment,subsequent_increment,effective_from
44,UK Fixed,0.0100,0,60,60,2026-10-01
447,UK Mobile,0.0200,0,60,60,2026-10-01
1,USA,0.0080,0,6,6,2026-10-01
```

The import is previewed (rows with errors are listed with their line number) and applied atomically. A rate with a future `effective_from` becomes active at that instant; the previous rate stays until then, so price changes can be loaded in advance. Longest prefix wins; `Test` in the deck page shows which rate a number gets.

Prices are decimals with six places; they are never rounded during rating. Increments: `initial_increment` seconds are always charged, then `subsequent_increment` blocks (60/60 is per minute, 6/6 per six seconds, 1/1 per second).

### 5.2 Route groups and routes

A route group is a routing table assigned to customers. Each route is a prefix with an ordered list of carriers. Longest prefix wins; the empty prefix is the default route.

Route groups > group > New route: prefix, destination name, and the carrier list. For each carrier:

* **Order** (drag or arrows): the failover order in normal mode.
* **Weight**: carriers with the same priority share traffic proportionally to weight.
* **Window**: time of day and day of week when the carrier may be used, for example `mon-fri 08:00-18:00 Europe/London` or `sat,sun 00:00-24:00; mon-fri 18:00-24:00`. Empty means always. A closed window skips the carrier.
* **Enabled**: temporarily remove a carrier without deleting the row.

**LCR mode** on the group orders the carriers by buying rate instead of your order. The **Preview** on a route shows, for a sample number, each carrier's buy rate and the margin against a selling deck; negative margins are flagged (and blocked when `SBC_ROUTING_BLOCK_NEGATIVE_MARGIN` is on).

The **Simulator** (menu) runs the whole decision for a customer and a number without placing a call: normalisation, block lists, selling rate, balance check, route, carriers in order with their dial strings, skipped carriers with the reason (`gateway_down`, `carrier_capacity`, `skipped_no_rate`, `outside_window`, `negative_margin_blocked`) and the SIP response the customer would get.

### 5.3 Header rules

On a customer or a carrier, Header rules tab. A rule has a direction (`egress`: the INVITE to the carrier; `response`: responses to the customer), an action and a priority:

* `add` sets a header to a value; placeholders `{caller}`, `{called}`, `{customer}`, `{carrier}`, `{call_uuid}`, `{node_ip}`.
* `passthrough` copies the customer's header of that name to the carrier (by default no customer header is copied; identity headers can never be passed through).
* `remove` cancels a lower priority add or passthrough.

Customer rules apply to all its calls; a carrier rule for the same header wins. Example: customer rule `passthrough X-Account-Id`, carrier rule `add X-Route-Key {customer}-{called}`.

## 6. Watching traffic

### 6.1 Live calls

Calls > Live: every call in progress with customer, carrier, numbers, state, duration, codecs and the FreeSWITCH channel. **Hang up** ends a call (the customer receives a BYE; the call is billed normally). The view also tells you when a reservation exists without a channel in FreeSWITCH (the reconciler cleans those up).

### 6.2 CDRs

CDRs lists every attempted call, including rejections. Filter by time, customer, carrier, disposition, number, SIP code or Call-ID; export the current filter as CSV in one of three shapes: **for the customer** (no carrier, routing or cost columns, safe to send with an invoice), **for the carrier** (no customer, routing or selling columns, for disputes), or **full** (every column including margin, attempts, transports, SRTP, privacy and STIR results). Click a row for the detail: the attempts timeline (each carrier tried, its response, classification, PDD, buy rate and any attempt fee), media (codecs, media mode, RTP statistics: jitter, packet loss, MOS), transport and SRTP per leg, privacy and STIR/SHAKEN result, and the money: selling rate, billed seconds, price, cost, margin, reserved and charged amounts.

Dispositions: `answered`, `no_answer`, `busy`, `failed` (carrier or network fault), `cancelled` (the customer gave up), `rejected_auth` (address, status, TLS/SRTP/STIR policy), `rejected_balance`, `rejected_route` (no route, blocked, invalid number).

**SIP trace** on a CDR (next to the UUID) opens a new tab with every SIP message of the call from the capture store: customer and carrier legs in time order, source and destination, method or status code, and the full headers and body of any message you expand; filter by leg, expand all, download as text or **export as pcap** (customer leg, carrier leg or the complete call) for Wireshark or sngrep, with the exact SIP bytes, addresses, ports and timestamps as captured. Capture is always on for the live stack and messages are kept for the retention period (30 days by default). **trace** next to it shows the SBC's own decision log for the call (API steps, Lua, FreeSWITCH lines).

### 6.3 Reports

Reports has tabs for traffic over time, per customer, per carrier, per destination, profit (revenue, cost, margin), failures by SIP code and carrier, rejections by reason, quality (ASR, ACD, PDD, MOS), peaks (concurrent calls and CPS), the balance statement of any account (opening balance, movements, closing balance, exportable), and top destinations. Every tab takes a time range and, where it applies, a customer or carrier filter; tables export to CSV.

## 7. Money operations

* **Top up** when a customer pays; **adjust** to correct mistakes (always with a description; both are audited).
* **Low balance**: customers under `SBC_BILLING_LOW_BALANCE_THRESHOLD` appear on the dashboard and in System > Notifications.
* **Statement**: Reports > Statement for any period, or the monthly PDF invoice on the customer's Invoices tab.
* **Exchange rates**: when a customer's account currency differs from a rate deck's currency the rate is converted with System > Exchange rates; the CDR stores the rate used.
* **Reconcile**: `make live-reconcile` (or the System > Health tab) proves that every balance equals the sum of its ledger and that every reservation matches a live call. It should always report 0 mismatches.

## 8. System

* **Health**: readiness of Postgres, Redis, the FreeSWITCH link and both SIP profiles; version; node name.
* **Configuration**: the running configuration (secrets hidden).
* **Failover rules**: how SIP responses are classified (`carrier_fault`, `number_fault`, `busy`, `no_answer`, ...) and which ones try the next carrier.
* **Global blacklist**: prefixes no customer may call (premium rate, satellite, fraud destinations).
* **Banned IPs**: addresses banned automatically (scanner protection) or by hand; unban here. On a live box bans are also enforced in the host firewall.
* **Exchange rates**: currency pairs used for cross currency rating.
* **Settings**: free form key/value settings (UI preferences).
* **Notifications**: low balance and other operational notices.
* **SIP trace**: capture SIP on the customer profile for N minutes (filtered per customer in the customer's Trace tab).
* **Users** (admin): create users with a role, reset passwords with a one time link, disable accounts. **API keys** (admin): keys with a role for integrations, sent as `Authorization: Bearer sbc_...`. **Audit log**: who changed what, with before and after values.
* **Gateways**: FreeSWITCH gateway states for all carriers.

## 9. Common tasks, step by step

**Onboard a new customer**
1. Rate decks: make sure the selling deck exists (import the CSV).
2. Route groups: make sure the routes cover the destinations the customer will call.
3. Customers > New customer: name, deck, route group, limits, policies.
4. IP addresses: add the customer's addresses.
5. Account: top up with the prepaid amount.
6. Simulator: check a few numbers give the expected carrier and price.
7. Ask the customer for a test call; check the CDR.

**Add a new carrier for a destination**
1. Rate decks: import the carrier's buying deck.
2. Carriers > New carrier: gateway, transport, deck, codecs, capacity.
3. If the carrier signals from addresses other than its gateway host, fill "Extra signalling sources".
4. Carrier > Gateway: wait for `UP` (or `NOREG` when pinging is off).
5. Route groups: add the carrier to the routes, set order, weight, window.
6. Simulator or Preview: check margins.

**A customer says "all my calls fail"**
1. CDRs filtered by the customer: read the disposition and SIP code.
2. `403 IP not authorized`: add the address (and unban it in System > Banned IPs if it got banned).
3. `402 Not enough funds`: top up or set allowed credit.
4. `503 No route` / `404 No rate for destination`: routes or selling deck missing the prefix; use the Simulator.
5. `503 All carriers failed`: the attempts timeline shows each carrier's response; check the Gateway tab and the carrier's own status.
6. No CDR at all: the INVITE never reached the SBC (firewall, wrong address or port, banned address dropped by the host firewall).

**Change prices from a date**
1. Prepare the CSV with `effective_from` set to the date.
2. Import into the existing deck; the new rows sit next to the old ones.
3. Test with a number and a date in the deck page; nothing changes until the date.
