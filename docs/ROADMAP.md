# Roadmap

Features from the specification that are documented rather than built, with the FreeSWITCH mechanism each one would use. Items are ordered by how often a wholesale operator asks for them. The Section 7 features (SIP TLS and SRTP, header rules, privacy, 100rel, media bypass, DTMF interworking, scanner bans) are built; see D-52 to D-60.

| Feature | Mechanism | Notes |
|---------|-----------|-------|
| Time-of-day and day-of-week routing windows | `route_carriers.window` (cron-like or `mon-fri 08:00-18:00 Europe/London`); `Route` filters carriers outside their window | Pure Go, unit testable |
| Carrier failed attempt charging | `carriers.charge_failed_attempts` exists; billing would rate each attempt with `classification != answered` at the carrier's connect fee | Needs a `cost` ledger entry per attempt |
| Invoices and statements as PDF | `reports.Statement` is the data; render with a Go PDF library on a monthly cron | CSV export exists today |
| STIR/SHAKEN | A `passthrough` header rule for `Identity` already forwards the customer's signature; verification and signing need an STI-VS/AS integration | Not started |
| High availability | Two SBC hosts behind a VIP (keepalived, VRRP); Postgres primary and streaming replica with automatic failover (Patroni or a managed service); Redis Sentinel; the Go API is stateless and can run on both hosts. FreeSWITCH call state is not replicated: in-flight calls drop on failover, reservations are released by the reconciliation worker on the surviving node within `orphan_timeout` | Documented honestly: FreeSWITCH has no shared call state without commercial extensions |
| HEP/HOMER capture | Uncomment `capture-server` in `sofia.conf.xml` and set `sip-capture=yes` on both profiles; FreeSWITCH sends HEPv3 to HOMER | The SIP trace toggle in the UI is the built-in alternative |
| Rate deck versioning and diff | `effective_from`/`effective_to` already allow future decks; a UI diff between two imports is not built | |
| Per-attempt PDD from the b-leg | `uuid_dump` of the b-leg is not available after the bridge returns; the current PDD attribution uses the a-leg progress timestamps (D-29) | An ESL listener on b-leg `CHANNEL_PROGRESS` could store exact times per attempt |
| Email and webhook delivery of notifications | `notifications` rows are written (low balance); a delivery worker with SMTP/webhook config would consume them | |
| Least cost routing with quality weighting | LCR mode orders by buy rate; a quality score (ASR, PDD from the roll-ups) could be blended in | |
| Number portability lookup | An external ENUM/HTTP query before Step 3 (`npdi`, `rn` parameters) | |
| Volumetric flood protection at the packet level | Export `banned_ips` to an nftables set on the host (the ACL ban stops calls, not packets) and rate limit UDP 5060 per source with `limit rate` | The ban list and its API exist; a small exporter would keep the set in sync |
| Per customer TLS client certificate verification | `SBC_FS_TLS_VERIFY_POLICY=in` plus a CA bundle in `cafile.pem`; bind the certificate subject to the customer | Verify policy is `none` today (D-53) |
