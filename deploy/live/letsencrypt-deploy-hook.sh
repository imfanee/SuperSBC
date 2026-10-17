#!/bin/sh
# Copy a freshly issued certificate to where the live stack reads it and
# reload the two services that serve TLS (D-80). certbot runs this after
# every successful renewal; it is also safe to run by hand.
set -eu

DOMAIN="${SBC_TLS_DOMAIN:-sbc.softautonomi.com}"
SRC="/etc/letsencrypt/live/${DOMAIN}"
DST="${SBC_LIVE_DIR:-/root/supersbc/deploy/live}/tls"
PROJECT="${SBC_COMPOSE_PROJECT:-supersbc-live}"

[ -r "${SRC}/fullchain.pem" ] || { echo "no certificate at ${SRC}"; exit 1; }
install -d -m 750 "${DST}"

# nginx reads these two directly.
install -m 644 "${SRC}/fullchain.pem" "${DST}/fullchain.pem"
install -m 640 "${SRC}/privkey.pem" "${DST}/privkey.pem"

# FreeSWITCH wants the key and the chain in one file for SIP TLS and WSS.
umask 027
cat "${SRC}/privkey.pem" "${SRC}/fullchain.pem" > "${DST}/agent.pem.new"
mv "${DST}/agent.pem.new" "${DST}/agent.pem"
cp "${SRC}/chain.pem" "${DST}/cafile.pem"
cp "${SRC}/fullchain.pem" "${DST}/wss.pem"

# Reload what is running; a stopped stack simply picks the files up next time.
if command -v docker >/dev/null 2>&1; then
    docker exec "${PROJECT}-web-1" nginx -s reload 2>/dev/null || true
    docker exec "${PROJECT}-freeswitch-1" fs_cli -x "reloadxml" 2>/dev/null || true
    docker exec "${PROJECT}-freeswitch-1" fs_cli -x "sofia profile external-ingress restart" 2>/dev/null || true
fi
echo "certificate for ${DOMAIN} installed in ${DST}"
