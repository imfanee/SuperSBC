-- +goose Up
-- STIR/SHAKEN verification (D-64): per customer policy and per call result.
CREATE TYPE stir_kind AS ENUM ('ignore', 'verify', 'require');
ALTER TABLE customers ADD COLUMN stir_mode stir_kind NOT NULL DEFAULT 'ignore';
ALTER TABLE cdrs ADD COLUMN stir_status text, ADD COLUMN stir_attest char(1);

-- +goose Down
ALTER TABLE cdrs DROP COLUMN stir_status, DROP COLUMN stir_attest;
ALTER TABLE customers DROP COLUMN stir_mode;
DROP TYPE stir_kind;
