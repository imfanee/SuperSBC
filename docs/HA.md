# High availability

Two SBC boxes, one virtual address, no shared call state. This document says exactly what survives a failure and what does not, and how to set it up with the pieces in `deploy/ha`.

## What the design gives you

| Failure | Effect | Recovery |
|---------|--------|----------|
| sbc-api crashes on a node | FreeSWITCH on that node rejects new calls with `503 SBC internal error` (fail closed); calls in progress continue and are billed when the API is back (mod_json_cdr spools, `make replay-cdrs`) | compose restarts the container in seconds |
| FreeSWITCH crashes or the box dies | calls in progress on that node drop; the VIP moves to the other node within about 3 seconds (keepalived `fall 3`, `interval 2` less the advert interval); new calls land on the survivor | reservations of the dead node are released by its own reconciler when it returns, or by any node after `max_call_duration + orphan_timeout` (D-68) |
| Postgres primary dies | no new calls anywhere (money cannot be reserved); calls in progress continue and their CDRs are spooled | promote the replica, repoint `SBC_DATABASE_URL` (multi-host URL, see below) |
| Redis master dies | Sentinel elects the replica within `down-after-milliseconds` (5 s); admission counters may be a few seconds stale, the reconciler resyncs them (D-31) | automatic |

What it does not give you: seamless failover of calls in progress. FreeSWITCH keeps call state in memory; replicating it needs the commercial FreeSWITCH HA extensions or a stateless SIP proxy layer in front (Kamailio with dispatcher), which is out of scope. A wholesale customer sees a failover as a burst of dropped calls, not as an outage.

## Node layout

Each box runs the live stack (`deploy/live`) with its own FreeSWITCH, sbc-api, nginx and a local Postgres and Redis role:

* **Box A**: Postgres primary, Redis master, sentinel, keepalived MASTER (priority 150).
* **Box B**: Postgres streaming replica, Redis replica, sentinel, keepalived BACKUP (priority 100).
* A third sentinel anywhere else gives the quorum of 2 a tie breaker.

Both sbc-api instances talk to the same primary Postgres and the same Redis master, so a customer's concurrent call and CPS limits, carrier capacities, bans and caches are cluster wide. The API is stateless; every node renders its own FreeSWITCH gateways and ACLs from the shared database and listens to the same Redis change notifications.

## Setting it up

1. **VIP.** Install keepalived on both boxes with `deploy/ha/keepalived/keepalived.conf.example` (`state BACKUP`, `priority 100` and swapped unicast addresses on box B), `check_sbc.sh` and `sbc_notify.sh` in `/usr/local/bin`. Apply `deploy/ha/sysctl-ha.conf` (`net.ipv4.ip_nonlocal_bind=1`) so FreeSWITCH can bind the VIP on the backup too.
2. **FreeSWITCH on the VIP.** In `.env.live` on both boxes: `SBC_FS_NODE_IP=<VIP>`, `SBC_FS_EXT_IP=<VIP>`, distinct `SBC_NODE_NAME` values (`live-a`, `live-b`), and `SBC_FS_ESL_LISTEN_IP` at the local docker bridge address as before. Carriers see one address in From, Contact and SDP whichever box is active; RTP follows the VIP.
3. **Postgres.** On box A run `REPLICATION_PASSWORD=... REPLICA_IP=<box B> deploy/ha/postgres/enable-replication.sh`, publish 5432 to box B only (firewall plus a port mapping on the live compose file), then on box B `PG_PRIMARY_HOST=<box A> PG_PRIMARY_PORT=25432 REPLICATION_PASSWORD=... docker compose -f deploy/ha/docker-compose.pg-replica.yml --env-file .env.live up -d`. Set `SBC_DATABASE_URL=postgres://supersbc:<pw>@<box A>:25432,<box B>:25432/supersbc?target_session_attrs=read-write` on both boxes: pgx tries the hosts in order and only accepts the one that is writable, so after a promotion (`docker compose -f deploy/ha/docker-compose.pg-replica.yml exec pg-replica pg_ctl promote`) a restart of sbc-api is enough. Automatic promotion is the job of Patroni or a managed Postgres; it is not bundled because a wrong automatic promotion of a money database is worse than five minutes of manual failover.
4. **Redis.** `deploy/ha/docker-compose.redis-ha.yml` on both boxes (`REDIS_REPLICAOF="<box A> 6379"` on box B) with `deploy/ha/redis/sentinel.conf.example` as `deploy/ha/redis/sentinel.conf`. Set `SBC_REDIS_SENTINEL_ADDRS=<box A>:26379,<box B>:26379,<third>:26379` and `SBC_REDIS_SENTINEL_MASTER=sbcmaster` on both boxes; `SBC_REDIS_URL` then only supplies the password and database number.
5. **Firewall.** Open VRRP (protocol 112) between the boxes, Postgres 25432 and Redis 6379/26379 between the boxes only, and keep everything else as in `deploy/live/nftables.conf`.
6. **Verify.** `curl -s http://127.0.0.1:28080/readyz` on both boxes, `ip addr` shows the VIP on the master only, then `systemctl stop keepalived` on box A during a test call: the call drops, the next call succeeds on box B within seconds, and `make live-reconcile` on box A after restart releases the orphaned reservation.

## Behaviour details worth knowing

* Reservations carry the node name (`active_calls.node`). The sweep that asks FreeSWITCH "is this channel still there" only runs against the node's own reservations, so a healthy node never releases the money of a call that is alive on the other node (D-68). Reservations past `max_call_duration + orphan_timeout` are released by any node because FreeSWITCH would have hung the call up by then.
* Periodic workers (roll-ups, invoices, ban expiry, gateway pings) run on every node and are idempotent; the unique constraints in the database make duplicates harmless.
* Bans are cluster wide (database) and each node renders them into its own ACL and firewall set.
* `sbc_node` in every CDR and `node` in every log line tell you which box handled a call.
