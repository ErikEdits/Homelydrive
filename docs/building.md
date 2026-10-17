# Homelydrive selbst bauen

## Voraussetzungen

| Für | Werkzeuge |
|---|---|
| Weboberfläche | Node.js 22 + npm |
| Server, PC-App | Go 1.26 |
| PC-App unter Linux | gcc und `libfuse-dev` (virtuelles Laufwerk über FUSE) |
| Android-App | JDK 17, Android SDK (Platform 35) |
| Pakete | [nfpm](https://nfpm.goreleaser.com) (.deb), [appimagetool](https://github.com/AppImage/appimagetool), [Inno Setup 6](https://jrsoftware.org/isinfo.php) (Windows) |

Unter Windows braucht die PC-App zum Bauen nichts Zusätzliches; zur Laufzeit wird [WinFsp](https://winfsp.dev) für das Laufwerk geladen (der Installer bringt es mit).

## Server und PC-App

```sh
scripts/build.sh v1.0.0                                  # für diesen PC → dist/<os>-<arch>/
GOOS=windows GOARCH=amd64 scripts/build.sh v1.0.0        # Windows-Programme unter Linux bauen
```

Das Skript baut zuerst die Weboberfläche (`web/`), die in beide Programme eingebettet wird (`SKIP_WEB=1` überspringt das). Unter Windows erzeugt `scripts/winres.sh` vorher Symbol und Versionsinfo.

Zum Entwickeln:

```sh
cd web && npm ci && npm run dev     # Weboberfläche mit Live-Reload (Proxy auf :8420)
go run ./cmd/homelydrive-server     # Server
go run ./cmd/homelydrive            # PC-App
go test ./...                       # Tests
cd web && npm run check             # Typprüfung + Übersetzungen prüfen
```

## Pakete

```sh
VERSION=1.0.0 nfpm package -f packaging/linux/nfpm-server.yaml -p deb -t dist/
VERSION=1.0.0 nfpm package -f packaging/linux/nfpm-client.yaml -p deb -t dist/
packaging/appimage/build-appimage.sh v1.0.0
iscc /DAppVersion=1.0.0 packaging\windows\server.iss     # Windows
iscc /DAppVersion=1.0.0 packaging\windows\client.iss     # braucht dist\winfsp.msi
```

## Android-App

```sh
cd android
./gradlew assembleDebug        # app/build/outputs/apk/debug/app-debug.apk
HD_VERSION=v1.0.0 ./gradlew assembleRelease
```

Die App verwendet nur das Android-Framework (keine AndroidX- oder Google-Bibliotheken). Signieren: siehe [releases.md](releases.md).

## Aufbau

```
cmd/homelydrive-server     Einstieg Server (Dienst, Server-Fenster, Befehle)
cmd/homelydrive            Einstieg PC-App
internal/server/api        HTTP-API, Weboberfläche, WebDAV-Weiterleitung
internal/server/core       Dateien, Benutzer, Freigaben, Uploads, Suche, Tresor
internal/server/storage    mehrere Speicherorte als ein Dateisystem
internal/server/tailnet    Tailscale-Erkennung und Listener
internal/client/engine     Ordner-Upload (PC-App)
internal/client/drive      virtuelles Laufwerk (cgofuse: WinFsp/FUSE)
web/src/app                Weboberfläche   web/src/admin  Server-Fenster   web/src/client  PC-App-Fenster
android/                   Android-App
```
