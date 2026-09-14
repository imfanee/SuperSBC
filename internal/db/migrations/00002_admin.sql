-- +goose Up
-- +goose StatementBegin
-- Block lists (Section 7, routing and policy): global blacklist and per-customer blocked prefixes.
CREATE TABLE blocked_prefixes (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    customer_id uuid REFERENCES customers(id) ON DELETE CASCADE,   -- NULL = global
    prefix      text NOT NULL CHECK (prefix ~ '^[0-9]+$'),
    reason      text NOT NULL DEFAULT '',
    enabled     boolean NOT NULL DEFAULT true,
    created_at  timestamptz NOT NULL DEFAULT now(),
    UNIQUE (customer_id, prefix)
);
CREATE UNIQUE INDEX blocked_prefixes_global_idx ON blocked_prefixes (prefix) WHERE customer_id IS NULL;

-- Exchange rates for multi-currency accounts (Section 7, billing): 1 base = rate quote.
CREATE TABLE fx_rates (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    base           char(3) NOT NULL,
    quote          char(3) NOT NULL,
    rate           numeric(18,8) NOT NULL CHECK (rate > 0),
    effective_from timestamptz NOT NULL DEFAULT now(),
    created_by     text,
    created_at     timestamptz NOT NULL DEFAULT now(),
    UNIQUE (base, quote, effective_from)
);

-- Refresh tokens are stored hashed so a database leak cannot mint sessions.
CREATE TABLE sessions (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash  text NOT NULL UNIQUE,
    expires_at  timestamptz NOT NULL,
    revoked_at  timestamptz,
    remote_ip   inet,
    user_agent  text,
    created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX sessions_user_idx ON sessions (user_id);

-- Login attempts for rate limiting (per email and per IP).
CREATE TABLE login_attempts (
    id         bigserial PRIMARY KEY,
    email      text NOT NULL,
    remote_ip  inet,
    success    boolean NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX login_attempts_email_idx ON login_attempts (email, created_at DESC);
CREATE INDEX login_attempts_ip_idx ON login_attempts (remote_ip, created_at DESC);

-- Per-customer international prefix (D-26) and PAI trust flag (D-43).
ALTER TABLE customers ADD COLUMN intl_prefix text NOT NULL DEFAULT '00';
ALTER TABLE customers ADD COLUMN trust_pai boolean NOT NULL DEFAULT false;
ALTER TABLE customers ADD COLUMN blocked_prefixes_enabled boolean NOT NULL DEFAULT true;

-- Currency and the exchange rate applied, stored on the CDR (D-45).
ALTER TABLE cdrs ADD COLUMN sell_currency char(3);
ALTER TABLE cdrs ADD COLUMN buy_currency char(3);
ALTER TABLE cdrs ADD COLUMN sell_fx numeric(18,8) NOT NULL DEFAULT 1;
ALTER TABLE cdrs ADD COLUMN buy_fx numeric(18,8) NOT NULL DEFAULT 1;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE cdrs DROP COLUMN IF EXISTS sell_currency, DROP COLUMN IF EXISTS buy_currency, DROP COLUMN IF EXISTS sell_fx, DROP COLUMN IF EXISTS buy_fx;
ALTER TABLE customers DROP COLUMN IF EXISTS intl_prefix, DROP COLUMN IF EXISTS trust_pai, DROP COLUMN IF EXISTS blocked_prefixes_enabled;
DROP TABLE IF EXISTS login_attempts, sessions, fx_rates, blocked_prefixes;
-- +goose StatementEnd
