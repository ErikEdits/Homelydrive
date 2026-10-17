#!/usr/bin/env bash
# Builds AppImages of the PC app and the server from dist/linux-amd64/.
#
#   packaging/appimage/build-appimage.sh VERSION
#
# Downloads appimagetool if APPIMAGETOOL does not point to it.
set -euo pipefail
cd "$(dirname "$0")/../.."

VERSION="${1:?usage: build-appimage.sh VERSION}"
V="${VERSION#v}"
TOOL="${APPIMAGETOOL:-dist/appimagetool-x86_64.AppImage}"
if [ ! -x "$TOOL" ]; then
  curl -fsSL -o "$TOOL" https://github.com/AppImage/appimagetool/releases/download/continuous/appimagetool-x86_64.AppImage
  chmod +x "$TOOL"
fi
# No FUSE needed on the build machine.
export APPIMAGE_EXTRACT_AND_RUN=1 ARCH=x86_64

# appdir NAME BINARY DESKTOP_FILE ICON_NAME DEFAULT_ARGS
appdir() {
  local name=$1 bin=$2 desktop=$3 icon=$4 defaults=$5
  local dir="dist/appimage/$name.AppDir"
  rm -rf "$dir"
  mkdir -p "$dir/usr/bin" "$dir/usr/lib" "$dir/usr/share/applications" "$dir/usr/share/icons/hicolor/256x256/apps"
  cp "dist/linux-amd64/$bin" "$dir/usr/bin/"
  cp "packaging/linux/$desktop" "$dir/usr/share/applications/"
  cp "packaging/linux/$desktop" "$dir/"
  cp assets/logo-256.png "$dir/usr/share/icons/hicolor/256x256/apps/$icon.png"
  cp assets/logo-256.png "$dir/$icon.png"
  cp assets/logo-256.png "$dir/.DirIcon"
  cat > "$dir/AppRun" <<APPRUN
#!/bin/sh
HERE="\$(dirname "\$(readlink -f "\$0")")"
export LD_LIBRARY_PATH="\$HERE/usr/lib\${LD_LIBRARY_PATH:+:\$LD_LIBRARY_PATH}"
# Started by double click (no terminal, no arguments): use the default action.
if [ \$# -eq 0 ] && [ ! -t 0 ] && [ -n "$defaults" ]; then set -- $defaults; fi
exec "\$HERE/usr/bin/$bin" "\$@"
APPRUN
  chmod +x "$dir/AppRun"
  echo "$dir"
}

# PC app: bring libfuse.so.2 along for the virtual drive.
dir=$(appdir homelydrive homelydrive homelydrive.desktop homelydrive "")
lib=$(ldconfig -p | awk '/libfuse\.so\.2 .*x86-64/ {print $NF; exit}')
if [ -n "$lib" ]; then cp -L "$lib" "$dir/usr/lib/libfuse.so.2"; fi
"$TOOL" --no-appstream "$dir" "dist/Homelydrive-$V-x86_64.AppImage"

# Server: a double click opens the server window.
dir=$(appdir homelydrive-server homelydrive-server homelydrive-server.desktop homelydrive-server "gui")
"$TOOL" --no-appstream "$dir" "dist/Homelydrive-Server-$V-x86_64.AppImage"

ls -la dist/*.AppImage
