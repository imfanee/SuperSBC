#!/usr/bin/env bash
# Prepares the live environment once: .env.live with random secrets, a
# self-signed TLS certificate (replace with a real one in deploy/live/tls),
# and prints the bootstrap admin password exactly once.
set -euo pipefail
cd "$(dirname "$0")/../.."
NODE_IP=${1:-$(ip -4 route get 1.1.1.1 2>/dev/null | awk '{for(i=1;i<=NF;i++) if($i=="src") print $(i+1)}' | head -1)}
if [ -z "$NODE_IP" ]; then echo "usage: deploy/live/init.sh <public ip>"; exit 1; fi
if [ -f .env.live ]; then
  echo ".env.live already exists; not overwriting (delete it to start over)"
else
  rnd() { head -c 48 /dev/urandom | base64 | tr -dc 'A-Za-z0-9' | head -c "$1"; }
  ADMIN_PW=$(rnd 20)
  cat > .env.live <<ENV
# Live environment (generated $(date -u +%FT%TZ) by deploy/live/init.sh). Keep this file private.
SBC_ENV=prod
SBC_NODE_NAME=live-1
SBC_LOG_LEVEL=info

SBC_INTERNAL_SECRET=$(rnd 40)
SBC_ESL_PASSWORD=$(rnd 32)
SBC_JWT_SECRET=$(rnd 64)
POSTGRES_PASSWORD=$(rnd 32)
GRAFANA_ADMIN_PASSWORD=$(rnd 20)

SBC_BOOTSTRAP_ADMIN_EMAIL=admin@example.com
SBC_BOOTSTRAP_ADMIN_PASSWORD=$ADMIN_PW

SBC_ADMIN_LISTEN=:8080
SBC_INTERNAL_LISTEN=:8081
SBC_DATABASE_URL=postgres://opensbc:PGPW@postgres:5432/opensbc?sslmode=disable
SBC_AUTO_MIGRATE=true
SBC_REDIS_URL=redis://redis:6379/0
SBC_ESL_HOST=172.29.0.1
SBC_ESL_PORT=8021
SBC_FS_CONFIG_DIR=/fsconfig
SBC_FS_LOG_FILE=/var/log/freeswitch/freeswitch.log
SBC_AUTH_COOKIE_SECURE=true
SBC_ACL_MODE=strict

SBC_BILLING_RESERVE_MINUTES=5
SBC_BILLING_MAX_CALL_DURATION=4h
SBC_BILLING_ORPHAN_TIMEOUT=10m
SBC_BILLING_LOW_BALANCE_THRESHOLD=10.00
SBC_BILLING_CURRENCY=USD
SBC_ROUTING_ORIGINATE_TIMEOUT=60
SBC_ROUTING_PROGRESS_TIMEOUT=8
SBC_ROUTING_BLOCK_NEGATIVE_MARGIN=false
SBC_ROUTING_GLOBAL_MAX_CPS=20
SBC_ROUTING_GLOBAL_MAX_CHANNELS=200
SBC_MEDIA_TIMEOUT_SEC=300
SBC_MEDIA_HOLD_TIMEOUT_SEC=1800
SBC_FAILOVER_RULES_FILE=/app/failover.yaml
SBC_FAILOVER_MIN_RING_SECONDS_FOR_NO_ANSWER=10
SBC_FAILOVER_BREAKER_CONSECUTIVE_FAULTS=20
SBC_FAILOVER_BREAKER_ASR_THRESHOLD_PERCENT=10
SBC_FAILOVER_BREAKER_ASR_MIN_SAMPLES=50
SBC_FAILOVER_BREAKER_DEGRADED_SECONDS=60
SBC_FAILOVER_GATEWAY_PING_INTERVAL_SECONDS=10
SBC_BAN_THRESHOLD=20
SBC_BAN_WINDOW=5m
SBC_BAN_DURATION=1h

# FreeSWITCH on the host network
SBC_FS_NODE_IP=$NODE_IP
SBC_FS_EXT_IP=$NODE_IP
SBC_FS_ESL_LISTEN_IP=172.29.0.1
SBC_FS_INGRESS_PORT=5060
SBC_FS_EGRESS_PORT=5080
SBC_FS_TLS=true
SBC_FS_INGRESS_TLS_PORT=5061
SBC_FS_EGRESS_TLS_PORT=5081
SBC_FS_TLS_VERIFY_POLICY=none
SBC_FS_TLS_VERSION=tlsv1.2,tlsv1.3
SBC_FS_INGRESS_100REL=false
SBC_FS_EGRESS_100REL=false
# HEP/HOMER capture (D-65): empty = off. Example: udp:homer:9060;hep=3;capture_id=100
SBC_FS_HEP_SERVER=
SBC_FS_RTP_START=16384
SBC_FS_RTP_END=32768
SBC_FS_MAX_SESSIONS=400
SBC_FS_SESSIONS_PER_SECOND=40
SBC_FS_INGRESS_CODECS=PCMA,PCMU,OPUS,G722
SBC_FS_EGRESS_CODECS=PCMA,PCMU,OPUS,G722
SBC_FS_USER_AGENT=OpenSBC
SBC_FS_LOG_LEVEL=info
SBC_API_INTERNAL_URL=http://127.0.0.1:8081

# Host ports
SBC_HTTP_PORT=8080
SBC_HTTPS_PORT=8443
SBC_INTERNAL_PORT=8081
SBC_ADMIN_PORT=28080
SBC_PG_PORT=25432
SBC_REDIS_PORT=26379
SBC_PROMETHEUS_PORT=29090
BACKUP_KEEP_DAYS=30
ENV
  sed -i "s|PGPW|$(grep '^POSTGRES_PASSWORD=' .env.live | cut -d= -f2)|" .env.live
  chmod 600 .env.live
  echo "wrote .env.live"
  echo "==> bootstrap admin: admin@example.com / $ADMIN_PW   (shown once; change it after first login)"
fi
mkdir -p deploy/live/tls backups/live
if [ ! -f deploy/live/tls/server.crt ]; then
  openssl req -x509 -newkey rsa:2048 -nodes -days 825 -keyout deploy/live/tls/server.key -out deploy/live/tls/server.crt \
    -subj "/CN=$NODE_IP/O=OpenSBC" -addext "subjectAltName=IP:$NODE_IP" >/dev/null 2>&1
  chmod 600 deploy/live/tls/server.key
  echo "wrote self-signed certificate deploy/live/tls/server.crt for $NODE_IP (replace with a real one when you have a hostname)"
fi
# FreeSWITCH TLS files (D-53): agent.pem = key plus certificate, cafile.pem = the chain to trust.
# Regenerated from server.crt/server.key whenever they are newer, so replacing the certificate is enough.
if [ ! -f deploy/live/tls/agent.pem ] || [ deploy/live/tls/server.crt -nt deploy/live/tls/agent.pem ]; then
  cat deploy/live/tls/server.key deploy/live/tls/server.crt > deploy/live/tls/agent.pem
  cp deploy/live/tls/server.crt deploy/live/tls/cafile.pem
  chmod 600 deploy/live/tls/agent.pem
  echo "wrote deploy/live/tls/agent.pem and cafile.pem for FreeSWITCH"
fi
echo "next: make live-up"
