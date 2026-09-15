#!/usr/bin/env bash
# Turns the running live Postgres into a streaming primary (D-68):
#   REPLICATION_PASSWORD=... REPLICA_IP=198.51.100.12 deploy/ha/postgres/enable-replication.sh
# Idempotent. Postgres restarts once for wal_level (a few seconds; in-flight
# API requests retry through the pool).
set -euo pipefail
cd "$(dirname "$0")/../../.."
LIVE="docker compose -p opensbc-live --env-file .env.live -f deploy/live/docker-compose.live.yml"
: "${REPLICATION_PASSWORD:?set REPLICATION_PASSWORD}"
: "${REPLICA_IP:?set REPLICA_IP (address of the replica box)}"
$LIVE exec -T postgres psql -U opensbc -d postgres -v ON_ERROR_STOP=1 <<SQL
DO \$\$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'replicator') THEN
    CREATE ROLE replicator WITH REPLICATION LOGIN PASSWORD '${REPLICATION_PASSWORD}';
  ELSE
    ALTER ROLE replicator WITH PASSWORD '${REPLICATION_PASSWORD}';
  END IF;
END \$\$;
ALTER SYSTEM SET wal_level = 'replica';
ALTER SYSTEM SET max_wal_senders = 10;
ALTER SYSTEM SET wal_keep_size = '1GB';
SQL
$LIVE exec -T postgres sh -c "grep -q 'replicator ${REPLICA_IP}/32' \$PGDATA/pg_hba.conf || echo 'host replication replicator ${REPLICA_IP}/32 scram-sha-256' >> \$PGDATA/pg_hba.conf"
$LIVE restart postgres
echo "primary ready; the replica box can run: PG_PRIMARY_HOST=<this box> REPLICATION_PASSWORD=... docker compose -f deploy/ha/docker-compose.pg-replica.yml --env-file .env.live up -d"
echo "note: publish postgres to the replica only (deploy/live/docker-compose.live.yml binds 127.0.0.1; add a firewall rule and a second port mapping for ${REPLICA_IP})"
