# filex desktop app

The filex explorer in its own window, with folder sync that keeps running in the
background. Windows, Linux and macOS (Apple Silicon) — and on every one of
them there is a copy that runs without being installed.

It is the same explorer the web app and embedders use — not a separate,
half-finished copy. What it adds on top: **several accounts at once**, **folders
synced to your disk**, and **staying alive in the tray** so that sync actually
happens when the window is closed.

It is **not** an admin console. Your server's own settings live in its admin
panel, and the app links out to it in your browser.

---

## Install

### With a package manager

The package managers install the same app and keep it updated themselves (the
app then leaves updating to them). The desktop app is **`filex-app`** in every
one of them; plain `filex` is the [CLI](CLI.md).

| Platform | Command | Notes |
|---|---|---|
| Ubuntu and other Linux with snapd | `sudo snap install filex-app` | Also in Ubuntu's App Center — search for *filex*. The sign-in is stored in your keyring once the snap may reach it: `sudo snap connect filex-app:password-manager-service` (the app says so when it is needed). |
| macOS 13+ (Apple Silicon) | `brew install brf-tech/filex/filex-app` | Homebrew tap [`BRF-Tech/homebrew-filex`](https://github.com/BRF-Tech/homebrew-filex). The first launch is blocked once, as below: the app is not signed with a Developer ID. |
| Windows 10/11 | `winget install BRFTech.filex-app` | The same per-user installer as the download below. A new package waits for winget's review before it can be installed, so this works a few days after the first release that submits it. |

### Download

| Platform | File | What it does |
|---|---|---|
| Windows 10/11 (64-bit) | [`filex-desktop-x64.exe`](https://github.com/BRF-Tech/filex/releases/latest/download/filex-desktop-x64.exe) | Installer. Installs for **your user only** (`%LOCALAPPDATA%\Programs\filex`) — no administrator rights, and the app can replace its own files, which is what lets it update itself quietly. Adds a Start-menu entry. |
| Windows 10/11 (64-bit) | [`filex-desktop-portable-x64.exe`](https://github.com/BRF-Tech/filex/releases/latest/download/filex-desktop-portable-x64.exe) | **Portable** — nothing is installed. Double-click it wherever it is: a USB stick, `Downloads`, a work machine you may not install software on. It keeps its files in a `filex-data` folder beside itself. See [Portable](#portable-windows) below. |
| Linux (any, 64-bit) | [`filex-desktop-x86_64.AppImage`](https://github.com/BRF-Tech/filex/releases/latest/download/filex-desktop-x86_64.AppImage) | **Portable** — no installation. `chmod +x` and run. |
| Debian / Ubuntu | [`filex-desktop-amd64.deb`](https://github.com/BRF-Tech/filex/releases/latest/download/filex-desktop-amd64.deb) | System-wide install (package `filex-app`), appears in your applications menu. |
| Fedora / openSUSE | [`filex-desktop-x86_64.rpm`](https://github.com/BRF-Tech/filex/releases/latest/download/filex-desktop-x86_64.rpm) | The same, as an RPM (package `filex-app`). |
| macOS 13+ (Apple Silicon) | [`filex-desktop-arm64.dmg`](https://github.com/BRF-Tech/filex/releases/latest/download/filex-desktop-arm64.dmg) | Drag to *Applications*. **Unsigned** — see the first-launch note below. Intel Macs: no build; the web app works there. |

```bash
# Debian / Ubuntu
sudo apt install ./filex-desktop-amd64.deb

# Fedora / openSUSE
sudo dnf install ./filex-desktop-x86_64.rpm     # openSUSE: sudo zypper install ./filex-desktop-x86_64.rpm

# Anything else
chmod +x filex-desktop-x86_64.AppImage
./filex-desktop-x86_64.AppImage
```

On Linux the installed app's command is **`filex-app`** — `filex` is the
[CLI](CLI.md). Up to 0.43.x the desktop package itself was called `filex` and
took that command; installing a newer .deb or .rpm replaces it in one step, and
the app carries over its *Start when I sign in* entry and the default-app
choices made under the old name.

```powershell
# Windows, nothing installed: put the .exe where you want it and run it.
# The folder it makes beside itself is the whole of what it leaves behind.
.\filex-desktop-portable-x64.exe
```

> ⚠ **The packages are not code-signed yet.** Windows SmartScreen will show
> "Windows protected your PC" — *More info* → *Run anyway*. This is a real gap,
> not something to wave away: a signing certificate is a paid, separate step.
> Verify what you downloaded against `checksums.txt` on the release if you want
> certainty.
>
> ⚠ **macOS, first launch.** The app is not signed with a Developer ID, only
> *ad-hoc sealed*, so a downloaded copy is blocked once: open **System Settings →
> Privacy & Security → Open Anyway** (older versions: right-click → *Open*).
> Without that seal macOS 26 would not warn but *refuse* — "malware blocked and
> moved to Trash", no override — which is why the seal is there. Signing +
> notarization is the actual fix, and the same paid decision as on Windows.

**Server requirement:** filex **v0.11.0 or newer**. The app signs in through two
endpoints that older servers do not have, and will tell you so rather than
failing silently.

---

## Signing in

You are **not** asked for a password in the app. It opens your server's own
login page in your browser, and your browser hands back a one-time code.

That is not decoration: a login form inside the app could only ever do
username + password, which would lock out every installation behind an identity
provider — Keycloak, OIDC, passkeys, MFA, corporate SSO. Your browser already
has that session.

1. Type your server address (e.g. `https://files.example.com`).
2. Your browser opens; sign in however that server expects.
3. The browser hands the app a one-time code and you are in.

**If nothing opens, or the browser cannot get back to the app** — a locked-down
machine, a portable browser the OS has no handler for, or you finished signing
in on your phone — the waiting screen shows a copyable address, and the browser
shows a code you can type into the app by hand. Either route works.

**If a sign-in fails, you stay where you were.** A sign-in link from an earlier
attempt, or a code the server refuses, leaves the app on the waiting screen of
the attempt you are on — the address and the code box are still there — with
what happened written under them. A code the server refused cannot be used
again (so a code cannot be guessed by repeating), and **Start again in the
browser** begins a new attempt for the same server without retyping it. Clicking
the tray or Dock icon, or starting the app again while it waits, keeps that
screen too. **Cancel** is the one way back to the server address. (Before
v0.43.0 any of these threw the waiting screen away and left the attempt
unreachable — [issue #36](https://github.com/BRF-Tech/filex/issues/36).)

Add more accounts with **+** on the left rail; switch between them by clicking.

**Signing in again** to the same server as the same person (with **+**) replaces
that account's credential and keeps everything else — its synced folders, its
filex folder, its place on the rail — and its folder sync restarts with the new
credential by itself.

**Sign out** (*Settings → Accounts*) is not the same thing. It forgets the
account on this computer and its folder sync stops at once; the files stay where
they are, on both sides. The folders do not come back if you sign in afterwards:
that is a new account on this computer, and you keep them again from the
explorer. To get a working credential back for an account you still want, use
**Reconnect** (below) instead of signing out.

### Signed out by the server

When the server stops accepting this computer's credential — an admin revoked
the token, or it expired — the app stops asking. The account's folder sync is
stopped (the engine exits instead of retrying every 30 seconds), its bell is no
longer polled, and it stays that way after a restart. You are told once, with a
notification; the rail shows a red dot on the account, and its file view reads
**Signed out of &lt;server&gt;** with two buttons:

- **Reconnect** opens the same browser sign-in for the same server. Sign in as
  the same person and the account gets its new credential and keeps everything
  else — its synced folders, its filex folder — and sync picks up where it
  stopped. The same button is on the account in *Settings → Accounts*.
- **Sign out** forgets the account, as above.

### The token the app is given

Signing in gives the app a **personal** API token (kind `user`, see
[MCP.md → Token kinds](MCP.md#token-kinds--user-vs-app)) that acts as you: its
scopes are `read,write,delete`, or `read` alone for a **viewer** account — what
you could mint for yourself on the API keys page, never more, never `admin`. It
appears in your token list labelled *filex desktop — &lt;platform&gt;*; revoking it
there signs that copy of the app out.

The pairing is finished by your **signed-in browser session** and nothing else.
`POST /api/auth/desktop/complete` answers `403` with `reason:
"session_required"` to any API token of either kind, and mints nothing — a
token must not be able to mint a wider one for its owner, and an integration's
token must not be able to turn itself into a person's.

⚠ **Pairings made before this version hold an `app` token.** The server minted
them without a kind, which reads as `app`, so the desktop window answered `403`
(`app_token`) on its API keys, S3 keys, SSH keys and NFS exports panels, and the
explorer hid **Recent**, **Starred** and **Shared with me**. An existing pairing
is not converted on upgrade; to fix one, **sign in again** to the same server as
the same person (with **+**) — the account keeps its synced folders — then
revoke the old *filex desktop* entry on the API keys page in your browser: the
app forgets the old token, but nothing revokes it on the server. Or have an
admin hand the existing token back to its person, which needs no new sign-in:

```bash
curl -X PATCH https://files.example.com/api/admin/ai-tokens/42 \
  -H 'Content-Type: application/json' -b cookies.txt -d '{"kind":"user"}'
```

---

## Opening and previewing files

> ⚠ **Everything in this section landed after v0.13.4.** On v0.13.4 and older,
> opening an Office document in the app failed with a `401`, and **Open in new
> tab** did nothing at all. Check [Releases](RELEASES.md) against the version
> Settings reports.

**Office documents open in the app.** Word, Excel and PowerPoint files open in
the embedded OnlyOffice editor, the same one the web app uses, provided your
server has OnlyOffice configured ([OnlyOffice](ONLYOFFICE.md)).

**Each document opens in its own window.** Opening a file (any type) opens it in
a dedicated window, one per document, titled with the file's name — the explorer
window stays where it is. Open a few documents and they are separate windows in
the taskbar, each named after its file.

**Single click or double click, your choice.** By default a single click
**selects** and a double click **opens** (Enter opens the selection) — the
classic desktop file-manager gesture. **Settings → Open files with** switches
between *Double-click* and *Single-click*. A touchscreen tap always opens, and
the checkbox is always the one click that selects.

The app authenticates with a bearer token rather than a cookie, and it hands the
explorer a *function* that returns the current account's token — because the
token changes when you switch accounts, and a value captured once would be the
wrong account's. The explorer used to read that header builder synchronously,
which quietly dropped a token supplied that way: the OnlyOffice config request,
the starred list and recently-opened all answered `401`, and an editor that
cannot fetch its own config simply never appears. The builder is asynchronous
now and every viewer waits for it.

**Open in new tab** opens the file in your **browser**, on your server's own
editor page (`/files/edit`). Inside the app there is no browser tab to open, and
a relative link resolves against the app's internal `app://filex` origin — an
address no operating system can open, which is why the menu entry used to do
nothing visible. It now resolves against your server first.

**Images, video, audio and downloads** work the same way they do on the web. A
`<img>`, `<video>` or download link cannot carry an `Authorization` header, so
the app attaches the signed-in account's bearer to requests it makes to your
server itself. This applies only to the account's own server — nothing is
attached to any other origin.

---

## Searching everywhere (⌘K)

**Ctrl+K** (⌘K on a Mac) opens the same palette the web explorer has, and its
**Everywhere** group searches file names **and contents** across every drive
the account can reach — the server's full-text index
([Search](SEARCH.md#which-explorer-box-asks-what)), not only the folder on
screen. Each result names its drive and folder, and a content match shows the
line it was found in.

A result is something to act on, not only to open:

- **Click it** (or Enter) — the app goes to its folder with the row ticked, and a
  file opens in its own window, as a double-click would.
- **The download button** on the row saves it without opening anything; a folder
  arrives as one `.zip`.
- **Drag it** out onto the desktop or into a folder — the same drag-out the file
  list uses ([Dragging files out](#dragging-files-out)), folders included.

**Several accounts on the rail are searched at once.** The results are grouped
under one badge per account — the server's name (or address), the email signed
in there, and the account's rail colour — with the account you are looking at
first. Every account is searched as **that** account: nobody sees a file they
could not open in their own account, and a result from another account opens,
downloads and drags with that account's own sign-in. Opening one switches the
rail to it. A signed-out account is left out until you reconnect it.

Recent and Starred are in the left panel, as on the web.

---

## Opening documents from your computer

A Word, Excel or PowerPoint file **on your own disk** can be opened with filex.
Double-click it and it opens in the editor your filex server runs — with no
Office installed on this computer.

That is the point of it. Most Linux desktops have no Microsoft Office, many Macs
have none, and plenty of Windows machines have none either; filex already had a
perfectly good editor, and the documents on your desktop had no way into it.

**Types filex will handle:** `.docx` `.doc` `.xlsx` `.xls` `.pptx` `.ppt`
`.odt` `.ods` `.odp` `.rtf`. Deliberately nothing else — images, PDFs and code
already open in something on every OS, and taking a file type away from an app
that handles it better is not an improvement.

**Your server needs OnlyOffice** for the editing itself
([OnlyOffice](ONLYOFFICE.md)). Without it the document still opens, in whatever
viewer your server offers for that type.

### What happens to the file

Two things can happen, and filex picks the right one per document:

| The document is… | What filex does |
|---|---|
| inside a folder you **keep on this computer** | Opens its twin on the server directly. Nothing is copied. Saving goes to the server, and sync brings it back down to that same file — the one on your disk. |
| anywhere else | Copies it to a hidden working folder on your account (`<storage>://.filex-open`), opens that, and **writes every save back over your original file**. When you close the window the copy is deleted. (That folder is the one place among filex's own that the server lets a person write — and only these requests: create it at the root, upload `<session>-<name>` into it, save it from the editor, delete it. See [BACKEND.md](BACKEND.md#names-filex-keeps-for-itself).) |

In the second case a strip along the bottom of the editor window names the file
on your disk that saves are landing on, for as long as the window is open. It is
not decoration: you are editing a copy, and you should be able to see where it
goes home to.

**The copy is cleaned up** when the editor window closes — after a short wait,
because OnlyOffice writes its last save about ten seconds *after* the editor
disconnects, and deleting the copy any earlier would throw that save away.
Deleted copies land in your account's trash like anything else you delete, and
age out under the same retention policy.

**If filex is closed or crashes while a document is open**, the copy is dealt
with the next time the app starts. If it holds an edit that never reached your
disk, that edit is saved *beside* your document as
`report.filex-recovered-<time>.docx`, and you are told. It is never written over
your file — the app was not running, and your local copy may have moved on in
the meantime.

**If a save cannot be written back** — the document is locked by another
program, or its folder turned read-only — filex says so with a notification and
a dialog, and names the file it kept your edit in. A save that silently fails is
the one outcome this feature must never produce.

### Making filex the app that opens them

Installing filex makes it **available** under "Open with". It never takes a file
type over by itself — that is your decision, and *Settings → Open documents with
filex* has the button that gets you to it.

| | What the button does | What it cannot do |
|---|---|---|
| **Windows** | Opens the OS's own **Default apps** page, where you pick filex for the type. | Set the default for you. Since Windows 10 the `UserChoice` registry key is protected by a hash over the extension, your account's SID and a Microsoft salt; an application cannot write it, and forging that hash is exactly what the protection exists to stop. An installer that appears to manage it is either overwriting the plain `.docx` ProgId behind your back or tampering. |
| **Linux** | Runs `xdg-mime default filex-app.desktop …` for these types — which genuinely sets the default. (A snap cannot reach your desktop's settings; Settings explains the file manager's *Open with* instead.) | — |
| **macOS** | Explains where: Finder → **Get Info** → *Open with* → filex → **Change All…** | Set it for you. The API exists (`LSSetDefaultRoleHandlerForContentType`) but Electron exposes no binding for it, and filex ships no native code. |

On macOS filex registers with rank *Alternate* on purpose: it appears in the
"Open with" list and never becomes the handler for a document type merely
because nothing else has claimed it.

> A run from source (`electron .`) is registered with nothing — only an
> installed copy is. Settings says so, rather than offering a button that would
> point your OS at a copy of Electron.

---

## Language

*Settings → Language* — **System**, **English** or **Türkçe**. System follows
your operating system, which is what the app did when there was nothing to
choose, so nothing changes until you pick.

The choice moves the whole app at once: this window, the tray menu (built by a
different process), and the file list inside it (a separate component with its
own catalogue). A Turkish shell around an English file list is one app
pretending to be two — which is what it used to look like before any of this
was translated.

Switching takes effect immediately and **keeps the folder you are looking at**;
nothing reloads.

---

## How it looks

There is no appearance setting in *Settings*, and that is deliberate: the theme
and the palette are a choice about the file list, so they are set where every
other filex front door sets them — the **"..."** menu in the file list
(*Theme*, *Compact view*). The app's own chrome — the account rail, Settings,
the boot and sign-in screens, the dialogs — follows whatever you pick there, in
light, dark and every palette. A second switch here would be a second answer to
the same question.

⚠ This app remembers that choice **on this computer**, in its own window
storage. The web app keeps it on your account instead
([`/api/me/prefs`](BACKEND.md#interface-preferences)), so a palette picked in a
browser is not carried into this app, and one picked here is not carried into
a browser.

The window even reopens on the ground it last painted, so launching filex on a
dark palette no longer flashes white first.

**The windows are frameless.** The native OS title bar is gone. On **Windows and
Linux** filex draws its own minimize / maximize / close — in a slim title bar on
the main window, and in a reserved top strip on each document window so the
controls never sit on top of the document's own top row (OnlyOffice's
profile/share stays clear). On **macOS** the native traffic lights are kept
(top-left) and no buttons are drawn. The top strip is the drag handle in every
case.

The filex mark and wordmark sit at the top left, in the same corner the web app
puts them, and follow the palette with everything else. The account rail down
the side carries servers only — one logo, not two.

The sign-in window is the one exception, and only because it has to be: it
appears before there is an account, so there is nobody whose preference it could
read. It follows your operating system until you are signed in.

---

## Keeping folders on this computer

The window is the online view of everything on the server. A folder you also
want *on the machine* — offline, in Explorer/Finder, open to every other program
— is one right-click away:

**Right-click a folder → 📌 Keep on this computer.**

- The **first** keep asks where filex may put things on this computer. The
  default is `~/filex/<server>`; anywhere else works too, and the answer is
  remembered per account (re-signing in does not ask again).
- Every kept folder mirrors under that root as `<root>/<storage>/<path…>`, so
  the disk reads exactly like the server does.
- A whole **storage** can be kept — right-click it on the drives screen. That is
  the "sync everything" shape, and it stays one pair.
- Keeping a **parent** absorbs folders already kept inside it: one pair, nothing
  re-downloaded. A folder inside a kept parent says *Kept on this computer with
  its parent* rather than pretending it could leave on its own.
- **A single file** can be kept as well — right-click the file. It mirrors to
  the same place its folder would (`<root>/<storage>/<path>`), syncs both ways,
  and *Open local folder* shows it among its neighbours rather than opening it.
- Kept folders offer **📂 Open local folder** and **☁ Keep online only**. Online
  only asks what should happen to the local copy — move it to the Trash, or
  leave it where it is — and names the folder it means. Cancel cancels. When
  the copy goes to the Trash the empty folders the mirror created go with it;
  anything holding real content stays.

Everything else stays online-only: the window shows it, the disk does not carry
it.

### Reading the badges

Every row says where it lives, in the grammar drive clients already taught:

| | |
|---|---|
| ✓ | on this computer |
| ◐ | holding kept items somewhere below — the answer to "is anything in here on my disk?" without opening the folder |
| ⟳ | being synced right now |
| ☁ | online-only |

While the engine is working, a strip along the bottom of the window names the
folder it is on and shows what it is doing — listing, or `12/345` with a
progress bar. It disappears when the run settles.

Under each folder in *Settings* the same line says what is true of it now:

- the phase, with its figures ("listing the server — 48,211 items so far",
  "97 changes to make", "finishing up — 40/97");
- "waiting for its first check" for a folder no pass has finished for since
  the app started (a folder just added waits behind the others);
- "watching for changes" only once a pass has left it in step;
- "moving to the new filex folder…" while the filex folder is moved.

An engine that stops on its own is started again after 5 s, 15 s, a minute,
then every five minutes, and the line says so. The tray icon's tooltip carries
the pause, whether a pass is running or a folder is failing, and the unread
count.

### The filex folder on this computer

*Settings ⚙ → filex folder on this computer* names the root, opens it, and can
**change it**. Kept folders move with it: the account's watcher is stopped, each
mirror is relocated under the new root and its pair is **repointed** there
(`filex sync move`), history and all — so the pass that follows is an ordinary
incremental one, not a first-run merge, and nothing is downloaded or uploaded
again. Pairs you made by hand somewhere else stay where you put them. A folder
inside the current root (or one that contains it) is refused rather than
half-moved, and a move to another drive is copied across — with modification
times preserved, since those are what the engine reads change from — rather
than failing. If a move fails halfway, the pair follows whichever side holds the
complete folder; if even that cannot be arranged, the folder is unpaired rather
than left pointing at a partial tree, and the dialog says so.

A move runs once at a time ("Change…" reads "Moving…" until it ends), and
nothing starts the account's watcher again before it ends: a watcher reading
half-moved mirrors would see a mass delete.


### When filex holds items back

A folder's **first** sync sometimes finds far more on this computer than the
server has, in a server folder that already has content — which is what an old
copy of the folder (a restored backup, a machine that was away for months) looks
like. Uploading all of it would put stale files back on the server, so the
engine holds those items instead and asks. The folder's card in *Settings →
Synced folders* then says how many items on this computer are not on the server
or differ from it, with two buttons:

- **Upload them** — they are wanted: they go to the server on the next run.
- **Move to local trash** — they are not: after one more question they move into
  this computer's sync trash, kept for 30 days (*Removed by sync*). Nothing on
  the server changes.

Everything else in the folder keeps syncing while it waits, and after either
answer the folder's sync restarts at once.

### Bandwidth and hours

A first sync of a large store can fill the server's line for hours, and
everybody else using that server feels it. *Settings → Synced folders* has three
rows for the engine:

- **Download limit** and **Upload limit** — Unlimited, 10, 5 or 1 MB/s. The
  limit is shared by all of one account's transfers (they run four at a time),
  and each signed-in account has its own. It applies to folder sync only:
  opening, previewing or dragging a file in the window is not limited.
- **When to sync** — Any time, Evenings & nights (19:00–08:00) or Nights
  (22:00–07:00), in this computer's local time. Outside those hours no new round
  starts and the folder reads *waiting for the sync window*; a round still
  running when they end stops the way Ctrl-C stops it — what it finished is
  recorded — and carries on in the next window.

A change restarts the watchers at once, so it applies to a transfer already
running. The line under each folder in Settings says what the engine is doing
with that folder right now — listing the server, moving files, finishing up,
waiting for the window — or the error from its last round. While files move it
reads, for example, *moving files — 120/11704, 1.2 GiB of 52.6 GiB — about
8 h 10 min left*: the byte counts appear once there are bytes to move, the
estimate after the first few seconds of transfer, from the average rate so far
(so a limit or a busy line shows up in it).

> Sync runs while the app does, so a folder kept a moment ago starts filling at
> once — no restart — unless sync is paused (see
> [Running in the background](#running-in-the-background)). After that, an edit
> on either side is synced as it happens: a save in the browser is on disk
> within about a second, and a save on this computer is on the server just as
> fast (the numbers are in [Folder sync](SYNC.md#how-fast-a-change-arrives)).
> Under each synced folder the app says how changes reach it — *Live*,
> *Polling* (the server cannot announce changes; they arrive with the
> 30-second check) or *Offline*. A folder that another filex on this computer
> is already syncing — a second copy of the app, or `filex sync run` in a
> terminal — says *Another filex on this computer is syncing this folder*; this
> copy leaves it alone and takes it over when that one stops
> ([why](SYNC.md#one-engine-per-folder-on-this-computer)). The engine's rules
> below apply unchanged: the first pass deletes nothing.

---

## Syncing folders by hand

**Settings ⚙ → Sync a folder…** picks a folder on the server by browsing it, then
asks which folder on this computer to keep it in step with. Use it when the
local folder already exists somewhere else — a photo library, a project checkout
— and should stay there instead of moving under the filex root.

The engine is the same one `filex sync` uses from a terminal — one
implementation, one list of pairs — so the app and the CLI can never disagree
about what is syncing. Full rules and limits: **[docs/SYNC.md](SYNC.md)**. In
short:

- The **first sync deletes nothing** — both sides are merged.
- A **delete never beats an edit**.
- Changed in both places → **both are kept**.
- Anything sync removes from this computer is **kept for 30 days**
  (*Settings → Removed by sync*).

---

## Dragging files out

Select anything and drag it onto your desktop, into an Explorer/Finder window,
or into another program. Folders and multi-selections arrive as **separate real
files and folders** — nothing is zipped on the way.

**There is no size limit and no waiting.** A 100 GB file drags out as fast as a
1 KB one. Understanding why takes one sentence:

> The operating system copies a dragged file at the moment you let go, straight
> from a path on this computer — so something has to be at that path already.

filex satisfies that in one of two ways, and picks for you:

**1. Real files, when it already has them.** Anything you keep on this computer,
and any small selection you ticked a moment ago (up to ten files under 8 MB are
fetched quietly in the background as soon as they are selected), is handed to the
drag as a complete file. This is the route that also works when you drop onto an
*application* — a chat window, an image editor — because the program receives a
file that is genuinely there.

**2. Stand-ins, for everything else.** The drag starts with empty placeholders
carrying the right names, which the shell copies in microseconds. filex then
finds the folder they landed in, removes them, and downloads the real content
there, showing progress in the window ("42 files so far…", counted inside
folders too) with **Stop**, which keeps what has arrived and fetches nothing
more. Nothing is fetched before the drag, so size stops mattering. Downloads land on `name.filexpart` and are renamed only
once complete, so nothing ever wears the real name half-written.

⚠ Route 2 cannot fill in a drop onto an **application**: nothing is written to
disk, so there is no landing place to find, and the program is left holding the
empty stand-in. The app tells you when it could not find where you dropped
(*"Could not find where it was dropped"*) rather than pretending it worked. Dropping into a
folder — Explorer, Finder, the desktop — is the case that always works. If you
want a specific large file to be droppable into a program, keep it on this
computer first: then route 1 applies.

⚠ **A copy is always what leaves.** The app never hands the OS the file that
lives inside your synced folder, even when it is identical: a drop the target
completes as a *move* would take that file out of the mirror, and the next sync
run would then remove it from the server too.

Prepared copies live in the app's data folder and are swept after a week.

⚠ Dragging **into** the app is unchanged, and so is dragging a row onto a folder
inside the window: that still moves the file on the server, with no bytes
travelling to this computer.

Route 2 needs a recursive filesystem watch, which Windows and macOS have and
Linux does not; on Linux the app stays on route 1 (prepared copies).

### In a browser

A single file can be dragged out of the web explorer as well — the browser
downloads it into wherever you dropped it. Folders and multi-selections cannot:
a web page can hand the operating system exactly one download. That is a browser
limit, not a filex one, and it is what the desktop app is for.

The browser fetches that download itself, at the drop, and its download stack
sends cookies but never an `Authorization` header. So what the drag carries
depends on how the page is signed in — the same explorer code decides it the
same way on every surface:

- **A cookie session** (an embed such as a portal's Files tab) hands over the
  plain download URL; the cookie travels with it, through whatever route the
  embed already proxies.
- **A bearer session** — the web app itself after you sign in — hands over a
  **one-file link** that the server mints for you: good for one download, for
  at most a minute, only for that file, and only on the address it was made on.
  At the drop the server asks again whether *you* may still read the file (a
  permission taken away in between refuses it), and the download is written to
  the Audit log (*downloaded by dragging it out*). The link itself never is.

A row and a ⌘K result drag out alike. A folder drags out in neither
session — the download button gives it as one archive.

⚠ **The link is asked for before the drag, not during it.** A page has to fill
in the drag the instant it starts and cannot wait for the server there, so the
explorer asks while the pointer rests on a file row and again when you press.
Measured against a local server (2026-09-25, e2e 71): a mint takes 3–4 ms in
the browser's own request timing (n = 6), and 66 ms passed from Playwright
starting to hover the row to the link being in hand — a person rests on a row
far longer than either before a drag, so the gap only shows over a very slow
connection. A drag that still beats it carries no download, and dropping it on
the desktop does nothing; drag again. It never drops a file that would turn out
to be an error page.

Measured with a real mouse in `e2e/tests/71-admin-drag-out-link.spec.ts`
(bearer: row, ⌘K result, the bytes behind the link, a drag that beats the link,
the cookie session) and `e2e/tests/47-palette-everywhere.spec.ts`; the server
side in `backend/internal/api/handlers/download_link_test.go`.

## Sharing

The share dialog is the same one the web app has — **Share** on any file or
folder. Create a link, copy it, email it, or hand it to the system with
**📤 Share**.

⚠ What that last button does depends on the platform, and it is worth saying
plainly rather than implying more than is there:

| Platform | Pressing 📤 Share opens |
|---|---|
| macOS | The **real system share sheet** — AirDrop, Messages, whatever you have. |
| Windows / Linux | A **native menu**: copy the link, copy the whole message, send by email, open in a browser. |
| Phone / browser | The OS share sheet, as always — this is where it has worked all along. |

The Windows share sheet needs WinRT, which Electron does not expose. Rather than
draw an imitation of it, the app offers the two things people actually do with a
link.

## Running in the background

Closing the window leaves filex in the tray and **sync keeps running** — that is
the point of a desktop app. Turn it off in *Settings → Keep running in the
background*, and the window close becomes a real quit.

*Start when I sign in* registers filex as a login item. Settings reports what the
OS actually did with that request, not what was asked for: policies and
sandboxes refuse it often enough that showing our own intent back would be a
lie. On Windows that means the Task Manager flag too — an entry that is still in
the registry but switched off in *Startup apps* reads as off.

That switch is the **only** thing that writes the login item. If you turn filex
off in the OS's own list instead — Task Manager's *Startup apps* or *Settings →
Apps → Startup* on Windows, *Login Items* on macOS, the autostart entry on
Linux — the app takes that as your answer: at its next start the switch in
Settings turns itself off to match, and filex never puts itself back. (After a
reinstall into a different folder the switch can read off for the same reason;
turn it on again once.)

While any folder is being synced, filex keeps the computer from **idle-sleeping**
— a first sync of a large store can take all night, and an overnight sleep used
to cost hours of it. The screen still dims and locks as usual, and a closed lid,
the power button or a flat battery still put the machine to sleep. The hold is
released the moment the round settles, and on quit.

**Pause sync** — in the tray menu, and as a switch at the top of *Settings →
Synced folders* — stops every folder's sync, on every account, until you resume
it. It is remembered: a paused filex stays paused after a restart, a reboot and
the hidden start at sign-in, which quitting never did. The window keeps working
while sync is paused; only the background transfers stop, and the tray icon's
tooltip says *sync paused*. Resume starts the watchers again, and each folder
picks up where it left off.

Quit properly from the tray menu.

---

## Updates

The app updates itself and you are not asked about it. It checks a few times a
day, downloads in the background, and applies the update at a moment that costs
you nothing: when you quit, or — since this app is normally left in the tray for
days — once the machine has been idle for ten minutes with no window open. It
comes back where it was, in the tray. No installer window, no restart prompt.

The sync watchers are stopped before the swap and start again on their own
afterwards, so an update never lands in the middle of a transfer. The idle-time
install also **waits for sync**: an idle machine with no window open is exactly
what an overnight first sync looks like, so it installs only once no folder is
being worked on (or when you quit).

*Settings → Updates* shows what it is doing and offers **Install it now** for
anyone who would rather not wait. `FILEX_NO_UPDATE=1` turns the whole thing off.

> On Windows this only works because the app installs **per-user**. An install
> under `C:\Program Files` needs administrator rights to replace its own files,
> so every update would stop at a UAC prompt — which is no longer possible: the
> installer does not offer a machine-wide install.
>
> ⚠ **The portable Windows copy does not update itself either**, and for a
> different reason: there is no installation for an update to replace. It is
> one self-extracting `.exe`, so there is no install directory to patch and no
> installer to hand the running copy over to. It is treated exactly like the
> macOS case below — nothing is ever downloaded that could not be applied, and
> *Settings → Updates* says plainly that this copy does not update itself and
> offers a **Download** button. Updating is: fetch the new `.exe`, put it over
> the old one. The `filex-data` folder beside it is untouched, so your accounts
> and settings survive.
>
> ⚠ **On macOS the app does not update itself yet.** The updater refuses to
> swap an unsigned app (Squirrel.Mac checks the signature of what it installs),
> so until the build is signed a new version means downloading the new `.dmg`
> yourself. The app knows that about itself and says so: a build that cannot
> swap itself downloads nothing, and *Settings → Updates* shows the new
> version with a **Download** button instead of promising an install that
> never arrives.

---

## Where your data lives

| What | Where |
|---|---|
| Account tokens | Your OS keychain (Windows Credential Manager / macOS Keychain / libsecret). The app **refuses to store a token in plaintext** if the keychain is unavailable. |
| Which folders are paired | `~/.filex/sync/pairs.json` — shared with the CLI |
| Sync bookkeeping + local trash | `~/.filex/sync/` |
| Window state, account list | The app's own config directory |
| Documents open through "Open with" | One small record per document in `openwith/` under the app's config directory — the note of which local file a working copy has to go home to. It is written before the editor opens, which is what makes a crash recoverable, and removed when the copy is cleaned up. |
| An edit that could not be written back | `openwith-recovered/` under the app's config directory, when even the document's own folder refused the write. The dialog names the exact path. |

**The portable copy moves every one of those rows into one folder beside the
`.exe`** — including `~/.filex/sync`, which is otherwise shared with the CLI.
See below.

Signing out removes the account and its token, and stops syncing its folders.
**Your files are left exactly where they are** on both sides — unpairing is not
deleting.

---

## Portable (Windows)

`filex-desktop-portable-x64.exe` is one file. Nothing is installed, nothing is
written to the registry, and there is no Start-menu entry. Put it on a USB
stick, in `Downloads`, on a machine where you are not allowed to install
software, and double-click it.

**Everything it keeps goes in a `filex-data` folder next to the `.exe`** — the
account list, your settings, the log, the drag-out cache, the working copies of
documents opened with *Open with filex*, and the sync engine's own bookkeeping.
That is the point of the build rather than a detail of it: delete the folder
and nothing of yours is left on a machine that is not yours. *Settings* shows
the exact path with a button that opens it, so you never have to take that on
trust.

```
D:\
├── filex-desktop-portable-x64.exe
└── filex-data\          ← everything. Delete this and you are gone.
```

Move both to another folder — or another disk — and the app carries on with the
same accounts. Copy only the `.exe` and it starts fresh beside its new home.

Two things it does **not** do, and both are deliberate:

- **It does not update itself.** See [Updates](#updates).
- **Your accounts do not travel between machines.** Tokens are encrypted with
  the machine's own keychain (Windows DPAPI), so a `filex-data` folder carried
  to a different computer — or to a different Windows account — cannot be
  decrypted there and you sign in again. That is the right way round: a stick
  you leave behind on a train does not hand anyone your server.

> ⚠ **If the `.exe` sits somewhere it cannot write** — `C:\Program Files`, a
> read-only stick, a share mounted without write access — it cannot keep its
> promise. Rather than refuse to start, it falls back to
> `%APPDATA%\@brftech\filex-desktop-portable` and **says so in Settings, with
> the path**. That directory is deliberately not the installed app's own
> profile: a copy carried in from outside must never read or write the accounts
> of whoever owns the machine.

While it runs, the app unpacks itself into a temporary directory (that is what
a single-file `.exe` is), and removes that directory again when you quit. The
only thing left where you put it is the `.exe` and its `filex-data`.

---

## Troubleshooting

**A folder dragged out stayed empty** — two separate causes, both fixed by
0.27.3; update the app. If it still happens, the log at
`%APPDATA%\@brftech\filex-desktop\logs\filex-desktop.log` (Windows) says which
step stopped: look for the `[drag]` and `[xfer]` lines of that gesture.

**A folder dragged out stayed empty (0.27.1 and older)** — fixed in 0.27.2. A file inside it whose
name was not ASCII (`Türkçe adlı dosya.txt`) made the transfer throw while
reading the response, and everything after it stopped. Update the app; the
server no longer sends such a header either.

**"Could not find where it was dropped" after dragging out** — the drop went to an
application rather than a folder, so there was nowhere on disk to put the file.
Drop into a folder (or the desktop), or keep the file on this computer first and
drag it from there.

**"Could not reach &lt;server&gt;"** on the file view — the server did not answer
the file listing: it is down, or this computer is off the network. *Try again*
once it is back. (A server that answers but refuses the credential shows
**Signed out of &lt;server&gt;** instead — see
[Signed out by the server](#signed-out-by-the-server). Use **Reconnect** there;
do not sign out first: signing out forgets which folders the account was
keeping on this computer.)

**Nothing syncs, and Settings says the engine is missing** — the package could
not find the `filex` binary it ships with. Reinstall, or point the app at a CLI
you have with `FILEX_CLI=/path/to/filex`.

**A folder shows "attention"** — the line under it is the engine's own last
message. `filex sync run --pair <id>` in a terminal shows the same thing with
more detail. It is the news from that folder's LAST round, not a verdict: it
clears by itself (and the dot on the rail turns back) as soon as a later round
of the same folder goes through.

**An Office document will not open**, or the editor area stays blank — first
check that your server has OnlyOffice configured at all
([OnlyOffice](ONLYOFFICE.md)); the web app in your browser is the quickest test.
If it works there but not here, you are on v0.13.4 or older: the editor's config
request was answered `401` because the app's token never reached it. Update.

**"Open in new tab" does nothing** — same story, same fix: on v0.13.4 and older
the app asked the OS to open an `app://filex` address, which no OS can act on.

**Double-clicking a .docx still opens the old app** — installing filex adds it
to the "Open with" list; it does not become the default. Pick it once: *Settings
→ Open documents with filex* takes you to the right place on your OS. On Windows
that page is the only place the default can be set at all.

**filex says it cannot open the file** — it opens office documents only (the
list is in *Settings*). Anything else stays with the app you already use.

**"Sign in to filex first"** — the app has no account yet, so there is no server
to open the document on. Add one and try again.

**The edit did not reach my document** — filex tells you when a write-back
fails, and names where it kept your edit. If you saw no message, look for
`[openwith]` lines in the log
(`%APPDATA%\@brftech\filex-desktop\logs\filex-desktop.log` on Windows): every
upload, write-back and cleanup leaves one.

**filex recovered an unsaved edit** — the app was closed or crashed while a
document was open, and the working copy on the server was newer than the file on
your disk. The newer version is beside your document as
`<name>.filex-recovered-<time>.<ext>`; compare the two and keep the one you
want. Your original was not overwritten.

**The window opens on an admin panel** — you are on a build older than v0.13.0.
Update.
