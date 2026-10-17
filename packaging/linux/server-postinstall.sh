#!/bin/sh
# Homelydrive Server: start the systemd service after installing/updating.
set -e
mkdir -p /var/lib/homelydrive
chmod 750 /var/lib/homelydrive
if [ -d /run/systemd/system ]; then
  systemctl daemon-reload || true
  systemctl enable homelydrive.service >/dev/null 2>&1 || true
  systemctl restart homelydrive.service || true
fi
cat <<'MSG'

Homelydrive Server läuft jetzt als Dienst (homelydrive.service).
Server-Fenster öffnen:   homelydrive-server gui
Daten liegen in:         /var/lib/homelydrive
Erreichbar nur über Tailscale: http://<tailscale-name>:8420

MSG
