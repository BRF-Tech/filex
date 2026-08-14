# filex desktop app

The filex explorer in its own window, with folder sync that keeps running in the
background. Windows and Linux today.

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
> ⚠ **No macOS build.** Use the web app there; folder sync is Windows/Linux only
> for now.

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

## Language

The window follows your operating system's language (Turkish or English today).
Before this, the app's own shell — the rail, Settings, the sign-in screen — was
English while the file listing inside it was translated, which reads as a bug
rather than a choice. The waiting state you see before the first listing arrives
is now a full screen of its own instead of a bare line of text.

---

## Syncing folders

**Settings ⚙ → Sync a folder…** picks a folder on the server by browsing it, then
asks which folder on this computer to keep it in step with.

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

---

## Where your data lives

| What | Where |
|---|---|
| Account tokens | Your OS keychain (Windows Credential Manager / libsecret). The app **refuses to store a token in plaintext** if the keychain is unavailable. |
| Which folders are paired | `~/.filex/sync/pairs.json` — shared with the CLI |
| Sync bookkeeping + local trash | `~/.filex/sync/` |
| Window state, account list | The app's own config directory |

Signing out removes the account and its token, and stops syncing its folders.
**Your files are left exactly where they are** on both sides — unpairing is not
deleting.

---

## Troubleshooting

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

**The window opens on an admin panel** — you are on a build older than v0.13.0.
Update.
