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
- [Versioning](#versioning) — [how it works](#how-versioning-works) · [retention](#version-retention) · [what triggers a snapshot](#what-triggers-a-snapshot) · [endpoints](#versioning-endpoints) · [restoring is a write](#restoring-is-a-write) · [failure modes](#versioning--failure-modes--troubleshooting)
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

⚠ **Restoring enqueues a virus scan** of every file it brings back (a folder
restore scans its whole subtree). The trash is also where the antivirus job
puts an infected file — quarantine and a user deletion produce the identical
row — so a restore can release something ClamAV condemned, and the bytes may
have been sitting there since before the current signature database. The scan
is asynchronous, exactly like an upload's; see
[PROTECTION.md](PROTECTION.md#restoring-is-a-write).

⚠⚠ **The storage sync worker does not touch the trash**, and it never
un-deletes a row. It used to: it walked into `.filex-trash/`, found an object
with no live row, found the soft-deleted one, and cleared `deleted_at` — so a
deleted file came back on its own, and a quarantined one left quarantine, at
the next sync pass. See
[ARCHITECTURE.md](ARCHITECTURE.md#the-walk-and-the-trash) for the rule that
replaced it and for how an install that already took the damage repairs itself.

> **Two edge behaviours worth knowing:**
> - If the storage driver can rename, the trash step is a rename. If it can only
>   **copy**, filex copies into the trash key and deletes the source afterwards,
>   so the bytes still survive. Only a driver that can do **neither** falls back
>   to a real hard delete — and then the item is deliberately **not** listed in
>   the trash, because a Restore there could never work. None of the shipped
>   drivers (local, S3, SFTP, FTP, WebDAV) fall into that case.
> - Removing one item from the trash for good is an administrator's action,
>   `DELETE /api/admin/trash/{id}`. A path under `.filex-trash/` cannot be
>   deleted (or written, moved or shared) through any person-facing surface —
>   it is refused with **403 `RESERVED_NAME`**. ⚠ Before 0.43 the file
>   manager's `delete` hard-deleted it for any editor, which bypassed exactly
>   that rule.

### Every delete surface uses the same trash

Deletion is not a web‑UI‑only concept. The web explorer, **WebDAV**, **SFTP**,
**FTPS**, **NFS**, the **S3 gateway**, the **AI/REST** endpoints, the **MCP**
tools, the **CLI/sync client** and the asynchronous batch‑ops worker all go
through one shared helper (`trash.Put`), so an item deleted from any of them
lands in the trash the same way and is restored the same way. A protocol added
later inherits the behaviour by calling that helper instead of driving the
storage driver itself.

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

⚠ Until this was fixed, a folder whose name (or whose parent's name) is not plain
ASCII — `Müşteri`, `Çıktılar` — went to the trash **without** its contents: the
descendants were matched by a prefix length counted in bytes where the database
counts characters. The files stayed live under a folder that was gone, and the
next storage sync tombstoned them one by one, outside the folder's trash entry.
Restoring such a folder had the same blind spot.

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

### Who deleted it

Every item in the trash records **who put it there** (`nodes.deleted_by`,
migration 00061), and both Trash views show it: the explorer in a "Deleted by"
column ("You" for your own deletes), the admin's Trash page by name.

- **Who is recorded.** It is the person the delete was done for, whichever way
  it arrived: the explorer, the API, WebDAV/SFTP/FTPS/S3 under their
  account, or a delete the operations queue ran later (the queue keeps who
  asked).
- **Folders.** Deleting a folder records the person on the folder and on
  every item inside it, because the trash lists a folder's contents as rows of
  their own.
- **Nobody recorded.** An item shows a dash in two cases, and the hover text
  says so:
  - nobody in filex deleted it: the scanner found it gone from the storage,
    or the virus scan quarantined it;
  - it was deleted before this was kept.

  The two cannot be told apart, so neither is called "System".
- **Restore.** A restore clears the record. If the item goes back into the
  trash, it records whoever deleted it that time.
- **Deleted accounts.** Deleting an account clears the record wherever it
  names that account.

The listing resolves the names in one lookup per page, as the file listing
does for owners. Nothing is backfilled: there is no honest way to know who
deleted an item before the column existed.

### Retention & purge

Trashed items are kept for a fixed window, then hard‑deleted automatically.

| Setting | Where | Default | Meaning |
|---|---|---|---|
| `trash.retention_days` | DB `settings` table | **30** | Days a soft‑deleted item survives before automatic purge. Missing, non‑numeric, or `≤ 0` values fall back to 30. |

A **daily background loop** scans for nodes whose `deleted_at` is older than the
retention window and, for each one:

1. deletes the backing storage object under `.filex-trash/` (**best‑effort** —
   if the driver delete fails, the run logs a warning and still continues).
   A row the storage sync soft‑deleted **where it stood** (it found the file
   gone) has no bytes of its own, so only the row goes: whatever stands at its
   path now arrived later and is left alone. ⚠ The purge used to delete that
   path anyway, which destroyed a file that had come back under the old name —
   and, for a folder row, the whole folder that stood there again;
2. decrements the owner's [quota](STORAGE.md) usage (files only);
3. hard‑deletes the DB row.

The first tick fires **one interval after startup**, not immediately, so a
restart‑looping server doesn't hammer the backend. The purge walks the trash in
batches of 500 rows, **by id**, so every row is met once per run — a row it may
not touch (another storage, another tenant) or cannot purge is passed over, not
read again — and reports a summary (`scanned` / `deleted` / `failed` /
`bytes`). **One purge sweep runs at a time:** the daily loop waits for an admin
"empty trash" that is running, and an "empty trash" asked for while another
sweep runs waits its turn (see below). Two sweeps over the same rows would each release the
owner's quota for them.

A purge narrowed to one storage (`storage_id`) or to a tenant's own storages
reads only those storages' rows. ⚠ It used to read every storage's oldest rows
and skip the foreign ones, and a skipped row never goes away: with 500 older
trashed rows on other storages, emptying one storage's trash re-read the same
500 until the request timed out, and purged nothing. A batch in which not one
row could be purged now ends the run as well (the failures are counted and
logged; the next run tries again).

### Trash endpoints

**User (authenticated session/token):**

| Method & path | Body / query | Notes |
|---|---|---|
| `GET /api/files/manager/trash` | `?storage_id=…&limit=…&offset=…` | Lists soft‑deleted items. `limit` defaults to 50 (max 500). Each entry shows the **original** `name`/`path` (not the internal trash key), `deleted_at`, `size`, `storage_name`, **`ttl_days`** (days remaining before purge, floored at 0), and who deleted it: **`deleted_by_id`**, **`deleted_by_name`**, and **`deleted_by_self`** (`true` when it was the caller). The three are absent when nobody is recorded (see *Who deleted it* below). |
| `POST /api/files/manager/restore` | `{ "node_id": 123 }` | Moves the file back to its original path and re‑attaches the row. Returns **409** `{ "code": "EXISTS", "name", "path" }` when something already holds that path; nothing moves and the entry stays in the trash. |

The explorer's **Trash** view draws these entries in its own table with the
facts a deleted item has: **Deleted** (when — the date column, sortable and
grouped by it), **Deleted from** (the storage and folder it will be restored
to) and **Time left** (`ttl_days`: "30 days", "1 day", *Due for deletion* at
0). A trashed row has no owner in this listing, so the Owner column is not
drawn there, and **+ New** is gone — nothing is made inside the Trash.

Both are **filtered by access**: a [confined](RBAC.md) (root‑locked) caller only
sees / can restore items whose original path is inside its root, and
[RBAC](RBAC.md) requires **≥viewer** to see an item in the list and **≥editor**
on its original path to restore it (restore writes the file back).

An entry is judged on the path it was deleted **from**, never on its trash key.
A row old enough to record no original path — its path is still inside
`.filex-trash/` — has nothing to judge, so it is neither listed nor restorable,
for anybody; an admin can still purge it. Judging it on the bin would hand the
answer to whoever holds a grant on `.filex-trash/`.

**Admin only:**

| Method & path | Body / query | Notes |
|---|---|---|
| `POST /api/admin/trash/empty` | `?older_than_days=N&storage_id=…` **or** JSON `{ "older_than_days": N, "storage_id": … }` | Queues a purge of everything deleted more than `N` days before **the moment it is asked for**, in one storage or every storage the caller can reach. **`0` or missing days is everything in the trash at that moment** — a file deleted while the purge runs stays in the trash. Waits up to two seconds: **200** with the final counts when the purge is done by then, otherwise **202** with its progress so far while it carries on as an ops job — see the run fields below. **409** `{ "code": "BUSY", "job": … }` while the caller's tenant already has one queued or running (`job` is that run); another tenant's purge, or the nightly retention, does not refuse it — it waits its turn (`queued: true`). **400** for anything it cannot read — a non‑integer or negative day count, a storage id that is not a number, an unknown field — and nothing is purged. |
| `GET /api/admin/trash/empty` | — | The latest purge the caller's tenant asked for: queued, running or finished. `{ "running": false }` alone when it has asked for none. |
| `DELETE /api/admin/trash/{id}` | — | Immediately hard‑delete one trashed node (storage object + quota + row). |

A run reports `{ ok, op_id, running, queued, cancelled, storage_id,
older_than_days, total, total_bytes, scanned, purged, failed, bytes, started_at,
finished_at, error }`. `total` / `total_bytes` are the rows in its scope when it
was asked for and the bytes their files hold; `running: false` is the end —
`purged` can finish below `total`, because a folder takes the rows inside it
along. `failed` counts rows that could not be purged (they stay in the trash;
the server log names them); `error` is a run that could not go on at all;
`cancelled` is a run somebody stopped. `started_at` is the moment it was asked
for — the cutoff the run purges below.

**The run is an ops job** (kind `trash-empty`, `op_id`): it is in the
explorer's operations centre and the admin tray, `GET /api/files/ops/{op_id}`
reads it and `POST /api/files/ops/{op_id}/cancel` stops it (an administrator,
or the admin who asked). A stopped run finishes the row in hand and stops;
what it had not reached stays in the trash. It never takes the queue's worker —
copies, moves, deletes and upload commits keep running beside it — and a
restart does not forget it: the row is requeued at boot and the run carries on
with what is left, still bounded by the moment it was asked for. A run belongs
to the tenant that asked for it: another tenant's admin neither sees it nor its
counts, on this endpoint or in the operations list.

> ⚠ Up to v0.42.2 the purge ran **inside** the request and answered only
> when it was done, so a large trash could not be emptied from the UI at all:
> tens of thousands of files take many minutes, and the first proxy timeout in
> front of filex (nginx: 60 s; the admin page's own client: 30 s) cut the request
> — and the purge with it. A script that reads `purged` from any 2xx should now
> check `running` too: a 202 carries the counts so far, not the final ones.
> (Contributed by Berk Başarır, [#47](https://github.com/BRF-Tech/filex/pull/47).)

### Trash — failure modes & troubleshooting

**"Empty trash" seemed to do nothing, or answered 504.**
Up to v0.42.2 the purge ran inside the request and the first proxy timeout cut
it short; update. A large trash now shows its progress on the admin Trash page,
in the explorer's trash banner and in the operations centre, and carries on if
you leave the page. A purge still running when the server restarts carries on
after the restart. To stop one, press **Stop** on the Trash page or Cancel on
its row in the operations centre: what it purged is gone, the rest is still in
the trash.

**A restored file reappeared at the storage root, not its old folder.**
Its original parent directory was itself deleted in the meantime. filex prefers
a **root restore** over orphaning the row — move the file back manually once the
folder exists again.

**Restore answers 409 `EXISTS`.**
A file or folder now holds the original path. filex refuses rather than
overwrite it or pour one folder into another. Rename or move what is there,
then restore again.

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

Only **files** are versioned. Directories are skipped, and so are symlinks a
storage does not follow ([Symlinks](STORAGE.md#symlinks)); a link inside a
`local` storage's folder is catalogued as what it points at, so a linked file
is versioned like any other. A snapshot
is also skipped when there is nothing to capture — a brand‑new file with no live
content yet, or a row whose object isn't on the backend.

A snapshot is **not a catalogue entry**. It belongs to its `node_versions` row,
which is keyed by the file it versions, and the storage sync never walks into
`.versions/` (nor `.thumbs/`, nor `.filex-trash/`). Older versions did: a full
scan minted a hidden, system-owned row for every snapshot folder and file, and
those rows could later land in the trash — where purging a folder row deletes
its whole prefix, i.e. the version history. The next full sync after upgrading
drops every such row from the catalogue (never from the backend), and the sync's
delete pass never moves anything inside these trees into the trash.

⚠ That last case is a **silent** skip: if the catalogued path and the object on
the backend ever disagree, the guard finds nothing to snapshot and reports
success. Every shipped driver normalises the key it is handed, so the two agree
in practice — but a storage **plugin** that does not would lose history with no
error anywhere. Plugin authors: normalise, and see [PLUGINS.md](PLUGINS.md).

⚠⚠ **Until v0.34.0 that disagreement was not hypothetical: it happened to
every moved or renamed file.** `Store.MoveNode` did not update `storage_key`,
so the row went on naming the old path, and versioning prefers `storage_key`
over `path` — the snapshot stated `ErrNotFound`, reported "nothing to
snapshot", and the destructive write went ahead with **zero versions written**.
The column now follows the path on a live row, and migration `00033` repairs
the rows that already exist, because nothing else would: the periodic walk
writes `seen_at` and `UpdateNodeMeta` and neither statement touches this
column. (It deliberately does *not* follow the path on a **trashed** row, where
`storage_key` is the only record of where restore puts the file back.)

**Restore** copies a recorded version back over the live file and refreshes the
node's size/etag. Passing `snapshot_current: true` snapshots the current content
**first**, so the restore itself is reversible.

⚠ It also **enqueues a virus scan of the restored file**. Snapshots themselves
are never scanned — every destructive write takes one, so scanning each would
multiply the scan load by the edit rate for bytes nobody can reach — which left
"overwrite the infected file with a clean one, then roll back" as a way to put
an infected file live. The restore is where that is closed; see
[PROTECTION.md](PROTECTION.md#restoring-is-a-write).

### Version retention

| Setting | Where | Default | Meaning |
|---|---|---|---|
| `versions.keep_n` | DB `settings` table, written by **Protection → Version retention** (`PATCH /api/admin/protection`, accepted range 0–1000) | **0** | How many versions of a file to keep. `0` means "not configured": the daily retention sweep is off and the snapshot path applies its compile‑time safety trim of **20** instead. |

So a file's history is trimmed to the newest **20** snapshots out of the box,
and to `keep_n` when an operator sets one — the snapshot path honours the
setting inline, so a value **above** 20 really does keep more (it used to claw
every node back to 20 on the next snapshot, which made larger values
meaningless). A `keep_n > 0` additionally runs a **daily sweep** over every node
that has version rows, so lowering the number reaches files nobody is editing;
see [Protection → Version retention](PROTECTION.md#version-retention-versionskeep_n).

Trimming removes both the `node_versions` row and the backing `.versions/…`
object (best‑effort per object).

### What triggers a snapshot

**Every destructive write on the surfaces below.** Each of them calls the
pre‑write guard, which snapshots what is about to be lost:

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

⚠⚠ **`SFTP`, `FTPS` and `NFS` are not in that table, and their absence is
real, not an omission in the writing.** Those three write through
`internal/protocolsync`, which does not call the pre‑write guard, so a client
that replaces a file over SFTP, FTPS or NFS destroys the previous bytes with
**no snapshot taken and no error**. Everything else those surfaces share with
the rest of filex — the catalogue row, the search index, the realtime frame,
the antivirus scan, trash on delete — they do have. Versioning is the one gap.
If version history is what you are relying on, keep those three protocols out
of the write path, or check the file's history after the first overwrite rather
than assuming it.

**Renames and moves are not in that table because they never overwrite.** A
rename onto a name that is taken is refused (`409 NAME_TAKEN`); a move lands
beside it as `name-copy`. ⚠ Until the rename was guarded it replaced the file
that held the name with no snapshot, and the catalogue then dropped that file's
row — so the versions it already had were lost with it. A WebDAV `MOVE` sent
with `Overwrite: T` replaces the destination at the client's explicit request,
and deletes it into the trash first (the protocol's delete-before-move), so it
stays restorable from there.

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

| Method & path | Body / query | Permission | Notes |
|---|---|---|---|
| `GET /api/files/versions` | `?node_id=N` | **≥viewer** | Lists that node's snapshots, **newest first** (version number, size, etag, created). |
| `POST /api/files/versions/snapshot` | `{ "node_id": N }` | **≥editor** | Records the current content as a new version on demand — the "take a version now" button in the details panel's **Activity** tab, which is where a file's history and its comments live. Writes an object into the node's storage. |
| `POST /api/files/versions/restore` | `{ "node_id": N, "version_id": V, "snapshot_current": true }` | **≥editor** | Copies version `V` back over the live file. |
| `POST /api/files/save-text` | `{ "path": "adapter://rel", "content": "…" }` | **≥editor** | Saves text and snapshots the previous content first (see above). |

**Admin only:**

| Method & path | Notes |
|---|---|
| `DELETE /api/admin/versions/{id}` | Hard‑delete one version row **and** its backing `.versions/…` object. |

⚠ **The permission column is load-bearing, and it is new.** Before the release
this note ships in, these
routes had no ownership or ACL check of any kind: a `viewer`-role account could
`POST /restore` and overwrite the live bytes of any file whose node id it could
name, on **single-tenant installs too**. Restoring and snapshotting are writes,
so they now need **editor**, exactly like `save-text` beside them; listing stays
at **viewer**, because somebody who can read the file is not being told its
history is a secret.

⚠ Every one of these takes a raw `node_id`, so the node is resolved and
authorized before anything happens: existence (and **not trashed** — a trashed
row's live path is its `storage_key`, so restoring onto one wrote bytes to a
path the catalogue says holds nothing) → tenancy → the token's `root:`
confinement → RBAC. The first three answer **404**, identical to a node that
never existed, so the endpoint cannot be used to discover which ids are real;
only the RBAC refusal is **403 `insufficient permission`**.

⚠ `snapshot_current` is honoured only when the pre-write guard is switched off
(`FILEX_VERSIONS_ON_OVERWRITE=0`). With the guard on — the default — a restore
already snapshots the bytes it is about to replace, and doing it twice would
record identical content and spend a retention slot on the duplicate. See
[Restoring is a write](#restoring-is-a-write) below.

### Restoring is a write

A restore replaces the live bytes at an unchanged path, so it is treated as one:

- **It goes through the pre-write guard.** Before the copy, the bytes that are
  about to be destroyed are snapshotted, and a snapshot that cannot be taken
  refuses the restore with **503 `SNAPSHOT_FAILED`** instead of overwriting them
  unrecoverably — the same contract every other write surface has. ⚠ Restore was
  the one write in filex that skipped this until the release this note ships in, so rolling back twice in
  a row destroyed whatever was live in between with nothing recorded.
- **It emits `file.updated`.** Restores used to change a file's bytes and tell no
  webhook subscriber; they now go through the same post-write gate as an upload.
- **It enqueues a virus scan** of the restored file — see above.
- **It needs `≥editor`** on the file, like every other write.

⚠ Because the guard already snapshots the outgoing content, `snapshot_current`
only does work when the guard is switched off
([`FILEX_VERSIONS_ON_OVERWRITE=0`](CONFIGURATION.md#versioning-on-overwrite)).
With the guard on, honouring both would record identical bytes twice and spend a
retention slot on the duplicate.

### Versioning — failure modes & troubleshooting

**Version history is empty even though I've edited the file.**
Check which surface wrote it. **SFTP, FTPS and NFS take no snapshot at all**
(see the table above) — that is the likeliest answer, and it fails silently. The
other three: the guard is switched off
([`FILEX_VERSIONS_ON_OVERWRITE=0`](CONFIGURATION.md#versioning-on-overwrite),
which logs a WARN at boot); directories and unfollowed symlinks are **never** versioned;
and the **first** save of a new file has no prior content to snapshot.

**A version I wanted is gone / "restore" can't find it.**
Retention keeps only the newest **20** versions per file — or `versions.keep_n`
when an operator has set one — and older snapshots are trimmed after each new
save. An admin `DELETE /api/admin/versions/{id}` also removes one permanently.
Once trimmed/deleted, a version is unrecoverable.

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
