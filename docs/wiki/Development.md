# Development

This page is for people who build, test or change BOBRES. Read [Architecture](Architecture) first for how the parts fit. The source is available to read and audit. Before you copy or redistribute code, read the [LICENSE](https://github.com/sobhanaz/bobres-3x-ui-telegram-panel/blob/main/LICENSE).

## Tools you need

| Tool | Version | For |
|---|---|---|
| Go | 1.26 or newer (`go.mod` says `go 1.26.0`) | The services and the CLI |
| golangci-lint | v2.14.0 | `make lint` |
| Node.js | 24 | The dashboard and the Mini App |
| buf | latest | `make proto` |
| Docker | any recent | Tests that need a real stack, and the lab |

`make` forces `GOTOOLCHAIN=local`, so it builds with the `go` on your PATH. Use a current 1.26.x for parity with CI and the images. Outside `make`, an older Go downloads go1.26.0, the oldest patch, so install a current 1.26.x instead. Install the linter with:

```bash
go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0
```

## Everyday commands

| Command | Does |
|---|---|
| `make build` | Builds `bot`, `core`, `payments`, `provisioner`, `backup` and `bobres` into `bin/` |
| `make test` | `go test -race -count=1 ./...` |
| `make lint` | golangci-lint v2 |
| `make vuln` | govulncheck |
| `make fmt` | gofmt |
| `make proto` | Regenerates protobuf code (needs buf) |
| `make dashboard` | Installs and builds the dashboard into the folder core embeds. Run it before `make build` |
| `make dashboard-test` | Type-checks and tests the dashboard |
| `make miniapp`, `make miniapp-test` | The same for the Mini App |

Core embeds `web/dashboard/dist/app` and `web/miniapp/dist/app`. If they are not built, `/admin` and `/app` show a "not built" notice.

For live editing of the dashboard, run a local core on port 8088 and start Vite:

```bash
cd web/dashboard && npm run dev
```

You can try the CLI without building a binary. For example `go run ./cmd/bobres help` prints the command list, and `go run ./cmd/bobres install -h` prints the install flags.

## Tests that need databases

Database tests need PostgreSQL 13 or newer and Redis.

| Variable | Meaning |
|---|---|
| `BOBRES_TEST_DATABASE_URL` | A URL for any database on a server where the role may `CREATE DATABASE`. The default is the local socket in `/tmp` |
| `BOBRES_TEST_REDIS_ADDR` | Default `127.0.0.1:6379` |
| `BOBRES_TEST_REQUIRE_DB=1` | Fail instead of skipping when a server is missing. CI sets it |
| `BOBRES_TEST_POOL_MAX_CONNS=4` | Caps the pools. Use it on `internal/core/domain` and `internal/core/store` to prove a test does not need a big machine |

Each test package gets its own throwaway database. Without the servers, those tests skip.

## Tests against real panels

The adapter and provisioner tests run against a real 3x-ui panel in CI, for v3.8.5, v3.9.0, v3.10.0 and v3.10.2. They sit behind the `integration` build tag and read `XUI_TEST_URL` and `XUI_TEST_TOKEN` (CI also sets `XUI_TEST_REQUIRE=1`). CI prepares a fresh panel with `go run ./tools/xuidev -url http://127.0.0.1:2053`, which prints a token. Then:

```bash
go test -tags integration -count=1 -p 1 -run Integration ./internal/provisioner/adapter/ ./internal/provisioner/server/
```

A separate CI job, "Marzban integration", starts the `gozargah/marzban:v0.8.4` image. To run the same panel and tests on your machine, use `tools/marzbandev/local.sh up|test|down`. The in-process fakes (`internal/xuifake`, `internal/marzbanfake`, `internal/tgfake`, `internal/zpfake`, `internal/tronfake`) let most tests run with no network.

## The lab

`deploy/lab` has two throwaway Ubuntu 24.04 containers that behave like fresh VPSes: one for the BOBRES stack and one for a 3x-ui panel. You install both as on real servers. The README there explains starting, stopping and wiping them.

## CI

`.github/workflows` has four workflows:

- `ci.yml`: race tests against Postgres and Redis, golangci-lint, govulncheck, the dashboard (vue-tsc, vitest, npm audit), shellcheck, image builds with an end-to-end install and a restore drill, the 3x-ui matrix, the Marzban job and gitleaks.
- `release.yml`: on a tag. Builds amd64 and arm64 images, signs them with cosign, builds the CLI and signs `checksums.txt`.
- `resign.yml`: re-signs the latest release before its signature expires (45 days).
- `pages.yml`: publishes the site.

## Rules the code follows

- Money is an integer in minor units with a currency. Never a float. The ledger is append-only.
- Secrets are never logged, audited or returned by an API. Each service gets only the secrets it needs.
- Migrations are forward-only (goose) and additive.
- Events are past-tense and versioned. Additive changes only within a version. Handlers are idempotent.
- Every user-facing text is a key with a Persian and an English value. Tests check that both languages have the same keys (and, for the bot, the same placeholders).
- A change to the bot, dashboard or API gets a test. Money flows get race, replay and tamper tests.

## How a change gets built

1. A short spec in `docs/`: scope, API, data, security and tests.
2. A branch and a pull request. Independent reviewers read the diff for correctness, security and the texts.
3. CI green, then merged. The release notes (`CHANGELOG.md`) say what changed.
4. A release candidate on the lab, then a release.

Ideas and changes of order go in [docs/ROADMAP.md](https://github.com/sobhanaz/bobres-3x-ui-telegram-panel/blob/main/docs/ROADMAP.md). Decisions stay in [docs/PLAN.md](https://github.com/sobhanaz/bobres-3x-ui-telegram-panel/blob/main/docs/PLAN.md).

## Adding a Mini App section

A section touches no shared file. In core, add `internal/core/web/miniapp_<name>.go` whose `init` calls `registerMiniFeature(miniFeature{routes, me})`. In the app, add `web/miniapp/src/sections/<name>/` with an `index.ts` that calls `registerSection(...)` and a `messages.ts` with both languages. See [docs/17-mini-app.md](https://github.com/sobhanaz/bobres-3x-ui-telegram-panel/blob/main/docs/17-mini-app.md).

## Releases

A tag makes a release. The details of publishing stable and beta releases, and of re-signing, are in the [releases runbook](https://github.com/sobhanaz/bobres-3x-ui-telegram-panel/blob/main/docs/runbooks/releases.md) and `tools/sign/README.md`. Never commit a private signing key.

See also [Roadmap](Roadmap).
