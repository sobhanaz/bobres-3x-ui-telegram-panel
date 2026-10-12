#!/usr/bin/env bash
# BOBRES bootstrap installer. One command, as root:
#
#   bash <(curl -fsSL https://raw.githubusercontent.com/sobhanaz/bobres-3x-ui-telegram-panel/main/install/install.sh)
#
# or with sudo, optionally passing `bobres install` flags after `-s --`:
#
#   curl -fsSL https://raw.githubusercontent.com/sobhanaz/bobres-3x-ui-telegram-panel/main/install/install.sh | sudo bash
#   curl -fsSL .../install.sh | sudo bash -s -- --domain panel.example.com
#
# Afterwards, run `bobres` for the management menu.
#
# What this does: checks the OS, installs Docker if missing, downloads the `bobres` CLI,
# VERIFIES the release signature and checksum, installs it, then hands over to `bobres install`.
#
# Trust model (read this): two vendor ROOT Ed25519 public keys are embedded below. They sign
# `keys.txt` (trusted release keys, with expiry, sequence number and revocations). A release
# key from that list signs `checksums.txt` (which carries version and expiry), and the binary
# must match the signed checksum. A compromised download host therefore
# cannot substitute a binary. The remaining trust roots are THIS script (fetched over HTTPS,
# so pin/verify it if you can) and Docker's official installer (https://get.docker.com).
#
# The whole script is wrapped in main() and only runs on the last line, so a truncated
# download can never execute a partial script.
set -euo pipefail

VERSION_SCRIPT="0.2.0"
BIN_NAME="bobres"
WORK=""

# ROOT public keys (Ed25519). Roots sign only keys.txt (the list of trusted release keys);
# a release key signs each release's checksums.txt. Any one root is enough to accept keys.txt,
# so losing one root is survivable. Rotate roots by shipping a new install.sh.
read -r -d '' RELEASE_ROOT_1 <<'PUBKEY' || true
-----BEGIN PUBLIC KEY-----
MCowBQYDK2VwAyEAKZqHG4ociEUeAtlxJX89TxKY7c3EEWS8JQHUKXBUKZw=
-----END PUBLIC KEY-----
PUBKEY
read -r -d '' RELEASE_ROOT_2 <<'PUBKEY' || true
-----BEGIN PUBLIC KEY-----
MCowBQYDK2VwAyEADz7vJ+DMVvKQyotUaJSpnIOlvdbig/KAUzO49wfq3ng=
-----END PUBLIC KEY-----
PUBKEY
RELEASE_ROOTS=("$RELEASE_ROOT_1" "$RELEASE_ROOT_2")

# semver_num N -> N if it is a number (digits only), else 0
semver_num() {
  case $1 in ""|*[!0-9]*) echo 0 ;; *) echo "$1" ;; esac
}

# semver_lt A B -> success if A < B, in semver 2.0 order (internal/release.Compare): X.Y.Z,
# then a pre-release sorts before its release (1.2.0-rc.1 < 1.2.0), and pre-releases compare
# identifier by identifier: numbers as numbers (rc.9 < rc.10) and before words, words in
# ASCII order, and more identifiers after fewer. A leading v and +build are ignored.
semver_lt() {
  local a=${1#v} b=${2#v} apre="" bpre="" a1 a2 a3 b1 b2 b3 x y
  a=${a%%+*}; b=${b%%+*}
  case $a in *-*) apre=${a#*-}; a=${a%%-*} ;; esac
  case $b in *-*) bpre=${b#*-}; b=${b%%-*} ;; esac
  IFS=. read -r a1 a2 a3 <<<"$a"
  IFS=. read -r b1 b2 b3 <<<"$b"
  a1=$(semver_num "${a1:-}"); a2=$(semver_num "${a2:-}"); a3=$(semver_num "${a3:-}")
  b1=$(semver_num "${b1:-}"); b2=$(semver_num "${b2:-}"); b3=$(semver_num "${b3:-}")
  [ "$a1" -lt "$b1" ] && return 0; [ "$a1" -gt "$b1" ] && return 1
  [ "$a2" -lt "$b2" ] && return 0; [ "$a2" -gt "$b2" ] && return 1
  [ "$a3" -lt "$b3" ] && return 0; [ "$a3" -gt "$b3" ] && return 1
  # the same X.Y.Z: a release is above its pre-releases
  [ "$apre" = "$bpre" ] && return 1
  [ -z "$apre" ] && return 1
  [ -z "$bpre" ] && return 0
  while :; do
    [ -z "$apre" ] && return 0   # A ran out first: fewer identifiers sort first
    [ -z "$bpre" ] && return 1
    x=${apre%%.*}; y=${bpre%%.*}
    case $apre in *.*) apre=${apre#*.} ;; *) apre="" ;; esac
    case $bpre in *.*) bpre=${bpre#*.} ;; *) bpre="" ;; esac
    [ "$x" = "$y" ] && continue
    case "$x" in
      ""|*[!0-9]*)
        case "$y" in ""|*[!0-9]*) [ "$x" \< "$y" ]; return ;; esac
        return 1 ;;                # a word sorts after a number
      *)
        case "$y" in ""|*[!0-9]*) return 0 ;; esac
        semver_numlt "$x" "$y"; return ;;
    esac
  done
}

# semver_numlt X Y -> success if the number X < Y (digits only, any length: compared without
# leading zeros by length, then digit by digit; the same number written differently, 01 and 1,
# by its text)
semver_numlt() {
  local x=${1#"${1%%[!0]*}"} y=${2#"${2%%[!0]*}"}
  x=${x:-0}; y=${y:-0}
  [ "${#x}" -lt "${#y}" ] && return 0; [ "${#x}" -gt "${#y}" ] && return 1
  [ "$x" = "$y" ] && { [ "$1" \< "$2" ]; return; }
  [ "$x" \< "$y" ]
}

# release_channel VALUE -> the update channel install.sh takes releases from (stable or beta,
# as internal/release.ParseChannel reads BOBRES_UPDATE_CHANNEL: empty is stable, any case);
# fails on anything else.
release_channel() {
  local c
  c=$(printf '%s' "$1" | tr '[:upper:]' '[:lower:]' | tr -d '[:space:]')
  case $c in
    ""|stable) echo stable ;;
    beta) echo beta ;;
    *) return 1 ;;
  esac
}

# verify_release DIR STATE_DIR [CHANNEL]
# DIR holds checksums.txt, checksums.txt.sig, keys.txt, keys.txt.sig.N. Prints the verified
# version on success (last line "VERSION=x.y.z" is not used; use RELEASE_VERSION/KEYS_SEQ globals).
# On failure prints a reason to stderr and returns 1. Nothing is written to STATE_DIR here.
# CHANNEL (default stable) is the channel the install follows: stable refuses a release whose
# signed header says "channel beta" (internal/release.ChannelAllows), so a pre-release that ends
# up as GitHub's latest release is not installed unless BOBRES_UPDATE_CHANNEL=beta asks for it.
verify_release() {
  local dir=$1 state=$2 channel=${3:-stable} now root ok=0 n=0 f
  now=$(date -u +%Y-%m-%dT%H:%MZ)

  # 1. keys.txt must carry a valid signature from ANY embedded root
  for f in "$dir"/keys.txt.sig.*; do
    [ -e "$f" ] || continue
    for root in "${RELEASE_ROOTS[@]}"; do
      n=$((n + 1))
      printf '%s\n' "$root" > "$dir/root${n}.pem"
      if openssl pkeyutl -verify -pubin -inkey "$dir/root${n}.pem" -rawin \
           -in "$dir/keys.txt" -sigfile "$f" >/dev/null 2>&1; then ok=1; break 2; fi
    done
  done
  [ "$ok" -eq 1 ] || { echo "key list signature is INVALID (no embedded root verified it)" >&2; return 1; }

  # 2. key list freshness, anti-rollback
  [ "$(sed -n 1p "$dir/keys.txt")" = "bobres-keys v1" ] || { echo "unknown key list format" >&2; return 1; }
  local seq kexp minver
  seq=$(awk '$1=="seq"{print $2; exit}' "$dir/keys.txt")
  kexp=$(awk '$1=="expires"{print $2; exit}' "$dir/keys.txt")
  minver=$(awk '$1=="min_version"{print $2; exit}' "$dir/keys.txt")
  case $seq in ""|*[!0-9]*) echo "bad key list seq" >&2; return 1 ;; esac
  [ -n "$kexp" ] && [ -n "$minver" ] || { echo "key list is missing expires/min_version" >&2; return 1; }
  [ "$now" \< "$kexp" ] || { echo "key list expired at $kexp (check the system clock, or upgrade install.sh)" >&2; return 1; }
  if [ -f "$state/keys.seq" ]; then
    local last; last=$(cat "$state/keys.seq")
    case $last in ""|*[!0-9]*) last=0 ;; esac
    [ "$seq" -ge "$last" ] || { echo "key list seq $seq is older than the last seen $last (rollback attempt?)" >&2; return 1; }
  fi

  # 3. release header (inside signed bytes)
  local ver kid rexp
  ver=$(awk '$1=="#" && $2=="version"{print $3; exit}' "$dir/checksums.txt")
  kid=$(awk '$1=="#" && $2=="key_id"{print $3; exit}' "$dir/checksums.txt")
  rexp=$(awk '$1=="#" && $2=="expires"{print $3; exit}' "$dir/checksums.txt")
  [ -n "$ver" ] && [ -n "$kid" ] && [ -n "$rexp" ] || { echo "release header is missing version/key_id/expires" >&2; return 1; }
  if awk -v k="$kid" '$1=="revoked" && $2==k {f=1} END{exit !f}' "$dir/keys.txt"; then
    echo "release key $kid is REVOKED" >&2; return 1
  fi
  local b64
  b64=$(awk -v k="$kid" '$1=="key" && $2==k {print $3; exit}' "$dir/keys.txt")
  [ -n "$b64" ] || { echo "release key $kid is not in the signed key list" >&2; return 1; }
  { echo "-----BEGIN PUBLIC KEY-----"; printf '%s' "$b64" | fold -w 64; echo; echo "-----END PUBLIC KEY-----"; } > "$dir/rel.pem"
  openssl pkeyutl -verify -pubin -inkey "$dir/rel.pem" -rawin \
    -in "$dir/checksums.txt" -sigfile "$dir/checksums.txt.sig" >/dev/null 2>&1 \
    || { echo "release signature is INVALID; refusing to install (the download may have been tampered with)" >&2; return 1; }
  [ "$now" \< "$rexp" ] || { echo "release metadata expired at $rexp (stale or replayed release)" >&2; return 1; }
  if semver_lt "$ver" "$minver"; then echo "release $ver is below the minimum allowed $minver" >&2; return 1; fi
  local rchan
  rchan=$(awk '$1=="#" && $2=="channel"{print $3; exit}' "$dir/checksums.txt")
  if [ "$rchan" = "beta" ] && [ "$channel" != "beta" ]; then
    echo "release $ver is a beta release (a pre-release) and this install follows stable: to install it, run install.sh with BOBRES_UPDATE_CHANNEL=beta" >&2; return 1
  fi
  if [ -f "$state/release.version" ]; then
    local cur; cur=$(cat "$state/release.version")
    if [ -n "$cur" ] && semver_lt "$ver" "$cur"; then echo "release $ver is older than installed $cur (downgrade refused)" >&2; return 1; fi
  fi
  RELEASE_VERSION=$ver; KEYS_SEQ=$seq
  return 0
}

main() {
  export PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
  umask 022

  # The latest (non-pre-release) GitHub release of this repository.
  local BASE_URL="${BOBRES_BASE_URL:-https://github.com/sobhanaz/bobres-3x-ui-telegram-panel/releases/latest/download}"
  # The channel followed: stable refuses a release signed for beta, wherever it is downloaded
  # from (even when GitHub serves it as the latest release).
  local CHANNEL
  local INSTALL_DIR="${BOBRES_BIN_DIR:-/usr/local/bin}"
  local RED="" GREEN="" YELLOW="" BOLD="" RESET=""
  if [ -t 1 ]; then
    RED=$'\033[31m'; GREEN=$'\033[32m'; YELLOW=$'\033[33m'; BOLD=$'\033[1m'; RESET=$'\033[0m'
  fi
  info() { printf '%s==>%s %s\n' "$GREEN" "$RESET" "$*"; }
  warn() { printf '%swarning:%s %s\n' "$YELLOW" "$RESET" "$*" >&2; }
  die()  { printf '%serror:%s %s\n' "$RED" "$RESET" "$*" >&2; exit 1; }

  usage() {
    cat <<EOF
BOBRES installer ${VERSION_SCRIPT}

Usage: install.sh [installer flags] [-- bobres install flags]

Installer flags:
  -h, --help       Show this help
  -v, --version    Show the installer version
  --check          Only run system checks, install nothing

Everything else is passed to 'bobres install' (for example --domain).
Pass your license with the BOBRES_LICENSE environment variable (not on the command line,
where it would show up in 'ps' and shell history).

Environment:
  BOBRES_BASE_URL  Where release files are downloaded from (must be https://)
  BOBRES_BIN_DIR   Where the bobres binary is installed (default /usr/local/bin)
  BOBRES_UPDATE_CHANNEL
                   stable (default) or beta. On stable a release signed for beta (a
                   pre-release) is refused; set beta, with BOBRES_BASE_URL pointing at
                   the pre-release, to install one.
EOF
  }

  local CHECK_ONLY=0
  local -a PASSTHROUGH=()
  while [ "$#" -gt 0 ]; do
    case "$1" in
      -h|--help) usage; return 0 ;;
      -v|--version) echo "install.sh ${VERSION_SCRIPT}"; return 0 ;;
      --check) CHECK_ONLY=1 ;;
      --license|--license=*) die "pass the license via the BOBRES_LICENSE environment variable, not on the command line" ;;
      --) shift; PASSTHROUGH+=("$@"); break ;;
      *) PASSTHROUGH+=("$1") ;;
    esac
    shift
  done

  CHANNEL="$(release_channel "${BOBRES_UPDATE_CHANNEL:-}")" \
    || die "BOBRES_UPDATE_CHANNEL must be stable or beta (got '${BOBRES_UPDATE_CHANNEL:-}')"

  # ---- transport: HTTPS only (tests may set BOBRES_ALLOW_INSECURE_HTTP=1) ----
  case "$BASE_URL" in
    https://*) ;;
    http://*) [ "${BOBRES_ALLOW_INSECURE_HTTP:-0}" = "1" ] || die "BOBRES_BASE_URL must start with https://" ;;
    *) die "BOBRES_BASE_URL must start with https://" ;;
  esac
  local -a CURL=(curl -fsSL --retry 3 --connect-timeout 15 --max-time 300)
  if [ "${BOBRES_ALLOW_INSECURE_HTTP:-0}" != "1" ]; then
    CURL+=(--proto '=https' --tlsv1.2)
  fi

  # ---- root ----
  [ "$(id -u)" -eq 0 ] || die "this installer must run as root. Try: curl -fsSL https://raw.githubusercontent.com/sobhanaz/bobres-3x-ui-telegram-panel/main/install/install.sh | sudo bash"

  # ---- platform ----
  [ "$(uname -s)" = "Linux" ] || die "only Linux is supported (Ubuntu 22.04/24.04, Debian 11/12/13)"
  [ -r /etc/os-release ] || die "cannot read /etc/os-release; unsupported system"
  local OS_ID OS_VER ARCH
  # shellcheck disable=SC1091
  OS_ID="$(. /etc/os-release && echo "${ID:-unknown}")"
  # shellcheck disable=SC1091
  OS_VER="$(. /etc/os-release && echo "${VERSION_ID:-unknown}")"
  case "${OS_ID}:${OS_VER}" in
    ubuntu:22.04|ubuntu:24.04|debian:11|debian:12|debian:13) ;;
    *) die "unsupported OS ${OS_ID} ${OS_VER}. Supported: Ubuntu 22.04/24.04, Debian 11/12/13" ;;
  esac
  case "$(uname -m)" in
    x86_64|amd64) ARCH="amd64" ;;
    aarch64|arm64) ARCH="arm64" ;;
    *) die "unsupported CPU architecture $(uname -m) (need amd64 or arm64)" ;;
  esac
  info "System: ${OS_ID} ${OS_VER} (${ARCH})"

  # ---- prerequisites (openssl verifies the release signature) ----
  local tool
  for tool in curl sha256sum awk; do
    command -v "$tool" >/dev/null 2>&1 || die "'$tool' is required but not installed (apt-get install -y curl coreutils gawk)"
  done
  if ! command -v openssl >/dev/null 2>&1; then
    if [ "$CHECK_ONLY" -eq 1 ]; then
      warn "openssl is missing; the real install will install it"
    else
      info "Installing openssl (needed to verify the release signature)"
      DEBIAN_FRONTEND=noninteractive apt-get update -qq </dev/null >/dev/null 2>&1 || true
      DEBIAN_FRONTEND=noninteractive apt-get install -y -qq openssl </dev/null >/dev/null 2>&1 || die "could not install openssl"
    fi
  fi

  if [ "$CHECK_ONLY" -eq 1 ]; then
    info "Checks passed. Nothing installed (--check)."
    return 0
  fi

  # ---- docker (children get </dev/null so they cannot eat the rest of a piped script) ----
  if command -v docker >/dev/null 2>&1 && docker info >/dev/null 2>&1; then
    info "Docker already installed"
  else
    info "Installing Docker (official convenience script from https://get.docker.com)"
    local tmp_docker
    tmp_docker="$(mktemp)"
    "${CURL[@]}" https://get.docker.com -o "$tmp_docker"
    sh "$tmp_docker" </dev/null
    rm -f "$tmp_docker"
    systemctl enable --now docker </dev/null >/dev/null 2>&1 || true
    docker info >/dev/null 2>&1 || die "Docker was installed but the daemon is not reachable"
  fi
  docker compose version >/dev/null 2>&1 || die "Docker Compose v2 plugin is missing (apt-get install -y docker-compose-plugin)"

  # ---- download ----
  local ASSET
  WORK="$(mktemp -d)"   # global on purpose: the EXIT trap runs after main() has returned
  trap 'rm -rf "${WORK:-}"' EXIT
  ASSET="${BIN_NAME}_linux_${ARCH}"

  info "Downloading release files"
  "${CURL[@]}" -o "${WORK}/${ASSET}" "${BASE_URL}/${ASSET}" || die "download failed: ${BASE_URL}/${ASSET}"
  "${CURL[@]}" -o "${WORK}/checksums.txt" "${BASE_URL}/checksums.txt" || die "download failed: ${BASE_URL}/checksums.txt"
  "${CURL[@]}" -o "${WORK}/checksums.txt.sig" "${BASE_URL}/checksums.txt.sig" || die "download failed: ${BASE_URL}/checksums.txt.sig (releases must be signed)"
  "${CURL[@]}" -o "${WORK}/keys.txt" "${BASE_URL}/keys.txt" || die "download failed: ${BASE_URL}/keys.txt"
  local n
  for n in 1 2 3 4; do
    "${CURL[@]}" -o "${WORK}/keys.txt.sig.${n}" "${BASE_URL}/keys.txt.sig.${n}" 2>/dev/null || rm -f "${WORK}/keys.txt.sig.${n}"
  done

  info "Verifying release signature"
  local STATE_DIR="${BOBRES_STATE_DIR:-/var/lib/bobres}"
  verify_release "$WORK" "$STATE_DIR" "$CHANNEL" || die "verification failed; refusing to install"

  info "Verifying checksum"
  local EXPECTED ACTUAL
  EXPECTED="$(awk -v a="$ASSET" '$2 == a || $2 == "*" a {print $1; exit}' "${WORK}/checksums.txt")"
  [ -n "$EXPECTED" ] || die "no checksum listed for ${ASSET}; refusing to install"
  ACTUAL="$(sha256sum "${WORK}/${ASSET}" | awk '{print $1}')"
  [ "$EXPECTED" = "$ACTUAL" ] || die "checksum mismatch for ${ASSET} (expected ${EXPECTED}, got ${ACTUAL}); aborting"

  # ---- install atomically: copy next to the target, then rename ----
  install -d -m 0755 "$INSTALL_DIR"
  install -m 0755 "${WORK}/${ASSET}" "${INSTALL_DIR}/.${BIN_NAME}.new"
  mv -f "${INSTALL_DIR}/.${BIN_NAME}.new" "${INSTALL_DIR}/${BIN_NAME}"
  install -d -m 0755 "$STATE_DIR"
  printf '%s\n' "$KEYS_SEQ" > "$STATE_DIR/keys.seq"
  printf '%s\n' "$RELEASE_VERSION" > "$STATE_DIR/release.version"
  info "Installed ${INSTALL_DIR}/${BIN_NAME} ($("${INSTALL_DIR}/${BIN_NAME}" version </dev/null))"

  info "Starting ${BOLD}bobres install${RESET}"
  # Interactive prompts need a real terminal, not the (already consumed) pipe. /dev/tty can
  # exist yet be unopenable (no controlling terminal: CI, cron, docker without -t), so test
  # by opening it, and fall back to /dev/null (non-interactive flags are then required).
  if { : </dev/tty; } 2>/dev/null; then
    exec "${INSTALL_DIR}/${BIN_NAME}" install ${PASSTHROUGH[@]+"${PASSTHROUGH[@]}"} </dev/tty
  fi
  exec "${INSTALL_DIR}/${BIN_NAME}" install ${PASSTHROUGH[@]+"${PASSTHROUGH[@]}"} </dev/null
}

# Runs only if the whole file was downloaded (this is the last line).
[ "${BOBRES_INSTALL_SOURCE_ONLY:-0}" = "1" ] || main "$@"
