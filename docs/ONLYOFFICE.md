# OnlyOffice integration

filex can open Word/Excel/PowerPoint documents (and PDF, ODF, etc.) for
**in-browser editing and co-authoring** by embedding a self-hosted
[OnlyOffice Document Server](https://www.onlyoffice.com/). Edits are saved
straight back into the storage backend the file came from.

This integration is **optional**. If you don't configure it, filex works
normally — Office files just open in the built-in read-only preview instead of
an editor (see [What happens if it isn't configured](#what-happens-if-its-not-configured)).

It works in every surface that embeds the explorer — the web app, the
[desktop app](DESKTOP.md), and any host page using `<filex-explorer>` — because
they all open the same editor component against the same endpoints. The desktop
app also feeds it documents that are **not** on the server yet: double-click an
Office file on your own disk and it is opened here and written back to that path
([Opening documents from your computer](DESKTOP.md#opening-documents-from-your-computer)).
That is the case where configuring this is worth the most — a machine with no
Office installed gets an editor for the documents already sitting on it. There is
one thing to know about token-authenticated hosts: the editor config is fetched
with the host's credentials, and a host that supplies its token as a *function*
(the desktop app does, because the token changes when you switch accounts) was
dropped before the request, which answered `401` and left the editor blank.
Fixed after v0.13.4 — see [Releases](RELEASES.md).

---

## Three machines, three addresses

⚠ **Read this before you fill in the Document Server URL.** Almost every
"it tests fine and then does not work" report is this and only this:

| Address | Who has to reach it | How it is checked |
|---|---|---|
| the **Document Server URL** | your **browser** — it loads the editor's JavaScript straight from there | the admin page probes it **from your browser** |
| the same **Document Server URL** | the **filex process** — it polls `/healthcheck` | the **Test** button |
| the **callback URL** (or `FILEX_PUBLIC_URL` when it is empty) | the **Document Server** — it fetches the document and POSTs the save back | the **Test** button asks the Document Server to download a one-shot URL from filex and reports whether it arrived |

These are three different machines, and an address that works for one can be
useless to another. The classic case: on Docker or podman you type the
container name, `http://onlyoffice`. filex reaches it, **Test goes green**, and
your browser cannot resolve that name at all — so the editor fails with the
same message as a missing configuration.

**A green Test means "the filex process reached that URL". Nothing more.**
Since v0.37 the admin page says so, and probes the browser leg itself: it
reports *From the filex server: …* and *From this browser: …* as two separate
lines, and warns next to the field when the address is one a browser cannot
load (a bare container name, or `localhost` on an install published elsewhere).

Since v0.38 the third leg is measured too. filex hands the Document Server's
conversion endpoint a one-shot URL of its own and watches for the request to
arrive, so the admin page answers *the document server reached filex* or *it
did not* instead of declining to say. A Document Server that refuses the
request's signature is reported as **unmeasured**, not as a broken route — that
is a JWT problem, and sending you to the wrong address would be worse than
saying nothing.

filex still warns about the shapes that certainly cannot work before you press
anything: a callback address of `localhost`, `127.0.0.1` or `0.0.0.0`, or a
hostname filex itself cannot resolve.

### Two commands that say which half is wrong

```bash
# the browser leg — run this from a workstation, not from the server
curl -I <document-server-url>/web-apps/apps/api/documents/api.js

# the callback leg — run this from INSIDE the document server container
podman exec -it onlyoffice curl -I "$FILEX_PUBLIC_URL/healthz"
```

Both must answer `200`. The first failing while filex's own Test passes is
exactly the container-name case above. The second failing means saves will be
lost even though the editor opens.

### When the Document Server needs a different address from your users

`FILEX_PUBLIC_URL` builds two different things: every share link a person
clicks, and the document URL the Document Server fetches. Those usually want
the same address, and sometimes they cannot be the same — a Document Server on
a container network may only reach filex as `http://filex:5212`, while your
users need `https://files.example.com`.

Set the **callback URL** for that. It is the address the Document Server uses,
and nothing else reads it:

```bash
FILEX_PUBLIC_URL=https://files.example.com      # people, share links, e-mails
FILEX_ONLYOFFICE_CALLBACK_URL=http://filex:5212 # the Document Server alone
```

The same field is in the admin page under the Document Server URL, and applies
live. Leave it empty — which is the default and what every single-address
install wants — and the public URL is used, exactly as before.

## How it works

Three pieces cooperate, all signed with one shared secret (HS256 / HMAC-SHA256):

```
 Browser                 filex                         OnlyOffice Document Server
   │  open doc  ───────────►│                                       │
   │  ◄── JWT-signed editor config (documentServerUrl + doc url)     │
   │  load iframe ──────────────────────────────────────────────────►│
   │                        │◄── GET signed fetch URL (source bytes) │  (1) fetch
   │                        │      /api/files/onlyoffice/fetch        │
   │      …user edits…      │                                        │
   │                        │◄── POST callback (JWT) on save ────────│  (2) save
   │                        │      /api/files/onlyoffice/callback     │
   │                        │──► write revision back to storage      │
```

1. **Config** — filex builds a JSON editor descriptor, signs it with the shared
   secret, and hands it to the embedded iframe. It contains a **signed, short‑lived
   fetch URL** the Document Server uses to pull the current bytes.
2. **Fetch** — `GET /api/files/onlyoffice/fetch?...&sig=...` streams the source
   to the Document Server. Public but unguessable (HMAC over node id + expiry)
   and time‑limited — no filex session needed, because the Document Server is a
   server, not the user's browser.
3. **Callback** — on save the Document Server POSTs to
   `POST /api/files/onlyoffice/callback?node=<id>` with a JWT; filex verifies the
   JWT, downloads the saved revision, and writes it back through the storage
   driver.

The shared secret configured in filex — on *Settings → External services*, or
seeded from `FILEX_ONLYOFFICE_JWT` — **must equal** the Document Server's JWT
secret. That is the entire trust relationship.

### What a save does

A save-back is a write like any other write in filex, and since **v0.34.0** it
goes through the same shared post-write gate every other surface does. In
order, the callback:

1. takes a **version snapshot**, so the revision it is about to replace stays
   recoverable from the file's history — and **refuses the save** if that
   snapshot cannot be taken, because losing history is not a reason to also
   lose the file;
2. writes the revision through the storage driver;
3. refreshes the node row's size, mime, etag and mtime from the driver;
4. runs the post-write gate: **re-index** (so content search returns the new
   text, not the old), **thumbnail**, a **realtime change frame** so a browser
   with the folder open updates instead of finding out on its next navigation,
   the canonical **`file.updated`** webhook event stamped
   `meta.origin: "onlyoffice"`, and an **antivirus scan**
   ([PROTECTION.md](PROTECTION.md#antivirus-clamav)).

⚠ The event is `file.updated` and never `file.uploaded`: a save-back replaces
a document that was already there. A subscriber that watched `file.uploaded`
for edits needs to subscribe to `file.updated` as well.

⚠ The event carries **no actor**, and the in-app notification is therefore
admin-visible rather than scoped to one person's bell. The callback is a public
route — the document server posts it, not a signed-in browser — so there is no
request user to attribute it to. It is the same treatment every other actorless
write gets (the sync walk, the async ops worker). The **webhook** delivery is
unaffected either way.

#### When the scan runs, and why the status code decides it

The document server tells filex which kind of save this is, and the two kinds
get different answers:

| Callback status | What it means | Scan |
|---|---|---|
| **2** — ready for saving | Every editor **closed** the document and the server assembled the final revision. It arrives once per editing session, roughly **10 s after the last editor disconnects**. | **Immediately**, like an upload. The bytes are final and nobody is still typing. |
| **6** — force save | An **interim** save with the document still open. filex never asks for one and the document server does not send them by default (`autoAssembly` is off); an operator can switch them on, and then they repeat for as long as somebody keeps the document open. | **Debounced** — one scan per file per save window, the same treatment a Ctrl+S burst gets in the built-in text editor. |

Statuses 1, 3, 4 and 7 write nothing, so they announce nothing.

⚠ On a default install this means exactly **one scan per editing session**,
and it is not deferred: scanning a finished document on a timer would buy no
coalescing (there is only one save to coalesce) and would leave it unscanned
for up to a full window. The window is only worth paying for where saves
actually repeat. With force-save switched on, a long session costs one scan
per window while it is open **plus** the immediate scan of the final revision
when it closes — deliberately, because a document server that dies mid-session
never sends status 2 at all, and then the debounced scan of the interim bytes
is the only one there will ever be.

The window itself is the setting on **Admin → Protection**
(`antivirus.save_scan_window_minutes`, default 30 min) — it is one window, shared
with the text editor, not a second knob.

---

## Prerequisites

- A reachable **OnlyOffice Document Server** (Community Edition is fine).
- Three addresses that each work from the machine that needs them — read
  [Three machines, three addresses](#three-machines-three-addresses) first. It
  is the single most common cause of "it tested fine and then did not work".

---

## Setup

### 1. Run the Document Server with a JWT secret

```yaml
# docker-compose.yml (excerpt)
services:
  onlyoffice:
    image: onlyoffice/documentserver:latest
    environment:
      JWT_ENABLED: "true"
      JWT_SECRET: "a-long-random-shared-secret"   # keep this
      JWT_HEADER: "Authorization"
    ports:
      - "8080:80"
```

Pick a long random `JWT_SECRET` and keep it — filex needs the **same** value.

### 2. Point filex at it

Two ways, and either is complete on its own.

**In the admin UI** — *Settings → External services*. Fill in the URL and the JWT
secret, press **Test**, save. **The change applies immediately; filex does not
need a restart.** This is the right route when the Document Server lives in a
separate compose file or you would rather not hand filex a secret through the
environment.

**In the environment** (or the equivalent `external_services.onlyoffice` block
in `config.yaml`):

```bash
FILEX_ONLYOFFICE_URL=https://office.example.com   # Document Server base URL
FILEX_ONLYOFFICE_JWT=a-long-random-shared-secret  # MUST match JWT_SECRET above
```

Both are required. filex treats OnlyOffice as **enabled only when both are set**.

⚠ **The environment wins on every restart.** A service configured through env or
`config.yaml` is re-asserted onto its stored row each time filex starts, because
compose is declarative: editing `FILEX_ONLYOFFICE_URL` and restarting has to take
effect. So an edit made in the admin UI to an env-pinned service applies now and
is reverted at the next boot — the card in the UI is labelled **"Set by the
environment"** when that is the case. Leave the variables unset if you want the
UI to own the setting.

### 3. Make sure all three addresses work

The full picture is [Three machines, three
addresses](#three-machines-three-addresses); the short version:

- Serve both filex and the Document Server over **HTTPS** in production. Browsers
  block an HTTPS page from loading an HTTP iframe (mixed content), so an HTTP
  Document Server behind an HTTPS filex will silently fail to load. The admin
  page names this case rather than reporting it as "unreachable".
- The Document Server URL must be one **a browser** can open — not only one
  filex can reach from inside the container network.
- `FILEX_PUBLIC_URL` must be resolvable **from the Document Server container/host**
  (it fetches source + posts callbacks there). In Docker, that usually means a
  real hostname or the compose service name — never `http://localhost`.

Then press **Test** on *Settings → External services* and read **both** lines it
prints. *From the filex server: reachable* and *From this browser: not
reachable* together mean the address is container-internal: the editor loads in
the browser, so it will fail there.

That's it — reopen an Office file in filex and it should launch the editor.

---

## Configuration reference

| Env var | `config.yaml` | Required | Description |
|---|---|---|---|
| `FILEX_ONLYOFFICE_URL` | `external_services.onlyoffice.url` | yes | Document Server base URL (e.g. `https://office.example.com`) |
| `FILEX_ONLYOFFICE_JWT` | `external_services.onlyoffice.jwt_secret` | yes | Shared HS256 secret — identical to the Document Server's `JWT_SECRET` |
| `FILEX_ONLYOFFICE_CALLBACK_URL` | `external_services.onlyoffice.callback_url` | no | The address the **Document Server** uses to reach filex. Empty (the default) means `FILEX_PUBLIC_URL`. Set it only when those two must differ — see [When the Document Server needs a different address](#when-the-document-server-needs-a-different-address-from-your-users) |

Both are optional in the sense that the **admin UI** can supply them instead —
whichever way they arrive, the value the running process uses is the one in the
`external_services` table, read on every request. `GET /api/admin/external`
returns `env_managed: true` for a service the environment pins.

The signed fetch URL is valid for **1 hour** by default.

---

## Supported file types

The editor opens the standard OnlyOffice set, grouped into three document types:

- **Documents (word):** `doc, docx, docm, dot, dotx, dotm, odt, ott, rtf, txt, html, htm, epub, fodt, mht, xml, xps, pdf, wps, …`
- **Spreadsheets (cell):** `xls, xlsx, xlsm, xlt, xltx, xltm, xlsb, csv, ods, ots, fods, et, ett, …`
- **Presentations (slide):** `ppt, pptx, pptm, pot, potx, potm, pps, ppsx, ppsm, odp, otp, fodp, dps, dpt, …`

An unknown extension returns **415 Unsupported Media Type** — filex falls back to
preview/download for those.

---

## What happens if it's not configured

Nothing breaks. With no URL and secret — from either source:

- filex reports OnlyOffice as **disabled** in its capabilities.
- Office files open in the **read-only preview** (or download), not an editor.
- The editor endpoint returns `onlyoffice: not configured` if called directly.

You can add OnlyOffice later at any time — it's purely additive.

---

## Failure modes & troubleshooting

### Failure: "OnlyOffice not configured" / no Edit option
Only one (or neither) of URL and secret is set. Set **both** — in *Settings →
External services*, which takes effect immediately, or as
`FILEX_ONLYOFFICE_URL` + `FILEX_ONLYOFFICE_JWT` followed by a restart.

⚠ A **URL with no secret** is the usual cause, and it is easy to miss: the Test
button probes `/healthcheck` and answers "reachable" for a Document Server that
is perfectly healthy, while the editor still refuses because filex has nothing to
sign the descriptor with. Reachable is not the same as configured.

### Failure: editor shows "Download failed" / "token" error
The two JWT secrets don't match. The secret filex holds — the `external_services`
row, whatever put it there — **must** equal the Document Server's `JWT_SECRET`.
A mismatch makes the Document Server reject the config (or filex reject the
callback) with a token error. ⚠ Correct it on whichever side is wrong: an edit
in *Settings → External services* takes effect on the next request with no
filex restart, while `FILEX_ONLYOFFICE_JWT` is re-asserted onto the row at boot
and therefore needs one. The Document Server needs a restart either way.

### Failure: document won't load or save
Almost always a **reachability / URL** problem — and which of the three
addresses is wrong is the whole diagnosis. Open *Settings → External services*
and read the two probe lines plus any warning next to the URL field; the
[two commands](#two-commands-that-say-which-half-is-wrong) answer the same
question from a shell.

- **Won't load** (blank iframe / "editor cannot connect"): the browser can't
  reach `FILEX_ONLYOFFICE_URL`, or it's HTTP behind an HTTPS filex (mixed
  content). Serve the Document Server over HTTPS on a real hostname.
- **Won't fetch source** ("Download failed"): the Document Server can't reach
  the callback address (`FILEX_ONLYOFFICE_CALLBACK_URL`, or `FILEX_PUBLIC_URL`
  when it is empty). Press **Test**: the third line now says whether the
  Document Server managed to download a probe file from filex, and prints the
  exact URL it was given. Make that URL resolve from the Document Server's
  network — or, when it cannot, set the callback URL to one that does — and
  check that a reverse proxy forwards `/api/files/onlyoffice/fetch` to filex.
  ⚠ A filex log with **no** `GET /api/files/onlyoffice/fetch` line after the
  editor opened is this failure: the request never arrived.
- **Edits aren't saved**: the Document Server can't POST the callback to
  `/api/files/onlyoffice/callback`. Same address and same fix — the save goes
  where the fetch came from. Check the filex logs for callback errors
  (`onlyoffice: ...`).

### Failure: 415 on open
Unsupported extension (see the type list above). Expected — use preview/download.

### Failure: "signature expired"
The signed fetch URL is older than its TTL (1h). Reopen the document to mint a
fresh URL. (This only appears if the Document Server retries a stale fetch much
later.)

---

## Security notes

- The **fetch URL is public but signed** (HMAC over node id + expiry) and
  **expires** — a leaked URL only exposes one node for a short window.
- The **callback is authenticated by JWT** — filex validates the Document
  Server's token before writing anything back, and only acts on the
  "ready to save" / "force save" statuses.
- The shared secret is the whole trust boundary. Treat it as one wherever it
  lives — an env file with `chmod 600` and not committed, or the stored row,
  which `GET /api/admin/external` redacts to `"***"` and never returns.

---

## See also

- [CONFIGURATION.md](CONFIGURATION.md) — full config/env reference
- [INSTALLATION.md](INSTALLATION.md) — running filex
- [DOCKER.md](DOCKER.md) — container deployment
