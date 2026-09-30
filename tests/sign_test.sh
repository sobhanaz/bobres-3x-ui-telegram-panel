#!/usr/bin/env bash
# End-to-end test for tools/sign (keygen/keylist/sign). Portable: bash 3.2, macOS + Linux.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
SIGN="$ROOT/tools/sign/sign.sh"
KEYGEN="$ROOT/tools/sign/keygen.sh"
KEYLIST="$ROOT/tools/sign/keylist.sh"

failures=0
pass() { printf 'PASS: %s\n' "$1"; }
fail() { printf 'FAIL: %s\n' "$1"; failures=$((failures + 1)); }
verify() { openssl pkeyutl -verify -pubin -inkey "$1" -rawin -in "$2" -sigfile "$3" >/dev/null 2>&1; }

command -v openssl >/dev/null 2>&1 || { echo "FAIL: openssl is required"; exit 1; }

TMP_DIR="$(mktemp -d 2>/dev/null || mktemp -d -t bobres-sign-test)"
trap 'rm -rf "$TMP_DIR"' EXIT

"$KEYGEN" roots "$TMP_DIR/roots" >/dev/null
"$KEYGEN" release rel-test "$TMP_DIR/rel" >/dev/null
REL_PUB="$TMP_DIR/rel/rel-test.pub.pem"
REL_KEY="$TMP_DIR/rel/rel-test.key.pem"
KEYS="$TMP_DIR/keys.txt"

"$KEYLIST" build --seq 1 --expires-days 30 --min-version 0.0.1 --key "rel-test=$REL_PUB" -o "$KEYS" >/dev/null
"$KEYLIST" sign "$KEYS" "$TMP_DIR/roots/root-1.key.pem" "$TMP_DIR/roots/root-2.key.pem" >/dev/null

mkdir -p "$TMP_DIR/dist"
echo one > "$TMP_DIR/dist/bobres_linux_amd64"
echo two > "$TMP_DIR/dist/bobres_darwin_arm64"
"$SIGN" --version 1.2.3 --key-id rel-test --key "$REL_KEY" "$TMP_DIR/dist" >/dev/null

if verify "$REL_PUB" "$TMP_DIR/dist/checksums.txt" "$TMP_DIR/dist/checksums.txt.sig"; then
  pass "release signature verifies"; else fail "release signature verifies"; fi
if verify "$TMP_DIR/roots/root-1.pub.pem" "$KEYS" "$KEYS.sig.1"; then
  pass "keys.txt.sig.1 verifies with root-1"; else fail "keys.txt.sig.1 verifies with root-1"; fi
if verify "$TMP_DIR/roots/root-2.pub.pem" "$KEYS" "$KEYS.sig.2"; then
  pass "keys.txt.sig.2 verifies with root-2"; else fail "keys.txt.sig.2 verifies with root-2"; fi
if verify "$TMP_DIR/roots/root-2.pub.pem" "$KEYS" "$KEYS.sig.1"; then
  fail "root-2 must not verify root-1 signature"; else pass "root-2 rejects root-1 signature"; fi

cp "$TMP_DIR/dist/checksums.txt" "$TMP_DIR/tampered.txt"
echo tampered >> "$TMP_DIR/tampered.txt"
if verify "$REL_PUB" "$TMP_DIR/tampered.txt" "$TMP_DIR/dist/checksums.txt.sig"; then
  fail "tampered checksums must fail"; else pass "tampered checksums fail"; fi

if grep -q '^# version 1.2.3$' "$TMP_DIR/dist/checksums.txt" && grep -q '^# key_id rel-test$' "$TMP_DIR/dist/checksums.txt"; then
  pass "header carries version and key_id"; else fail "header carries version and key_id"; fi

mkdir -p "$TMP_DIR/dist0"
echo one > "$TMP_DIR/dist0/bobres_linux_amd64"
"$SIGN" --version v1.2.3 --key-id rel-test --key "$REL_KEY" --expires-days 0 "$TMP_DIR/dist0" >/dev/null
issued="$(awk '/^# issued /{print $3}' "$TMP_DIR/dist0/checksums.txt")"
expires="$(awk '/^# expires /{print $3}' "$TMP_DIR/dist0/checksums.txt")"
if [ -n "$issued" ] && [ "$expires" = "$issued" ]; then
  pass "expires-days 0 => expires == issued"; else fail "expires-days 0 (issued=$issued expires=$expires)"; fi

"$KEYLIST" build --seq 2 --expires-days 30 --min-version 0.0.1 --key "rel-test=$REL_PUB" --revoke rel-test -o "$TMP_DIR/keys2.txt" >/dev/null
if grep -q '^revoked rel-test$' "$TMP_DIR/keys2.txt"; then
  pass "revoked line present"; else fail "revoked line present"; fi

if "$SIGN" --version 1.2 --key-id rel-test --key "$REL_KEY" "$TMP_DIR/dist0" >/dev/null 2>&1; then
  fail "bad semver must be rejected"; else pass "bad semver rejected"; fi

[ "$failures" -eq 0 ] || { echo "$failures check(s) failed"; exit 1; }
echo "all checks passed"
