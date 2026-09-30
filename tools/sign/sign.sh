#!/usr/bin/env bash
# Sign one release: writes a signed checksums.txt whose header carries the version, key id,
# issue time and expiry. The header is INSIDE the signed bytes, which is what stops an
# attacker replaying an old-but-validly-signed release (rollback attack).
#
#   tools/sign/sign.sh --version 1.2.3 --key-id rel-2026a --key <release-key.pem> \
#                      [--channel stable] [--expires-days 45] <dist-dir>
#
# <dist-dir> must contain the release files (bobres_linux_amd64, ...). The script hashes them,
# writes dist/checksums.txt with the header, and signs it -> dist/checksums.txt.sig
set -euo pipefail
umask 022

die() { printf 'error: %s\n' "$*" >&2; exit 1; }
command -v openssl >/dev/null || die "openssl is required"

VERSION="" KEY_ID="" KEY="" CHANNEL="stable" DAYS=45 DIR=""
while [ "$#" -gt 0 ]; do
  case $1 in
    --version) VERSION=${2:?}; shift 2 ;;
    --key-id) KEY_ID=${2:?}; shift 2 ;;
    --key) KEY=${2:?}; shift 2 ;;
    --channel) CHANNEL=${2:?}; shift 2 ;;
    --expires-days) DAYS=${2:?}; shift 2 ;;
    -*) die "unknown flag: $1" ;;
    *) DIR=$1; shift ;;
  esac
done
[ -n "$VERSION" ] && [ -n "$KEY_ID" ] && [ -n "$KEY" ] && [ -n "$DIR" ] \
  || die "usage: sign.sh --version V --key-id ID --key FILE [--channel C] [--expires-days D] <dist-dir>"
[ -d "$DIR" ] || die "no such directory: $DIR"
[ -f "$KEY" ] || die "no such key: $KEY"
# Strip a leading v so the installer always compares bare semver.
VERSION=${VERSION#v}
case $VERSION in
  [0-9]*.[0-9]*.[0-9]*) ;;
  *) die "--version must be semver like 1.2.3 (got $VERSION)" ;;
esac
case $KEY_ID in *[!a-zA-Z0-9._-]*|"") die "bad --key-id: $KEY_ID" ;; esac
case $CHANNEL in stable|beta) ;; *) die "--channel must be stable or beta" ;; esac
case $DAYS in *[!0-9]*|"") die "--expires-days must be an integer" ;; esac

now_iso() { date -u +%Y-%m-%dT%H:%MZ; }
exp_iso() {
  if date -u -v+1d >/dev/null 2>&1; then date -u -v+"${DAYS}"d +%Y-%m-%dT%H:%MZ
  else date -u -d "+${DAYS} days" +%Y-%m-%dT%H:%MZ; fi
}

sha() { # portable sha256: "<hex>  <name>"
  if command -v sha256sum >/dev/null; then sha256sum "$@"
  else shasum -a 256 "$@"; fi
}

cd "$DIR"
files=$(find . -maxdepth 1 -type f ! -name 'checksums.txt' ! -name 'checksums.txt.sig' -exec basename {} \; | LC_ALL=C sort)
[ -n "$files" ] || die "no files to sign in $DIR"

{
  echo "# bobres-release v1"
  echo "# version $VERSION"
  echo "# channel $CHANNEL"
  echo "# key_id $KEY_ID"
  echo "# issued $(now_iso)"
  echo "# expires $(exp_iso)"
  # shellcheck disable=SC2086
  sha $files
} > checksums.txt

openssl pkeyutl -sign -inkey "$KEY" -rawin -in checksums.txt -out checksums.txt.sig
chmod 644 checksums.txt checksums.txt.sig
printf 'signed release %s (%s, key %s), %s file(s)\n' "$VERSION" "$CHANNEL" "$KEY_ID" "$(echo "$files" | wc -l | tr -d ' ')"
head -6 checksums.txt
