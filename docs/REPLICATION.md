# Storage replication

Replication keeps a **second, backup‑only copy** of a storage's files. Every
write, delete, move and copy that lands on a storage is **fanned out** to a
linked backup sink in the background, so you always have an off‑site mirror you
can fall back to if the primary backend has a bad day.

This is an **advanced feature**. A normal filex install doesn't need it —
reach for it when you want a warm backup of a bucket/disk on a *different*
provider (e.g. an S3 bucket mirrored to a second region, or a local disk mirrored
to remote SFTP).

- [How it works](#how-it-works) — [targets vs storages](#targets-vs-storages) · [modes](#per-path-modes)
- [Setup](#setup) — [create a target](#1-create-a-replication-target) · [link a storage](#2-link-a-storage-to-the-target) · [rules](#3-rules--per-path-modes)
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
   │  ReplicationTarget   │  (backup-only sink — never shown, never read from
   │  (backup sink)       │   except as a fallback when primary read fails)
   └──────────────────────┘
```

The write to the **primary** happens synchronously — the user's request only
returns once the primary has the file. The copy to the backup target then
happens **asynchronously** in the background, by reading the object back from
the primary and writing it to the target. Reads normally come from the primary;
if a primary **read** or **stat** errors, the wrapper transparently falls back
to the replica so individual downloads keep working during a primary outage.
(Directory *listings* always come from the primary — they are never served from
the backup, so a listing can't show a half‑replicated view.)

### Targets vs storages

filex draws a hard line between the two:

| | **Storage** | **ReplicationTarget** |
|---|---|---|
| Shows in the Storages list / file explorer | **yes** — a named top‑level folder | **never** |
| Written to directly by users | yes | **no** — backup only |
| Read from | yes (source of truth) | only as a **fallback** when the primary errors |
| Role | primary backend | backup sink for one or more storages |
| Configured at | **Storages** page | **Replication** page |

A **ReplicationTarget** is just a backend definition (driver + config) with no
mount point. A regular **Storage** points at one via its `replica_target_id`
field; once linked, the storage is transparently wrapped by a *replicated
driver* that fans its writes out to the target. The target itself is invisible —
it will never appear as a folder and users can't browse it.

Both a storage and a target use the **same adapters** (`local` · `s3` · `sftp` ·
`webdav` · `ftp`) and the **same `config` shape**. See
[STORAGE.md → Adapters](STORAGE.md#adapters) for every adapter's config keys.

> **Pick a *different* backend for the target.** Mirroring an S3 bucket to
> another bucket on the same account, or a disk to a folder on the same disk,
> defeats the point — a provider/hardware failure would take out both copies.

### Per‑path modes

Not every path has to be mirrored the same way. A **rule engine** maps a
**path pattern → mode**, so you can (for example) fully mirror `documents/**`
but never propagate deletes under `archive/**`. There are three modes:

| Mode | Creates / updates | Deletes | Moves / copies / mkdir |
|---|---|---|---|
| **`mirror`** (default) | replicated | replicated | replicated |
| **`append_only`** | replicated | **not** replicated — the file **stays** on the backup | replicated |
| **`skip`** | not replicated | not replicated | not replicated |

- **`mirror`** — the backup tracks the primary exactly, deletions included.
- **`append_only`** — the backup only ever *grows*: new/changed files are
  copied, but a delete on the primary leaves the backup copy in place. Good for
  a tamper‑resistant archive where you never want a delete to erase the backup.
- **`skip`** — the path is excluded from replication entirely.

Rules are evaluated by **priority ascending** — the **first enabled rule whose
pattern matches wins**. If no rule matches (or there are no rules), the storage
falls back to the **default mode** from settings (`default_mode`, itself
defaulting to `mirror`).

**Patterns** are globs matched against the path (forward slashes):

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
or an admin‑scoped API token.

### 1. Create a replication target

`POST /api/admin/replication-targets`. The body is a backend definition — same
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
| `name` | string | — | Display name for the target. Required. |
| `driver` | string | — | `local` · `s3` · `sftp` · `webdav` · `ftp`. Required. |
| `config` | object | `{}` | Per‑adapter settings — see [STORAGE.md → Adapters](STORAGE.md#adapters). |
| `mode` | string | `async` | Fan‑out mode. `async` (default) fans writes out in the background. |
| `enabled` | bool | `true` | Disabled targets are ignored by the fan‑out engine. |

> The target's `mode` (`async`/`sync`) is **not** the same thing as a per‑path
> [replica mode](#per-path-modes) (`mirror`/`append_only`/`skip`). The target
> mode governs *when* the copy happens; today the engine always fans out
> **asynchronously**, so the user's write never waits on the backup.

### 2. Link a storage to the target

A target does nothing until a storage points at it. On the **Replication** page,
pick the storage and choose the target; via the API, `PATCH` the storage and set
`replica_target_id`:

```bash
curl -X PATCH https://files.example.com/api/admin/storages/7 \
  -H 'Content-Type: application/json' -b cookies.txt \
  -d '{ "replica_target_id": 3 }'
```

From that point on, every mutation on storage `7` is mirrored to target `3`
according to the [rules](#3-rules--per-path-modes). To **stop** replicating,
clear the link (`"replica_target_id": null`). Deleting a target automatically
unlinks every storage that pointed at it.

> The legacy `role` and `replica_of_id` columns on a storage are retained only
> for backwards compatibility with old (v0.1.16) deployments. The current model
> is the single `replica_target_id` foreign key — ignore the legacy fields.

### 3. Rules — per‑path modes

With no rules, everything replicates in the storage's default mode (`mirror`).
Add rules only where you want different behavior. `POST /api/admin/replica/rules`:

```bash
# Never propagate deletes under archive/ — keep the backup append-only.
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
| `description` | string | Free‑text note (optional). |

The catch‑all default mode lives in **settings**, not in a rule:

```bash
curl -X PATCH https://files.example.com/api/admin/replica/settings \
  -H 'Content-Type: application/json' -b cookies.txt \
  -d '{ "default_mode": "mirror", "report_enabled": true, "report_cron": "0 */6 * * *" }'
```

Rule and settings changes take effect immediately — the engine reloads its
cache after every create/update/delete.

---

## Reconcile & repair

Because fan‑out is asynchronous, a backup write can fail *after* the primary
write already succeeded (target briefly unreachable, credentials rotated, disk
full, …). Every such failure is recorded in a **failures** table keyed by
`(path, op)`, with an error code, message and attempt count, and it fires a
`replica_fail` notification. Repeated failures on the same path bump the attempt
count rather than piling up rows.

**Repair re‑runs the failed operation against the backup.** filex uses the
persistent [queue](CONFIGURATION.md#queue) to do this reliably:

- **Fix all** — `POST /api/admin/replica/fix` (`ReconcileAll`) enqueues one
  `replica_retry` queue op for **every unresolved failure**, and returns the
  number queued. Progress is visible on the Queue page.
- **Fix one** — `POST /api/admin/replica/fix-one` with `{path, op}` enqueues a
  single retry for one failure.
- The **retry handler** picks each op up and re‑executes it against the backup:
  a `write`/`move`/`copy` re‑reads the object from the primary and writes it to
  the target; a `delete` removes it from the target. On success it **resolves**
  the failure row (sets `resolved_at`); on error the queue **retries with
  backoff** (up to 3 attempts) before giving up.

> Repair rides on the persistent queue, so it needs the queue enabled (its
> default). With `FILEX_QUEUE_DRIVER=redis`/`postgres` retries survive restarts
> and work across nodes; on the default `sqlite` queue they're local to the
> instance. See [CONFIGURATION.md → Queue](CONFIGURATION.md#queue).

## Status report & notifications

A **status report** summarises replication health: how many failures are
currently unresolved and how many were repaired in the last 24 h. It's a
**singleton** — one row, overwritten on each run — fetched with
`GET /api/admin/replica/report` (`204 No Content` until the first run).

You can run it two ways:

- **On a schedule** — set `report_cron` (a standard cron spec, e.g.
  `0 */6 * * *`) and `report_enabled: true` in
  [settings](#3-rules--per-path-modes). An empty spec or `report_enabled: false`
  removes the schedule.
- **On demand** — `POST /api/admin/replica/report/run-now`.

Each run **upserts** the report row (so the latest counts are always available)
but only **emits a `replica_status_report` notification when it's actionable** —
i.e. when there are failures, there were repairs, *or* a webhook URL is
configured (you've opted in to receive every report at your own endpoint). This
stops an every‑few‑hours cron from flooding the in‑app bell with "0 failures"
no‑ops. When it does notify, the **in‑app** message stays terse while the
**webhook** payload carries the **full list of failed paths** so you can pipe it
into your own tooling.

Other replication events that reach the bell + webhook:

| Event | Severity | When |
|---|---|---|
| `replica_fail` | warning | a background fan‑out (write/delete/move/copy) to the backup failed |
| `primary_read_fail` | error | a read/stat fell back to the backup because the primary errored |
| `replica_reconcile_done` | info | a **Fix all** run queued one or more retries |
| `replica_status_report` | info | a status report ran and was actionable (see above) |

---

## Admin endpoints

All under `/api/admin`, admin‑only. When the replica subsystem isn't wired up,
these return **`503 Service Unavailable`** (`{"error":"replica offline"}` /
`"replica reconcile offline"`).

**Replication targets** (backup sinks):

| Method | Path | Purpose |
|---|---|---|
| `GET` | `/replication-targets` | List targets |
| `POST` | `/replication-targets` | Create a target |
| `GET` | `/replication-targets/{id}` | Get one |
| `PATCH` | `/replication-targets/{id}` | Update |
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
| `GET` | `/replica/failures?unresolved=true&limit=50&offset=0` | Paginate failures |
| `GET` | `/replica/failures/count` | Count of **unresolved** failures |

**Repair:**

| Method | Path | Purpose |
|---|---|---|
| `POST` | `/replica/fix` | Enqueue a retry for **every** unresolved failure → `{queued}` |
| `POST` | `/replica/fix-one` | Enqueue one retry — body `{path, op}` (`op` = `write`/`delete`/`move`/`copy`) |

**Report & settings:**

| Method | Path | Purpose |
|---|---|---|
| `GET` | `/replica/report` | Latest status report (`204` if none yet) |
| `POST` | `/replica/report/run-now` | Generate a report immediately |
| `GET` | `/replica/settings` | Get `{report_cron, report_enabled, default_mode}` |
| `PATCH` | `/replica/settings` | Update settings (reloads cron + rules engine) |

> These same operations are also exposed as **token‑authenticated REST** under
> `/api/ai/admin/...` for admin‑scoped API tokens (used by MCP/automation),
> alongside the session‑cookie admin panel above.

---

## Failure modes & troubleshooting

### Failures are accumulating in the list
The backup target rejected or couldn't receive some writes. Check the failure
rows' error codes (`REPLICA_WRITE_FAIL`, `PRIMARY_READBACK_FAIL`,
`REPLICA_NO_WRITER`, …) and confirm the **target is reachable** and its
credentials are current (the same "Test connection" checks you'd run on a
storage apply to the target's config). Once the target is healthy, hit
**Fix all** (`POST /api/admin/replica/fix`) to replay the backlog; resolved rows
drop out of the unresolved count.

### Files aren't being replicated at all
Check, in order:
1. **The storage isn't linked** — `replica_target_id` is empty. Link it on the
   Replication page (step 2). Without a link there's no wrapper and nothing
   fans out.
2. **The target is disabled** (`enabled: false`).
3. **A rule set the path to `skip`** — or the `default_mode` is `skip`. Review
   `/api/admin/replica/rules` and `/api/admin/replica/settings`; remember the
   **lowest‑priority matching rule wins**.
4. **Deletes specifically not propagating** — that path is likely `append_only`
   (deletes are intentionally *not* mirrored in that mode).

### The status report never emits a notification
By design it only notifies when there's something to say. If counts are `0/0`
and you still want a heartbeat on every run, **configure a webhook URL** — the
report then posts to it every cron tick. Also confirm `report_enabled: true`
and a **valid cron spec** in settings (an invalid spec is rejected and no
schedule is installed). `GET /api/admin/replica/report` still returns the
latest row regardless of whether a notification fired.

### Endpoints return 503
The replica components aren't wired for this instance. Ensure the replica
subsystem (and its queue dependency) is enabled in the deployment; repair
endpoints (`/fix`, `/fix-one`, `/report/run-now`) additionally need the
reconcile **Service**, which comes online once a storage is paired with a target.

### A download worked even though the primary was down
Expected. Read and stat fall back to the backup when the primary errors, and a
`primary_read_fail` notification is emitted so you know the primary needs
attention — the backup covered for it.

---

## See also

- [STORAGE.md](STORAGE.md) — storages, adapters, and the `config` shape shared with targets
- [CONFIGURATION.md](CONFIGURATION.md) — global config/env, including the [queue](CONFIGURATION.md#queue) that repair rides on
- [RBAC.md](RBAC.md) — per‑storage / per‑file access control
