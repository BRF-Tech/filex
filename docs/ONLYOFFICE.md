# OnlyOffice integration

filex can open Word/Excel/PowerPoint documents (and PDF, ODF, etc.) for
**in-browser editing and co-authoring** by embedding a self-hosted
[OnlyOffice Document Server](https://www.onlyoffice.com/). Edits are saved
straight back into the storage backend the file came from.

This integration is **optional**. If you don't configure it, filex works
normally — Office files just open in the built-in read-only preview instead of
an editor (see [What happens if it isn't configured](#what-happens-if-its-not-configured)).

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

The shared secret in filex (`FILEX_ONLYOFFICE_JWT`) **must equal** the Document
Server's JWT secret — that's the entire trust relationship.

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

Set two environment variables (or the equivalent `external_services.onlyoffice`
block in `config.yaml`):

```bash
FILEX_ONLYOFFICE_URL=https://office.example.com   # Document Server base URL
FILEX_ONLYOFFICE_JWT=a-long-random-shared-secret  # MUST match JWT_SECRET above
```

Both are required. filex treats OnlyOffice as **enabled only when both are set**.

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

Nothing breaks. With no `FILEX_ONLYOFFICE_URL` / `FILEX_ONLYOFFICE_JWT`:

- filex reports OnlyOffice as **disabled** in its capabilities.
- Office files open in the **read-only preview** (or download), not an editor.
- The editor endpoint returns `onlyoffice: not configured` if called directly.

You can add OnlyOffice later at any time — it's purely additive.

---

## Failure modes & troubleshooting

### Failure: "OnlyOffice not configured" / no Edit option
Only one (or neither) of the two variables is set. Set **both**
`FILEX_ONLYOFFICE_URL` and `FILEX_ONLYOFFICE_JWT` and restart filex.

### Failure: editor shows "Download failed" / "token" error
The two JWT secrets don't match. `FILEX_ONLYOFFICE_JWT` **must** equal the
Document Server's `JWT_SECRET`. A mismatch makes the Document Server reject the
config (or filex reject the callback) with a token error. Fix the secret on
either side and restart both.

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
- The shared secret is the whole trust boundary. Treat `FILEX_ONLYOFFICE_JWT`
  as a secret (env file with `chmod 600`, not committed).

---

## See also

- [CONFIGURATION.md](CONFIGURATION.md) — full config/env reference
- [INSTALLATION.md](INSTALLATION.md) — running filex
- [DOCKER.md](DOCKER.md) — container deployment
