# Roadmap

Features from the specification that are documented rather than built, with the FreeSWITCH mechanism each one would use. Items are ordered by how often a wholesale operator asks for them. The Section 7 features (SIP TLS and SRTP, header rules, privacy, 100rel, media bypass, DTMF interworking, scanner bans) are built; see D-52 to D-60.

| Feature | Mechanism | Notes |
|---------|-----------|-------|
| Rate deck versioning and diff | `effective_from`/`effective_to` already allow future decks; a UI diff between two imports is not built | |
| Per-attempt PDD from the b-leg | `uuid_dump` of the b-leg is not available after the bridge returns; the current PDD attribution uses the a-leg progress timestamps (D-29) | An ESL listener on b-leg `CHANNEL_PROGRESS` could store exact times per attempt |
| Email and webhook delivery of notifications | `notifications` rows are written (low balance); a delivery worker with SMTP/webhook config would consume them | |
| Number portability lookup | An external ENUM/HTTP query before Step 3 (`npdi`, `rn` parameters) | |
| Seamless failover of calls in progress | A stateless SIP proxy layer (Kamailio dispatcher) in front of two FreeSWITCH nodes with media anchored on the proxy side, or the commercial FreeSWITCH HA extensions | The active-passive design (docs/HA.md) drops the calls of a failed node |
