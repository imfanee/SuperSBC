-- +goose Up
-- Routing modes per route group (D-73): lossless, quality based and
-- percentage based routing next to the existing least cost mode.
ALTER TABLE route_groups
    ADD COLUMN lossless_mode boolean NOT NULL DEFAULT false,
    ADD COLUMN quality_mode  boolean NOT NULL DEFAULT false,
    ADD COLUMN percent_mode  boolean NOT NULL DEFAULT false,
    ADD CONSTRAINT route_groups_percent_exclusive CHECK (NOT (percent_mode AND (lcr_mode OR lossless_mode OR quality_mode)));
-- A zero weight is a valid share in percent mode (failover only).
ALTER TABLE route_carriers DROP CONSTRAINT route_carriers_weight_check;
ALTER TABLE route_carriers ADD CONSTRAINT route_carriers_weight_check CHECK (weight >= 0);

-- +goose Down
ALTER TABLE route_carriers DROP CONSTRAINT route_carriers_weight_check;
ALTER TABLE route_carriers ADD CONSTRAINT route_carriers_weight_check CHECK (weight > 0);
ALTER TABLE route_groups DROP CONSTRAINT route_groups_percent_exclusive,
    DROP COLUMN lossless_mode, DROP COLUMN quality_mode, DROP COLUMN percent_mode;
