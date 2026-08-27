# Uploads

filex has two write paths, and the difference between them is who waits.

| Path | When | What happens |
|---|---|---|
| **Direct** — `POST /api/files/manager?action=upload` | small files, every existing integration | one multipart request; the client waits for the storage backend to finish writing |
| **Staged** — `/api/files/upload/*` | large files, slow or distant backends, anything that must survive a dropped connection | the bytes land in filex's own staging area first, are acknowledged, and are transferred to the backend afterwards by a background job |

The direct path is fine for a 20 KB text file and is not going anywhere. The
staged path exists because on a slow backend the progress bar shows the
*backend's* speed, and a 4 GB upload over a flaky link starts from zero.

There used to be a third path — `POST /api/files/upload/init` — which handed the
browser presigned S3 URLs. It is still served for older embedders, but it
requires a driver that implements multipart: `local`, `sftp`, `ftp` and `webdav`
answer `501 storage does not support multipart upload`, and on that path filex
never sees the bytes at all. **No filex client speaks it any more.** The staged
path replaces it everywhere and works on **every** driver, including `local`,
`sftp`, `ftp`, `webdav` and an OS-mounted NAS.

⚠ A [storage plugin](PLUGINS.md) declaring `multipart` also implements that
interface, so `init` does **not** 501 on it — but it hands back no part URLs
(its multipart exists for the staged commit, where filex pushes the parts), so
the browser is left with nothing to PUT to. That is another reason not to reach
for this path.

---

## Which client does what

| Client | Small files | Large files | Resumes across… |
|---|---|---|---|
| Web explorer / desktop explorer / embeds (`@brftech/filex-core`) | `?action=upload` | staged, chunked | a dropped connection, **and a page reload** (see *Resuming in a browser*) |
| CLI (`filex upload`, `filex upload -r`) | `?action=upload` | staged, chunked | a dropped connection **and a process restart** |
| `filex sync` / the desktop app's folder sync | same code as the CLI — `cliclient.uploadFile` | | |
| Public drop links (`/d/{token}`) | synchronous write | staged ingest | — (one request; staging removes the *wait*, not the retry) |
| ShareX (`/api/sharex/upload`) | synchronous write | staged ingest | — |
| AI / REST (`/api/ai/upload`, MCP `file_write`) | synchronous write | staged ingest | — |
| Upload tickets (`/u/{ticket}`, MCP `file_upload_ticket`) | synchronous write | staged ingest | — (one request, single-use ticket) |

"Large" means **above the chunk size** — `FILEX_UPLOAD_CHUNK_SIZE`, 8 MiB by
default — on every one of them, so the word means the same thing everywhere.

### Upload tickets: the agent's way past its own context

An AI agent is the one client that cannot simply POST a local file: everything it
sends goes inside a tool call, and on MCP that means through the model's context,
where a 130 MB file (~173 MB base64) does not fit at all. A **ticket** splits the
operation — the agent's authorized call pins the destination, and the URL it gets
back accepts exactly one upload **with no credentials**, so an agent holding no
filex token can still run `curl -T`. On the wire the redeem is just another
whole-body upload, so everything below (staging, the disk guard, quota) applies
to it unchanged. See [MCP.md](MCP.md) for the endpoint contract.

The whole-body surfaces cannot chunk: each is one request carrying the
whole file. They still get the second half of the contract through one shared
helper (`StagedUpload.IngestStream`): the bytes land in staging, the node is
created and listed at once, and the driver write happens afterwards in the ops
worker. On an instance with no staging directory configured they fall back to
the synchronous write, unchanged.

### Resuming in a browser

The server holds the bytes, so a dropped connection costs one chunk. What a
browser cannot survive on its own is *itself* — a reloaded tab, a crashed
renderer — because nothing in memory outlives it and the platform will not hand
a `File` back without a fresh user gesture.

So `packages/core` writes a bookmark to `localStorage` (`filex:uploads:v1`)
holding the upload id, the destination and the file's identity (name, size,
`lastModified`). Picking the same file again continues the same staged session:
`GET /api/files/upload/{id}` gives the offset, and the UI says *"Resuming X from
62 %"* rather than silently starting over. A file whose size or mtime changed
does **not** inherit the session — splicing a new tail onto an old head is the
one way a resumable upload corrupts data. Bookmarks expire after 24 h, matching
`FILEX_UPLOAD_STAGING_TTL`, so a note never outlives the bytes it describes.

The offset in the bookmark is a *hint*, used for display and for deciding a
session is worth asking about. The byte to continue from is always the server's.

### Resuming in the CLI (and therefore in `filex sync`)

Same shape, on disk: `~/.filex/uploads/<key>.json` (override with
`FILEX_UPLOAD_STATE`), written **before the first chunk** and updated after each
one. The key is a digest of (server URL, remote path, local path); the record
carries the local file's size and mtime and is discarded if either changed.

The CLI also declares a `sha256:` at `begin`, so the **server** verifies the
whole assembled object at commit. That is the check a resume actually needs:
parts written by two different processes, possibly days apart, are only provably
one file if something compares the whole of it against a value fixed before the
first chunk went out.

`filex sync run --watch` and the desktop app's sync panel run this same binary,
so pausing a laptop mid-transfer costs the current chunk and nothing else.

---

## The protocol

```
POST   /api/files/upload/begin        {path, name, size, mime?, hash?, chunk_size?}
                                      → 200 {id, chunk_size, offset, total_size, expires_at}
PUT    /api/files/upload/{id}         Content-Range: bytes A-B/total   + the chunk body
                                      → 200 {offset, received, total_size, state}
GET    /api/files/upload/{id}         → 200 {offset, received, state, parts, complete, error?}
POST   /api/files/upload/{id}/commit  → 202 {op_id, node_id, transfer_state:"staged"}
DELETE /api/files/upload/{id}         → 200 {ok:true}   (abort + delete staging)
```

All of it is under the authenticated `/api/files` group, so the session/API-token
rules, the tenant scope and root confinement apply exactly as everywhere else.

Responses carry both `chunk_size` and `chunkSize` (and `op_id`/`opId`,
`node_id`/`nodeId`). The repo's JSON is snake_case; the camelCase duplicates are
there so a client written against the protocol sketch in the handover works
without a translation layer.

### begin

| Field | Meaning |
|---|---|
| `path` | destination **directory**, `adapter://sub/dir` — sanitised by the same guard every other mutation uses |
| `name` | file name (basename only; `..`, `/`, `\` are refused) |
| `size` | exact byte length. It is verified before anything reaches the driver |
| `mime` | optional. Advisory only — the mime actually stored is sniffed from the bytes, as on the direct path |
| `hash` | optional `sha256:<hex>` or `md5:<hex>`, verified at commit |
| `chunk_size` | optional. The server's answer is binding — use the `chunk_size` it returns |

Refusals worth knowing:

* `403` — no write permission on the destination, or the storage is read-only.
* `409` — a folder already exists with that name.
* `413 QUOTA_EXCEEDED` — quota is **reserved at begin**, see below.
* `507 NO_DISK_SPACE` — the staging filesystem has less than `size × 1.2` free.
* `501` — the driver cannot write at all, or staging is not configured.

### PUT — one chunk

`Content-Range: bytes A-B/total` (B inclusive, RFC 9110). `A` must land on the
grid `begin` handed out — `A % chunk_size == 0` — and the body must be exactly
as long as the range claims. That makes the part number derivable
(`A / chunk_size + 1`), which is what lets chunks arrive **out of order** and a
retried chunk simply overwrite itself.

A body shorter than the announced range — the shape a dropped connection takes —
is refused with `400 SHORT_CHUNK` and **the offset does not move**. Accepting a
partial chunk is how a resumable upload silently corrupts a file.

### GET — the resume oracle

`offset` is the resume point and it is authoritative. A client that lost its
state (tab closed, process restarted, app reinstalled) asks here and continues
from that byte. See *the offset contract* below for what it is not.

### commit

Verifies the size (and the hash, when one was declared), then:

1. creates or updates the node **immediately** with `transfer_state = "staged"`,
   so the file is listed the moment the commit is accepted;
2. submits an `upload-commit` op and answers `202` with `op_id` and `node_id`.

Poll `GET /api/files/ops/{op_id}` — the same tray endpoint copy/move/delete use —
for the transfer. `ok` means the bytes are on the driver and the node is
`stored`; `failed` means the staging directory has been kept and `commit` can be
called again to retry, without re-uploading a byte.

### DELETE — abort

Deletes the staging directory and the session row. Refused with `409` while a
transfer is running; wait for the op or let it fail.

---

## Staging layout

```
<data_dir>/uploads/<id>/manifest.json
<data_dir>/uploads/<id>/000001.part
<data_dir>/uploads/<id>/000002.part
```

```jsonc
{
  "version": 1,
  "id": "…",
  "total_size": 4294967296,
  "chunk_size": 8388608,
  "hash": "sha256:…",          // optional, as declared at begin
  "parts": [                    // sorted by n
    { "n": 1, "size": 8388608, "md5": "…" },
    { "n": 2, "size": 8388608, "md5": "…" }
  ],
  "created_at": "…",
  "updated_at": "…"             // the mtime the sweeper ages by
}
```

**Numbered parts, not one append-only file.** A single `<id>.part` plus a byte
offset would serve a sequential resumable client and nothing else. An
S3-compatible `UploadPart` API — planned directly on top of this layer — receives
parts out of order, numbered, each needing its own ETag. The numbered store
serves both: the sequential protocol is the special case where parts arrive in
order, and the per-part md5 makes the S3 composite ETag
(`md5(concat(part md5s))-N`) computable from the manifest alone, without
re-reading the data.

Both the parts and the manifest are written to a temporary file, fsynced, and
renamed into place, so a reader never sees a half-written file and an interrupted
chunk leaves no debris.

### The offset contract

`offset` is the total size of the **contiguous run of parts from part 1**. It is
not "how many bytes are staged": a part written past a hole is kept and reported
in `received`, but it does not move `offset` until the hole is filled. That is
what a sequential client needs — resuming from anything else would upload a file
with a gap in it.

### Staging boundaries vs backend boundaries

**Staging part sizes belong to the client; backend part sizes belong to the
driver.** On commit, a driver that implements `storage.PartUploader` — S3, and
any [storage plugin](PLUGINS.md) that declares `multipart` — gets a real
multipart upload with the parts **re-chunked** to at least 5 MiB — S3 rejects
any smaller non-final part, and it does so at `CompleteMultipartUpload`, i.e.
after every byte has already been sent. A client that sent 1 MiB chunks must not
be able to break the backend. Every other driver gets one `Writer.Write` over
the assembled staging file.

> ⚠ filex pushes those parts itself here (`PUT …/multipart/part` on a plugin):
> the staged file is on filex's disk, so there is nothing to hand a browser. A
> plugin that returns no `part_urls` from `multipart/init` is doing the normal
> thing.

---

## Quota, the disk guard, and GC

**Quota is reserved at `begin`**, not settled at commit. The reservation is
derived from the open rows themselves — the sum of `total_size` over the user's
`staging` and `committing` uploads — so it is released the moment a row leaves
that set (commit, abort or sweep) and it can never drift from the rows it
describes. Without it a staged upload would be invisible to the ceiling until it
committed, and a user could stage far past their limit.

**The disk guard** refuses at `begin` when the staging filesystem has less than
`size × 1.2` free. The whole object passes through staging, so accepting an
upload that cannot fit only moves the failure to the worst possible moment — and
takes the rest of the instance's disk with it. A probe that cannot measure (an
unsupported platform, a permission problem) does **not** refuse: a guard that
blocks every upload because it cannot read a number is worse than no guard.

**GC.** A staging directory idle for longer than `FILEX_UPLOAD_STAGING_TTL`
(default 24 h) is swept, together with its row, and every removal is logged with
its id, path and staged size. `committing` rows are never swept — their bytes are
being read right now. `failed` rows are, once they too have been idle for a full
TTL, so a permanently failing upload does not keep its bytes forever. Directories
with no row at all (a crash between `mkdir` and the `INSERT`) age out by their
own mtime.

---

## Configuration

| Variable | Default | Meaning |
|---|---|---|
| `FILEX_UPLOAD_STAGING_DIR` | `<data_dir>/uploads` | where in-flight parts live |
| `FILEX_UPLOAD_CHUNK_SIZE` | `8388608` (8 MiB) | default part size when the client does not ask |
| `FILEX_UPLOAD_STAGING_TTL` | `24h` | idle time before the sweeper removes a staging directory |

Client-side (the CLI and anything embedding it):

| Variable | Default | Meaning |
|---|---|---|
| `FILEX_UPLOAD_STATE` | `~/.filex/uploads` | where the CLI keeps its resume bookmarks |

In `config.yaml`:

```yaml
upload:
  staging_dir: /var/lib/filex/uploads
  chunk_size: 8388608
  staging_ttl: 24h
  sweep_interval: 1h
```

⚠ Put the staging directory on a filesystem with room for the largest upload you
expect — the whole object passes through it.

---

## Example

```bash
# 1. begin
curl -sX POST https://filex.example/api/files/upload/begin \
  -H 'Content-Type: application/json' -b cookies.txt \
  -d '{"path":"main://reports","name":"2026.tar","size":104857600,"chunk_size":8388608}'
# → {"id":"…","chunk_size":8388608,"offset":0,…}

# 2. one chunk (repeat; any order, resumable)
curl -sX PUT https://filex.example/api/files/upload/$ID -b cookies.txt \
  -H "Content-Range: bytes 0-8388607/104857600" \
  --data-binary @chunk-0

# 3. where should I resume?
curl -s https://filex.example/api/files/upload/$ID -b cookies.txt
# → {"offset":8388608,"state":"staging","complete":false,…}

# 4. commit, then watch the transfer
curl -sX POST https://filex.example/api/files/upload/$ID/commit -b cookies.txt
# → {"op_id":42,"node_id":915,"transfer_state":"staged"}
curl -s https://filex.example/api/files/ops/42 -b cookies.txt
```

---

## What still runs on a staged commit

Everything the direct path runs, in the same order and through the same helpers:
the kind-conflict guard, mime sniffing (including the ZIP-based office-format
refinement), the node upsert, the search index, the thumbnail job, the
`writehook` gate — which is what fans out to the antivirus scan, the
`file.uploaded` webhook and notifications — and the realtime folder-change event.
The event's `meta.origin` is `manager`, same as a direct upload, with
`meta.staged = true` added so a consumer can tell which path it came from.

## `transfer_state`

`nodes.transfer_state` is `stored` for everything written before this feature and
for everything whose bytes are on the driver; it is `staged` between a commit
being accepted and its transfer finishing. `staged_uploads.node_id` is the
reverse index from a node back to the staging directory holding its bytes.

## Reading a file while it is still transferring

A `staged` node is readable. Every read surface resolves its byte source through
one helper (`internal/filebody`), which answers "staging" while
`transfer_state = "staged"` and "the driver" otherwise:

| Surface | Route |
|---|---|
| App download / preview, including `Range` | `GET /api/files/manager?action=download|preview` |
| Raw node read | `GET /api/files/read?id=…` |
| Public share link | `GET /s/{token}` |
| Folder-share browse | `GET /s/{token}/f/*` |
| WebDAV `GET` / `PROPFIND`, including `Range` | `GET /dav/{storage}/…` |
| Thumbnails | the `thumb` pipeline |
| OnlyOffice fetch, archive zip/unzip, AI/MCP read + zip/unzip, versioning snapshots, antivirus, content indexing | — |

Three rules decide what a reader sees:

* **Metadata is the committed metadata.** `Stat` answers with the size that was
  committed and an ETag computed from the staged parts
  (`md5(concat(part md5s))-N`), not with the backend's absence. On an overwrite
  the previous ETag is deliberately replaced at commit time — keeping it would
  let a client holding the old file revalidate, get a `304` and keep the version
  it was just told had been replaced.
* **A failed transfer keeps serving.** The staging directory is kept on failure
  so the transfer can be retried without re-sending a byte, and the node stays
  `staged` — so reads keep coming out of staging, exactly as they did while the
  transfer was running.
* **Staged with no staging is an error, never a body.** A `failed` session that
  the sweeper removes after a full idle TTL leaves a node claiming `staged` with
  no bytes behind it. Reads then answer `503` with `code: STAGING_GONE` and a
  message naming the file. There is no fallback to the driver: on an overwrite
  it holds the previous version at that exact path, and serving that would be a
  silent wrong answer rather than a visible failure.

Ranged reads work out of staging too — the assembled staging reader seeks across
part boundaries, so a video is scrubbable and a download resumable before its
bytes have reached the backend. Public share links keep their existing
whole-body behaviour (one request is one download against the link's cap).

Two things are deliberately NOT staging-aware, because their problem is the
listing rather than the read:

* the **cached folder-share ZIP** (`?zip=…`, `internal/sharezip`) — its file list
  and its cache key both come from `drv.List`, so a staged file is invisible to
  it and an overwritten one signs with pre-overwrite metadata. Making only its
  reads staging-aware would produce a *cached* archive keyed by stale metadata,
  which is worse: it would then be served to everyone until the signature moved.
  The uncached streaming fallback does resolve each member through the helper,
  so an in-flight overwrite is archived with the committed bytes.
* the **public folder-share browse listing**, for the same reason: a staged file
  that has never existed on the backend is not in `drv.List`, so it is not shown
  on the page — though it downloads correctly once addressed at
  `/s/{token}/f/<name>`. The app's own listing is DB-backed and shows it.
