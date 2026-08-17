# Quotas — what counts, and when

Quota is **per user**. `users.quota_bytes` is the ceiling (`0` = unlimited) and
`users.usage_bytes` is what that account currently stores. The admin surface is
`GET|POST /api/admin/users/{id}/quota` and the caller's own snapshot is
`GET /api/files/quota/me` — see [Backend → Admin: quota](BACKEND.md#admin-quota).

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
| **Overwrite by another user** | old owner `-=` old size, writer `+=` new size, owner changes | the bytes on the disk are the ones the writer just put there |
| **Overwrite by no one** (storage scanner sees the file changed on the backend) | delta applied, owner **unchanged** | nobody uploaded it; the existing owner still holds it |
| **Trash** (soft delete) | **nothing** | the bytes are still on the storage — deleting does not free space, emptying the trash does |
| **Restore** | **nothing** | they never stopped counting |
| **Move / rename** (including between folders) | **nothing** | same row, same owner, same bytes |
| **Move between storages** | **nothing** if the row moves; a copy-then-delete nets to zero | the account stores it once either way |
| **Copy** | counted **again** | a copy really is a second set of bytes on the disk |
| **Purge / permanent delete** | subtracted | the bytes are gone |

The purge is the **only** release point. That is what makes "delete does not
free space" true rather than aspirational, and it is why a user cannot get
under their ceiling by filling the trash.

### Who the bytes belong to

Attribution is resolved in this order:

1. an **explicit owner** on the request context (`quotastore.WithOwner`) — used
   where the writer is not the account being billed:
   - the **public file-drop link** bills the link's creator. The uploader is
     anonymous by design, but the files land in the creator's storage, so they
     are the creator's bytes. Without this the drop would be the one write
     surface with no ceiling at all;
   - the **async copy worker** bills the owner of the **source** file. It runs
     on a server-lifetime context long after the request is gone.
2. the **authenticated account** — every logged-in surface, including WebDAV
   Basic auth and API-token writes (a token is bound to an account).
3. **nobody**. A file the storage scanner discovered was not uploaded through
   filex, so it stays unowned and uncounted — until a user overwrites it, at
   which point it becomes theirs.

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
that overrides `CreateNode`, `UpdateNodeMeta` and `HardDeleteNode`. Every write
surface — browser upload, staged upload, staged ingest, WebDAV `PUT`, the public
drop, ShareX, the AI/REST API, save-text, archive extract, copy — reaches it
through the store, so none of them carries quota code and a write path added
next month is counted on the day it is written.

> ⚠ **History.** Until v0.20 `quota.AddUsage` and `Store.SetNodeOwner` had **no
> callers anywhere in the tree**. `usage_bytes` was never incremented,
> `GetNodeOwner` always returned `nil`, and so the `SubUsage` at purge — the
> only place it was called — could never run either. Nothing was counted, so
> nothing was ever refused: a user with a 1 MB quota could upload 10 GB. The
> ceiling, the admin page and the "trashed bytes still count" rule were all
> describing behaviour that did not exist.
