# OnlyOffice integration

filex can open Word/Excel/PowerPoint documents (and PDF, ODF, etc.) for
**in-browser editing and co-authoring** by embedding a self-hosted
[OnlyOffice Document Server](https://www.onlyoffice.com/). Edits are saved
straight back into the storage backend the file came from.

This integration is **optional**. If you don't configure it, filex works
normally - Office files just open in the built-in read-only preview instead of
an editor (see [What happens if it isn't configured](#what-happens-if-its-not-configured)).

Since 0.50 the same Document Server also draws the **thumbnails** of office
documents - a picture of the first page - and nothing else does: filex ships
no office suite (see [Thumbnails](#thumbnails)).

Since 0.51 a **`.csv`** opens in ONLYOFFICE's spreadsheet editor too while it
is configured, and is edited there and saved back as the same kind of CSV;
without it a `.csv` opens in filex's read-only table, as before (see
[CSV files](#csv-files)).

It works in every surface that embeds the explorer - the web app, the
[desktop app](DESKTOP.md), and any host page using `<filex-explorer>` - because
they all open the same editor component against the same endpoints. The desktop
app also feeds it documents that are **not** on the server yet: double-click an
Office file on your own disk and it is opened here and written back to that path
([Opening documents from your computer](DESKTOP.md#opening-documents-from-your-computer)).
That is the case where configuring this is worth the most - a machine with no
Office installed gets an editor for the documents already sitting on it. There is
one thing to know about token-authenticated hosts: the editor config is fetched
with the host's credentials, and a host that supplies its token as a *function*
(the desktop app does, because the token changes when you switch accounts) was
dropped before the request, which answered `401` and left the editor blank.
Fixed after v0.13.4 - see [Releases](RELEASES.md).

---

## Three machines, three addresses

⚠ **Read this before you fill in the Document Server URL.** Almost every
"it tests fine and then does not work" report is this and only this:

| Address | Who has to reach it | How it is checked |
|---|---|---|
| the **Document Server URL** | your **browser** - it loads the editor's JavaScript straight from there | the admin page probes it **from your browser** |
| the same **Document Server URL** | the **filex process** - it polls `/healthcheck` | the **Test** button |
| the **callback URL** (or `FILEX_PUBLIC_URL` when it is empty) | the **Document Server** - it fetches the document and POSTs the save back | the **Test** button asks the Document Server to download a one-shot URL through filex's signed document fetch endpoint and reports whether it arrived and was served |
| the Document Server's **converted copy** (`<document-server>/cache/files/.../Editor.bin`) | your **browser** again - after the Document Server has fetched and converted the document, the editor loads the converted copy from this address | not by the Test. The Document Server builds this address from the `Host`, `X-Forwarded-Host` and `X-Forwarded-Proto` headers it receives, so the reverse proxy in front of it has to pass them (see [The browser leg after the download](#the-browser-leg-after-the-download)). When the editor says "Download failed", filex says whether it got this far |

These are three different machines, and an address that works for one can be
useless to another. The classic case: on Docker or podman you type the
container name, `http://onlyoffice`. filex reaches it, **Test goes green**, and
your browser cannot resolve that name at all - so the editor fails with the
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
request's signature is reported as **unmeasured**, not as a broken route - that
is a JWT problem, and sending you to the wrong address would be worse than
saying nothing.

Since 0.50.0 that URL is the **document fetch endpoint itself**
(`/api/files/onlyoffice/fetch`), signed the way a document's URL is and
checked by the same signature check, with filex's configuration in force. A
reverse proxy that forwards some other path but not this one, or a fetch filex
refuses, now fails the Test as it fails every document; the line then says
*filex refused the request* and why. Only the storage read is left out,
because there is no document behind a probe (the editor's own diagnosis covers
that - see [Failure: editor shows "Download failed"](#failure-editor-shows-download-failed)).

The Test also asks the Document Server a **second time, without a token**. One
that enforces JWT refuses it (error -8); one that takes it has JWT off, and the
card shows a warning: with JWT off the Document Server still checks the token
in filex's editor configuration, against its own secret, and refuses it, so no
document opens ("The document security token is not correctly formed"); it
refuses to download from private addresses, and posts its save callbacks
unsigned, which filex refuses. Measured with Docs 9.4 (`e2e/realenv`, the
S2 case of issue #80): the Document Server's log says `checkJwt error ...
invalid signature` for the editor and `... is not allowed. Because, It is
private IP address` for the download.

filex still warns about the shapes that certainly cannot work before you press
anything: a callback address of `localhost`, `127.0.0.1` or `0.0.0.0`, or a
hostname filex itself cannot resolve.

### Two commands that say which half is wrong

```bash
# the browser leg - run this from a workstation, not from the server
curl -I <document-server-url>/web-apps/apps/api/documents/api.js

# the callback leg - run this from INSIDE the document server container
podman exec -it onlyoffice curl -I "$FILEX_PUBLIC_URL/healthz"
```

Both must answer `200`. The first failing while filex's own Test passes is
exactly the container-name case above. The second failing means saves will be
lost even though the editor opens.

### When the Document Server needs a different address from your users

`FILEX_PUBLIC_URL` builds two different things: every share link a person
clicks, and the document URL the Document Server fetches. Those usually want
the same address, and sometimes they cannot be the same - a Document Server on
a container network may only reach filex as `http://filex:5212`, while your
users need `https://files.example.com`.

Set the **callback URL** for that. It is the address the Document Server uses,
and nothing else reads it:

```bash
FILEX_PUBLIC_URL=https://files.example.com      # people, share links, emails
FILEX_ONLYOFFICE_CALLBACK_URL=http://filex:5212 # the Document Server alone
```

The same field is in the admin page under the Document Server URL, and applies
live. Leave it empty - which is the default and what every single-address
install wants - and the public URL is used, exactly as before.

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

1. **Config** - filex builds a JSON editor descriptor, signs it with the shared
   secret, and hands it to the embedded iframe. It contains a **signed, short-lived
   fetch URL** the Document Server uses to pull the current bytes.
2. **Fetch** - `GET /api/files/onlyoffice/fetch?...&sig=...` streams the source
   to the Document Server. Public but unguessable (HMAC over node id + expiry)
   and time-limited - no filex session needed, because the Document Server is a
   server, not the user's browser.
3. **Callback** - on save the Document Server POSTs to
   `POST /api/files/onlyoffice/callback?node=<id>` with a JWT; filex verifies the
   JWT, downloads the saved revision, and writes it back through the storage
   driver.

The shared secret configured in filex - on *Settings → External services*, or
seeded from `FILEX_ONLYOFFICE_JWT` - **must equal** the Document Server's JWT
secret. That is the entire trust relationship.

### Thumbnails

Office thumbnails (0.50) are made by the same Document Server, through its
**conversion service** (`ConvertService.ashx`) rather than the editor; the
whole design is in [thumbnails.md → Office through OnlyOffice](thumbnails.md#office-through-onlyoffice).
What it means for the Document Server:

- **What is sent.** One request per document: where to download it, its type,
  `png`, a `thumbnail` box of 320 x 320 pixels (a spreadsheet also a
  `spreadsheetLayout`), and a key, all inside the signed token. The Document
  Server downloads the document from the same fetch endpoint the editor uses,
  with an address signed **for that purpose** (`p=thumb`, part of the HMAC,
  10 minutes; an app's office conversion uses `p=convert`), so a thumbnail's
  address is never an editor's, nor the other way round. filex downloads the
  picture **only from the Document Server's own address**; an answer naming
  any other address is refused and that address is never fetched.
- **What is never sent.** An end-to-end encrypted file: the thumbnail
  pipeline does not ask for one, and the fetch endpoint refuses ciphertext for
  a conversion (415).
- **The editors go first.** A Community Edition Document Server runs one
  converter, shared with the editors; an editor opening a document has the
  higher priority, a conversion waits. filex therefore sends it one document
  at a time per process (`thumbs.office_slots`, 1 to 4), at most
  `thumbs.office_max_mb` (25 MB), 60 seconds each, and asks again after a
  back-off (2, 8, 32 minutes, about 2 and about 8 hours) when it did not
  answer. Enabling OnlyOffice on an install that has many office documents
  therefore draws them gradually, as they are listed, and an editor is never
  queued behind a folder of thumbnails for long.
- **Its cache.** The Document Server keeps a conversion's result about a day
  under the request's key; filex's keys name the file, its content, the
  parameters and the try, so nothing stale or failed is answered from it.
- **The editor's diagnosis is not touched.** What the fetch endpoint answers a
  thumbnail's download is kept apart from the record behind the editor's
  "Download failed" explanation (`GET /api/files/onlyoffice/diagnose`).
- **Several replicas.** The address is checked by any replica (it carries its
  own signature); the one-at-a-time limit is per process.

Without a Document Server, office documents get no thumbnail: they show their
type icon, and *Admin → Tools → Thumbnail repair* lists them as "OnlyOffice is
not configured". Configure it (no restart) and they are drawn as they are
listed.

### What a save does

A save-back is a write like any other write in filex, and since **v0.34.0** it
goes through the same shared post-write gate every other surface does. First
the callback checks that the file is still the version the editing session
opened; a file that changed since is not written over, the save goes beside it
([When the document changes while it is open](#when-the-document-changes-while-it-is-open)).
Then, in order, it:

1. takes a **version snapshot**, so the revision it is about to replace stays
   recoverable from the file's history - and **refuses the save** if that
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
route - the document server posts it, not a signed-in browser - so there is no
request user to attribute it to. It is the same treatment every other actorless
write gets (the sync walk, the async ops worker). The **webhook** delivery is
unaffected either way.

#### When the scan runs, and why the status code decides it

The document server tells filex which kind of save this is, and the two kinds
get different answers:

| Callback status | What it means | Scan |
|---|---|---|
| **2** - ready for saving | Every editor **closed** the document and the server assembled the final revision. It arrives once per editing session, roughly **10 s after the last editor disconnects**. | **Immediately**, like an upload. The bytes are final and nobody is still typing. |
| **6** - force save | An **interim** save with the document still open. filex never asks for one and the document server does not send them by default (`autoAssembly` is off); an operator can switch them on, and then they repeat for as long as somebody keeps the document open. | **Debounced** - one scan per file per save window, the same treatment a Ctrl+S burst gets in the built-in text editor. |

Statuses 1, 3, 4 and 7 write nothing, so they announce nothing.

⚠ On a default install this means exactly **one scan per editing session**,
and it is not deferred: scanning a finished document on a timer would buy no
coalescing (there is only one save to coalesce) and would leave it unscanned
for up to a full window. The window is only worth paying for where saves
actually repeat. With force-save switched on, a long session costs one scan
per window while it is open **plus** the immediate scan of the final revision
when it closes - deliberately, because a document server that dies mid-session
never sends status 2 at all, and then the debounced scan of the interim bytes
is the only one there will ever be.

The window itself is the setting on **Admin → Protection**
(`antivirus.save_scan_window_minutes`, default 30 min) - it is one window, shared
with the text editor, not a second knob.

---

## The editor in a frame of its own

The Document Server's editor API is a script, `web-apps/apps/api/documents/api.js`,
that an integrator loads into its own page - and a script in filex's page can
do whatever that page can: read the key the web client keeps for the session
(`sessionStorage`), call filex's API as the signed-in person, read the page.
That is why the [security notes](#security-notes) say to trust the Document
Server as much as filex itself.

**Set `FILEX_ONLYOFFICE_FRAME_ORIGIN` to the Document Server's own origin and
that script runs there instead.** filex then serves a small page,
`<document server origin>/filex-frame/editor`, through the Document Server's
reverse proxy, and the viewer frames it instead of loading `api.js` into its
own page. The script runs on the origin it came from: no origin gets anything
it did not have.

```
 filex's page (files.example.com)                 the frame (docs.example.com)
   POST /api/files/onlyoffice/config  ──► filex
   ◄── { documentServerUrl, config (signed), frame }
   <iframe src="frame#<session>" sandbox=…> ─────► GET /filex-frame/editor
                                                  (the proxy sends it to filex)
                                         ◄──────── hello {session}
   open {session, config} + a port ─────────────►  loads api.js from the Document Server,
                                                   new DocsAPI.DocEditor(config)
   ◄── over the port: started, ready, state {dirty}, error {channel, code}, failed
```

### Setting it up

1. On the Document Server's host, send one path to filex and leave the rest
   alone. With Caddy:

   ```
   docs.example.com {
   	handle /filex-frame/* {
   		reverse_proxy filex:5212
   	}
   	handle {
   		reverse_proxy onlyoffice:80
   	}
   }
   ```

   The proxy must pass the `Host` header on (Caddy does). With nginx, a
   `location /filex-frame/ { proxy_pass http://filex:5212; proxy_set_header Host $host; }`
   beside the Document Server's own `location /`.
2. Set `FILEX_ONLYOFFICE_FRAME_ORIGIN=https://docs.example.com` on filex
   (YAML `external_services.onlyoffice.frame_origin`) and restart: filex reads
   it at start, because it decides what that host may be answered.
3. Open a document. The ONLYOFFICE card under **External services** loses its
   `editor_same_origin` note, and the start-up log says
   `onlyoffice: the editor runs in a frame on its own origin`.

On that host filex answers `/filex-frame/editor` and **nothing else** - not
its pages, not its API, not a share - at the host's root, whatever filex's own
`FILEX_BASE_PATH`; on every other host `/filex-frame/` is a 404, so the page
never runs on filex's own origin. A value that is not an origin, filex's own
origin, or the app-interface origin stops filex at start, saying what to
write.

Without it, `FILEX_APP_UI_ORIGIN` is used when it is set (the page is then
`<interface origin><base>/_appui/_onlyoffice/editor`); the frame origin comes
first when both are set.

### Why the Document Server's own origin is enough

The Document Server's host is usually the **same site** as filex
(`docs.example.com` beside `files.example.com`), and no new domain is needed:

- **Origins, not sites, keep a page's things apart.** `sessionStorage`,
  `localStorage`, the page itself and its `window` belong to one origin; a
  script on `docs.example.com` reaches none of `files.example.com`'s. The
  frame's sandbox also takes `document.domain` away from it and from
  everything inside it, so it cannot relax its way back (and filex's pages
  never set it).
- **Every cookie filex sets is HttpOnly.** On a multi-tenant install the
  session cookie's `Domain` is the parent domain (`.example.com`), so the
  browser sends it to the Document Server's host too - but no script there can
  read it. The cookies, all HttpOnly, `SameSite=Lax`, `Secure` behind TLS: the
  session (`filex_session`), a share's PIN unlock, the sign-in state and flow
  of an SSO sign-in. filex sets no cookie a script must read.
- **A request from there cannot use it.** `SameSite=Lax` does not keep the
  cookie off a same-site request, so filex's
  [cross-origin guard](CONFIGURATION.md#requests-from-other-origins) refuses a
  state-changing request the browser says came from another origin - the same
  site included (`Sec-Fetch-Site: same-site`) - unless that origin is trusted,
  and the frame origin never is: not even when a `FILEX_CORS_ALLOWED_ORIGINS`
  wildcard (`https://*.example.com`) covers it. The same holds for reading:
  filex sends that origin no CORS answer, so no script there can read what
  filex answers, with the cookie or without.

What the Document Server's own code can do is what it always could: it sees
the documents it edits, and its server receives the cookies the browser sends
it - it is infrastructure you run. What changes is that it no longer runs in
filex's page.

### How it works

- **What crosses.** The editor configuration, as filex's server signed it,
  goes to the frame once; the editor's events come back over a `MessagePort`
  the page handed over with it. Nothing else does. Saving was never the
  browser's business - the Document Server fetches the document from filex and
  posts the save back, server to server - so the frame needs no way to filex
  at all.
- **The checks, on filex's side.** The page takes the frame's `hello` only from
  the frame element it drew (`event.source`), only from the origin the frame's
  address names, and only with the one-time session it put in that address's
  fragment - once per frame, never after the frame loaded a second document.
  It posts the configuration to that origin by name, never to `*`. Messages
  that are not the protocol's (`filex-oo`, version 1) are dropped.
- **The checks, on the frame's side.** It answers no page but its parent,
  takes the configuration only with its own session and only once, and loads
  `api.js` only from the Document Server filex's server names in the page -
  never one the framing page names. Its own policy allows no other script:
  `script-src` is the frame's script by hash and the Document Server, and
  `frame-src`, `connect-src`, `img-src`, `font-src` and `form-action` name the
  Document Server only. It is served `no-store`, so a Document Server changed
  in the admin page is the one the next editor loads, and it reads no cookie.
- **The frame element.** Built with `sandbox="allow-scripts allow-same-origin
  allow-forms allow-popups allow-downloads allow-modals"` before its address and
  before it is in the page, `referrerpolicy="no-referrer"`, and
  `allow="clipboard-read; clipboard-write; fullscreen; autoplay"` (the editor's
  paste button, a slideshow, media in a presentation - no camera, microphone or
  screen). `allow-same-origin` keeps the frame on its own origin, which the
  Document Server's editor needs for its storage, and that origin is not
  filex's. No `allow-top-navigation`: the editor never takes the page away.
- **Who may frame it.** `frame-ancestors *`. The explorer is embedded in other
  sites by design (`<filex-explorer>`, tenants on their own domains), and the
  page holds nothing: what it opens is a configuration the framing page's own
  session obtained, which that page could as well hand to the Document Server
  directly.
- **The same on every surface.** The web app, the editor tab, the desktop
  app's document windows and every embed open office documents through the one
  viewer, so they all take the frame when the server names one. A page that
  embeds the explorer and sends a Content-Security-Policy must allow the
  frame's origin - the Document Server's - in its `frame-src`
  ([INTEGRATION.md](INTEGRATION.md)).
- **What does not change.** Opening, editing, saving, "Download as" and
  printing inside the editor, a [CSV](#csv-files) or an
  [older format](#a-save-in-another-format) and its note, the
  [Download failed diagnosis](#failure-editor-shows-download-failed), and the
  rule for [a document that changes while it is open](#when-the-document-changes-while-it-is-open):
  the reload is this frame taken away and a new one on a fresh configuration,
  and "edited" is the editor's `onDocumentStateChange`, arriving over the port.
  If the frame does not answer within 20 seconds, the viewer says the editor
  frame did not load from that origin (to an administrator) and the Document
  Server is not answering (to everybody else).

**With neither setting nothing changes:** `api.js` is loaded into filex's page,
as in every release before. There is no in-between on filex's own origin, on
purpose: the Document Server's editor needs a frame with `allow-same-origin`
(its own storage), and a frame with `allow-same-origin` on filex's own origin
*is* filex - it would isolate nothing. Nor is the Document Server's origin the
default: the Document Server does not serve this page, its proxy has to send
it to filex, and a default that relied on a proxy rule nobody wrote yet would
leave every editor blank after an upgrade. So that setup is said out loud
instead: the ONLYOFFICE card carries a note (`editor_same_origin`), filex logs
a warning at start (`onlyoffice: the editor's script (api.js) runs in filex's
own pages`), and the browser console says it once per page.

---

## Prerequisites

- A reachable **OnlyOffice Document Server** (Community Edition is fine).
- Three addresses that each work from the machine that needs them - read
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

Pick a long random `JWT_SECRET` and keep it - filex needs the **same** value.

⚠⚠ **Always set one.** The save callback route is public, and filex can only
tell a genuine save from a forged one by its signature. With a secret, an
unsigned callback is refused (since v0.43.0). **Without** one there would be
nothing to check a callback against, so filex does not run ONLYOFFICE at all:
a URL with no secret leaves opening and saving off, and the admin Panel and
*External services* show a red warning - it cannot be dismissed - until a
secret is set.

### 2. Point filex at it

Two ways, and either is complete on its own.

**In the admin UI** - *Settings → External services*. Fill in the URL and the JWT
secret, press **Test now**, save. Since 0.50.0 **Test now** tests the values in
the form as they are, before you save them, and saves nothing - the card says
so - so a wrong address never reaches the editors that are already open.
**Saving applies immediately; filex does not need a restart.** This is the right route when the Document Server lives in a
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
is reverted at the next boot - the card in the UI is labelled **"Set by the
environment"** when that is the case. That includes switching it off there:
OnlyOffice is then off for the editor, the office thumbnails and the apps'
office engine alike, until the next start switches it on again from
`FILEX_ONLYOFFICE_URL`; remove the variable to switch it off for good (the card
and the `PATCH` answer say so). Leave the variables unset if you want the
UI to own the setting.

### 3. Make sure all three addresses work

The full picture is [Three machines, three
addresses](#three-machines-three-addresses); the short version:

- Serve both filex and the Document Server over **HTTPS** in production. Browsers
  block an HTTPS page from loading an HTTP iframe (mixed content), so an HTTP
  Document Server behind an HTTPS filex will silently fail to load. The admin
  page names this case rather than reporting it as "unreachable".
- The Document Server URL must be one **a browser** can open - not only one
  filex can reach from inside the container network.
- `FILEX_PUBLIC_URL` must be resolvable **from the Document Server container/host**
  (it fetches source + posts callbacks there). In Docker, that usually means a
  real hostname or the compose service name - never `http://localhost`.

Then press **Test** on *Settings → External services* and read **both** lines it
prints. *From the filex server: reachable* and *From this browser: not
reachable* together mean the address is container-internal: the editor loads in
the browser, so it will fail there.

That's it - reopen an Office file in filex and it should launch the editor.

---

## Configuration reference

| Env var | `config.yaml` | Required | Description |
|---|---|---|---|
| `FILEX_ONLYOFFICE_URL` | `external_services.onlyoffice.url` | yes | Document Server base URL (e.g. `https://office.example.com`) |
| `FILEX_ONLYOFFICE_JWT` | `external_services.onlyoffice.jwt_secret` | yes | Shared HS256 secret - identical to the Document Server's `JWT_SECRET` |
| `FILEX_ONLYOFFICE_CALLBACK_URL` | `external_services.onlyoffice.callback_url` | no | The address the **Document Server** uses to reach filex. Empty (the default) means `FILEX_PUBLIC_URL`. Set it only when those two must differ - see [When the Document Server needs a different address](#when-the-document-server-needs-a-different-address-from-your-users) |
| `FILEX_ONLYOFFICE_FRAME_ORIGIN` | `external_services.onlyoffice.frame_origin` | no (recommended) | The Document Server's own origin (`https://docs.example.com`), whose proxy sends `/filex-frame/*` to filex: the editor's `api.js` runs in a frame there, not in filex's page. Read at start - see [The editor in a frame of its own](#the-editor-in-a-frame-of-its-own) |
| `FILEX_APP_UI_ORIGIN` | `app_ui_origin` | no | The origin app interfaces are served from; when `FILEX_ONLYOFFICE_FRAME_ORIGIN` is empty the editor's frame is served there |

Both are optional in the sense that the **admin UI** can supply them instead -
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

An unknown extension returns **415 Unsupported Media Type** - filex falls back to
preview/download for those.

---

## CSV files

Since 0.51 a **`.csv`** opens in ONLYOFFICE's spreadsheet editor while
OnlyOffice is configured. That is the product's default, not a setting each
install has to make: filex's own read-only table is the second choice, and
without OnlyOffice it is the only one, as before. Only `.csv` (a `.tsv` still
opens in the table).

- **Opening.** A double click opens it the way an office document opens: a
  look first, in the editor's view mode, with an **Edit** button that opens
  the editor tab. A person without write access gets the view mode only, as
  for every office file.
- **A choice like any other.** ONLYOFFICE is a handler of the open capability
  for `.csv` ([APP-PLUGINS.md → Default apps](APP-PLUGINS.md#default-apps-which-app-opens-a-file-and-which-draws-its-thumbnail),
  handler id `onlyoffice`): the file menu's *Open with* lists *Open with
  ONLYOFFICE* and *Open with the built-in viewer*, **Choose an app…** can make
  either the person's default (`PUT /api/me/open-with/csv`), and the
  administrator can reorder or switch it off under *Admin → Plugins → Default
  apps*, which lists `.csv` while OnlyOffice is configured
  (`PUT /api/admin/file-types/csv`). Switch OnlyOffice off and a `.csv` opens
  in the table again, without an error: a rule or a person's choice that names
  ONLYOFFICE is kept, and used again once it is back. An administrator sees
  *Open with ONLYOFFICE* greyed with where to set it up meanwhile; nobody else
  is offered it.

| | |
|---|---|
| ![A semicolon CSV open in ONLYOFFICE's spreadsheet, a look first](https://filex.sh/shots/csvoffice/csv-view-1440.83237ba55d3d.png) | ![The CSV in the editor tab, with the line on what a save keeps](https://filex.sh/shots/csvoffice/csv-edit-1440.4efc379a293d.png) |
| A double click: the spreadsheet, a look first, no "Choose CSV options" question. | **Edit**: the editor tab, and filex's line on what a save as CSV keeps. |
| ![The file menu: Open with ONLYOFFICE, the built-in viewer, Choose an app…](https://filex.sh/shots/csvoffice/csv-open-with-menu.2caeabb88a28.png) | ![Choose an app…, ONLYOFFICE and the table](https://filex.sh/shots/csvoffice/csv-choose-app.57b1ed93440f.png) |
| The file's menu offers both. | **Choose an app…** can make either the person's default. |
| ![Default apps: .csv, ONLYOFFICE first, the table second](https://filex.sh/shots/csvoffice/default-apps-csv-1440.f78775e4ce39.png) | ![ONLYOFFICE switched off: its row greyed, saying where to set it up](https://filex.sh/shots/csvoffice/csv-menu-no-onlyoffice.dc12b704a282.png) |
| *Admin → Plugins → Default apps* lists `.csv` while ONLYOFFICE is connected. | ONLYOFFICE switched off: the table opens the file, and an administrator sees the row greyed. |

### Opening without the "Choose CSV options" question

ONLYOFFICE asks for a CSV's encoding and delimiter before it shows anything,
unless the editor's config carries them (`document.options`). filex reads the
first 64 KiB of the file and passes what it finds, so the file opens straight
away (measured on Docs 9.4: ready in about 4 seconds, no dialog):

- **the delimiter**: comma, semicolon, tab or `|` - the one that splits the
  first lines (outside quotes) into the same number of fields; a comma when
  nothing does (a one-column file, an empty one);
- **the encoding**: UTF-8 (`codePage` 65001), with or without a byte order
  mark. A file that is not UTF-8 (UTF-16, a legacy code page such as
  Windows-1254) gets no options: ONLYOFFICE asks which encoding it is, because
  a wrong guess would change every non-ASCII letter and the save would keep
  the change.

### What a save writes

The Document Server saves an edited CSV when the last editor closes it, as for
every document ([What a save does](#what-a-save-does)). Measured on Docs 9.4
with its default (`assemblyFormatAsOrigin: true`), the callback's file is a
CSV (`filetype: "csv"`), but written ONLYOFFICE's way whatever the file was:
comma-separated, a UTF-8 byte order mark in front, `\n` line ends. filex puts
the file's own way back before it writes it - its delimiter, its byte order
mark or none, its `\r\n` or `\n` - and the text of every cell nobody changed
([Cells nobody changed keep their text](#cells-nobody-changed-keep-their-text);
0.51.0 left every value as ONLYOFFICE wrote it). A semicolon file stays a
semicolon file. A file that was not UTF-8 is saved as UTF-8, with a byte order
mark so that a reader knows.

⚠⚠ A `.csv` is only ever written as CSV text:

- A Document Server with `assemblyFormatAsOrigin: false` saves an edited CSV
  as **XLSX** (`filetype: "xlsx"`, measured). filex converts it back to CSV
  through the Document Server's own conversion service (the file is offered
  to it for that one conversion, as an app's office conversion is: UTF-8,
  then the file's own delimiter and its unchanged cells as above; what the
  conversion writes for a number or a date was not measured) and writes that.
  Before 0.51 the XLSX bytes were written under the `.csv` name.
- Anything else - another type, bytes that are a zip or an old Excel workbook
  whatever the callback says, a conversion that fails, a save over 256 MiB -
  is **not written**. The file stays as it was; the log says
  `onlyoffice callback refused: not written` with the reason; the Document
  Server is told the save failed; each person who edited it gets a bell
  notice in their language (*Your edit to list.csv was not saved*, and why:
  the `file.upload_failed` notice an upload that never reached the storage
  gets); and the audit log writes `file.office_save_refused`. The filex
  process that refused the save gives the next opening of the file a new
  editing session: measured on Docs 9.4, an editor opened on the session
  whose save was refused never finished loading.

A CSV is the one kind converted back and written in place. Any other kind
saved in another format is written beside the file and never over it: see
[A save in another format](#a-save-in-another-format).

### Cells nobody changed keep their text

ONLYOFFICE reads a CSV the way a spreadsheet does: a cell that looks like a
number or a date becomes one, and a save writes every cell back as the
spreadsheet shows it, edited or not. Measured on 0.51.0 with Docs 9.4.0, one
cell edited in a semicolon file, the editor in English - in cells nobody had
touched `05320000001` came back as `5320000001`, `007` as `7`, `000` as `0`
and `01.02.2026` as `1/2/2026` (`15.03.2026` stayed: ONLYOFFICE read the month
first, and 15 is no month), and every data row gained an empty cell at its
end. With the editor in Turkish the dates came back as `1.02.2026`. 0.51.0
wrote that.

filex now reads the file the save is about to replace, lines its rows up with
the rows ONLYOFFICE saved, and writes the file's own text back wherever the
saved text is the same value written ONLYOFFICE's way:

- **A row in which nothing changed** is written byte for byte as it was - its
  quotes, a delimiter at its end and its own line end included - wherever it
  stands now: a sort moves rows and changes none of them.
- **In a row somebody edited**, the cells they did not edit keep their text.
- **A row added** in ONLYOFFICE is written as ONLYOFFICE saved it, without the
  empty cells ONLYOFFICE pads a row with beyond the file's own width. **A row
  deleted** there is gone.
- **Empty lines at the end of the file** stay (ONLYOFFICE never writes them)
  unless a row was added under the last row, where they stood. An empty row
  in the middle stays as the file wrote it, a line of delimiters or a bare
  line end. A first line `sep=;` stays, and the file ends with a line end if
  and only if it did.

"The same value written ONLYOFFICE's way" is what Docs 9.4.0 was measured
writing for a cell nobody touched (the editor in English, Turkish, German and
French), and nothing else. It is one way only: `7` is what ONLYOFFICE writes
for `007`, so the file keeps `007`; `007` is never what it writes for `7`.

- **A whole number** without its leading zeros (`007` and `7`, `05320000001`
  and `5320000001`), its `+` (`+905320000001` and `905320000001`) or the
  spaces in front of it and one after it (` 42` and `42`); one written as
  hexadecimal (`0x10` and `16`); and one too long for a spreadsheet's number:
  `9007199254740993` comes back as `9007199254740992`, `123456789012345678`
  as `1.2345678901234568e+17`, and every one from 2^63 up, either way, as
  `-9.2233720368547758e+18` (`12345678901234567890`, a 24-digit account
  number).
- **A number with a decimal point**, which ONLYOFFICE keeps to six decimals:
  `03.50` and `3.50`, `0.1234567` and `0.1234570`, `+1.5` and `1.5`; with
  nothing before the point, `.5` as `.0` (the value gone) and `-.5` as
  `-0.5`. Never a decimal comma: `3,5` is text to ONLYOFFICE in every
  language measured and comes back as it was.
- **A date with a four-digit year from 1900 on**, written the way the editor's
  language writes one, with the same three numbers in the same order. In
  English month/day/year without leading zeros: `01.02.2026`, `01/02/2026`
  and `1-2-2026` as `1/2/2026`. In Turkish day.month.year, the month in two
  digits: `01.02.2026`, `1/2/2026` and `1-2-2026` as `1.02.2026`, `15/3/2026`
  as `15.03.2026`. An ISO date: `2026-02-01` as `2/1/2026` and as
  `1.02.2026`.
- **`true` and `false`**, saved as `TRUE` and `FALSE` (`True` comes back as it
  was).
- **A tab inside a cell**, which ONLYOFFICE drops (unless the file is
  tab-separated), and a text longer than 32767 characters, which it cuts
  there.

⚠ Nothing that only looks like one of these. A date ONLYOFFICE does not read
as one (`15.03.2026` in English, the 15th month) comes back as it was, and
what a person types over it is theirs: in the English editor a typed
`15/3/2026`, `01.02.2026` or `1.2.2026` is saved as typed, and the file gets
it. That is also why German's `01.02.2026` and French's `01/02/2026` are no
rule. A line end inside a quoted cell comes back as it was. Every rule is
pinned by the bytes a Docs 9.4.0 saved
(`backend/internal/onlyoffice/csv_keep_measured_test.go`).

⚠ What is still written as ONLYOFFICE writes it:

- **The cell you edit.** Type `007` into a cell and the file gets `7`.
- **A change that is only another way of writing the same value** cannot be
  made in ONLYOFFICE. It shows `007` as `7` already and saves the same text
  whether or not somebody retyped it, so filex cannot tell the two apart and
  the file keeps `007`. Edit the file as text for that.
- **What ONLYOFFICE changes in a way no rule puts back**, edited or not
  (measured, every language unless said): a time (`08:05:30` as `8:05`,
  `00:13` as `0:12`, a minute short), a percent (`12.34%` as `12.3%`), a
  number with an exponent (`1e3` as `1000.00000`), `5.` as `5`, a date with a
  time (`2026-02-01T10:30:45Z` as `2/1/2026 10:30`), a two-digit year
  (`01.02.26` as `1/2/2026`), a year before 1900 (`01.02.1850` as
  `1/2/3750`), a day that is not in its month (`31.02.2026` as `3.03.2026`
  in Turkish), a formula (`=1+1` as `2`). Keep such a file out of
  ONLYOFFICE, or edit it as text.
- **A date in an editor language other than English and Turkish**, unless
  that language happens to write it the English or the Turkish way (German
  writes `15/3/2026` as `15.03.2026`, which is the Turkish way, and it is
  kept; `1.2.2026` as `01.02.2026`, which is not).
- **A date typed as text in the way the other language writes one.** The
  English editor keeps a date typed with dots as text (measured: `01.02.2026`
  and `1.2.2026` typed are saved as typed). Typed as `1.02.2026` over a cell
  whose text has the same three numbers in the same order (`1.2.2026`), it is
  the Turkish way of writing that text, and filex keeps the file's; the same
  holds the other way round for a date the Turkish editor cannot read
  (`1/13/2026` typed over `01/13/2026`). Only the spelling is lost: the
  numbers and their order are the cell's.
- **A file that is not UTF-8** (ONLYOFFICE asked for its encoding when it
  opened): its text cannot be compared with what ONLYOFFICE saved, so its
  first save is written as in 0.51.0, in UTF-8 with a byte order mark. From
  then on it is a UTF-8 file and its cells are kept.
- **A file over 64 MiB or two million rows, or with a row of more than 16384
  cells** (as many columns as an ONLYOFFICE sheet has): written as in 0.51.0.
- **A column added, removed or moved**: the cells from that column on are
  written as ONLYOFFICE saved them, in every row. The cells before it keep
  their text only while they are more than half of the row's filled cells;
  otherwise the whole row is ONLYOFFICE's. A last column that was removed
  leaves an empty cell at the end of each row.
- **A row filex cannot tell from a new one**: a row in which half of the cells
  or more were changed at once, a row that was edited and also moved (edited,
  then the list sorted), and every one of several rows that differ only in
  how a value is written (`A;007` and `A;7`) when one of them was deleted or
  another added.
- **A row typed to read like another row of the file** (`5` typed where
  another row says `05`) is written as typed while the rows around it stand
  where they stood. When rows were added, deleted or moved there in the same
  save, or that other row was itself changed, filex cannot tell which of the
  rows that now read the same is the file's: the first of them in the save
  gets the file's text, `05`.

A save is never refused, and never held back, over any of this. When the cells
of a file cannot be kept at all it is written as in 0.51.0 - the file's
delimiter, byte order mark and line ends, ONLYOFFICE's values - and the log
says `onlyoffice callback: CSV cells not kept` with the reason (`not_utf8`,
`too_large`, `too_many_records`, `too_many_fields`, `unreadable`). Two reasons
are filex's own failure and are logged as a warning, worth a report:
`check_failed` when what filex was about to write did not read back as the
save, and `panic` when its comparison failed (the line then carries what
failed and where; never a cell's text). The file is compared as it is on the
storage when the save arrives, and the revision the save replaces stays in the
file's history ([What a save does](#what-a-save-does)).

### What a CSV cannot keep

A CSV is values separated by a delimiter, nothing else. Saved as CSV, only
the **values of the active sheet** are kept: formatting, formulas (their
values are kept), other sheets, images and charts are not. ONLYOFFICE says so
itself when a CSV opens in its editor ("The CSV format does not support saving
a multi-sheet file or any elements except text. Only the active sheet will be
saved.") - in English only, and, on a browser that opens it for the first
time, partly under ONLYOFFICE's own "New" tip. So filex says it as well, in
the person's language, in a line under the viewer's bar for as long as a CSV
is open for editing. To keep any of that, save a copy in another format from
ONLYOFFICE (*File → Download as*, *Save copy as*).

---

## A save in another format

The Document Server writes an edited document back in its own format when it
can (`assemblyFormatAsOrigin`, on by default) and in OOXML when it cannot, or
when that setting is off. It writes no Word 97, Excel 97 or PowerPoint 97
file: measured on Docs 9.4 with its defaults, an edited **`.xls` comes back as
XLSX** and a **`.doc` as DOCX** (the callback's `filetype`, and zip bytes).
Before 0.51 filex wrote those bytes under the old name: a `rapor.doc` that was
a DOCX inside, which some programs open and some refuse.

Since 0.51 the file is **not touched**. The edit is written **beside it**, in
the format it came back in, under the same name with that format's extension:
`rapor.doc` stays as it was and `rapor.docx` holds the edit. When that name is
taken, the next free one is used - `rapor (2).docx`, the numbering *New
document* uses - and nothing is replaced. Then:

- the new file is catalogued, indexed, scanned and announced like any new
  file (`file.uploaded`, `meta.origin: "onlyoffice"`, `meta.saved_beside`: the
  path of the file that was edited);
- each person who edited the document gets that notice in their bell, in
  their language: *Your edit was saved as rapor.docx* - *ONLYOFFICE saved
  rapor.doc as DOCX, which a .doc file cannot hold, so your edit is in
  rapor.docx, in the same folder. rapor.doc did not change.* (the webhook
  hears it once);
- the audit log writes `file.office_saved_beside`, with the new file, the one
  that was edited and the format;
- the next opening of `rapor.doc` is a new editing session (the file did not
  change, so its session key would not either).

**Only for somebody who may create that file there.** The new file is
created for one of the people who edited the document, and only for one who
may create it in that folder. filex records whom it handed an editing session
of the document (every editor config it signs names the person), takes the
editors from the callback's signed token (never from its body), keeps those
the session had, and checks for each, at the save and on the new file's own
name, that their account is on, their tenant reaches the storage, and their
permissions let them create it there (`files.create`: role, folder
exceptions, grants, a blocked file type, an app's lock). The first who may is
the one it is written for (the audit row names them). When nobody may, the
save is not written: the editors read *Your edit to rapor.doc was not
saved* with *You cannot create new files in this folder, and ONLYOFFICE saves
a .doc file only as a new DOCX file beside it. To keep an edit, download it
from ONLYOFFICE's File menu before you close the document, and save it
somewhere else.*, and the audit log writes `file.office_save_refused`. The record of
who opened a session is the filex process's memory: after a restart the
signed editors are taken as they are.

It is kept only for the formats a document is saved in - DOCX, XLSX, PPTX
(and their macro-enabled forms), ODT, ODS, ODP - and only when the bytes are
the package the callback names. Anything else is **not written**: the file
stays as it was, the log says `onlyoffice callback refused: not written` and
why, the Document Server is told the save failed, the people who edited it
get *Your edit to rapor.doc was not saved* with the reason (the bell's
*upload failed* notice, `file.upload_failed`, in their language), and the audit
log writes `file.office_save_refused`. A file in its own format (a `.docx`
saved as DOCX, an `.odt` as ODT) is written in place, as always; a `.csv` is
converted back and written in place ([CSV files](#csv-files)).

**A document opened from somebody's computer** (the desktop app's *Open with
filex*) is edited through a working copy, `.filex-open/<session>-rapor.doc`,
and its save in another format is written beside that copy:
`.filex-open/<session>-rapor.docx`, then `<session>-rapor (2).docx` for the
next save of the session. Nobody is notified about it from here - the server
announces nothing about filex's own folders, neither in the bell nor to a
webhook. The desktop app (0.53 and later) brings it home **beside the
person's own file** as `rapor.docx` and tells them, writes the session's later
saves to that same file, and removes the copies with the session
([DESKTOP.md → A save in another format](DESKTOP.md#a-save-in-another-format)).
An older desktop app does not: the edit stays in the working folder and is
removed with it.

---

## When the document changes while it is open

The Document Server edits the version it fetched when the editing session
opened, and saves the whole document back when the session ends (about ten
seconds after the last editor closes). If the file changed in between -
another person saved it over WebDAV, a sync client brought a newer copy, an
agent rewrote it on a mounted folder, a second editing session opened on the
newer version - that save used to be written over the newer version, and the
change it replaced was gone without a word. Since the release after 0.52 it is
not.

**The session's version.** When filex hands out an editing config it records,
per document key, the version the storage driver reports for the file at that
moment (size, modification time, etag). A second person handed the same key
joins the running session and sees *its* version, so the first record stands.
The session's own save moves the record on: a session that force-saves and
then saves again is not "out of date" after its first save.

**The save.** Right before the callback writes over the file it asks the
driver again. Same version: written, as always. Different: the save is
written **beside** the file as `<name>.filex-conflict-<time>.<ext>` (UTC time,
the name the desktop app gives its conflict copies too; the next free
`(2)` when it is taken), and the file keeps the other change. As with
[a save in another format](#a-save-in-another-format), it is written for one
of the session's editors who may create a file in that folder; the editors get
*rapor.docx changed while you were editing it* - *rapor.docx was changed
somewhere else after your editing session opened it, so your edit was not
written over that version: it is in rapor.filex-conflict-20261006T101500.docx,
in the same folder. rapor.docx keeps the other change.*, and the audit log
writes `file.office_saved_conflict`. When none of them may create a file there
the save is not written and they are told why (`file.office_save_refused`).

**The open editor.** An office document open in filex's viewer (the explorer,
the editor tab, the embeds) joins its folder on the realtime feed, like the
explorer does for the folder it shows. When a change names the document it
asks the server whether its session is still current -
`POST /api/files/onlyoffice/session` `{path, key, action: "state"}` answers
`{stale, known}` - because the editor's own save looks the same on the feed.
A stale session:

- with nothing unsaved in the editor, is reloaded: the editor is closed and
  opened again on a fresh configuration (a new key - the file changed), and
  a note says *The file was updated outside filex; the new version is loaded*;
- with edits in the editor, asks which version stays: **Keep the outside
  version** (the edits made here are dropped: `action: "theirs"`, the
  session's save is not written when it comes), **Write mine** (`action:
  "mine"`: the session now stands on the version there, and its save is
  written over it - over that version only; a later change makes it stale
  again), **Keep both** (nothing to send: the session's save goes beside the
  file, as above). Escape puts the question away without answering it; until
  an answer nothing is written over anything.

"Edits in the editor" means ONLYOFFICE's `onDocumentStateChange` said `true`
since this editor opened. It stays true when ONLYOFFICE later says `false`:
that only means the edits reached the Document Server, not the file.

⚠ ONLYOFFICE's `refreshFile()` is not used for this. It answers the Document
Server's own `onRequestRefreshFile` (Docs 8.3 and later: an editor opened with
a key that was already saved, or a reconnect) and only when there are no
unsaved changes; it is not a way for an integrator to say "load the new
version now". The reload is `destroyEditor()` and a new editor.

**Where the record lives.** In the database (table `office_sessions`,
migration 00092), so a restart does not forget a running session and every
filex instance behind the same database knows the sessions any of them handed
out - an answer given through one instance (*Write mine*, *Keep the outside
version*) holds for a save that arrives at another. In front of the table sits
filex's in-process cache (`internal/memcache`; no Redis, nothing outside the
process): it only makes the frequent reads cheap - every editing config asks
whether its session is recorded already. The decisions - may this save go
over the file, did the person drop it, is the editor's session still current -
are always read from the database, because another instance's answer is not
in this instance's cache; a write goes to the database first and to the cache
only when the database took it. The modification time is stored as an integer
(Unix nanoseconds), so it compares exactly on every engine.

A row is removed when its session ends (the last save, or *closed with no
change*); one whose session never said so is swept two days later (an hourly
look, on the server's minute maintenance tick). A session filex has no record
of - opened before this table existed, or its record expired - is judged by
the key the document would get now: an older key is out of date, and is
recorded as such.

The desktop app's "Open with filex" adds the same rule for a document on your
own disk, where the server cannot see the change
([DESKTOP.md → When the file changes while it is open](DESKTOP.md#when-the-file-changes-while-it-is-open)).

---

## Creating new documents

**+ New → New document** makes an empty Word, Excel, PowerPoint or OpenDocument
file and opens it in the editor that handles it.

⚠ The bytes do **not** come from the Document Server, and they do not come from
LibreOffice either. They are minimal valid documents embedded in the filex
binary (`backend/internal/newdoc` - XML parts zipped in memory), because `:slim`
and the bare binary deliberately carry no external tools and a feature that
works on one image and 500s on another is worse than one with a narrower
promise. So a `.docx` can be created on an install with no LibreOffice
anywhere near it.

What OnlyOffice decides is whether the type is **offered at all**.
`GET /api/files/capabilities` publishes `newdoc_types` - every type this build
can create, each as `{ext, group, mime, requires}` - and `requires` names the
service the *editor* needs: `"onlyoffice"`, `"drawio"`, or empty for the
built-in code and markdown editors. The client crosses that against the
`external` block, so with no Document Server configured the six office formats
(`docx`, `xlsx`, `pptx`, `odt`, `ods`, `odp`) are not in the picker and the
dialog says why - *"Office documents need a document server (OnlyOffice), which
is not configured here."* - rather than creating a file nobody on this install
could then open.

An installed app may add rows of its own (`new_documents` - draw.io's
**draw.io diagram**, say): they are listed under **Apps**, in the app's own
words, while the app runs, and the new file - empty, or a copy of the app's
template - opens in the app's interface, a draft like any other
([APP-PLUGINS.md](APP-PLUGINS.md#what-a-person-sees)).

⚠ The server states the dependency and the client resolves it, deliberately:
an embedder may point at a document server this process cannot reach, so the
client is the only place that knows the true answer. What it must not do is
re-derive the dependency from an extension list of its own - that list rots the
moment the registry grows a type.

### Naming the file

The name field holds the **whole file name**. Choosing a type fills it in with
that type's extension - `Untitled.txt`, `Untitled.docx` - and clicking into the
field selects the name part only, the way a rename does, so typing replaces
`Untitled` and keeps the extension. After that the name is yours:

- **Text and code types take any name.** `LICENSE`, `NOTICE`, `Makefile`,
  `Dockerfile`, `.gitignore`, `test.conf` and `example.custom` can all be made
  as Plain text; `README` can be made as Markdown. The type decides what the
  file is - its contents and the editor it opens in straight after - not what
  it is called, so `LICENSE` opens in the text editor. Opened again later, a
  file whose name says nothing (no extension, or one filex does not know) opens
  as plain text when the server says its bytes are text, and saves like any
  other text file.
- **Office documents and diagrams keep their extension.** Their editors find
  them by it - OnlyOffice picks Word, Excel or PowerPoint from the extension,
  and `report` with none is a ZIP nothing opens - so if you remove it the dialog
  says *"This type keeps its extension, so the file will be created as
  report.docx"* and the server adds it back.
- **A text type cannot borrow a document's extension.** `x.docx` made as Plain
  text would be an empty `.docx` that no editor opens, so the dialog refuses it
  and asks for the Word document type (or another extension).
- **Switching the type keeps what you typed.** Only the previous type's default
  extension is swapped (`notes.txt` → `notes.md`); a name with no extension, or
  one you chose (`test.conf`), stays as it is.

![The New document dialog with a Plain text document named LICENSE](https://filex.sh/shots/newdoc/newdoc-any-name-1280.c36430b7d719.png)

The create itself is `POST /api/files/manager?action=newfile` with
`{path, name, type, exact_name}`, where `type` is one of the `newdoc_types`
keys and `exact_name: true` says `name` is the whole file name. Without
`exact_name` the server appends the type's extension when the name lacks it -
the contract a client from before #56 relies on - and with it only a type whose
row says `ext_required: true` still gains its extension. A text type asked for
under another type's `ext_required` extension answers `400 EXT_NEEDS_TYPE`.
Where the server keeps drafts (below), the explorer sends the same body to
`POST /api/files/drafts` instead.

### Drafts: nothing is in the folder until you save

Choosing New document, naming it and closing the editor used to leave an empty
file behind (#71). Now **Create makes a draft**: a real file, with the name and
type you chose, kept in your own drafts area of that storage - and the folder
you were in gets nothing until you save it.

- **The editor opens on the draft**, with a bar above it that says where it
  will go ("will be saved to projects / docs") and a **Save** button. The
  built-in editors write into the draft as you type (every few seconds); the
  document server saves into it through its own autosave, and draw.io when
  you press its Save. While there are changes not yet written, the browser's own
  "leave this page?" question stands between you and a closed tab.
- **Save** puts the draft in its folder under its name. If a file has taken the
  name in the meantime, filex asks first - *"projects / docs already has a file
  called report.txt. Save the draft as report (2).txt instead?"* - and nothing
  moves before you answer. If the folder itself is gone, Save says so and the
  draft stays in Drafts.
- **Closing a draft you never saved** asks three things: **Save to disk**,
  **Keep in Drafts** (the default) or **Discard**. A discarded draft goes to
  the [Trash](TRASH-VERSIONING.md#discarded-drafts), where the usual retention
  deletes it; restoring it puts it back in Drafts.
- **Drafts**, in the navigation panel beside Recent, Starred and Trash, lists
  your drafts from every storage - name, where it will be saved, storage,
  modified - with **Open**, **Save to disk** and **Delete** on each row. The
  panel shows how many you have; a draft never raises a notification.
- **A draft is yours alone.** Nobody else sees it, an administrator included:
  it is not in any listing, search, share, WebDAV, S3, SFTP, FTPS or NFS view,
  desktop sync or storage usage figure, and no version history is kept of it.
  On disk it is `.filex-drafts/<your user id>/<key>/<name>` - one of the
  [names filex keeps for itself](BACKEND.md#names-filex-keeps-for-itself).
- **At most 50 drafts per person**, by default. An administrator changes the
  number under *Admin → Protection → Drafts*
  ([PROTECTION.md](PROTECTION.md#drafts)); at the limit, New document says so
  and offers to open Drafts - it never creates the file in the folder instead.

| What Create opens - a draft, under the bar that says where Save puts it | Closing a draft that was never saved |
|---|---|
| ![The text editor on a new draft, with the draft bar](https://filex.sh/shots/newdoc/newdoc-license-editor-1280.7c6b9a266ab5.png) | ![Save to disk, Keep in Drafts or Discard](https://filex.sh/shots/newdoc/drafts-close-1280.d49e1e1267da.png) |

| Save, when a file has taken the name meanwhile | Drafts, in the navigation panel |
|---|---|
| ![Save the draft under another name?](https://filex.sh/shots/newdoc/drafts-taken-1280.c0d6502f3476.png) | ![The Drafts view](https://filex.sh/shots/newdoc/drafts-view-1280.6092d950f3fb.png) |

Drafts belong to a person, so a caller that is not one creates the file
directly, as before: an app token, and an embed confined to one folder by its
host (one proxy token shared by all its users). `GET /api/files/capabilities`
carries `drafts: { limit }` exactly when the caller's New document makes
drafts. The endpoints are in [BACKEND.md → Drafts](BACKEND.md#drafts).

---

## Office conversions for apps (the office engine)

Since 0.50 the Document Server is also the apps' **office engine**: when an app
converts an office document - the Convert app's Word, Excel, PowerPoint and
OpenDocument targets, PDF and PDF/A from any of them - filex hands the file to
the Document Server's conversion API. filex ships and runs no LibreOffice, not
even one installed next to a bare binary, so this is the only way office
documents are converted.

- **Nothing more to set up.** The engine uses the URL, the JWT secret and the
  callback address configured above, and the Document Server downloads the file
  through the same door as an editor's document
  (`/api/files/onlyoffice/fetch`, with an address made for that one conversion
  and withdrawn when it ends). A setup where documents open in the editor
  converts too.
- **Live.** Connecting or removing the Document Server under External services
  turns the engine on or off at once; no restart.
- **Not connected:** an app's office targets say so ("Office documents are
  converted by ONLYOFFICE, and none is connected"), an administrator sees the
  greyed action and the install review say "connect it under External
  services", and a job that tries anyway fails with that sentence - never a
  silent error.
- **What it does not do that LibreOffice did** (measured on Document Server
  9.4): HTML from a spreadsheet or a presentation, and one CSV file per sheet
  (the first sheet is written). PDF/A comes out as PDF/A-2a. Text and CSV
  results are UTF-8 and start with a byte order mark; a CSV takes the
  separator asked for. `.odg` drawings and EPUB books are read.

For app authors: [PLUGIN-KIT → The office engine](PLUGIN-KIT.md#the-office-engine).

---

## What happens if it's not configured

Nothing breaks. With no URL and secret - from either source:

- filex reports OnlyOffice as **disabled** in its capabilities.
- Office files open in the **read-only preview** (or download), not an editor.
- A `.csv` opens in filex's **read-only table** (0.51; [CSV files](#csv-files)),
  and *Open with ONLYOFFICE* is greyed for an administrator, not offered to
  anybody else.
- **+ New → New document** offers only the types a built-in editor opens -
  Markdown, plain text, CSV, JSON, YAML, XML, HTML and the code formats - and
  names the missing service for the rest (see
  [Creating new documents](#creating-new-documents)).
- The editor endpoint returns `onlyoffice: not configured` if called directly.
- Apps cannot convert office documents (the office engine is unavailable; see
  [Office conversions for apps](#office-conversions-for-apps-the-office-engine)).
- Office documents have **no thumbnail** (0.50): their type icon, and the
  reason on the Thumbnail repair tab ([Thumbnails](#thumbnails)).

You can add OnlyOffice later at any time - it's purely additive.

---

## Failure modes & troubleshooting

### Failure: "OnlyOffice not configured" / no Edit option
Only one (or neither) of URL and secret is set. Set **both** - in *Settings →
External services*, which takes effect immediately, or as
`FILEX_ONLYOFFICE_URL` + `FILEX_ONLYOFFICE_JWT` followed by a restart.

⚠ A **URL with no secret** is the usual cause, and it is easy to miss: the Test
button probes `/healthcheck` and answers "reachable" for a Document Server that
is perfectly healthy, while the editor still refuses because filex has nothing to
sign the descriptor with. Reachable is not the same as configured.

### Failure: editor shows "Download failed"

"Download failed." is **one message for two different failures**:

1. the Document Server could not download the document **from filex**, or
2. your **browser** could not load the converted copy back **from the Document
   Server** (`<document-server>/cache/files/.../Editor.bin`).

**Since 0.50.0 the editor says which.** Under the Document Server's "Download
failed." it adds one of three sentences, from what filex's fetch endpoint saw
for that document:

| The editor says | What filex saw | Where to look |
|---|---|---|
| *The document server downloaded this file from filex, but your browser could not load the converted copy from the document server…* | the Document Server fetched the document after it was opened, and filex served it | [The browser leg after the download](#the-browser-leg-after-the-download) |
| *The document server never asked filex for this file. If the file was opened before, the document server may be using its own copy of it.* | no fetch for this document since filex last opened it | the third leg of the Test, and the Document Server's own log (below) |
| *filex refused the document server's request for this file: …* | the fetch arrived and filex refused it; the reason is named | the reason table below |

The editor asks `GET /api/files/onlyoffice/diagnose?path=<storage>://<path>`
(or `?id=<node id>`), which answers only a person who may open the document -
the same tenant, root and viewer checks as the editor configuration, and the
same `404` for a document they cannot see:

```json
{ "verdict": "served", "opened_at": "2026-10-01T10:00:00Z",
  "fetch": { "at": "2026-10-01T10:00:01Z", "status": 200 },
  "scope": "this_process" }
```

`verdict` is `served`, `not_requested` or `refused`; a refusal's `fetch` carries
`reason_code` and the English `reason`. There is no MCP tool for it on
purpose: it explains a failure of the in-browser editor, which an agent never
opens, and every refusal is in the server log with the same reason. ⚠ This is **one filex process's
memory**: it keeps the last answer for up to 512 documents (the least recently
touched one goes first) and is empty after a restart. With **several replicas**
behind a load balancer, each one knows only the requests it served, so the
Document Server's download may have reached another replica than the one the
editor asks - `not_requested` from one replica is then not the whole story;
read the logs of all of them. A request whose signature does not check out
(not the Document Server's) only annotates a document filex opened, so a
stranger cannot fill this memory or invent entries in it.

The Test only covers the first. To tell them apart from the logs, open a
document the Document Server has **not** seen before (or create a new one): it
keeps a converted copy of every document it has already processed, keyed by
the document's version, and does not ask filex for it again - so an old document
can hide the answer. Then, within a minute:

```bash
podman logs --since 3m filex 2>&1 | grep -E 'onlyoffice/(config|fetch)|could not download'
```

- `msg=http method=GET path=/api/files/onlyoffice/fetch status=200`: filex
  served the document. It is failure 2 - read
  [The browser leg after the download](#the-browser-leg-after-the-download).
- `msg="onlyoffice: the document server could not download this file" ... reason=...`:
  filex refused the request, and `reason` names why (table below).
- only the `config` line and no `fetch` line: the Document Server never asked
  filex. Read the third leg of the Test (below), and the Document Server's own
  download errors:

  ```bash
  podman exec onlyoffice sh -c "grep -E 'downloadFile|private IP' /var/log/onlyoffice/documentserver/converter/out.log | tail -n 20"
  ```

⚠ A **successful** download writes no `WARN` line. Only a refusal does; a
fetch filex served shows up only as the `msg=http` access line with
`status=200`. So "no WARN" does not mean "no request" - look for the access line.

A refusal is logged with its reason:

```
level=WARN msg="onlyoffice: the document server could not download this file"
      node=42 storage=3 reason="..." err="..."
```

| `reason` (log) | `reason_code` (diagnosis) | What it means |
|---|---|---|
| `the link is malformed` | `bad_link` | The address the Document Server sent was cut or rewritten on the way. |
| `signature refused` | `signature_expired`, `signature_bad` | The link expired, or the JWT secret changed under a running editor (or the address was altered). See the token section below. |
| `no catalogue row for this document` | `not_found` | The node was deleted between opening the editor and the download. |
| `the storage could not be opened` | `storage_unavailable` | Wrong endpoint, wrong credentials, or the backend is down - this is about the **storage**, not about OnlyOffice. |
| `the document body could not be located` | `body_unavailable` | The document is still being uploaded and its staged bytes are gone, or filex could not tell where its bytes are. |
| `the object is not on the storage` | `object_missing` | The catalogue has the row, the bucket does not have the object. Run a sync. |
| `reading the object failed` | `read_failed` | The storage answered, then failed mid-read. `err` carries the driver's own message. |

**No `fetch` line at all** means the request never arrived, and the problem is
the third address - the route from the Document Server back to filex. Press
**Test** in *Settings → External services* and read the third leg: it reports
*reached*, *did not reach*, or *could not be measured*, and the last of those
is **not** a pass. See [Three machines, three addresses](#three-machines-three-addresses).

⚠ **Check JWT before the private-address setting.** When the Test reports
error -4 for a container address such as `http://filex:5212`, the first thing
to check is that JWT is **enabled on the Document Server with the same secret
filex has**. By default a Document Server fetches an address that arrives
inside a signed (JWT) request without its private-address filter (the table
below says where each release sets that) - so with JWT on, a container
address works, and a -4 means the Document Server really cannot reach filex at
that address (try it from inside its container, below). This command prints
the three switches without printing the secret; all three must be `true`:

```bash
podman exec onlyoffice /var/www/onlyoffice/documentserver/npm/json \
  -f /etc/onlyoffice/documentserver/local.json services.CoAuthoring.token.enable
```

If they are not, set `JWT_ENABLED=true` and `JWT_SECRET=<the secret filex has>`
on the Document Server container and recreate it. With JWT off the Document
Server refuses filex's editor token (it checks it against its own secret, so
no document opens), and filex refuses its save callbacks because they arrive
unsigned.

The **private-address filter** matters when JWT is off on the Document Server
(no token is verified, so no request counts as signed), or when its exemption
for signed requests has been turned off:

| ONLYOFFICE Docs | Private addresses refused by default | A signed request skips the filter |
|---|---|---|
| 7.3 and earlier | no | - |
| 7.4 | yes | yes, always |
| 7.5 to 8.0 | yes | yes, unless `services.CoAuthoring.server.allowPrivateIPAddressForSignedRequests` is `false` |
| 8.1 and later | yes | yes, unless `externalRequest.directIfIn.jwtToken` is `false` or `externalRequest.directIfIn.allowList` is not empty |

Read from the ONLYOFFICE server source: 7.4.0 changed
`request-filtering-agent.allowPrivateIPAddress` from `true` to `false`
([e0aaeee6](https://github.com/ONLYOFFICE/server/commit/e0aaeee64331aff30270251bba1d830c65568346)),
7.5.0 added `allowPrivateIPAddressForSignedRequests`, default `true`
([1deefe3e](https://github.com/ONLYOFFICE/server/commit/1deefe3ee2b4483f0ad717172352508fcb39877e)),
and 8.1.0 replaced it with `externalRequest`
([446245c0](https://github.com/ONLYOFFICE/server/commit/446245c0aa3282e8b3c8999170cee03303f55d3c);
defaults in the [server configuration reference](https://api.onlyoffice.com/docs/docs-api/get-started/configuration/server-config/)).

When the filter applies, the Document Server refuses to download from a
private IP address (a docker or podman network, RFC 1918) and gives up before
sending anything: filex logs nothing, the editor says "Download failed", and
the Test reports error -4. Its own log shows:

```
... is not allowed. Because, It is private IP address
```

If that line is there and JWT has to stay off (or the exemption for signed
requests is off), allow private addresses on the Document Server and restart
it - either the environment variable on its container:

```bash
ALLOW_PRIVATE_IP_ADDRESS=true
```

or, in its `local.json`:

```json
{ "services": { "CoAuthoring": { "request-filtering-agent": { "allowPrivateIPAddress": true } } } }
```

#### The browser leg after the download

When filex served the document (`status=200`) and the editor still says
"Download failed", the Document Server did its part: it converted the document
and told the editor where to load the converted copy. That address is
`<document-server>/cache/files/<key>/Editor.bin`, and the Document Server
builds it from the request it received - the `Host`, `X-Forwarded-Host` and
`X-Forwarded-Proto` headers. A reverse proxy that does not pass them makes it
build the wrong address: `http://` behind an HTTPS page (the browser blocks it
as mixed content), an internal host name, or a link its own web server then
refuses (403).

To see it: press F12, open **Network**, tick **Preserve log**, open the
document and type `Editor.bin` in the filter. Look at the request URL and its
status, and at the **Console** for a "Mixed Content" error. If the URL starts
with `http://`, has a host other than the Document Server's public one, or
answers 403, fix the proxy in front of the Document Server. With nginx:

```nginx
proxy_set_header Host $host;
proxy_set_header X-Forwarded-Host $host;
proxy_set_header X-Forwarded-Proto $scheme;
```

Caddy and Traefik send these by default. ONLYOFFICE's own guide:
<https://helpcenter.onlyoffice.com/installation/docs-community-proxy.aspx>.

### Failure: "token" error on open
The two JWT secrets don't match. The secret filex holds - the `external_services`
row, whatever put it there - **must** equal the Document Server's `JWT_SECRET`.
A mismatch makes the Document Server reject the config (or filex reject the
callback) with a token error. ⚠ Correct it on whichever side is wrong: an edit
in *Settings → External services* takes effect on the next request with no
filex restart, while `FILEX_ONLYOFFICE_JWT` is re-asserted onto the row at boot
and therefore needs one. The Document Server needs a restart either way.

A save callback that carries **no** token is refused as long as filex holds a
secret: the callback route is public, and an unsigned one would let anybody
who can reach it overwrite a file. If saves fail with `the callback is not
signed`, the Document Server is running with `JWT_ENABLED` off - turn it on
with the same secret.

### Failure: document won't load or save
Almost always a **reachability / URL** problem - and which of the three
addresses is wrong is the whole diagnosis. Open *Settings → External services*
and read the two probe lines plus any warning next to the URL field; the
[two commands](#two-commands-that-say-which-half-is-wrong) answer the same
question from a shell.

- **Won't load** (blank iframe / "editor cannot connect"): the browser can't
  reach `FILEX_ONLYOFFICE_URL`, or it's HTTP behind an HTTPS filex (mixed
  content). Serve the Document Server over HTTPS on a real hostname.
- **Won't fetch source** ("Download failed", and filex logged no
  `path=/api/files/onlyoffice/fetch` access line): the Document Server can't
  reach the callback address (`FILEX_ONLYOFFICE_CALLBACK_URL`, or
  `FILEX_PUBLIC_URL` when it is empty). Press **Test**: the third line now says
  whether the Document Server managed to download a probe file from filex, and
  prints the exact URL it was given. Make that URL resolve from the Document
  Server's network - or, when it cannot, set the callback URL to one that does -
  and check that a reverse proxy forwards `/api/files/onlyoffice/fetch` to
  filex. ⚠ A filex log with **no** `GET /api/files/onlyoffice/fetch` access line
  after the editor opened is this failure: the request never arrived. A
  document the Document Server converted before is not fetched again, so test
  with a new one.
- **Won't load the converted copy** ("Download failed", and filex logged
  `path=/api/files/onlyoffice/fetch status=200`): filex and the Document
  Server did their part, and the browser could not load
  `<document-server>/cache/files/.../Editor.bin`. That is the proxy in front of
  the Document Server - [The browser leg after the download](#the-browser-leg-after-the-download).
- **Edits aren't saved**: the Document Server can't POST the callback to
  `/api/files/onlyoffice/callback`. Same address and same fix - the save goes
  where the fetch came from. Check the filex logs for callback errors
  (`onlyoffice: ...`).
- **Test says the server leg is not reachable, but `curl` to the Document
  Server works**: read the line printed under it. `/healthcheck returned HTTP
  502` (or 503/504) means the Document Server's own nginx answered and the
  **docservice** process behind it did not - the network between the two
  machines is fine. The welcome page (`/welcome/`) is a static file nginx
  serves by itself, so it loads either way. Run `supervisorctl status` inside
  the Document Server container (`ds:docservice` and `ds:converter` must be
  `RUNNING`) and read `/var/log/onlyoffice/documentserver/docservice/err.log`.
  Its own log shows the same thing as `connect() failed (111: Connection
  refused) while connecting to upstream … :8000`. `no answer within 3s` is a
  different problem: filex gives up at three seconds, so a slow route or TLS
  handshake fails the check even when the service is up.

### Failure: 415 on open
Unsupported extension (see the type list above). Expected - use preview/download.

### Failure: "signature expired"
The signed fetch URL is older than its TTL (1h). Reopen the document to mint a
fresh URL. (This only appears if the Document Server retries a stale fetch much
later.)

---

## Security notes

- The **fetch URL is public but signed** (HMAC over node id + expiry) and
  **expires** - a leaked URL only exposes one node for a short window. A
  conversion's address (a thumbnail, an app's conversion) also signs its
  purpose and lives 10 minutes, and an end-to-end encrypted file is never
  served through one.
- The **callback is authenticated by JWT** - filex validates the Document
  Server's token before writing anything back, and only acts on the
  "ready to save" / "force save" statuses.
- The shared secret is the whole trust boundary. Treat it as one wherever it
  lives - an env file with `chmod 600` and not committed, or the stored row,
  which `GET /api/admin/external` redacts to `"***"` and never returns.
- ⚠ **Without `FILEX_ONLYOFFICE_FRAME_ORIGIN` (or `FILEX_APP_UI_ORIGIN`) you
  trust the Document Server as much as filex's own pages.** To open the
  editor, the explorer then loads the server's
  `web-apps/apps/api/documents/api.js` as a script *into the filex page itself* -
  that is how the Document Server's editor API works - so that script runs with
  everything the page can do, in the signed-in person's session. Point filex
  only at a Document Server you run or trust as fully as filex, reach it over
  HTTPS, and keep its host as closely guarded as filex's. With
  `FILEX_ONLYOFFICE_FRAME_ORIGIN` the script runs in a frame on the Document
  Server's own origin instead and reaches none of that
  ([The editor in a frame of its own](#the-editor-in-a-frame-of-its-own));
  the Document Server still sees the documents it edits, as it always must.
  (draw.io is different: it runs in its own frame, and filex only exchanges
  messages with that frame at its configured origin.)

---

## See also

- [CONFIGURATION.md](CONFIGURATION.md) - full config/env reference
- [INSTALLATION.md](INSTALLATION.md) - running filex
- [DOCKER.md](DOCKER.md) - container deployment
