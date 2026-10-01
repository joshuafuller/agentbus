#!/bin/sh
# Install a checksum-verified pair through one atomic symlink replacement.
set -eu
REPO="joshuafuller/agentbus"
VERSION="${AGENTBUS_VERSION:-v0.4.0}"
DEST="${AGENTBUS_DEST:-$HOME/.local/bin}"
case "$VERSION" in *[!A-Za-z0-9._-]* | "") echo "invalid release version" >&2; exit 1 ;; esac
os=$(uname -s | tr '[:upper:]' '[:lower:]')
arch=$(uname -m)
case "$arch" in x86_64) arch=amd64 ;; aarch64 | arm64) arch=arm64 ;; esac
case "$os/$arch" in linux/amd64 | linux/arm64 | darwin/amd64 | darwin/arm64) ;; *) echo "unsupported platform: $os/$arch" >&2; exit 1 ;; esac
asset="agentbus-$os-$arch.tar.gz"
command -v gh >/dev/null 2>&1 || { echo "install requires an authenticated gh" >&2; exit 1; }
mkdir -p "$DEST"
# Canonicalize so the installed symlink works with a relative AGENTBUS_DEST too.
DEST=$(cd "$DEST" && pwd)
tmp=$(mktemp -d "$DEST/.agentbus-install.XXXXXX")
trap 'rm -rf "$tmp"' EXIT HUP INT TERM
if gh release download "$VERSION" -R "$REPO" -p "$asset" -p SHA256SUMS -D "$tmp"; then
 expected=$(awk -v asset="$asset" '$2 == asset || $2 == "*" asset { print; found++ } END { if (found != 1) exit 1 }' "$tmp/SHA256SUMS")
 if command -v sha256sum >/dev/null 2>&1; then (cd "$tmp" && printf '%s\n' "$expected" | sha256sum -c -)
 else (cd "$tmp" && printf '%s\n' "$expected" | shasum -a 256 -c -); fi
 # Accept exactly two regular files, with no links or path components.
 entries=$(tar -tzf "$tmp/$asset" | sort)
 [ "$entries" = "$(printf 'agentbus\nagentbus-iroh')" ] || { echo "invalid release archive" >&2; exit 1; }
 [ "$(tar -tvzf "$tmp/$asset" | cut -c1 | sort -u)" = "-" ] || { echo "release archive must contain regular files" >&2; exit 1; }
 mkdir "$tmp/pair"
 tar -xzf "$tmp/$asset" -C "$tmp/pair"
else
 command -v go >/dev/null 2>&1 && command -v cargo >/dev/null 2>&1 || { echo "source fallback requires Go and Rust" >&2; exit 1; }
 echo "release unavailable; building tag $VERSION from source..." >&2
 gh repo clone "$REPO" "$tmp/source" -- --depth 1 --branch "$VERSION"
 (cd "$tmp/source" && make build LDFLAGS="-X main.version=$VERSION")
 mkdir "$tmp/pair"
 cp "$tmp/source/agentbus" "$tmp/source/agentbus-iroh" "$tmp/pair/"
fi
chmod +x "$tmp/pair/agentbus" "$tmp/pair/agentbus-iroh"
help=$("$tmp/pair/agentbus" help)
case "$help" in *ab1*) ;; *) echo "binary lacks Iroh ticket support" >&2; exit 1 ;; esac
built_version=$("$tmp/pair/agentbus" version)
case "$built_version" in "agentbus $VERSION "*) ;; *) echo "binary version does not match $VERSION" >&2; exit 1 ;; esac
helper_version=$("$tmp/pair/agentbus-iroh" --version)
case "$helper_version" in "agentbus-iroh ${VERSION#v} (official iroh 1.3.0)") ;; *) echo "helper version does not match $VERSION" >&2; exit 1 ;; esac
# Keep previous pairs for rollback. A unique directory prevents mutating a live pair.
mkdir -p "$DEST/.agentbus-releases"
pair=$(mktemp -d "$DEST/.agentbus-releases/$VERSION.XXXXXX")
mv "$tmp/pair/agentbus" "$tmp/pair/agentbus-iroh" "$pair/"
ln -s "$pair/agentbus" "$tmp/agentbus"
mv -f "$tmp/agentbus" "$DEST/agentbus"
printf 'installed %s/agentbus (%s)\n' "$DEST" "$VERSION"
