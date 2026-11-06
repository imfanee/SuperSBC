-- +goose Up
-- Deleting unpaid invoices (D-88). A deleted invoice stays in the table,
-- marked with who deleted it, when and why, so the number sequence has no
-- holes and the invoice ledger can show the reversal. Only live invoices hold
-- their period: the overlap rule and the uniqueness of a period ignore
-- deleted rows, so the days can be invoiced again.
ALTER TABLE invoices ADD COLUMN deleted_at timestamptz;
ALTER TABLE invoices ADD COLUMN deleted_by text;
ALTER TABLE invoices ADD COLUMN delete_reason text NOT NULL DEFAULT '';
ALTER TABLE invoices DROP CONSTRAINT invoices_account_period_key;
CREATE UNIQUE INDEX invoices_account_period_live_key ON invoices (account_id, period_start, period_end) WHERE deleted_at IS NULL;

-- +goose Down
DROP INDEX invoices_account_period_live_key;
DELETE FROM invoices WHERE deleted_at IS NOT NULL;
ALTER TABLE invoices ADD CONSTRAINT invoices_account_period_key UNIQUE (account_id, period_start, period_end);
ALTER TABLE invoices DROP COLUMN delete_reason, DROP COLUMN deleted_by, DROP COLUMN deleted_at;
