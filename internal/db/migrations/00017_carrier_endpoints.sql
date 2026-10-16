-- +goose Up
-- Several signalling endpoints per carrier, load balanced with failover (D-75).
-- Each endpoint becomes its own FreeSWITCH gateway so OPTIONS pings and dial
-- attempts are per endpoint. Existing carriers keep their single endpoint.
CREATE TYPE endpoint_lb_kind AS ENUM ('failover', 'round_robin', 'weighted');

CREATE TABLE carrier_endpoints (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    carrier_id  uuid NOT NULL REFERENCES carriers(id) ON DELETE CASCADE,
    seq         int NOT NULL,
    host        text NOT NULL,
    port        int NOT NULL DEFAULT 5060 CHECK (port > 0 AND port < 65536),
    transport   transport_kind NOT NULL DEFAULT 'udp',
    priority    int NOT NULL DEFAULT 1 CHECK (priority > 0),
    weight      int NOT NULL DEFAULT 100 CHECK (weight >= 0),
    enabled     boolean NOT NULL DEFAULT true,
    notes       text NOT NULL DEFAULT '',
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now(),
    UNIQUE (carrier_id, seq),
    UNIQUE (carrier_id, host, port, transport)
);
CREATE INDEX carrier_endpoints_carrier_idx ON carrier_endpoints (carrier_id, priority, seq);
CREATE TRIGGER carrier_endpoints_updated BEFORE UPDATE ON carrier_endpoints
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

ALTER TABLE carriers
    ADD COLUMN endpoint_lb endpoint_lb_kind NOT NULL DEFAULT 'failover',
    -- How long a dead endpoint stays out of rotation before it is tried again
    -- (used when OPTIONS ping is off: deadness is learned from call attempts).
    ADD COLUMN endpoint_retry_seconds int NOT NULL DEFAULT 300 CHECK (endpoint_retry_seconds > 0),
    -- Consecutive failed attempts that mark an endpoint dead.
    ADD COLUMN endpoint_fail_threshold int NOT NULL DEFAULT 2 CHECK (endpoint_fail_threshold > 0),
    -- Learn deadness from calls. Off by default: it cannot tell a carrier that
    -- answers "503 Service Unavailable" from an address that answers nothing
    -- (FreeSWITCH reports both the same way), so parking is opt in per carrier.
    ADD COLUMN endpoint_park_on_failure boolean NOT NULL DEFAULT false;

-- Every existing carrier becomes a one endpoint carrier.
INSERT INTO carrier_endpoints (carrier_id, seq, host, port, transport)
SELECT id, 1, gateway_host, gateway_port, transport FROM carriers WHERE deleted_at IS NULL;

-- The attempt record gains the endpoint it used.
ALTER TABLE cdrs ADD COLUMN IF NOT EXISTS endpoint_host text;

-- +goose Down
ALTER TABLE cdrs DROP COLUMN IF EXISTS endpoint_host;
ALTER TABLE carriers DROP COLUMN endpoint_lb, DROP COLUMN endpoint_retry_seconds, DROP COLUMN endpoint_fail_threshold, DROP COLUMN endpoint_park_on_failure;
DROP TABLE carrier_endpoints;
DROP TYPE endpoint_lb_kind;
