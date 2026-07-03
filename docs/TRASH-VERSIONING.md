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
> - If the storage driver can't rename (no move support), delete falls back to a
>   legacy **hard delete** — there's nothing to restore.
> - Deleting an item that is **already in trash** (its path is under
>   `.filex-trash/`) **hard‑deletes it permanently** — this is how "empty a
>   single item from trash" works.

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

In the current release the wired trigger is the **text / code editor save** —
`POST /api/files/save-text`. When you save a file from the built‑in code or
markdown editor, filex snapshots the **existing** file first, then writes the new
content. The snapshot is taken **only when** versioning is enabled *and* the file
already exists in the cache (the very first save of a new file has nothing prior
to capture).

> **Binary overwrites (re‑uploading a file) are not versioned in v0.1.** Version
> history reflects text‑editor saves only. Other write paths (upload finalize,
> archive extract) do not currently snapshot.

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
