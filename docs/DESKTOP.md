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
| Windows 10/11 (64-bit) | [`filex-desktop-x64.exe`](https://github.com/BRF-Tech/filex/releases/latest/download/filex-desktop-x64.exe) | Installer. Lets you pick the location, adds a Start-menu entry. |
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

**The window opens on an admin panel** — you are on a build older than v0.13.0.
Update.
