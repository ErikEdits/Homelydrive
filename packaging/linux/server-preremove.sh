#!/bin/sh
# Stop the service before the files are removed (not on upgrades).
set -e
if [ -d /run/systemd/system ]; then
  case "$1" in
    remove|purge|0)
      systemctl disable --now homelydrive.service >/dev/null 2>&1 || true
      ;;
  esac
fi
