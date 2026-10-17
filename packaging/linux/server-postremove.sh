#!/bin/sh
# The data in /var/lib/homelydrive is kept; delete it by hand if wanted.
set -e
if [ -d /run/systemd/system ]; then
  systemctl daemon-reload >/dev/null 2>&1 || true
fi
