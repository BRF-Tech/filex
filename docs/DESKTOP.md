# filex desktop app

The filex explorer in its own window, with folder sync that keeps running in the
background. Windows and Linux (x64 and arm64) and macOS (Apple Silicon) - and
on every one of them there is a copy that runs without being installed.

It is the same explorer the web app and embedders use, not a separate copy. It
adds **several accounts at once**, **folders synced to your disk**, and
**staying in the tray** so that sync runs with the window closed. It is **not**
an admin console: the app opens your server's admin panel in your browser.

## In five minutes

1. **Install** it from the Microsoft Store or the Snap Store, with `snap` or
   `brew`, or download the file for your platform ([Install](#install)). Your
   server needs filex v0.11.0 or newer.
2. **Sign in**: type your server's address, sign in on the page your browser
   opens, and the browser hands the app a one-time code
   ([Signing in](#signing-in)).
3. **Keep a folder**: right-click it → **Keep on this computer**. It syncs both
   ways under one filex folder on your disk; everything else stays online-only
   in the window ([Keeping folders on this computer](#keeping-folders-on-this-computer)).

With ONLYOFFICE on your server, Office documents on your own disk open in its
editor too ([Opening documents from your computer](#opening-documents-from-your-computer)).

---

## Install

### From a store

<div class="store-badges">
<a href="https://apps.microsoft.com/detail/9PKXDJLVZWXW"><picture><source media="(prefers-color-scheme: dark)" srcset="./badges/ms-store-light.svg"><img src="./badges/ms-store-dark.svg" alt="Download from the Microsoft Store" height="52"></picture></a>&nbsp;&nbsp;&nbsp;
<a href="https://snapcraft.io/filex-app"><picture><source media="(prefers-color-scheme: dark)" srcset="./badges/snap-store-white.svg"><img src="./badges/snap-store-black.svg" alt="Get it from the Snap Store" height="52"></picture></a>
</div>

The Microsoft Store lists it as *filex File Manager* (Windows 10/11), the Snap
Store as `filex-app` (Ubuntu's App Center finds it when you search for
*filex*). Both install the same app as the downloads below and keep it up to
date themselves; the app then leaves updating to them.

### With a package manager

The desktop app is **`filex-app`** in every one of them; plain `filex` is the
[CLI](CLI.md).

| Platform | Command | Notes |
|---|---|---|
| Ubuntu and other Linux with snapd | `sudo snap install filex-app` | The Snap Store build, for x64 and arm64 (snapd picks the one for the machine). To keep the sign-in in your keyring: `sudo snap connect filex-app:password-manager-service` (the app says when it is needed). See [the snap and the sandbox](#the-snap-and-the-sandbox). |
| macOS 13+ (Apple Silicon) | `brew install brf-tech/filex/filex-app` | Homebrew tap [`BRF-Tech/homebrew-filex`](https://github.com/BRF-Tech/homebrew-filex); `brew upgrade` keeps it current (the Mac app does not update itself). The app is not signed with a Developer ID, so the first launch is blocked once, as below. |
| Windows 10/11 | [Microsoft Store](https://apps.microsoft.com/detail/9PKXDJLVZWXW) - *filex File Manager* | **The one code-signed Windows build** (Microsoft signs it): no SmartScreen prompt, and the Store updates it. Beside the installer below, both read the same accounts and folders (Settings says so). |
| Windows 10/11 | `winget install BRFTech.filex-app` - **not installable yet** | The same per-user installer as the download below, updating itself the same way. Every release submits it, and the desktop app's package is still waiting for its first review by the winget moderators: until that is approved, `winget` does not find it. The [CLI](CLI.md)'s package, `BRFTech.filex`, is on winget already. |

### Download

| Platform | File | What it does |
|---|---|---|
| Windows 10/11 (x64) | [`filex-desktop-x64.exe`](https://github.com/BRF-Tech/filex/releases/latest/download/filex-desktop-x64.exe) | Installer, for **your user only** (`%LOCALAPPDATA%\Programs\filex`): no administrator rights, and the app can replace its own files, so it updates itself quietly. Adds a Start-menu entry. |
| Windows 10/11 (x64) | [`filex-desktop-portable-x64.exe`](https://github.com/BRF-Tech/filex/releases/latest/download/filex-desktop-portable-x64.exe) | **Portable** - nothing is installed. Run it from anywhere: a USB stick, `Downloads`, a work machine you may not install software on. It keeps its files in a `filex-data` folder beside itself ([Portable](#portable-windows)). |
| Windows on Arm (arm64) | [`filex-desktop-arm64.exe`](https://github.com/BRF-Tech/filex/releases/latest/download/filex-desktop-arm64.exe) · [`filex-desktop-portable-arm64.exe`](https://github.com/BRF-Tech/filex/releases/latest/download/filex-desktop-portable-arm64.exe) | The same installer and portable copy for arm64: no emulation, and the sync engine inside is arm64 too. An installed copy updates itself to the next arm64 build. |
| Linux (any, x64) | [`filex-desktop-x86_64.AppImage`](https://github.com/BRF-Tech/filex/releases/latest/download/filex-desktop-x86_64.AppImage) | **Portable** - no installation. `chmod +x` and run. On Ubuntu 23.10 and later it needs a one-time step first: [AppImage on recent Ubuntu](#appimage-on-recent-ubuntu). |
| Debian / Ubuntu (x64) | [`filex-desktop-amd64.deb`](https://github.com/BRF-Tech/filex/releases/latest/download/filex-desktop-amd64.deb) | System-wide install (package `filex-app`), appears in your applications menu. |
| Fedora / openSUSE (x64) | [`filex-desktop-x86_64.rpm`](https://github.com/BRF-Tech/filex/releases/latest/download/filex-desktop-x86_64.rpm) | The same, as an RPM (package `filex-app`). |
| Linux (arm64) | [`filex-desktop-arm64.AppImage`](https://github.com/BRF-Tech/filex/releases/latest/download/filex-desktop-arm64.AppImage) · [`filex-desktop-arm64.deb`](https://github.com/BRF-Tech/filex/releases/latest/download/filex-desktop-arm64.deb) · [`filex-desktop-aarch64.rpm`](https://github.com/BRF-Tech/filex/releases/latest/download/filex-desktop-aarch64.rpm) | The same three for arm64, each updating itself from its own arm64 feed. The AppImage needs the same [one-time step on recent Ubuntu](#appimage-on-recent-ubuntu). |
| macOS 13+ (Apple Silicon) | [`filex-desktop-arm64.dmg`](https://github.com/BRF-Tech/filex/releases/latest/download/filex-desktop-arm64.dmg) | Drag to *Applications*. **Unsigned** - see the first-launch note below. Intel Macs: no build; the web app works there. |

Which one is yours: `uname -m` on Linux (`x86_64` is x64, `aarch64` is
arm64), Settings → System → About → *System type* on Windows. The web app's
offer of the desktop app and [filex.sh](https://filex.sh/#downloads) read it
from the browser (Chromium-based browsers name the processor, Firefox on Linux
says `aarch64`); where the browser does not say (Safari, Firefox on Windows),
the x64 file is offered with the arm64 one beside it.

```bash
# Debian / Ubuntu
sudo apt install ./filex-desktop-amd64.deb      # arm64: ./filex-desktop-arm64.deb

# Fedora / openSUSE
sudo dnf install ./filex-desktop-x86_64.rpm     # arm64: ./filex-desktop-aarch64.rpm
                                                # openSUSE: sudo zypper install ./filex-desktop-x86_64.rpm

# Anything else
chmod +x filex-desktop-x86_64.AppImage          # arm64: filex-desktop-arm64.AppImage
./filex-desktop-x86_64.AppImage
```

On Linux the installed app's command is **`filex-app`** - `filex` is the
[CLI](CLI.md).

```powershell
# Windows, nothing installed: put the .exe where you want it and run it.
# The folder it makes beside itself is the whole of what it leaves behind.
.\filex-desktop-portable-x64.exe
```

> ⚠ **The downloads are not code-signed yet** (the Microsoft Store build is:
> Microsoft signs it). Windows SmartScreen will show
> "Windows protected your PC" - *More info* → *Run anyway*. To be certain of
> what you downloaded, check it against `checksums.txt` on the release.
>
> ⚠ **macOS, first launch.** The app is not signed with a Developer ID, only
> *ad-hoc sealed*, so a downloaded copy is blocked once: open **System Settings →
> Privacy & Security → Open Anyway** (older versions: right-click → *Open*).

**Server requirement:** filex **v0.11.0 or newer**. The app signs in through two
endpoints older servers do not have, and says so rather than failing silently.

---

### AppImage on recent Ubuntu

On Ubuntu 23.10 and later (24.04 LTS included, and the distributions built on
them) the AppImage needs two things before it opens. The `.deb` needs neither,
so on Ubuntu it is the simpler choice.

1. **FUSE 2.** An AppImage mounts itself with FUSE 2, which Ubuntu no longer
   installs by default (since 22.04). Without it nothing opens; from a terminal
   it says `dlopen(): error loading libfuse.so.2`.

   ```bash
   sudo apt install libfuse2t64     # Ubuntu 24.04 and later; on 22.04: libfuse2
   ```

2. **Room for Chromium's sandbox.** The app's pages run in Chromium's sandbox,
   built from user namespaces, and since 23.10 Ubuntu lets a program create
   them only if an AppArmor profile allows it
   (`kernel.apparmor_restrict_unprivileged_userns=1`). The `.deb` and the `.rpm`
   carry a setuid helper (`chrome-sandbox`) instead; an AppImage cannot. This
   profile gives that one permission to the AppImage, and only to a file named
   like it in a home folder:

   ```bash
   sudo tee /etc/apparmor.d/filex-appimage > /dev/null <<'EOF'
   abi <abi/4.0>,
   include <tunables/global>

   profile filex-appimage @{HOME}/**/filex-desktop-*.AppImage flags=(unconfined) {
     userns,
     include if exists <local/filex-appimage>
   }
   EOF
   sudo apparmor_parser -r /etc/apparmor.d/filex-appimage
   ```

   It is read again at every boot. The image can live in any folder under your
   home under its release name (`filex-desktop-x86_64.AppImage` or
   `filex-desktop-arm64.AppImage`), which its updates keep. To take the profile
   away again:
   `sudo apparmor_parser -R /etc/apparmor.d/filex-appimage && sudo rm /etc/apparmor.d/filex-appimage`.

**Without the profile the app says so itself.** Every Linux build starts
through a small launcher (`filex-app`; the Electron binary beside it is
`filex-app-bin`) that checks the sandbox can be built (not in the snap:
[the snap and the sandbox](#the-snap-and-the-sandbox)). When it cannot, the
launcher shows these steps - in a window where zenity or kdialog is installed,
and in the terminal in any case - and exits with code 78; the app does not
open. The same holds for an AppImage added to the menu by AppImageLauncher,
Gear Lever or appimaged: neither the menu entry they copy from the image nor
the image's own entry point (`AppRun`, which passes on exactly the arguments it
was given) switches the sandbox off.

> ⚠ Starting the app with `--no-sandbox` yourself also opens it, and is not a
> fix: it switches the sandbox off, so a page that got the better of the viewer
> would run with your account's access to your files and to the tokens the app
> holds. The launcher passes a `--no-sandbox` you typed through and, outside
> the snap, never adds one. Use the profile, or the `.deb`.

### The snap and the sandbox

The snap starts the app with Chromium's own sandbox switched off
(`--no-sandbox`) and relies on snapd's strict confinement instead: an AppArmor
profile, a seccomp filter and a mount namespace of its own around the whole
app. There is nothing to connect for it, and the launcher checks nothing in the
snap.

The confinement keeps the app, all of it, away from the rest of the system,
but does not wall the pages the app shows off from the app itself. A page that
got the better of the viewer would have what the app has inside the snap: the
files in your home folder that the `home` interface reaches, the network, and
the tokens the app holds. In the `.deb`, the `.rpm` and the AppImage each page
also runs in Chromium's sandbox, with none of that. If the difference matters
on your machine, install the `.deb` or the `.rpm`. Why the snap is built this
way:
[CONTRIBUTING.md → Desktop snap without Chromium's sandbox](CONTRIBUTING.md#desktop-snap-without-chromiums-sandbox).

The snap is built on `core24` (Ubuntu 24.04) with Snapcraft's GNOME extension,
for x64 and arm64. Its first install also brings the GNOME 46 runtime
(`gnome-46-2404`), the graphics libraries (`mesa-2404`) and the GTK themes
(`gtk-common-themes`), which other snaps share. The window opens through X11
(XWayland on a Wayland desktop). Every release installs the snap on x64 and
arm64 Ubuntu 24.04 machines and checks this confinement before it publishes.

### On a phone or a tablet: the web app

There is no desktop app for a phone: on Android, an iPhone or an iPad, filex
is the web app, which installs like an app - an icon on the home screen, a
window of its own with no address bar (the manifest's `standalone` display),
and its notifications. An instance with its own branding installs under its
own name and accent colour, with its logo as the icon when the logo is a
picture (uploaded, or at a public address), so two filex servers on one phone
are two apps you can tell apart ([The installed app](CONFIGURATION.md#the-installed-app-pwa)).
Where a computer is offered the desktop app, a phone
gets a band at the bottom of the sign-in page and the file list (which makes
room for it) offering the web app; a phone is never offered the desktop app.
On a branded instance the band and the settings row say the instance's own
name where the steps below say filex, and the band shows the icon the home
screen will get.

- **Android** (Chrome, Edge, Samsung Internet): **Install filex as a web app
  → Install** opens the browser's own install dialog. A browser offers that
  only once it judges the site worth it (Chrome: a tap and about 30 seconds on
  the site, on any visit); until then the band names the menu item (*Install
  app* or *Add to Home screen*), and it becomes the **Install** button the
  moment the browser offers one. Firefox never offers it to a page, so there
  the menu's way stays.
- **iPhone and iPad** (iOS 16.4 or later, any browser): no browser offers an
  install to a page, so the band says how: **Share** (on iOS 26: **⋯** first)
  → **Add to Home Screen** → **Add**. On iOS 26 leave **Open as Web App** on,
  or the icon opens a Safari tab, which cannot show notifications.
- **×** on the band closes it for good, on every device you sign in on; the
  desktop app's reminder on a computer is a separate one. The offer stays in
  the account menu's **Settings → Preferences → Install as a web app** while
  the browser can install.
- **Once installed**, nothing is offered on that device, in the installed app
  or a browser tab (the browser stops offering it, and filex remembers the
  install in that browser until the browser offers it again).

**Notifications on a phone.** While filex is open - a tab or the installed
app - a new row in the bell raises a notification once you allow it in
**Settings → Notifications** ([browser notifications](NOTIFICATIONS.md#browser-notifications)).
With filex closed, **Push notifications on this device** in the same pane
carries them on ([Web Push](NOTIFICATIONS.md#web-push)). On an iPhone or iPad
both work only in the app added to the Home Screen (iOS 16.4 or later), never
in a Safari tab, and the permission is asked there, from that button.

## Signing in

You are **not** asked for a password in the app. It opens your server's own
login page in your browser, and your browser hands back a one-time code - so
Keycloak, OIDC, passkeys, MFA and corporate SSO work exactly as they do on the
web, often on a session your browser already has (a login form inside the app
could only take a username and password).

1. Type your server address (e.g. `https://files.example.com`, or
   `https://example.com/filex` for a filex [served under a
   sub-path](DEPLOYMENT.md#serving-filex-under-a-sub-path) - every request the
   app makes keeps the path). An address pasted from the browser's address bar
   works too: the page part (`/admin/login`, `/drive/explore`) is dropped, the
   server's own path is not.
2. Your browser opens; sign in however that server expects.
3. The browser hands the app a one-time code and you are in.

**If nothing opens, or the browser cannot get back to the app** - a locked-down
machine, a portable browser the OS has no handler for, or you finished signing
in on your phone - the waiting screen shows a copyable address, and the browser
shows a code you can type into the app by hand.

**If a sign-in fails, you stay where you were.** A sign-in link from an earlier
attempt, or a code the server refuses, leaves the app on the waiting screen of
the current attempt - address and code box still there - with what happened
written under them. A refused code cannot be used again (so a code cannot be
guessed by repeating), and **Start again in the browser** begins a new attempt
for the same server. Clicking the tray or Dock icon, or starting the app again
while it waits, keeps that screen too. **Cancel** is the one way back to the
server address.

Add more accounts with **+** on the left rail; switch between them by clicking.

**Signing in again** to the same server as the same person (with **+**) replaces
that account's credential and keeps everything else - its synced folders, its
filex folder, its place on the rail - and its sync restarts with the new
credential.

**Sign out** (*Settings → Accounts*) forgets the account on this computer and
stops its sync at once; the files stay where they are, on both sides. Signing
in afterwards makes a new account here, and you keep its folders again from the
explorer. To get a working credential back, use **Reconnect** (below) instead.

### Signed out by the server

When the server stops accepting this computer's credential - an admin revoked
the token, or it expired - the app stops asking: the account's sync stops (the
engine exits instead of retrying every 30 seconds), its bell is no longer
polled, and it stays that way after a restart. You are told once, with a
notification; the rail shows a red dot on the account, and its file view reads
**Signed out of &lt;server&gt;** with two buttons:

- **Reconnect** opens the same browser sign-in for the same server. Sign in as
  the same person and the account gets its new credential and keeps its synced
  folders and its filex folder; sync picks up where it stopped. The same button
  is on the account in *Settings → Accounts*.
- **Sign out** forgets the account, as above.

### The token the app is given

Signing in gives the app a **personal** API token (kind `user`, see
[MCP.md → Token kinds](MCP.md#token-kinds---user-vs-app)) that acts as you: its
scopes are `read,write,delete`, or `read,write` for a **viewer** account - never
`admin`. It appears in your token list as *filex desktop - &lt;platform&gt;*;
revoking it there signs that copy of the app out.

It also carries **`comments:rw`**, so the app reads, adds and deletes comments
as the browser does (the account's own `comments.write` still decides). Other
API keys and agents' tokens hold comments only at `read` unless given more
([RBAC.md → Permissions with a level](RBAC.md#permissions-with-a-level-comments));
the desktop pairing is the one token minted with it unasked. The token's
**Actions → Comments: read only** on the API keys page in your browser makes
that desktop read comments only.

A viewer's desktop carries `write` because changing the account itself - the
profile, the password, two-factor - needs a token's `write`
([RBAC.md → API tokens](RBAC.md#api-tokens-verbs-on-every-surface)), so its
account menu saves what the web app saves. It changes no file: the viewer role
still refuses every write, link and grant, and the API keys page still mints
only `read` for a viewer.

Only your **signed-in browser session** can finish the pairing.
`POST /api/auth/desktop/complete` answers `403` with `reason:
"session_required"` to any API token of either kind, and mints nothing - so no
token can mint a wider one for its owner, or turn an integration's token into a
person's.

**Pairings from older versions are not converted on upgrade:**

- **Made before 0.43.0, it holds an `app` token**: the window answers `403`
  (`app_token`) on its API keys, S3 keys, SSH keys and NFS exports panels, and
  the explorer hides **Recent**, **Starred** and **Shared with me**. **Sign in
  again** to the same server as the same person (with **+**; the account keeps
  its synced folders), then revoke the old *filex desktop* entry on the API
  keys page in your browser - the app forgets the old token, but nothing
  revokes it on the server. Or an admin hands the existing token back to its
  person with no new sign-in (the command below).
- **A viewer's pairing made before 0.49.0 holds a `read` token**: its account
  menu answers 403 on a save until the app signs in again - **Reconnect** on
  the account in *Settings → Accounts*.

```bash
curl -X PATCH https://files.example.com/api/admin/ai-tokens/42 \
  -H 'Content-Type: application/json' -b cookies.txt -d '{"kind":"user"}'
```

---

## Opening and previewing files

**Office documents open in the app**, in the embedded OnlyOffice editor the web
app uses, when your server has OnlyOffice configured ([OnlyOffice](ONLYOFFICE.md)).

**Each document opens in its own window**, one per document, titled with the
file's name; the explorer window stays where it is, and the documents are
separate windows in the taskbar.

**Single click or double click.** By default a single click **selects** and a
double click **opens** (Enter opens the selection), as in a desktop file
manager; **Settings → Open files with** switches between *Double-click* and
*Single-click*. A touchscreen tap always opens, and the checkbox always
selects.

**Open in new tab** opens the file in your **browser**, on your server's own
editor page (`/files/edit`): the app has no browser tab, so the link is
resolved against your server, not the app's internal `app://filex` origin.

**Images, video, audio and downloads** work as they do on the web. A `<img>`,
`<video>` or download link cannot carry an `Authorization` header, so the app
attaches the account's bearer to the requests it makes to that account's own
server - and to no other origin.

---

## Searching everywhere (⌘K)

**Ctrl+K** (⌘K on a Mac) opens the web explorer's palette, and its
**Everywhere** group searches file names **and contents** across every drive
the account can reach - the server's full-text index
([Search](SEARCH.md#which-explorer-box-asks-what)), not only the folder on
screen. Each result names its drive and folder; a content match shows the line
it was found in. A result is something to act on:

- **Click it** (or Enter) - the app goes to its folder with the row ticked, and a
  file opens in its own window, as a double-click would.
- **The download button** on the row saves it without opening anything; a folder
  arrives as one `.zip`.
- **Drag it** out onto the desktop or into a folder, as from the file list
  ([Dragging files out](#dragging-files-out)), folders included.

**Every account on the rail is searched at once**, each as **that** account:
nobody sees a file they could not open in their own account. The results are
grouped under one badge per account - the server's name (or address), the
email signed in there, the account's rail colour - with the account you are
looking at first, and a result opens, downloads and drags with its own
account's sign-in. Opening one switches the rail to it. A signed-out account is
left out until you reconnect it.

Recent and Starred are in the left panel, as on the web.

---

## Opening documents from your computer

Double-click a Word, Excel or PowerPoint file **on your own disk** - or a CSV -
and it opens in the editor your filex server runs, with no Office installed on
this computer.

**Types filex will handle:** `.docx` `.doc` `.xlsx` `.xls` `.pptx` `.ppt`
`.odt` `.ods` `.odp` `.rtf`, and `.csv`. Deliberately nothing else - images,
PDFs and code already open in something on every OS. With ONLYOFFICE on the
server a `.csv` opens in the spreadsheet editor, and a save as CSV comes back
in the file's own dialect
([ONLYOFFICE.md → CSV files](ONLYOFFICE.md#csv-files)).

**Your server needs OnlyOffice** for the editing itself
([OnlyOffice](ONLYOFFICE.md)). Without it the document opens in whatever viewer
your server has for that type.

### What happens to the file

Two things can happen, and filex picks the right one per document:

| The document is… | What filex does |
|---|---|
| inside a folder you **keep on this computer** | Opens its twin on the server directly. Nothing is copied. Saving goes to the server, and sync brings it back down to that same file - the one on your disk. |
| anywhere else | Copies it to a hidden working folder on your account (`<storage>://.filex-open`), opens that, and **writes every save back over your original file** - as long as it is still the version filex last read or wrote ([below](#when-the-file-changes-while-it-is-open)); a save in another format - an old `.doc` saved as DOCX - goes beside it instead ([below](#a-save-in-another-format)). When you close the window the copy is deleted. (The server allows only this round trip's writes in that folder: [BACKEND.md → Names filex keeps for itself](BACKEND.md#names-filex-keeps-for-itself).) |

In the second case a strip along the bottom of the editor window names the file
on your disk that saves are landing on, for as long as the window is open.

**The copy is cleaned up** when the editor window closes, after a short wait:
OnlyOffice writes its last save about ten seconds *after* the editor
disconnects. Deleted copies land in your account's trash like anything else you
delete, and age out under the same retention policy.

**If filex is closed or crashes while a document is open**, the next start
deals with the copy. An edit that never reached your disk is saved *beside*
your document as `report.filex-recovered-<time>.docx`, and you are told - never
over your file, which may have moved on in the meantime.

**If a save cannot be written back** - the document is locked by another
program, or its folder turned read-only - a notification and a dialog say so
and name the file that holds your edit. A save never fails silently.

### A save in another format

ONLYOFFICE saves no Word 97, Excel 97 or PowerPoint 97 file: an edited
`rapor.doc` comes back as **DOCX**, an `.xls` as XLSX, a `.ppt` as PPTX (and a
Document Server set to `assemblyFormatAsOrigin: false` saves an ODF file as
OOXML too; [ONLYOFFICE.md → A save in another format](ONLYOFFICE.md#a-save-in-another-format)).
Your `rapor.doc` is **never written** with them - a DOCX under a `.doc` name is
a file some programs open and some refuse. Instead:

| | |
|---|---|
| **The first save** | goes **beside** the document as `rapor.docx`, in the same folder - or, when that name is taken (an earlier edit of the unchanged `rapor.doc`, a file of your own), the next free name, `rapor (2).docx`; nothing is replaced. A notification says *Your edit was saved as rapor.docx*, and the strip under the editor names the file later saves go to. |
| **Every later save** of that window | goes to **that same** `rapor.docx`, not a new file, while it is still what filex wrote there. Changed in another program meanwhile, it is not written over: the save goes beside it as `rapor.filex-conflict-<time>.docx`, and the saves after it follow. |
| **A `.csv`** | saved as CSV goes over the `.csv` itself, like any save over its document. Saved as a spreadsheet (a server that sends one back), it goes beside it as `Tablo.xlsx`. |

When the window closes, the notification names the file that holds the edit
(`rapor.docx`). The copies on the server - the working copy and the saves
beside it - go with the session; after a crash, a save that never came home is
put beside the document as `rapor.filex-recovered-<time>.docx`.

The server writes such a save beside its working copy
(`.filex-open/<session>-rapor.docx`). A server older than 0.51 writes it over
the working copy instead; the app tells it by its bytes (a zip under a `.doc`
name) and puts it in the same place.

### When the file changes while it is open

Something else may rewrite the document while filex has it open: an agent, a
script, another editor, a sync client. filex watches the file (its folder, so
a program that saves through a temporary file renamed over the document is
seen too; on a drive that sends no change events the check every 2.5 seconds
finds it), and **never writes a save over a version it did not see**:

| In the filex window… | What happens |
|---|---|
| nothing unsaved | The new version is loaded into the editor, and a note says *The file was updated outside filex; the new version is loaded.* |
| changes of your own (or a save filex is holding) | You are asked which version stays: **Keep the outside version** - your changes in filex are dropped and the new version is loaded; **Write mine** - your version replaces it when it is saved; **Keep both** - it stays where it is, and your saves go beside it as `report.filex-conflict-<time>.docx`. |

Until you answer, nothing is written over either version, and a save that
arrives meanwhile waits. Escape puts the question away; a line under the
editor's bar brings it back. Closing the window without answering keeps both:
your version goes beside the document as a conflict copy, and you are told. A
save that arrives after the window closed and finds the document changed goes
beside it too.

What is compared is the **content**: filex's own saves are never taken for a
change from outside, a timestamp that moved over the same bytes (a backup tool,
`touch`) is no change, and a same-size rewrite within the file system's
timestamp precision (FAT keeps two-second times) is still caught.

The new version reaches your account as a new working copy, so a late save of
the editor you were in can never land on it; that late save is kept beside the
document, unless you chose to drop your changes. Working copies go with the
session, and after a crash the next start recovers what is left (beside the
document) and removes it.

> A server older than this app serves an editor page that cannot say whether
> it holds unsaved changes. With such a server the app asks the same question
> in a dialog of its own, every time the file changes.

A document inside a folder you keep on this computer is the server's file: the
change reaches the server through sync, and the editor hears it from the server
([ONLYOFFICE.md → When the document changes while it is open](ONLYOFFICE.md#when-the-document-changes-while-it-is-open)).

### Making filex the app that opens them

Installing filex makes it **available** under "Open with". It never takes a file
type over by itself - that is your decision, and *Settings → Open documents with
filex* has the button that gets you to it.

| | What the button does | What it cannot do |
|---|---|---|
| **Windows** | Opens the OS's own **Default apps** page, where you pick filex for the type. | Set the default for you: Windows protects that choice (the `UserChoice` key) so that no application can write it. |
| **Linux** | Runs `xdg-mime default filex-app.desktop …` for these types - which genuinely sets the default. (A snap cannot reach your desktop's settings; Settings explains the file manager's *Open with* instead.) | - |
| **macOS** | Explains where: Finder → **Get Info** → *Open with* → filex → **Change All…** | Set it for you: Electron exposes no binding for the macOS call that does, and filex ships no native code. |

On macOS filex registers with rank *Alternate* on purpose: it appears in the
"Open with" list and never becomes the handler for a document type merely
because nothing else has claimed it.

> A run from source (`electron .`) is registered with nothing - only an
> installed copy is. Settings says so, rather than offering a button that would
> point your OS at a copy of Electron.

---

## Notifications and your account

The top bar ends the way the web app's does: a **bell** and your **avatar**.
They are the web app's own components, drawn by the file list itself
(`config.notifications` and `config.account` in
[INTEGRATION.md](INTEGRATION.md)), so the two front doors cannot drift apart.

**The bell** carries the number of notifications you have not read (exact to
99, `99+` above), opens onto the newest fifteen with *Mark all read*, and
*View all* opens the whole list over the files: page by page, *Unread only*,
and *Mark read* on each row. Clicking a notification **marks it read and takes
you to what it is about, in this window** - the folder with the file selected
(and the app's screen on it, when an app asked for one), the Trash with the
deleted item selected, or an app's home view. A share opens its public page in
your browser; a notification with nowhere to go takes no click. Administrators
also get *Manage notifications*, which opens the server's console in the
browser. The list is the server's, so a notification read in the web app is
read here too.

**The native notification** (a Windows toast, macOS Notification Center, the
Linux desktop's) still arrives for everything new, also with the window closed
and filex in the tray. Clicking it opens the same place as its row in the bell
does, **and marks it read**. *Settings → Notifications* turns the native ones
off; the bell stays. The app reads the bell once every 15 seconds for the
native notifications, the dock / taskbar badge (and the tray tooltip) and the
bell in the window alike - the window adds no requests of its own.

**The avatar** holds your settings as a person on this server - the
interface's. The app's own settings are behind ⚙ at the bottom of the rail.

| Row | What it does |
|-----|--------------|
| **User settings** | Opens the web app's settings dialog **in this window** - the same dialog, not a copy: *Profile* (name, e-mail, username, picture), *Preferences* (time zone, light / automatic / dark and the palette, compact file list, how an untouched folder opens, your storage), *Notifications* (the bell on or off, which events reach you) and *Security* (password, two-factor sign-in). It saves to your account on the server, so a browser shows the same choices. |
| **Admin panel ↗** | Administrators only - the server's console, in the browser. |
| *Shortcuts*, *Restart the tour* | The file list's own settings that are not in the dialog (a narrow window keeps a "..." for the folder's own verbs only). The view modes and the details panel are not here either: their switches are on screen at the end of the folder bar. |

The server's version sits at the foot, as it does on the web.

**Signing out is not in the avatar**: signing an account out of *this app* is
done in *Settings → Accounts*, beside every other account on the rail.

What the avatar and its dialog do **not** repeat, because it has a home in this
app already: the language and single-click opening, running in the background,
native notifications and starting at sign-in (*Settings → This app* - the web
dialog's own switches for them are left out here); synced folders, the filex
folder, *Pause sync*, bandwidth and hours (*Settings → Synced folders*);
storage connections and mounting as a drive (*Settings*); other accounts and
signing out (the rail, and *Settings → Accounts*). The web dialog's start page
and desktop-app downloads mean nothing in the app and are not drawn.

### The App store

When an administrator shows the **store screen** to you
([APP-PLUGINS.md → The store screen](APP-PLUGINS.md#the-store-screen)), the
navigation panel has **Apps → App store** here exactly as in the web app -
the same row, under the same rule, decided by the file list and the server,
not by this app. It opens the server's own App store page in a window of its
own, the way a document opens: the catalog of the stores chosen for you,
**Ask for this app** and **My requests**. **Back to files** closes that window.

---

## Language

**The app's language is your account's.** The window, the tray menu, the file
list inside the window, the notifications and the sync engine's messages all
speak the language of the account on screen - the same one the web panel, your
phone's push notifications and your emails use.

*Settings → Language* - **English** or **Türkçe** - **changes your account's
language** on the server, so the web panel follows too. With nobody signed in -
or an account the server has signed out, until you reconnect it - there is
no account to change: the app speaks your operating system's language (or the
account's last known one) and the choice is greyed out. A language changed
somewhere else (the web panel, another computer) reaches the app when you start
it, switch accounts or bring the window forward.

A change moves the whole app at once - this window, the tray menu (built by
another process) and the file list inside it (a separate component with its
own catalogue) - and **keeps the folder you are looking at**; nothing reloads.
The sync engine says its lines under each synced folder itself, in the language
of the account it syncs ([The engine's event stream](#the-engines-event-stream)),
and a changed account language restarts that account's engines. When the
account's language is one a language pack on your server adds, the sync lines
and the notifications come in it even though the window itself draws only
English or Turkish (whichever of the two the operating system uses).

---

## How it looks

There is no appearance setting in *Settings*: the theme and the palette are set
where every other filex front door sets them - the **avatar** at the top right,
*User settings → Preferences → Appearance* (and *Compact file list* beside it).
The app's own chrome - the account rail, Settings, the boot and sign-in
screens, the dialogs - follows that choice in light, dark and every palette,
and the window reopens on the colour it last painted, so a dark palette does
not flash white first.

⚠ This app remembers that choice **on this computer**, in its own window
storage. The web app keeps it on your account
([`/api/me/prefs`](BACKEND.md#interface-preferences)), so a palette picked in a
browser is not carried into this app, and one picked here is not carried into
a browser.

**The windows are frameless.** On **Windows and Linux** filex draws its own
minimize / maximize / close - in a slim title bar on the main window, and in a
reserved top strip on each document window, so the controls never sit on the
document's own top row (OnlyOffice's profile/share stays clear). On **macOS**
the native traffic lights are kept (top-left) and no buttons are drawn. The top
strip is the drag handle in every case, and every dialog and screen of the file
list opens below it, so minimise, maximise and close answer the first click and
the window drags whatever is open. (The dialogs keep Escape, a click outside to
dismiss them, and the keyboard focus inside while open.)

The filex mark and wordmark sit at the top left, as in the web app, and follow
the palette with everything else; the account rail carries servers only. The
sign-in window is the one exception to the palette: it appears before there is
an account whose preference it could read, so it follows your operating system
until you are signed in.

---

## Keeping folders on this computer

The window is the online view of everything on the server. A folder you also
want *on the machine* - offline, in Explorer/Finder, open to every other program -
is one right-click away:

**Right-click a folder → 📌 Keep on this computer.**

- The **first** keep asks where filex may put things on this computer. The
  default is `~/filex/<server>`; anywhere else works too, and the answer is
  remembered per account (signing in again does not ask again).
- Every kept folder mirrors under that root as `<root>/<storage>/<path…>`, so
  the disk reads exactly like the server does.
- A whole **storage** can be kept - right-click it on the drives screen. That is
  the "sync everything" shape, and it stays one pair.
- Keeping a **parent** absorbs folders already kept inside it: one pair, nothing
  downloaded again. A folder inside a kept parent says *Kept on this computer
  with its parent*.
- **A single file** can be kept as well - right-click the file. It mirrors to
  the same place its folder would (`<root>/<storage>/<path>`), syncs both ways,
  and *Open local folder* shows it among its neighbours rather than opening it.
- Kept folders offer **📂 Open local folder** and **☁ Keep online only**. Online
  only asks whether the local copy moves to the Trash or stays where it is,
  naming the folder it means; Cancel cancels. When the copy goes to the Trash,
  the empty folders the mirror created go with it; anything holding real
  content stays.

Everything else stays online-only: the window shows it, the disk does not carry
it.

### Reading the badges

Every row says where it lives, in the grammar drive clients already taught:

| | |
|---|---|
| ✓ | on this computer |
| ◐ | holding kept items somewhere below - the answer to "is anything in here on my disk?" without opening the folder |
| ⟳ | being synced right now |
| ☁ | online-only |

While the engine is working, a strip along the bottom of the window names the
folder it is on and shows what it is doing - listing, or `12/345` with a
progress bar. It disappears when the run settles.

Under each folder in *Settings* the same line says what is true of it now:

- the phase, with its figures ("listing the server - 48,211 items so far",
  "97 changes to make", "finishing up - 40/97");
- "waiting for its first check" for a folder no pass has finished for since
  the app started (a folder just added waits behind the others);
- "watching for changes" only once a pass has left it in step;
- "moving to the new filex folder…" while the filex folder is moved.

### The engine's event stream

Every line about what the engine is doing - a phase with its figures, a folder
another filex syncs, the sync window, an error, *Live* / *Polling* / *Offline*,
a folder that cannot be watched - is the engine's own sentence. The app starts
each watcher with `filex sync run --json` (no `--lang`: the engine speaks the
account's language), reads its events
([the full list](SYNC.md#the-event-stream---json)), acts on each one's code and
figures, and shows its message as it is. The words the window keeps are its own
states: paused, signed out, waiting for the first check, watching, moving, and
that the engine stopped (with the engine's reason after it).

The app always runs the engine it ships with. An engine older than 0.54 (only
possible with `FILEX_CLI` pointing at another binary) does not know `--json`;
the folder then says the engine could not start and may be older than the app,
followed by the engine's own refusal.

An engine that stops on its own is started again after 5 s, 15 s, a minute,
then every five minutes, and the line says so. The tray icon's tooltip carries
the pause, whether a pass is running or a folder is failing, and the unread
count.

### The filex folder on this computer

*Settings ⚙ → filex folder on this computer* names the root, opens it, and can
**change it**. Kept folders move with it: the account's watcher is stopped, each
mirror is relocated under the new root and its pair is **repointed** there
(`filex sync move`), history and all - so the pass that follows is an ordinary
incremental one, and nothing is downloaded or uploaded again. Pairs you made by
hand somewhere else stay where you put them. A folder inside the current root
(or one that contains it) is refused rather than half-moved, and a move to
another drive is copied across, modification times preserved (the engine reads
change from them). If a move fails halfway, the pair follows whichever side
holds the complete folder; if even that cannot be arranged, the folder is
unpaired rather than left pointing at a partial tree, and the dialog says so.

A move runs once at a time ("Change…" reads "Moving…" until it ends), and
nothing starts the account's watcher again before it ends: a watcher reading
half-moved mirrors would see a mass delete.

### When filex holds items back

A folder's **first** sync sometimes finds far more on this computer than the
server has, in a server folder that already has content - which is what an old
copy of the folder (a restored backup, a machine that was away for months)
looks like. The engine holds those items instead of uploading them
([the rule](SYNC.md#a-first-run-that-would-re-upload-a-stale-copy-asks-first)),
and the folder's card in *Settings → Synced folders* says how many items on
this computer are not on the server or differ from it, with two buttons:

- **Upload them** - they are wanted: they go to the server on the next run.
- **Move to local trash** - they are not: after one more question they move into
  this computer's sync trash, kept for 30 days (*Removed by sync*). Nothing on
  the server changes.

Everything else in the folder keeps syncing while it waits, and after either
answer the folder's sync restarts at once.

### Bandwidth and hours

A first sync of a large store can fill the server's line for hours.
*Settings → Synced folders* has three rows for the engine:

- **Download limit** and **Upload limit** - Unlimited, 10, 5 or 1 MB/s, shared
  by all of one account's transfers (they run four at a time); each signed-in
  account has its own. Folder sync only: opening, previewing or dragging a file
  in the window is not limited.
- **When to sync** - Any time, Evenings & nights (19:00-08:00) or Nights
  (22:00-07:00), in this computer's local time. Outside those hours no new round
  starts and the folder reads *waiting for the sync window*; a round still
  running when they end stops the way Ctrl-C stops it - what it finished is
  recorded - and carries on in the next window. The hours are read by the
  engine alone ([`filex sync window`](SYNC.md#filex-sync-window)): the app
  stores the engine's answer, and a stored window the engine refuses is cleared
  at start, with the engine's reason under the row.

A change restarts the watchers at once, so it applies to a transfer already
running. The line under each folder in Settings says what the engine is doing
with it right now - listing the server, moving files, finishing up, waiting for
the window - or the error from its last round. While files move it reads, for
example, *moving files - 120/11,704, 1.2 GB of 52.6 GB - about 8 h 10 min
left*: the byte counts appear once there are bytes to move, the estimate after
the first few seconds, from the average rate so far (so a limit or a busy line
shows up in it).

> Sync runs while the app does, so a folder kept a moment ago starts filling at
> once - no restart - unless sync is paused (see
> [Running in the background](#running-in-the-background)). After that, an edit
> on either side is synced as it happens: a save in the browser is on disk
> within about a second, and a save on this computer is on the server just as
> fast (the numbers are in [Folder sync](SYNC.md#how-fast-a-change-arrives)).
> Under each synced folder the app says how changes reach it - *Live*,
> *Polling* (the server cannot announce changes; they arrive with the
> 30-second check) or *Offline*. A folder that another filex on this computer
> already syncs - a second copy of the app, or `filex sync run` in a terminal -
> says *Another filex on this computer is syncing this folder*; this copy
> leaves it alone and takes it over when that one stops
> ([why](SYNC.md#one-engine-per-folder-on-this-computer)). The engine's rules
> below apply unchanged: the first pass deletes nothing.

---

## Syncing folders by hand

**Settings ⚙ → Sync a folder…** picks a folder on the server by browsing it, then
asks which folder on this computer to keep it in step with. Use it when the
local folder already exists somewhere else - a photo library, a project checkout -
and should stay there instead of moving under the filex root.

The engine is the same one `filex sync` uses from a terminal - one
implementation, one list of pairs - so the app and the CLI can never disagree
about what is syncing. Full rules and limits: **[docs/SYNC.md](SYNC.md)**. In
short:

- The **first sync deletes nothing** - both sides are merged.
- A **delete never beats an edit**.
- Changed in both places → **both are kept**.
- Anything sync removes from this computer is **kept for 30 days**
  (*Settings → Removed by sync*).

---

## Dragging files out

Select anything and drag it onto your desktop, into an Explorer/Finder window,
or into another program. Folders and multi-selections arrive as **separate real
files and folders** - nothing is zipped on the way.

**There is no size limit and no waiting** - a 100 GB file drags out as fast as
a 1 KB one. The operating system copies a dragged file the moment you let go,
straight from a path on this computer, so something has to be at that path
already. filex does that in one of two ways, and picks for you:

**1. Real files, when it already has them.** Anything you keep on this computer,
and any small selection you ticked a moment ago (up to ten files under 8 MB are
fetched quietly in the background as soon as they are selected), is handed to
the drag as a complete file. This route also works when you drop onto an
*application* - a chat window, an image editor - because the program receives
a file that is genuinely there.

**2. Stand-ins, for everything else.** The drag starts with empty placeholders
carrying the right names, which the shell copies in microseconds. filex then
finds the folder they landed in, removes them, and downloads the real content
there, with progress in the window ("42 files so far…", counted inside folders
too) and **Stop**. Stop takes effect at once, in the middle of a file too: the
file on its way down is dropped (its `name.filexpart` removed), every file that
had fully arrived stays, and nothing more is fetched. Of two drops filling in at
the same time, Stop ends the one the line is showing. Nothing is fetched before
the drag, so size stops mattering. Downloads land on `name.filexpart` and are
renamed only once complete, so nothing ever wears the real name half-written.

⚠ Route 2 cannot fill in a drop onto an **application**: nothing is written to
disk, so there is no landing place to find, and the program is left holding the
empty stand-in. The app then says *"Could not find where it was dropped"*.
Dropping into a folder - Explorer, Finder, the desktop - always works. To make a
large file droppable into a program, keep it on this computer first: then
route 1 applies.

⚠ **A copy is always what leaves.** The app never hands the OS the file that
lives inside your synced folder, even when it is identical: a drop the target
completes as a *move* would take that file out of the mirror, and the next sync
run would then remove it from the server too. Prepared copies live in the app's
data folder and are swept after a week.

⚠ Dragging **into** the app is unchanged, and so is dragging a row onto a folder
inside the window: that still moves the file on the server, with no bytes
travelling to this computer.

Route 2 needs a recursive filesystem watch, which Windows and macOS have and
Linux does not; on Linux the app stays on route 1 (prepared copies).

### In a browser

A single file can be dragged out of the web explorer as well - the browser
downloads it into wherever you dropped it. Folders and multi-selections cannot:
a web page can hand the operating system exactly one download. That is a browser
limit, and it is what the desktop app is for.

The browser fetches that download itself, at the drop, sending cookies but
never an `Authorization` header. So what the drag carries depends on how the
page is signed in - the same explorer code decides it the same way on every
surface:

- **A cookie session** (an embed such as a portal's Files tab) hands over the
  plain download URL; the cookie travels with it, through whatever route the
  embed already proxies.
- **A bearer session** - the web app itself after you sign in - hands over a
  **one-file link** that the server mints for you: good for one download, for
  at most a minute, only for that file, and only on the address it was made on.
  At the drop the server asks again whether *you* may still read the file (a
  permission taken away in between refuses it), and the download is written to
  the Audit log (*downloaded by dragging it out*). The link itself never is.

A row and a ⌘K result drag out alike. A folder drags out in neither
session - the download button gives it as one archive.

Nothing of an end-to-end encrypted folder or a vault is dragged out - not in
the browser, not in the desktop app, and not from a ⌘K result, Recent,
Starred, a tag or a search either, while a plain folder is open: the bytes
there are ciphertext, and the operating system would save them under the
file's name as if they were the document. **Download** gives such a file
decrypted; inside the window the drag still moves it.

⚠ **The link is asked for before the drag, not during it.** A page has to fill
in the drag the instant it starts and cannot wait for the server there, so the
explorer asks while the pointer rests on a file row and again when you press.
That takes milliseconds, so the gap only shows over a very slow connection
([measured](CONTRIBUTING.md#desktop-drag-out-link-timing)). A drag that still
beats it carries no download, and dropping it on the desktop does nothing;
drag again. It never drops a file that would turn out to be an error page.

## Sharing

The share dialog is the same one the web app has - **Share** on any file or
folder. Create a link, copy it, email it, or hand it to the system with
**📤 Share**, which opens:

| Platform | Pressing 📤 Share opens |
|---|---|
| macOS | The **real system share sheet** - AirDrop, Messages, whatever you have. |
| Windows / Linux | A **native menu**: copy the link, copy the whole message, send by email, open in a browser. |
| Phone / browser | The OS share sheet, as always - this is where it has worked all along. |

The Windows share sheet needs WinRT, which Electron does not expose. Rather than
draw an imitation of it, the app offers the two things people actually do with a
link.

## Mounting the server as a drive

*Settings → Mount as a drive* attaches the server - every storage you can see -
as a drive of the operating system over WebDAV, and **Detach** takes it off
again. The credential is the account's own token, handed to the system's
mounter on its input: it never appears on a command line or in the log. The
drive lasts for the session, and after a restart the app shows none rather than
claim one a reboot may have dropped.

| Platform | Mounts with | Where it appears | Verified |
|---|---|---|---|
| Windows | Windows' WebDAV client (its WebClient service, started for you) | A free drive letter, from Z: down | Yes, end to end |
| macOS | `mount_webdav`, Finder's client | `~/filex-drives/filex` - a folder of your own. (Not `/Volumes`: a user cannot create a folder there.) | Not yet on a Mac |
| Linux | `gio mount` (GVfs) | Where GVfs mounts it, listed by your file manager | Not yet |

⚠ It needs HTTPS. Windows refuses to send the credential to a WebDAV drive over
plain `http://`, and elsewhere it would travel in the clear. Very large files
are better synced than opened through the Windows drive. For mounting by hand,
[WebDAV](WEBDAV.md) has the same addresses.

## Running in the background

Closing the window leaves filex in the tray and **sync keeps running**. Turn
that off in *Settings → Keep running in the background*, and closing the window
quits.

*Start when I sign in* registers filex as a login item. Settings reports what
the OS actually did with that request, not what was asked for, since policies
and sandboxes often refuse it; on Windows an entry still in the registry but
switched off in Task Manager's *Startup apps* reads as off.

That switch is the **only** thing that writes the login item. If you turn filex
off in the OS's own list instead - Task Manager's *Startup apps* or *Settings →
Apps → Startup* on Windows, *Login Items* on macOS, the autostart entry on
Linux - the app takes that as your answer: at its next start the switch turns
itself off to match, and filex never puts itself back. (After a reinstall into
a different folder the switch can read off for the same reason; turn it on
again once.)

While any folder is being synced, filex keeps the computer from **idle-sleeping** -
a first sync of a large store can take all night. The screen still dims and
locks as usual, and a closed lid, the power button or a flat battery still put
the machine to sleep. The hold is released the moment the round settles, and on
quit.

**Pause sync** - in the tray menu, and as a switch at the top of *Settings →
Synced folders* - stops every folder's sync, on every account, until you resume
it, and stays paused across a restart, a reboot and the hidden start at
sign-in. The window keeps working; only the background transfers stop, and the
tray icon's tooltip says *sync paused*. Resume starts the watchers again, and
each folder picks up where it left off.

Quit properly from the tray menu.

---

## Updates

The app updates itself without asking. It checks a few times a day, downloads
in the background, and installs at a moment that costs you nothing: when you
quit, or - since the app normally sits in the tray for days - once the machine
has been idle for ten minutes with no window open. It comes back where it was,
in the tray. No installer window, no restart prompt.

The sync watchers are stopped before the swap and start again on their own
afterwards, so an update never lands in the middle of a transfer. The idle-time
install also **waits for sync** - an idle machine with no window open is exactly
what an overnight first sync looks like - so it installs only once no folder is
being worked on (or when you quit).

*Settings → Updates* shows what it is doing and offers **Install it now**.
`FILEX_NO_UPDATE=1` turns the whole thing off.

Every release is installed over the previous one on real Windows and Ubuntu
machines (x64 and arm64) before it ships - the installer, the `.deb`, the
AppImage and the snap - and fails unless one copy is left, of the new version,
where the old one was, with your accounts and settings as the old one left
them.

> On Windows this only works because the app installs **per-user**. An install
> under `C:\Program Files` needs administrator rights to replace its own files,
> so every update would stop at a UAC prompt - which is why the installer does
> not offer a machine-wide install.
>
> ⚠ **The portable Windows copy does not update itself either**, for a
> different reason: it is one self-extracting `.exe`, with no install directory
> to patch and no installer to hand the running copy over to. As on macOS
> (below), nothing is downloaded that could not be applied: *Settings →
> Updates* says that this copy does not update itself and offers a
> **Download** button. Updating is: fetch the new `.exe`, put it over the old
> one. The `filex-data` folder beside it is untouched, so your accounts and
> settings survive.
>
> ⚠ **On macOS the app does not update itself yet.** The updater refuses to
> swap an unsigned app (Squirrel.Mac checks the signature of what it installs),
> so until the build is signed a new version means downloading the new `.dmg`
> yourself. A build that cannot swap itself downloads nothing, and *Settings →
> Updates* shows the new version with a **Download** button instead.

---

## Where your data lives

| What | Where |
|---|---|
| Account tokens | Your OS keychain (Windows Credential Manager / macOS Keychain / libsecret). The app **refuses to store a token in plaintext** if the keychain is unavailable. |
| Which folders are paired | `~/.filex/sync/pairs.json` - shared with the CLI |
| Sync bookkeeping + local trash | `~/.filex/sync/` |
| Window state, account list | The app's own config directory |
| Documents open through "Open with" | One small record per document in `openwith/` under the app's config directory - the note of which local file a working copy has to go home to. It is written before the editor opens, which is what makes a crash recoverable, and removed when the copy is cleaned up. |
| An edit that could not be written back | `openwith-recovered/` under the app's config directory, when even the document's own folder refused the write. The dialog names the exact path. |

**The portable copy moves every one of those rows into one folder beside the
`.exe`** - including `~/.filex/sync`, which is otherwise shared with the CLI.
See below.

Signing out removes the account and its token, and stops syncing its folders.
**Your files are left exactly where they are** on both sides - unpairing is not
deleting.

---

## Portable (Windows)

`filex-desktop-portable-x64.exe` is one file. Nothing is installed, nothing is
written to the registry, and there is no Start-menu entry. Put it on a USB
stick, in `Downloads`, on a machine where you are not allowed to install
software, and double-click it.

**Everything it keeps goes in a `filex-data` folder next to the `.exe`** - the
account list, your settings, the log, the drag-out cache, the working copies of
documents opened with *Open with filex*, and the sync engine's own bookkeeping.
Delete the folder and nothing of yours is left on a machine that is not yours;
*Settings* shows its exact path with a button that opens it.

```
D:\
├── filex-desktop-portable-x64.exe
└── filex-data\          ← everything. Delete this and you are gone.
```

Move both to another folder - or another disk - and the app carries on with the
same accounts. Copy only the `.exe` and it starts fresh beside its new home.

Two things it does **not** do, on purpose:

- **It does not update itself.** See [Updates](#updates).
- **Your accounts do not travel between machines.** Tokens are encrypted with
  the machine's own keychain (Windows DPAPI), so a `filex-data` folder carried
  to a different computer - or to a different Windows account - cannot be
  decrypted there and you sign in again: a stick left behind on a train does
  not hand anyone your server.

> ⚠ **If the `.exe` sits somewhere it cannot write** - `C:\Program Files`, a
> read-only stick, a share mounted without write access - it still starts, falls
> back to `%APPDATA%\@brftech\filex-desktop-portable` and **says so in Settings,
> with the path**. That directory is deliberately not the installed app's own
> profile: a copy carried in from outside must never read or write the accounts
> of whoever owns the machine.

While it runs, the app unpacks itself into a temporary directory (that is what
a single-file `.exe` is) and removes it again when you quit. Only the `.exe`
and its `filex-data` are left where you put it.

---

## Troubleshooting

**A folder dragged out stayed empty** - update the app: two causes of it were
fixed in 0.27.2 and 0.27.3. If it still happens, the log at
`%APPDATA%\@brftech\filex-desktop\logs\filex-desktop.log` (Windows) says which
step stopped: look for the `[drag]` and `[xfer]` lines of that gesture.

**"Could not find where it was dropped" after dragging out** - the drop went to an
application rather than a folder, so there was nowhere on disk to put the file.
Drop into a folder (or the desktop), or keep the file on this computer first and
drag it from there.

**"Could not reach &lt;server&gt;"** on the file view - the server did not answer
the file listing: it is down, or this computer is off the network. *Try again*
once it is back. (A server that answers but refuses the credential shows
**Signed out of &lt;server&gt;** instead - see
[Signed out by the server](#signed-out-by-the-server). Use **Reconnect** there;
do not sign out first: signing out forgets which folders the account was
keeping on this computer.)

**Nothing syncs, and Settings says the engine is missing** - the package could
not find the `filex` binary it ships with. Reinstall, or point the app at a CLI
you have with `FILEX_CLI=/path/to/filex`.

**A folder shows "attention"** - the line under it is the engine's own last
message; `filex sync run --pair <id>` in a terminal shows the same thing with
more detail. It is the news from that folder's LAST round, not a verdict: it
clears by itself (and the dot on the rail turns back) as soon as a later round
of the same folder goes through.

**An Office document will not open**, or the editor area stays blank - first
check that your server has OnlyOffice configured at all
([OnlyOffice](ONLYOFFICE.md)); the web app in your browser is the quickest test.
If it works there but not here, update the app: on v0.13.4 and older the
editor's config request was answered `401` because the app's token never
reached it, and **Open in new tab** did nothing. A build older than v0.13.0
opens on an admin panel instead of your files.

**Double-clicking a .docx still opens the old app** - installing filex adds it
to the "Open with" list; it does not become the default. Pick it once: *Settings
→ Open documents with filex* takes you to the right place on your OS. On Windows
that page is the only place the default can be set at all.

**filex says it cannot open the file** - it opens office documents only (the
list is in *Settings*). Anything else stays with the app you already use.

**"Sign in to filex first"** - the app has no account yet, so there is no server
to open the document on. Add one and try again.

**The edit did not reach my document** - filex tells you when a write-back
fails, and names where it kept your edit. If you saw no message, look for
`[openwith]` lines in the log
(`%APPDATA%\@brftech\filex-desktop\logs\filex-desktop.log` on Windows): every
upload, write-back and cleanup leaves one.

**filex recovered an unsaved edit** - the app was closed or crashed while a
document was open, and the working copy on the server was newer than the file on
your disk. The newer version is beside your document as
`<name>.filex-recovered-<time>.<ext>`; compare the two and keep the one you
want. Your original was not overwritten.
