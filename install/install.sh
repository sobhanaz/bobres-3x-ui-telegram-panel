#!/usr/bin/env bash
# BOBRES bootstrap installer.
#
#   curl -fsSL https://get.example.com/install.sh | sudo bash
#   curl -fsSL https://get.example.com/install.sh | sudo bash -s -- --domain panel.example.com
#
# This script only bootstraps: it checks the OS, installs Docker if missing, downloads the signed
# `bobres` CLI, verifies its SHA-256 checksum, installs it, and hands over to `bobres install`.
# It never asks for or stores secrets itself.
set -euo pipefail

VERSION_SCRIPT="0.1.0"
BASE_URL="${BOBRES_BASE_URL:-https://get.example.com/releases/latest}"
INSTALL_DIR="${BOBRES_BIN_DIR:-/usr/local/bin}"
BIN_NAME="bobres"

if [ -t 1 ]; then
  RED=$'\033[31m'; GREEN=$'\033[32m'; YELLOW=$'\033[33m'; BOLD=$'\033[1m'; RESET=$'\033[0m'
else
  RED=""; GREEN=""; YELLOW=""; BOLD=""; RESET=""
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

Everything else is passed to 'bobres install' (for example --domain, --license).
Environment:
  BOBRES_BASE_URL  Where release files are downloaded from
  BOBRES_BIN_DIR   Where the bobres binary is installed (default /usr/local/bin)
EOF
}

# ---- argument handling (only installer-level flags are consumed) -------------
CHECK_ONLY=0
PASSTHROUGH=()
while [ "$#" -gt 0 ]; do
  case "$1" in
    -h|--help) usage; exit 0 ;;
    -v|--version) echo "install.sh ${VERSION_SCRIPT}"; exit 0 ;;
    --check) CHECK_ONLY=1 ;;
    --) shift; PASSTHROUGH+=("$@"); break ;;
    *) PASSTHROUGH+=("$1") ;;
  esac
  shift
done

# ---- root -------------------------------------------------------------------
if [ "$(id -u)" -ne 0 ]; then
  die "this installer must run as root. Try: curl -fsSL <url> | sudo bash"
fi

# ---- platform ---------------------------------------------------------------
[ "$(uname -s)" = "Linux" ] || die "only Linux is supported (Ubuntu 22.04/24.04, Debian 11/12)"
[ -r /etc/os-release ] || die "cannot read /etc/os-release; unsupported system"
# shellcheck disable=SC1091
. /etc/os-release
OS_ID="${ID:-unknown}"; OS_VER="${VERSION_ID:-unknown}"
case "${OS_ID}:${OS_VER}" in
  ubuntu:22.04|ubuntu:24.04|debian:11|debian:12) ;;
  *) die "unsupported OS ${OS_ID} ${OS_VER}. Supported: Ubuntu 22.04/24.04, Debian 11/12" ;;
esac
case "$(uname -m)" in
  x86_64|amd64) ARCH="amd64" ;;
  aarch64|arm64) ARCH="arm64" ;;
  *) die "unsupported CPU architecture $(uname -m) (need amd64 or arm64)" ;;
esac
info "System: ${OS_ID} ${OS_VER} (${ARCH})"

for tool in curl sha256sum; do
  command -v "$tool" >/dev/null 2>&1 || die "'$tool' is required but not installed (apt-get install -y curl coreutils)"
done

if [ "$CHECK_ONLY" -eq 1 ]; then
  info "Checks passed. Nothing installed (--check)."
  exit 0
fi

# ---- docker -----------------------------------------------------------------
if command -v docker >/dev/null 2>&1 && docker info >/dev/null 2>&1; then
  info "Docker already installed"
else
  info "Installing Docker (official convenience script)"
  tmp_docker="$(mktemp)"
  curl -fsSL https://get.docker.com -o "$tmp_docker"
  sh "$tmp_docker"
  rm -f "$tmp_docker"
  systemctl enable --now docker >/dev/null 2>&1 || true
  docker info >/dev/null 2>&1 || die "Docker was installed but the daemon is not reachable"
fi
docker compose version >/dev/null 2>&1 || die "Docker Compose v2 plugin is missing (apt-get install -y docker-compose-plugin)"

# ---- download + verify ------------------------------------------------------
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
ASSET="${BIN_NAME}_linux_${ARCH}"

info "Downloading ${ASSET}"
curl -fsSL --retry 3 --connect-timeout 15 -o "${WORK}/${ASSET}" "${BASE_URL}/${ASSET}" \
  || die "download failed: ${BASE_URL}/${ASSET}"
curl -fsSL --retry 3 --connect-timeout 15 -o "${WORK}/checksums.txt" "${BASE_URL}/checksums.txt" \
  || die "download failed: ${BASE_URL}/checksums.txt"

info "Verifying checksum"
EXPECTED="$(awk -v a="$ASSET" '$2 == a || $2 == "*" a {print $1; exit}' "${WORK}/checksums.txt")"
[ -n "$EXPECTED" ] || die "no checksum listed for ${ASSET}; refusing to install"
ACTUAL="$(sha256sum "${WORK}/${ASSET}" | awk '{print $1}')"
if [ "$EXPECTED" != "$ACTUAL" ]; then
  die "checksum mismatch for ${ASSET} (expected ${EXPECTED}, got ${ACTUAL}); aborting"
fi

# ---- install ----------------------------------------------------------------
install -d -m 0755 "$INSTALL_DIR"
install -m 0755 "${WORK}/${ASSET}" "${INSTALL_DIR}/${BIN_NAME}"
info "Installed ${INSTALL_DIR}/${BIN_NAME} ($("${INSTALL_DIR}/${BIN_NAME}" version))"

info "Starting ${BOLD}bobres install${RESET}"
exec "${INSTALL_DIR}/${BIN_NAME}" install ${PASSTHROUGH[@]+"${PASSTHROUGH[@]}"}
