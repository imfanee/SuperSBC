# Billing

Prepaid wholesale billing with a credit limit, a per-call reservation, a true-up on hangup and an append-only ledger. Money is `NUMERIC(18,6)` in Postgres and `shopspring/decimal` in Go. There are no floats anywhere on the money path.

## 1. Accounts and available balance

Every customer and every carrier has one row in `accounts`:

| Column | Meaning |
|--------|---------|
| `balance` | money actually on the account (customer: prepaid; carrier: deposit with the supplier, negative when we owe them) |
| `allowed_credit` | post-paid headroom; `0` for strict prepaid |
| `reserved` | the sum of reservations of calls in progress |

The one definition of what a customer may still spend:

```
available = balance + allowed_credit - reserved
```

It is implemented once in SQL (`account_available(accounts)`) and once in Go (`model.Account.Available`). Both are used by tests, and the reservation `UPDATE` inlines the same expression so the check and the mutation are one atomic statement.

## 2. Rating (`internal/rating`)

```
BilledSeconds(billsec, initial, subsequent, min_duration):
   billsec <= 0            -> 0
   billsec < min_duration  -> 0                         (grace)
   billsec <= initial      -> initial
   else                    -> initial + ceil((billsec - initial) / subsequent) * subsequent

Amount(billed_seconds, rate_per_min, connect_fee):
   billed_seconds == 0     -> 0                         (no connect fee on a zero-billed call)
   else                    -> round_half_up(connect_fee + rate_per_min * billed_seconds / 60, 6)
```

Examples (from the unit test table):

| increments | billsec | billed | at 0.02/min | with fee 0.01 |
|-----------:|--------:|-------:|------------:|--------------:|
| 60/60 | 1 | 60 | 0.020000 | 0.030000 |
| 60/60 | 61 | 120 | 0.040000 | 0.050000 |
| 30/6 | 31 | 36 | 0.012000 | 0.022000 |
| 1/1 | 1 | 1 | 0.000333 | 0.010333 |
| 6/6 | 59 | 60 | 0.020000 | 0.030000 |
| any, min_duration 3 | 2 | 0 | 0.000000 | 0.000000 |

Rounding is half-up at six decimal places, once, after the division. The UI rounds to four places for display only.

## 3. Reservation (Section 3 Steps 4 and 5)

At setup, after the selling rate is known:

```
reserve_amount = Amount(BilledSeconds(reserve_minutes * 60, rate...), rate)     (D-18)
```

With `SBC_BILLING_RESERVE_MINUTES=5` and a 60/60 rate of 0.02 that is 0.10; with a 0.01 connect fee it is 0.11.

Checks, in this order (D-19):

1. `available > 0`
2. `available >= reserve_amount`

Both are re-evaluated inside the reservation statement:

```sql
UPDATE accounts SET reserved = reserved + $amt
WHERE id = $1 AND (balance + allowed_credit - reserved) > 0 AND (balance + allowed_credit - reserved) >= $amt
RETURNING ...
```

Two concurrent calls from one customer serialise on the row lock the `UPDATE` takes; the second one re-evaluates `WHERE` after the first committed and fails when the money is gone. `TestIntegrationConcurrentReserve` runs eight goroutines against funds for one reservation and asserts exactly one wins; e2e scenario (g) does the same with two real SIP calls.

The same transaction inserts the `reserve` ledger entry (amount negative, `balance_after` unchanged because reservations do not touch `balance`), the pending CDR row and the `active_calls` row. A Redis key `resv:<uuid>` with TTL `max_call_duration + orphan_timeout` mirrors the reservation for operators (`redis-cli keys 'resv:*'`).

### Maximum call duration

```
spendable        = available                      (before the reservation; the reservation is part of it)
max_call_seconds = min(floor((spendable - connect_fee) * 60 / rate_per_min), max_call_duration)
```

Lua sets `execute_on_answer=sched_hangup +max_call_seconds ALLOTTED_TIMEOUT` on the a-leg. FreeSWITCH also enforces `max_call_duration` (4 h default) through the same mechanism.

## 4. True-up on hangup (Section 5)

`billing.Engine.Bill` runs one transaction:

1. `SELECT ... FROM cdrs WHERE call_uuid = $1 FOR UPDATE`; if `billed_at IS NOT NULL` stop (idempotent, D-17).
2. `billsec = end_time - answer_time` (0 when never answered), from FreeSWITCH's `billsec` variable.
3. `price = Amount(BilledSeconds(billsec, sell...), sell)`; `cost = Amount(BilledSeconds(billsec, buy...), buy)` for the carrier that answered. Failed attempts cost 0 (D-23).
4. Customer account (locked): `reserved -= reserved_amount` with ledger `release(+reserved_amount)`; `balance -= price` with ledger `charge(-price)`.
5. Carrier account (locked): `balance -= cost` with ledger `cost(-cost)`.
6. `UPDATE cdrs` with every field of Section 2.5, `DELETE FROM active_calls`.
7. Commit, then Redis: `DEL resv:<uuid>`, `DECR cc:customer:<id>`, `PUBLISH cdr.completed <uuid>`.
8. If `available < SBC_BILLING_LOW_BALANCE_THRESHOLD` a `notifications` row is written.

Net effect on `available`: the 5-minute pre-deduction is replaced by the true price. Shorter calls get the difference back, longer calls pay more, and "longer" is bounded by `max_call_seconds`.

### The overrun edge (D-21)

`sched_hangup` fires at whole seconds and increments round up, so the true price can exceed the money that `max_call_seconds` was computed from by at most one `subsequent_increment` at the rate. Example: 0.10 available at 0.02/min 60/60 gives `max_call_seconds = 300`; the hangup at 300 s bills 300 s = 0.10 exactly. With a 30/6 rate and 0.10 available, `max_call_seconds = 300`, a hangup one tick late bills 306 s = 0.102, overrunning by 0.002. This is charged against `allowed_credit`, or leaves the balance slightly negative on a strict prepaid account. It is deliberate: hanging up one increment early would short-change every customer on every call.

## 5. Ledger

`ledger_entries` is append-only. Types:

| type | sign | touches `balance`? | when |
|------|------|--------------------|------|
| `reserve` | negative | no (`reserved` only) | setup |
| `release` | positive | no | hangup, rejection after reservation, reconciliation |
| `charge` | negative | yes | hangup, customer price |
| `cost` | negative | yes | hangup, carrier account |
| `topup` | positive | yes | operator or seed |
| `adjustment` | signed | yes | operator |
| `refund` | positive | yes | operator |

`balance_after` on every entry is the account `balance` after the entry was applied (unchanged for reserve and release).

## 6. Invariants and `make reconcile`

For every account:

```
balance  == sum(amount WHERE type IN (charge, cost, topup, adjustment, refund))
reserved == sum(active_calls.reserved_amount)
reserved == -sum(amount WHERE type IN (reserve, release))
```

`sbc-api reconcile` (`make reconcile`) prints one line per account and exits non-zero on any mismatch. The e2e tests call it after every money-moving scenario.

## 7. Orphaned calls

If neither the ESL event nor the `mod_json_cdr` POST arrives (API down for longer than FreeSWITCH's retries, FreeSWITCH crash), `billing.Reconciler` runs every minute: reservations older than `expires_at + orphan_timeout` are checked against `show channels`; when the channel is gone the reservation is released, no charge is made (the true billsec is unknown), and the CDR is finalised as `failed` with `reject_reason = orphaned reservation`. The `json_cdr_failed` spool directory keeps the original CDR JSON for manual rating if needed.

## 8. Rejected calls

Rejections before the reservation (unknown IP, suspended, limits, malformed number, no rate, no funds) write a final CDR with `billsec = 0` and no ledger entries. Rejections after the reservation (no route) release it (`release` entry) and finalise the CDR as `rejected_route`.
