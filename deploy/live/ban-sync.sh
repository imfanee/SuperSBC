#!/usr/bin/env bash
# Mirrors the SBC ban list (written by sbc-api to deploy/live/state/banned_ips.txt,
# SBC_BAN_EXPORT_FILE) into the nftables set "inet sbc banned" so packets from
# banned addresses are dropped before they reach FreeSWITCH (D-66).
# Idempotent; run from the systemd timer (deploy/live/opensbc-ban-sync.timer),
# cron, or once with "make live-ban-sync".
set -euo pipefail
FILE=${1:-"$(dirname "$0")/state/banned_ips.txt"}
if ! nft list set inet sbc banned >/dev/null 2>&1; then
  echo "nftables set inet sbc banned missing: run make live-firewall first" >&2
  exit 1
fi
elements=""
if [ -f "$FILE" ]; then
  while read -r ip secs; do
    case "$ip" in ''|\#*) continue ;; esac
    if [ "${secs:-0}" -gt 0 ] 2>/dev/null; then
      elements="$elements${elements:+, }$ip timeout ${secs}s"
    else
      elements="$elements${elements:+, }$ip"
    fi
  done < "$FILE"
fi
nft flush set inet sbc banned
if [ -n "$elements" ]; then
  nft add element inet sbc banned "{ $elements }"
fi
n=0; [ -n "$elements" ] && n=$(printf '%s' "$elements" | tr ',' '\n' | grep -c .)
echo "ban-sync: $n entries applied from $FILE"
