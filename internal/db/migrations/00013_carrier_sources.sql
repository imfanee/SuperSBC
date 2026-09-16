-- +goose Up
-- Extra signalling source addresses of a carrier (clusters that send from
-- addresses other than the gateway host); allow-listed in the host firewall (D-69).
ALTER TABLE carriers ADD COLUMN signalling_sources text[] NOT NULL DEFAULT '{}';

-- +goose Down
ALTER TABLE carriers DROP COLUMN signalling_sources;
