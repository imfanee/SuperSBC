-- +goose Up
-- TLS client certificate verification (D-67): the certificate subject (CN or
-- a SAN) a customer presents on its TLS connections; rendered into the
-- ingress profile's tls-verify-in-subjects list.
ALTER TABLE customers ADD COLUMN tls_subject text NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE customers DROP COLUMN tls_subject;
