#!/usr/bin/env bash
# The performance and size baseline of BOBRES (docs/21-benchmark.md): the load
# test, each binary's size, build time, start and memory, the migrations, the
# test suites, the frontend bundles, the release images and CI times, the code
# size and the Go micro-benchmarks. Raw results go to docs/bench/<date>/<variant>/
# as JSON, with summary.md and summary.json written from them at the end.
#
#   tools/bench/run.sh                 # everything, the plain build (about 4 hours)
#   tools/bench/run.sh build stack     # some steps only
#   BENCH_VARIANT=garble tools/bench/run.sh            # after code protection
#   tools/bench/benchtool compare: see the end of this file's output
#
# Settings (environment):
#   BENCH_VARIANT  the build variant, which names the output directory: plain
#                  (default), garble, or any name.
#   BENCH_BUILD    the variant's build command, used where release.yml runs
#                  `go build`: "go build" for plain, "garble -literals -tiny build"
#                  for garble, unless set.
#   BENCH_REPEAT   how many times each timed measurement runs (default 3).
#   BENCH_DATE     the output's date directory (default: today).
#   BENCH_OUT      the output directory (default docs/bench/$BENCH_DATE/$BENCH_VARIANT).
#   BENCH_WORK     scratch for binaries, caches and logs (default $TMPDIR/bobres-bench-$BENCH_VARIANT).
#   BENCH_REDIS    a throwaway Redis for the tests and the bot (default 127.0.0.1:6391;
#                  started under BENCH_WORK when nothing answers there, stopped at the end).
#   BENCH_QUICK=1  short load runs and one repetition: checks the script, measures nothing.
#   BENCH_RELEASE  the release whose images and workflow runs are read (default 0.19.0).
#   BENCH_VERSION  the version stamped into the binaries (default: describe --tags).
#   BOBRES_TEST_DATABASE_URL  the test Postgres, as for go test (default: the /tmp socket).
#
# Steps: sys loc web build stack loadtest migrate tests gobench release report.
# The default is all of them for the plain variant. For another variant it is
# sys web build stack release report: the in-process load test, the test suites,
# the migrations, the micro-benchmarks and the code size are measured on the
# source with go test, so code protection does not change them.
#
# Nothing here touches a real server, database or account: the services run on
# 127.0.0.1 against a database the run creates and drops, with a fake 3x-ui panel
# and a fake Telegram API, and with every outbound HTTP(S) request sent to a dead
# proxy. The release step reads public data only (ghcr.io manifests, gh run view).
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT"
export GOTOOLCHAIN="${GOTOOLCHAIN:-local}"

VARIANT="${BENCH_VARIANT:-plain}"
case "$VARIANT" in
  *[!a-zA-Z0-9._-]*|"") echo "BENCH_VARIANT must be a plain name like plain or garble" >&2; exit 2 ;;
esac
if [[ -z "${BENCH_BUILD:-}" ]]; then
  case "$VARIANT" in
    garble) BENCH_BUILD="garble -literals -tiny build" ;;
    *) BENCH_BUILD="go build" ;;
  esac
fi
REPEAT="${BENCH_REPEAT:-3}"
QUICK="${BENCH_QUICK:-0}"
[[ "$QUICK" == 1 ]] && REPEAT=1
DATE="${BENCH_DATE:-$(date +%F)}"
OUT="${BENCH_OUT:-$ROOT/docs/bench/$DATE/$VARIANT}"
WORK="${BENCH_WORK:-${TMPDIR:-/tmp}/bobres-bench-$VARIANT}"
WORK="${WORK%/}"
REDIS="${BENCH_REDIS:-127.0.0.1:6391}"
RELEASE="${BENCH_RELEASE:-0.19.0}"
# The version string release.yml stamps in (internal/version.Version).
VERSION="${BENCH_VERSION:-}"
MODULE="$(go list -m)"
SERVICES=(core bot payments provisioner backup)
BINARIES=("${SERVICES[@]}" bobres)
HOST="$(go env GOHOSTOS)-$(go env GOHOSTARCH)"

if [[ $# -gt 0 ]]; then
  STEPS=("$@")
elif [[ "$VARIANT" == plain ]]; then
  STEPS=(sys loc web build stack loadtest migrate tests gobench release report)
else
  STEPS=(sys web build stack release report)
fi

mkdir -p "$OUT" "$WORK/logs"
BT="$WORK/benchtool"
TIMINGS="$OUT/timings.jsonl"
FAILURES=()

log() { printf '\n== %s\n' "$*" >&2; }

# measure LABEL REP -- CMD...: time CMD (in the directory MDIR, default the
# repository) into timings.jsonl, its output into WORK/logs. A failure is
# recorded and reported at the end; the run goes on.
measure() {
  local label="$1" rep="$2"; shift 3
  local logf="$WORK/logs/${label//\//_}-r$rep.log"
  if ! "$BT" timeit -out "$TIMINGS" -label "$label" -rep "$rep" -log "$logf" -dir "${MDIR:-$ROOT}" -- "$@"; then
    FAILURES+=("$label #$rep (log: $logf)")
    echo "  FAILED: $label #$rep, see $logf" >&2
    return 1
  fi
}

setup() {
  log "benchtool"
  go build -o "$BT" ./tools/bench/benchtool
  # The version string release.yml stamps in (internal/version.Version): the
  # tag, as `describe --tags` names this commit.
  [[ -n "$VERSION" ]] || VERSION="$("$BT" describe)"
  LDFLAGS="-s -w -X $MODULE/internal/version.Version=$VERSION"
  # The Redis the tests and the bot use: a throwaway one, never another session's.
  local host="${REDIS%:*}" port="${REDIS##*:}"
  if ! redis-cli -h "$host" -p "$port" ping >/dev/null 2>&1; then
    mkdir -p "$WORK/redis"
    redis-server --bind "$host" --port "$port" --daemonize yes --dir "$WORK/redis" --save '' --appendonly no \
      --pidfile "$WORK/redis/redis.pid" >/dev/null
    STARTED_REDIS=1
    for _ in $(seq 50); do redis-cli -h "$host" -p "$port" ping >/dev/null 2>&1 && break; sleep 0.1; done
  fi
}

cleanup() {
  if [[ "${STARTED_REDIS:-0}" == 1 ]]; then
    redis-cli -h "${REDIS%:*}" -p "${REDIS##*:}" shutdown nosave >/dev/null 2>&1 || true
  fi
}
trap cleanup EXIT

step_sys() {
  log "system"
  "$BT" sysinfo -out "$OUT/system.json" -variant "$VARIANT" -build "$BENCH_BUILD"
}

step_loc() {
  log "code size"
  "$BT" loc -root "$ROOT" -out "$OUT/loc.json"
}

# The dashboard and the Mini App: core embeds both, so they are built before
# the Go binaries, as deploy/Dockerfile does.
step_web() {
  for app in dashboard miniapp; do
    log "web/$app"
    MDIR="$ROOT/web/$app" measure "web/$app/npm-ci" 1 -- npm ci --no-audit --no-fund || true
    for rep in $(seq "$REPEAT"); do
      MDIR="$ROOT/web/$app" measure "web/$app/build" "$rep" -- npm run build || true
    done
    "$BT" sizes -out "$OUT/sizes/web-$app.json" -label "web/$app" -gzip -strip-prefix "web/$app/dist/app/" "web/$app/dist/app"
  done
}

# The variant's build command as words ("go build", "garble -literals -tiny build").
read -ra BUILD_CMD <<< "$BENCH_BUILD"

# build_cmd TARGET BIN DEST GOCACHE: one release-style build, as an argv.
build_cmd() {
  local target="$1" bin="$2" dest="$3" cache="$4"
  BUILD_ARGV=(env CGO_ENABLED=0 GOOS="${target%-*}" GOARCH="${target#*-}" GOCACHE="$cache"
    "${BUILD_CMD[@]}" -trimpath -ldflags "$LDFLAGS" -o "$dest/$bin" "./cmd/$bin")
}

# Release target (linux/amd64): every binary clean (an empty GOCACHE, the
# modules already downloaded) and then warm (the same cache again: every
# package comes from the cache and only the link runs; the binary is removed
# first, or go build would see it is up to date and do nothing), REPEAT times.
# This machine's own target: one build, for the stack step to run.
step_build() {
  [[ -f web/dashboard/dist/app/index.html && -f web/miniapp/dist/app/index.html ]] ||
    echo "  warning: the web apps are not built (run the web step first): core embeds a notice instead" >&2
  local dest="$WORK/bin/linux-amd64" cache="$WORK/gocache-clean"
  mkdir -p "$dest"
  for rep in $(seq "$REPEAT"); do
    for bin in "${BINARIES[@]}"; do
      log "build linux-amd64/$bin #$rep"
      rm -rf "$cache"
      rm -f "${dest:?}/$bin"
      build_cmd linux-amd64 "$bin" "$dest" "$cache"
      measure "build/linux-amd64/$bin/clean" "$rep" -- "${BUILD_ARGV[@]}" || true
      rm -f "${dest:?}/$bin"
      measure "build/linux-amd64/$bin/warm" "$rep" -- "${BUILD_ARGV[@]}" || true
    done
  done
  rm -rf "$cache"
  "$BT" sizes -out "$OUT/sizes/bin-linux-amd64.json" -label "bin/linux-amd64" -strip-prefix "$dest/" "$dest"
  if [[ "$HOST" != linux-amd64 ]]; then
    log "build $HOST (to run the stack here)"
    mkdir -p "$WORK/bin/$HOST"
    for bin in "${BINARIES[@]}"; do
      build_cmd "$HOST" "$bin" "$WORK/bin/$HOST" "$(go env GOCACHE)"
      "${BUILD_ARGV[@]}"
    done
    "$BT" sizes -out "$OUT/sizes/bin-$HOST.json" -label "bin/$HOST" -strip-prefix "$WORK/bin/$HOST/" "$WORK/bin/$HOST"
  fi
}

# The real binaries as separate processes, under the load test. The load
# generator is always a plain build: only the services are the variant.
step_stack() {
  local scen="stress:200:30s,paced:500:60s:2s"
  [[ "$QUICK" == 1 ]] && scen="stress:20:5s,paced:20:5s:200ms"
  go build -o "$WORK/loadtest" ./tools/loadtest
  for rep in $(seq "$REPEAT"); do
    log "stack #$rep ($scen)"
    rm -rf "$WORK/stack-r$rep"
    measure "stack/run" "$rep" -- "$BT" stack -bin-dir "$WORK/bin/$HOST" -loadtest "$WORK/loadtest" -variant "$VARIANT" \
      -work "$WORK/stack-r$rep" -redis "$REDIS" -scenarios "$scen" -out "$OUT/stack/stack-r$rep.json" || true
  done
}

# The in-process load test, the scenarios of docs/12-load-test.md: stress at
# 50/100/250/500 users for 30 s, then 500 and 2,000 customers 2 s apart for 60 s.
# Each run is a fresh stack and waits for its delivery backlog to clear.
step_loadtest() {
  local dur=30s pdur=60s stress=(50 100 250 500) paced=(500 2000)
  if [[ "$QUICK" == 1 ]]; then dur=3s pdur=5s stress=(5) paced=(20); fi
  mkdir -p "$OUT/loadtest"
  go test -c -o "$WORK/loadtest.test" ./tools/loadtest
  for rep in $(seq "$REPEAT"); do
    for n in "${stress[@]}"; do
      log "load test: stress $n users #$rep"
      measure "loadtest/inproc/stress-$n" "$rep" -- env BOBRES_TEST_REQUIRE_DB=1 "$WORK/loadtest.test" -test.run TestLocalStack \
        -test.count=1 -test.timeout 60m -test.v -users "$n" -duration "$dur" -json "$OUT/loadtest/inproc-stress-$n-r$rep.json" || true
    done
    for n in "${paced[@]}"; do
      log "load test: paced $n users #$rep"
      measure "loadtest/inproc/paced-$n" "$rep" -- env BOBRES_TEST_REQUIRE_DB=1 "$WORK/loadtest.test" -test.run TestLocalStack \
        -test.count=1 -test.timeout 60m -test.v -users "$n" -duration "$pdur" -think 2s -json "$OUT/loadtest/inproc-paced-$n-r$rep.json" || true
    done
  done
}

step_migrate() {
  log "migrations"
  "$BT" migrate -repeat "$REPEAT" -out "$OUT/migrate.json"
}

step_tests() {
  local env=(BOBRES_TEST_REQUIRE_DB=1 "BOBRES_TEST_REDIS_ADDR=$REDIS")
  local pkgs=./...
  [[ "$QUICK" == 1 ]] && pkgs=./internal/money/...
  for rep in $(seq "$REPEAT"); do
    log "go test #$rep"
    measure test/go/default "$rep" -- env "${env[@]}" go test -count=1 -timeout 30m "$pkgs" || true
    measure test/go/p2 "$rep" -- env "${env[@]}" go test -count=1 -p 2 -timeout 30m "$pkgs" || true
    # As CI's Test (race) job runs them (without its coverage profile), then
    # its concurrency step with the pool at 4 connections.
    measure test/go/race "$rep" -- env "${env[@]}" go test -race -count=1 -timeout 20m "$pkgs" || true
    measure test/go/race-pool4 "$rep" -- env "${env[@]}" BOBRES_TEST_POOL_MAX_CONNS=4 go test -race -count=1 -timeout 10m \
      ./internal/core/domain/ ./internal/core/store/ || true
    # As CI runs them (npm test), with a longer per-test timeout: a few
    # dashboard tests pass in about a second alone and time out at 5 s when
    # the machine is busy, which would drop a run from the median.
    for app in dashboard miniapp; do
      local raw="$WORK/vitest-$app-r$rep.json"
      rm -f "$raw"
      MDIR="$ROOT/web/$app" measure "test/vitest/$app" "$rep" -- npx vitest run --testTimeout=30000 \
        --reporter=default --reporter=json --outputFile="$raw" || true
      # Only the counts are kept: the raw report holds every assertion.
      mkdir -p "$OUT/vitest"
      if [[ -f "$raw" ]]; then
        jq '{numTotalTests, numPassedTests, numFailedTests, numPendingTests, files: (.testResults | length),
          numFailedTestSuites, success, startTime}' "$raw" > "$OUT/vitest/$app-r$rep.json"
      fi
    done
  done
  # Counted from the source by the loc step: run it too.
  "$BT" loc -root "$ROOT" -out "$OUT/loc.json"
}

step_gobench() {
  log "Go micro-benchmarks"
  local count=5 pkgs
  [[ "$QUICK" == 1 ]] && count=1
  pkgs="$(grep -rl --include='*_test.go' '^func Benchmark' cmd internal tools | xargs -n1 dirname | sort -u | sed 's|^|./|')"
  # shellcheck disable=SC2086 # one package per word
  if env BOBRES_TEST_REQUIRE_DB=1 "BOBRES_TEST_REDIS_ADDR=$REDIS" go test -run '^$' -bench . -benchmem -count "$count" $pkgs > "$OUT/gobench.txt" 2>&1; then
    "$BT" gobench -in "$OUT/gobench.txt" -out "$OUT/gobench.json"
  else
    FAILURES+=("gobench (see $OUT/gobench.txt)")
  fi
}

# The release's images on ghcr.io and the job times of the last green CI run
# on main and of the release's workflow run (public data, read only).
step_release() {
  log "release artifacts"
  "$BT" ghcr -version "$RELEASE" -out "$OUT/ghcr.json" || FAILURES+=("ghcr")
  if ! command -v gh >/dev/null; then
    echo "  gh is not installed: no CI times" >&2
    return
  fi
  local ci rel
  ci="$(gh run list --workflow CI --branch main --status success --limit 1 --json databaseId --jq '.[0].databaseId' || true)"
  rel="$(gh run list --workflow Release --branch "v$RELEASE" --limit 1 --json databaseId --jq '.[0].databaseId' || true)"
  local runs=()
  for id in $ci $rel; do
    gh run view "$id" --json databaseId,workflowName,headBranch,jobs > "$WORK/run-$id.json" && runs+=("$WORK/run-$id.json")
  done
  [[ ${#runs[@]} -gt 0 ]] || return 0
  jq -s '{runs: [.[] | {workflow: .workflowName, id: .databaseId, ref: .headBranch,
      wall_s: (([.jobs[].completedAt | select(. != "0001-01-01T00:00:00Z") | fromdateiso8601] | max) - ([.jobs[].startedAt | fromdateiso8601] | min)),
      jobs: [.jobs[] | {name, conclusion, startedAt, completedAt,
        seconds: (if .completedAt == "0001-01-01T00:00:00Z" then 0 else (.completedAt | fromdateiso8601) - (.startedAt | fromdateiso8601) end)}]}]}' \
    "${runs[@]}" > "$OUT/ci.json"
}

step_report() {
  log "report"
  "$BT" report -dir "$OUT"
  echo "  $OUT/summary.md" >&2
}

setup
for s in "${STEPS[@]}"; do
  if ! declare -F "step_$s" >/dev/null; then
    echo "unknown step $s (steps: sys loc web build stack loadtest migrate tests gobench release report)" >&2
    exit 2
  fi
  "step_$s"
done

if [[ ${#FAILURES[@]} -gt 0 ]]; then
  printf '\nFailed measurements (not counted in the medians):\n' >&2
  printf '  %s\n' "${FAILURES[@]}" >&2
  exit 1
fi
printf '\nDone: %s\nCompare two variants: %s compare -a docs/bench/%s/plain -b %s\n' "$OUT" "$BT" "$DATE" "$OUT" >&2
