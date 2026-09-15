#!/usr/bin/env bash
# Runs once on the primary's first start (mounted into /docker-entrypoint-initdb.d/):
# creates the replication role and allows the replica to stream WAL.
set -euo pipefail
psql -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d postgres <<SQL
CREATE ROLE replicator WITH REPLICATION LOGIN PASSWORD '${REPLICATION_PASSWORD:?set REPLICATION_PASSWORD}';
SQL
cat >> "$PGDATA/pg_hba.conf" <<HBA
host replication replicator ${REPLICA_CIDR:-0.0.0.0/0} scram-sha-256
HBA
cat >> "$PGDATA/postgresql.conf" <<CONF
wal_level = replica
max_wal_senders = 10
wal_keep_size = 1GB
synchronous_commit = on
CONF
