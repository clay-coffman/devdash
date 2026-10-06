#!/bin/sh
# Install devdash into ~/.local/bin from a GitHub release.
#   curl -fsSL https://raw.githubusercontent.com/clay-coffman/devdash/main/install.sh | sh
# Environment: DEVDASH_VERSION=v0.1.0 to pin a release (default: latest),
#              DEVDASH_BIN_DIR to install elsewhere (default: ~/.local/bin).
set -eu
REPO=clay-coffman/devdash
VERSION=${DEVDASH_VERSION:-latest}
BIN_DIR=${DEVDASH_BIN_DIR:-$HOME/.local/bin}

os=$(uname -s | tr '[:upper:]' '[:lower:]')
arch=$(uname -m)
case "$arch" in
  x86_64|amd64) arch=amd64 ;;
  aarch64|arm64) arch=arm64 ;;
  *) echo "devdash: unsupported architecture $arch" >&2; exit 1 ;;
esac
case "$os" in linux|darwin) ;; *) echo "devdash: unsupported OS $os" >&2; exit 1 ;; esac
asset="devdash_${os}_${arch}"

if [ "$VERSION" = latest ]; then
  base="https://github.com/$REPO/releases/latest/download"
else
  base="https://github.com/$REPO/releases/download/$VERSION"
fi

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
echo "devdash: downloading $asset ($VERSION)" >&2
curl -fsSL -o "$tmp/$asset" "$base/$asset"
curl -fsSL -o "$tmp/SHA256SUMS" "$base/SHA256SUMS"
want=$(grep " $asset\$" "$tmp/SHA256SUMS" | cut -d' ' -f1)
if command -v sha256sum >/dev/null 2>&1; then
  got=$(sha256sum "$tmp/$asset" | cut -d' ' -f1)
else
  got=$(shasum -a 256 "$tmp/$asset" | cut -d' ' -f1)
fi
if [ -z "$want" ] || [ "$want" != "$got" ]; then
  echo "devdash: checksum mismatch for $asset" >&2
  exit 1
fi
mkdir -p "$BIN_DIR"
install -m 755 "$tmp/$asset" "$BIN_DIR/devdash"
echo "devdash: installed $("$BIN_DIR/devdash" version) to $BIN_DIR/devdash" >&2
case ":$PATH:" in
  *":$BIN_DIR:"*) ;;
  *) echo "devdash: add $BIN_DIR to your PATH" >&2 ;;
esac
