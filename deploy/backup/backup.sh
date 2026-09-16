#!/bin/sh
# Nightly pg_dump of the SuperSBC database. Runs inside the "backup" compose
# service (profile: backup). Keeps BACKUP_KEEP_DAYS days (default 14).
set -eu
: "${PGHOST:=postgres}" "${PGUSER:=supersbc}" "${PGDATABASE:=supersbc}" "${BACKUP_DIR:=/backups}" "${BACKUP_KEEP_DAYS:=14}"
mkdir -p "$BACKUP_DIR"
stamp=$(date -u +%Y%m%d-%H%M%S)
file="$BACKUP_DIR/supersbc-$stamp.dump"
pg_dump -Fc -h "$PGHOST" -U "$PGUSER" "$PGDATABASE" > "$file"
echo "backup written: $file ($(du -h "$file" | cut -f1))"
find "$BACKUP_DIR" -name 'supersbc-*.dump' -mtime +"$BACKUP_KEEP_DAYS" -delete
