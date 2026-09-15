-- +goose Up
-- Monthly invoices / statements per account (roadmap item, D-63). One row per
-- account and period; the PDF is rendered on demand from the stored figures.
CREATE SEQUENCE invoice_seq;
CREATE TABLE invoices (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    number        text NOT NULL UNIQUE,
    account_id    uuid NOT NULL REFERENCES accounts(id),
    owner_type    text NOT NULL,
    owner_id      uuid NOT NULL,
    owner_name    text NOT NULL,
    period_start  timestamptz NOT NULL,
    period_end    timestamptz NOT NULL,
    currency      char(3) NOT NULL,
    opening       numeric(18,6) NOT NULL,
    topups        numeric(18,6) NOT NULL,
    charges       numeric(18,6) NOT NULL,
    costs         numeric(18,6) NOT NULL,
    adjustments   numeric(18,6) NOT NULL,
    refunds       numeric(18,6) NOT NULL,
    closing       numeric(18,6) NOT NULL,
    calls         bigint NOT NULL,
    billed_seconds bigint NOT NULL,
    lines         jsonb NOT NULL DEFAULT '[]'::jsonb,
    created_at    timestamptz NOT NULL DEFAULT now(),
    UNIQUE (account_id, period_start)
);
CREATE INDEX invoices_owner_idx ON invoices (owner_type, owner_id, period_start DESC);

-- +goose Down
DROP TABLE invoices;
DROP SEQUENCE invoice_seq;
