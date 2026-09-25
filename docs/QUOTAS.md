# Quotas — what counts, and when

Quota is **per user**. `users.quota_bytes` is the ceiling (`0` = unlimited) and
`users.usage_bytes` is what that account currently stores. The admin surface is
`GET|POST /api/admin/users/{id}/quota` and the caller's own snapshot is
`GET /api/files/quota/me` — see [Backend → Admin: quota](BACKEND.md#admin-quota).

⚠ A per-user total is not "how full is this drive", and the two must not be
printed under the same label. That second question has its own endpoint,
`GET /api/files/quota/storages`: per-**storage** usage, filtered to the storages
the caller may open, so a non-administrator's storage card shows a real figure
instead of a per-user sum wearing a drive's name. It is a property of the drive
— item-level grants do not narrow it, and a drive you cannot open is not
reported at all. Also in [Backend → Admin: quota](BACKEND.md#admin-quota).

The storage line under the explorer's navigation (web and desktop app) and the
storage chip in the admin top bar print one or the other by a single rule
(`packages/core/src/lib/storageLine.ts`): a person **with** a quota sees their
own usage against it, because that ceiling is what refuses their next upload;
a person **without** one sees the size of the drives they can open, the figure
the Home cards print. Without a quota the per-user total is only what that
person uploaded — a file a storage sync discovered has no owner — so on a drive
filled by a sync it is a small fraction of the drive.

This page is about the other half: **how `usage_bytes` gets its value**, which
is the part that has to be exactly right or the ceiling is decoration.

## The rule

One identity holds the whole design together:

```
usage_bytes(u) == SUM(nodes.size) WHERE owner_id = u AND type = 'file'
                  — trashed rows INCLUDED
```

`nodes.owner_id` is stamped when the bytes land, and everything else follows
from that identity:

| Event | What happens to usage | Why |
|---|---|---|
| **Bytes land** (any write path) | owner stamped, size added | the account now stores them |
| **Overwrite** | the **delta** is applied | one file on disk is one file's worth of quota |
| **Overwrite by another user** | the **owner** takes the delta; the owner does **not** change | an overwrite changes the bytes, not whose file it is. Who did the writing is recorded in `last_actor_id` and nowhere else |
| **Overwrite of an unowned (SYSTEM) row** | the writer **adopts** it and its full size is added | "nobody's" is not "somebody else's" — and without adoption a scanner-found file could be filled with gigabytes no quota ever counted |
| **Overwrite by no one** (storage scanner sees the file changed on the backend) | delta applied, owner **unchanged** | nobody uploaded it; the existing owner still holds it |
| **Trash** (soft delete) | **nothing** | the bytes are still on the storage — deleting does not free space, emptying the trash does |
| **Restore** | **nothing** | they never stopped counting |
| **Move / rename** (including between folders) | **nothing** | same row, same owner, same bytes |
| **Move between storages** | **nothing** if the row moves; a copy-then-delete nets to zero | the account stores it once either way |
| **Copy** | counted **again** | a copy really is a second set of bytes on the disk |
| **Purge / permanent delete** | subtracted | the bytes are gone |
| **A symlink the storage may not follow** | **not counted** | since v0.43.0 an out-of-root link is typed `symlink` rather than catalogued as a file, so it is not indexed, scanned, versioned or counted; switching *Follow symlinks that leave this folder* on for that storage brings its target's bytes back into the count ([STORAGE.md](STORAGE.md#symlinks)) |

The purge is the **only** release point. That is what makes "delete does not
free space" true rather than aspirational, and it is why a user cannot get
under their ceiling by filling the trash.

### Who the bytes belong to

Ownership is a fact on the row rather than a by-product of the last write.
Migration `00038` split it in two: `nodes.owner_id` is **who put the thing
here**, `nodes.last_actor_id` is **who touched it last**, and they move
independently — an edit, an overwrite, a move or a restore changes the actor and
leaves the owner alone. The listing's **Owner** column and the filter row's
**Owner** chip read the first one; quota reads it too.

⚠ That changed a quota behaviour, deliberately. An overwrite by another user
used to *move* the bytes to the writer; a file that already has an owner now
keeps it, so user B can grow the total user A is billed for by overwriting A's
file with a bigger one. B needs write access to A's file to do it, and the
alternative — ownership silently changing hands every time somebody edits a
shared document — makes the Owner column unable to answer the only question it
is asked.

The acting identity is resolved in this order:

1. an **explicit identity** on the context — `quotastore.WithOwner` for a
   surface with no logged-in user whose bytes still belong to someone, and
   `WithActor` for work running long after the request that asked for it:
   - the **public file-drop link** bills the link's creator. The uploader is
     anonymous by design, but the files land in the creator's storage, so they
     are the creator's bytes. Without this the drop would be the one write
     surface with no ceiling at all. The row is also marked `external_upload`
     and gets **no actor**: writing "last changed by Ada" onto a file Ada has
     never seen is a lie the UI cannot see through;
   - an **upload ticket** bills its minter. The redeem carries no credential, so
     the write runs as the person who minted the ticket: their grants decide,
     their quota is charged, the file lands owned by them;
   - the **async copy/move worker** acts as the person who asked, carried in
     `pending_ops.actor_id`. A **copy** is a new file, so the copier owns it (a
     queue row written before that column existed names nobody and falls back to
     the source file's owner, rather than leaving a second set of real bytes
     uncounted); a **move** is the same file, so it changes only the actor.
2. the **authenticated account** — every logged-in surface, including WebDAV,
   FTPS, SFTP, NFS and the S3 gateway (the protocol servers stamp the principal
   on the connection context) and every API-token write (a token is bound to an
   account).
3. **nobody** — SYSTEM. A file the storage scanner discovered was not put there
   by anyone, so it stays unowned and uncounted until a user writes it, at which
   point they adopt it. ⚠ That holds however the scan was *started*: the admin
   **Sync now** button hands its own request context to the walk, and until the
   identity was stripped inside the scan, one click stamped every object in the
   bucket as that admin's — and billed the lot to them. Finding a file is not
   putting it there.

### Reconciling

`POST /api/admin/users/{id}/quota/recompute` rebuilds `usage_bytes` from the
node rows using exactly the identity above — **trashed rows included**. Run it
if you have restored a database from an inconsistent backup, or after a bulk
import that bypassed filex.

> ⚠ Before v0.20 the reconciler filtered `deleted_at IS NULL`, so a recompute
> silently forgave every trashed byte and the release at purge then subtracted
> them a second time (clamped at zero, so the drift never showed up as an
> error). Fixed; the two now agree by construction.

## Where it is enforced

| Guard | Where | Response |
|---|---|---|
| **Ceiling** | staged upload `begin` | `413` `{"code":"QUOTA_EXCEEDED"}` |
| **Staging disk headroom** | staged upload `begin` | `507` `{"code":"NO_DISK_SPACE"}` |

The ceiling is checked against `usage_bytes` **plus the bytes already reserved
by this user's open staged uploads**. Reserving at `begin` rather than settling
at commit is deliberate: an upload that never commits would otherwise be
invisible to the ceiling and a user could stage past it. The reservation is
derived from the open rows themselves, so it is released by the row leaving the
open set — commit, abort, or sweep — and can never drift from what it describes.

Both guards increment `filex_guard_refusals_total{guard="quota"|"disk"}` and
write a log line naming the numbers involved. See [Metrics](METRICS.md).

## Where it is implemented

All of it lives in **one** place: `internal/quotastore`, a `db.Store` decorator
over `CreateNode`, `UpdateNodeMeta` and `HardDeleteNode` (the three that move
bytes) plus `MoveNode`, `RestoreNode` and `RestoreNodeAt` (which move no bytes
and only record who acted). Every write surface — browser upload, staged upload,
staged ingest, WebDAV `PUT`, the public drop, ShareX, the AI/REST API,
save-text, archive extract, copy — reaches it through the store, so none of them
carries quota code and a write path added next month is counted on the day it is
written.

That is also why the attribution itself lives here: the store is the one thing
every write surface has in common, so `owner_id`, `last_actor_id` and
`external_upload` are written in this package and nowhere else. `CreateNode`
stamps them **onto the model before the INSERT** rather than as a follow-up
`UPDATE` — an archive extract, a desktop sync or a scanner walk creates rows in
bulk, and a second round trip per row is a cost this feature does not need to
have.

> ⚠ **History.** Until v0.20 `quota.AddUsage` and `Store.SetNodeOwner` had **no
> callers anywhere in the tree**. `usage_bytes` was never incremented,
> `GetNodeOwner` always returned `nil`, and so the `SubUsage` at purge — the
> only place it was called — could never run either. Nothing was counted, so
> nothing was ever refused: a user with a 1 MB quota could upload 10 GB. The
> ceiling, the admin page and the "trashed bytes still count" rule were all
> describing behaviour that did not exist.
