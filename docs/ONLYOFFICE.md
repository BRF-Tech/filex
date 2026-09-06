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
- The Document Server and filex must be able to reach **each other over HTTP(S)**:
  - the browser must reach the Document Server (iframe assets),
  - the Document Server must reach filex's **public URL** (fetch + callback).
- `FILEX_PUBLIC_URL` must be the URL the Document Server can actually resolve —
  not `localhost` (see [Failure: document won't load / won't save](#failure-document-wont-load-or-save)).

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

### 3. Make sure both sides are reachable

- Serve both filex and the Document Server over **HTTPS** in production. Browsers
  block an HTTPS page from loading an HTTP iframe (mixed content), so an HTTP
  Document Server behind an HTTPS filex will silently fail to load.
- `FILEX_PUBLIC_URL` must be resolvable **from the Document Server container/host**
  (it fetches source + posts callbacks there). In Docker, that usually means a
  real hostname or the compose service name — never `http://localhost`.

That's it — reopen an Office file in filex and it should launch the editor.

---

## Configuration reference

| Env var | `config.yaml` | Required | Description |
|---|---|---|---|
| `FILEX_ONLYOFFICE_URL` | `external_services.onlyoffice.url` | yes | Document Server base URL (e.g. `https://office.example.com`) |
| `FILEX_ONLYOFFICE_JWT` | `external_services.onlyoffice.jwt_secret` | yes | Shared HS256 secret — identical to the Document Server's `JWT_SECRET` |

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
Almost always a **reachability / URL** problem:

- **Won't load** (blank iframe / "editor cannot connect"): the browser can't
  reach `FILEX_ONLYOFFICE_URL`, or it's HTTP behind an HTTPS filex (mixed
  content). Serve the Document Server over HTTPS on a real hostname.
- **Won't fetch source** ("Download failed"): the Document Server can't reach
  filex's `FILEX_PUBLIC_URL`. Make sure that URL resolves from the Document
  Server's network, and that a reverse proxy forwards
  `/api/files/onlyoffice/fetch` to filex.
- **Edits aren't saved**: the Document Server can't POST the callback to
  `/api/files/onlyoffice/callback`. Same fix — the callback goes to
  `FILEX_PUBLIC_URL`, so it must be reachable from the Document Server. Check the
  filex logs for callback errors (`onlyoffice: ...`).

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
