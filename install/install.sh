#!/usr/bin/env bash
# BOBRES bootstrap installer.
#
#   curl -fsSL https://get.example.com/install.sh | sudo bash
#   curl -fsSL https://get.example.com/install.sh | sudo bash -s -- --domain panel.example.com
#
# What this does: checks the OS, installs Docker if missing, downloads the `bobres` CLI,
# VERIFIES the release signature and checksum, installs it, then hands over to `bobres install`.
#
# Trust model (read this): the vendor Ed25519 public key below is embedded in this script.
# `checksums.txt` must carry a valid signature (`checksums.txt.sig`) from the matching private
# key, and the binary must match the signed checksum. A compromised download host therefore
# cannot substitute a binary. The remaining trust roots are THIS script (fetched over HTTPS,
# so pin/verify it if you can) and Docker's official installer (https://get.docker.com).
#
# The whole script is wrapped in main() and only runs on the last line, so a truncated
# download can never execute a partial script.
set -euo pipefail

VERSION_SCRIPT="0.2.0"
BIN_NAME="bobres"
WORK=""

# Vendor release-signing public key (Ed25519). Rotation: list several keys here for one release.
read -r -d '' RELEASE_PUBKEY_1 <<'PUBKEY' || true
-----BEGIN PUBLIC KEY-----
MCowBQYDK2VwAyEA6KBVWYbiD2b9Z5dWPYw3DB/0KIHad7spYISBFLfbdGw=
-----END PUBLIC KEY-----
PUBKEY
RELEASE_PUBKEYS=("$RELEASE_PUBKEY_1")

main() {
  export PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
  umask 022

  local BASE_URL="${BOBRES_BASE_URL:-https://get.example.com/releases/latest}"
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
  [ "$(id -u)" -eq 0 ] || die "this installer must run as root. Try: curl -fsSL <url> | sudo bash"

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

  # ---- verify signature over checksums.txt, then the binary against it ----
  info "Verifying release signature"
  local verified=0 i=0 key
  for key in "${RELEASE_PUBKEYS[@]}"; do
    i=$((i + 1))
    printf '%s\n' "$key" > "${WORK}/pub${i}.pem"
    if openssl pkeyutl -verify -pubin -inkey "${WORK}/pub${i}.pem" -rawin \
         -in "${WORK}/checksums.txt" -sigfile "${WORK}/checksums.txt.sig" >/dev/null 2>&1; then
      verified=1
      break
    fi
  done
  [ "$verified" -eq 1 ] || die "release signature is INVALID; refusing to install (the download may have been tampered with)"

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
main "$@"
