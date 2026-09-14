-- +goose Up
-- +goose StatementBegin
-- Rejected calls have no customer or carrier: NULL keys must be allowed and
-- still be unique per hour and disposition.
ALTER TABLE cdr_hourly_stats DROP CONSTRAINT cdr_hourly_stats_pkey;
ALTER TABLE cdr_hourly_stats ALTER COLUMN customer_id DROP NOT NULL;
ALTER TABLE cdr_hourly_stats ALTER COLUMN carrier_id DROP NOT NULL;
CREATE UNIQUE INDEX cdr_hourly_stats_key ON cdr_hourly_stats (hour, customer_id, carrier_id, disposition) NULLS NOT DISTINCT;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS cdr_hourly_stats_key;
DELETE FROM cdr_hourly_stats WHERE customer_id IS NULL OR carrier_id IS NULL;
ALTER TABLE cdr_hourly_stats ADD PRIMARY KEY (hour, customer_id, carrier_id, disposition);
-- +goose StatementEnd
