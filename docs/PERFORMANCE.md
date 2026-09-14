# Performance

Load test results and what they taught. Script: `tests/load/run.sh [cps] [seconds] [talk_ms]` (sipp from the `loadtest` customer to `carrier-answer` through the full pipeline: authorise, rate, reserve, route, bridge, bill).

## Test host

* 4 vCPU `QEMU Virtual CPU version 2.5+` (no AVX, no AES-NI), 3.8 GiB RAM, single virtual disk. A small KVM guest, roughly a quarter of a modern 8-core server.
* Everything on one host in Docker: FreeSWITCH 1.10.12, sbc-api, PostgreSQL 16, Redis 7, the sipp mock carrier and the sipp load generator (which itself uses CPU).
* No RTP is sent by sipp; FreeSWITCH still allocates RTP sockets and timers for every leg, so the signalling and billing path is fully exercised while media relay CPU is not.

## Results

| Run | Offered | CDRs | Answered | Failed | PDD avg | PDD p95 | Billed | Ledger |
|-----|--------:|-----:|---------:|-------:|--------:|--------:|-------:|--------|
| 20 CPS, 30 s, 2 s talk | 600 | 600 | 600 (100 %) | 0 | 1 ms | 19 ms | 100 % | reconciles |
| 25 CPS, 180 s, 3 s talk | 4500 | 4500 | 4500 (100 %) | 0 | 10 ms | 39 ms | 100 % | reconciles |
| 50 CPS, 600 s, 3 s talk | 30000 | 24530 | 21024 (70 %) | 3506 | 2352 ms | 4221 ms | 100 % of CDRs | reconciles |

At 50 CPS this host is CPU saturated (load average above 80 on 4 vCPUs, FreeSWITCH at 230 % CPU, the load generator and mock carrier competing for the same cores). Calls queue, PDD climbs into seconds, concurrent calls pile up to 470 instead of the expected 150 and FreeSWITCH hits its `max-sessions` limit of 1000 (two sessions per call), refusing new INVITEs with `503 Service Unavailable`. About 5500 INVITEs were rejected before a session existed and therefore have no CDR (they are visible in the sipp statistics only). Every call that did reach the pipeline was billed exactly once and the ledger reconciles.

The honest conclusion: **25 CPS with 100 % success is the sustainable rate of this 4 vCPU virtual machine**; 50 CPS needs a host with more cores or a dedicated FreeSWITCH host. The control plane is not the bottleneck: at 25 CPS the setup call from Lua to the API takes 10 ms on average and 39 ms at p95, and even in the saturated 50 CPS run 87 % of setup calls completed under 50 ms (the tail was CPU starvation, not database contention).

## Sizing guidance

* FreeSWITCH: budget one modern core per 25 to 40 CPS of signalling with media relay (no transcoding); transcoding (G.711 to Opus) costs roughly one core per 30 to 40 concurrent calls.
* sbc-api: one call is about 15 short SQL statements (setup transaction, attempt updates, billing transaction). Postgres on an SSD sustains several hundred CPS per core; the API itself used 0.5 cores at 50 CPS.
* Postgres: `synchronous_commit` stays `on` (money); the commit latency of the disk bounds the per-customer account lock time. On a slow virtual disk consider a separate volume for `pg_wal`.
* Redis: negligible (admission counters and caches; 7 % of a core at 50 CPS).
* File descriptors: four sockets per call in FreeSWITCH; `ulimits.nofile` is set to 65536 in compose.
* `SBC_FS_MAX_SESSIONS` is two per concurrent call; `SBC_FS_SESSIONS_PER_SECOND` is two per CPS.

## Defects found by the load test (fixed)

1. **Pool deadlock in billing.** The billing transaction loaded the rate rows and the carrier account through the pool while already holding a pool connection inside the transaction. Under load all 20 connections were held by transactions waiting for a 21st, and requests timed out. Fixed by running every read inside the transaction (`RateByIDQ`, `AccountByOwnerQ`) and raising the pool to 50.
2. **Long account lock windows.** The customer account row was locked at the start of both the reservation and the billing transaction and held while unrelated rows were written. Reordered so the per-call rows (CDR, active_calls) are written first and the account lock is taken last, immediately before commit.
3. **Aborted billing on client disconnect.** `mod_json_cdr` gives up after 10 s and the cancelled request context aborted the billing transaction. Billing now runs on a detached context with its own 30 s timeout (the ESL trigger and the reconciler remain as backups; billing is idempotent).
4. **File descriptor exhaustion in FreeSWITCH.** The container default of 1024 descriptors exhausted at about 150 concurrent calls: RTP socket errors, `488 Not Acceptable Here`, and finally the ESL listener died. Fixed with `ulimits.nofile: 65536`.
5. **Orphan sweep only after four hours.** After an API restart lost hangup events, reservations waited for `max_call_duration + orphan_timeout`. The reconciler now checks every reservation older than `orphan_timeout` against `show channels` whenever FreeSWITCH is reachable and releases the ones whose channel is gone (25 of them within a minute in the test).
6. **Spooled CDRs.** 3032 CDRs were spooled by `mod_json_cdr` while the API was restarting; `make replay-cdrs` re-posts them (idempotent), which the load test confirmed (`replayed 3032, failed 0`).

## Reproducing

```bash
make up && docker compose --profile e2e up -d && make seed
tests/load/run.sh 25 180 3000          # sustainable on a 4 vCPU VM
tests/load/run.sh 50 600 3000          # the specification's target; needs more cores
```

The script prints the disposition breakdown, PDD statistics, the number of unbilled CDRs (must be 0) and the reconcile result, and leaves the sipp statistics in `tests/e2e/out/load-<stamp>.csv`.
