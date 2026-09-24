# Realtime updates & presence

An explorer with a live connection does **not** poll. It re-lists a folder when
the server tells it that folder changed, and it draws a presence strip of who
else is looking at it. Both travel over one WebSocket.

This page is the contract: how the socket is authenticated, what comes down it,
and — the part that decides how an integration behaves under load — how a burst
of changes is coalesced.

## The socket

| Step | Endpoint | Notes |
|---|---|---|
| 1. mint a ticket | `POST /api/files/ws-ticket` | Answers `{"ticket":"…","ws_url":"wss://host/api/ws"}`. The ticket is single-use and short-lived (60 s). |
| 2. open the socket | `GET /api/ws?ticket=<t>` | Also accepts the session cookie for a same-origin browser. |

The ticket exists so an **embedded** explorer never holds a durable token: the
host application proxies step 1 (injecting its own credential server-side) and
hands the browser only the one-shot ticket, which the browser spends
immediately. A ticket inherits the caller's identity, RBAC and root
confinement, so a confined embed can only ever subscribe inside its own root.

### Client → server

```json
{"type":"subscribe","path":"main://reports/2026"}
{"type":"focus","file":"q3.pdf"}      // null clears it
{"type":"ping"}
```

`subscribe` moves the connection into one folder's room; there is one room per
`(storage, folder)`. Subscribing to a folder you may not read is refused with
an error frame and the socket stays open.

### Server → client

```json
{"type":"change","path":"main://reports/2026","action":"upload","name":"q3.pdf"}
{"type":"presence","path":"main://reports/2026","users":[{"id":4,"uid":"4","name":"Ayşe","file":"q3.pdf","avatar":"<data: URI or https URL>"}]}
```

`path` is echoed back exactly as **that** client spelled it on `subscribe`, so
an embed subscribing with a confine-relative path and a native panel using the
absolute one can both match frames against what they asked for.

`action` is one of `create`, `delete`, `rename`, `move` or `upload`; a rename
or move also carries `new_name`. An overwrite announces as `upload`. `modify`
(no `name`) is the folder-size refresh: about two seconds after a change, every
folder from the changed one up to the storage root is told to repaint its size
column — the folder's entries did not change. **It is advisory.** The only thing a client is required to do with a change
frame is re-fetch the listing — `action`/`name` are there for toasts and for
future incremental patching, and the sections below say exactly when they are
not populated.

## Coalescing: what a burst looks like on the wire

A folder can change far more often than a listing is worth re-fetching, and two
shapes make that concrete (both measured on 2026-09-06):

- **One NFS write is not one change.** NFSv3 has no "close", so an NFS client
  sends an independent `WRITE` RPC per `wsize` chunk and the server commits on
  each one. A `cp -p` of a 5 MB file over a real mount produced **6**
  announcements — one `CREATE` plus five 1 MiB `WRITE`s — for one file.
- **Extracting a 5 000-file zip produced 5 000 announcements**, one per member,
  over roughly three minutes.

So each room emits on the **leading edge** and merges the rest:

- The first change in a quiet room goes out **immediately**, with nothing added
  to it. A single ordinary upload's write-to-visible latency is untouched.
- Anything arriving on top of a recent change is merged into **one** trailing
  frame per window. The window starts at **200 ms** and doubles per flush up to
  **1.5 s**, so a short burst settles almost at once and a long job costs a
  bounded trickle instead of thousands of frames.
- The window resets the moment a change arrives into a room that has gone
  quiet.

Merged frames carry a `count`:

```json
{"type":"change","path":"main://","action":"upload","name":"big.iso","count":5}
```

| Field | Meaning |
|---|---|
| `count` absent | one change — every frame a client saw before coalescing existed, so an older reader needs no change |
| `count` > 1, `name` present | that many **identical** changes (the NFS case above: the same upload of the same name, over and over) |
| `count` > 1, `name` absent | that many **different** changes; naming one of five thousand would be worse than naming none |

⚠ **Nothing is dropped, only merged.** A burst always ends with a frame that
reflects its final state — a folder whose last frame was swallowed would show a
stale listing until the person navigated away and back, which is worse than the
noise coalescing exists to remove.

### What this means for an integration

Treat a change frame as *"this folder changed, re-fetch it"*, and debounce the
re-fetch **with a ceiling**. A plain trailing debounce starves: while frames
keep arriving closer together than the debounce window, each one cancels the
pending reload and the folder is never re-listed at all. The bundled explorer
(`@brftech/filex`, and therefore the web app, the desktop app and every embed)
debounces 200 ms with a 2 s ceiling for exactly that reason.

## Watching a whole tree (sync clients)

A room is one folder, and a connection sits in one room at a time — the right
shape for an explorer, which shows one folder. A client that **mirrors** a tree
(the desktop sync engine, `filex sync run --watch`) needs every change at any
depth, for several trees at once, without appearing in anybody's presence
strip. That is a watch:

```json
{"type":"watch","paths":["main://projects","main://inbox"]}
```

- **Recursive**: a watch on `main://projects` hears every change at or below
  that folder.
- **The set is replaced** by each `watch` message; `[]` clears it. Up to 1 000
  roots per connection.
- **Silent**: a watcher is not a viewer — it is never listed in a presence
  roster and never receives presence frames.
- **Authorised like a subscribe**: the ticket's confinement, then RBAC ≥ viewer
  on the root, then the tenant boundary. Grants in filex are additive (a grant
  on a folder covers everything below it), so whoever may read the root may
  read every folder under it and nothing is checked per event.

Every `watch` is **acknowledged**, even when every root was refused:

```json
{"type":"watching","roots":["main://projects"],"errors":[{"path":"main://inbox","error":"forbidden"}]}
```

⚠ The acknowledgement is the capability probe. A server older than the watch
ignores the message, and without an answer "nothing has changed" and "nothing
will ever be announced" look the same. The sync engine waits 10 s for it and,
without one, stays on its interval poll (`live: polling`).

Changes arrive as their own frame type, so a browser that only knows `change`
and `presence` never mistakes one for the other:

```json
{"type":"tree_change","root":"main://projects","dirs":["2026/q3"],"action":"upload","name":"plan.md"}
{"type":"tree_change","root":"main://projects","dirs":["","2026/q3","archive"],"action":"upload","count":17}
{"type":"tree_change","root":"main://projects","overflow":true,"count":5000}
```

| Field | Meaning |
|---|---|
| `root` | the watched path, exactly as the client spelled it |
| `dirs` | the changed folders **relative to `root`** (`""` is the root itself) — relative, because a confined client spells its root differently from the storage's absolute path |
| `action`/`name`/`new_name` | only when every merged change was the same one |
| `count` | how many changes the frame stands for, when more than one |
| `overflow` | more than 256 distinct folders changed — they are not named; treat the whole root as changed |

Each watch coalesces like a room (leading frame at once, then one merged frame
per 200 ms → 1.5 s window), with one difference: **a watch never drops a
frame**. A room drops a frame when the client's queue is full, which costs a
viewer one stale listing; for a mirror it would cost a missed edit until it
next asks the change log (or until its `--full-every` walk), so a watch keeps
the merge and retries until it is delivered or the client disconnects.

⚠ The size-column refresh (`modify`, above) is **not** delivered to watches: the
real change already was, and a mirror obeying it would re-list every ancestor
folder after every save — its own uploads included.

## Presence

`presence` frames carry everyone **else** in the room — seeing yourself is
noise — de-duplicated per identity, so two tabs from one person collapse to one
entry while two end users behind a single shared proxy token stay distinct
(that is what `uid` is for; key your list on it, not on `id`).

A rename or a delete fixes the focus server-side: someone reading `eski.pdf`
when it is renamed is shown against `yeni.pdf` without their client having to
notice. ⚠ That fix-up is applied for **every** change, coalesced or not.

Avatars ride inside every presence frame, which is why the stored picture is
capped small — see [Backend](BACKEND.md).

## When there is no socket

If the ticket or the upgrade is unavailable (an old backend, a proxy that
blocks upgrades, an environment without WebSocket), the client retries with a
capped backoff and then falls back to re-listing the folder every **12 s**,
surfacing a small "no live connection" badge. The page always keeps working.

⚠ A reverse proxy in front of filex must pass WebSocket upgrades through, and
must preserve the `Host` header — the cookie-authenticated (same-origin) upgrade
is origin-checked. See [Deployment](DEPLOYMENT.md).

## What does *not* announce itself

The periodic **storage sync** — the walk that reconciles the catalogue with
what is actually on the storage — repairs rows and the search index but emits
no change frames. A file that appears **only** because the sync found it (it was
written to the backing storage behind filex's back, not through filex) therefore
does not push an open explorer; that folder updates on navigation, or on the
next change made through filex. ⚠ Not on the 12 s poll — that timer only runs
while the socket is degraded (see above), so on a healthy connection there is
nothing polling to pick the file up. Writes that go through filex — the web
app, the API, WebDAV, SFTP, FTPS, S3, NFS — all announce.

**filex's own folders** never announce. A change inside `.filex-trash`,
`.versions`, `.thumbs` or the desktop's `.filex-open` — a trash move, a
version snapshot, a working copy being saved — and a change that names one
(the desktop creating `.filex-open` at the root) are dropped as the first
thing `Hub.EmitChange` does, before any delivery: the one door the HTTP
handlers, the protocol servers and the folder-size refresher all publish
through, so rooms and recursive watches are filtered alike. The list is
`backend/internal/syspath`.

**OnlyOffice save-back** announces, as of v0.34.0: the callback routes through
the same post-write gate as every other write, so a document saved out of the
Office editor pushes a change frame at the folder that holds it and fires
`file.updated`. See [ONLYOFFICE.md](ONLYOFFICE.md#what-a-save-does).
