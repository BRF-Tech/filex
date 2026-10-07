# Storage replication

Replication keeps a **second, backup-only copy** of a storage's files. Every
write, delete, move and copy that lands on a storage is **fanned out** to a
linked backup sink in the background, and the files the storage already held
when it was linked are copied there by an [initial copy](#initial-copy), so
you always have an off-site mirror you can fall back to if the primary backend
has a bad day.

This is an **advanced feature**. A normal filex install doesn't need it -
reach for it when you want a warm backup of a bucket/disk on a *different*
provider (e.g. an S3 bucket mirrored to a second region, or a local disk mirrored
to a Hetzner Storage Box over SMB or SFTP).

> ⚠ **Before 0.53 replication never ran.** A storage linked to a target was
> saved as linked, and nothing was ever sent to the target: no copy, no
> failure, no notification (GitHub Discussion #91). From 0.53 a link takes
> effect at once, and after the upgrade every storage that was already linked
> gets its [initial copy](#initial-copy) once.

- [How it works](#how-it-works) - [targets vs storages](#targets-vs-storages) · [modes](#per-path-modes)
- [Setup](#setup) - [create a target](#1-create-a-replication-target) · [link a storage](#2-link-a-storage-to-the-target) · [rules](#3-rules---per-path-modes)
- [Initial copy](#initial-copy)
- [The storage's folder on the target](#the-storages-folder-on-the-target) · [filex's own folders](#filexs-own-folders)
- [Reconcile & repair](#reconcile--repair)
- [Status report & notifications](#status-report--notifications)
- [Admin endpoints](#admin-endpoints)
- [Failure modes & troubleshooting](#failure-modes--troubleshooting)
- [See also](#see-also)

---

## How it works

```
  write / delete / move / copy
            │
            ▼
   ┌─────────────────┐   synchronous    ┌──────────────┐
   │  your Storage   │ ───────────────► │   primary    │  (source of truth,
   │ (wrapper driver)│                  │   backend    │   shown in explorer)
   └────────┬────────┘                  └──────────────┘
            │  async fan-out (background goroutine, per-path rule)
            ▼
   ┌──────────────────────┐
   │  ReplicationTarget   │  (backup-only sink - never shown, never read from
   │  (backup sink)       │   except as a fallback when the primary is down)
   └──────────────────────┘
```

The server builds one driver per storage and hands it to everything that
reads or writes the storage: uploads, saves, copies, moves, deletes, the
trash, versions, thumbnails and the SFTP, FTP, NFS and S3 doors. When the
storage is linked to a replication target that exists and is **enabled**, that
driver is the **replication wrapper** around the storage's own driver; when it
is not linked, or its target is switched off, it is the storage's own driver.

The write to the **primary** happens synchronously - the user's request only
returns once the primary has the file. The copy to the backup target then
happens **asynchronously** in the background, by reading the object back from
the primary and writing it to the target, with the file's modification time
when the target can keep one (local, SFTP, SMB). Reads normally come from the
primary; if a primary **read** or **stat** fails because the primary is down
(a timeout, a refused connection), the wrapper transparently falls back to the
replica so individual downloads keep working during a primary outage.

- A file the primary simply **does not have** is never looked for on the
  backup: in `append_only` the backup keeps every deleted file, and a fallback
  would serve deleted files again.
- Directory *listings* always come from the primary - they are never served
  from the backup, so a listing can't show a half-replicated view.
- The wrapper keeps what the storage's own driver can do: a disk keeps the
  modification times clients send, a bucket keeps its multipart uploads (the
  fan-out runs when the upload is assembled) and its download links. A
  browser-direct upload link (presigned PUT) is refused on a replicated
  storage, because the file would skip the fan-out; filex's own clients never
  ask for one.
- One file's copy to the target may take five minutes plus the time the file
  needs at 256 KiB/s, so a large file on a slow link is not cut off.
- Every storage writes into a **folder of its own** on the target
  (`<folder>/docs/a.txt`), so storages sharing a target never meet
  ([below](#the-storages-folder-on-the-target)).
- **filex's own folders** - the trash, version history, thumbnails, the
  desktop's open-with copies, the drafts - never reach the target
  ([below](#filexs-own-folders)).

### Targets vs storages

filex draws a hard line between the two:

| | **Storage** | **ReplicationTarget** |
|---|---|---|
| Shows in the Storages list / file explorer | **yes** - a named top-level folder | **never** |
| Written to directly by users | yes | **no** - backup only |
| Read from | yes (source of truth) | only as a **fallback** when the primary is down |
| Role | primary backend | backup sink for one or more storages |
| Configured at | **Storages** page | **Replication** page |

A **ReplicationTarget** is just a backend definition (driver + config) with no
mount point. A regular **Storage** points at one via its `replica_target_id`
field; once linked, the storage is transparently wrapped by a *replicated
driver* that fans its writes out to the target. The target itself is invisible -
it will never appear as a folder and users can't browse it. Several storages
may share one target: each writes into a
[folder of its own](#the-storages-folder-on-the-target) there.

Both a storage and a target use the **same adapters** (`local` · `s3` · `sftp` ·
`webdav` · `ftp` · `smb`) and the **same `config` shape** - the target dialog
renders from the same driver descriptors the storage form does, so a driver
cannot be offered for one and missing from the other. See
[STORAGE.md → Adapters](STORAGE.md#adapters) for every adapter's config keys.

> **Pick a *different* backend for the target.** Mirroring an S3 bucket to
> another bucket on the same account, or a disk to a folder on the same disk,
> defeats the point - a provider/hardware failure would take out both copies.

### Per-path modes

Not every path has to be mirrored the same way. A **rule engine** maps a
**path pattern → mode**, so you can (for example) fully mirror `documents/**`
but never propagate deletes under `archive/**`. There are three modes:

| Mode | Creates / updates | Deletes | Moves / copies / mkdir |
|---|---|---|---|
| **`mirror`** (default) | replicated | replicated | replicated |
| **`append_only`** | replicated | **not** replicated - the file **stays** on the backup | replicated |
| **`skip`** | not replicated | not replicated | not replicated |

- **`mirror`** - the backup tracks the primary exactly, deletions included.
- **`append_only`** - the backup only ever *grows*: new/changed files are
  copied, but a delete on the primary leaves the backup copy in place. Good for
  a tamper-resistant archive where you never want a delete to erase the backup.
- **`skip`** - the path is excluded from replication entirely, the
  [initial copy](#initial-copy) included.

Before any rule, [filex's own folders](#filexs-own-folders) are `skip`: no
rule can bring them back.

Rules are evaluated by **priority ascending** - the **first enabled rule whose
pattern matches wins**. If no rule matches (or there are no rules), the storage
falls back to the **default mode** from settings (`default_mode`, itself
defaulting to `mirror`). Rules apply to every replicating storage.

**Patterns** are globs matched against the path inside the storage (forward
slashes; a leading `/` is not part of the match, so `docs/**` matches
`/docs/a.txt` and `docs/a.txt` alike):

| Pattern | Matches |
|---|---|
| `*.tmp` | `foo.tmp` |
| `documents/sensitive/*` | `documents/sensitive/report.pdf` (one segment) |
| `documents/temp/**` | `documents/temp/a/b.txt` (any depth) |
| `documents/**/cache/*` | `documents/x/y/cache/c.bin` |

`*` matches within a single path segment; `**` spans multiple segments.

---

## Setup

Three steps: create a target, link a storage to it, and (optionally) add rules.
The **Replication** admin page walks you through all three; the equivalent API
calls are shown below for automation. All endpoints require an **admin** session
or an admin-scoped API token, and on a multi-tenant install the platform
operator's: a target is an instance-wide sink.

### 1. Create a replication target

`POST /api/admin/replication-targets`. The body is a backend definition - same
`driver` + `config` you'd use for a storage, minus any mount/sync fields:

```bash
curl -X POST https://files.example.com/api/admin/replication-targets \
  -H 'Content-Type: application/json' -b cookies.txt \
  -d '{
    "name": "offsite-backup",
    "driver": "s3",
    "config": { "bucket": "backup-bucket", "prefix": "filex-mirror",
                "region": "auto", "endpoint": "https://s3.backup.example.com",
                "path_style": true, "access_key": "…", "secret_key": "…" },
    "mode": "async",
    "enabled": true
  }'
```

| Field | Type | Default | Meaning |
|---|---|---|---|
| `name` | string | - | Display name for the target. Required. |
| `driver` | string | - | `local` · `s3` · `sftp` · `webdav` · `ftp` · `smb`. Required. |
| `config` | object | `{}` | Per-adapter settings - see [STORAGE.md → Adapters](STORAGE.md#adapters). |
| `mode` | string | `async` | Fan-out mode. `async` (default) fans writes out in the background. |
| `enabled` | bool | `true` | A disabled target is not written to: its storages are served unwrapped. |

**Credentials are never shown back.** Every read of a target - the
Replication page, `GET /api/admin/replication-targets[/{id}]`, an admin API
key on `/api/ai/admin/...` and the `admin_replication_targets_*` MCP tools -
answers with the configuration's credentials as `***`: the fields the
driver's descriptor marks secret (an S3 access and secret key, an SMB, SFTP,
WebDAV or FTP password, an SFTP private key) and any key that is a credential
by name (`password`, `secret`, `token`, `private_key`, …). A `PATCH` that sends
`***` back keeps the stored value, so a configuration can be edited as it was
shown; a new value replaces it. ⚠ Only while the driver and the address
(`host`, `endpoint`, `url`, …) stay the same: a target pointed somewhere new
needs its credentials typed again (**400** `SECRET_NEEDED`), so a saved
password is never sent to a server of somebody's choosing. Storages follow
the same rule ([STORAGE.md](STORAGE.md#editing-a-storage-afterwards)).

> The target's `mode` (`async`/`sync`) is **not** the same thing as a per-path
> [replica mode](#per-path-modes) (`mirror`/`append_only`/`skip`). The target
> mode governs *when* the copy happens; today the engine always fans out
> **asynchronously**, so the user's write never waits on the backup.

`PATCH /api/admin/replication-targets/{id}` changes a target; the fields the
body leaves out keep their values (`{"enabled": false}` switches it off and
nothing else). Every storage linked to it is rebuilt at once. When the
**driver or configuration** changes, or the target is **switched back on**,
their [initial copies](#initial-copy) begin again: the new place is empty, or
the changes made while it was off went nowhere (files the target already holds
are left alone, so a run against the same place mostly compares).

A target that **cannot be opened** - a configuration its driver refuses, a
plugin driver that is not running - does not take its storages down: they keep
working, and every change that would reach the backup is recorded as a
`REPLICA_UNAVAILABLE` failure with the reason, until the target is fixed and
*Fix all* replays them.

### 2. Link a storage to the target

A target does nothing until a storage points at it. On the **Replication** page,
pick the target on the storage's row; via the API, `PATCH` the storage and set
`replica_target_id`:

```bash
curl -X PATCH https://files.example.com/api/admin/storages/7 \
  -H 'Content-Type: application/json' -b cookies.txt \
  -d '{ "replica_target_id": 3 }'
```

From that point on - no restart - every mutation on storage `7` is mirrored to
target `3`, inside the storage's [own folder](#the-storages-folder-on-the-target)
there, according to the [rules](#3-rules---per-path-modes), and the files it
already holds are copied by its [initial copy](#initial-copy). To **stop**
replicating, clear the link (`"replica_target_id": null`, or `-` on the page):
the storage's next write stays home. Deleting a target automatically unlinks
every storage that pointed at it. A link to a target id that does not exist is
refused (**400**), and on a multi-tenant install only the platform operator
may change a storage's link (**403** `supertenant_only` otherwise).

Linking does not stop or restart the storage's scan: the scan walks the same
storage either way.

> The legacy `role` and `replica_of_id` columns on a storage are retained only
> for backwards compatibility with old (v0.1.16) deployments. The current model
> is the single `replica_target_id` foreign key - ignore the legacy fields.

### 3. Rules - per-path modes

With no rules, everything replicates in the storage's default mode (`mirror`).
Add rules only where you want different behavior. `POST /api/admin/replica/rules`:

```bash
# Never propagate deletes under archive/ - keep the backup append-only.
curl -X POST https://files.example.com/api/admin/replica/rules \
  -H 'Content-Type: application/json' -b cookies.txt \
  -d '{ "path_pattern": "archive/**", "mode": "append_only",
        "priority": 10, "enabled": true, "description": "keep deleted archives" }'

# Exclude scratch files entirely.
curl -X POST https://files.example.com/api/admin/replica/rules \
  -H 'Content-Type: application/json' -b cookies.txt \
  -d '{ "path_pattern": "**/*.tmp", "mode": "skip", "priority": 20, "enabled": true }'
```

| Field | Type | Meaning |
|---|---|---|
| `path_pattern` | string | Glob to match (see [patterns](#per-path-modes)). |
| `mode` | string | `mirror` · `append_only` · `skip`. |
| `priority` | int | Lower wins. First enabled matching rule decides the mode. |
| `enabled` | bool | Disabled rules are skipped during matching. |
| `description` | string | Free-text note (optional). |

The catch-all default mode lives in **settings**, not in a rule:

```bash
curl -X PATCH https://files.example.com/api/admin/replica/settings \
  -H 'Content-Type: application/json' -b cookies.txt \
  -d '{ "default_mode": "mirror", "report_enabled": true, "report_cron": "0 */6 * * *" }'
```

Rule and settings changes take effect immediately - the engine reloads its
cache after every create/update/delete.

---

## Initial copy

The fan-out only sees changes made **after** a storage is linked. What the
storage already held is copied by its **initial copy**, which starts on its
own when:

- a storage is linked to an enabled target (or created linked);
- a storage is relinked to another target (a new copy, to the new one);
- the target's driver or configuration changes, or it is switched back on
  (the copy begins again);
- filex starts after the upgrade to 0.53 and a storage has been linked since
  before: the copy runs **once** for every link that never had one. A link
  whose copy finished is not copied again at the next start.

It runs in the background from the persistent [queue](CONFIGURATION.md#queue)
(op type `replica_initial_copy`, the lowest priority), in **slices** of about
45 seconds. Each slice walks part of the storage and queues the next one, and
writes its place down every few files, so a restart - or a second filex
instance on the same database - goes on from the last file it finished; one
slice at a time holds a storage's copy (a claim that lapses if its worker
dies).

What each file gets:

| | Counted as |
|---|---|
| under a `skip` rule | **left out** - not copied |
| the target already has it: the same size and the same modification time (within two seconds; on an object store, which keeps no settable time, a copy not older than the file) | **already there** - not sent again |
| anything else | **copied**, with its modification time when the target can keep one |
| the copy failed | **failed** - a row in the failures list (op `write`), and the copy goes on |

It walks the storage twice: once to **count** the files, then to **copy**. The
walk holds one folder's listing per level of depth, never the storage, so a
storage of millions of files costs the memory of its largest folder.
[filex's own folders](#filexs-own-folders) are not entered at all, and not
counted.

When **ten files in a row fail and the target does not answer** (a cheap
question to its root fails), the failures were not the files' fault: the copy
rewinds to before them, **waits** (phase `waiting`, the reason in
`last_error`, one `replica_fail` notification) and resumes on its own after
ten minutes. When the target answers, those failures stay in the list as
failures of the files themselves and the copy goes on.

The **Replication** page shows each linked storage's copy on its row: the
phase (queued, counting, copying, waiting, done), *N of M files done* with a
bar, and what that is made of - copied (and how much), already on the target,
left out by a rule, failed. A finished or waiting copy can be started again
with **Run again** (`POST /api/admin/replica/initial-copies/{storage_id}/restart`):
every file is looked at anew and the ones the target already holds are left
alone.

---

## The storage's folder on the target

A target can be shared by several storages, and a path is relative to its
storage: two storages each holding `rapor.docx` would overwrite each other on
a target written at its root. So every storage writes inside a **folder of
its own** on the target - its writes, its deletes, its moves, the initial
copy, the read fallback, *Fix all* and the "already there" comparison all
work inside it.

- The folder is chosen **once**, when the storage is first linked to the
  target: its name, made safe for every backend - letters and digits (any
  script) and ` `, `-`, `_`, `.` stay, `/ \ : * ? " < > |` and control
  characters become `-`, the ends lose dots, spaces and dashes, at most 64
  characters; a name Windows reserves (`CON`, `LPT1`, …) gets the storage's
  id, and so does an empty one (`storage-<id>`).
- Two storages on one target never share a folder, compared without case (an
  SMB share would merge `Arşiv` and `arşiv`): the second gets its storage id
  appended (`arşiv-12`).
- **Renaming the storage does not move it** - a backup that followed every
  rename would leave the old folder behind, full. A target switched off and
  on keeps it too. Unlinking the storage, or linking it to another target,
  lets it go.
- Changing it is its own operation:
  `PUT /api/admin/replica/links/{storage_id}` with `{"folder": "…"}` (made
  safe the same way; **409** when another storage on the target uses it). The
  storage's driver is rebuilt and its initial copy begins again in the new
  folder; **the old folder is left on the target as it is** - move or delete
  it there yourself.
- `GET /api/admin/replica/links` lists every linked storage's folder; the
  Replication page shows it on the storage's row ("Folder on the target").

## filex's own folders

filex keeps its own machinery inside a storage: the trash (`.filex-trash`),
version history (`.versions`), legacy thumbnails (`.thumbs`), the desktop
app's open-with working copies (`.filex-open`) and the drafts
(`.filex-drafts`), at any depth. They are filex's, tied to rows in its
database - a backup of them restores nothing a person could open - so
replication **leaves them out**: they are `skip` before every rule, and no
rule can bring them back. Neither the live fan-out nor the initial copy
writes them.

What that means for a file going through them:

| On the primary | On the backup |
|---|---|
| a file is deleted (moved to the trash) | a **delete** - in `mirror`; `append_only` keeps it |
| it is restored from the trash | a **new file** (or folder), copied from the primary |
| the trash is emptied | nothing |
| a version is restored over the file | the file is **written** again |

The empty-folder marker (`.keepdir`) and an encrypted folder's key file
(`.filex-e2e.json`) are not in that list: they belong to the folders they are
in, and an encrypted folder cannot be decrypted without its key file.

---

## Reconcile & repair

Because fan-out is asynchronous, a backup write can fail *after* the primary
write already succeeded (target briefly unreachable, credentials rotated, disk
full, …). Every such failure is recorded in a **failures** table keyed by
`(storage, path, op)`, with an error code, message and attempt count, and it
fires a `replica_fail` notification. Repeated failures on the same path bump
the attempt count rather than piling up rows; the same path failing on two
storages is two failures. (A failure recorded before 0.53 carries storage `0`:
no storage claims it, and a repair resolves it.)

**Repair re-runs the failed operation against the backup**, through the
storage's live wrapper - so it goes to the target the storage is linked to
**now**. filex uses the persistent [queue](CONFIGURATION.md#queue) to do this
reliably:

- **Fix all** - `POST /api/admin/replica/fix` (`ReconcileAll`) enqueues one
  `replica_retry` queue op for **every unresolved failure** that has none waiting
  already. It answers `{queued, already_queued}`: a retry still waiting in the
  queue absorbs the next request for the same failure, so pressing again adds
  nothing. Progress is visible on the Queue page.
- **Fix one** - `POST /api/admin/replica/fix-one` with `{storage_id, path, op}`
  enqueues a single retry for one failure, `{ok, queued}`. `queued: false`
  means one was already waiting. A caller that leaves `storage_id` out is
  matched to the one unresolved failure at that path and op (**400** when
  there are several).
- The **retry handler** picks each op up and re-executes it against the backup:
  a `write`/`move`/`copy` re-reads the object from the primary and writes it to
  the target; a `delete` removes it from the target. On success it **resolves**
  the failure row (sets `resolved_at`); on error the row's error and attempt
  count are updated and the queue **retries with backoff** (up to 3 attempts)
  before giving up. A file the storage no longer has, and a storage that no
  longer replicates (unlinked, its target switched off or deleted, the storage
  gone), leave nothing to repair: the failure is resolved. A repaired `move`
  writes the new name; the old name stays on the backup.

Both answer **503** `{"error": "no replica configured"}` only when no storage
is linked to an enabled target.

> Repair and the initial copy ride on the persistent queue, so they need the
> queue enabled (its default). With `FILEX_QUEUE_DRIVER=redis`/`postgres`
> retries survive restarts and work across nodes; on the default `sqlite`
> queue they're local to the instance. See
> [CONFIGURATION.md → Queue](CONFIGURATION.md#queue).

## Status report & notifications

A **status report** summarises replication health: how many failures are
currently unresolved and how many were repaired in the last 24 h. It's a
**singleton** - one row, overwritten on each run - fetched with
`GET /api/admin/replica/report` (`204 No Content` until the first run).

You can run it two ways:

- **On a schedule** - set `report_cron` (a standard cron spec, e.g.
  `0 */6 * * *`) and `report_enabled: true` in
  [settings](#3-rules---per-path-modes). An empty spec or `report_enabled: false`
  removes the schedule.
- **On demand** - `POST /api/admin/replica/report/run-now`.

Each run **upserts** the report row (so the latest counts are always available)
but only **emits a `replica_status_report` notification when it's actionable** -
i.e. when there are failures, there were repairs, *or* a webhook URL is
configured (you've opted in to receive every report at your own endpoint).
⚠ That last condition means the **legacy** `FILEX_WEBHOOK_URL` specifically -
a webhook v2 target subscribed to `replica_status_report` does not make a
quiet report actionable, because the decision is taken before any target is
matched. This
stops an every-few-hours cron from flooding the in-app bell with "0 failures"
no-ops. When it does notify, the **in-app** message stays terse while the
**webhook** payload carries the **full list of failed paths** so you can pipe it
into your own tooling.

Other replication events that reach the bell + webhook:

| Event | Severity | When |
|---|---|---|
| `replica_fail` | warning | a background fan-out (write/delete/move/copy) to the backup failed, or an [initial copy](#initial-copy) started waiting for its target (`meta.initial: true`); `meta.storage_id` and `meta.storage` name the storage |
| `primary_read_fail` | error | a read/stat fell back to the backup because the primary was down |
| `replica_reconcile_done` | info | a **Fix all** run queued one or more new retries (retries already waiting are not announced again) |
| `replica_status_report` | info | a status report ran and was actionable (see above) |

The initial copy's per-file failures are rows in the failures list and are
not each announced.

---

## Admin endpoints

All under `/api/admin`, admin-only, and the platform operator's on a
multi-tenant install. When the replica subsystem isn't wired up, these return
**`503 Service Unavailable`** (`{"error":"replica offline"}` /
`"replica reconcile offline"`).

**Replication targets** (backup sinks):

| Method | Path | Purpose |
|---|---|---|
| `GET` | `/replication-targets` | List targets |
| `POST` | `/replication-targets` | Create a target |
| `GET` | `/replication-targets/{id}` | Get one |
| `PATCH` | `/replication-targets/{id}` | Update the fields the body names; rebuilds its storages (and restarts their initial copies on a new driver/config or when switched back on) |
| `DELETE` | `/replication-targets/{id}` | Delete (unlinks any storages pointing at it) |

**Rules:**

| Method | Path | Purpose |
|---|---|---|
| `GET` | `/replica/rules` | List rules |
| `POST` | `/replica/rules` | Create a rule |
| `PATCH` | `/replica/rules/{id}` | Update a rule |
| `DELETE` | `/replica/rules/{id}` | Delete a rule |

**Failures:**

| Method | Path | Purpose |
|---|---|---|
| `GET` | `/replica/failures?unresolved=true&limit=50&offset=0` | Paginate failures (each with its `storage_id`) |
| `GET` | `/replica/failures/count` | Count of **unresolved** failures |

**Repair:**

| Method | Path | Purpose |
|---|---|---|
| `POST` | `/replica/fix` | Enqueue a retry for **every** unresolved failure with none waiting → `{queued, already_queued}` |
| `POST` | `/replica/fix-one` | Enqueue one retry - body `{storage_id, path, op}` (`op` = `write`/`delete`/`move`/`copy`) → `{ok, queued}` |

**Folders on the targets:**

| Method | Path | Purpose |
|---|---|---|
| `GET` | `/replica/links` | Every linked storage's folder on its target → `{items: [{storage_id, storage_name, target_id, target_name, folder, created_unix}]}` |
| `PUT` | `/replica/links/{storage_id}` | Change it - body `{folder}`; **409** when another storage on the target uses it or the storage is not linked. Begins the storage's initial copy again; the old folder stays on the target |

**Initial copies:**

| Method | Path | Purpose |
|---|---|---|
| `GET` | `/replica/initial-copies` | Every replicating storage's initial copy → `{items: [{storage_id, storage_name, target_id, target_name, phase, counted, total, done, copied, present, excluded, failed, copied_bytes, last_error, started_unix, updated_unix, finished_unix}]}` |
| `POST` | `/replica/initial-copies/{storage_id}/restart` | Begin the storage's initial copy again (**409** when it is not linked to an enabled target) |

**Report & settings:**

| Method | Path | Purpose |
|---|---|---|
| `GET` | `/replica/report` | Latest status report (`204` if none yet) |
| `POST` | `/replica/report/run-now` | Generate a report immediately |
| `GET` | `/replica/settings` | Get `{report_cron, report_enabled, default_mode}` |
| `PATCH` | `/replica/settings` | Update settings (reloads cron + rules engine) |

> These same operations are also exposed as **token-authenticated REST** under
> `/api/ai/admin/...` for admin-scoped API tokens (used by MCP/automation),
> alongside the session-cookie admin panel above. The MCP tools are
> `admin_replica_*` (`admin_replica_initial_copies` for the copies,
> `admin_replica_links` for the folders). Targets' credentials are masked on
> every one of them.

---

## Failure modes & troubleshooting

### Failures are accumulating in the list
The backup target rejected or couldn't receive some writes. Check the failure
rows' error codes (`REPLICA_WRITE_FAIL`, `PRIMARY_READBACK_FAIL`,
`REPLICA_NO_WRITER`, `REPLICA_UNAVAILABLE`, `REPLICA_DRIVER_REPLACED`, …) and
confirm the **target is reachable** and its credentials are current (the same
"Test connection" checks you'd run on a storage apply to the target's
config). Once the target is healthy, hit **Fix all**
(`POST /api/admin/replica/fix`) to replay the backlog; resolved rows drop out
of the unresolved count.

- `REPLICA_UNAVAILABLE` - the target could not be opened at all; the message
  says why (its driver refused the configuration, or a plugin driver is not
  running). Fix the target on the Replication page: its storages are rebuilt
  on save.
- `REPLICA_DRIVER_REPLACED` - the change reached the storage while its
  replication was being reconfigured (the storage or its target was saved at
  that moment); **Fix all** replays it through the new configuration.
- `REPLICA_KIND_CONFLICT` - the target has a folder where the storage has a
  file of the same name; remove the folder from the target.

### Files aren't being replicated at all
Check, in order:
1. **filex is older than 0.53** - nothing was ever replicated before 0.53
   (the link was saved and not used). Upgrade: every linked storage then gets
   its initial copy once.
2. **The storage isn't linked** - `replica_target_id` is empty. Link it on the
   Replication page (step 2). Without a link there's no wrapper and nothing
   fans out.
3. **The target is disabled** (`enabled: false`): its storages are served
   unwrapped until it is switched back on (which restarts their initial
   copies).
4. **A rule set the path to `skip`** - or the `default_mode` is `skip`. Review
   `/api/admin/replica/rules` and `/api/admin/replica/settings`; remember the
   **lowest-priority matching rule wins**.
5. **The target cannot be opened** - the failures list fills with
   `REPLICA_UNAVAILABLE` rows that say why.
6. **Deletes specifically not propagating** - that path is likely `append_only`
   (deletes are intentionally *not* mirrored in that mode).

### Where are a storage's files on the target?
In the storage's own folder there, not at the target's root: the Replication
page shows it on the storage's row, `GET /api/admin/replica/links` lists them.
It does not follow a rename of the storage. filex's own folders (trash,
versions, thumbnails, open-with copies, drafts) are never there.

### The files that were there before the link are not on the target
That is the [initial copy](#initial-copy)'s work; its progress is on the
storage's row on the Replication page (`GET /api/admin/replica/initial-copies`).
It needs the queue (on by default) and runs at the lowest priority, so a busy
queue delays it. *Waiting* means the target stopped answering; it resumes on
its own, and **Run again** starts it anew. A file counted as *already there*
had the same size and time on the target; if the target's copy is wrong
anyway, delete it there and run the copy again.

### The status report never emits a notification
By design it only notifies when there's something to say. If counts are `0/0`
and you still want a heartbeat on every run, **configure a webhook URL** - the
report then posts to it every cron tick. Also confirm `report_enabled: true`
and a **valid cron spec** in settings (an invalid spec is rejected and no
schedule is installed). `GET /api/admin/replica/report` still returns the
latest row regardless of whether a notification fired.

### Endpoints return 503
`"replica offline"` / `"replica reconcile offline"`: the replica components
aren't wired for this instance; ensure the replica subsystem (and its queue
dependency) is enabled in the deployment. `"no replica configured"` from
`/fix` or `/fix-one`: no storage is linked to an enabled target - there is
nothing a repair could go through.

### A download worked even though the primary was down
Expected. Read and stat fall back to the backup when the primary is down, and
a `primary_read_fail` notification is emitted so you know the primary needs
attention - the backup covered for it. A file the primary simply does not have
is **not** looked for on the backup.

---

## See also

- [STORAGE.md](STORAGE.md) - storages, adapters, and the `config` shape shared with targets
- [CONFIGURATION.md](CONFIGURATION.md) - global config/env, including the [queue](CONFIGURATION.md#queue) that repair and the initial copy ride on
- [RBAC.md](RBAC.md) - per-storage / per-file access control
