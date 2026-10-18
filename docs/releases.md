# Eine neue Version veröffentlichen

Alle Downloads baut GitHub Actions automatisch (`.github/workflows/release.yml`). Es gibt zwei Wege:

**A – Versions-Tag pushen:**

```sh
git tag v1.0.0
git push origin v1.0.0
```

**B – Commit mit `[release]`:** die Versionsnummer in der Datei `VERSION` erhöhen und mit `[release]` in der Commit-Nachricht pushen. Der Workflow legt den Tag `v<VERSION>` dann selbst an.

```sh
echo 1.0.0 > VERSION
git commit -am "Version 1.0.0 [release]"
git push
```

Nach etwa 15 Minuten steht unter *Releases* eine neue Version mit allen Dateien (Windows-Installer, .deb, AppImage, APK, Prüfsummen). Zusätzlich wird das Docker-Image `ghcr.io/erikedits/homelydrive` veröffentlicht.

Ein Probelauf ohne Veröffentlichung: *Actions → Release → Run workflow* (die Dateien liegen dann als Artefakte am Lauf).

Server und Apps prüfen regelmäßig über die GitHub-API (der Server stündlich, die Apps täglich), ob es eine neuere Version gibt, und zeigen dann einen Hinweis.

## Android: fester Signierschlüssel (wichtig für Updates)

Android installiert ein Update nur, wenn es mit **demselben Schlüssel** signiert ist wie die installierte App – sonst kommt „App nicht installiert“. Ohne Schlüssel signiert der Release-Lauf mit einem wechselnden Einmal-Schlüssel, und die App muss vor jedem Update deinstalliert werden.

**Einrichten (einmalig, 2 Minuten):**

1. Im Repository auf GitHub: *Settings → Secrets and variables → Actions → New repository secret*
2. Name: `HD_SIGNING_PASSWORD`
3. Wert: ein langes, zufälliges Passwort (z. B. 30+ Zeichen aus einem Passwort-Manager). **Gut aufheben** – ohne dieses Passwort kann niemand mehr Updates bauen, die sich über die installierte App installieren lassen.

Beim nächsten Release erzeugt der Workflow den Schlüssel selbst, verschlüsselt ihn mit diesem Passwort und legt ihn als `android/signing/release.p12.enc` im Repository ab. Ab dann sind alle APKs gleich signiert. (Einmalig muss eine App, die noch mit einem Einmal-Schlüssel signiert war, deinstalliert werden.)

**Alternative – eigener Keystore:**

```sh
keytool -genkeypair -v -keystore homelydrive.keystore -alias homelydrive \
        -keyalg RSA -keysize 4096 -validity 36500
base64 -w0 homelydrive.keystore > keystore.txt
```

Dann die Secrets `HD_KEYSTORE_BASE64` (Inhalt von `keystore.txt`), `HD_KEYSTORE_PASSWORD`, `HD_KEY_ALIAS` (`homelydrive`) und `HD_KEY_PASSWORD` anlegen. Sind diese gesetzt, haben sie Vorrang.

## F-Droid

Die App erfüllt die Anforderungen von F-Droid (nur freie Software, keine Google-Bibliotheken, keine Tracker). Für die Aufnahme in das offizielle F-Droid-Verzeichnis wird ein Antrag im Repository [fdroiddata](https://gitlab.com/fdroid/fdroiddata) gestellt.
