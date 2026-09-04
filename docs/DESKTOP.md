# filex desktop app

The filex explorer in its own window, with folder sync that keeps running in the
background. Windows, Linux and macOS (Apple Silicon).

It is the same explorer the web app and embedders use — not a separate,
half-finished copy. What it adds on top: **several accounts at once**, **folders
synced to your disk**, and **staying alive in the tray** so that sync actually
happens when the window is closed.

It is **not** an admin console. Your server's own settings live in its admin
panel, and the app links out to it in your browser.

---

## Install

| Platform | File | What it does |
|---|---|---|
| Windows 10/11 (64-bit) | [`filex-desktop-x64.exe`](https://github.com/BRF-Tech/filex/releases/latest/download/filex-desktop-x64.exe) | Installer. Installs for **your user only** (`%LOCALAPPDATA%\Programs\filex`) — no administrator rights, and the app can replace its own files, which is what lets it update itself quietly. Adds a Start-menu entry. |
| Linux (any, 64-bit) | [`filex-desktop-x86_64.AppImage`](https://github.com/BRF-Tech/filex/releases/latest/download/filex-desktop-x86_64.AppImage) | **Portable** — no installation. `chmod +x` and run. |
| Debian / Ubuntu | [`filex-desktop-amd64.deb`](https://github.com/BRF-Tech/filex/releases/latest/download/filex-desktop-amd64.deb) | System-wide install, appears in your applications menu. |
| macOS 12+ (Apple Silicon) | [`filex-desktop-arm64.dmg`](https://github.com/BRF-Tech/filex/releases/latest/download/filex-desktop-arm64.dmg) | Drag to *Applications*. **Unsigned** — see the first-launch note below. Intel Macs: no build; the web app works there. |

```bash
# Debian / Ubuntu
sudo apt install ./filex-desktop-amd64.deb

# Anything else
chmod +x filex-desktop-x86_64.AppImage
./filex-desktop-x86_64.AppImage
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

Add more accounts with **+** on the left rail; switch between them by clicking.

---

## Opening and previewing files

> ⚠ **Everything in this section landed after v0.13.4.** On v0.13.4 and older,
> opening an Office document in the app failed with a `401`, and **Open in new
> tab** did nothing at all. Check [Releases](RELEASES.md) against the version
> Settings reports.

**Office documents open in the app.** Word, Excel and PowerPoint files open in
the embedded OnlyOffice editor, the same one the web app uses, provided your
server has OnlyOffice configured ([OnlyOffice](ONLYOFFICE.md)).

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
| anywhere else | Copies it to a hidden working folder on your account (`<storage>://.filex-open`), opens that, and **writes every save back over your original file**. When you close the window the copy is deleted. |

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
| **Linux** | Runs `xdg-mime default filex.desktop …` for these types — which genuinely sets the default. | — |
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


> Sync runs while the app does, so a folder kept a moment ago starts filling on
> the next round (30 seconds) — no restart. The engine's rules below apply
> unchanged: the first pass deletes nothing.

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
  (*Settings → Recently deleted*).

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
and any small selection you clicked a moment ago (up to ten files under 8 MB are
fetched quietly in the background as soon as they are selected), is handed to the
drag as a complete file. This is the route that also works when you drop onto an
*application* — a chat window, an image editor — because the program receives a
file that is genuinely there.

**2. Stand-ins, for everything else.** The drag starts with empty placeholders
carrying the right names, which the shell copies in microseconds. filex then
finds the folder they landed in, removes them, and downloads the real content
there, showing progress in the window. Nothing is fetched before the drag, so
size stops mattering. Downloads land on `name.filexpart` and are renamed only
once complete, so nothing ever wears the real name half-written.

⚠ Route 2 cannot fill in a drop onto an **application**: nothing is written to
disk, so there is no landing place to find, and the program is left holding the
empty stand-in. The app tells you when it could not find where you dropped
(*"Bırakılan yer bulunamadı"*) rather than pretending it worked. Dropping into a
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

## Sharing

The share dialog is the same one the web app has — **Share / Permissions** on any
file or folder. Create a link, copy it, email it, or hand it to the system with
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
lie.

Quit properly from the tray menu.

---

## Updates

The app updates itself and you are not asked about it. It checks a few times a
day, downloads in the background, and applies the update at a moment that costs
you nothing: when you quit, or — since this app is normally left in the tray for
days — once the machine has been idle for ten minutes with no window open. It
comes back where it was, in the tray. No installer window, no restart prompt.

The sync watchers are stopped before the swap and start again on their own
afterwards, so an update never lands in the middle of a transfer.

*Settings → Updates* shows what it is doing and offers **Install it now** for
anyone who would rather not wait. `FILEX_NO_UPDATE=1` turns the whole thing off.

> On Windows this only works because the app installs **per-user**. An install
> under `C:\Program Files` needs administrator rights to replace its own files,
> so every update would stop at a UAC prompt — which is no longer possible: the
> installer does not offer a machine-wide install.
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

Signing out removes the account and its token, and stops syncing its folders.
**Your files are left exactly where they are** on both sides — unpairing is not
deleting.

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

**"Bırakılan yer bulunamadı" after dragging out** — the drop went to an
application rather than a folder, so there was nowhere on disk to put the file.
Drop into a folder (or the desktop), or keep the file on this computer first and
drag it from there.

**"Could not reach &lt;server&gt;"** on the file view — the app reached the sign-in
step but not the file listing. Usually the token was revoked server-side; sign
out and back in.

**Nothing syncs, and Settings says the engine is missing** — the package could
not find the `filex` binary it ships with. Reinstall, or point the app at a CLI
you have with `FILEX_CLI=/path/to/filex`.

**A folder shows "attention"** — the line under it is the engine's own last
message. `filex sync run --pair <id>` in a terminal shows the same thing with
more detail.

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
