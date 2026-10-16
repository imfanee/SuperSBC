-- +goose Up
-- Why a call was rejected, in enough detail to act on (D-77). reject_reason is
-- what the caller was told ("No route"); reject_detail is what an operator
-- needs ("carrier drc_avo skipped by lossless routing: buy 0.220000 > sell
-- 0.200000"), and reject_step says where in the setup it happened.
ALTER TABLE cdrs
    ADD COLUMN reject_step TEXT,
    ADD COLUMN reject_detail TEXT;

CREATE INDEX cdrs_reject_step_idx ON cdrs (reject_step, start_time DESC) WHERE reject_step IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS cdrs_reject_step_idx;
ALTER TABLE cdrs DROP COLUMN IF EXISTS reject_step, DROP COLUMN IF EXISTS reject_detail;
