#!/bin/sh
# Vesta CLI installer.
#
#   curl -fsSL https://raw.githubusercontent.com/vesta-infra/vesta-kubernetes/develop/install.sh | sh
#
# Environment variables:
#   VESTA_VERSION      version to install, e.g. 0.6.2 (default: latest release)
#   VESTA_INSTALL_DIR  install directory (default: /usr/local/bin)

set -eu

REPO="vesta-infra/vesta-kubernetes"
INSTALL_DIR="${VESTA_INSTALL_DIR:-/usr/local/bin}"
VERSION="${VESTA_VERSION:-}"

log() { printf '%s\n' "$*" >&2; }
die() { printf 'error: %s\n' "$*" >&2; exit 1; }

need() { command -v "$1" >/dev/null 2>&1 || die "$1 is required but not installed"; }

detect_platform() {
  os=$(uname -s | tr '[:upper:]' '[:lower:]')
  arch=$(uname -m)

  case "$os" in
    linux)   os=linux ;;
    darwin)  os=darwin ;;
    *) die "unsupported OS: $os (Windows users: download the .zip from https://github.com/$REPO/releases)" ;;
  esac

  case "$arch" in
    x86_64|amd64)  arch=amd64 ;;
    arm64|aarch64) arch=arm64 ;;
    *) die "unsupported architecture: $arch" ;;
  esac

  PLATFORM="${os}_${arch}"
}

latest_version() {
  # Resolve the tag the GitHub "latest release" redirect points at, so this
  # works without an API token and without jq.
  url=$(curl -fsSLI -o /dev/null -w '%{url_effective}' \
    "https://github.com/$REPO/releases/latest")
  case "$url" in
    */releases/tag/*) tag="${url##*/tag/}" ;;
    *) die "no published releases found for $REPO -- set VESTA_VERSION to install a specific version" ;;
  esac
  printf '%s' "${tag#v}"
}

main() {
  need curl
  need tar

  detect_platform

  if [ -z "$VERSION" ]; then
    log "Resolving latest release..."
    VERSION=$(latest_version)
  fi
  VERSION="${VERSION#v}"

  archive="vesta_${VERSION}_${PLATFORM}.tar.gz"
  base="https://github.com/$REPO/releases/download/v${VERSION}"

  tmp=$(mktemp -d)
  trap 'rm -rf "$tmp"' EXIT INT TERM

  log "Downloading vesta ${VERSION} (${PLATFORM})..."
  curl -fsSL "$base/$archive" -o "$tmp/$archive" \
    || die "download failed: $base/$archive"

  # Checksum verification is best-effort: skip only if no sha tool is present.
  if curl -fsSL "$base/checksums.txt" -o "$tmp/checksums.txt" 2>/dev/null; then
    expected=$(grep " $archive\$" "$tmp/checksums.txt" | awk '{print $1}')
    if [ -n "$expected" ]; then
      if command -v sha256sum >/dev/null 2>&1; then
        actual=$(sha256sum "$tmp/$archive" | awk '{print $1}')
      elif command -v shasum >/dev/null 2>&1; then
        actual=$(shasum -a 256 "$tmp/$archive" | awk '{print $1}')
      else
        actual=""
        log "warning: no sha256 tool found, skipping checksum verification"
      fi
      if [ -n "$actual" ] && [ "$actual" != "$expected" ]; then
        die "checksum mismatch for $archive (expected $expected, got $actual)"
      fi
    fi
  fi

  tar -xzf "$tmp/$archive" -C "$tmp"
  [ -f "$tmp/vesta" ] || die "archive did not contain a vesta binary"
  chmod +x "$tmp/vesta"

  if [ -w "$INSTALL_DIR" ] || { [ ! -e "$INSTALL_DIR" ] && mkdir -p "$INSTALL_DIR" 2>/dev/null; }; then
    mv "$tmp/vesta" "$INSTALL_DIR/vesta"
  else
    log "Installing to $INSTALL_DIR (requires sudo)..."
    need sudo
    sudo mkdir -p "$INSTALL_DIR"
    sudo mv "$tmp/vesta" "$INSTALL_DIR/vesta"
  fi

  log "Installed $INSTALL_DIR/vesta"
  case ":$PATH:" in
    *":$INSTALL_DIR:"*) ;;
    *) log "warning: $INSTALL_DIR is not on your PATH" ;;
  esac
  "$INSTALL_DIR/vesta" version
}

main "$@"
