#!/usr/bin/env bash
# Builds the Homelydrive server and PC app into dist/<os>-<arch>/.
#
#   scripts/build.sh [VERSION]
#
# GOOS/GOARCH choose the target (default: this computer). SKIP_WEB=1 reuses an
# existing web/dist. The Linux PC app needs cgo and libfuse-dev (virtual
# drive); Windows needs nothing extra (WinFsp is loaded at runtime).
set -euo pipefail
cd "$(dirname "$0")/.."

VERSION="${1:-$(git describe --tags --always 2>/dev/null || echo dev)}"
GOOS="${GOOS:-$(go env GOOS)}"
GOARCH="${GOARCH:-$(go env GOARCH)}"
export GOOS GOARCH

if [ -z "${SKIP_WEB:-}" ]; then
  (cd web && npm ci --no-audit --no-fund && npm run build)
fi

LDFLAGS="-s -w -X github.com/erikedits/homelydrive/internal/version.Version=${VERSION}"
EXT=""
if [ "$GOOS" = windows ]; then
  EXT=".exe"
  LDFLAGS="$LDFLAGS -H windowsgui"
  scripts/winres.sh "$VERSION"
fi

OUT="dist/${GOOS}-${GOARCH}"
mkdir -p "$OUT"

echo "Server  -> $OUT/homelydrive-server$EXT"
CGO_ENABLED=0 go build -trimpath -ldflags "$LDFLAGS" -o "$OUT/homelydrive-server$EXT" ./cmd/homelydrive-server

CLIENT_CGO=0
if [ "$GOOS" = linux ]; then CLIENT_CGO=1; fi
echo "PC-App  -> $OUT/homelydrive$EXT"
CGO_ENABLED=$CLIENT_CGO go build -trimpath -ldflags "$LDFLAGS" -o "$OUT/homelydrive$EXT" ./cmd/homelydrive
