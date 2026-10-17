#!/usr/bin/env bash
# Creates the Windows resources (icon, version info, manifest) as .syso files
# next to both programs; "go build" picks them up automatically.
#
#   scripts/winres.sh [VERSION]
set -euo pipefail
cd "$(dirname "$0")/.."

V="${1:-0.0.0}"
V="${V#v}"
V="${V%%-*}"
if ! [[ "$V" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then V="0.0.0"; fi

# The tool runs on this computer, whatever GOOS/GOARCH the build targets.
WINRES="env -u GOOS -u GOARCH -u CGO_ENABLED go run github.com/tc-hib/go-winres@v0.3.3"
ICON="$PWD/assets/logo.ico"
for prog in homelydrive homelydrive-server; do
  if [ "$prog" = homelydrive ]; then desc="Homelydrive"; else desc="Homelydrive Server"; fi
  (cd "cmd/$prog" && $WINRES simply --arch amd64,arm64 --manifest gui --icon "$ICON" \
    --product-name "Homelydrive" --file-description "$desc" \
    --product-version "$V.0" --file-version "$V.0" \
    --copyright "GPL-3.0 - github.com/ErikEdits/Homelydrive" --original-filename "$prog.exe")
done
