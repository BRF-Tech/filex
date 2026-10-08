# Trash & version history

filex protects against two everyday mistakes - deleting the wrong file and
overwriting good content. **Trash** turns a delete into a reversible soft-delete
with a retention window. **Versioning** keeps historical snapshots of a file's
contents so an earlier revision can be restored.

Both features live entirely **inside the storage backend** you already mounted
(see [STORAGE.md](STORAGE.md)) - filex adds a hidden `.filex-trash/` and a
hidden `.versions/` prefix on the same disk/bucket. There is no separate trash
server or version store to provision.

- [Trash](#trash) - [how it works](#how-trash-works) · [what it holds](#what-the-trash-holds) · [retention & purge](#retention--purge) · [endpoints](#trash-endpoints) · [failure modes](#trash---failure-modes--troubleshooting)
- [Versioning](#versioning) - [how it works](#how-versioning-works) · [retention](#version-retention) · [what triggers a snapshot](#what-triggers-a-snapshot) · [endpoints](#versioning-endpoints) · [restoring is a write](#restoring-is-a-write) · [failure modes](#versioning---failure-modes--troubleshooting)
- [See also](#see-also)

---

## Trash

### How trash works

Deleting a file or folder from the explorer is a **soft delete**, not an erase:

1. filex **renames** the underlying object on its storage backend to
   `.filex-trash/<unix>-<rand>__<basename>` (a collision-proof key under the
   hidden trash prefix). Nothing is removed from disk/bucket yet.
2. The DB row's `deleted_at` timestamp is set, and the **original path is
   preserved** in the row's `storage_key` column. The row's live `path` /
   `path_hash` are rewritten to the trash location, so a fresh upload at the
   original path still works.
3. The item drops out of normal listings (the `.filex-trash/` prefix is filtered
   out) but stays in the database, ready to restore.

**Restore** reverses step 1: the object is renamed back from `.filex-trash/…` to
its original path, and the parent directory is **re-resolved** so the row
re-attaches in the right place in the tree. If the original parent no longer
exists, filex falls back to a **root restore** rather than leaving the row
orphaned in trash.

⚠ **Restoring enqueues a virus scan** of every file it brings back (a folder
restore scans its whole subtree). The trash is also where the antivirus job
puts an infected file - quarantine and a user deletion produce the identical
row - so a restore can release something ClamAV condemned, and the bytes may
have been sitting there since before the current signature database. The scan
is asynchronous, exactly like an upload's; see
[PROTECTION.md](PROTECTION.md#restoring-is-a-write).

⚠⚠ **The storage sync worker does not touch the trash**, and it never
un-deletes a row. It used to: it walked into `.filex-trash/`, found an object
with no live row, found the soft-deleted one, and cleared `deleted_at` - so a
deleted file came back on its own, and a quarantined one left quarantine, at
the next sync pass. See
[ARCHITECTURE.md](ARCHITECTURE.md#the-walk-and-the-trash) for the rule that
replaced it and for how an install that already took the damage repairs itself.

> **Two edge behaviours worth knowing:**
> - If the storage driver can rename, the trash step is a rename. If it can only
>   **copy**, filex copies into the trash key and deletes the source afterwards,
>   so the bytes still survive. Only a driver that can do **neither** falls back
>   to a real hard delete - and then the item is deliberately **not** listed in
>   the trash, because a Restore there could never work. None of the shipped
>   drivers (local, S3, SFTP, FTP, WebDAV) fall into that case.
> - Removing one item from the trash for good is an administrator's action,
>   `DELETE /api/admin/trash/{id}`. A path under `.filex-trash/` cannot be
>   deleted (or written, moved or shared) through any person-facing surface -
>   it is refused with **403 `RESERVED_NAME`**. ⚠ Before 0.43 the file
>   manager's `delete` hard-deleted it for any editor, which bypassed exactly
>   that rule.

### What the trash holds

The trash holds what was **deleted in filex**: its bytes wait in
`.filex-trash/`, and a restore puts them back. A file or folder deleted
**outside filex** - in a shell, by another program on the disk, in the bucket -
is not in the trash: there is nothing of it there to restore. The next scan
that finds it gone removes it from the catalogue instead (see
[STORAGE.md → Sync](STORAGE.md#sync)).

> ⚠ Up to 0.47 the scan put such an item in the trash **where it stood**, and
> the trash listed it with a Restore that could bring nothing back (issue #74).
> From 0.48 those entries are no longer listed, and a restore of one by id
> answers **404** `trash entry not found`; the next full scan of the storage
> removes them from the catalogue, as do the nightly retention purge and
> **Empty trash**. Nothing on the storage is touched.
>
> ⚠ Up to 0.49 the operations queue still wrote rows of that shape itself
> (issue #104): a queued delete of an item that was already gone, a delete on a
> storage that keeps no trash, and the source of a move to another storage
> soft-deleted the item's row where it stood - for a folder, only the folder's
> row, with its contents left live under it. From 0.50 those branches drop the
> rows, every row below a folder with it, exactly as the explorer's delete of
> an item that is already gone does: each file's bytes leave its owner's quota,
> and its [version history](#versioning) goes with it.

### Every delete surface uses the same trash

Deletion is not a web-UI-only concept. The web explorer, **WebDAV**, **SFTP**,
**FTPS**, **NFS**, the **S3 gateway**, the **AI/REST** endpoints, the **MCP**
tools, the **CLI/sync client** and the asynchronous batch-ops worker all go
through one shared helper (`trash.Put`), so an item deleted from any of them
lands in the trash the same way and is restored the same way. A protocol added
later inherits the behaviour by calling that helper instead of driving the
storage driver itself.

The helper never destroys data: when a backend cannot preserve the bytes it
reports that instead of deleting them, and the caller decides what to do. That
is also what keeps the emitted events honest - `file.trashed` fires **only**
when the bytes are genuinely restorable, `file.deleted` when they are really
gone.

A **folder** goes to trash as one restorable unit: the folder row is retagged
into the trash and its cached descendants are dragged along with it, so a single
Restore brings the whole subtree back. Note that the descendants are still
individual rows, and the trash listing is flat - a deleted folder therefore
shows its children as separate entries even though restoring the folder is one
action.

⚠ Until this was fixed, a folder whose name (or whose parent's name) is not plain
ASCII - `Müşteri`, `Çıktılar` - went to the trash **without** its contents: the
descendants were matched by a prefix length counted in bytes where the database
counts characters. The files stayed live under a folder that was gone, and the
next storage sync tombstoned them one by one, outside the folder's trash entry.
Restoring such a folder had the same blind spot.

> ⚠ **Sync clients delete in bulk.** A single `rclone sync --delete` run can
> remove hundreds of files, and every one of them now lands in the trash. That
> is the point - the run is recoverable - but it also means a bulk delete
> **does not free space** until the retention window passes, and each file adds
> a row to the flat trash listing. Watch storage headroom after a large sync,
> and use the admin "empty trash" action when you need the space back
> immediately.

### Quota and the trash

**Trashed items keep counting against the owner's quota.** Usage is decremented
when an item is *purged*, not when it is trashed (see `trash.purgeOne`), and
that is deliberate: bytes parked in `.filex-trash/` still occupy the backend.
Deleting does not free space - emptying the trash does. Every surface follows
the same rule; none of them adjust quota at delete time.

> ⚠ Until v0.20 this was **theory**: nothing incremented `usage_bytes` at
> all, so nothing was counted and nothing was ever released either. The
> accounting is real now - see [Quotas](QUOTAS.md) for the full set of
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
  their own. A folder on a storage that cannot keep deleted bytes is deleted
  outright and is not in the trash at all: it and every row below it leave the
  catalogue.
- **Nobody recorded.** An item shows a dash in two cases, and the hover text
  says so:
  - nobody in filex deleted it: the virus scan quarantined it (up to 0.47,
    also an item the scanner found gone from the storage - see
    [what the trash holds](#what-the-trash-holds));
  - it was deleted before this was kept.

  The two cannot be told apart, so neither is called "System".
- **Restore.** A restore clears the record. If the item goes back into the
  trash, it records whoever deleted it that time.
- **Deleted accounts.** Deleting an account clears the record wherever it
  names that account.

The listing resolves the names in one lookup per page, as the file listing
does for owners. Nothing is backfilled: there is no honest way to know who
deleted an item before the column existed.

### Discarded drafts

A [draft](ONLYOFFICE.md#drafts-nothing-is-in-the-folder-until-you-save) (a new
document not saved yet) that its owner discards - *Discard* when closing it, or
*Delete* in Drafts - goes to the trash like any other file, and the usual
retention deletes it.

- **Only its owner sees it there.** Drafts are private, and so is a discarded
  one: the trash listing leaves it out for everybody else, administrators
  included.
- **"Deleted from" reads Drafts.** The entry carries `draft: true`, and its
  `path` is just its name - never the hidden `.filex-drafts/…` folder it lived in.
- **Restore puts it back in Drafts**, still meant for the folder it was
  created for.

### Retention & purge

Trashed items are kept for a fixed window, then hard-deleted automatically.

| Setting | Where | Default | Meaning |
|---|---|---|---|
| `trash.retention_days` | DB `settings` table | **30** | Days a soft-deleted item survives before automatic purge. Missing, non-numeric, or `≤ 0` values fall back to 30. |

A **daily background loop** scans for nodes whose `deleted_at` is older than the
retention window and, for each one:

1. deletes the backing storage object under `.filex-trash/` (**best-effort** -
   if the driver delete fails, the run logs a warning and still continues).
   A row soft-deleted **where it stood** (a scan up to 0.47 found the file
   gone) has no bytes of its own, so only the row goes: whatever stands at its
   path now arrived later and is left alone. ⚠ The purge used to delete that
   path anyway, which destroyed a file that had come back under the old name -
   and, for a folder row, the whole folder that stood there again;
2. decrements the owner's [quota](STORAGE.md) usage (files only);
3. hard-deletes the DB row;
4. deletes the file's snapshots under `.versions/<id>/` (its
   [version history](#versioning)): their rows go with the file's, so nothing
   could reach them afterwards. ⚠ Up to 0.49 they were left on the storage for
   good, after a purge and whenever else a file left the catalogue for good.

A trashed folder's contents are purged first, deepest first, each row on its
own (a sub-folder purged before what is in it took its contents through the
database cascade, releasing nothing). ⚠ A folder that still has **live** rows
under it is not purged: an earlier version could leave a folder deleted where
it stood with its contents live (see
[what the trash holds](#what-the-trash-holds)), and purging it took those
files with it. Such a folder is counted in `failed` and stays until nothing
live is under it - the storage sync removes those rows once it finds their
files gone - and the next run purges it.

The first tick fires **one interval after startup**, not immediately, so a
restart-looping server doesn't hammer the backend. The purge walks the trash in
batches of 500 rows, **by id**, so every row is met once per run - a row it may
not touch (another storage, another tenant) or cannot purge is passed over, not
read again - and reports a summary (`scanned` / `deleted` / `failed` /
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
| `GET /api/files/manager/trash` | `?storage_id=…` or `?storage=<adapter name>`, `&limit=…&offset=…&lang=…` | Lists soft-deleted items. `limit` defaults to 50 (max 500). Each entry shows the **original** `name`/`path` (not the internal trash key), `deleted_at`, `size`, `storage_name`, **`ttl_days`** (days remaining before purge, floored at 0), and who deleted it: **`deleted_by_id`**, **`deleted_by_name`**, and **`deleted_by_self`** (`true` when it was the caller). The three are absent when nobody is recorded (see *Who deleted it* below). Beside the page the answer counts **every** entry the caller may see: **`total`**, **`total_bytes`**, **`newest_deleted_at`**, the same three per storage in **`storages`** (`[{storage_id, storage_name, count, bytes, newest_deleted_at}]` - what a storage's virtual `.trash` row shows) and **`summary`**, the server's sentence for it in the reader's language (`?lang=`, else the account's, else `Accept-Language`). `storage=<name>` narrows it to one storage by its adapter name; a name no storage has lists nothing (up to 0.53 the parameter was not read, so a storage's `.trash` row summed the newest 50 deletions of every storage). **`draft: true`** marks one of the caller's own [discarded drafts](#discarded-drafts), whose `path` is then just its name; nobody else is shown it. `total` counts the entries **the caller may see**, and `offset`/`limit` page through those - up to v0.46.0 a member whose trash was interleaved with entries they may not see was told `total` = the length of the filtered first page, and could not reach the rest. `limit` above 500 is read as 50. |
| `POST /api/files/manager/restore` | `{ "node_id": 123 }` | Moves the file back to its original path and re-attaches the row. Returns **409** `{ "code": "EXISTS", "name", "path" }` when something already holds that path; nothing moves and the entry stays in the trash. |
| `POST /api/files/manager/restore` | `{ "node_ids": [123, 124] }` | Since 0.54: a selection in **one** request, inside it (no queue). Every entry is judged and restored on its own; **200** whatever the mix, `{ "done", "failed", "reason_code", "taken", "summary" }` - `reason_code` is why the first entry that did not come back did not (`exists`, `not_found`, `forbidden`, `failed`), `taken` names the entries whose place is taken, and `summary` is the server's sentence for all of it in the reader's language (*"2 items restored - 1 item was not restored: something already has the name “b.txt”"*). The explorer's Restore on a server without a queue, and its **Undo** of a delete, send this. At most **1000** entries. |
| `POST /api/files/manager/restore?queued=1` | `{ "node_ids": [123, 124] }` | The same checks for every entry, and one refusal refuses the batch. What they allow is queued, one job per storage: **202** `{ "ops": [{ "kind": "restore", … }], "done", "summary" }` (*"Restoring 3 items…"*), followed with `GET /api/files/ops`, where each job's row carries its own `summary` once it ends. An entry whose place is taken fails on its own, and the job's `error` says so; the others come back. Offered when `capabilities.queued` lists `restore`; the explorer's Restore uses it then. At most **1000** entries per request: more answer **400** `{ "code": "TOO_MANY", "max": 1000 }`. |

An agent has the same two through the AI surface (since 0.50): the MCP tools
`trash_list` and `trash_restore`, and their REST twins `GET /api/ai/trash` and
`POST /api/ai/trash/restore` (`{node_ids}`, always queued) - the same handler,
so the same checks and answers ([MCP.md](MCP.md#copy-apps-operations-trash-versions-archives-links)).

The explorer's **Trash** view draws these entries in its own table with the
facts a deleted item has: **Deleted** (when - the date column, sortable and
grouped by it), **Deleted from** (the storage and folder it will be restored
to) and **Time left** (`ttl_days`: "30 days", "1 day", *Due for deletion* at
0). A trashed row has no owner in this listing, so the Owner column is not
drawn there, and **+ New** is gone - nothing is made inside the Trash. It
reads the trash 200 entries at a time: the banner above the list says the
server's count and size of the whole trash (`summary`), with how many are on
screen, and **Show more** reads the next page. Up to 0.53 it read the first 50
and stopped, so older entries could be neither seen nor restored there. The
admin **Trash** page pages the same listing with its table's pager.

Both are **filtered by access**: a [confined](RBAC.md) (root-locked) caller only
sees / can restore items whose original path is inside its root, and
[RBAC](RBAC.md) requires **≥viewer** to see an item in the list and **≥editor**
on its original path to restore it (restore writes the file back). With
multi-tenancy on, an entry of another tenant's storage answers **404** `trash
entry not found`, as an id that does not exist does - one at a time and in a
queued batch. Up to v0.46.0 the restore never asked whose storage the entry
was in, and a storage without access control let any member restore another
tenant's deleted file by its id.

An entry is judged on the path it was deleted **from**, never on its trash key.
A row old enough to record no original path - its path is still inside
`.filex-trash/` - has nothing to judge, so it is neither listed nor restorable,
for anybody; an admin can still purge it. Judging it on the bin would hand the
answer to whoever holds a grant on `.filex-trash/`.

**Admin only:**

| Method & path | Body / query | Notes |
|---|---|---|
| `POST /api/admin/trash/empty` | `?older_than_days=N&storage_id=…` **or** JSON `{ "older_than_days": N, "storage_id": … }` | Queues a purge of everything deleted more than `N` days before **the moment it is asked for**, in one storage or every storage the caller can reach. **`0` or missing days is everything in the trash at that moment** - a file deleted while the purge runs stays in the trash. Waits up to two seconds: **200** with the final counts when the purge is done by then, otherwise **202** with its progress so far while it carries on as an ops job - see the run fields below. **409** `{ "code": "BUSY", "job": … }` while the caller's tenant already has one queued or running (`job` is that run); another tenant's purge, or the nightly retention, does not refuse it - it waits its turn (`queued: true`). **400** for anything it cannot read - a non-integer or negative day count, a storage id that is not a number, an unknown field - and nothing is purged. |
| `GET /api/admin/trash/empty` | - | The latest purge the caller's tenant asked for: queued, running or finished. `{ "running": false }` alone when it has asked for none. |
| `GET /api/admin/trash/empty/preview` | `?older_than_days=N&storage_id=…&lang=…` | **The dry run** (since 0.54): what `POST …/empty` would delete with the same narrowing, nothing deleted - `{ "dry_run": true, "count", "bytes", "storage_id", "older_than_days", "summary" }`. Counted by the purge's own tally over the caller's own reach (every entry of every storage the caller's tenant reaches, other people's deletes included), so `count` is what the purge deletes; `summary` is the confirmation's sentence (*"This permanently deletes 61,844 items (12.3 GB). It cannot be undone."*). The explorer's and the admin page's **Empty the trash?** dialogs show it and keep their button shut until it has answered, and while `count` is 0. **400** for a narrowing it cannot read, like the empty. ⚠ Up to 0.53 the explorer's confirmation counted the rows it had loaded - the first page of 50 - while the purge took the whole trash. |
| `DELETE /api/admin/trash/{id}` | - | Immediately hard-delete one trashed node (storage object + quota + row). The id of a node that is not in the trash answers **404** `trash entry not found` and nothing is touched (up to v0.46.0 it was hard-deleted like a trash entry). |
| `POST /api/admin/trash/purge` | `{ "node_ids": [123, 124] }`, `?queued=1`, `?lang=…` | Since 0.54: a selection deleted for good in **one** request. Every entry gets the checks `DELETE …/{id}` asks (its tenant's, and in the trash); what is refused is counted, not fatal. Inside the request: **200** `{ "done", "failed", "reason_code", "summary" }`. With `queued=1` one `purge` job per storage: **202** with `ops` as well, `done` being the entries handed to the jobs and `summary` *"Deleting 3 items permanently…"*; each job's row says how it ended in its own `summary`. The explorer's **Delete permanently** of a selection sends this (it was one `DELETE` per entry). At most **1000** entries. |
| `DELETE /api/admin/trash/{id}?queued=1` | - | The same ownership check, then the purge is a job of the operations queue: **202** `{ "op": { "kind": "purge", … } }`, followed with `GET /api/files/ops`. A folder is purged one object and one row at a time; inside the request the admin page's client gave up after 30 s. Offered when `capabilities.queued` lists `purge`; the admin Trash page uses it then (and restores with `POST /api/files/manager/restore?queued=1`); the explorer queues a selection with `POST /api/admin/trash/purge?queued=1`. The rows say "Deleting permanently…", no second purge is sent for them, and each job's `summary` says what was deleted and what was not, and why, when it ends. Once running it is not cancelled half-way. The job purges what is in the trash **when it runs**: an entry restored while the purge waited in the queue is left alone, and the job ends `failed` with `the item is not in the trash`. |

A run reports `{ ok, op_id, running, queued, cancelled, storage_id,
older_than_days, total, total_bytes, scanned, purged, failed, bytes, started_at,
finished_at, error, summary }`. **`summary`** (since 0.54) is where the run
stands, said by the server in the reader's language: *"Emptying the trash… 120
of 61,844"*, *"Trash emptied: 61,844 items deleted for good, 12.3 GB freed."*,
*"Trash emptied, but 3 items could not be deleted and are still in the
trash."*, *"Emptying the trash was stopped after 4,000 items; …"*. The explorer
and the admin page show it as it is - neither builds its own sentence any more,
and neither sends a person to the server log. A run that could not go on
answers **500** with `error` (its English record, an operator's second line),
`message` and `summary` (what a person is shown); **409 BUSY** carries
`message` too. The operations list's row of a `trash-empty`, `restore` or
`purge` job carries the same `summary`. `total` / `total_bytes` are the rows in its scope when it
was asked for and the bytes their files hold; `running: false` is the end -
`purged` can finish below `total`, because a folder takes the rows inside it
along. `failed` counts rows that could not be purged (they stay in the trash;
the server log names them); `error` is a run that could not go on at all;
`cancelled` is a run somebody stopped. `started_at` is the moment it was asked
for - the cutoff the run purges below.

**The run is an ops job** (kind `trash-empty`, `op_id`): it is in the
explorer's operations centre and the admin tray, `GET /api/files/ops/{op_id}`
reads it and `POST /api/files/ops/{op_id}/cancel` stops it (an administrator
of the tenant that asked; below an administrator nobody sees another person's
ops) - for an agent, `op_get` / `op_cancel` (`GET /api/ai/ops/{id}`,
`POST /api/ai/ops/{id}/cancel`), and `admin_trash_restore` /
`admin_trash_purge` / `admin_trash_purge_batch` with `queued: true` run as jobs
the same way; `admin_trash_empty_preview` is the dry run. A stopped run finishes the row in hand and stops;
what it had not reached stays in the trash. It never takes the queue's worker -
copies, moves, deletes and upload commits keep running beside it - and a
restart does not forget it: the row is requeued at boot and the run carries on
with what is left, still bounded by the moment it was asked for. A run belongs
to the tenant that asked for it: another tenant's admin neither sees it nor its
counts, on this endpoint or in the operations list.

> ⚠ Up to v0.42.2 the purge ran **inside** the request and answered only
> when it was done, so a large trash could not be emptied from the UI at all:
> tens of thousands of files take many minutes, and the first proxy timeout in
> front of filex (nginx: 60 s; the admin page's own client: 30 s) cut the request -
> and the purge with it. A script that reads `purged` from any 2xx should now
> check `running` too: a 202 carries the counts so far, not the final ones.
> (Contributed by Berk Başarır, [#47](https://github.com/BRF-Tech/filex/pull/47).)

### Trash - failure modes & troubleshooting

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
a **root restore** over orphaning the row - move the file back manually once the
folder exists again.

**Restore answers 409 `EXISTS`.**
A file or folder now holds the original path. filex refuses rather than
overwrite it or pour one folder into another. Rename or move what is there,
then restore again.

**A folder restore answered 504, or the page gave up waiting.**
The restore carries on to the end: it no longer depends on anybody waiting for
the answer. List the folder again to see it back. Up to v0.46.0 the proxy's
timeout stopped it between two objects, leaving the folder half in the trash
and half back in place; a second restore then answered 409 `EXISTS`, because
the half that had come back held the name. The rest of such a folder is still
under its `.filex-trash/` key on the backend, to be moved back by hand.

**Restore reports success but the file isn't back on disk.**
The DB flag is cleared **best-effort**: if the driver's move step fails, filex
still un-trashes the row and logs a warning (`trash restore move failed`). Find
the object under `.filex-trash/` on the backend and move it to the original path
by hand.

**An item vanished from trash before its `ttl_days` reached 0.**
Either an admin ran **empty trash** / purged it, or it was **deleted while
already in trash** (which is a permanent hard delete - see the edge behaviours
above).

**`ttl_days` shows 0 but the item is still listed.**
Purge runs on a daily tick - an expired item lingers until the next run. Admins
can force it with `POST /api/admin/trash/empty`.

**Can't delete (or restore) on a particular mount.**
That storage is likely **read-only** - writes (including trashing and restoring)
return **403 `storage is read-only`**. See [read-only mounts](STORAGE.md#read-only-mounts).

**Leftover `.filex-trash/…` objects on the backend.**
Purge deletes the DB row even when the storage delete fails (permissions, outage).
The object is orphaned but harmless; delete it with your storage's own tooling.

---

## Versioning

### How versioning works

Before filex overwrites a file, it can **snapshot the current bytes** so you can
roll back. Snapshots are copied into the **same storage backend** under
`.versions/<node_id>/<version_n>`, and each is recorded as a `node_versions`
row (version number, size, etag). Where the driver supports server-side copy the
snapshot is a fast backend copy; otherwise filex streams the bytes
(read → write).

Only **files** are versioned. Directories are skipped, and so are symlinks a
storage does not follow ([Symlinks](STORAGE.md#symlinks)); a link inside a
`local` storage's folder is catalogued as what it points at, so a linked file
is versioned like any other. A snapshot
is also skipped when there is nothing to capture - a brand-new file with no live
content yet, or a row whose object isn't on the backend.

A snapshot is **not a catalogue entry**. It belongs to its `node_versions` row,
which is keyed by the file it versions, and the storage sync never walks into
`.versions/` (nor `.thumbs/`, nor `.filex-trash/`). Older versions did: a full
scan minted a hidden, system-owned row for every snapshot folder and file, and
those rows could later land in the trash - where purging a folder row deletes
its whole prefix, i.e. the version history. The next full sync after upgrading
drops every such row from the catalogue (never from the backend), and the sync's
delete pass never moves anything inside these trees into the trash.

⚠ That last case is a **silent** skip: if the catalogued path and the object on
the backend ever disagree, the guard finds nothing to snapshot and reports
success. Every shipped driver normalises the key it is handed, so the two agree
in practice - but a storage **plugin** that does not would lose history with no
error anywhere. Plugin authors: normalise, and see [PLUGINS.md](PLUGINS.md).

⚠⚠ **Until v0.34.0 that disagreement was not hypothetical: it happened to
every moved or renamed file.** `Store.MoveNode` did not update `storage_key`,
so the row went on naming the old path, and versioning prefers `storage_key`
over `path` - the snapshot stated `ErrNotFound`, reported "nothing to
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
are never scanned - every destructive write takes one, so scanning each would
multiply the scan load by the edit rate for bytes nobody can reach - which left
"overwrite the infected file with a clean one, then roll back" as a way to put
an infected file live. The restore is where that is closed; see
[PROTECTION.md](PROTECTION.md#restoring-is-a-write).

### Version retention

| Setting | Where | Default | Meaning |
|---|---|---|---|
| `versions.keep_n` | DB `settings` table, written by **Protection → Version retention** (`PATCH /api/admin/protection`, accepted range 0-1000) | **0** | How many versions of a file to keep. `0` means "not configured": the daily retention sweep is off and the snapshot path applies its compile-time safety trim of **20** instead. |

So a file's history is trimmed to the newest **20** snapshots out of the box,
and to `keep_n` when an operator sets one - the snapshot path honours the
setting inline, so a value **above** 20 really does keep more (it used to claw
every node back to 20 on the next snapshot, which made larger values
meaningless). A `keep_n > 0` additionally runs a **daily sweep** over every node
that has version rows, so lowering the number reaches files nobody is editing;
see [Protection → Version retention](PROTECTION.md#version-retention-versionskeep_n).

Trimming removes both the `node_versions` row and the backing `.versions/…`
object (best-effort per object).

A file that leaves the catalogue for good takes its whole history with it:
purged from the trash, removed by the storage sync after a delete outside
filex, or deleted when its bytes were already gone. Its snapshots are deleted
from the storage and its `.versions/<id>/` folder with them (best-effort; a
failure is logged and leaves an orphan). ⚠ Up to 0.49 nothing deleted them:
the `node_versions` rows went with the file's row, and the bytes stayed on the
storage where nothing could reach them. Those orphans are not cleaned up
retroactively; a `.versions/<id>/` folder whose id has no row any more can be
deleted by hand.

### What triggers a snapshot

**Every destructive write on the surfaces below.** Each of them calls the
pre-write guard, which snapshots what is about to be lost:

| Surface | Endpoint / entry point |
|---|---|
| Browser upload (single POST) | `POST /api/files/manager?action=upload` |
| Browser upload (staged / chunked) | `POST /api/files/upload/{id}/commit` |
| Public file-drop link | `POST /d/{token}` |
| Ticketed upload | `PUT`/`POST /u/{ticket}` |
| AI / REST write | `POST /api/ai/upload` |
| MCP `file_write`, `file_zip`, `file_unzip` | `/api/ai/mcp` |
| ShareX | `POST /api/sharex/upload` |
| Archive extract / add | `POST /api/files/archive/extract`, `/add`; MCP `archive_extract` and `POST /api/ai/archive/extract` |
| Text / code editor save | `POST /api/files/save-text` |
| OnlyOffice save-back | `POST /api/files/onlyoffice/callback` |
| WebDAV | `PUT` |
| S3 gateway | `PutObject`, `CompleteMultipartUpload`, `CopyObject` |

⚠⚠ **`SFTP`, `FTPS` and `NFS` are not in that table, and their absence is
real, not an omission in the writing.** Those three write through
`internal/protocolsync`, which does not call the pre-write guard, so a client
that replaces a file over SFTP, FTPS or NFS destroys the previous bytes with
**no snapshot taken and no error**. Everything else those surfaces share with
the rest of filex - the catalogue row, the search index, the realtime frame,
the antivirus scan, trash on delete - they do have. Versioning is the one gap.
If version history is what you are relying on, keep those three protocols out
of the write path, or check the file's history after the first overwrite rather
than assuming it.

**Renames and moves are not in that table because they never overwrite.** A
rename onto a name that is taken is refused (`409 NAME_TAKEN`); a move lands
beside it as `name-copy`. ⚠ Until the rename was guarded it replaced the file
that held the name with no snapshot, and the catalogue then dropped that file's
row - so the versions it already had were lost with it. A WebDAV `MOVE` sent
with `Overwrite: T` replaces the destination at the client's explicit request,
and deletes it into the trash first (the protocol's delete-before-move), so it
stays restorable from there.

A snapshot is taken **only when** there is something to lose: the path already
holds a catalogued **file**. A brand-new file, a directory, and filex's own
internal trees (`.versions/`, `.thumbs/`, `.filex-trash/`, `.filex-drafts/`,
`.keepdir` markers) cost one indexed lookup and nothing else. So a draft,
which the editor saves into every few seconds, collects no version history;
once it is saved into its folder, its later saves are versioned like any
other file's.

> ⚠ **This used to be untrue, and the untrue version was written down.** Until
> the pre-write guard landed, the only wired trigger really was the text-editor
> save, while this page and the `versioning` package doc both described a
> guarantee that covered uploads and archive extraction. The practical effect
> was that re-uploading a file over itself destroyed the old bytes with nothing
> kept, while editing the *same* file in the browser kept a version - so the
> feature looked like it worked right up until the moment you needed it.

**If the snapshot cannot be taken, the write is refused.** That is the whole
point: losing version history is not a reason to also lose the file. The
surfaces answer **503** with `"code": "SNAPSHOT_FAILED"` and the existing file
is left untouched.

Two batch surfaces differ, deliberately. Archive extract and the AI/MCP `unzip`
tool **skip just the refused member and keep going**, reporting a `refused`
count alongside `count`/`extracted` - a guard refusal is transient and
system-caused, unlike the permanent, user-caused skips in the same loop (a
zip-slip entry, a file/folder kind clash). If **every** member was refused and
nothing landed, they answer 503 `SNAPSHOT_FAILED` with the count rather than a
misleading `200 {"count":0}`.

Turning it off: [`FILEX_VERSIONS_ON_OVERWRITE=0`](CONFIGURATION.md#versioning-on-overwrite)
makes the guard a no-op - writes then behave exactly as they did before it
existed. `FILEX_VERSIONS_FAIL_OPEN=1` keeps the snapshot attempt but lets a
failed one through instead of refusing the write. Both log a WARN at boot, so a
non-default state is visible without reading the config.

`save-text` has its own guardrails:

- **Body:** `{ "path": "<adapter>://<relative/path>", "content": "…" }`.
- **Extension whitelist:** only text/code types round-trip here - `txt`, `md`,
  `json`, `jsonc`, `yaml`/`yml`, `toml`, `ini`, `env`, `csv`, `xml`, `svg`,
  `html`, CSS/SCSS/LESS, JS/TS/JSX/Vue/Svelte, and common source languages
  (`go`, `py`, `php`, `rb`, `rs`, `java`, `c`/`cpp`/`h`, `sh`, `sql`, …), draw.io
  diagrams (`drawio`, `dio` - their XML, saved by the draw.io viewer), plus
  special filenames like `Dockerfile`, `Makefile`, `.gitignore`,
  `.editorconfig`. Anything else returns **415 `extension not allowed for
  save-text`** - binary/office formats have dedicated edit channels (e.g.
  OnlyOffice).
- **Permission:** requires **≥editor** on the file ([RBAC](RBAC.md)) → **403**
  otherwise.
- **Read-only mount:** returns **403 `storage is read-only`**.

### Versioning endpoints

**User (authenticated session/token):**

| Method & path | Body / query | Permission | Notes |
|---|---|---|---|
| `GET /api/files/versions` | `?node_id=N` | **≥viewer** | Lists that node's snapshots, **newest first** (version number, size, etag, created). |
| `POST /api/files/versions/snapshot` | `{ "node_id": N }` | **≥editor** | Records the current content as a new version on demand - the "take a version now" button in the details panel's **Activity** tab, which is where a file's history and its comments live. Writes an object into the node's storage. |
| `POST /api/files/versions/restore` | `{ "node_id": N, "version_id": V, "snapshot_current": true }` | **≥editor** | Copies version `V` back over the live file. |
| `POST /api/files/save-text` | `{ "path": "adapter://rel", "content": "…" }` | **≥editor** | Saves text and snapshots the previous content first (see above). |

**Admin only:**

| Method & path | Notes |
|---|---|
| `DELETE /api/admin/versions/{id}` | Hard-delete one version row **and** its backing `.versions/…` object. |

**Agents** (since 0.50): the MCP tools `file_versions`, `file_version_restore`
and `file_snapshot`, and their REST twins `GET /api/ai/versions?path=`,
`POST /api/ai/versions/restore` and `POST /api/ai/versions/snapshot`, take the
file's **path** and run the routes above with the node it names - the same
permissions. From there an encrypted folder's key file is never rolled back
or snapshotted (`403 RESERVED_NAME`): rolling it back would bring back the key
a password change retired ([MCP.md](MCP.md#copy-apps-operations-trash-versions-archives-links)).

⚠ **The permission column is load-bearing, and it is new.** Before the release
this note ships in, these
routes had no ownership or ACL check of any kind: a `viewer`-role account could
`POST /restore` and overwrite the live bytes of any file whose node id it could
name, on **single-tenant installs too**. Restoring and snapshotting are writes,
so they now need **editor**, exactly like `save-text` beside them; listing stays
at **viewer**, because somebody who can read the file is not being told its
history is a secret.

⚠ Every one of these takes a raw `node_id`, so the node is resolved and
authorized before anything happens: existence (and **not trashed** - a trashed
row's live path is its `storage_key`, so restoring onto one wrote bytes to a
path the catalogue says holds nothing) → tenancy → the token's `root:`
confinement → RBAC. The first three answer **404**, identical to a node that
never existed, so the endpoint cannot be used to discover which ids are real;
only the RBAC refusal is **403 `insufficient permission`**.

⚠ `snapshot_current` is honoured only when the pre-write guard is switched off
(`FILEX_VERSIONS_ON_OVERWRITE=0`). With the guard on - the default - a restore
already snapshots the bytes it is about to replace, and doing it twice would
record identical content and spend a retention slot on the duplicate. See
[Restoring is a write](#restoring-is-a-write) below.

### Restoring is a write

A restore replaces the live bytes at an unchanged path, so it is treated as one:

- **It goes through the pre-write guard.** Before the copy, the bytes that are
  about to be destroyed are snapshotted, and a snapshot that cannot be taken
  refuses the restore with **503 `SNAPSHOT_FAILED`** instead of overwriting them
  unrecoverably - the same contract every other write surface has. ⚠ Restore was
  the one write in filex that skipped this until the release this note ships in, so rolling back twice in
  a row destroyed whatever was live in between with nothing recorded.
- **It emits `file.updated`.** Restores used to change a file's bytes and tell no
  webhook subscriber; they now go through the same post-write gate as an upload.
- **It enqueues a virus scan** of the restored file - see above.
- **It needs `≥editor`** on the file, like every other write.

⚠ Because the guard already snapshots the outgoing content, `snapshot_current`
only does work when the guard is switched off
([`FILEX_VERSIONS_ON_OVERWRITE=0`](CONFIGURATION.md#versioning-on-overwrite)).
With the guard on, honouring both would record identical bytes twice and spend a
retention slot on the duplicate.

### Versioning - failure modes & troubleshooting

**Version history is empty even though I've edited the file.**
Check which surface wrote it. **SFTP, FTPS and NFS take no snapshot at all**
(see the table above) - that is the likeliest answer, and it fails silently. The
other three: the guard is switched off
([`FILEX_VERSIONS_ON_OVERWRITE=0`](CONFIGURATION.md#versioning-on-overwrite),
which logs a WARN at boot); directories and unfollowed symlinks are **never** versioned;
and the **first** save of a new file has no prior content to snapshot.

**A version I wanted is gone / "restore" can't find it.**
Retention keeps only the newest **20** versions per file - or `versions.keep_n`
when an operator has set one - and older snapshots are trimmed after each new
save. An admin `DELETE /api/admin/versions/{id}` also removes one permanently.
Once trimmed/deleted, a version is unrecoverable.

**`version belongs to a different node`.**
The `version_id` in a restore request doesn't belong to the `node_id` you sent.
Re-list with `GET /api/files/versions?node_id=N` and use an ID from that node.

**I saved the file, but no new version appeared.**
`save-text` treats snapshotting as **best-effort**: if the pre-write snapshot
fails (storage or DB hiccup) filex logs `save-text: snapshot failed (continuing
with write)` and **still saves your edit** - you keep the new content, but that
one pre-edit state wasn't captured. Check the server log.

**Can't save / snapshot on a particular mount.**
The storage is **read-only** (403 `storage is read-only`) - no writes, so no
snapshots either. Restore also writes the live file and needs a writable driver.

---

## See also

- [STORAGE.md](STORAGE.md) - mounts, adapters, read-only mounts, quota
- [RBAC.md](RBAC.md) - viewer / editor / admin levels and confinement that gate
  the trash list, restore, and save-text
- [CONFIGURATION.md](CONFIGURATION.md) - global config / env reference
- [SSO.md](SSO.md) - sign-in and account roles
