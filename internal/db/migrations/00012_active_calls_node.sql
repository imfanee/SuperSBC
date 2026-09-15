-- +goose Up
-- Multi node operation (D-68): the reconciliation sweep of a node must only
-- consult FreeSWITCH about the reservations that node made.
ALTER TABLE active_calls ADD COLUMN node text NOT NULL DEFAULT '';
CREATE INDEX active_calls_node_idx ON active_calls (node, started_at);

-- +goose Down
ALTER TABLE active_calls DROP COLUMN node;
