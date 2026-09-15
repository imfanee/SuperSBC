#!/usr/bin/env bash
# keepalived health check for an OpenSBC node: the local API must report
# ready (Postgres, Redis, ESL and both Sofia profiles) or the node loses
# priority and the VIP moves. Install as /usr/local/bin/check_sbc.sh.
set -u
PORT=${SBC_ADMIN_PORT:-28080}
out=$(curl -sf --max-time 2 "http://127.0.0.1:${PORT}/readyz") || exit 1
case "$out" in *'"ready":true'*) exit 0 ;; esac
exit 1
