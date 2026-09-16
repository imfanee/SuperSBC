#!/usr/bin/env bash
# Generates a self-signed SIP TLS identity for FreeSWITCH: agent.pem (key +
# certificate) and cafile.pem. Replace with a CA-issued pair for production
# (carriers usually verify the certificate).
#   freeswitch/tls/gen.sh <common name or ip> [out dir]
set -euo pipefail
CN=${1:-localhost}
OUT=${2:-$(dirname "$0")}
mkdir -p "$OUT"
if [[ "$CN" =~ ^[0-9.]+$ ]]; then SAN="IP:$CN"; else SAN="DNS:$CN"; fi
openssl req -x509 -newkey rsa:2048 -nodes -days 825 -keyout "$OUT/key.pem" -out "$OUT/cert.pem" -subj "/CN=$CN/O=SuperSBC" -addext "subjectAltName=$SAN" >/dev/null 2>&1
cat "$OUT/key.pem" "$OUT/cert.pem" > "$OUT/agent.pem"
cp "$OUT/cert.pem" "$OUT/cafile.pem"
chmod 600 "$OUT/agent.pem" "$OUT/key.pem"
echo "wrote $OUT/agent.pem and $OUT/cafile.pem for $CN"
