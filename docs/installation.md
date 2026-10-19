# Homelydrive installieren

Diese Anleitung führt Schritt für Schritt durch die Einrichtung: zuerst Tailscale, dann der Server, dann die Apps.

- [1. Tailscale](#1-tailscale)
- [2. Server unter Windows](#2-server-unter-windows)
- [3. Server unter Linux](#3-server-unter-linux)
- [4. Server einrichten (Server-Fenster)](#4-server-einrichten-server-fenster)
- [5. PC-App](#5-pc-app)
- [6. Android-App](#6-android-app)
- [7. Im Browser und als Web-App](#7-im-browser-und-als-web-app)
- [8. WebDAV](#8-webdav)
- [9. Häufige Fragen](#9-häufige-fragen)

## 1. Tailscale

Homelydrive ist **nur über Tailscale** erreichbar. Tailscale verbindet deine Geräte zu einem privaten Netz („Tailnet“), egal wo sie gerade sind – zu Hause, unterwegs, im Mobilfunk. Es braucht keine Portfreigabe im Router und keine eigene Domain.

1. Tailscale installieren: <https://tailscale.com/download> (kostenlos für private Nutzung)
   - auf dem **Server-PC**
   - auf **jedem PC und Handy**, das auf Homelydrive zugreifen soll
2. Überall mit **demselben Konto** anmelden.
3. In der [Tailscale-Verwaltung](https://login.tailscale.com/admin/machines) siehst du alle Geräte mit ihrem Namen (z. B. `mein-pc`) und ihrer Adresse (`100.x.y.z`).

Homelydrive erkennt Tailscale auf dem Server selbst – du musst dort nichts eintragen. Andere Programme in deinem Tailnet stören nicht.

## 2. Server unter Windows

1. `Homelydrive-Server-Setup-x.y.z.exe` aus den [Releases](https://github.com/ErikEdits/Homelydrive/releases) herunterladen und ausführen.
2. Der Installer richtet den **Windows-Dienst „Homelydrive Server“** ein. Er startet automatisch mit Windows, auch ohne Anmeldung.
3. Am Ende öffnet sich das **Server-Fenster** (später über das Startmenü: „Homelydrive Server“).

Die Daten (Datenbank, Einstellungen, Vorschaubilder und – solange du keinen anderen Speicherort wählst – die Dateien) liegen in `C:\ProgramData\Homelydrive`.

> Windows SmartScreen kann bei neuen Programmen warnen („Der Computer wurde durch Windows geschützt“). Über „Weitere Informationen“ → „Trotzdem ausführen“ geht es weiter.

## 3. Server unter Linux

**Debian, Ubuntu, Linux Mint** – mit dem .deb-Paket:

```sh
sudo apt install ./homelydrive-server_x.y.z_amd64.deb
```

Das Paket richtet den systemd-Dienst `homelydrive.service` ein und startet ihn. Daten liegen in `/var/lib/homelydrive`.

```sh
systemctl status homelydrive      # läuft er?
journalctl -u homelydrive -f      # Protokoll live
homelydrive-server gui            # Server-Fenster öffnen
```

**Andere Distributionen** – mit dem AppImage:

```sh
chmod +x Homelydrive-Server-x.y.z-x86_64.AppImage
sudo ./Homelydrive-Server-x.y.z-x86_64.AppImage service install   # als Dienst einrichten
sudo ./Homelydrive-Server-x.y.z-x86_64.AppImage service start
./Homelydrive-Server-x.y.z-x86_64.AppImage gui                     # Server-Fenster
```

Lege das AppImage vorher an einen festen Ort (z. B. `/opt/homelydrive/`), denn der Dienst merkt sich diesen Pfad.

**Docker** ist optional möglich, siehe [docker.md](docker.md).

### Server-Fenster

Das Server-Fenster öffnet sich als eigenes App-Fenster (über Chrome, Chromium oder Edge). Ist keiner dieser Browser installiert, öffnet es sich im Standard-Browser unter `http://127.0.0.1:8420/admin/`. Es ist **nur auf dem Server-PC selbst** erreichbar.

### Befehle

```
homelydrive-server                     Server im Vordergrund starten
homelydrive-server gui                 Server-Fenster öffnen
homelydrive-server service install     als Dienst einrichten (Windows-Dienst / systemd)
homelydrive-server service start|stop|restart|status|uninstall
homelydrive-server user add NAME [--admin] [--quota-gb N]
homelydrive-server user passwd NAME
homelydrive-server user list
homelydrive-server user delete NAME
homelydrive-server --data ORDNER ...   anderer Datenordner
```

## 4. Server einrichten (Server-Fenster)

1. **Erstes Konto anlegen** – es wird Admin und kann weitere Benutzer erstellen.
2. **Übersicht**: zeigt, ob Tailscale verbunden ist, und die **Adresse für die Apps**, z. B. `http://mein-pc:8420`. Diese Adresse brauchst du gleich.
3. **Benutzer**: weitere Personen anlegen. Jeder bekommt standardmäßig **100 GB** (änderbar, 0 = unbegrenzt). Der Admin kann fremde Dateien **nicht** sehen.
4. **Speicherorte**: eine oder mehrere Festplatten/Ordner als Speicher hinzufügen. Homelydrive verteilt neue Dateien auf den Ort mit dem meisten freien Platz. Bei 90 % Belegung gibt es eine Warnung.
5. **Geteilte Ablagen**: gemeinsame Bereiche, z. B. „Familie“, mit festgelegten Mitgliedern.
6. **Ordner einbinden**: vorhandene Ordner des Server-PCs (z. B. `D:\Fotos`) in Homelydrive sichtbar machen. Die Dateien bleiben, wo sie sind.
7. **Einstellungen**: Port (Standard 8420), Papierkorb-Dauer (Standard 30 Tage), Update-Prüfung, HTTPS, Sprache.
8. **Protokoll**: alle Meldungen des Servers.

Dateien, die du direkt in einen Speicherordner kopierst, erkennt der Server automatisch.

### HTTPS (optional)

Für die Installation als Web-App auf dem Handy und für das Windows-Netzlaufwerk per WebDAV ist HTTPS nötig. Schalte dazu in der Tailscale-Verwaltung unter **DNS** „MagicDNS“ und „HTTPS Certificates“ ein und aktiviere im Server-Fenster **HTTPS über Tailscale-Zertifikat**. Die Adresse lautet dann `https://mein-pc.dein-tailnet.ts.net:8420`.

### Video-Vorschaubilder (optional)

Für Vorschaubilder von Videos `ffmpeg` installieren (Windows: z. B. `winget install ffmpeg`, Linux: `sudo apt install ffmpeg`). Das Server-Fenster zeigt an, ob es gefunden wurde.

## 5. PC-App

**Windows**: `Homelydrive-Setup-x.y.z.exe` ausführen. Der Installer bringt **WinFsp** mit (für das virtuelle Laufwerk) und trägt die Einträge im Rechtsklick-Menü ein.

**Linux**:

```sh
sudo apt install ./homelydrive_x.y.z_amd64.deb
# oder
chmod +x Homelydrive-x.y.z-x86_64.AppImage && ./Homelydrive-x.y.z-x86_64.AppImage
```

Dann:

1. App starten – sie erscheint als **Symbol im Infobereich** (Taskleiste) und öffnet ihr Fenster.
2. Server-Adresse (z. B. `mein-pc` oder `100.101.102.103`), Benutzername und Passwort eingeben.
3. **Upload-Ordner** hinzufügen: Ordner auf dem PC und Ziel auf dem Server wählen. Neue und geänderte Dateien werden automatisch hochgeladen. Löschst du etwas auf dem PC, bleibt es auf dem Server.
4. **Virtuelles Laufwerk**: alle Server-Dateien als Laufwerk `H:` (Windows) bzw. Ordner `~/Homelydrive` (Linux). Dateien werden erst beim Öffnen geladen.
5. **Rechtsklick** auf Dateien im Explorer / Dateimanager: „Auf Homelydrive hochladen“ und „Homelydrive-Link kopieren“ (Windows Explorer, Nautilus, Nemo, Caja, Dolphin).
6. Im Fenster: Bandbreiten-Regler, Upload-Verlauf, Benachrichtigungen, manuelles Hoch- und Herunterladen.

Die App startet nicht automatisch mit dem PC. Wer das möchte, legt eine Verknüpfung in den Autostart-Ordner.

## 6. Android-App

1. `Homelydrive-x.y.z.apk` auf dem Handy herunterladen und öffnen. Android fragt einmal, ob der Browser/Dateimanager Apps installieren darf („Unbekannte Apps installieren“).
2. **Tailscale-App** auf dem Handy einschalten.
3. In Homelydrive Server-Adresse, Benutzername und Passwort eingeben.
4. Reiter **Hochladen** → **Auf dem Handy gefunden**: Die App sucht selbst alle Ordner mit Fotos und Videos (wie Google Fotos – Kamera, Screenshots, WhatsApp, Telegram …) sowie Downloads und Dokumente. Gewünschte Ordner anhaken und **Ausgewählte hinzufügen** tippen. Weitere Ordner gibt es über **Ordner hinzufügen**: Es öffnet sich der eingebaute Datei-Explorer. Tippen öffnet einen Ordner, **lange drücken wählt aus** – so lassen sich mehrere Ordner auf einmal wählen (Kamera, WhatsApp-Bilder, Downloads, Dokumente …). Beim ersten Mal fragt Android nach dem **Zugriff auf alle Dateien** – das ist nötig, damit jeder Ordner gewählt werden kann.
5. Für jeden Ordner lässt sich der **Zielordner** auf dem Server festlegen und ob die Dateien nach dem Hochladen **auf dem Handy gelöscht** werden sollen (nie, sofort, nach 7/30/90 Tagen).

Neue Dateien werden **sofort** hochgeladen, auch im Hintergrund und nach einem Neustart. Damit Android das nicht unterbricht, unter **Mehr → Im Hintergrund laufen** die Akku-Optimierung für Homelydrive ausschalten.

Weitere Funktionen:

- **Galerie** (im Explorer oben das Bild-Symbol bzw. „Fotos & Videos hochladen“): Fotos und Videos in Alben wie bei Google Fotos oder der Galerie – Album öffnen, Fotos antippen, hochladen. Album lange drücken wählt das ganze Album. Beim Hinzufügen von Upload-Ordnern lassen sich hier ganze Alben antippen.
- **Dateien vom Handy hochladen**: im Reiter *Hochladen* bzw. über den Hochladen-Knopf im Reiter *Dateien*. Im Explorer beliebig viele Dateien und ganze Ordner auswählen (lange drücken, dann weitere antippen) und in den gewählten Server-Ordner hochladen. Die Android-Auswahl ist dort über „Andere App“ weiterhin erreichbar.
- **Dateien**-Reiter: die komplette Weboberfläche (Suchen, Teilen, Fotos, Papierkorb …), automatisch angemeldet.
- **Android-„Dateien“-App**: Homelydrive erscheint dort als Speicherort; jede App kann Dateien direkt vom Server öffnen und dort speichern.
- **Teilen-Menü**: in jeder App „Teilen“ → „In Homelydrive hochladen“.
- **Scanner**: Mehr → Dokument scannen → Seiten fotografieren, drehen, zuschneiden, als PDF hochladen.
- **Widget** für den Startbildschirm: Upload-Status und „Hochladen“-Knopf.
- Benachrichtigungen bei neuen Freigaben.

Die App nutzt **keine Google-Dienste** und funktioniert auch auf Handys ohne Google (z. B. mit /e/OS, LineageOS, GrapheneOS).

## 7. Im Browser und als Web-App

Jeder Browser im Tailnet kann die Adresse öffnen, z. B. `http://mein-pc:8420`. Mit HTTPS (siehe oben) lässt sich Homelydrive auf Handy und PC als **Web-App installieren** („Zum Startbildschirm hinzufügen“ bzw. Installieren-Symbol in der Adresszeile).

Öffentliche Links (`/s/…`) und Upload-Links (`/u/…`) funktionieren für alle Geräte im Tailnet. Personen ohne Zugang zu deinem Tailnet kannst du über die [Tailscale-Funktion „Share“](https://tailscale.com/kb/1084/sharing) einladen.

## 8. WebDAV

Adresse: `http://mein-pc:8420/dav/` (Anmeldung mit Benutzername und Passwort). Damit lässt sich Homelydrive in Dateimanagern und Programmen nutzen, die WebDAV können (z. B. Nautilus „Mit Server verbinden“: `dav://mein-pc:8420/dav/`). Das Windows-Netzlaufwerk verlangt HTTPS – oder einfach das virtuelle Laufwerk der PC-App verwenden.

## 9. Häufige Fragen

**Der Virenschutz (Norton, Avast, AVG …) blockiert oder löscht Homelydrive.**
Die Programme sind noch nicht digital signiert; manche Virenschutz-Programme halten neue, unbekannte Programme deshalb für verdächtig. Typisch bei Norton: Das Server-Fenster geht kurz auf, schließt sich wieder und alles ist verschwunden. Abhilfe: im Virenschutz die Ordner `C:\Program Files\Homelydrive Server`, `C:\Program Files\Homelydrive` und `C:\ProgramData\Homelydrive` als Ausnahme eintragen (bei Norton: *Einstellungen → Antivirus → Scans und Risiken → Ausschlüsse/Niedrige Risiken*, dort bei beiden Listen „Konfigurieren“ → „Ordner hinzufügen“), Einträge in der Quarantäne wiederherstellen und den Installer erneut ausführen. Den Fehlalarm kann man dem Hersteller melden (Norton: <https://submit.norton.com/?type=FP>).

**Ein Ordner lässt sich nicht löschen, umbenennen oder verschieben („Keine Berechtigung“).**
Bis Version 0.2.3 hat der Server unter Windows Ordner mit Unterordnern durch seine Ordner-Überwachung gesperrt. Ab 0.2.4 ist das behoben: einfach den neuen Server-Installer ausführen. Kommt die Meldung danach noch, hat ein anderes Programm eine Datei darin geöffnet (z. B. ein Video-Player), oder dem Server fehlen die Rechte am Speicherort.

**Die App findet den Server nicht.**
Ist Tailscale auf beiden Geräten eingeschaltet und im selben Konto? Läuft der Server (Server-Fenster → Übersicht)? Die richtige Adresse steht im Server-Fenster.

**Kann ich Homelydrive ohne Tailscale im Heimnetz nutzen?**
Nein, das ist absichtlich so: Der Server lauscht nur auf der Tailscale-Adresse und auf dem PC selbst.

**Wo liegen meine Dateien?**
Ganz normal als Dateien in den Speicherorten (Standard: `C:\ProgramData\Homelydrive\storage` bzw. `/var/lib/homelydrive/storage`), getrennt nach Benutzern. Ausnahme: der Tresor – seine Dateien sind verschlüsselt.

**Wie mache ich ein Backup?**
Den Datenordner und die Speicherorte mit einem Backup-Programm deiner Wahl sichern.

**Update installieren?**
Der Server und die Apps zeigen an, wenn eine neue Version erscheint. Einfach den neuen Installer bzw. das neue Paket installieren – Daten und Einstellungen bleiben erhalten.

**Passwort vergessen?**
Auf dem Server: `homelydrive-server user passwd NAME` oder im Server-Fenster unter Benutzer.
