#!/usr/bin/env bash
# Tests install.sh verify_release() against keys made by tools/sign. bash 3.2 + Linux.
# shellcheck disable=SC2034,SC2086,SC2015
set -uo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
T="$(mktemp -d 2>/dev/null || mktemp -d -t bobres-iv)"
trap 'rm -rf "$T"' EXIT
BOBRES_INSTALL_SOURCE_ONLY=1
# shellcheck disable=SC1091
. "$ROOT/install/install.sh"
set +e
fails=0
ok()  { echo "PASS: $1"; }
bad() { echo "FAIL: $1"; fails=$((fails + 1)); }
expect_ok()   { if "$@" 2>/dev/null; then ok "$DESC"; else bad "$DESC"; fi; }
expect_fail() { if "$@" 2>/dev/null; then bad "$DESC"; else ok "$DESC"; fi; }

K="$ROOT/tools/sign/keygen.sh"; L="$ROOT/tools/sign/keylist.sh"; S="$ROOT/tools/sign/sign.sh"
"$K" roots "$T/roots" >/dev/null; "$K" release rel-a "$T/rel" >/dev/null; "$K" release rel-b "$T/rel" >/dev/null
RELEASE_ROOTS=("$(cat "$T/roots/root-1.pub.pem")" "$(cat "$T/roots/root-2.pub.pem")")

# make_case NAME SEQ KEYARGS... ; builds $T/NAME with signed keys + a release signed by rel-a
make_case() {
  local name=$1 seq=$2 ver=$3 days=$4; shift 4
  local d="$T/$name"; mkdir -p "$d"
  "$L" build --seq "$seq" --expires-days 30 --min-version 1.0.0 "$@" -o "$d/keys.txt" >/dev/null
  "$L" sign "$d/keys.txt" "$T/roots/root-2.key.pem" >/dev/null   # only root-2 signs -> sig.1
  echo bin > "$d/bobres_linux_amd64"
  ( "$S" --version "$ver" --key-id rel-a --key "$T/rel/rel-a.key.pem" --expires-days "$days" "$d" >/dev/null )
  # sign.sh hashed bin too; move it out so dir mirrors download dir
  rm -f "$d/bobres_linux_amd64"
}
STATE="$T/state"; mkdir -p "$STATE"
A="--key rel-a=$T/rel/rel-a.pub.pem"

make_case good 5 1.2.3 30 $A
DESC="valid release accepted (signed by root-2 only)"; expect_ok verify_release "$T/good" "$STATE"
[ "${RELEASE_VERSION:-}" = "1.2.3" ] && [ "${KEYS_SEQ:-}" = "5" ] && ok "globals set" || bad "globals set"

make_case revoked 6 1.2.3 30 $A --revoke rel-a
DESC="revoked key rejected"; expect_fail verify_release "$T/revoked" "$STATE"

make_case notlisted 7 1.2.3 30 --key "rel-b=$T/rel/rel-b.pub.pem"
DESC="key not in list rejected"; expect_fail verify_release "$T/notlisted" "$STATE"

make_case expired 8 1.2.3 0 $A
sleep 61
DESC="expired release rejected"; expect_fail verify_release "$T/expired" "$STATE"

make_case lowmin 9 0.5.0 30 $A
DESC="version below min_version rejected"; expect_fail verify_release "$T/lowmin" "$STATE"

cp -R "$T/good" "$T/tamper"; echo x >> "$T/tamper/checksums.txt"
DESC="tampered checksums rejected"; expect_fail verify_release "$T/tamper" "$STATE"

cp -R "$T/good" "$T/badkeys"; echo "key evil AAAA" >> "$T/badkeys/keys.txt"
DESC="tampered keys.txt rejected"; expect_fail verify_release "$T/badkeys" "$STATE"

cp -R "$T/good" "$T/nosig"; rm -f "$T"/nosig/keys.txt.sig.*
DESC="missing key list signature rejected"; expect_fail verify_release "$T/nosig" "$STATE"

echo 10 > "$STATE/keys.seq"
DESC="lower key list seq rejected (rollback)"; expect_fail verify_release "$T/good" "$STATE"
echo 5 > "$STATE/keys.seq"; echo 2.0.0 > "$STATE/release.version"
DESC="downgrade below installed rejected"; expect_fail verify_release "$T/good" "$STATE"

RELEASE_ROOTS=("$(cat "$T/rel/rel-a.pub.pem")")
rm -f "$STATE"/*
DESC="unknown root rejected"; expect_fail verify_release "$T/good" "$STATE"

[ "$fails" -eq 0 ] && echo "all checks passed" || { echo "$fails failed"; exit 1; }
