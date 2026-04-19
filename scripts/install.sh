#!/usr/bin/env bash
# ccsync installer — downloads the latest release binary from GitHub.
# Usage: curl -fsSL https://raw.githubusercontent.com/colinc86/ccsync/main/scripts/install.sh | bash
set -euo pipefail

REPO="colinc86/ccsync"
BINARY="ccsync"
PREFIX="${PREFIX:-$HOME/.local}"
INSTALL_DIR="$PREFIX/bin"

os() {
  case "$(uname -s)" in
    Darwin) echo "darwin" ;;
    Linux)  echo "linux" ;;
    *) echo "unsupported OS: $(uname -s)" >&2; exit 1 ;;
  esac
}

arch() {
  case "$(uname -m)" in
    x86_64|amd64) echo "x86_64" ;;
    arm64|aarch64) echo "arm64" ;;
    *) echo "unsupported arch: $(uname -m)" >&2; exit 1 ;;
  esac
}

latest_tag() {
  curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" \
    | awk -F'"' '/"tag_name":/ {print $4; exit}'
}

main() {
  local tag os_name arch_name asset url tmp
  tag="$(latest_tag)"
  if [[ -z "$tag" ]]; then
    echo "couldn't resolve latest release tag" >&2
    exit 1
  fi
  os_name="$(os)"
  arch_name="$(arch)"
  asset="${BINARY}_${tag#v}_${os_name}_${arch_name}.tar.gz"
  url="https://github.com/$REPO/releases/download/$tag/$asset"

  tmp="$(mktemp -d)"
  trap 'rm -rf "$tmp"' EXIT
  echo "downloading: $url"
  curl -fsSL "$url" -o "$tmp/$asset"
  tar -C "$tmp" -xzf "$tmp/$asset"
  mkdir -p "$INSTALL_DIR"
  mv "$tmp/$BINARY" "$INSTALL_DIR/$BINARY"
  chmod 0755 "$INSTALL_DIR/$BINARY"
  echo
  echo "installed: $INSTALL_DIR/$BINARY"
  echo
  if [[ ":$PATH:" != *":$INSTALL_DIR:"* ]]; then
    echo "NOTE: $INSTALL_DIR is not in your PATH."
    echo "      add this to your shell rc: export PATH=\"$INSTALL_DIR:\$PATH\""
  fi
}

main "$@"
