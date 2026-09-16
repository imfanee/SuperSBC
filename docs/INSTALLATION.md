# SuperSBC installation guide

Step by step from an empty Linux box to a running SBC. Two set-ups are covered: the **development stack** (everything inside a private Docker network with mock customers and carriers; for learning, testing and development) and the **live stack** (FreeSWITCH on the host network with a public address; for real customers). Both can run on the same box, which is how the pilot deployment works.

Time needed: about 15 minutes for the development stack, an hour for a live box including TLS and the firewall.

## 1. Requirements

| | Minimum | Recommended for production |
|---|---|---|
| OS | Debian 12 or Ubuntu 22.04 (any systemd Linux with Docker works) | same |
| CPU / RAM | 2 vCPU, 4 GB (development) | 8 cores, 16 GB (about 50 CPS with media relay, see PERFORMANCE.md) |
| Disk | 20 GB | SSD, 100 GB (CDRs grow roughly 2 KB per call) |
| Software | Docker 26+ with Compose v2, GNU make, git, curl, openssl | plus nftables and systemd |
| Network (live) | one public IPv4 address; UDP/TCP 5060, TCP 5061, UDP 16384 to 32768 reachable from customers | TCP 5080/5081 reachable from carriers only |
| For development work | Go 1.26, Node 22, golangci-lint, luacheck (only if you change code) | |

Install Docker on Debian/Ubuntu:

```bash
curl -fsSL https://get.docker.com | sh
apt-get install -y make git curl openssl nftables
docker compose version     # must print v2.x
```

## 2. Get the code

```bash
git clone git@github.com:imfanee/SuperSBC.git /root/supersbc
cd /root/supersbc
```

The live systemd units and scripts assume `/root/supersbc`; if you clone elsewhere, edit `deploy/live/supersbc-fwsync.service` and the include path in `deploy/live/nftables.conf`.

## 3. Development stack (first call in 15 minutes)

Step 1. Create the environment file and set the three secrets:

```bash
cp .env.example .env
sed -i "s/^SBC_INTERNAL_SECRET=.*/SBC_INTERNAL_SECRET=$(openssl rand -hex 24)/; s/^SBC_ESL_PASSWORD=.*/SBC_ESL_PASSWORD=$(openssl rand -hex 16)/; s/^SBC_JWT_SECRET=.*/SBC_JWT_SECRET=$(openssl rand -hex 32)/" .env
grep -n "change-me" .env      # must print nothing
```

Set `SBC_BOOTSTRAP_ADMIN_PASSWORD` in `.env` to the password of the first admin user.

Step 2. Generate the SIP TLS certificate for the lab (self signed, FreeSWITCH's internal address):

```bash
freeswitch/tls/gen.sh 172.28.0.10
```

Step 3. Build and start:

```bash
make up          # builds sbc-api and the web UI, starts postgres, redis, sbc-api, freeswitch, web
make seed        # demo customers, carriers, rate decks and routes
```

`make up` waits until the API is healthy. Check:

```bash
curl -s http://127.0.0.1:18080/readyz | jq
```

Every check (`postgres`, `redis`, `esl`, `sofia.external-ingress`, `sofia.external-egress`) must be `ok`.

Step 4. Log in to the UI at http://127.0.0.1:13000 with `admin@example.com` and the bootstrap password. The API documentation is at http://127.0.0.1:18080/api/docs.

Step 5. Make a test call with the mock customer and carrier:

```bash
make e2e         # 42 sipp scenarios: authentication, billing, failover, TLS, SRTP, ...
```

or a single call by hand:

```bash
docker compose --profile e2e up -d
docker compose --profile e2e-clients run --rm customer-acme -sf /scenarios/uac_call.xml -s 442071234567 172.28.0.10:5060 -m 1 -nostdin
```

Then open CDRs in the UI: the call from customer `acme` to `447...` routed to `carrier-answer`, billed at the demo rates.

Optional:

```bash
docker compose --profile monitoring up -d   # Prometheus 127.0.0.1:19090, Grafana 127.0.0.1:13001 (admin/admin)
make homer-up                                # HOMER SIP capture UI 127.0.0.1:19080 (admin/sipcapture)
```

Stop with `make down`; wipe everything (including the database) with `make clean`.

## 4. Live stack (real customers)

The live stack is a second compose project (`supersbc-live`) with its own `.env.live`, volumes, bridge network (172.29.0.0/24) and ports. FreeSWITCH runs on the host network so SIP and RTP use the box's public address directly.

Step 1. Initialise (generates `.env.live` with random secrets, a self signed web certificate and the FreeSWITCH TLS files, and prints the bootstrap admin password once):

```bash
deploy/live/init.sh <public ip>
```

Save the printed password. Review `.env.live`: `SBC_FS_NODE_IP` (and `SBC_FS_EXT_IP` when the box is behind 1:1 NAT), `SBC_BILLING_CURRENCY`, `SBC_ROUTING_GLOBAL_MAX_CPS` / `_CHANNELS` (capacity caps), `SBC_INVOICE_OPERATOR_NAME` and `_ADDRESS` (printed on invoices).

Step 2. Start:

```bash
make live-up
curl -sk https://127.0.0.1:8443/readyz | jq
```

Step 3. Firewall. The ruleset in `deploy/live/nftables.conf` allows ssh, the web UI (8080 redirects to 8443), customer SIP (5060 UDP/TCP, 5061 TLS) from the `customers` set, RTP, and carrier SIP (5080/5081) from the `carriers` set; it drops everything else, rate limits SIP per source and drops the `banned` set. The three sets are filled automatically from the database by `sbc-fwsync` (customer addresses, carrier gateways and extra signalling sources, bans), so adding a customer address or a carrier in the UI opens the firewall for it within a couple of seconds. Apply and persist:

```bash
make live-firewall      # ruleset (sets start empty)
make live-fwsync        # builds and installs the sync service; fills the sets
nft list set inet sbc customers; nft list set inet sbc carriers
```

Step 4. Log in at `https://<public ip>:8443` (self signed certificate warning is expected until step 5), change the admin password (top right, Account), enable two factor authentication, create personal user accounts (System > Users) and stop using the bootstrap account.

Step 5. Real certificates. For the web UI put a CA issued `server.crt` and `server.key` into `deploy/live/tls/`, run `deploy/live/init.sh` again (it rebuilds `agent.pem`/`cafile.pem` for FreeSWITCH from them) and `make live-up`. SIP TLS on 5061/5081 uses the same certificate.

Step 6. Onboard the first customer and carrier: follow the [User guide](USER_GUIDE.md) sections "Carriers" and "Customers", then place a test call and check the CDR.

Step 7. Backups and monitoring:

```bash
make live-backup                    # immediate pg_dump into backups/live
$(LIVE) --profile backup up -d      # nightly dumps (see Makefile for the LIVE variable)
make live-monitoring                # Grafana at https://<ip>:8443/grafana/
```

Step 8 (optional). SIP capture with HOMER: `make homer-up`, set `SBC_FS_HEP_SERVER=udp:172.29.0.1:9060;hep=3;capture_id=2` in `.env.live`, `make live-up`.

## 5. Ports on a box that runs both stacks

| Service | Development | Live |
|---|---|---|
| SIP customers | 172.28.0.10:5060 / 5061 (inside Docker) | `<public ip>`:5060 / 5061 |
| SIP carriers | 172.28.0.10:5080 / 5081 | `<public ip>`:5080 / 5081 (allow-listed) |
| RTP | 16384 to 32768 inside Docker | `<public ip>`:16384 to 32768 |
| Web UI | 127.0.0.1:13000 | 8080 (redirect) and 8443 (HTTPS) |
| Admin API | 127.0.0.1:18080 | 127.0.0.1:28080 and through nginx at `/api/` |
| Postgres / Redis | 127.0.0.1:15432 / 16379 | 127.0.0.1:25432 / 26379 |
| Prometheus / Grafana | 127.0.0.1:19090 / 13001 | 127.0.0.1:29090 / `/grafana/` |
| HOMER | 127.0.0.1:19080 | same instance |

## 6. Upgrading

```bash
cd /root/supersbc
git pull
make up          # development: rebuild and restart, migrations run automatically
make live-up     # live: same for the live project
make live-reconcile
```

Migrations are embedded in the API and applied at start (`SBC_AUTO_MIGRATE=true`). Take `make live-backup` before an upgrade. FreeSWITCH restarts drop calls in progress; do live upgrades at a quiet time. Roll back a migration with `make rollback` (development) only when the release notes say the schema change is reversible.

## 7. Uninstalling

```bash
make down                 # development containers
make clean                # plus volumes and images
$(LIVE) down -v           # live containers and volumes (destroys the live database; take a backup first)
systemctl disable --now supersbc-fwsync nftables
```

## Next steps

* [User guide](USER_GUIDE.md): day to day work in the UI.
* [Administrator guide](ADMIN_GUIDE.md): configuration, security, operations.
* [Configuration reference](CONFIGURATION.md): every environment variable.
* [Troubleshooting](TROUBLESHOOTING.md): symptoms and fixes.
