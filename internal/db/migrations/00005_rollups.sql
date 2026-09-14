-- +goose Up
-- +goose StatementBegin
-- Hourly roll-up of cdrs for fast dashboards (Section 10). Maintained by the
-- roll-up worker: the last few hours are recomputed every minute, older hours
-- only when a CDR in them was updated after the roll-up.
CREATE TABLE cdr_hourly_stats (
    hour           timestamptz NOT NULL,
    customer_id    uuid,
    carrier_id     uuid,
    disposition    disposition_kind NOT NULL,
    calls          bigint NOT NULL DEFAULT 0,
    billsec        bigint NOT NULL DEFAULT 0,
    sell_price     numeric(18,6) NOT NULL DEFAULT 0,
    cost           numeric(18,6) NOT NULL DEFAULT 0,
    pdd_ms_sum     bigint NOT NULL DEFAULT 0,
    pdd_count      bigint NOT NULL DEFAULT 0,
    short_calls    bigint NOT NULL DEFAULT 0,
    computed_at    timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (hour, customer_id, carrier_id, disposition)
);
CREATE INDEX cdr_hourly_stats_hour_idx ON cdr_hourly_stats (hour);
CREATE INDEX cdrs_updated_idx ON cdrs (updated_at);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS cdrs_updated_idx;
DROP TABLE IF EXISTS cdr_hourly_stats;
-- +goose StatementEnd
