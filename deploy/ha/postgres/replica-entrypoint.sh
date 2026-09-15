#!/usr/bin/env bash
# Entrypoint of the streaming replica container: takes a base backup from the
# primary on first start (pg_basebackup -R writes standby.signal and the
# primary_conninfo), then runs Postgres as a hot standby.
set -euo pipefail
if [ ! -s "$PGDATA/PG_VERSION" ]; then
  echo "taking base backup from ${PRIMARY_HOST:?}:${PRIMARY_PORT:-5432}"
  until PGPASSWORD="${REPLICATION_PASSWORD:?}" pg_basebackup -h "$PRIMARY_HOST" -p "${PRIMARY_PORT:-5432}" -U replicator -D "$PGDATA" -Fp -Xs -R -P; do
    echo "primary not ready, retrying in 5s"; sleep 5
  done
  chown -R postgres:postgres "$PGDATA"; chmod 700 "$PGDATA"
fi
exec docker-entrypoint.sh postgres
