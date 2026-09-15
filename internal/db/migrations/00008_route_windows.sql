-- +goose Up
-- Time-of-day and day-of-week routing windows per route carrier (roadmap item).
-- Syntax: "mon-fri 08:00-18:00 Europe/London; sat,sun 00:00-24:00"; empty = always.
ALTER TABLE route_carriers ADD COLUMN "window" TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE route_carriers DROP COLUMN "window";
