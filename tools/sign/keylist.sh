#!/usr/bin/env bash
# Build and sign the BOBRES key list (keys.txt), which tells installers WHICH release keys
# are currently trusted. Signed by a ROOT key (offline), so a compromised release key can be
# revoked without customers reinstalling.
#
#   tools/sign/keylist.sh build  --seq N --expires-days D --min-version V \
#                                --key ID=path/to/ID.pub.pem [--key ...] [--revoke ID] -o keys.txt
#   tools/sign/keylist.sh sign   keys.txt <root-private-key.pem> [more-root-keys...]
#
# Format (line based so the installer can parse it with plain awk/grep):
#   bobres-keys v1
#   seq <monotonic integer>        # installers refuse a LOWER seq (anti-rollback)
#   expires <YYYY-MM-DDTHH:MMZ>    # installers refuse an expired list
#   min_version <semver>           # oldest release version still acceptable
#   revoked <key-id>               # zero or more
#   key <key-id> <base64 of the DER public key>
set -euo pipefail
umask 077

die() { printf 'error: %s\n' "$*" >&2; exit 1; }
command -v openssl >/dev/null || die "openssl is required"

pub_b64() { openssl pkey -pubin -in "$1" -outform DER | openssl base64 -A; }

cmd_build() {
  local seq="" days="" minver="" out="" ; local -a keys=() revoked=()
  while [ "$#" -gt 0 ]; do
    case $1 in
      --seq) seq=${2:?}; shift 2 ;;
      --expires-days) days=${2:?}; shift 2 ;;
      --min-version) minver=${2:?}; shift 2 ;;
      --key) keys+=("${2:?}"); shift 2 ;;
      --revoke) revoked+=("${2:?}"); shift 2 ;;
      -o) out=${2:?}; shift 2 ;;
      *) die "unknown flag: $1" ;;
    esac
  done
  [ -n "$seq" ] && [ -n "$days" ] && [ -n "$minver" ] && [ -n "$out" ] || die "need --seq --expires-days --min-version -o"
  [ "${#keys[@]}" -gt 0 ] || die "at least one --key ID=path is required"
  case $seq in *[!0-9]*|"") die "--seq must be an integer" ;; esac

  local expires
  if date -u -v+1d >/dev/null 2>&1; then
    expires=$(date -u -v+"${days}"d +%Y-%m-%dT%H:%MZ)   # BSD/macOS
  else
    expires=$(date -u -d "+${days} days" +%Y-%m-%dT%H:%MZ)  # GNU
  fi

  {
    echo "bobres-keys v1"
    echo "seq $seq"
    echo "expires $expires"
    echo "min_version $minver"
    local r
    for r in ${revoked[@]+"${revoked[@]}"}; do echo "revoked $r"; done
    local spec id path
    for spec in "${keys[@]}"; do
      id=${spec%%=*}; path=${spec#*=}
      [ "$id" != "$spec" ] || die "--key needs ID=path form, got: $spec"
      [ -f "$path" ] || die "no such public key: $path"
      case $id in *[!a-zA-Z0-9._-]*) die "bad key id: $id" ;; esac
      echo "key $id $(pub_b64 "$path")"
    done
  } > "$out"
  chmod 644 "$out"
  printf 'wrote %s (seq %s, expires %s, min_version %s, %s key(s), %s revoked)\n' \
    "$out" "$seq" "$expires" "$minver" "${#keys[@]}" "${#revoked[@]}"
}

# Sign with EACH root key: keys.txt.sig.1, keys.txt.sig.2 ... Installers accept the list if
# ANY embedded root verifies ANY signature, so losing one root key is survivable.
cmd_sign() {
  local file=${1:?usage: keylist.sh sign <keys.txt> <root-key.pem> [...]}; shift
  [ -f "$file" ] || die "no such file: $file"
  [ "$#" -gt 0 ] || die "at least one root private key is required"
  local i=0 key
  for key in "$@"; do
    [ -f "$key" ] || die "no such key: $key"
    i=$((i + 1))
    openssl pkeyutl -sign -inkey "$key" -rawin -in "$file" -out "${file}.sig.${i}"
    chmod 644 "${file}.sig.${i}"
    printf 'signed: %s.sig.%s\n' "$file" "$i"
  done
}

case ${1:-} in
  build) shift; cmd_build "$@" ;;
  sign)  shift; cmd_sign "$@" ;;
  *) die "usage: keylist.sh build ... | keylist.sh sign <keys.txt> <root-key.pem> [...]" ;;
esac
