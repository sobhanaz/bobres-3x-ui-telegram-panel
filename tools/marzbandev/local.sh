#!/usr/bin/env bash
# A throwaway Marzban panel in Docker, to try BOBRES against the real thing on a
# Mac or a Linux machine (the same image and settings as the CI job "Marzban
# integration"):
#
#   tools/marzbandev/local.sh up      start it, make the admin and the inbounds
#   tools/marzbandev/local.sh test    run the integration tests against it
#   tools/marzbandev/local.sh down    remove it
#
# Marzban listens on its own loopback unless it has TLS files, which a published
# Docker port cannot reach; `up` starts a few lines of Python inside the
# container that pass the port on. The panel is then at http://127.0.0.1:8000
# (MARZBAN_DEV_PORT) and its Shadowsocks inbound at 127.0.0.1:1080
# (MARZBAN_DEV_SS_PORT). The passwords are made at `up` and kept in a file
# readable by you only. Needs Docker and Go.
set -euo pipefail

# The image CI tests: change the tag and the digest together.
IMAGE="gozargah/marzban:v0.8.4@sha256:8e422c21997e5d2e3fa231eeff73c0a19193c20fc02fa4958e9368abb9623b8d"
VERSION="v0.8.4"
NAME="${MARZBAN_DEV_NAME:-bobres-marzban-dev}"
PORT="${MARZBAN_DEV_PORT:-8000}"
SS_PORT="${MARZBAN_DEV_SS_PORT:-1080}"
ENV_FILE="${TMPDIR:-/tmp}/$NAME.env"
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"

RELAY='
import asyncio
async def pipe(r, w):
    try:
        while True:
            d = await r.read(65536)
            if not d:
                break
            w.write(d)
            await w.drain()
    except Exception:
        pass
    finally:
        w.close()
async def handle(r, w):
    try:
        ur, uw = await asyncio.open_connection("127.0.0.1", 8000)
    except Exception:
        w.close()
        return
    await asyncio.gather(pipe(r, uw), pipe(ur, w))
async def main():
    s = await asyncio.start_server(handle, "0.0.0.0", 8001)
    await s.serve_forever()
asyncio.run(main())
'

up() {
  if docker ps -a --format '{{.Names}}' | grep -qx "$NAME"; then
    echo "$NAME exists already: run '$0 down' first" >&2
    exit 1
  fi
  local boot admin
  boot="$(openssl rand -hex 16)"
  admin="$(openssl rand -hex 16)"
  docker run -d --name "$NAME" -p "127.0.0.1:$PORT:8001" -p "127.0.0.1:$SS_PORT:1080" \
    -e SUDO_USERNAME=bootstrap -e SUDO_PASSWORD="$boot" \
    -e JWT_ACCESS_TOKEN_EXPIRE_MINUTES=1 -e JOB_RECORD_USER_USAGES_INTERVAL=2 -e JOB_REVIEW_USERS_INTERVAL=2 \
    "$IMAGE" > /dev/null
  docker exec -d "$NAME" python -c "$RELAY"
  (cd "$ROOT" && go run ./tools/marzbandev -url "http://127.0.0.1:$PORT" \
    -bootstrap-user bootstrap -bootstrap-pass "$boot" -user bobres-ci -pass "$admin") > /dev/null
  umask 077
  cat > "$ENV_FILE" <<EOF
MARZBAN_ADMIN_PASSWORD=$admin
MARZBAN_BOOTSTRAP_PASSWORD=$boot
EOF
  echo "Marzban $VERSION is at http://127.0.0.1:$PORT (admin bobres-ci); '$0 test' runs the tests, '$0 down' removes it."
}

run_tests() {
  [ -r "$ENV_FILE" ] || { echo "no panel: run '$0 up' first" >&2; exit 1; }
  # shellcheck source=/dev/null
  . "$ENV_FILE"
  export BOBRES_TEST_REQUIRE_DB="${BOBRES_TEST_REQUIRE_DB:-1}"
  export MARZBAN_TEST_REQUIRE=1 MARZBAN_TEST_URL="http://127.0.0.1:$PORT" MARZBAN_TEST_ALLOW_PRIVATE=1
  export MARZBAN_TEST_VERSION="$VERSION" MARZBAN_TEST_SHORT_TOKENS=1
  export MARZBAN_TEST_SS_ADDR="127.0.0.1:$SS_PORT" MARZBAN_TEST_SS_TARGET=127.0.0.1:8000
  export MARZBAN_TEST_USER=bobres-ci MARZBAN_TEST_PASSWORD="$MARZBAN_ADMIN_PASSWORD"
  export MARZBAN_TEST_ENV_USER=bootstrap MARZBAN_TEST_ENV_PASSWORD="$MARZBAN_BOOTSTRAP_PASSWORD"
  cd "$ROOT"
  go test -tags integration -count=1 -v -run Integration ./internal/marzban/
  go test -tags integration -count=1 -v -run IntegrationMarzban ./internal/provisioner/adapter/
  go test -tags integration -count=1 -v -run MarzbanReal ./tests/
  go test -tags integration -count=1 -v -run MarzbanServerEndToEnd ./internal/core/web/
  MARZBAN_TEST_USER=bootstrap MARZBAN_TEST_PASSWORD="$MARZBAN_BOOTSTRAP_PASSWORD" MARZBAN_TEST_OWNERLESS=1 \
    go test -tags integration -count=1 -v -run MarzbanReal ./tests/
}

down() {
  docker rm -f "$NAME" > /dev/null 2>&1 || true
  rm -f "$ENV_FILE"
  echo "$NAME removed"
}

case "${1:-}" in
  up) up ;;
  test) run_tests ;;
  down) down ;;
  *) echo "usage: $0 up|test|down" >&2; exit 2 ;;
esac
