#!/usr/bin/env bash
# Load test: sipp places calls from the "loadtest" customer through the SBC
# to carrier-answer at a fixed CPS for a fixed time (Section 12, M6).
#
#   tests/load/run.sh [cps] [seconds] [talk_ms]
#
# Requires: make up, docker compose --profile e2e up -d, make seed.
# Results: tests/e2e/out/load-<stamp>.{csv,msg} plus a summary on stdout.
set -euo pipefail
CPS=${1:-50}
SECS=${2:-600}
TALK=${3:-3000}
cd "$(dirname "$0")/../.."
stamp=$(date -u +%Y%m%d-%H%M%S)
calls=$((CPS * SECS))
mkdir -p tests/e2e/out
sed "s/__TALK__/$TALK/g" tests/e2e/scenarios/uac_call.xml.tmpl > tests/e2e/out/uac_load.xml
before=$(docker compose exec -T postgres psql -U supersbc -d supersbc -tAc "SELECT count(*) FROM cdrs")
start=$(date -u +%FT%TZ)
echo "load test: $CPS cps for $SECS s ($calls calls, talk ${TALK} ms), started $start"
set +e
docker compose --profile e2e-clients run --rm -T customer-loadtest \
  -sf /out/uac_load.xml -i 172.28.0.110 -p 5060 -mi 172.28.0.110 -mp 7000 \
  -s 442071234567 -r "$CPS" -rp 1000 -l 5000 -m "$calls" -nostdin \
  -cid_str "load-%u-%p-$stamp@%s" -trace_stat -stf "/out/load-$stamp.csv" -fd 10 \
  -trace_err -error_file "/out/load-$stamp.err" -timeout $((SECS + 120))s 172.28.0.10:5060 > "tests/e2e/out/load-$stamp.log" 2>&1
rc=$?
set -e
end=$(date -u +%FT%TZ)
echo "sipp exit code $rc, finished $end"
sleep 5
docker compose exec -T postgres psql -U supersbc -d supersbc -c "
  SELECT disposition, count(*) AS calls, round(avg(pdd_ms)) AS pdd_avg_ms, percentile_cont(0.95) WITHIN GROUP (ORDER BY pdd_ms) AS pdd_p95_ms,
         round(avg(billsec),2) AS acd, sum(sell_price) AS revenue, sum(cost) AS cost
  FROM cdrs WHERE start_time >= '$start' AND start_time <= '$end' GROUP BY 1 ORDER BY 2 DESC"
docker compose exec -T postgres psql -U supersbc -d supersbc -c "
  SELECT count(*) AS cdrs_written, count(*) FILTER (WHERE billed_at IS NULL) AS unbilled, max(end_time) - min(start_time) AS span
  FROM cdrs WHERE start_time >= '$start' AND start_time <= '$end'"
docker compose exec -T api /app/sbc-api reconcile 2>/dev/null | tail -1
tail -3 "tests/e2e/out/load-$stamp.csv" | cut -d';' -f1-12
