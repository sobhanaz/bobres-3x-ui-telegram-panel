# Benchmark baseline: v0.19.0 before code protection

This is the performance and size baseline of BOBRES v0.19.0, taken before the binaries are built with code protection
(garble). `tools/bench/run.sh` takes it and can take it again after any change to how the binaries are built, so the
two can be compared number by number. The raw results are in [bench/2026-10-29/plain/](bench/2026-10-29/plain/),
with the full generated tables in its [summary.md](bench/2026-10-29/plain/summary.md).

v0.20.0 came out while this ran. It changes the messages to resellers and the actions for services missing from the
panel: no code the load test or the services' start goes through. The baseline stays on v0.19.0.

## Summary

- **The ceiling has not moved since 2026-10-12.** On a quiet machine core finished 366–448 customer journeys a second
  in-process (6,600–8,000 calls a second), against 361–414 then. Run as six separate binaries with the services
  talking over gRPC as in production, it finished 327 a second at 200 users. Throughput stays flat from 50 to 500
  users and only latency grows, as before.
- **The limit is still one Postgres commit at a time**, behind core's event lock, with core's 16-connection pool
  full: 100% of pool requests waited during every stress run. Core used 2.7 of 16 CPU threads at 327 journeys a
  second. CPU is not the limit.
- **Two calls got slower since 2026-10-12.** `GetWallet` makes about 9 database round trips now instead of 1 (the
  Toman wallet's USDT equivalent reads the settings twice, 4 reads each time), so under stress it is the slowest
  read and as slow as the writes: p95 34 ms at 50 users against 22 ms then, 215 ms at 250 users. `CreateOrder` reads
  the license state (2 more queries) before its transaction. At a normal pace both are still a few milliseconds.
- **A load test error that is not a production bug.** Every in-process stress run had 10–15 `CreateOrder` errors
  (`Internal`), always at most the pool size and never more with more users or a longer run. The test stack empties
  every table, `core.install` too, so the first wave of purchases all create the install row at once, and
  `store.InstallID` can then read no row. A real install has the row from its migration. The separate binaries had 0
  errors in more than 35,000 journeys.
- **The binaries** for linux/amd64, built as release.yml builds them: core 38.4 MB, bot 25.3, payments 19.2,
  provisioner 18.9, backup 14.1, the `bobres` CLI 10.0. A clean build of core takes 28 s here, a relink 1.7 s.
- **Start and memory.** Ready in 21–750 ms on an empty database (core 690 ms, most of it its 48 migrations) and in
  24–98 ms on a loaded one. Idle, the six processes hold 116 MB together (core 33 MB). Under the stress load core
  peaked at 62 MB and the six together at about 180 MB.
- **Deliveries cost more CPU than the purchases.** The provisioner used about 15 ms of CPU per delivery to the fake
  panel (0.6 cores at 40 deliveries a second); core about 0.46 ms per call. Worth a profile before a store sells
  hundreds of services a minute.
- **The machine was shared.** Other sessions ran test suites on the same Postgres, and after a restart Spotlight
  indexed the disk. Runs that started with a load average above 4 were up to 4 times slower. The tables give the
  median of all runs with their range, and the quiet runs apart.

## The machine

- Intel Core i9-9980HK (8 cores, 16 threads, 2.4 GHz), 64 GB RAM, internal SSD, macOS 26.6.2: the Mac of
  2026-10-12.
- Go 1.27.1 (release.yml and the images use 1.26.x), Node 24.11.1, npm 11.6.2.
- Postgres 16.15 (Homebrew) on the local socket with the stock settings: `max_connections` 100, `shared_buffers`
  128 MB, `synchronous_commit` on, `fsync` on, `wal_sync_method` open_datasync. 663 databases were on the server,
  570 of them test databases left by killed test runs of other sessions; the server's autovacuum visits each.
- Measured 2026-10-29 and, after the Mac restarted halfway, 2026-10-30. The load average before each measurement is
  in the raw results and in the tables.

## Load test, in-process: against 2026-10-12

The same tool and scenarios as [12-load-test.md](12-load-test.md): the whole backend in one process (core, payments
and provisioner over gRPC, a fake 3x-ui panel), a fresh stack per run, 3 runs each. Stress: no pause between a
customer's updates; paced: 2 s, give or take 1 s.

| Run | 2026-10-12 journeys/s | v0.19.0 journeys/s, quiet runs | v0.19.0, all runs: median (range) | Journey p50 / p95 / p99 ms, quiet | Errors per run | Waiting at the end, drained in |
|---|---:|---:|---:|---|---:|---|
| stress, 50 users | 361 | 385 | 171 (107–385) | 133 / 142 / 190 (was 125 / 193 / 573) | 10–15 | 10,942 in 259 s |
| stress, 100 users | – | 379 | 190 (124–379) | 257 / 292 / 341 | 14–15 | 10,917 in 274 s |
| stress, 200 users | 371, 414 | – | – | – | – | – |
| stress, 250 users | – | 366 | 98 (98–366) | 675 / 737 / 812 | 15 | 10,830 in 281 s |
| stress, 500 users | 399 | 385 (361–448) | 385 (361–448) | 1,357 / 1,413 / 1,472 (was 1,244 / 1,322 / 1,351) | 14–15 | 11,734 in 335 s |
| paced, 500 users | 31.1 | 31 | 31 (31–32) | 14,058 / 16,622 / 17,590 | 0 | none |
| paced, 2,000 users | 122.9 | 123 | 122 (119–124) | 14,025 / 16,568 / 17,515 | 0 | 5,797 in 136 s (was 6,168 in 117 s) |

"Quiet" means the 1-minute load average was below 4 when the run started. The 2026-10-12 page ran 50/200/500
users; this run adds 100 and 250 as the brief asked, and has no 200-user in-process run (the separate binaries ran
200).

Per call, the quiet runs, p50 / p95 ms (2026-10-12 in brackets where it ran the same count):

| Call | 50 users | 100 users | 250 users | 500 users |
|---|---|---|---|---|
| GetUser | 1.6 / 18.4 (2.3 / 18.9) | 4.2 / 22.3 | 15.0 / 38.1 | 40.1 / 110 (42.9 / 130) |
| UpsertUser | 2.0 / 19.2 (2.7 / 19.5) | 4.8 / 23.0 | 17.0 / 37.3 | 44.6 / 91.1 (45.9 / 89.5) |
| AdminAdjustBalance | 11.0 / 29.7 (14.8 / 36.9) | 30.9 / 41.3 | 67.0 / 106 | 149 / 221 (156 / 248) |
| ListPlans | 4.8 / 22.8 (4.6 / 21.9) | 12.1 / 32.2 | 38.1 / 79.0 | 49.2 / 150 (52.6 / 114) |
| **GetWallet** | **15.9 / 34.0 (4.5 / 21.8)** | **45.6 / 79.2** | **94.4 / 215** | **131 / 205 (49.5 / 115)** |
| CreateOrder | 10.4 / 28.9 (7.7 / 26.1) | 36.6 / 49.3 | 90.8 / 154 | 150 / 348 (107 / 236) |
| PayOrderWithWallet | 8.4 / 26.8 (9.7 / 29.2) | 11.3 / 30.1 | 34.9 / 62.2 | 64.2 / 163 (65.6 / 207) |
| ListSubscriptions | 1.7 / 18.6 (2.3 / 18.9) | 4.4 / 22.5 | 16.7 / 37.4 | 40.3 / 89.0 (42.6 / 86.3) |
| AddTicketMessage | 9.3 / 27.9 (11.7 / 32.5) | 17.0 / 35.8 | 53.3 / 90.1 | 109 / 191 (116 / 207) |

At a pace (500 users) every call took 0.4–2 ms at p50 and at most about 20 ms at p99, as on 2026-10-12.

## The service binaries as separate processes

`benchtool stack` runs the real binaries, built as release.yml builds them but for this Mac (darwin/amd64): a fresh
database, a fake 3x-ui panel and a fake Telegram API, a throwaway Redis, the services on 127.0.0.1 with a token per
pair as `docker-compose.yml` wires them, and every outbound HTTP(S) request sent to a dead proxy. It starts them one
after the other, measures them idle, drives core with `tools/loadtest` (200 users for 30 s without pauses, then
500 users for 60 s 2 s apart, each followed by the delivery drain), restarts each on the loaded database and stops
them. 3 runs (2026-10-30, load average 3.3–8 when the stress load started).

| Service | Ready, empty database | Ready, loaded database | RSS idle | CPU idle | Under the stress load: CPU, RSS max | Peak RSS |
|---|---:|---:|---:|---:|---:|---:|
| core | 688 ms | 98 ms | 32.8 MB | 0.35% | 2.72 cores, 59.3 MB | 62.3 MB |
| payments | 242 ms | 71 ms | 18.0 MB | 0.15% | 0.00 cores, 18.5 MB | 20.9 MB |
| provisioner | 127 ms | 64 ms | 16.9 MB | 0.00% | 0.14 cores (0.63 in the drain), 31.7 MB | 37.2 MB |
| bot | 43 ms | 63 ms | 20.8 MB | 0.15% | 0.04 cores, 25.4 MB | 31.2 MB |
| backup | 21 ms | 24 ms | 9.7 MB | 0.00% | 0.00 cores, 9.7 MB | 9.8 MB |
| tls-agent (core's binary) | 30 ms | 32 ms | 17.7 MB | 0.05% | 0.00 cores, 17.7 MB | 18.1 MB |

- **Ready** is exec until `/readyz` answers 200 (the tls-agent: its first heartbeat file). On an empty database
  core runs its 48 migrations first (most of its 0.69 s), payments 10 and the provisioner 7. The bot is ready
  once Telegram (the fake) answered `getMe`.
- **First exec.** The first run of a freshly built binary took 0.7–1.0 s before it did anything: macOS scans a new
  executable. Later runs: 15–32 ms. This is the Mac, not BOBRES (Linux has no such scan), and it is kept out of the
  starts above.
- **Throughput:** 327 journeys a second (271–328), journey p95 673 ms, 0 errors; 9,600 orders waited when the 30 s
  ended and were delivered in 233 s (about 41 a second). At a pace: 31 journeys a second, as in-process, deliveries
  kept up.
- **CPU per unit of work:** core 85 CPU seconds for 10,200 journeys, about 8 ms per journey or 0.46 ms per call. The
  provisioner 142 CPU seconds for 9,600 deliveries, about 15 ms per delivery to a panel that answers from memory.
- **Database after both scenarios:** 82 MB for 12,300 customers, about 6.7 KB each (4.4 KB on 2026-10-12: the
  outbox, ledger and audit rows of newer features). The largest table is still `core.outbox_core` (21 MB of 77 MB
  in-process).

## Binaries

Built as release.yml builds them, `CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X …/internal/version.Version=v0.19.0"`,
with the dashboard and the Mini App built first because core embeds them. Clean: an empty `GOCACHE` (the modules
already downloaded); warm: the same build again from that cache, the binary removed first so the link really runs.
3 builds each.

| Binary | linux/amd64 | darwin/amd64 | Clean build | Warm build (link) |
|---|---:|---:|---:|---:|
| core | 38.36 MB | 39.21 MB | 28.5 s (19.8–32.8) | 1.7 s |
| bot | 25.29 MB | 25.89 MB | 26.5 s | 1.5 s |
| payments | 19.17 MB | 19.71 MB | 18.6 s | 1.2 s |
| provisioner | 18.90 MB | 19.44 MB | 17.7 s | 1.2 s |
| backup | 14.07 MB | 14.46 MB | 15.2 s | 1.0 s |
| bobres (CLI) | 9.97 MB | 10.17 MB | 13.8 s | 0.8 s |

The CLI's `main.imageDigests` is left out here; the release sets it to the five image digests, about 400 bytes more.
Core holds the dashboard (4.7 MB of files) and the Mini App (0.3 MB).

## Database

- **Migrations** on an empty database: 1.03 s for all three schemas (core 0.71 s, 48 files; payments 0.20 s, 10;
  provisioner 0.09 s, 7), 3 runs. Running them again on a migrated database, as every restart does: 0.05 s. (On a
  quiet Postgres the first quick check took 0.37 s.)
- **Size after the load test:** 77–80 MB after an in-process stress run of about 11,500 customers who each
  registered, bought once and opened a ticket, with every order delivered: 6.7 KB a customer. `core.outbox_core`
  21 MB, `core.ledger_entries` 9 MB, `core.subscriptions` 9 MB, `core.audit_log` 5 MB, `core.orders` 5 MB.

## Tests

The test suites are the part this baseline measured least well: they ran when the machine was busiest, and a test
run competes with the other sessions' test runs for the same Postgres. Each line is one run.

| Suite | Wall time | Load average before | Result |
|---|---:|---:|---|
| `go test -count=1 ./...` (default `-p`, 16) | 482 s | 2.4 | passed (2026-10-29) |
| the same | 548 s | 9.3 | 80 packages passed, 4 failed: `CREATE DATABASE` timed out after 15 s in `internal/testdb` (2026-10-30) |
| `go test -count=1 -p 2 ./...` | 576 s | 4.3 | passed (2026-10-29) |
| the same | 1,019 s | 4.3, rising to 12 | passed (2026-10-30) |
| `go test -race -count=1 -timeout 20m ./...`, as CI's Test (race) job | 1,250 s | 70 | 3 packages hit the 20-minute timeout (2026-10-30); stopped there |

- On GitHub's runner (4 vCPUs) the same race job takes 11.5 minutes: 692 s in the last green CI run on main
  (v0.20.0's), which is the steadier number to compare against.
- CI's small-pool step and both apps' vitest were not measured in the full run: the machine's load average went
  above 50 and the measurements were stopped. A quick check on 2026-10-29 (load average 5–10): the dashboard's 1,135
  vitest tests in 53 files took 74 s (603 s of CPU), the Mini App's 381 tests 8 s. In CI the whole Dashboard job
  (install, types, tests, build) takes 186 s and the Mini App job 44 s.
- Go test functions: 2,985 `Test` and, new with this baseline, 11 `Benchmark` functions in 7 packages.

`tools/bench/run.sh tests` measures all of it, 3 times; run it again on a quiet machine before comparing test
times.

## Frontend bundles

`npm run build` (vue-tsc, then Vite), 3 runs; gzip -9 sizes, as Vite reports them.

| App | Files | Raw | Gzip | Largest file | Build |
|---|---:|---:|---:|---|---:|
| Dashboard | 213 | 4.7 MB | 2.4 MB | `index-*.js` 782 KB (199 KB gzip); the beaver animation 301 KB, PrimeIcons 342 KB | 18.1 s |
| Mini App | 4 | 0.3 MB | 0.1 MB | | 5.2 s |

Every file is in [sizes/web-dashboard.json](bench/2026-10-29/plain/sizes/web-dashboard.json) and
[web-miniapp.json](bench/2026-10-29/plain/sizes/web-miniapp.json).

## Release artifacts

The v0.19.0 images on ghcr.io, read anonymously as a store pulls them; compressed layers, the download:

| Image | linux/amd64 | linux/arm64 |
|---|---:|---:|
| bobres-core | 14.0 MB | 12.8 MB |
| bobres-bot | 8.8 MB | 7.9 MB |
| bobres-payments | 7.1 MB | 6.5 MB |
| bobres-provisioner | 7.0 MB | 6.4 MB |
| bobres-backup | 121.4 MB | 118.8 MB |

The backup image is `postgres:16-alpine` (for `pg_dump`) with the Go binary on top; the others are a static binary,
CA certificates and time zones on `scratch`. A fresh install downloads about 159 MB of BOBRES images.

Workflow times (`gh run view --json jobs`):

- **Release v0.19.0** (run 40906669599): 21.5 minutes from the first job to the last. Verify (race tests, small-pool
  tests, govulncheck) 9.7 min; the five images, built in parallel for amd64 and arm64 and signed: 6.3–10.5 min
  (bot the slowest); the CLI for four platforms, signed checksums and the release: 1.3 min.
- **CI on main**, the last green run (40933572561, v0.20.0): 11.5 minutes, set by Test (race) at 692 s. Then the Docker
  images with the end-to-end install 296 s, Marzban 218 s, Dashboard 186 s, Lint 151 s, the 3x-ui matrix 56–65 s
  each, Build binaries 69 s, Mini App 44 s, govulncheck 20 s, gitleaks 11 s.

## Code size

| Kind | Files | Lines |
|---|---:|---:|
| Go, hand written | 494 | 140,077 |
| Go tests | 518 | 153,999 |
| Go, generated (`gen/`) | 14 | 26,070 |
| TypeScript (web) | 95 | 13,833 |
| TypeScript tests (web) | 80 | 29,021 |
| Vue | 180 | 37,372 |
| SQL migrations | 65 | 2,727 |
| Shell | 15 | 2,239 |
| Docs (Markdown, without the wiki) | 87 | 18,870 |
| Wiki | 39 | 3,416 |

## Go micro-benchmarks

The repository had none; this baseline adds 11 `Benchmark` functions in 7 packages, cheap and deterministic (fixed
keys, a fixed clock), next to the code they measure. `go test -run '^$' -bench . -benchmem -count 5`, median of 5,
on a quiet machine (load average below 4):

| Package | Benchmark | What it is | ns/op | allocs/op |
|---|---|---|---:|---:|
| internal/license | Verify | A store checking its license token | 65,100 | 10 |
| internal/license | Sign | The license server issuing one | 29,300 | 8 |
| internal/license | AdminVerify | The license server checking a signed admin request | 59,800 | 6 |
| internal/release | Verify | Verifying a release's key list and signed checksums | 190,500 | 53 |
| internal/core/store | GetUserByTelegramID | The lookup that starts every bot update (test Postgres) | 123,500 | 23 |
| internal/core/store | ListPlans | The Buy menu's plan list, 8 plans | 407,500 | 167 |
| internal/core/monitor | FoldRepeats | Folding 1,000 repeated log lines on the Monitoring page | 1,171,000 | 6,004 |
| internal/bot/i18n | Money (fa-IRT / en-IRT / fa-USDT) | A price as the bot writes it | 256 / 231 / 308 | 5 / 4 / 5 |
| internal/core/domain | TierPrice | A reseller's price for 3 plans | 35 | 0 |
| internal/core/domain | CashbackAmount | An order's cashback in 128-bit arithmetic | 1.5 | 0 |
| internal/payments/domain | TONNano | USDT to nanoton, rounded up, in big integers | 89 | 2 |

`ListPlans` makes several queries for 8 plans (0.4 ms against 0.12 ms for one user), and `FoldRepeats` spends about
1.2 µs per line, mostly in its two regular expressions. These are the numbers `-literals` could move most: the
license and release checks carry string constants, and the store's queries are long ones.

## What limits throughput now

1. **Core's event writes, one commit at a time** (limit 1 of [12-load-test.md](12-load-test.md)). Unchanged: the
   in-process ceiling is the same within the noise, at 2.7 CPU cores of 16. A slower disk lowers it in proportion;
   a busy shared Postgres here cut it to a quarter.
2. **The pool of 16 connections, full under stress**, with every request waiting (100%, 2–30 ms on average). Calls
   that take several connections one after the other wait each time, which is now what makes `GetWallet` (about 9
   round trips) and `CreateOrder` (the customer, the plan, the license state, then its transaction) the slowest
   calls at 100 users and more. Reading the settings once per call in `UsdtRateInfo` and its callers would make
   `GetWallet` a 2-query read again.
3. **Deliveries, one at a time**: 35–42 a second while draining a backlog here (40–53 on 2026-10-12), 6–21 a second
   while the stress load runs. In production each delivery also waits for the panel's HTTP calls.
4. In practice, as before, Telegram (about 30 messages a second per bot, about 4 journeys a second) and the panel
   limit a shop long before core does.

A store with a license that caps its active services (Standard: 500) takes one more lock and a count over the
subscriptions in every new order. A fresh install is on its 30-day trial, with no cap, which is what every run here
measured.

## How to run it

```sh
tools/bench/run.sh                                # everything for the plain build: about 4 hours here
tools/bench/run.sh build stack report             # some steps only
BENCH_REPS="1 2" tools/bench/run.sh stack         # run 1 and 2 again (outliers); newer results replace older
BENCH_QUICK=1 BENCH_OUT=/tmp/b tools/bench/run.sh # a few minutes: checks the script, measures nothing
```

It needs Go, Node and npm, `jq`, `redis-server` and `redis-cli`, the test Postgres (`BOBRES_TEST_DATABASE_URL` as for
`go test`, default the `/tmp` socket), and `gh` for the CI times. It starts its own Redis (default
`127.0.0.1:6391`) when nothing answers there, and stops it at the end. Logs, binaries and caches stay in
`BENCH_WORK` (`$TMPDIR/bobres-bench-<variant>`).

| Step | What it measures |
|---|---|
| `sys` | Machine, toolchains, Postgres settings, commit, load average |
| `loc` | Lines of code by kind, Go test functions |
| `web` | `npm ci`, then `npm run build` of both apps, 3 times; every bundle file raw and gzip -9 |
| `build` | Every binary for linux/amd64 as release.yml builds it, clean and warm, 3 times; then one build for this machine for `stack` |
| `stack` | The binaries as separate processes: first exec, start, idle, two load scenarios with their drains, restart, stop |
| `loadtest` | The in-process load test of 12-load-test.md: stress at 50/100/250/500 users for 30 s, 500 and 2,000 users 2 s apart for 60 s |
| `migrate` | All migrations on an empty database, then again on the migrated one |
| `tests` | `go test ./...` (default `-p`, and `-p 2`), CI's race run and its small-pool step, both apps' vitest |
| `gobench` | `go test -bench . -benchmem -count 5` on every package with a `Benchmark` function |
| `release` | The release's images on ghcr.io (manifests, anonymously) and the job times of the last green CI and Release runs |
| `report` | `summary.md` and `summary.json` from the raw results |

Settings: `BENCH_VARIANT` (the output directory's name, default `plain`), `BENCH_BUILD` (the build command used where
release.yml runs `go build`), `BENCH_REPEAT` (3), `BENCH_REPS`, `BENCH_DATE`, `BENCH_OUT`, `BENCH_WORK`,
`BENCH_REDIS`, `BENCH_RELEASE` (0.19.0), `BENCH_VERSION` (the version stamped in), `BENCH_STRESS_USERS`,
`BENCH_PACED_USERS`. The script's header explains each.

Results go to `docs/bench/<date>/<variant>/`: `system.json`, `timings.jsonl` (one line per timed command: wall and
CPU time, peak RSS, exit status, the load average before and after; a re-run replaces the line of the same label and
run), `loadtest/inproc-*.json` (`tools/loadtest -json`), `stack/stack-r*.json`, `sizes/`, `migrate.json`,
`loc.json`, `gobench.txt` and `.json`, `vitest/`, `ghcr.json`, `ci.json`, and `summary.md` and `summary.json`
written from them. The helper is `tools/bench/benchtool`, a Go program in the module (it uses the fakes in
`internal/`).

**Re-run outliers.** The summary's notes list every measurement whose runs spread more than 15%. Look at the load
average beside it; if it was high, run that step again with `BENCH_REPS`.

## How to compare after garble

Build the protected variant and measure it the same way, on the same machine, as quietly as possible:

```sh
BENCH_VARIANT=garble tools/bench/run.sh            # BENCH_BUILD defaults to "garble -literals -tiny build"
BENCH_VARIANT=garble BENCH_BUILD="garble -literals build" tools/bench/run.sh   # other flags: set BENCH_BUILD
"$TMPDIR/bobres-bench-garble/benchtool" compare -a docs/bench/2026-10-29/plain -b docs/bench/<date>/garble -out compare.md
```

For a variant other than `plain` the default steps are `sys web build stack release report`: the in-process load
test, the test suites, the migrations, the micro-benchmarks and the code size are built by `go test` from the
source, which garble does not touch. `compare` prints every metric of both runs with the change and marks the
changes larger than the noise of either run.

**What should change, and is fine within reason:**

- **Binary size.** `-tiny` drops file names, line numbers and panic messages, which shrinks a binary; `-literals`
  replaces every string literal with code that rebuilds it, which grows one. Expect a few percent either way, more
  for core, whose SQL is long string constants. The images grow or shrink by the same bytes.
- **Build time.** garble obfuscates every package, the standard library included, and keeps its own cache: expect
  the clean build to take several times longer; the warm one somewhat longer.
- **First exec and start.** A literal rebuilt at package initialisation runs once per start; expect at most a few
  milliseconds more to `/readyz`. The macOS first-exec scan depends on the binary's size.
- **CPU per call, a little.** With `-literals` each use of a string constant rebuilds it: every SQL query, every log
  message, every text key. Expect core's CPU under the stress load (2.7 cores here) to rise by a few percent; a
  rise beyond 10%, or a lower stress throughput on the stack, is worth a look at which literals sit on the hot path.
- **Panics and stack traces** become unreadable without garble's reverse mapping (`garble reverse`): keep the seed
  of each release.

**What must not change:**

- **Errors: 0** in both stack scenarios, and the in-process numbers (garble does not build them).
- **Stack throughput** at 200 users within the runs' spread (271–328 journeys a second here), and the paced
  scenario at 31 journeys a second with the deliveries keeping up: the limit is the database, not the code.
- **Memory:** idle and peak RSS within about 10%.
- **What the binaries do:** `--version` still prints v0.19.0 (garble keeps `-ldflags -X`), the tls-agent still
  starts from core's binary, migrations still run (they are embedded files, not literals), and the dashboard and
  the Mini App still load (embedded too).
- **The frontend bundles, the migrations and the database size**, which do not depend on how Go code is built.

## Notes and open points

**Not measured, or measured fewer times than planned** (the Mac restarted halfway and other sessions then loaded it
to a load average of 50–70; `tools/bench/run.sh` does all of it):

- The test suites 3 times each: `go test` default and `-p 2` ran twice, the race run once (it hit its timeout),
  CI's small-pool step and both vitest suites not at all in the full run (see Tests).
- In-process stress at 200 users (12-load-test.md had it; this run did 100 and 250 instead, and 200 on the
  separate binaries).
- Quiet repeats of the in-process stress at 50, 100 and 250 users: one quiet run each (two re-runs landed on a busy
  machine).
- The darwin/arm64 and linux/arm64 binaries (the images' arm64 sizes are in the release table); the vendor's
  `licensesrv`, which is not part of an install.
- RSS and CPU in Docker on Linux: the services ran natively on macOS.

- **Other sessions on the machine.** Up to about 20 of load average from other sessions' builds and test suites on
  the same Postgres, and, after the restart, Spotlight. The in-process stress runs that started busy were 2–4 times
  slower than the quiet ones; the summary lists them. Before the garble run, drop the abandoned `bobres_test_*`
  databases (570 here) when no test is running, and measure both variants on the same day.
- **The in-process `CreateOrder` errors** come from the load test's empty `core.install` table, not from a live
  store: migration 00012 inserts the row and nothing deletes it. Checked by hand: with the row deleted, 236 of 320
  concurrent `TrialStartedAt` calls (20 rounds of 16) failed with "install id: no rows in result set". The
  `INSERT … ON CONFLICT DO NOTHING` waits for the other inserter, then the `SELECT` in the same statement still sees
  the snapshot from before it. Re-reading the row in a second statement would fix it; or `internal/testenv` could
  leave `core.install` alone.
- **vitest** runs with `--testTimeout=30000`: on a busy machine a few dashboard tests take longer than vitest's
  default 5 s and would drop a run from the median.
- Not covered, as on 2026-10-12: the bot's own handling (Telegram, its Redis limiter), a real 3x-ui panel, Docker
  networking and the payments gateways. A run on a 2-vCPU Linux VPS with Docker is still the measurement that would
  say most about a real store.
