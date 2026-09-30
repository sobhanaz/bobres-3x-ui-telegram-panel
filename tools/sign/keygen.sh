#!/usr/bin/env bash
# Generate the BOBRES trust hierarchy.
#
#   tools/sign/keygen.sh roots <out-dir>          # two offline ROOT keys (do this once, then back them up)
#   tools/sign/keygen.sh release <id> <out-dir>   # one release signing key (goes into CI)
#
# Root keys sign only the key list (keys.txt). The release key signs each release's
# checksums.txt. That split means a stolen release key can be revoked by publishing a new
# keys.txt, without customers reinstalling anything.
set -euo pipefail
umask 077

die() { printf 'error: %s\n' "$*" >&2; exit 1; }

command -v openssl >/dev/null || die "openssl is required"

fingerprint() { # sha256 of the DER public key, first 16 bytes, uppercase hex in groups of 4
  openssl pkey -pubin -in "$1" -outform DER 2>/dev/null \
    | openssl dgst -sha256 -binary \
    | head -c 16 | od -An -tx1 | tr -d ' \n' | tr 'a-f' 'A-F' \
    | sed 's/..../& /g; s/ $//'
}

cmd_roots() {
  local out=${1:?usage: keygen.sh roots <out-dir>}
  mkdir -p "$out"
  local i
  for i in 1 2; do
    local key="$out/root-${i}.key.pem" pub="$out/root-${i}.pub.pem"
    [ -e "$key" ] && die "$key already exists; refusing to overwrite a root key"
    openssl genpkey -algorithm ed25519 -out "$key"
    chmod 600 "$key"
    openssl pkey -in "$key" -pubout -out "$pub"
    printf 'root-%s fingerprint: %s\n' "$i" "$(fingerprint "$pub")"
  done
  cat <<EOF

Two root keys were written to: $out
  root-1.key.pem  root-2.key.pem   <- PRIVATE. Never commit. Store in two different places.
  root-1.pub.pem  root-2.pub.pem   <- public; these get embedded in install.sh

Next:
  1. Back up BOTH private keys now (password manager + an offline copy).
  2. Publish the fingerprints above wherever customers can check them (docs, website, channel).
  3. Root keys are used only to sign keys.txt. Keep them offline; never put them in CI.
EOF
}

cmd_release() {
  local id=${1:?usage: keygen.sh release <key-id> <out-dir>} out=${2:?usage: keygen.sh release <key-id> <out-dir>}
  case $id in
    *[!a-zA-Z0-9._-]*|"") die "key id must be [a-zA-Z0-9._-]+ (e.g. rel-2026a)" ;;
  esac
  mkdir -p "$out"
  local key="$out/${id}.key.pem" pub="$out/${id}.pub.pem"
  [ -e "$key" ] && die "$key already exists"
  openssl genpkey -algorithm ed25519 -out "$key"
  chmod 600 "$key"
  openssl pkey -in "$key" -pubout -out "$pub"
  printf 'release key %s fingerprint: %s\n' "$id" "$(fingerprint "$pub")"
  cat <<EOF

Release key written to: $out
  ${id}.key.pem   <- PRIVATE: store as the GitHub environment secret RELEASE_SIGNING_KEY
  ${id}.pub.pem   <- public: add it to keys.txt with tools/sign/keylist.sh

This key is replaceable: if it leaks, revoke it in keys.txt and issue a new one.
EOF
}

case ${1:-} in
  roots)   shift; cmd_roots "$@" ;;
  release) shift; cmd_release "$@" ;;
  *) die "usage: keygen.sh roots <out-dir> | keygen.sh release <key-id> <out-dir>" ;;
esac
