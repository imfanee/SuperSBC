-- +goose Up
-- +goose StatementBegin
CREATE EXTENSION IF NOT EXISTS "pgcrypto";

CREATE TYPE customer_status AS ENUM ('active', 'suspended', 'blocked');
CREATE TYPE carrier_status AS ENUM ('active', 'disabled');
CREATE TYPE transport_kind AS ENUM ('udp', 'tcp', 'tls', 'any');
CREATE TYPE owner_kind AS ENUM ('customer', 'carrier');
CREATE TYPE ledger_kind AS ENUM ('reserve', 'release', 'charge', 'cost', 'topup', 'adjustment', 'refund');
CREATE TYPE disposition_kind AS ENUM ('pending', 'answered', 'no_answer', 'busy', 'failed', 'rejected_auth', 'rejected_balance', 'rejected_route', 'cancelled');

CREATE TABLE rate_groups (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name        text NOT NULL UNIQUE,
    currency    char(3) NOT NULL DEFAULT 'USD',
    description text NOT NULL DEFAULT '',
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now(),
    deleted_at  timestamptz
);

CREATE TABLE rates (
    id                   uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    rate_group_id        uuid NOT NULL REFERENCES rate_groups(id) ON DELETE CASCADE,
    prefix               text NOT NULL CHECK (prefix ~ '^[0-9]+$'),
    destination          text NOT NULL DEFAULT '',
    rate_per_min         numeric(18,6) NOT NULL CHECK (rate_per_min >= 0),
    connect_fee          numeric(18,6) NOT NULL DEFAULT 0 CHECK (connect_fee >= 0),
    initial_increment    int NOT NULL DEFAULT 60 CHECK (initial_increment > 0),
    subsequent_increment int NOT NULL DEFAULT 60 CHECK (subsequent_increment > 0),
    min_duration         int NOT NULL DEFAULT 0 CHECK (min_duration >= 0),
    effective_from       timestamptz NOT NULL DEFAULT now(),
    effective_to         timestamptz,
    enabled              boolean NOT NULL DEFAULT true,
    created_at           timestamptz NOT NULL DEFAULT now(),
    updated_at           timestamptz NOT NULL DEFAULT now(),
    UNIQUE (rate_group_id, prefix, effective_from)
);
CREATE INDEX rates_prefix_idx ON rates (rate_group_id, prefix text_pattern_ops);

CREATE TABLE route_groups (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name        text NOT NULL UNIQUE,
    description text NOT NULL DEFAULT '',
    lcr_mode    boolean NOT NULL DEFAULT false,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now(),
    deleted_at  timestamptz
);

CREATE TABLE routes (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    route_group_id uuid NOT NULL REFERENCES route_groups(id) ON DELETE CASCADE,
    prefix         text NOT NULL CHECK (prefix ~ '^[0-9]*$'),
    destination    text NOT NULL DEFAULT '',
    enabled        boolean NOT NULL DEFAULT true,
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now(),
    UNIQUE (route_group_id, prefix)
);

CREATE TABLE carriers (
    id                     uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name                   text NOT NULL UNIQUE CHECK (name ~ '^[a-z0-9_-]+$'),
    status                 carrier_status NOT NULL DEFAULT 'active',
    rate_group_id          uuid REFERENCES rate_groups(id),
    gateway_host           text NOT NULL,
    gateway_port           int NOT NULL DEFAULT 5060,
    transport              transport_kind NOT NULL DEFAULT 'udp',
    dni_prefix             text NOT NULL DEFAULT '',
    ani_prefix             text NOT NULL DEFAULT '',
    strip_digits           int NOT NULL DEFAULT 0,
    auth_username          text,
    auth_password          text,
    from_domain            text,
    register               boolean NOT NULL DEFAULT false,
    allowed_codecs         text[] NOT NULL DEFAULT '{PCMA,PCMU}',
    max_concurrent_calls   int NOT NULL DEFAULT 0,
    max_cps                int NOT NULL DEFAULT 0,
    failover_sip_codes     int[],
    sip_options_ping       boolean NOT NULL DEFAULT true,
    charge_failed_attempts boolean NOT NULL DEFAULT false,
    ignore_early_media     boolean NOT NULL DEFAULT false,
    notes                  text NOT NULL DEFAULT '',
    created_at             timestamptz NOT NULL DEFAULT now(),
    updated_at             timestamptz NOT NULL DEFAULT now(),
    deleted_at             timestamptz
);

CREATE TABLE route_carriers (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    route_id   uuid NOT NULL REFERENCES routes(id) ON DELETE CASCADE,
    carrier_id uuid NOT NULL REFERENCES carriers(id) ON DELETE CASCADE,
    priority   int NOT NULL CHECK (priority > 0),
    weight     int NOT NULL DEFAULT 100 CHECK (weight > 0),
    enabled    boolean NOT NULL DEFAULT true,
    UNIQUE (route_id, carrier_id)
);

CREATE TABLE customers (
    id                   uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name                 text NOT NULL UNIQUE,
    status               customer_status NOT NULL DEFAULT 'active',
    rate_group_id        uuid REFERENCES rate_groups(id),
    route_group_id       uuid REFERENCES route_groups(id),
    max_concurrent_calls int NOT NULL DEFAULT 0,
    max_cps              int NOT NULL DEFAULT 0,
    allowed_codecs       text[] NOT NULL DEFAULT '{PCMA,PCMU}',
    tech_prefix          text,
    default_country_code text,
    notes                text NOT NULL DEFAULT '',
    created_at           timestamptz NOT NULL DEFAULT now(),
    updated_at           timestamptz NOT NULL DEFAULT now(),
    deleted_at           timestamptz
);

CREATE TABLE customer_ips (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    customer_id uuid NOT NULL REFERENCES customers(id) ON DELETE CASCADE,
    ip_cidr     cidr NOT NULL,
    port        int,
    transport   transport_kind NOT NULL DEFAULT 'any',
    created_at  timestamptz NOT NULL DEFAULT now(),
    UNIQUE (ip_cidr, port, transport)
);
CREATE INDEX customer_ips_cidr_idx ON customer_ips USING gist (ip_cidr inet_ops);

CREATE TABLE accounts (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    owner_type     owner_kind NOT NULL,
    owner_id       uuid NOT NULL,
    currency       char(3) NOT NULL DEFAULT 'USD',
    balance        numeric(18,6) NOT NULL DEFAULT 0,
    allowed_credit numeric(18,6) NOT NULL DEFAULT 0 CHECK (allowed_credit >= 0),
    reserved       numeric(18,6) NOT NULL DEFAULT 0 CHECK (reserved >= 0),
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now(),
    UNIQUE (owner_type, owner_id)
);

-- The one and only definition of available balance (Section 2.1).
CREATE FUNCTION account_available(a accounts) RETURNS numeric
LANGUAGE sql IMMUTABLE AS $$ SELECT a.balance + a.allowed_credit - a.reserved $$;

CREATE TABLE ledger_entries (
    id            bigserial PRIMARY KEY,
    account_id    uuid NOT NULL REFERENCES accounts(id),
    call_uuid     uuid,
    type          ledger_kind NOT NULL,
    amount        numeric(18,6) NOT NULL,
    balance_after numeric(18,6) NOT NULL,
    description   text NOT NULL DEFAULT '',
    created_by    text,
    created_at    timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX ledger_entries_account_idx ON ledger_entries (account_id, created_at DESC);
CREATE INDEX ledger_entries_call_idx ON ledger_entries (call_uuid) WHERE call_uuid IS NOT NULL;

CREATE TABLE cdrs (
    call_uuid            uuid PRIMARY KEY,
    customer_id          uuid REFERENCES customers(id),
    carrier_id           uuid REFERENCES carriers(id),
    src_ip               inet,
    src_port             int,
    caller_number_raw    text NOT NULL DEFAULT '',
    caller_number        text NOT NULL DEFAULT '',
    called_number_raw    text NOT NULL DEFAULT '',
    called_number        text NOT NULL DEFAULT '',
    start_time           timestamptz NOT NULL DEFAULT now(),
    progress_time        timestamptz,
    answer_time          timestamptz,
    end_time             timestamptz,
    pdd_ms               int,
    ring_seconds         int NOT NULL DEFAULT 0,
    billsec              int NOT NULL DEFAULT 0,
    duration             int NOT NULL DEFAULT 0,
    sip_final_code       int,
    sip_final_reason     text,
    hangup_cause         text,
    disposition          disposition_kind NOT NULL DEFAULT 'pending',
    reject_reason        text,
    sell_rate_id         uuid REFERENCES rates(id),
    sell_rate_per_min    numeric(18,6),
    sell_billed_seconds  int NOT NULL DEFAULT 0,
    sell_price           numeric(18,6) NOT NULL DEFAULT 0,
    sell_destination     text,
    buy_rate_id          uuid REFERENCES rates(id),
    buy_rate_per_min     numeric(18,6),
    buy_billed_seconds   int NOT NULL DEFAULT 0,
    cost                 numeric(18,6) NOT NULL DEFAULT 0,
    margin               numeric(18,6) GENERATED ALWAYS AS (sell_price - cost) STORED,
    negative_margin      boolean NOT NULL DEFAULT false,
    reserved_amount      numeric(18,6) NOT NULL DEFAULT 0,
    charged_amount       numeric(18,6) NOT NULL DEFAULT 0,
    released_amount      numeric(18,6) NOT NULL DEFAULT 0,
    attempts             jsonb NOT NULL DEFAULT '[]'::jsonb,
    failover_depth       int NOT NULL DEFAULT 0,
    codec_in             text,
    codec_out            text,
    media_mode           text,
    rtp_stats            jsonb,
    sbc_node             text,
    billed_at            timestamptz,
    billed_by            text,
    created_at           timestamptz NOT NULL DEFAULT now(),
    updated_at           timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX cdrs_start_time_idx ON cdrs (start_time DESC);
CREATE INDEX cdrs_customer_idx ON cdrs (customer_id, start_time DESC);
CREATE INDEX cdrs_carrier_idx ON cdrs (carrier_id, start_time DESC);
CREATE INDEX cdrs_called_idx ON cdrs (called_number text_pattern_ops);
CREATE INDEX cdrs_pending_idx ON cdrs (start_time) WHERE disposition = 'pending';

CREATE TABLE active_calls (
    call_uuid       uuid PRIMARY KEY REFERENCES cdrs(call_uuid) ON DELETE CASCADE,
    customer_id     uuid NOT NULL REFERENCES customers(id),
    account_id      uuid NOT NULL REFERENCES accounts(id),
    called_number   text NOT NULL,
    reserved_amount numeric(18,6) NOT NULL,
    max_call_seconds int NOT NULL,
    started_at      timestamptz NOT NULL DEFAULT now(),
    expires_at      timestamptz NOT NULL
);
CREATE INDEX active_calls_account_idx ON active_calls (account_id);
CREATE INDEX active_calls_expires_idx ON active_calls (expires_at);

CREATE TABLE notifications (
    id          bigserial PRIMARY KEY,
    kind        text NOT NULL,
    owner_type  owner_kind,
    owner_id    uuid,
    payload     jsonb NOT NULL DEFAULT '{}'::jsonb,
    delivered_at timestamptz,
    created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE users (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    email         text NOT NULL UNIQUE,
    password_hash text NOT NULL,
    role          text NOT NULL CHECK (role IN ('admin', 'operator', 'viewer')),
    totp_secret   text,
    totp_enabled  boolean NOT NULL DEFAULT false,
    status        text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'disabled')),
    last_login_at timestamptz,
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE api_keys (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name        text NOT NULL,
    key_hash    text NOT NULL UNIQUE,
    key_prefix  text NOT NULL,
    last_used_at timestamptz,
    revoked_at  timestamptz,
    created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE audit_log (
    id          bigserial PRIMARY KEY,
    actor_id    uuid,
    actor_email text,
    action      text NOT NULL,
    entity_type text NOT NULL,
    entity_id   text,
    before      jsonb,
    after       jsonb,
    remote_ip   inet,
    created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX audit_log_created_idx ON audit_log (created_at DESC);

CREATE TABLE system_settings (
    key        text PRIMARY KEY,
    value      jsonb NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now(),
    updated_by text
);

-- updated_at maintenance
CREATE FUNCTION set_updated_at() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN NEW.updated_at = now(); RETURN NEW; END $$;

DO $$
DECLARE t text;
BEGIN
  FOREACH t IN ARRAY ARRAY['rate_groups','rates','route_groups','routes','carriers','customers','accounts','cdrs','users']
  LOOP
    EXECUTE format('CREATE TRIGGER %I_updated_at BEFORE UPDATE ON %I FOR EACH ROW EXECUTE FUNCTION set_updated_at()', t, t);
  END LOOP;
END $$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS system_settings, audit_log, api_keys, users, notifications, active_calls, cdrs, ledger_entries, accounts, customer_ips, customers, route_carriers, carriers, routes, route_groups, rates, rate_groups CASCADE;
DROP FUNCTION IF EXISTS set_updated_at();
DROP FUNCTION IF EXISTS account_available(accounts);
DROP TYPE IF EXISTS disposition_kind, ledger_kind, owner_kind, transport_kind, carrier_status, customer_status;
-- +goose StatementEnd
