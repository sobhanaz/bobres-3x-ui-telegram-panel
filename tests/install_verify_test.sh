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

# semver_lt: the order internal/release.Compare gives (semver 2.0), pre-releases included.
# lt A B: A < B; nlt A B: not (equal or greater).
lt()  { DESC="semver $1 < $2"; expect_ok semver_lt "$1" "$2"; }
nlt() { DESC="semver not $1 < $2"; expect_fail semver_lt "$1" "$2"; }
lt 1.2.9 1.2.10; lt 1.9.9 1.10.0; lt 2.0.0 10.0.0; nlt 1.2.3 1.2.3; nlt v1.2.3 1.2.3; nlt 1.2.3+b5 1.2.3
lt 1.2.0-rc.1 1.2.0; nlt 1.2.0 1.2.0-rc.1; lt 1.1.9 1.2.0-alpha; nlt 1.2.0-alpha 1.1.9
lt 1.2.0-rc.9 1.2.0-rc.10; nlt 1.2.0-rc.10 1.2.0-rc.9; lt 1.2.0-2 1.2.0-10
lt 1.2.0-rc.1 1.2.0-rc.1.1; nlt 1.2.0-rc.1.1 1.2.0-rc.1; lt 1.2.0-1 1.2.0-a; nlt 1.2.0-rc.a 1.2.0-rc.1
lt 1.2.0-rc.5 1.2.0-rc.99999999999999999999; lt 1.2.0-rc.01 1.2.0-rc.1; nlt 1.2.0-rc.1+b2 1.2.0-rc.1
nlt 1.2.0-rc.1 1.2.0-rc.1
# numbers too long for an int, by their value (internal/release TestCompare holds the same pairs)
lt 1.2.0-rc.9223372036854775807 1.2.0-rc.100000000000000000000; nlt 1.2.0-rc.100000000000000000000 1.2.0-rc.9223372036854775807
lt 1.2.0-rc.99999999999999999999 1.2.0-rc.100000000000000000000; nlt 1.2.0-rc.100000000000000000000 1.2.0-rc.99999999999999999999
nlt 1.2.0-rc.0100000000000000000000 1.2.0-rc.99999999999999999999; lt 1.2.0-rc.0 1.2.0-rc.00; lt 1.2.0-rc.00 1.2.0-rc.1
# semver 2.0's own example, each one below every later one
ORDER="1.0.0-alpha 1.0.0-alpha.1 1.0.0-alpha.beta 1.0.0-beta 1.0.0-beta.2 1.0.0-beta.11 1.0.0-rc.1 1.0.0"
for x in $ORDER; do
  later=0
  for y in $ORDER; do
    if [ "$x" = "$y" ]; then later=1; nlt "$x" "$y"; continue; fi
    if [ "$later" -eq 1 ]; then lt "$x" "$y"; else nlt "$x" "$y"; fi
  done
done

K="$ROOT/tools/sign/keygen.sh"; L="$ROOT/tools/sign/keylist.sh"; S="$ROOT/tools/sign/sign.sh"
"$K" roots "$T/roots" >/dev/null; "$K" release rel-a "$T/rel" >/dev/null; "$K" release rel-b "$T/rel" >/dev/null
RELEASE_ROOTS=("$(cat "$T/roots/root-1.pub.pem")" "$(cat "$T/roots/root-2.pub.pem")")

# make_case NAME SEQ VER DAYS KEYARGS... ; builds $T/NAME with signed keys + a release signed by
# rel-a for the channel $SIGN_CHANNEL (stable unless set)
SIGN_CHANNEL=stable
make_case() {
  local name=$1 seq=$2 ver=$3 days=$4; shift 4
  local d="$T/$name"; mkdir -p "$d"
  "$L" build --seq "$seq" --expires-days 30 --min-version 1.0.0 "$@" -o "$d/keys.txt" >/dev/null
  "$L" sign "$d/keys.txt" "$T/roots/root-2.key.pem" >/dev/null   # only root-2 signs -> sig.1
  echo bin > "$d/bobres_linux_amd64"
  ( "$S" --version "$ver" --channel "$SIGN_CHANNEL" --key-id rel-a --key "$T/rel/rel-a.key.pem" --expires-days "$days" "$d" >/dev/null )
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

# Pre-releases: above the release candidate before, below their own release and min_version.
make_case rc10 5 1.3.0-rc.10 30 $A
echo 1.3.0-rc.9 > "$STATE/release.version"
DESC="pre-release above the one installed accepted (rc.10 after rc.9)"; expect_ok verify_release "$T/rc10" "$STATE"
[ "${RELEASE_VERSION:-}" = "1.3.0-rc.10" ] && ok "pre-release version set" || bad "pre-release version set"
echo 1.3.0 > "$STATE/release.version"
DESC="pre-release below its installed release rejected"; expect_fail verify_release "$T/rc10" "$STATE"
echo 1.3.0-rc.10 > "$STATE/release.version"
make_case good13 5 1.3.0 30 $A
DESC="release above its installed pre-release accepted"; expect_ok verify_release "$T/good13" "$STATE"
rm -f "$STATE/release.version"
make_case rcmin 5 1.0.0-rc.1 30 $A
DESC="pre-release of min_version rejected"; expect_fail verify_release "$T/rcmin" "$STATE"

# Channels: stable (the default) refuses a release signed for beta, even when GitHub serves it
# as the latest release; BOBRES_UPDATE_CHANNEL=beta takes it, and stable releases on both.
SIGN_CHANNEL=beta; make_case beta14 5 1.4.0-rc.1 30 $A; SIGN_CHANNEL=stable
RELEASE_VERSION=""
DESC="beta release refused on the default channel"; expect_fail verify_release "$T/beta14" "$STATE"
DESC="beta release refused on the stable channel"; expect_fail verify_release "$T/beta14" "$STATE" stable
[ -z "${RELEASE_VERSION:-}" ] && ok "refused beta sets no version" || bad "refused beta sets no version"
why=$(verify_release "$T/beta14" "$STATE" 2>&1 >/dev/null)
case $why in
  *"is a beta release"*"BOBRES_UPDATE_CHANNEL=beta"*) ok "beta refusal says how to opt in" ;;
  *) bad "beta refusal says how to opt in: $why" ;;
esac
DESC="beta release accepted on the beta channel"; expect_ok verify_release "$T/beta14" "$STATE" beta
[ "${RELEASE_VERSION:-}" = "1.4.0-rc.1" ] && ok "beta version set" || bad "beta version set"
DESC="stable release accepted on the beta channel"; expect_ok verify_release "$T/good" "$STATE" beta
# A header without a channel line (signed so by hand) counts as stable, as in Go.
cp -R "$T/good" "$T/nochan"; sed -i.bak '/^# channel /d' "$T/nochan/checksums.txt"; rm -f "$T/nochan/checksums.txt.bak"
openssl pkeyutl -sign -inkey "$T/rel/rel-a.key.pem" -rawin -in "$T/nochan/checksums.txt" -out "$T/nochan/checksums.txt.sig"
DESC="release without a channel line accepted on the stable channel"; expect_ok verify_release "$T/nochan" "$STATE"

# release_channel reads BOBRES_UPDATE_CHANNEL as internal/release.ParseChannel does.
chan() {
  local got
  if got=$(release_channel "$1") && [ "$got" = "$2" ]; then ok "channel '$1' is $2"; else bad "channel '$1' is $2 (got '${got:-}')"; fi
}
chan "" stable; chan stable stable; chan " Beta " beta; chan BETA beta
DESC="channel nightly refused"; expect_fail release_channel nightly

# The license: nothing reads a BOBRES_LICENSE variable (no Go code, not this script), so
# neither the help nor the --license refusal may send the operator there; both name what does
# install one once the store is up.
help=$( (main --help) 2>&1 )
case $help in
  *BOBRES_LICENSE*) bad "help sends the license to BOBRES_LICENSE, which nothing reads" ;;
  *"bobres license activate"*"bobres license import"*"System > License"*) ok "help says how a license is installed" ;;
  *) bad "help says how a license is installed: $help" ;;
esac
for arg in --license --license=BOBRES-AAAA-BBBB-CCCC; do
  why=$( (main "$arg") 2>&1 ); code=$?
  case $why in
    *BOBRES_LICENSE*) bad "$arg refusal sends the license to BOBRES_LICENSE" ;;
    *"sudo bobres license activate"*"System > License"*) [ "$code" -eq 1 ] && ok "$arg refused with the way to activate" ||
      bad "$arg refused with exit $code" ;;
    *) bad "$arg refused with the way to activate: $why" ;;
  esac
done

RELEASE_ROOTS=("$(cat "$T/rel/rel-a.pub.pem")")
rm -f "$STATE"/*
DESC="unknown root rejected"; expect_fail verify_release "$T/good" "$STATE"

[ "$fails" -eq 0 ] && echo "all checks passed" || { echo "$fails failed"; exit 1; }
