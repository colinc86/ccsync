#!/usr/bin/env bash
# ccsync installer — downloads a release binary from GitHub.
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/colinc86/ccsync/main/scripts/install.sh | bash
#
# Environment:
#   VERSION   pin a specific release tag (e.g. VERSION=v0.2.0); defaults to latest
#   PREFIX    install prefix; binary goes under $PREFIX/bin (default: ~/.local)
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

# auth_header echoes a curl -H value ("Authorization: Bearer …") when a
# token is available, empty otherwise. Private-fork installs work by
# piggybacking on whatever auth the user already has set up: GH_TOKEN,
# GITHUB_TOKEN, or the GitHub CLI. Public repos need none of this.
auth_header() {
  local tok=""
  if [[ -n "${GH_TOKEN:-}" ]]; then
    tok="$GH_TOKEN"
  elif [[ -n "${GITHUB_TOKEN:-}" ]]; then
    tok="$GITHUB_TOKEN"
  elif command -v gh >/dev/null 2>&1; then
    tok="$(gh auth token 2>/dev/null || true)"
  fi
  if [[ -n "$tok" ]]; then
    echo "Authorization: Bearer $tok"
  fi
}

latest_tag() {
  local auth; auth="$(auth_header)"
  local body
  if [[ -n "$auth" ]]; then
    body="$(curl -fsSL -H "$auth" "https://api.github.com/repos/$REPO/releases/latest" 2>/dev/null || true)"
  else
    body="$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" 2>/dev/null || true)"
  fi
  # Retry with auth if the unauthenticated call returned nothing — covers
  # the private-repo case where the first curl 404'd.
  if [[ -z "$body" && -n "$auth" ]]; then
    body="$(curl -fsSL -H "$auth" "https://api.github.com/repos/$REPO/releases/latest" 2>/dev/null || true)"
  fi
  printf '%s\n' "$body" | awk -F'"' '/"tag_name":/ {print $4; exit}'
}

# download_asset streams the asset bytes to stdout. Tries the public
# /releases/download URL first, falls back to the authenticated
# /releases/assets/<id> endpoint when that 404s. Requires jq OR python3
# to parse the asset listing on the private path.
download_asset() {
  local tag="$1" asset="$2" out="$3"
  local direct="https://github.com/$REPO/releases/download/$tag/$asset"
  # Capture curl's HTTP status so we can distinguish "not found"
  # (try authenticated fallback) from network/DNS errors (stop and
  # surface the real problem). Pre-v0.6.18 any failure was silenced
  # to /dev/null and reported as "not publicly downloadable" — wrong
  # for every non-404 case, and users ran off to set GITHUB_TOKEN
  # when the actual problem was no network.
  local http_code curl_err errfile
  errfile="$(mktemp -t ccsync-curl.XXXXXX)"
  http_code="$(curl -sSL -o "$out" -w '%{http_code}' "$direct" 2>"$errfile" || true)"
  curl_err="$(cat "$errfile" 2>/dev/null || true)"
  rm -f "$errfile"
  if [[ "$http_code" == "200" ]]; then
    return 0
  fi
  if [[ "$http_code" != "404" && "$http_code" != "403" && "$http_code" != "401" ]]; then
    # Not an auth/not-found — could be DNS, TCP refused, 5xx, etc.
    # Surface curl's own diagnostic instead of pretending we know.
    echo "error: couldn't download $asset" >&2
    echo "       URL: $direct" >&2
    if [[ -n "$http_code" && "$http_code" != "000" ]]; then
      echo "       HTTP $http_code" >&2
    fi
    if [[ -n "$curl_err" ]]; then
      echo "       $curl_err" >&2
    fi
    return 1
  fi
  local auth; auth="$(auth_header)"
  if [[ -z "$auth" ]]; then
    echo "error: asset not publicly downloadable (HTTP $http_code)." >&2
    echo "       if $REPO is private, set GITHUB_TOKEN or run \`gh auth login\` and retry." >&2
    return 1
  fi
  # Resolve asset id from the tag metadata.
  local meta
  meta="$(curl -fsSL -H "$auth" -H "Accept: application/vnd.github+json" \
    "https://api.github.com/repos/$REPO/releases/tags/$tag")"
  local id
  if command -v jq >/dev/null 2>&1; then
    id="$(printf '%s' "$meta" | jq -r ".assets[] | select(.name == \"$asset\") | .id")"
  elif command -v python3 >/dev/null 2>&1; then
    id="$(printf '%s' "$meta" | python3 -c "
import json, sys
data = json.load(sys.stdin)
for a in data.get('assets', []):
    if a.get('name') == '$asset':
        print(a.get('id'))
        break")"
  else
    echo "error: need jq or python3 to parse private-repo asset metadata." >&2
    return 1
  fi
  if [[ -z "$id" ]]; then
    echo "error: asset $asset not found in release $tag." >&2
    return 1
  fi
  curl -fsSL -H "$auth" -H "Accept: application/octet-stream" \
    "https://api.github.com/repos/$REPO/releases/assets/$id" -o "$out"
}

main() {
  local tag os_name arch_name asset
  tag="${VERSION:-$(latest_tag)}"
  if [[ -z "$tag" ]]; then
    echo "couldn't resolve latest release tag" >&2
    echo "if $REPO is private, set GITHUB_TOKEN or run \`gh auth login\` first." >&2
    exit 1
  fi
  [[ "$tag" == v* ]] || tag="v$tag"
  os_name="$(os)"
  arch_name="$(arch)"
  asset="${BINARY}_${tag#v}_${os_name}_${arch_name}.tar.gz"

  # tmp lives at file scope so the EXIT trap can see it even after main
  # returns. The :- guard makes the trap safe if mktemp ever fails before
  # assignment under set -u.
  tmp="$(mktemp -d)"
  trap 'rm -rf "${tmp:-}"' EXIT
  echo "downloading: $asset ($tag)"
  download_asset "$tag" "$asset" "$tmp/$asset"
  tar -C "$tmp" -xzf "$tmp/$asset"
  mkdir -p "$INSTALL_DIR"
  mv "$tmp/$BINARY" "$INSTALL_DIR/$BINARY"
  chmod 0755 "$INSTALL_DIR/$BINARY"

  # Sanity: the binary should run. Catches architecture mismatch
  # (wrong arch download), corrupted tarballs, and missing dylibs
  # BEFORE the user hits them on first real invocation. A failure
  # here means something weird about the install — better to surface
  # it now than let them discover at 9pm on a Friday.
  local installed_version
  if installed_version="$("$INSTALL_DIR/$BINARY" --version 2>&1)"; then
    :
  else
    echo "error: installed $BINARY but couldn't run it (exec failed)." >&2
    echo "       try \`file $INSTALL_DIR/$BINARY\` — the download may be corrupt or the wrong arch." >&2
    exit 1
  fi

  echo
  echo "installed: $installed_version"
  echo "           $INSTALL_DIR/$BINARY"
  echo
  if [[ ":$PATH:" != *":$INSTALL_DIR:"* ]]; then
    echo "NOTE: $INSTALL_DIR is not in your PATH."
    echo "      add this to your shell rc: export PATH=\"$INSTALL_DIR:\$PATH\""
  fi
}

main "$@"
