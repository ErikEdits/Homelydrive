# Eine neue Version veröffentlichen

Alle Downloads baut GitHub Actions automatisch (`.github/workflows/release.yml`), sobald ein Versions-Tag gepusht wird:

```sh
git tag v1.0.0
git push origin v1.0.0
```

Nach etwa 15 Minuten steht unter *Releases* eine neue Version mit allen Dateien (Windows-Installer, .deb, AppImage, APK, Prüfsummen). Zusätzlich wird das Docker-Image `ghcr.io/erikedits/homelydrive` veröffentlicht.

Ein Probelauf ohne Veröffentlichung: *Actions → Release → Run workflow* (die Dateien liegen dann als Artefakte am Lauf).

Server und Apps prüfen regelmäßig über die GitHub-API (der Server stündlich, die Apps täglich), ob es eine neuere Version gibt, und zeigen dann einen Hinweis.

## Android: eigener Signierschlüssel

Android installiert ein Update nur, wenn es mit **demselben Schlüssel** signiert ist wie die installierte Version. Ohne eigenen Schlüssel signiert der Release-Lauf mit einem wechselnden Debug-Schlüssel – dann muss die App vor jedem Update deinstalliert werden. Darum einmalig einen Schlüssel anlegen:

```sh
keytool -genkeypair -v -keystore homelydrive.keystore -alias homelydrive \
        -keyalg RSA -keysize 4096 -validity 36500
base64 -w0 homelydrive.keystore > keystore.txt
```

Dann im Repository unter *Settings → Secrets and variables → Actions* vier Secrets anlegen:

| Secret | Inhalt |
|---|---|
| `HD_KEYSTORE_BASE64` | Inhalt von `keystore.txt` |
| `HD_KEYSTORE_PASSWORD` | Passwort des Keystores |
| `HD_KEY_ALIAS` | `homelydrive` |
| `HD_KEY_PASSWORD` | Passwort des Schlüssels |

Den Keystore gut aufheben (nicht ins Repository!) – geht er verloren, können bestehende Installationen keine Updates mehr bekommen.

## F-Droid

Die App erfüllt die Anforderungen von F-Droid (nur freie Software, keine Google-Bibliotheken, keine Tracker). Für die Aufnahme in das offizielle F-Droid-Verzeichnis wird ein Antrag im Repository [fdroiddata](https://gitlab.com/fdroid/fdroiddata) gestellt.
