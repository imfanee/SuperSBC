#!/bin/sh
# Re-posts CDRs that mod_json_cdr spooled to disk because sbc-api was
# unreachable. Run inside the freeswitch container:
#   docker compose exec freeswitch sh /etc/freeswitch/replay_json_cdr.sh
# Billing is idempotent, so replaying an already billed call is harmless.
set -u
DIR=${1:-/var/log/freeswitch/json_cdr_failed}
URL=${SBC_API_INTERNAL_URL:-http://api:8081}/internal/v1/cdr
ok=0; fail=0
for f in "$DIR"/*.cdr.json; do
  [ -f "$f" ] || continue
  if wget -q -O /dev/null --header="Content-Type: application/json" --header="X-SBC-Secret: ${SBC_INTERNAL_SECRET}" --post-file="$f" "$URL"; then
    rm -f "$f"; ok=$((ok+1))
  else
    fail=$((fail+1))
  fi
done
echo "replayed $ok, failed $fail"
