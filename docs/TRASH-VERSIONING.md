# Trash & version history

filex protects against two everyday mistakes — deleting the wrong file and
overwriting good content. **Trash** turns a delete into a reversible soft‑delete
with a retention window. **Versioning** keeps historical snapshots of a file's
contents so an earlier revision can be restored.

Both features live entirely **inside the storage backend** you already mounted
(see [STORAGE.md](STORAGE.md)) — filex adds a hidden `.filex-trash/` and a
hidden `.versions/` prefix on the same disk/bucket. There is no separate trash
server or version store to provision.

- [Trash](#trash) — [how it works](#how-trash-works) · [retention & purge](#retention--purge) · [endpoints](#trash-endpoints) · [failure modes](#trash--failure-modes--troubleshooting)
- [Versioning](#versioning) — [how it works](#how-versioning-works) · [retention](#version-retention) · [what triggers a snapshot](#what-triggers-a-snapshot) · [endpoints](#versioning-endpoints) · [failure modes](#versioning--failure-modes--troubleshooting)
- [See also](#see-also)

---

## Trash

### How trash works

Deleting a file or folder from the explorer is a **soft delete**, not an erase:

1. filex **renames** the underlying object on its storage backend to
   `.filex-trash/<unix>-<rand>__<basename>` (a collision‑proof key under the
   hidden trash prefix). Nothing is removed from disk/bucket yet.
2. The DB row's `deleted_at` timestamp is set, and the **original path is
   preserved** in the row's `storage_key` column. The row's live `path` /
   `path_hash` are rewritten to the trash location, so a fresh upload at the
   original path still works.
3. The item drops out of normal listings (the `.filex-trash/` prefix is filtered
   out) but stays in the database, ready to restore.

**Restore** reverses step 1: the object is renamed back from `.filex-trash/…` to
its original path, and the parent directory is **re‑resolved** so the row
re‑attaches in the right place in the tree. If the original parent no longer
exists, filex falls back to a **root restore** rather than leaving the row
orphaned in trash.

> **Two edge behaviours worth knowing:**
> - If the storage driver can rename, the trash step is a rename. If it can only
>   **copy**, filex copies into the trash key and deletes the source afterwards,
>   so the bytes still survive. Only a driver that can do **neither** falls back
>   to a real hard delete — and then the item is deliberately **not** listed in
>   the trash, because a Restore there could never work. None of the shipped
>   drivers (local, S3, SFTP, FTP, WebDAV) fall into that case.
> - Deleting an item that is **already in trash** (its path is under
>   `.filex-trash/`) **hard‑deletes it permanently** — this is how "empty a
>   single item from trash" works.

### Every delete surface uses the same trash

Deletion is not a web‑UI‑only concept. The web explorer, **WebDAV**, the
**AI/REST** endpoints, the **MCP** tools, the **CLI/sync client** and the
asynchronous batch‑ops worker all go through one shared helper (`trash.Put`),
so an item deleted from any of them lands in the trash the same way and is
restored the same way. A protocol added later inherits the behaviour by calling
that helper instead of driving the storage driver itself.

The helper never destroys data: when a backend cannot preserve the bytes it
reports that instead of deleting them, and the caller decides what to do. That
is also what keeps the emitted events honest — `file.trashed` fires **only**
when the bytes are genuinely restorable, `file.deleted` when they are really
gone.

A **folder** goes to trash as one restorable unit: the folder row is retagged
into the trash and its cached descendants are dragged along with it, so a single
Restore brings the whole subtree back. Note that the descendants are still
individual rows, and the trash listing is flat — a deleted folder therefore
shows its children as separate entries even though restoring the folder is one
action.

> ⚠ **Sync clients delete in bulk.** A single `rclone sync --delete` run can
> remove hundreds of files, and every one of them now lands in the trash. That
> is the point — the run is recoverable — but it also means a bulk delete
> **does not free space** until the retention window passes, and each file adds
> a row to the flat trash listing. Watch storage headroom after a large sync,
> and use the admin "empty trash" action when you need the space back
> immediately.

### Quota and the trash

**Trashed items keep counting against the owner's quota.** Usage is decremented
when an item is *purged*, not when it is trashed (see `trash.purgeOne`), and
that is deliberate: bytes parked in `.filex-trash/` still occupy the backend.
Deleting does not free space — emptying the trash does. Every surface follows
the same rule; none of them adjust quota at delete time.

> ⚠ Until v0.20 this was **theory**: nothing incremented `usage_bytes` at
> all, so nothing was counted and nothing was ever released either. The
> accounting is real now — see [Quotas](QUOTAS.md) for the full set of
> rules (overwrite, move, restore, copy, purge) and where they live.

### Retention & purge

Trashed items are kept for a fixed window, then hard‑deleted automatically.

| Setting | Where | Default | Meaning |
|---|---|---|---|
| `trash.retention_days` | DB `settings` table | **30** | Days a soft‑deleted item survives before automatic purge. Missing, non‑numeric, or `≤ 0` values fall back to 30. |

A **daily background loop** scans for nodes whose `deleted_at` is older than the
retention window and, for each one:

1. deletes the backing storage object (**best‑effort** — if the driver delete
   fails, the run logs a warning and still continues);
2. decrements the owner's [quota](STORAGE.md) usage (files only);
3. hard‑deletes the DB row.

The first tick fires **one interval after startup**, not immediately, so a
restart‑looping server doesn't hammer the backend. The purge is batched (500
rows at a time) and reports a summary (`scanned` / `deleted` / `failed` /
`bytes`).

### Trash endpoints

**User (authenticated session/token):**

| Method & path | Body / query | Notes |
|---|---|---|
| `GET /api/files/manager/trash` | `?storage_id=…&limit=…&offset=…` | Lists soft‑deleted items. `limit` defaults to 50 (max 500). Each entry shows the **original** `name`/`path` (not the internal trash key), `deleted_at`, `size`, `storage_name`, and **`ttl_days`** (days remaining before purge, floored at 0). |
| `POST /api/files/manager/restore` | `{ "node_id": 123 }` | Moves the file back to its original path and re‑attaches the row. |

Both are **filtered by access**: a [confined](RBAC.md) (root‑locked) caller only
sees / can restore items whose original path is inside its root, and
[RBAC](RBAC.md) requires **≥viewer** to see an item in the list and **≥editor**
on its original path to restore it (restore writes the file back).

**Admin only:**

| Method & path | Body / query | Notes |
|---|---|---|
| `POST /api/admin/trash/empty` | `?older_than_days=N` **or** JSON `{ "older_than_days": N, "storage_id": … }` | Immediate purge of everything older than `N` days. **`0` or missing wipes everything currently in trash.** Returns `{ ok, purged, failed, scanned, bytes }`. |
| `DELETE /api/admin/trash/{id}` | — | Immediately hard‑delete one trashed node (storage object + quota + row). |

### Trash — failure modes & troubleshooting

**A restored file reappeared at the storage root, not its old folder.**
Its original parent directory was itself deleted in the meantime. filex prefers
a **root restore** over orphaning the row — move the file back manually once the
folder exists again.

**Restore reports success but the file isn't back on disk.**
The DB flag is cleared **best‑effort**: if the driver's move step fails, filex
still un‑trashes the row and logs a warning (`trash restore move failed`). Find
the object under `.filex-trash/` on the backend and move it to the original path
by hand.

**An item vanished from trash before its `ttl_days` reached 0.**
Either an admin ran **empty trash** / purged it, or it was **deleted while
already in trash** (which is a permanent hard delete — see the edge behaviours
above).

**`ttl_days` shows 0 but the item is still listed.**
Purge runs on a daily tick — an expired item lingers until the next run. Admins
can force it with `POST /api/admin/trash/empty`.

**Can't delete (or restore) on a particular mount.**
That storage is likely **read‑only** — writes (including trashing and restoring)
return **403 `storage is read-only`**. See [read‑only mounts](STORAGE.md#read-only-mounts).

**Leftover `.filex-trash/…` objects on the backend.**
Purge deletes the DB row even when the storage delete fails (permissions, outage).
The object is orphaned but harmless; delete it with your storage's own tooling.

---

## Versioning

### How versioning works

Before filex overwrites a file, it can **snapshot the current bytes** so you can
roll back. Snapshots are copied into the **same storage backend** under
`.versions/<node_id>/<version_n>`, and each is recorded as a `node_versions`
row (version number, size, etag). Where the driver supports server‑side copy the
snapshot is a fast backend copy; otherwise filex streams the bytes
(read → write).

Only **files** are versioned. Directories and symlinks are skipped. A snapshot
is also skipped when there is nothing to capture — a brand‑new file with no live
content yet, or a row whose object isn't on the backend.

⚠ That last case is a **silent** skip: if the catalogued path and the object on
the backend ever disagree, the guard finds nothing to snapshot and reports
success. Every shipped driver normalises the key it is handed, so the two agree
in practice — but a storage **plugin** that does not would lose history with no
error anywhere. Plugin authors: normalise, and see [PLUGINS.md](PLUGINS.md).

**Restore** copies a recorded version back over the live file and refreshes the
node's size/etag. Passing `snapshot_current: true` snapshots the current content
**first**, so the restore itself is reversible.

### Version retention

| Setting | Value | Meaning |
|---|---|---|
| Versions kept per file | **20** (compile‑time default) | After each new snapshot, versions beyond the newest 20 are trimmed automatically. |

Trimming removes both the `node_versions` row and the backing `.versions/…`
object (best‑effort per object). Unlike trash's retention, the version count is
a fixed default rather than a DB‑tunable setting.

### What triggers a snapshot

**Every destructive write.** Before any surface replaces an existing file's
bytes it calls the pre‑write guard, which snapshots what is about to be lost:

| Surface | Endpoint / entry point |
|---|---|
| Browser upload (single POST) | `POST /api/files/manager?action=upload` |
| Browser upload (staged / chunked) | `POST /api/files/upload/{id}/commit` |
| Public file‑drop link | `POST /d/{token}` |
| Legacy presigned multipart | `POST /api/files/upload/finalize` |
| Ticketed upload | `PUT`/`POST /u/{ticket}` |
| AI / REST write | `POST /api/ai/upload` |
| MCP `file_write`, `file_zip`, `file_unzip` | `/api/ai/mcp` |
| ShareX | `POST /api/sharex/upload` |
| Archive extract / add | `POST /api/files/archive/extract`, `/add` |
| Text / code editor save | `POST /api/files/save-text` |
| OnlyOffice save‑back | `POST /api/files/onlyoffice/callback` |
| WebDAV | `PUT` |
| S3 gateway | `PutObject`, `CompleteMultipartUpload`, `CopyObject` |

A snapshot is taken **only when** there is something to lose: the path already
holds a catalogued **file**. A brand‑new file, a directory, and filex's own
internal trees (`.versions/`, `.thumbs/`, `.filex-trash/`, `.keepdir` markers)
cost one indexed lookup and nothing else.

> ⚠ **This used to be untrue, and the untrue version was written down.** Until
> the pre‑write guard landed, the only wired trigger really was the text‑editor
> save, while this page and the `versioning` package doc both described a
> guarantee that covered uploads and archive extraction. The practical effect
> was that re‑uploading a file over itself destroyed the old bytes with nothing
> kept, while editing the *same* file in the browser kept a version — so the
> feature looked like it worked right up until the moment you needed it.

**If the snapshot cannot be taken, the write is refused.** That is the whole
point: losing version history is not a reason to also lose the file. The
surfaces answer **503** with `"code": "SNAPSHOT_FAILED"` and the existing file
is left untouched.

Two batch surfaces differ, deliberately. Archive extract and the AI/MCP `unzip`
tool **skip just the refused member and keep going**, reporting a `refused`
count alongside `count`/`extracted` — a guard refusal is transient and
system‑caused, unlike the permanent, user‑caused skips in the same loop (a
zip‑slip entry, a file/folder kind clash). If **every** member was refused and
nothing landed, they answer 503 `SNAPSHOT_FAILED` with the count rather than a
misleading `200 {"count":0}`.

Turning it off: [`FILEX_VERSIONS_ON_OVERWRITE=0`](CONFIGURATION.md#versioning-on-overwrite)
makes the guard a no‑op — writes then behave exactly as they did before it
existed. `FILEX_VERSIONS_FAIL_OPEN=1` keeps the snapshot attempt but lets a
failed one through instead of refusing the write. Both log a WARN at boot, so a
non‑default state is visible without reading the config.

`save-text` has its own guardrails:

- **Body:** `{ "path": "<adapter>://<relative/path>", "content": "…" }`.
- **Extension whitelist:** only text/code types round‑trip here — `txt`, `md`,
  `json`, `jsonc`, `yaml`/`yml`, `toml`, `ini`, `env`, `csv`, `xml`, `svg`,
  `html`, CSS/SCSS/LESS, JS/TS/JSX/Vue/Svelte, and common source languages
  (`go`, `py`, `php`, `rb`, `rs`, `java`, `c`/`cpp`/`h`, `sh`, `sql`, …), plus
  special filenames like `Dockerfile`, `Makefile`, `.gitignore`,
  `.editorconfig`. Anything else returns **415 `extension not allowed for
  save-text`** — binary/office formats have dedicated edit channels (e.g.
  OnlyOffice).
- **Permission:** requires **≥editor** on the file ([RBAC](RBAC.md)) → **403**
  otherwise.
- **Read‑only mount:** returns **403 `storage is read-only`**.

### Versioning endpoints

**User (authenticated session/token):**

| Method & path | Body / query | Notes |
|---|---|---|
| `GET /api/files/versions` | `?node_id=N` | Lists that node's snapshots, **newest first** (version number, size, etag, created). |
| `POST /api/files/versions/restore` | `{ "node_id": N, "version_id": V, "snapshot_current": true }` | Copies version `V` back over the live file. `snapshot_current` (optional) snapshots the current content first so the restore can be undone. |
| `POST /api/files/save-text` | `{ "path": "adapter://rel", "content": "…" }` | Saves text and snapshots the previous content first (see above). |

**Admin only:**

| Method & path | Notes |
|---|---|
| `DELETE /api/admin/versions/{id}` | Hard‑delete one version row **and** its backing `.versions/…` object. |

### Versioning — failure modes & troubleshooting

**Version history is empty even though I've edited the file.**
Most often because the writes weren't text‑editor saves. In v0.1 **only
`save-text` snapshots** — re‑uploading a binary, extracting an archive, or
editing through another channel won't add history. Also note: directories and
symlinks are **never** versioned, and the **first** save of a new file has no
prior content to snapshot.

**A version I wanted is gone / "restore" can't find it.**
Retention keeps only the **newest 20** versions per file — older snapshots are
trimmed after each new save. An admin `DELETE /api/admin/versions/{id}` also
removes one permanently. Once trimmed/deleted, a version is unrecoverable.

**`version belongs to a different node`.**
The `version_id` in a restore request doesn't belong to the `node_id` you sent.
Re‑list with `GET /api/files/versions?node_id=N` and use an ID from that node.

**I saved the file, but no new version appeared.**
`save-text` treats snapshotting as **best‑effort**: if the pre‑write snapshot
fails (storage or DB hiccup) filex logs `save-text: snapshot failed (continuing
with write)` and **still saves your edit** — you keep the new content, but that
one pre‑edit state wasn't captured. Check the server log.

**Can't save / snapshot on a particular mount.**
The storage is **read‑only** (403 `storage is read-only`) — no writes, so no
snapshots either. Restore also writes the live file and needs a writable driver.

---

## See also

- [STORAGE.md](STORAGE.md) — mounts, adapters, read‑only mounts, quota
- [RBAC.md](RBAC.md) — viewer / editor / admin levels and confinement that gate
  the trash list, restore, and save‑text
- [CONFIGURATION.md](CONFIGURATION.md) — global config / env reference
- [SSO.md](SSO.md) — sign‑in and account roles
