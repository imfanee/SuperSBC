-- +goose Up
-- The customer leg SIP Call-ID, to correlate the CDR with captured SIP (D-71).
ALTER TABLE cdrs ADD COLUMN sip_call_id text;
CREATE INDEX cdrs_sip_call_id_idx ON cdrs (sip_call_id) WHERE sip_call_id IS NOT NULL;

-- +goose Down
ALTER TABLE cdrs DROP COLUMN sip_call_id;
