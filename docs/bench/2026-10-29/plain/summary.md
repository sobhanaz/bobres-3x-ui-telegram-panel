# BOBRES benchmark: v0.19.0-1-g9ea26b6, variant plain

Raw results are the JSON files next to this one; `tools/bench/benchtool report` wrote this summary and `compare` puts two variants side by side (docs/21-benchmark.md). Each timed number is the median of the runs, ± half their spread.

- Machine: Intel(R) Core(TM) i9-9980HK CPU @ 2.40GHz, 16 CPUs, 64 GB, macOS 26.6.2 (Darwin 25.6.0)
- Toolchains: go version go1.27.1 darwin/amd64; Node v24.11.1, npm 11.6.2
- Postgres: 16.15 (Homebrew) (max_connections 100, shared_buffers 128MB, synchronous_commit on, fsync on, wal_sync_method open_datasync)
- Code: v0.19.0-1-g9ea26b6 (9ea26b6f7436); build: `go build`
- Load average when the run started: [2.34 5 5.26]

## Load test, in-process (as in docs/12-load-test.md)

| Run | Users | Journeys/s | Calls/s | Journey p50 / p95 / p99 ms | Slowest call at p99 | Errors (all runs) | Pool acquires that waited | Waiting at the end, drained in | DB after | Load avg before |
|---|---:|---:|---:|---|---|---:|---|---|---:|---|
| stress | 50 | 171 (107–385) | 3071 (1920–6928) | 263 / 445 / 541 | GetWallet 110 ms | 40 (CreateOrder Internal 40) | 100%, 4.7 ms average | 4804 in 227 s | 41.6 MB | 6.1, 8.7, 2.1 |
| stress | 100 | 190 (124–379) | 3418 (2221–6821) | 481 / 777 / 938 | GetWallet 237 ms | 44 (CreateOrder Internal 44) | 100%, 10.6 ms average | 5457 in 231 s | 45.1 MB | 18.7, 10.4, 2.8 |
| stress | 250 | 98 (98–366) | 1757 (1756–6588) | 2296 / 3696 / 3838 | AdminAdjustBalance 756 ms | 45 (CreateOrder Internal 45) | 100%, 57.1 ms average | 2923 in 147 s | 32.3 MB | 9.9, 14.5, 2.3 |
| stress | 500 | 385 (361–448) | 6937 (6488–8058) | 1357 / 1413 / 1472 | CreateOrder 393 ms | 44 (CreateOrder Internal 44) | 100%, 30.2 ms average | 11734 in 335 s | 79.6 MB | 2.7, 2.8, 2.2 |
| paced | 500 | 31 (31–32) | 558 (557–572) | 14058 / 16622 / 17590 | AddTicketMessage 20.3 ms | 0 | 0%, 8.0 ms average | none | 26.7 MB | 3.0, 1.9, 2.0 |
| paced | 2000 | 122 (119–124) | 2192 (2136–2229) | 14049 / 16579 / 17550 | PayOrderWithWallet 20.3 ms | 0 | 11%, 0.4 ms average | 5797 in 136 s | 67.5 MB | 4.0, 2.5, 3.3 |

Quiet runs only (1-minute load average below 4 when the run started):

| Run | Users | Quiet runs | Journeys/s | Calls/s | Journey p50 / p95 / p99 ms |
|---|---:|---:|---:|---:|---|
| stress | 50 | 1 of 3 | 385 | 6928 | 133 / 142 / 190 |
| stress | 100 | 1 of 3 | 379 | 6821 | 257 / 292 / 341 |
| stress | 250 | 1 of 3 | 366 | 6588 | 675 / 737 / 812 |
| stress | 500 | 3 of 3 | 385 (361–448) | 6937 (6488–8058) | 1357 / 1413 / 1472 |
| paced | 500 | 3 of 3 | 31 (31–32) | 558 (557–572) | 14058 / 16622 / 17590 |
| paced | 2000 | 2 of 3 | 123 (122–124) | 2211 (2192–2229) | 14025 / 16568 / 17515 |

<details><summary>Per call: stress, 50 users (median of 3 runs)</summary>

| call | calls/s | p50 ms | p95 ms | p99 ms | max ms | errors |
|---|---:|---:|---:|---:|---:|---:|
| GetUser | 1364.9 | 3.8 | 20.8 | 24.2 | 189 | 0 |
| UpsertUser | 170.7 | 4.7 | 21.8 | 25.7 | 171 | 0 |
| AdminAdjustBalance | 170.7 | 37.5 | 53.4 | 96.4 | 239 | 0 |
| ListPlans | 512.2 | 11.8 | 31.7 | 39.8 | 223 | 0 |
| GetWallet | 170.7 | 47.7 | 79.6 | 110 | 268 | 0 |
| CreateOrder | 170.7 | 36.2 | 50.7 | 82.1 | 237 | 15 |
| PayOrderWithWallet | 170.2 | 30.5 | 43.7 | 61.5 | 233 | 0 |
| ListSubscriptions | 170.2 | 3.8 | 20.8 | 24.6 | 176 | 0 |
| AddTicketMessage | 170.2 | 33.4 | 47.0 | 73.7 | 233 | 0 |

</details>

<details><summary>Per call: stress, 100 users (median of 3 runs)</summary>

| call | calls/s | p50 ms | p95 ms | p99 ms | max ms | errors |
|---|---:|---:|---:|---:|---:|---:|
| GetUser | 1519.3 | 8.7 | 28.6 | 37.7 | 127 | 0 |
| UpsertUser | 190.0 | 10.1 | 29.7 | 37.1 | 123 | 0 |
| AdminAdjustBalance | 190.0 | 52.2 | 94.1 | 141 | 190 | 0 |
| ListPlans | 570.1 | 32.5 | 53.7 | 83.2 | 175 | 0 |
| GetWallet | 190.0 | 86.7 | 175 | 237 | 315 | 0 |
| CreateOrder | 190.0 | 63.7 | 113 | 165 | 224 | 15 |
| PayOrderWithWallet | 189.6 | 34.3 | 51.0 | 85.1 | 169 | 0 |
| ListSubscriptions | 189.6 | 9.3 | 29.0 | 34.9 | 123 | 0 |
| AddTicketMessage | 189.6 | 44.3 | 74.0 | 98.5 | 182 | 0 |

</details>

<details><summary>Per call: stress, 250 users (median of 3 runs)</summary>

| call | calls/s | p50 ms | p95 ms | p99 ms | max ms | errors |
|---|---:|---:|---:|---:|---:|---:|
| GetUser | 781.0 | 58.8 | 225 | 377 | 488 | 0 |
| UpsertUser | 97.8 | 61.2 | 200 | 402 | 491 | 0 |
| AdminAdjustBalance | 97.8 | 269 | 493 | 756 | 892 | 0 |
| ListPlans | 293.3 | 96.1 | 258 | 397 | 886 | 0 |
| GetWallet | 97.8 | 223 | 394 | 571 | 749 | 0 |
| CreateOrder | 97.8 | 245 | 649 | 735 | 953 | 15 |
| PayOrderWithWallet | 97.3 | 128 | 336 | 423 | 495 | 0 |
| ListSubscriptions | 97.3 | 64.9 | 159 | 303 | 487 | 0 |
| AddTicketMessage | 97.3 | 195 | 364 | 692 | 888 | 0 |

</details>

<details><summary>Per call: stress, 500 users (median of 3 runs)</summary>

| call | calls/s | p50 ms | p95 ms | p99 ms | max ms | errors |
|---|---:|---:|---:|---:|---:|---:|
| GetUser | 3083.0 | 40.1 | 110 | 162 | 248 | 0 |
| UpsertUser | 385.5 | 44.6 | 88.2 | 124 | 208 | 0 |
| AdminAdjustBalance | 385.5 | 149 | 221 | 245 | 295 | 0 |
| ListPlans | 1156.5 | 49.2 | 148 | 190 | 272 | 0 |
| GetWallet | 385.5 | 124 | 202 | 300 | 528 | 0 |
| CreateOrder | 385.5 | 146 | 348 | 393 | 462 | 15 |
| PayOrderWithWallet | 385.0 | 64.2 | 163 | 214 | 273 | 0 |
| ListSubscriptions | 385.0 | 40.3 | 85.9 | 137 | 206 | 0 |
| AddTicketMessage | 385.0 | 109 | 191 | 217 | 258 | 0 |

</details>

<details><summary>Per call: paced, 500 users (median of 3 runs)</summary>

| call | calls/s | p50 ms | p95 ms | p99 ms | max ms | errors |
|---|---:|---:|---:|---:|---:|---:|
| GetUser | 248.2 | 0.5 | 14.6 | 17.0 | 18.9 | 0 |
| UpsertUser | 31.0 | 1.0 | 1.9 | 14.8 | 18.4 | 0 |
| AdminAdjustBalance | 31.0 | 2.8 | 12.8 | 20.2 | 50.8 | 0 |
| ListPlans | 93.1 | 0.8 | 1.8 | 14.9 | 19.6 | 0 |
| GetWallet | 31.0 | 1.1 | 2.4 | 18.4 | 20.0 | 0 |
| CreateOrder | 31.0 | 1.4 | 3.2 | 18.9 | 21.1 | 0 |
| PayOrderWithWallet | 31.0 | 1.7 | 3.8 | 19.9 | 44.1 | 0 |
| ListSubscriptions | 31.0 | 0.4 | 1.1 | 1.7 | 17.5 | 0 |
| AddTicketMessage | 31.0 | 2.3 | 5.0 | 20.3 | 50.3 | 0 |

</details>

<details><summary>Per call: paced, 2000 users (median of 3 runs)</summary>

| call | calls/s | p50 ms | p95 ms | p99 ms | max ms | errors |
|---|---:|---:|---:|---:|---:|---:|
| GetUser | 974.3 | 0.3 | 14.4 | 16.7 | 32.2 | 0 |
| UpsertUser | 121.8 | 0.7 | 1.8 | 17.1 | 22.7 | 0 |
| AdminAdjustBalance | 121.8 | 2.1 | 8.0 | 19.9 | 51.5 | 0 |
| ListPlans | 365.3 | 0.6 | 2.0 | 5.6 | 36.7 | 0 |
| GetWallet | 121.8 | 1.1 | 3.3 | 18.1 | 61.6 | 0 |
| CreateOrder | 121.8 | 1.5 | 4.0 | 18.8 | 60.3 | 0 |
| PayOrderWithWallet | 121.8 | 2.1 | 6.8 | 20.3 | 71.8 | 0 |
| ListSubscriptions | 121.8 | 0.3 | 1.0 | 2.7 | 32.3 | 0 |
| AddTicketMessage | 121.8 | 1.9 | 6.5 | 19.2 | 107 | 0 |

</details>

## The service binaries as separate processes (3 runs)

Each run: a fresh database, the services started one after the other (cold: the database is empty, so each runs its migrations), an idle period (RSS idle is its median, CPU idle its average), then the load scenarios below one after the other on the same stack, each followed by the delivery drain, then every service restarted once on the loaded database (warm) and stopped. Ready is the time from exec to `/readyz` answering 200 (the tls-agent: its first heartbeat). RSS is sampled with ps; peak RSS and shutdown are from the stop after the load.

First exec is the binary's first `--version` in run 1, just after the build (macOS scans a new executable then; later runs are much faster), so it is not counted in the starts.

| Service | First exec | Ready, cold | Ready, warm | RSS idle | CPU idle | Shutdown | Peak RSS (rusage) |
|---|---:|---:|---:|---:|---:|---:|---:|
| payments | 839 ms | 242 (±34%) ms | 71 (±40%) ms | 18.0 (±1%) MB | 0.15% (±17%) | 3 (±22%) ms | 20.9 (±1%) MB |
| provisioner | 823 ms | 127 (±22%) ms | 64 (±21%) ms | 16.9 (±0%) MB | 0.00% (±0%) | 5 (±84%) ms | 37.2 (±2%) MB |
| core | 1043 ms | 688 (±6%) ms | 98 (±42%) ms | 32.8 (±0%) MB | 0.35% (±7%) | 9 (±42%) ms | 62.3 (±8%) MB |
| bot | 899 ms | 43 (±4%) ms | 63 (±34%) ms | 20.8 (±0%) MB | 0.15% (±0%) | 9 (±46%) ms | 31.2 (±1%) MB |
| backup | 725 ms | 21 (±7%) ms | 24 (±35%) ms | 9.7 (±1%) MB | 0.00% (±0%) | 9 (±81%) ms | 9.8 (±1%) MB |
| tls-agent | – | 30 (±8%) ms | 32 (±40%) ms | 17.7 (±1%) MB | 0.05% (±0%) | 5 (±50%) ms | 18.1 (±1%) MB |

### stress: 200 users for 30 s

327 (271–328) journeys/s, 5878 (4874–5905) calls/s, journey p95 673 ms, errors 0 (worst run); 9595 orders waiting at the end, delivered in 233 s.

| Service | CPU under load (cores) | RSS under load, mean / max | CPU in the drain (cores) | RSS in the drain, max |
|---|---:|---:|---:|---:|
| payments | 0.00 (±17%) | 18.4 / 18.5 (±1%) MB | 0.00 (±23%) | 20.5 MB |
| provisioner | 0.14 (±4%) | 30.6 / 31.7 (±1%) MB | 0.63 (±5%) | 36.7 MB |
| core | 2.72 (±7%) | 57.6 / 59.3 (±8%) MB | 0.20 (±10%) | 62.2 MB |
| bot | 0.04 (±4%) | 24.3 / 25.4 (±3%) MB | 0.10 (±15%) | 30.7 MB |
| backup | 0.00 (±0%) | 9.7 / 9.7 (±1%) MB | 0.00 (±0%) | 9.8 MB |
| tls-agent | 0.00 (±1%) | 17.7 / 17.7 (±1%) MB | 0.00 (±37%) | 18.0 MB |

### paced: 500 users for 60 s, 2 s between updates

31 (31–31) journeys/s, 561 (559–566) calls/s, journey p95 16626 ms, errors 0 (worst run); 0 orders waiting at the end, delivered in 0 s.

| Service | CPU under load (cores) | RSS under load, mean / max | CPU in the drain (cores) | RSS in the drain, max |
|---|---:|---:|---:|---:|
| payments | 0.00 (±27%) | 20.7 / 20.8 (±1%) MB | 0.00 (±0%) | 20.8 MB |
| provisioner | 0.61 (±23%) | 36.9 / 37.2 (±2%) MB | 0.00 (±0%) | 37.2 MB |
| core | 0.59 (±20%) | 62.2 / 62.2 (±8%) MB | 0.00 (±0%) | 62.2 MB |
| bot | 0.11 (±27%) | 30.7 / 31.2 (±1%) MB | 0.00 (±0%) | 31.2 MB |
| backup | 0.00 (±0%) | 9.8 / 9.8 (±2%) MB | 0.00 (±0%) | 9.8 MB |
| tls-agent | 0.00 (±16%) | 18.1 / 18.1 (±1%) MB | 0.00 (±0%) | 18.1 MB |

Database after all scenarios: 82.1 MB for 12326 customers.

## Binaries

Built as release.yml builds them: `CGO_ENABLED=0 <build> -trimpath -ldflags "-s -w -X ...version..."`, with the dashboard and the Mini App built first (core embeds them). Clean: an empty GOCACHE; warm: the same build again with that cache.

| Target | Binary | Size | Build, clean | Build, warm |
|---|---|---:|---:|---:|
| darwin-amd64 | backup | 14.46 MB | – | – |
| darwin-amd64 | bobres | 10.17 MB | – | – |
| darwin-amd64 | bot | 25.89 MB | – | – |
| darwin-amd64 | core | 39.21 MB | – | – |
| darwin-amd64 | payments | 19.71 MB | – | – |
| darwin-amd64 | provisioner | 19.44 MB | – | – |
| linux-amd64 | backup | 14.07 MB | 15.2 s (±7%) | 1.0 s (±1%) |
| linux-amd64 | bobres | 9.97 MB | 13.8 s (±1%) | 0.8 s (±8%) |
| linux-amd64 | bot | 25.29 MB | 26.5 s (±5%) | 1.5 s (±6%) |
| linux-amd64 | core | 38.36 MB | 28.5 s (±23%) | 1.7 s (±20%) |
| linux-amd64 | payments | 19.17 MB | 18.6 s (±9%) | 1.2 s (±4%) |
| linux-amd64 | provisioner | 18.90 MB | 17.7 s (±5%) | 1.2 s (±10%) |

## Database migrations

All migrations on an empty database, 3 runs, each on a fresh database: 1.03 (±3%) s (core 0.71 s, payments 0.20 s, provisioner 0.09 s); running them again on the migrated database (what every restart does): 0.050 (±0%) s. Migration files: core 48, payments 10, provisioner 7.

## Tests

| Suite | Wall time | CPU (user+sys) | Failed runs | Load avg before |
|---|---:|---:|---:|---|
| `go test -count=1 ./...` (default -p) | 482.4 s | 296 s | 1 of 2 | 2.4, 9.3 |
| `go test -count=1 -p 2 ./...` | 797.5 s (±28%) | 165 s | 0 of 2 | 4.3, 4.3 |
| `go test -race -count=1 -timeout 20m ./...`, as CI's Test (race) job (without its coverage profile) | – | 571 s | 1 of 1 | 70.9 |

Go test functions (top level): 2985 Test, 11 Benchmark, 0 Fuzz, 0 Example.

## Frontend bundles

### dashboard: 4.7 MB raw, 2.4 MB gzip, 213 files; `npm run build` 18.1 s (±8%)

<details><summary>Per file (largest first)</summary>

| File | Raw KB | Gzip KB |
|---|---:|---:|
| assets/index-D6whDqgy.js | 781.9 | 198.6 |
| assets/primeicons-Dr5RGzOO.svg | 342.5 | 102.6 |
| assets/bobres-wave-damZrktf.webp | 301.1 | 299.9 |
| assets/column-Dd32ejhd.js | 283.9 | 59.7 |
| assets/SystemPage-BYW3GobZ.js | 179.2 | 47.6 |
| assets/_plugin-vue_export-helper-B-H40yVN.js | 124.7 | 44.3 |
| assets/primeicons-DMOk5skT.eot | 85.2 | 40.8 |
| assets/primeicons-WjwUDZjB.woff | 85.1 | 40.7 |
| assets/primeicons-MpK4pl85.ttf | 85.0 | 40.7 |
| assets/BrandingPage-BOs0ZB-E.js | 75.6 | 20.8 |
| assets/PlansPage-GFZOcLOL.js | 72.4 | 17.0 |
| assets/select-SYGOD7fA.js | 69.5 | 17.1 |
| assets/format-Cu0VNzcW.js | 63.8 | 19.4 |
| assets/button-C1SHSFm4.js | 52.6 | 10.3 |
| assets/GatewaysPage-DX7Xux38.js | 49.7 | 11.2 |
| assets/inter-latin-ext-700-normal-TidjK2hL.woff | 48.6 | 48.5 |
| assets/inter-latin-ext-500-normal-BxGbmqWO.woff | 48.5 | 48.4 |
| assets/inter-latin-ext-400-normal-77YHD8bZ.woff | 47.6 | 47.5 |
| assets/ReportsPage-DRAJ0alP.js | 44.5 | 13.1 |
| assets/inter-latin-ext-700-normal-Ca8adRJv.woff2 | 36.2 | 36.3 |
| assets/charts-D5Ka_x0q.js | 36.2 | 11.2 |
| assets/inter-latin-ext-500-normal-CV4jyFjo.woff2 | 36.0 | 36.1 |
| assets/primeicons-C6QP2o4f.woff2 | 35.1 | 35.2 |
| assets/inter-latin-ext-400-normal-C1nco2VV.woff2 | 35.0 | 35.0 |
| assets/SettingsPage-BeEPdU5q.js | 34.5 | 9.7 |
| assets/PaymentsPage-D1HvaJeF.js | 34.3 | 10.4 |
| apple-touch-icon.png | 32.8 | 32.8 |
| assets/SystemPage-CdZWo2v9.css | 32.3 | 5.2 |
| assets/inter-latin-700-normal-BLAVimhd.woff | 31.3 | 31.2 |
| assets/inter-latin-500-normal-BL9OpVg8.woff | 31.3 | 31.1 |
| assets/inter-latin-400-normal-CyCys3Eg.woff | 30.7 | 30.6 |
| assets/index-BRp8h_5F.css | 30.6 | 6.3 |
| assets/listbox-egL8JpiU.js | 28.4 | 7.0 |
| assets/vazirmatn-arabic-500-normal-Dqq3-xo3.woff | 28.0 | 27.8 |
| assets/vazirmatn-arabic-700-normal-B5nPuCFv.woff | 27.9 | 27.7 |
| assets/vazirmatn-arabic-400-normal-C4W5XURk.woff | 27.3 | 27.1 |
| assets/LicensesPage-DhOJgc4a.js | 26.4 | 6.5 |
| assets/inter-latin-700-normal-Yt3aPRUw.woff2 | 24.4 | 24.3 |
| assets/inter-latin-500-normal-Cerq10X2.woff2 | 24.3 | 24.3 |
| assets/ResellerPage-3pTWSdPO.js | 24.0 | 6.9 |
| assets/inter-latin-400-normal-C38fXH4l.woff2 | 23.7 | 23.7 |
| assets/bobres-mascot-DrliBV4p.webp | 23.5 | 23.5 |
| assets/GiftCodesPage-Drp6CBZ-.js | 22.5 | 6.8 |
| assets/message-ymTqy8r_.js | 22.2 | 4.6 |
| assets/vazirmatn-arabic-500-normal-C_lbnnKa.woff2 | 21.8 | 21.8 |
| assets/vazirmatn-arabic-700-normal-Dge_DOjm.woff2 | 21.7 | 21.7 |
| assets/BroadcastEditPage-s8bVstyp.js | 21.7 | 7.0 |
| assets/ServersPage-CJ6w-UjW.js | 21.5 | 6.3 |
| assets/vazirmatn-arabic-400-normal-DMZFCm7K.woff2 | 21.1 | 21.1 |
| assets/vazirmatn-latin-700-normal-DrB0PBU6.woff | 20.9 | 20.9 |
| assets/vazirmatn-latin-500-normal-Bg_BALlD.woff | 20.9 | 20.9 |
| assets/vazirmatn-latin-400-normal-GKyMcI03.woff | 20.8 | 20.8 |
| assets/password-CNLgfsu3.js | 18.4 | 6.2 |
| assets/SettingsCard-BVH28aW2.js | 18.2 | 5.4 |
| favicon.svg | 17.5 | 6.6 |
| assets/SupplierPage-CFnAU8t7.js | 17.0 | 5.1 |
| assets/TicketPage-DeeAMwUI.js | 16.7 | 6.0 |
| assets/vazirmatn-latin-700-normal-9BlbvDRV.woff2 | 16.4 | 16.4 |
| assets/tab-CG5n0Sev.js | 16.4 | 4.9 |
| assets/vazirmatn-latin-500-normal-6zZzgpg4.woff2 | 16.4 | 16.4 |
| assets/vazirmatn-latin-400-normal-BT_DHTc7.woff2 | 16.1 | 16.1 |
| assets/UserPage-DCZnT_qt.js | 15.6 | 4.7 |
| assets/BroadcastPage-BsXDQiwe.js | 14.4 | 4.6 |
| assets/ResellersPage-CdHT-Mc8.js | 14.4 | 4.6 |
| assets/StaffPage-D61a39jR.js | 14.2 | 4.8 |
| assets/vazirmatn-latin-ext-500-normal-4XgegWYb.woff | 14.1 | 14.1 |
| assets/vazirmatn-latin-ext-700-normal-DMYLqBto.woff | 14.0 | 14.0 |
| assets/vazirmatn-latin-ext-400-normal-DPxMaNjI.woff | 14.0 | 14.0 |
| assets/runtime-dom.esm-bundler-Byh23fMS.js | 13.9 | 6.0 |
| assets/ProfessionalReport-Cjk_ks7A.js | 13.7 | 4.5 |
| assets/inter-cyrillic-ext-500-normal-BmqWE9Dz.woff | 13.5 | 13.5 |
| assets/inter-cyrillic-ext-700-normal-LO58E6JB.woff | 13.4 | 13.4 |
| assets/inter-cyrillic-ext-400-normal-DQukG94-.woff | 13.3 | 13.3 |
| assets/selectbutton-BxiFPUaC.js | 13.0 | 3.7 |
| assets/gateways-v3w4BKmN.js | 12.3 | 4.6 |
| assets/LoginPage-F3cyby3n.js | 12.2 | 4.2 |
| assets/checkbox-iydkof7s.js | 12.0 | 3.4 |
| assets/BrandingPage-DOhsGeeQ.css | 11.0 | 2.3 |
| assets/SupportPage-DvXIyyIn.js | 11.0 | 3.9 |
| assets/ServicePage-GNCZEt0B.js | 10.6 | 3.4 |
| assets/radiobutton-CvxfVbZj.js | 10.6 | 3.2 |
| assets/inter-cyrillic-ext-700-normal-BjwYoWNd.woff2 | 10.5 | 10.5 |
| assets/vazirmatn-latin-ext-700-normal-BfoXmNMx.woff2 | 10.4 | 10.5 |
| assets/inter-cyrillic-ext-500-normal-B0yAr1jD.woff2 | 10.4 | 10.5 |
| assets/vazirmatn-latin-ext-500-normal-CgxvvVrG.woff2 | 10.4 | 10.4 |
| assets/vazirmatn-latin-ext-400-normal-BdGhO0lm.woff2 | 10.2 | 10.3 |
| assets/inter-cyrillic-ext-400-normal-BQZuk6qB.woff2 | 10.2 | 10.3 |
| assets/inter-greek-700-normal-BUv2fZ6O.woff | 10.0 | 10.0 |
| assets/inter-greek-500-normal-Xzm54t5V.woff | 10.0 | 10.0 |
| assets/inter-cyrillic-500-normal-CxZf_p3X.woff | 9.9 | 9.9 |
| assets/inter-greek-400-normal-q2sYcFCs.woff | 9.9 | 9.9 |
| assets/inter-cyrillic-700-normal-DrXBdSj3.woff | 9.9 | 9.9 |
| assets/inter-cyrillic-400-normal-HOLc17fK.woff | 9.8 | 9.8 |
| assets/AccountPage-BaFFTCAI.js | 9.7 | 3.1 |
| assets/OverviewPage-jTgHR2tg.js | 9.5 | 2.9 |
| assets/DiscountsPage-C6W7hv53.js | 9.2 | 3.0 |
| assets/IsoColumns-CMEeSGM1.js | 8.7 | 3.8 |
| assets/GatewaysPage-BNUGcClO.css | 8.4 | 1.5 |
| assets/charts-DNY2N9dX.css | 8.3 | 1.8 |
| assets/settings-Dy-1a4kH.js | 8.0 | 3.2 |
| assets/inter-greek-700-normal-C3JjAnD8.woff2 | 7.9 | 7.9 |
| assets/inter-greek-500-normal-BIZE56-Y.woff2 | 7.9 | 7.9 |
| assets/inter-cyrillic-700-normal-CjBOestx.woff2 | 7.9 | 7.9 |
| assets/inter-cyrillic-500-normal-BasfLYem.woff2 | 7.9 | 7.9 |
| assets/inter-greek-400-normal-B4URO6DV.woff2 | 7.8 | 7.8 |
| assets/inter-cyrillic-400-normal-obahsSVq.woff2 | 7.7 | 7.7 |
| assets/OrderPage-cswzSnkw.js | 7.3 | 2.5 |
| assets/toggleswitch-D65yS717.js | 7.3 | 1.8 |
| assets/inter-greek-ext-700-normal-BoQ6DsYi.woff | 7.2 | 7.1 |
| assets/inter-greek-ext-500-normal-2j5mBUwD.woff | 7.2 | 7.1 |
| assets/inter-greek-ext-400-normal-KugGGMne.woff | 7.1 | 7.0 |
| assets/AuditPage-CVDc6P9x.js | 6.9 | 2.7 |
| assets/TiltDonut-DGEm0nOf.js | 6.9 | 2.9 |
| assets/inter-vietnamese-700-normal-BZaoP0fm.woff | 6.6 | 6.6 |
| assets/inter-vietnamese-500-normal-mJboJaSs.woff | 6.6 | 6.6 |
| assets/inter-vietnamese-400-normal-Bbgyi5SW.woff | 6.5 | 6.5 |
| assets/SettingsPage-CiX-Smur.css | 6.0 | 1.4 |
| assets/MakeResellerDialog-DhIIRvwG.js | 5.8 | 2.4 |
| assets/BroadcastStatus-CYVWryg6.js | 5.7 | 2.5 |
| assets/BroadcastsPage-CKlWr-fD.js | 5.4 | 2.2 |
| assets/inter-greek-ext-700-normal-qfdV9bQt.woff2 | 5.4 | 5.5 |
| assets/inter-greek-ext-500-normal-C4iEst2y.woff2 | 5.4 | 5.5 |
| assets/TicketPage-DualJfVW.css | 5.3 | 1.4 |
| assets/inter-greek-ext-400-normal-DGGRlc-M.woff2 | 5.3 | 5.3 |
| assets/SendDialog-DsbVBW-W.js | 5.3 | 2.2 |
| assets/textarea-D4K9XVVI.js | 5.2 | 1.7 |
| assets/inter-vietnamese-500-normal-DOriooB6.woff2 | 5.1 | 5.1 |
| assets/inter-vietnamese-700-normal-DlLaEgI2.woff2 | 5.1 | 5.1 |
| assets/BroadcastEditPage-B5lu4Vih.css | 5.0 | 1.2 |
| assets/ReportsPage-TscCdDob.css | 5.0 | 1.3 |
| assets/inter-vietnamese-400-normal-DMkecbls.woff2 | 5.0 | 5.0 |
| assets/progressbar-Dz5FJFyM.js | 5.0 | 1.3 |
| assets/api-CVYMcIhV.js | 4.9 | 2.5 |
| assets/inputotp-BOlEcMpd.js | 4.5 | 1.8 |
| assets/resellers-C7BK0omz.js | 4.4 | 1.8 |
| assets/servers-CIZYIRyO.js | 4.1 | 1.7 |
| assets/SecretField-BLI0OOvo.js | 3.8 | 1.6 |
| assets/chip-DtCHoG7E.js | 3.7 | 1.3 |
| assets/LedgerPage-BDUaIWL2.js | 3.5 | 1.5 |
| assets/ProfessionalReport-CZzZcsB1.css | 3.5 | 1.0 |
| assets/TicketsTable-vQMObpdq.js | 3.4 | 1.5 |
| assets/PaymentsTable-DKe6F1L_.js | 3.4 | 1.4 |
| assets/SupplierBalance-DKQaY6XW.js | 3.4 | 1.8 |
| assets/projection-9L5CP3LB.js | 3.1 | 1.2 |
| assets/SettingsCard-CeI3ul-k.css | 3.1 | 0.8 |
| assets/skeleton-BGx7Lv6e.js | 3.1 | 1.2 |
| assets/staff-CTNM-Mfr.js | 3.1 | 1.2 |
| assets/BroadcastPage-USlD7cUD.css | 2.9 | 0.9 |
| assets/ServicesTable-CBipEYLi.js | 2.8 | 1.3 |
| assets/eye-BR0-Vlr8.js | 2.8 | 1.5 |
| assets/LoginPage-HSDdSYA3.css | 2.7 | 0.9 |
| assets/ServicesPage-CMb89WhT.js | 2.5 | 1.2 |
| assets/TiltDonut-MujgEgyC.css | 2.4 | 0.9 |
| assets/OrdersTable-CqfDmCkd.js | 2.4 | 1.1 |
| assets/UsersPage-8jg24uyy.js | 2.4 | 1.0 |
| assets/LedgerTable-BGxk8nu8.js | 2.3 | 1.1 |
| assets/SessionList-BSbM4LLO.js | 2.3 | 1.2 |
| assets/UsersTable-CmeS8gCn.js | 2.2 | 1.0 |
| assets/tabpanel-B6jnnAmO.js | 2.2 | 0.9 |
| assets/ReasonDialog-BzR4PniN.js | 2.1 | 1.1 |
| assets/card-vYM-0pzo.js | 2.1 | 0.7 |
| assets/ServersPage-DdJJKbkZ.css | 1.8 | 0.6 |
| assets/IsoColumns-DUwu-08w.css | 1.7 | 0.7 |
| assets/TextPreview-B5PdmvfA.css | 1.7 | 0.6 |
| assets/reports-CZBzV7CV.js | 1.6 | 0.8 |
| assets/SupplierPage-CbGtQwuf.css | 1.6 | 0.6 |
| assets/ResellerPage-CTMvL6_5.css | 1.5 | 0.5 |
| assets/AccountPage-BgBgCmQv.css | 1.5 | 0.6 |
| assets/LicensesPage-DvcbXsNT.css | 1.4 | 0.5 |
| assets/GiftCodesPage-CGSiDI4o.css | 1.3 | 0.5 |
| assets/StaffPage-CZehDvLE.css | 1.1 | 0.4 |
| assets/OverviewPage-DdDHu5wH.css | 1.1 | 0.4 |
| index.html | 1.1 | 0.5 |
| assets/errors-AWC7MK6E.js | 1.1 | 0.5 |
| assets/PlansPage-DGLXOIy2.css | 1.1 | 0.4 |
| assets/ProblemList-6fH3fnBY.js | 1.0 | 0.7 |
| assets/StatusTag-DIercBmN.js | 1.0 | 0.5 |
| assets/SupportPage-BAUBfAHT.css | 1.0 | 0.4 |
| assets/SectionPlaceholder-B0ELpzLY.js | 1.0 | 0.6 |
| assets/PaymentsPage-Cp-wn3Mh.css | 1.0 | 0.4 |
| assets/csv-B9g80d-R.js | 0.9 | 0.6 |
| assets/TrafficBar-DDQzsauB.js | 0.9 | 0.6 |
| assets/TicketsTable-CfBa2VKb.css | 0.9 | 0.4 |
| assets/TextPreview-EdumoB40.js | 0.9 | 0.5 |
| assets/UserLink-5mIjcHj0.js | 0.8 | 0.5 |
| assets/BroadcastsPage-CiDVH34a.css | 0.8 | 0.4 |
| assets/useList-DRbcdyxT.js | 0.7 | 0.5 |
| assets/ResellersPage-DRfFUYal.css | 0.7 | 0.3 |
| assets/AuditPage-C98RngjX.css | 0.7 | 0.4 |
| assets/SessionList-bvVmXabW.css | 0.7 | 0.3 |
| assets/SendDialog-DhwY-TXI.css | 0.7 | 0.3 |
| assets/DiscountsPage-CXexmwl4.css | 0.6 | 0.3 |
| assets/BalanceText-DR8kbLtl.js | 0.6 | 0.4 |
| assets/TicketStatusTag-Dr8SEEA9.js | 0.5 | 0.3 |
| assets/SecretField-CwMc9mI2.css | 0.5 | 0.3 |
| assets/UserPage-D2n9Ucev.css | 0.5 | 0.3 |
| assets/ServicePage-vSuijhan.css | 0.4 | 0.2 |
| assets/ProblemList-B8N6S1cm.css | 0.4 | 0.2 |
| assets/OrderPage-8zPUcE8z.css | 0.4 | 0.2 |
| assets/TrafficBar-CKB3HPgC.css | 0.3 | 0.2 |
| assets/SupplierBalance-CNSiwg8E.css | 0.3 | 0.2 |
| assets/MakeResellerDialog-BJbjMICV.css | 0.3 | 0.2 |
| assets/ReasonDialog-BxjeHuvu.css | 0.3 | 0.2 |
| assets/SectionPlaceholder-G-O3B6cC.css | 0.2 | 0.2 |
| assets/UserLink-0K1EUE23.css | 0.2 | 0.2 |
| assets/ServicesTable-BBZZHT5Z.css | 0.2 | 0.1 |
| assets/PaymentsTable-y9HjU4PY.css | 0.1 | 0.1 |
| assets/LedgerPage-7UPEng-N.css | 0.1 | 0.1 |
| assets/OrdersTable-Ct8DM2w5.css | 0.1 | 0.1 |
| assets/ServicesPage-A-Pd8qTY.css | 0.1 | 0.1 |
| assets/LedgerTable--XKI2kCn.css | 0.1 | 0.1 |
| assets/UsersTable-C0uPsQgd.css | 0.1 | 0.1 |
| assets/BalanceText-GYIy1JAE.css | 0.0 | 0.1 |

</details>

### miniapp: 0.3 MB raw, 0.1 MB gzip, 4 files; `npm run build` 5.2 s (±25%)

<details><summary>Per file (largest first)</summary>

| File | Raw KB | Gzip KB |
|---|---:|---:|
| assets/index-BDRvfCyi.js | 236.7 | 71.5 |
| favicon.svg | 17.5 | 6.6 |
| assets/index-D7MZvKn0.css | 8.6 | 2.5 |
| index.html | 0.7 | 0.4 |

</details>

## Release artifacts

Images of 0.19.0 on ghcr.io (compressed: the download), read 2026-10-30T15:23:07Z:

| Image | linux/amd64 | linux/arm64 | Layers | Note |
|---|---:|---:|---:|---|
| core | 14.0 MB | 12.8 MB | 8 |  |
| bot | 8.8 MB | 7.9 MB | 8 |  |
| payments | 7.1 MB | 6.5 MB | 8 |  |
| provisioner | 7.0 MB | 6.4 MB | 8 |  |
| backup | 121.4 MB | 118.8 MB | 13 |  |

CI run 40933572561 (main): 11.5 min from the first job's start to the last one's end.

| Job | Result | Minutes |
|---|---|---:|
| Test (race) | success | 11.5 |
| Vulnerability scan | success | 0.3 |
| Dashboard (types, tests, build) | success | 3.1 |
| Shell lint + signing tests | success | 1.3 |
| Build binaries | success | 1.1 |
| Mini App (types, tests, build) | success | 0.7 |
| Secret scan | success | 0.2 |
| 3x-ui integration (v3.8.5) | success | 1.0 |
| 3x-ui integration (v3.10.2) | success | 1.1 |
| Lint | success | 2.5 |
| Docker images + compose + e2e install | success | 4.9 |
| Marzban integration | success | 3.6 |
| 3x-ui integration (v3.10.0) | success | 0.9 |
| 3x-ui integration (v3.9.0) | success | 1.1 |

Release run 40906669599 (v0.19.0): 21.5 min from the first job's start to the last one's end.

| Job | Result | Minutes |
|---|---|---:|
| Verify before publishing | success | 9.7 |
| Image core | success | 7.0 |
| Image bot | success | 10.5 |
| Image payments | success | 8.3 |
| Image provisioner | success | 6.3 |
| Image backup | success | 6.8 |
| CLI binaries + release | success | 1.3 |

## Code size

| Kind | Files | Lines | Non-blank |
|---|---:|---:|---:|
| go | 494 | 140077 | 130708 |
| go_test | 518 | 153999 | 145148 |
| go_generated | 14 | 26070 | 23119 |
| proto | 9 | 2488 | 2195 |
| ts | 95 | 13833 | 12428 |
| ts_test | 80 | 29021 | 27284 |
| vue | 180 | 37372 | 35678 |
| sql_migrations | 65 | 2727 | 2552 |
| shell | 15 | 2239 | 2045 |
| docs | 87 | 18870 | 15715 |
| docs_wiki | 39 | 3416 | 2241 |

## Go micro-benchmarks

`go test -run '^$' -bench . -benchmem -count N` on the packages that have them; ns/op is the median of the N runs.

| Package | Benchmark | ns/op | B/op | allocs/op |
|---|---|---:|---:|---:|
| internal/bot/i18n | Money/en-IRT | 231.1 (±2%) | 48 | 4 |
| internal/bot/i18n | Money/fa-IRT | 255.7 (±1%) | 88 | 5 |
| internal/bot/i18n | Money/fa-USDT | 308.2 (±2%) | 48 | 5 |
| internal/core/domain | CashbackAmount | 1.47 (±1%) | 0 | 0 |
| internal/core/domain | TierPrice | 35.45 (±2%) | 0 | 0 |
| internal/core/monitor | FoldRepeats | 1.171e+06 (±3%) | 340102 | 6004 |
| internal/core/store | GetUserByTelegramID | 1.235e+05 (±20%) | 1332 | 23 |
| internal/core/store | ListPlans | 4.075e+05 (±3%) | 15133 | 167 |
| internal/license | AdminVerify | 5.975e+04 (±1%) | 496 | 6 |
| internal/license | Sign | 2.931e+04 (±1%) | 3203 | 8 |
| internal/license | Verify | 6.512e+04 (±2%) | 954 | 10 |
| internal/payments/domain | TONNano | 88.56 (±0%) | 56 | 2 |
| internal/release | Verify | 1.905e+05 (±9%) | 6472 | 53 |

## Notes

- in-process stress-50 journeys/s: the 3 runs spread 163% (106.8 to 385): re-run it if the load average was high
- in-process stress-100 journeys/s: the 3 runs spread 134% (123.5 to 379.1): re-run it if the load average was high
- in-process stress-250 journeys/s: the 3 runs spread 275% (97.68 to 366.2): re-run it if the load average was high
- in-process stress-500 journeys/s: the 3 runs spread 23% (360.6 to 447.8): re-run it if the load average was high
- ready (cold) of payments: the 3 runs spread 67% (197.3 to 359.4): re-run it if the load average was high
- ready (cold) of provisioner: the 3 runs spread 44% (94.59 to 150.7): re-run it if the load average was high
- ready (cold) of tls-agent: the 3 runs spread 17% (25.38 to 30.39): re-run it if the load average was high
- stack stress journeys/s: the 3 runs spread 18% (270.8 to 328.1): re-run it if the load average was high
- clean build of linux-amd64/core: the 3 runs spread 46% (19.8 to 32.77): re-run it if the load average was high
- clean build of linux-amd64/payments: the 3 runs spread 18% (16.51 to 19.78): re-run it if the load average was high
- `go test -count=1 -p 2 ./...`: the 2 runs spread 55% (576.3 to 1019): re-run it if the load average was high

