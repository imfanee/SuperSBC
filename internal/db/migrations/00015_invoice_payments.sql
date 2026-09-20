-- +goose Up
-- Invoice ledger (D-72): custom invoice periods, the invoiced amount, and
-- payments received against invoices (accounts receivable), kept apart from
-- the prepaid call ledger.
ALTER TABLE invoices DROP CONSTRAINT invoices_account_id_period_start_key;
ALTER TABLE invoices ADD CONSTRAINT invoices_account_period_key UNIQUE (account_id, period_start, period_end);
ALTER TABLE invoices ADD COLUMN kind text NOT NULL DEFAULT 'monthly' CHECK (kind IN ('monthly', 'custom'));
ALTER TABLE invoices ADD COLUMN amount numeric(18,6) NOT NULL DEFAULT 0;
UPDATE invoices SET amount = CASE owner_type WHEN 'customer' THEN -charges ELSE -costs END;

CREATE TABLE invoice_payments (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id      uuid NOT NULL REFERENCES accounts(id),
    amount          numeric(18,6) NOT NULL CHECK (amount > 0),
    currency        char(3) NOT NULL,
    received_at     timestamptz NOT NULL DEFAULT now(),
    reference       text NOT NULL DEFAULT '',
    method          text NOT NULL DEFAULT '',
    notes           text NOT NULL DEFAULT '',
    ledger_entry_id bigint REFERENCES ledger_entries(id),
    created_by      text,
    created_at      timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX invoice_payments_account_idx ON invoice_payments (account_id, received_at DESC);

CREATE TABLE invoice_allocations (
    payment_id  uuid NOT NULL REFERENCES invoice_payments(id) ON DELETE CASCADE,
    invoice_id  uuid NOT NULL REFERENCES invoices(id),
    amount      numeric(18,6) NOT NULL CHECK (amount > 0),
    PRIMARY KEY (payment_id, invoice_id)
);
CREATE INDEX invoice_allocations_invoice_idx ON invoice_allocations (invoice_id);

-- +goose Down
DROP TABLE invoice_allocations;
DROP TABLE invoice_payments;
ALTER TABLE invoices DROP COLUMN amount, DROP COLUMN kind;
ALTER TABLE invoices DROP CONSTRAINT invoices_account_period_key;
ALTER TABLE invoices ADD CONSTRAINT invoices_account_id_period_start_key UNIQUE (account_id, period_start);
