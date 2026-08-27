#!/bin/sh
# agentbus installer. Downloads the release binary for this platform to
# ~/.local/bin/agentbus, falling back to a source build. Works against a
# private repo through an authenticated `gh`.
set -eu

REPO="joshuafuller/agentbus"
DEST="${AGENTBUS_DEST:-$HOME/.local/bin}"
BIN="$DEST/agentbus"

os=$(uname -s | tr '[:upper:]' '[:lower:]')
arch=$(uname -m)
case "$arch" in
  x86_64) arch=amd64 ;;
  aarch64 | arm64) arch=arm64 ;;
esac
asset="agentbus-$os-$arch"

mkdir -p "$DEST"

if command -v gh >/dev/null 2>&1 && gh release download -R "$REPO" -p "$asset" -O "$BIN" --clobber 2>/dev/null; then
  chmod +x "$BIN"
elif command -v go >/dev/null 2>&1 && command -v gh >/dev/null 2>&1; then
  echo "no release asset $asset; building from source..." >&2
  tmp=$(mktemp -d)
  trap 'rm -rf "$tmp"' EXIT
  gh repo clone "$REPO" "$tmp/agentbus" -- --depth 1 >/dev/null 2>&1
  (cd "$tmp/agentbus" && go build -o "$BIN" ./cmd/agentbus)
else
  echo "install failed: need an authenticated gh (and go for source builds)" >&2
  exit 1
fi

"$BIN" help >/dev/null 2>&1 || { echo "installed binary failed self-check" >&2; exit 1; }
echo "installed $BIN"
case ":$PATH:" in
  *":$DEST:"*) ;;
  *) echo "note: $DEST is not on your PATH; add it or call $BIN directly" ;;
esac
