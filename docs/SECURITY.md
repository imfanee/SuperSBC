# Security review checklist

OWASP ASVS 4.0 level 1 walkthrough of OpenSBC as built. "Yes" means implemented and covered by a test where one is practical; "Partial" and "No" items are tracked in ROADMAP.md.

## V2 Authentication

| Requirement | Status | Where |
|-------------|--------|-------|
| Passwords at least 10 characters (2.1.1 relaxed from 12 for operator convenience; change `min=10` in `auth.go` to tighten) | Yes | `internal/auth`, validator tags, zod schemas |
| Passwords stored with a memory-hard hash (2.4.1) | Yes, argon2id 19 MiB, t=2 | `auth.HashPassword` |
| Credential stuffing and brute force throttling (2.2.1) | Yes, 10 failures per email and 100 per IP in 15 minutes return 429 | `login_attempts` table, `TestM4_AuthAndRoles` |
| Generic error on bad credentials (2.2.2) | Yes, "invalid credentials" for unknown user and wrong password alike | `auth.go` |
| Optional TOTP second factor (2.8) | Yes, RFC 6238 via `pquerna/otp`, enabled after a confirmed code | `/auth/totp/*` |
| Password change requires the current password and revokes other sessions (2.1.5, 3.3.3) | Yes | `SetPassword` revokes sessions |
| Password reset tokens single use, time limited, hashed at rest (2.5) | Yes, one hour, SHA-256 stored | `password_resets` |
| No default credentials | Partial: the bootstrap admin password comes from `.env` and must be changed; the API refuses to create it when empty | `bootstrapAdmin` |

## V3 Session management

| Requirement | Status | Where |
|-------------|--------|-------|
| Session tokens in httpOnly, Secure, SameSite cookies (3.4) | Yes (Secure when `SBC_AUTH_COOKIE_SECURE=true`, set it behind TLS) | `issueSession` |
| Short-lived access token, refresh rotation (3.3) | Yes: 15 minute JWT, 7 day refresh rotated on every use, old token revoked | `refresh` handler |
| Logout invalidates the session (3.3.1) | Yes | `logout` |
| Refresh tokens stored hashed (3.2.3) | Yes | `sessions.token_hash` |
| API keys: revocable, hashed, prefix shown | Yes | `api_keys` |

## V4 Access control

| Requirement | Status | Where |
|-------------|--------|-------|
| Deny by default, enforced server side (4.1.1, 4.1.3) | Yes: every route under `authenticate`; writers need `operator`, money and users need `admin` | `requireRole` |
| CSRF protection on state changes (4.2.2) | Yes: double-submit cookie plus header, SameSite=Strict | `csrf` middleware, tested |
| Users cannot escalate or delete themselves (4.1.5) | Yes | `updateUser`, `deleteUser` |
| Internal call control API isolated | Yes: separate listener, shared secret header or basic auth, docker network only | `internalapi.auth` |

## V5 Validation, sanitisation, encoding

| Requirement | Status | Where |
|-------------|--------|-------|
| Positive validation of all input (5.1.3) | Yes: struct tags on every body, digits-only prefixes, decimal strings, CIDR by Postgres, allow-lists for `sort` and `group_by` | handlers |
| Parameterised queries only (5.3.4) | Yes | `internal/store`, `internal/reports` (dynamic parts come from allow-lists) |
| CSV import bounded and validated row by row (5.1) | Yes, 32 MiB, errors reported per line | `parseRatesCSV` |
| Output encoding | Yes: React escapes by default; no `dangerouslySetInnerHTML` | web |
| Lua to API payload safety | Yes: JSON encoder escapes quotes, spaces and backslashes as `\uXXXX` so mod_curl cannot split or unescape them | `sbc/json.lua` |

## V7 Error handling and logging

| Requirement | Status | Where |
|-------------|--------|-------|
| No stack traces or SQL to clients | Yes: `failErr` maps errors; 500 carries the message only | `server.go` |
| Security events logged (7.1) | Yes: login (success and failure), logout, every mutation with actor, entity and source IP in `audit_log` | `Handler.audit` |
| No secrets in logs (7.1.1) | Yes: passwords, API keys, tokens and carrier SIP passwords are never logged or returned | reviewed |

## V8 Data protection

| Requirement | Status | Where |
|-------------|--------|-------|
| Sensitive data not cached (8.2.1) | Yes: `Cache-Control: no-store` on the API; only UI preferences in `localStorage` | `securityHeaders` |
| Secrets outside the repository (8.3) | Yes: `.env` only, `.env.example` documents them | |
| Backups | Yes, optional nightly pg_dump | `deploy/backup` |

## V9 Communication

| Requirement | Status | Where |
|-------------|--------|-------|
| TLS for the admin UI | No (out of scope for compose): put nginx or a load balancer with TLS in front and set `SBC_AUTH_COOKIE_SECURE=true` | OPERATIONS.md |
| SIP TLS and SRTP | Roadmap | ROADMAP.md |

## V13 API

| Requirement | Status | Where |
|-------------|--------|-------|
| JSON content type enforced, bodies size limited (13.1) | Yes: 8 MiB body limit, JSON decoding errors return 400 | `decode` |
| Security headers (14.4) | Yes: CSP without inline scripts, `X-Frame-Options: DENY`, `nosniff`, `Referrer-Policy` on both nginx and the API | `web/nginx.conf`, `securityHeaders` |
| OpenAPI documented | Yes | `/api/docs` |

## VoIP specific

| Threat | Mitigation |
|--------|------------|
| Toll fraud from a compromised customer PBX | prepaid reservation per call, `max_call_seconds` from the balance, per customer CPS and channel limits, blocked prefixes (global and per customer), low balance notifications |
| SIP scanners | `SBC_ACL_MODE=strict` rejects unknown addresses in Sofia; rejections are counted per IP in `rejected_auth` CDRs and metrics; dynamic blocklisting is on the roadmap |
| Carrier spoofing inbound calls | the egress profile rejects every inbound INVITE (`403 Inbound not permitted`) |
| Header leakage | customer X-headers, PAI and RPID are stripped on egress; our own User-Agent; From carries the SBC address |
| Money loss on outages | fail closed: API down, Redis down or slow means `503 SBC internal error`, never an unbilled call; orphan reservations are released by the reconciler |
