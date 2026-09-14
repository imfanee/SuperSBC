-- +goose Up
-- +goose StatementBegin
-- "an IP identifies exactly one customer": the original UNIQUE treated NULL
-- ports as distinct, so two customers could claim the same address.
DELETE FROM customer_ips a USING customer_ips b
 WHERE a.id < b.id AND a.ip_cidr = b.ip_cidr AND a.port IS NOT DISTINCT FROM b.port AND a.transport = b.transport;
ALTER TABLE customer_ips DROP CONSTRAINT IF EXISTS customer_ips_ip_cidr_port_transport_key;
ALTER TABLE customer_ips ADD CONSTRAINT customer_ips_ip_cidr_port_transport_key UNIQUE NULLS NOT DISTINCT (ip_cidr, port, transport);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE customer_ips DROP CONSTRAINT IF EXISTS customer_ips_ip_cidr_port_transport_key;
ALTER TABLE customer_ips ADD CONSTRAINT customer_ips_ip_cidr_port_transport_key UNIQUE (ip_cidr, port, transport);
-- +goose StatementEnd
