# Load test

`tools/loadtest` drives core's gRPC API the way the bot does, with many customers at once, and reports throughput,
p50/p95/p99 per call and errors. This page has the numbers from 2026-10-12, what they mean, the limits they show and
what to watch on a live server.

## Summary

- **Ceiling on this machine.** Core handled about 360–410 complete customer journeys a second (6,500–7,500 calls a
  second) with no errors and no timeouts. Each journey is a new customer who registers, gets a top-up, buys a plan
  from the wallet, lists their services and opens a ticket. From 50 to 500 users throughput stayed flat and only
  latency grew: overload showed up as queueing, never as failures.
- **At a normal pace** (500 customers, about 2 s between taps), reads took under 1 ms and writes 1–2 ms at p50, and
  deliveries kept up with sales.
- **What limits core:** every core transaction that writes an event passes one lock at a time (by design, for event
  order). Connections waiting for it fill core's small connection pool, 16 connections here. CPU was not the limit.
- **Deliveries are the first thing to fall behind.** Core delivers paid orders one at a time. During the stress runs,
  and at 2,000 paced customers, a queue built up. It cleared after the load stopped, and no order was lost.
- **In practice the bot and Telegram are the limit, not core.** The bot handles customers' updates 8 at a time, and
  Telegram lets a bot send about 30 messages a second, which is about 4 journeys a second. Core did about 100 times
  that.

## What the tool does

Each simulated customer runs one journey after another, each as a new Telegram user. Every bot update starts with the
bot loading its sender (`GetUser`), as the real bot does:

| Bot update | Calls to core |
|---|---|
| /start from someone new | `GetUser` (NOT_FOUND: expected, not an error) |
| The language tap that registers them | `GetUser` (NOT_FOUND), `UpsertUser` |
| An admin tops the wallet up with the plan's price | `GetUser` (the owner), `AdminAdjustBalance` |
| Buy: the plan list | `GetUser`, `ListPlans` |
| A plan's page with the wallet balance | `GetUser`, `ListPlans`, `GetWallet` |
| Pay from the wallet | `GetUser`, `ListPlans`, `CreateOrder`, `PayOrderWithWallet` |
| My services | `GetUser`, `ListSubscriptions` |
| A message to support opens a ticket | `GetUser`, `AddTicketMessage` |

That is 18 calls per journey. The first error ends a journey, just as the bot stops at an error message.

- **Pace.** By default customers do not pause, so N users means about N calls in flight: a stress test. With
  `-think D` each customer waits D, give or take half, between updates, and customers start spread over the first D.
- **Report.** A Markdown table per run:
  - each call's count, rate, p50/p95/p99/max and errors by gRPC code. The percentiles cover successful calls only,
    so a fast failure never makes latency look better.
  - a row for whole journeys (which include the pauses).
  - on the local stack, notes on deliveries and core's connection pool. The pool is read as soon as the load
    stops, before the wait for the delivery queue, so its numbers cover the load alone.
- **Timeouts.** Each call times out after 10 s (`-timeout`).
- **Clean-up.** Before the run the tool adds an enabled plan, "Load test", priced at 10,000 IRT. Afterwards it
  switches that plan off.

**Covered:** core (gRPC, domain and store), Postgres, the delivery worker, the payment-event consumer, the provisioner
over gRPC, and a fake 3x-ui panel over HTTP. All of them run in one process, in `internal/testenv`.

**Not covered:**
- The bot process: Telegram, its Redis state and limiter, its 8 workers.
- The payments service's gateways (a wallet purchase never calls payments).
- A real 3x-ui panel (the fake one answers from memory).
- Docker networking, the notifier reading core's event feed, the dashboard, and broadcasts.

## How to run

The usual way is the whole backend in-process on the test Postgres (the same database setup as the Go tests). Each
user count gets a fresh stack. Run it without `-race`, or you measure the race detector:

```sh
go test ./tools/loadtest -run TestLocalStack -count=1 -timeout 60m -v -args -users 50,200,500 -duration 30s
go test ./tools/loadtest -run TestLocalStack -count=1 -timeout 60m -v -args -users 500,2000 -think 2s -duration 60s
```

Without `-users` the test skips, so `go test ./...` and CI do not run it. After each run it waits up to 10 minutes for
the delivery queue to empty. That wait is most of the time a stress run takes: about 5 minutes per user count here.

It can also drive a core that is already running, such as a development stack started by hand from the service
binaries. Put the bot's service token in an environment variable, never in a flag:

```sh
BOBRES_LOADTEST_TOKEN=... go run ./tools/loadtest -addr 127.0.0.1:9090 -owner <owner's Telegram id> -users 200 -duration 30s
```

It refuses an address that is not on this machine unless you pass `-force`. A run writes thousands of customers,
orders and tickets, and creates real clients on the stack's panel. **Never point it at a production install.**

## Machine and setup

- **Machine:** Intel Core i9-9980HK (8 cores, 16 threads, 2.4 GHz), 64 GB RAM, internal SSD, macOS 26.6.2. Other
  desktop apps were open: the load average was about 3.4 before the runs.
- **Postgres:** 16.15 (Homebrew) on the local Unix socket, with the stock settings: `max_connections` 100,
  `shared_buffers` 128 MB, `synchronous_commit` on, `fsync` on, `wal_sync_method` open_datasync.
- **Go:** 1.27.1, GOMAXPROCS 16. The load generator, core, payments, provisioner and the fake panel share one
  process. Core's connection pool is pgx's default, the larger of 4 and the CPU count: 16 here.
- **Code:** branch `claude/phase7-hardening` after `c368329`.

## Results

### Stress: no pause between updates

| Users | Journeys/s | Calls/s | Journey p50 / p95 / p99 | Slowest call at p99 | Errors | Pool acquires that waited, with the drain | Deliveries during the run | Waiting at the end, cleared in |
|---:|---:|---:|---|---|---:|---|---:|---|
| 50 | 361 | 6,503 | 125 / 193 / 573 ms | AdminAdjustBalance 88 ms | 0 | 93%, 3.4 ms average | 20.8/s | 10,245 in 222 s (46/s) |
| 200 | 371 | 6,684 | 502 / 690 / 1,477 ms | AdminAdjustBalance 215 ms | 0 | 93%, 18.1 ms | 12.2/s | 10,913 in 257 s (43/s) |
| 200 (repeat) | 414 | 7,451 | 481 / 513 / 592 ms | AdminAdjustBalance 106 ms | 0 | 93%, 16.3 ms | 13.1/s | 12,140 in 298 s (41/s) |
| 500 | 399 | 7,174 | 1,244 / 1,322 / 1,351 ms | CreateOrder 316 ms | 0 | 92%, 44.3 ms | 6.5/s | 12,114 in 306 s (40/s) |

Each run lasted 30 s. The two 200-user runs differ by about 10% in throughput, and the first one had more slow
outliers. Read these numbers as ±10%.

The pool column understates contention during the load. The tool used for these runs read the pool after the
delivery queue had emptied, 4–5 minutes after the load. So the column also counts the drain's acquires, which found
idle connections and did not wait. The tool now reads the pool as soon as the load stops. A later 200-user check
on the same machine, read that way, ran 380 journeys a second and found that 312,197 of 312,212 acquires waited
(100%), for 17.7 ms on average. That average agrees with the table, since the drain's acquires did not wait:
only the share is too low.

<details>
<summary>Per call: 50 users</summary>

| call | calls | calls/s | p50 ms | p95 ms | p99 ms | max ms | errors |
|---|---:|---:|---:|---:|---:|---:|---:|
| GetUser | 86968 | 2890.4 | 2.3 | 18.9 | 21.1 | 210 | 0 |
| UpsertUser | 10871 | 361.3 | 2.7 | 19.5 | 23.4 | 210 | 0 |
| AdminAdjustBalance | 10871 | 361.3 | 14.8 | 36.9 | 87.8 | 359 | 0 |
| ListPlans | 32613 | 1083.9 | 4.6 | 21.9 | 29.7 | 239 | 0 |
| GetWallet | 10871 | 361.3 | 4.5 | 21.8 | 30.0 | 238 | 0 |
| CreateOrder | 10871 | 361.3 | 7.7 | 26.1 | 44.4 | 287 | 0 |
| PayOrderWithWallet | 10871 | 361.3 | 9.7 | 29.2 | 56.2 | 330 | 0 |
| ListSubscriptions | 10871 | 361.3 | 2.3 | 18.9 | 21.3 | 168 | 0 |
| AddTicketMessage | 10871 | 361.3 | 11.7 | 32.5 | 67.4 | 348 | 0 |
| **whole journey** | 10871 | 361.3 | 125 | 193 | 573 | 925 | 0 |

</details>

<details>
<summary>Per call: 200 users (first run, then the repeat)</summary>

| call | calls | calls/s | p50 ms | p95 ms | p99 ms | max ms | errors |
|---|---:|---:|---:|---:|---:|---:|---:|
| GetUser | 90280 | 2970.6 | 13.7 | 34.9 | 75.9 | 418 | 0 |
| UpsertUser | 11285 | 371.3 | 14.3 | 34.6 | 58.7 | 402 | 0 |
| AdminAdjustBalance | 11285 | 371.3 | 62.1 | 101 | 215 | 755 | 0 |
| ListPlans | 33855 | 1114.0 | 35.3 | 48.8 | 88.5 | 592 | 0 |
| GetWallet | 11285 | 371.3 | 31.9 | 47.3 | 73.9 | 591 | 0 |
| CreateOrder | 11285 | 371.3 | 52.2 | 81.3 | 137 | 709 | 0 |
| PayOrderWithWallet | 11285 | 371.3 | 33.9 | 50.0 | 102 | 516 | 0 |
| ListSubscriptions | 11285 | 371.3 | 14.5 | 34.7 | 50.6 | 401 | 0 |
| AddTicketMessage | 11285 | 371.3 | 48.6 | 81.6 | 135 | 642 | 0 |
| **whole journey** | 11285 | 371.3 | 502 | 690 | 1477 | 1845 | 0 |

| call | calls | calls/s | p50 ms | p95 ms | p99 ms | max ms | errors |
|---|---:|---:|---:|---:|---:|---:|---:|
| GetUser | 100288 | 3311.6 | 12.8 | 32.6 | 43.5 | 133 | 0 |
| UpsertUser | 12536 | 414.0 | 13.3 | 33.1 | 39.6 | 82.8 | 0 |
| AdminAdjustBalance | 12536 | 414.0 | 58.4 | 87.2 | 106 | 159 | 0 |
| ListPlans | 37608 | 1241.9 | 32.9 | 45.6 | 49.7 | 101 | 0 |
| GetWallet | 12536 | 414.0 | 29.5 | 44.2 | 47.6 | 95.7 | 0 |
| CreateOrder | 12536 | 414.0 | 50.3 | 74.4 | 88.7 | 161 | 0 |
| PayOrderWithWallet | 12536 | 414.0 | 32.6 | 42.2 | 87.3 | 138 | 0 |
| ListSubscriptions | 12536 | 414.0 | 13.7 | 32.9 | 37.4 | 102 | 0 |
| AddTicketMessage | 12536 | 414.0 | 47.2 | 61.3 | 96.6 | 127 | 0 |
| **whole journey** | 12536 | 414.0 | 481 | 513 | 592 | 690 | 0 |

</details>

<details>
<summary>Per call: 500 users</summary>

| call | calls | calls/s | p50 ms | p95 ms | p99 ms | max ms | errors |
|---|---:|---:|---:|---:|---:|---:|---:|
| GetUser | 98528 | 3188.6 | 42.9 | 130 | 200 | 288 | 0 |
| UpsertUser | 12316 | 398.6 | 45.9 | 89.5 | 139 | 243 | 0 |
| AdminAdjustBalance | 12316 | 398.6 | 156 | 248 | 312 | 350 | 0 |
| ListPlans | 36948 | 1195.7 | 52.6 | 114 | 174 | 299 | 0 |
| GetWallet | 12316 | 398.6 | 49.5 | 115 | 163 | 206 | 0 |
| CreateOrder | 12316 | 398.6 | 107 | 236 | 316 | 388 | 0 |
| PayOrderWithWallet | 12316 | 398.6 | 65.6 | 207 | 252 | 301 | 0 |
| ListSubscriptions | 12316 | 398.6 | 42.6 | 86.3 | 146 | 226 | 0 |
| AddTicketMessage | 12316 | 398.6 | 116 | 207 | 250 | 290 | 0 |
| **whole journey** | 12316 | 398.6 | 1244 | 1322 | 1351 | 1417 | 0 |

</details>

### At a pace: 2 s (give or take 1 s) between a customer's updates

| Users | Journeys/s | Calls/s | p50, reads / writes | p95 | p99 | Pool acquires that waited, with the drain | Deliveries |
|---:|---:|---:|---|---|---|---|---|
| 500 | 31.1 | 560 | 0.4–0.5 / 1.1–2.2 ms | ≤ 5 ms, but GetUser 14 and AdminAdjustBalance 26 ms | ≤ 20 ms, but AdminAdjustBalance 70 ms | 79 of 71,111 | kept up: 31.1/s, none waiting at the end |
| 2,000 | 122.9 | 2,213 | 0.4–0.6 / 1.3–3.5 ms | 17–100 ms | 46–151 ms | 24%, 9.4 ms average | fell behind: 43.8/s, 6,168 waiting, cleared in 117 s (53/s) |

New journeys started for 60 s. The rates are over the whole time until the last journey ended, 77–78 s. A journey
took about 14 s here: 7 pauses averaging 2 s.

As in the stress runs, the 2,000-user pool figure also counts the 117 s drain after the load, so the share that
waited during the load was higher than 24%. The 500-user run had nothing to drain, so its figure is the load's.

<details>
<summary>Per call: 500 and 2,000 paced users</summary>

| call (500 users) | calls | calls/s | p50 ms | p95 ms | p99 ms | max ms | errors |
|---|---:|---:|---:|---:|---:|---:|---:|
| GetUser | 19184 | 248.7 | 0.4 | 14.4 | 16.7 | 34.2 | 0 |
| UpsertUser | 2398 | 31.1 | 0.8 | 1.8 | 17.7 | 56.1 | 0 |
| AdminAdjustBalance | 2398 | 31.1 | 2.2 | 26.0 | 70.3 | 130 | 0 |
| ListPlans | 7194 | 93.3 | 0.5 | 1.2 | 16.9 | 27.9 | 0 |
| GetWallet | 2398 | 31.1 | 0.4 | 0.9 | 1.4 | 17.5 | 0 |
| CreateOrder | 2398 | 31.1 | 1.1 | 2.3 | 18.3 | 45.2 | 0 |
| PayOrderWithWallet | 2398 | 31.1 | 1.6 | 3.6 | 19.2 | 47.3 | 0 |
| ListSubscriptions | 2398 | 31.1 | 0.4 | 1.0 | 2.9 | 23.5 | 0 |
| AddTicketMessage | 2398 | 31.1 | 1.8 | 4.9 | 19.6 | 28.4 | 0 |
| **whole journey** | 2398 | 31.1 | 14093 | 16614 | 17595 | 18750 | 0 |

| call (2,000 users) | calls | calls/s | p50 ms | p95 ms | p99 ms | max ms | errors |
|---|---:|---:|---:|---:|---:|---:|---:|
| GetUser | 76608 | 983.6 | 0.4 | 27.9 | 59.3 | 117 | 0 |
| UpsertUser | 9576 | 122.9 | 0.9 | 27.2 | 61.8 | 131 | 0 |
| AdminAdjustBalance | 9576 | 122.9 | 3.5 | 99.8 | 151 | 232 | 0 |
| ListPlans | 28728 | 368.8 | 0.6 | 17.8 | 51.5 | 140 | 0 |
| GetWallet | 9576 | 122.9 | 0.5 | 17.4 | 55.3 | 137 | 0 |
| CreateOrder | 9576 | 122.9 | 1.3 | 33.8 | 97.1 | 203 | 0 |
| PayOrderWithWallet | 9576 | 122.9 | 2.8 | 50.9 | 92.0 | 178 | 0 |
| ListSubscriptions | 9576 | 122.9 | 0.4 | 16.8 | 46.0 | 97.6 | 0 |
| AddTicketMessage | 9576 | 122.9 | 3.2 | 86.0 | 131 | 194 | 0 |
| **whole journey** | 9576 | 122.9 | 14119 | 16653 | 17634 | 19692 | 0 |

</details>

## What the numbers mean

- **Throughput reached its ceiling by 50 users.** More users only made each journey wait longer, and the numbers fit
  a closed system at its limit. For example, 500 users ÷ 399 journeys a second ≈ 1.25 s per journey, which matches
  the measured p50 of 1,244 ms.
  - Nothing failed and nothing hit the 10 s timeout, even at 500 users with no pause.
  - The slowest calls are the three writes that publish an event (`AdminAdjustBalance`, `AddTicketMessage` and
    `PayOrderWithWallet`) and `CreateOrder`. `CreateOrder` publishes nothing. It takes three pool connections one
    after another: one to load the customer, one for the plan, then one for its own transaction. Under stress each
    of them waits for a free connection, which is why it is as slow as the event writes (limit 2).
- **A normal pace looks like the paced 500-user run.** At p50, reads took under a millisecond and writes 1–2 ms.
  - Values of 15–20 ms show up at p95/p99 on every kind of call, reads included, even at light load. They were not
    investigated. They may come from this Mac, so check them on Linux before reading anything into them.
- **What this means in production:**
  - The bot handles customers' updates on 8 workers (`cmd/bot/main.go`), so core sees about 8 customer-driven calls
    in flight at most, plus the bot's notifier and broadcast workers. The 50-user stress level is already 6 times
    that.
  - Telegram's bot FAQ asks bots to stay at about 30 messages a second to different chats. A journey sends at least
    one message for each of its 8 updates, so one bot finishes at most about 4 journeys a second. That is roughly
    100 times less than core managed here.
  - A small VPS will be slower than this machine (see the limits below), but core still has a wide margin. In
    practice the shop is limited by Telegram, the bot and the 3x-ui panel.
- **Database size:** about 4.4 KB per customer who registered, bought once and opened a ticket. The repeat 200-user
  run had 53 MB for 12,537 customers, measured while deliveries were still running, so a little more once every
  service exists. The largest table was `core.outbox_core`, the event feed, at 16 MB.

## Limits found

1. **Core's event writes pass one at a time.**
   - **How it works:** a core transaction that publishes an event takes one advisory lock just before its outbox
     insert and holds it until it commits (`internal/eventbus/outbox.go`). This keeps sequence numbers in order for
     feed readers. The events are a top-up, a paid order, a new ticket and a delivered service.
   - **What the samples showed:** in all 12 samples of `pg_stat_activity` (one every 2 s during the repeat 200-user
     run), 6 to 10 sessions were waiting on an advisory lock. In this workload core's outbox lock is the only one
     contended: payments is idle on the wallet path, and the provisioner works on one order at a time. Meanwhile
     Postgres used about 3 CPU cores and the Go process about 4 of the 16 threads. So the ceiling is this lock and
     the disk flush at each commit, not CPU.
   - **The arithmetic:** a journey publishes 3 events in its own calls (the top-up, the payment and the ticket),
     and its delivery publishes 1 more. In the stress runs deliveries fell behind (6.5–21 a second during the load),
     so 400 journeys a second came to about 1,200 lock-holding commits a second, plus the deliveries. When
     deliveries keep up, a journey costs 4 event writes. A disk that flushes more slowly lowers the ceiling: at 2 ms
     per commit it would be about 500 event writes a second, about 125 journeys.
   - **Verdict:** this is by design and far above real traffic. It is the number to remember if a feature ever
     writes many events per customer action.
2. **Core's connection pool is small and cannot be changed.**
   - **Size:** pgx's default is the larger of 4 and the CPU count: 16 here, and 4 on a VPS with 4 or fewer vCPUs.
   - **Effect:** connections waiting for the event lock keep their pool slot. Under stress, practically every pool
     request had to wait (100% in the 200-user check above; the table's 92–93% also counts the drain), for 3–44 ms
     on average. Even plain reads were slowed: at 200 and 500 users `GetUser` took 13–43 ms at p50, against 0.4 ms
     at a normal pace. A call that takes several connections in turn waits each time: `CreateOrder` writes no event
     but takes three, and at 500 users it was as slow as `AdminAdjustBalance` (316 against 312 ms at p99).
   - **Why it cannot be raised today:** `pool_max_conns` in `BOBRES_DATABASE_URL` would size the pool. But core's
     migration step opens the same URL with `database/sql`, which passes the parameter to Postgres as a setting, and
     Postgres refuses the connection ("unrecognized configuration parameter", checked by hand).
   - **Fix, if ever needed:** a separate setting, or removing `pool_*` parameters before migrating.
3. **Deliveries run one at a time, and share core's pool and the event lock.**
   - **With the fake panel:**
     - About 80 orders a second with a short queue (550 orders, in a 5-user trial run).
     - 40–53 a second while working through 6,000–12,000 waiting orders. Each claim sorts every waiting order by
       `updated_at`, and the index covers `(status, next_attempt_at)`.
     - Only 6.5–21 a second during the stress load.
   - **Falling behind at a pace:** at 2,000 paced customers (123 purchases a second) deliveries fell behind. 6,168
     orders were waiting when the load stopped, and they cleared 2 minutes later.
   - **With a real 3x-ui panel:** each delivery adds the panel's own HTTP calls (create the client, then read its
     links). The delivery rate is then about 1 ÷ the time per delivery: at 200 ms, 5 a second or 300 a minute. A
     promo or a broadcast that sells faster than that builds a queue. Customers wait longer for their link, but no
     order is lost (see [provisioning backlog](runbooks/provisioning-backlog.md)).
4. **Event tables only grow.** I found no code that deletes old outbox rows. At about 1.3 KB per customer here this
   is fine for a long time at real volumes, but a retention job would be worth adding eventually.

## What to watch in production

The commands use the `dc` and `bsql` helpers from the [runbooks README](runbooks/README.md#before-you-start).

1. **The delivery queue.** It is the first thing to grow in a sales burst. Normally nothing is waiting, or only for a
   few seconds:

   ```sh
   bsql "SELECT status, count(*), now() - min(updated_at) AS oldest FROM core.orders
         WHERE status IN ('paid','provisioning','provision_failed') GROUP BY status;"
   ```

   If orders stay waiting for minutes, follow [provisioning backlog](runbooks/provisioning-backlog.md).

2. **What Postgres sessions are waiting on:**

   ```sh
   bsql "SELECT usename, state, wait_event_type, wait_event, count(*) FROM pg_stat_activity
         WHERE datname = 'bobres' GROUP BY 1, 2, 3, 4 ORDER BY 5 DESC;"
   ```

   - Many `svc_core` rows in `Lock / advisory` mean core's event writes are queueing, as in the stress runs above.
   - If all of `svc_core`'s connections are `active`, core's pool is full, and every bot call waits for a connection.
   - A few `active` rows and the rest `idle` is normal.

3. **The connection budget.**
   - Each Go service (core, payments, provisioner) may open up to the larger of 4 and the CPU count. Backups and psql
     sessions come on top.
   - Postgres allows 100 connections by default, and `docker-compose.yml` does not change that.
   - So only a host with around 32 or more CPUs could reach the limit in a burst. Check with
     `bsql "SELECT count(*), current_setting('max_connections') FROM pg_stat_activity;"`.

4. **Memory and CPU of the containers:** `docker stats --no-stream`. `docker-compose.yml` caps the app containers at
   512 MB and Postgres at 1 GB.

5. **Database size:** `bsql "SELECT pg_size_pretty(pg_database_size('bobres'));"`. Expect roughly 4–5 KB per
   customer and purchase, growing with tickets and events. See [disk full](runbooks/disk-full.md).

## Open points

- Run the tool on a real 2-vCPU VPS, with Docker and a real 3x-ui panel, to measure the delivery rate and the
  4-connection pool there. The Mac used here cannot pull Docker images.
- A setting for core's pool size (limit 2), if a large install ever needs one.
- A retention job for the outbox tables (limit 4).
- Run the stress and paced sets again with the current tool, to replace the pool columns, which include the drain.
