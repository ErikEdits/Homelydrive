<p align="center">
  <img src="assets/logo.svg" width="112" alt="Homelydrive logo">
</p>

<h1 align="center">Homelydrive</h1>

<p align="center">
  <b>Your own cloud – like Google Drive, but on your own PC.</b><br>
  Free, open source, no subscription. Connected through <a href="https://tailscale.com">Tailscale</a>.
</p>

<p align="center">
  <a href="README.md">Deutsch</a> ·
  <a href="https://github.com/ErikEdits/Homelydrive/releases">Downloads</a> ·
  <a href="LICENSE">GPL-3.0</a>
</p>

---

Homelydrive has three parts:

| Part | For | What it does |
|---|---|---|
| **Homelydrive Server** | Windows, Linux | The actual cloud. Runs as a background service and stores all files on your disks. Comes with a server window for the setup. |
| **Homelydrive PC app** | Windows, Linux | Uploads chosen folders automatically, virtual drive, tray icon, right-click menu. |
| **Homelydrive Android app** | Android 8+ | Uploads any phone folders right away, browse files, scan documents, share menu. |

Everything connects through **Tailscale**: the server is only reachable inside your private tailnet – no port forwarding, no domains, no access from the open internet.

## Features

**Files & web interface** (in the browser, as an installable web app and inside the Android app)

- My Drive, shared drives (e.g. "Family"), "Shared with me", mounted folders
- Uploads of any size (300 GB too), resumable, duplicates are recognised by checksum
- Same name in the same folder → "file (1).txt", nothing is overwritten
- Previews and thumbnails: pictures, videos, music, PDF, text
- Text editor with syntax highlighting and Markdown preview
- Full-text search (also inside PDF, Word, LibreOffice) with filters for type, date, size, owner, tag
- Photo timeline with slideshow, rotate and crop
- Favourites, recent files, folder colours, tags, notes, folder sizes, storage overview
- Trash (30 days), activity log, notifications
- Vault with its own password (AES-256 encrypted)
- Download as ZIP, extract ZIP files on the server
- Unknown file types are recognised and registered on upload
- WebDAV for other programs
- German and English, light and dark mode

**Sharing**

- Share with other users (view or edit)
- Links with password, expiry date and download counter/limit
- Upload links: others can send you files without an account
- Notification when someone shares something with you

**Server**

- Windows service / systemd service, starts automatically
- Finds Tailscale by itself and shows the address for the apps
- Several disks as storage locations, mount existing folders
- Picks up files copied straight into the storage folder
- Several users with storage limits (default 100 GB) – the admin cannot see other people's files
- Profile pictures
- Log view, warning at 90 % disk usage, update hint
- Optional HTTPS with the Tailscale certificate, optional Docker

**PC app**

- Uploads folders automatically (upload only: files deleted on the PC stay on the server)
- Virtual drive (WinFsp on Windows, FUSE on Linux) – files are loaded when opened
- Manual upload and download, right-click menu ("Upload to Homelydrive", "Copy link")
- Bandwidth slider, upload history, notifications

**Android app**

- Choose any folders (not just the camera), each with its own target folder
- Uploads new files right away, also in the background and after a reboot
- Optional: delete files on the phone after the upload
- Server files in the Android "Files" app, share menu, scanner to PDF, widget
- 100 % free: no Google services, F-Droid friendly

## Quick start

1. Install **Tailscale** on the server PC and on all devices, signed in to the same account: <https://tailscale.com/download>
2. **Install the server** from the [releases](https://github.com/ErikEdits/Homelydrive/releases):
   - Windows: `Homelydrive-Server-Setup-x.y.z.exe`
   - Linux: `homelydrive-server_x.y.z_amd64.deb` (or the AppImage)
3. Create the first account in the **server window** (it becomes the admin). The window also shows the address, e.g. `http://my-pc:8420`.
4. **Connect the apps**: install the PC app (`Homelydrive-Setup-x.y.z.exe`, `.deb` or AppImage) or the Android app (`Homelydrive-x.y.z.apk`) and enter the address – or simply open it in a browser.

The detailed guide (German) is in [docs/installation.md](docs/installation.md).

## Building

See [docs/building.md](docs/building.md). In short: Go 1.26, Node.js 22 and – for the PC app on Linux – `libfuse-dev`:

```sh
scripts/build.sh            # server + PC app for this computer into dist/
cd android && ./gradlew assembleDebug
```

## License

Homelydrive is free software under the [GNU General Public License v3.0](LICENSE).
