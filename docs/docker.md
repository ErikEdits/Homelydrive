# Homelydrive Server mit Docker (optional)

Der normale Weg ist der Installer (Windows) bzw. das .deb-Paket (Linux). Wer seinen Server lieber als Container betreibt, kann das Image `ghcr.io/erikedits/homelydrive` nutzen.

**Voraussetzung:** Tailscale läuft auf dem **Host** (nicht im Container). Der Container nutzt das Netzwerk des Hosts (`network_mode: host`), damit der Server die Tailscale-Adresse findet.

## docker compose

[`docker-compose.yml`](../docker-compose.yml) aus dem Projekt herunterladen, Passwort anpassen und starten:

```sh
docker compose up -d
docker compose logs -f
```

```yaml
services:
  homelydrive:
    image: ghcr.io/erikedits/homelydrive:latest
    restart: unless-stopped
    network_mode: host
    environment:
      HOMELYDRIVE_ADMIN_USER: admin          # legt beim ersten Start den Admin an
      HOMELYDRIVE_ADMIN_PASSWORD: change-me  # danach entfernen
      TZ: Europe/Berlin
    volumes:
      - ./homelydrive-data:/data
      # - /mnt/disk2:/mnt/disk2              # weitere Festplatten
```

- **Server-Fenster**: auf dem Host im Browser `http://127.0.0.1:8420/admin/` öffnen.
- **Weitere Festplatten**: als Volume einbinden und im Server-Fenster unter *Speicherorte* oder *Ordner einbinden* hinzufügen.
- **Benutzer per Befehl**: `docker exec -it homelydrive homelydrive-server user add anna`
- **Update**: `docker compose pull && docker compose up -d`

## Selbst bauen

```sh
docker build -t homelydrive --build-arg VERSION=v1.0.0 .
```

Das Image enthält `ffmpeg` für Video-Vorschaubilder. HTTPS über das Tailscale-Zertifikat ist im Container nicht verfügbar (dafür fehlt der `tailscale`-Befehl); im Tailnet ist die Verbindung ohnehin verschlüsselt.
