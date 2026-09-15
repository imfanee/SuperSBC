-- +goose Up
-- +goose StatementBegin
-- Section 7 [M5] features: media policy, DTMF, privacy, SRTP, header rules, scanner bans.
CREATE TYPE media_mode_kind AS ENUM ('anchor', 'proxy', 'bypass');
CREATE TYPE dtmf_kind AS ENUM ('rfc2833', 'info', 'inband');
CREATE TYPE srtp_kind AS ENUM ('off', 'optional', 'mandatory');
CREATE TYPE privacy_kind AS ENUM ('anonymize', 'pass', 'ignore');

ALTER TABLE customers
  ADD COLUMN media_mode  media_mode_kind NOT NULL DEFAULT 'anchor',
  ADD COLUMN dtmf_mode   dtmf_kind NOT NULL DEFAULT 'rfc2833',
  ADD COLUMN srtp_mode   srtp_kind NOT NULL DEFAULT 'optional',
  ADD COLUMN require_tls boolean NOT NULL DEFAULT false;

ALTER TABLE carriers
  ADD COLUMN media_mode   media_mode_kind NOT NULL DEFAULT 'anchor',
  ADD COLUMN dtmf_mode    dtmf_kind NOT NULL DEFAULT 'rfc2833',
  ADD COLUMN srtp_mode    srtp_kind NOT NULL DEFAULT 'off',
  ADD COLUMN privacy_mode privacy_kind NOT NULL DEFAULT 'anonymize';

-- Header manipulation rules. direction "egress": the INVITE towards the
-- carrier; "response": responses towards the customer. Customer rules apply
-- to the calls it originates, carrier rules to the calls it receives.
CREATE TABLE header_rules (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    owner_type owner_kind NOT NULL,
    owner_id   uuid NOT NULL,
    direction  text NOT NULL CHECK (direction IN ('egress', 'response')),
    action     text NOT NULL CHECK (action IN ('add', 'passthrough', 'remove')),
    header     text NOT NULL CHECK (header ~ '^[A-Za-z0-9-]+$'),
    value      text NOT NULL DEFAULT '',
    priority   int NOT NULL DEFAULT 100,
    enabled    boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX header_rules_owner_idx ON header_rules (owner_type, owner_id);

-- Dynamic blocklist for scanners (Section 7, SIP scanner / flood protection).
CREATE TABLE banned_ips (
    ip         inet PRIMARY KEY,
    reason     text NOT NULL DEFAULT '',
    hits       int NOT NULL DEFAULT 0,
    manual     boolean NOT NULL DEFAULT false,
    banned_at  timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz
);

ALTER TABLE cdrs
  ADD COLUMN transport_in text,
  ADD COLUMN transport_out text,
  ADD COLUMN srtp_in boolean NOT NULL DEFAULT false,
  ADD COLUMN srtp_out boolean NOT NULL DEFAULT false,
  ADD COLUMN privacy boolean NOT NULL DEFAULT false;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE cdrs DROP COLUMN IF EXISTS transport_in, DROP COLUMN IF EXISTS transport_out, DROP COLUMN IF EXISTS srtp_in, DROP COLUMN IF EXISTS srtp_out, DROP COLUMN IF EXISTS privacy;
DROP TABLE IF EXISTS banned_ips, header_rules;
ALTER TABLE carriers DROP COLUMN IF EXISTS media_mode, DROP COLUMN IF EXISTS dtmf_mode, DROP COLUMN IF EXISTS srtp_mode, DROP COLUMN IF EXISTS privacy_mode;
ALTER TABLE customers DROP COLUMN IF EXISTS media_mode, DROP COLUMN IF EXISTS dtmf_mode, DROP COLUMN IF EXISTS srtp_mode, DROP COLUMN IF EXISTS require_tls;
DROP TYPE IF EXISTS privacy_kind, srtp_kind, dtmf_kind, media_mode_kind;
-- +goose StatementEnd
