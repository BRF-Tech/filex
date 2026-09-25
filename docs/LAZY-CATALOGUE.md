# Lazy catalogue (local storages)

> Issue [#45](https://github.com/BRF-Tech/filex/issues/45) — idea by Alex
> (@ahjephson). Design note for `sync_mode: lazy`; the operator-facing summary
> lives in [STORAGE.md](STORAGE.md#lazy-catalogue) and the settings in
> [CONFIGURATION.md](CONFIGURATION.md#lazy-catalogue-settings).

filex keeps a **catalogue** of every storage: one row per file and folder. The
explorer lists from it, and search, shares, comments, tags, folder sizes, the
drive usage figure, the antivirus and the desktop sync all read it. Every other
sync mode builds it by walking the whole storage before anything else happens,
and keeps it current by walking the whole storage again. On a multi-terabyte
NAS that is hours of disk I/O before the first folder is right, and again on
every pass.

`lazy` turns that around for **local** storages: the folder somebody opens is
listed straight from disk at once and catalogued first, in the background; the
rest of the tree is catalogued afterwards (behaviour A) or only as people visit
it (behaviour B).

## The two behaviours

| | A — click first, fill in the background (default) | B — only on open |
|---|---|---|
| Config | `lazy_fill: background` | `lazy_fill: on_open` |
| Opened folder | listed from disk now, catalogued first | the same |
| Rest of the tree | a throttled filler catalogues it, then keeps refreshing folders nobody watches | never, unless an operator runs a full scan |
| Search, folder sizes, drive usage | complete once the filler converges; say so until then | say plainly that they cover visited folders only |
| Antivirus, tags, thumbnails | follow the catalogue: eventually everything | visited folders only |

Both behaviours share everything below; B simply has no filler.

## Per-folder catalogue state

Migration `00059_catalogue_folders` adds one table (SQLite, PostgreSQL, MySQL):

| column | meaning |
|---|---|
| `storage_id`, `path_hash` | the folder (primary key). `path_hash` is `pathkey.Hash(storage, path)`, the same key `nodes` uses; the storage root is `/`. |
| `path`, `depth` | the folder's canonical path (`/a/b`, `/` for the root) and its depth (root = 0). The filler walks the frontier shallowest first. |
| `state` | `uncatalogued` — discovered in a parent's listing, never listed itself (the filler's frontier). `catalogued` — its listing has been applied. `watched` — catalogued and under an fsnotify watch in this process. |
| `reconciled_at` | when the last complete listing of this folder was applied to the catalogue. |
| `visited_at` | the last time a person opened it (the watch LRU's clock, and the "visited" of behaviour B). |
| `watched_at` | when the current watch was placed; `NULL` = no watch. |
| `reconcile_on_open` | a watch was evicted, expired or lost: the catalogue may have drifted, reconcile before trusting it. |
| `entries`, `held_back` | what the last listing saw, and how many deletions the guard held back (see below). |

No row at all means "not catalogued". A folder a scan exclusion (`scan_exclude`,
issue #44) covers never gets a row: it is not part of the catalogue.

The store methods over this table are written once, in
`internal/db/catalogue_folders_sql.go`, and every driver embeds them with its
dialect (`$N` placeholders and real timestamps on PostgreSQL; MySQL's upsert
rewrite; timestamps bound as text on SQLite and MySQL). They were first
written once per driver, and the duplication gate
(`web/tests/quality/duplication.test.ts`) found four verbatim blocks between the
two copies.

`watched` describes this process. At start every `watched` row is demoted to
`catalogued` with `reconcile_on_open` set — the watches died with the old
process, and changes made while nobody was watching are exactly what a reconcile
is for.

### Transitions

```
            parent listed            folder listed            watch placed
 (no row) ───────────────▶ uncatalogued ─────────▶ catalogued ─────────▶ watched
     │                                    ▲          ▲   │                  │
     └────────── folder listed ───────────┘          │   └─ re-listed ──────┤
                                                     └──── evicted / TTL / restart
                                                           (reconcile_on_open = 1)
```

A folder that disappears from its parent's listing (and is confirmed gone) loses
its row and every row under it.

## Reconciling one folder

`reconcileFolder(dir)` is the one operation everything else is built from:

1. **Refuse** a path the storage's scan rule skips (filex's own trees, `scan_exclude`).
2. **Ensure the folder's own row** and its ancestors' (`protocolsync.EnsureDirChain`,
   quietly — see "Change frames").
3. **List** the folder from disk (`driver.List`, one directory, never recursive).
   A failed listing changes nothing.
4. **Apply** the listing with the scan's own per-entry code (the body of
   `sync.walk`, extracted as `catalogueEntry`): create rows for new entries
   (size, mime, etag, **mtime** — a row without one drifts later and makes the
   desktop re-download it), update drifted ones, settle landed staged uploads,
   repair a row left behind by a folder move, queue the antivirus for new or
   changed files, index them. The encrypted-folder marker is applied first.
   Child folders are **not** entered; each gets an `uncatalogued` row.
5. **Delete pass** (see the invariant).
6. **Record** the folder `catalogued` (or keep it `watched`) with `reconciled_at`.

It never takes the storage-wide run lock (`runMu`) — a full scan and any number
of folder reconciles run side by side. Two reconciles of the SAME folder never
do: a per-folder lock dedupes them, and a request that arrives while one runs
marks it dirty so it runs once more afterwards.

Running beside a full scan is safe because every create in both paths tolerates
losing the race: a unique-key refusal re-reads the row the other one wrote and
carries on with it (`walk`, `EnsureDirChain`). Before this change the walk
skipped the subtree of a folder row it failed to create, and a full scan that
met a folder reconcile at the top of a folder finished with a hole in its
catalogue.

## ⚠⚠ The deletion-safety invariant

> **A row is only ever removed because the folder that directly contains it was
> just listed, completely and successfully, and the row's entry was not in that
> listing and is confirmed gone.** A folder that was never visited or
> reconciled is never treated as deleted, however old its rows are.

Concretely:

- The delete pass is a **per-folder** step of `reconcileFolder`, never a
  storage-wide sweep. Lazy mode never calls `ListStaleNodes`: `seen_at` says
  nothing in a catalogue that is deliberately not walked.
- `deletePass(dir, listing)` refuses to run unless the folder's state row says
  it was reconciled by **this** listing (`reconciled_at` equals the listing's
  timestamp). A caller that reaches it any other way — an event handler, a
  cleanup, a future refactor — removes nothing.
- Candidates are **direct children** of the listed folder only. A candidate's
  subtree goes with it only when the candidate itself is confirmed gone; rows in
  a child folder that still exists are never candidates, whatever state that
  child is in.
- Each candidate is confirmed with `Stat` (`ErrNotFound` only — any other error
  keeps it), for folders as well as files (the full scan skips the Stat for
  folders; lazy mode does not).
- Never a row the scan rule skips, never an unstored (in-flight) upload.
- **Guard:** the root that lists empty while the catalogue holds children is the
  signature of an unmounted mount point, and removes nothing. Otherwise, when 10
  or more children vanish at once and the listing saw fewer than 70% of them,
  nothing is removed (the full scan's ratio, per folder — a readdir that came
  back short from a network filesystem). The folder records `held_back`, is
  listed from disk until a later reconcile clears it, and the admin page counts
  it. A handful of files deleted outside filex is ordinary life and goes through.
- A removed row is soft-deleted **in place** (the tombstone pass's own
  `tombstone`), so the trash purge never touches bytes at that path
  (`trash.ownsBytesAt`).
- An fsnotify event saying a watched folder itself went away does **not** delete
  it: the watch is dropped and the parent's next reconcile decides.

The tests are built around this (`backend/internal/sync/lazy_safety_test.go`),
and each guard is break-tested: reverting it makes a test show rows wrongly
removed.

## Listing

`manager.go vfIndex` stays cache-first with a driver fallback, and gains one
rule. A folder is listed **from disk with the catalogue overlaid** when the
catalogue cannot vouch for it:

- the storage is `lazy` and the folder is not currently watched, or its last
  reconcile held deletions back;
- **any** storage whose first scan has not finished (`last_sync_at` unset) — a
  partly catalogued folder used to show only what had been catalogued so far.

The overlay keeps what only the catalogue knows — id, owner, thumbnail, tags,
app badges — for every entry that has a row. An entry whose row has drifted
(`sync.ObjectDrift`) shows the disk's size and date. A row that is not on disk
is dropped, unless it is an upload whose bytes are still on their way. A listing
from disk also asks for the folder to be catalogued (see next section). When
the folder is current, it is listed from the catalogue exactly as before.

## Catalogue on open

Every listing of a lazy storage calls `Worker.CatalogueOpened(storage, dir)`:
non-blocking, deduplicated. A watched folder only has its LRU position
refreshed. Anything else is queued for `reconcileFolder` on a small pool that
always runs ahead of the filler; when it finishes the folder gets a watch and
its viewers get a refresh frame, so the next listing comes from the catalogue
with thumbnails and owners.

## Watches and their budget

One `fsnotify` watcher per lazy storage, on **visited** folders only:

- `lazy_max_watches` (default 1024): placing one more evicts the least
  recently opened.
- `lazy_watch_ttl` (minutes, default 60): a folder nobody has opened for that
  long loses its watch.
- An evicted or expired watch sets `reconcile_on_open`; the rows stay. The next
  open reconciles before the folder is trusted again.
- A burst of events for a folder becomes one reconcile of that folder after a 2 s
  quiet period — never a full scan (fsnotify mode walks the whole storage per
  burst).
- A kernel refusal (`ENOSPC`, inotify's own limit) is logged once and treated as
  a full budget.

## The background filler (behaviour A)

One goroutine per storage, resumable because its work list is the table:

1. The root, if it was never catalogued.
2. The frontier — `uncatalogued` rows, shallowest first, a batch at a time.
3. Once the frontier is empty the storage is **converged**: its `last_sync_at`
   is stamped and folder sizes are recomputed. From then on the filler refreshes
   catalogued folders that are not watched and whose `reconciled_at` is older
   than the storage's sync interval (`sync_interval_s`, default 15 minutes),
   oldest first.

Throttle: a short pause between folders while nobody is using the storage, a
long one (250 ms) for 15 s after anybody lists, searches or opens anything on
it, and every open request runs first. Scan exclusions apply (a skipped folder
never gets a row). Folder sizes are recomputed every 30 s while filling, not per
folder.

The filler reads its frontier a batch at a time, and an open runs ahead of it,
so the folder somebody just opened is usually still in the batch. Before each
folder the filler checks the state row again and skips one that is already
catalogued (or, when refreshing, listed within the interval or watched): it
used to list every opened folder a second time about two seconds later.

## Desktop sync pairs

A pair needs its whole subtree in the catalogue: the upload precondition the
engine sends (`expect=<size>:<mtime>`) is checked against the catalogue row. So:

- a recursive `watch` (the desktop's change stream) on a lazy storage queues its
  subtree for cataloguing, ahead of the filler — breadth-first, folder by folder,
  skipping folders reconciled within the refresh interval;
- while the desktop stays connected, the subtree is walked the same way again
  every refresh interval (the storage's `sync_interval_s`, at least 30 s). The
  engine asks the realtime hub which roots are watched right now
  (`Hub.WatchedRoots`). A paired folder nobody opens is not fsnotify-watched —
  the watch budget is for folders people visit — so without this, behaviour B
  would never see a change made outside filex inside a pair;
- the precondition falls back to the file on disk when there is no row, or when
  the row has drifted, so an engine that planned from a disk listing is judged
  against what it saw (and `expect=none` is refused when a file is already
  there);
- a reconcile that finds a real change in a folder that was already catalogued
  sends an ordinary change frame, which reaches `tree_change` watchers; the
  first catalogue of a folder sends a *derived* frame (explorers refresh their
  listing; mirrors ignore it, nothing changed on disk).

## Change frames

Cataloguing an entry that was already on disk is not a change: nothing is
announced per row, and `EnsureDirChain` runs without its "create" frames. What
is announced is per folder, after the reconcile:

| the reconcile… | frame |
|---|---|
| catalogued the folder for the first time | derived `modify` — explorers re-list, `tree_change` watchers and the `action=changes` log ignore it |
| found entries added, changed or removed in a folder it had catalogued before | `modify` — a real change, for everyone |
| found nothing | none |

The change log (`action=changes`) now skips derived events altogether: they
describe an aggregate or a catalogue refresh, never a change a sync client has to
fetch.

## Coverage in the UI

The listing, search and drive-usage responses carry a `coverage` object for a
storage whose catalogue is incomplete — a lazy storage that has not converged
(A), any lazy storage in behaviour B, or any storage still on its first scan:

```json
{"complete": false, "reason": "lazy_on_open", "catalogued_folders": 412, "pending_folders": 9001}
```

The shared explorer (`packages/core`, `lib/catalogCoverage.ts`) turns it into
one strip above the listing and the search results
(`data-testid="catalog-coverage"`), one sentence per reason:

| reason | the strip says |
|---|---|
| `first_scan` | the storage's first sync has not finished; search, folder sizes and usage leave part of it out |
| `lazy_filling` | filex is still cataloguing it (N % of the folders found so far) |
| `lazy_on_open` | only the folders people open are catalogued; search, sizes and usage cover those only |

A search typed at a storage's root spans every storage (the server's own rule),
so its strip names each storage that is not fully catalogued. The listing and
search responses carry `coverage` per storage in `storage_info`; the explorer
keeps the last answer, so a content search (which does not carry it) still gets
the strip.

Every folder whose subtree is not fully catalogued carries `size_partial` — its
size is drawn as a lower bound (`≥ 1.2 GB`) or `—` when nothing below it is
known, with a hover that says why. One helper (`useLocale.formatNodeSize`)
formats sizes for the list and the inspector's selection total, so they cannot
disagree. Home's drive card says **at least … used** when its figure is partial
(`/api/files/quota/storages` and `/api/admin/storages` both carry `coverage`
beside the figure).

An administrator sees **Catalog everything** on the strip in behaviour B. It
starts the ordinary full sync (`POST /api/admin/storages/{id}/sync`; the id is
looked up at click time, so the listing never carries it).

## Admin and observability

- The storage form (web admin, new and edit) offers the sync mode for every
  storage — it offered none before — and `lazy` only for a driver whose
  descriptor carries `lazy_fields` (the admin descriptor endpoint adds them for
  `model.LazyDrivers`, today `local`). For `lazy` it draws the behaviour and the
  two watch knobs with the same field renderer as every other storage setting
  (`web/src/components/StorageSyncMode.vue`). The API refuses a value outside
  the bounds the descriptor advertises (`storage.ValidateLazyConfig`, 400).
- The storage page shows the `catalogue` block (`StorageCatalogStatus.vue`):
  progress, the filler's state in words, folders catalogued / waiting, the
  watch budget in use, folders with deletions held back. It refreshes itself
  every 5 s while the catalogue is incomplete, without touching the form. The
  storage list carries one line: *Catalog: N %* or *Catalogued as folders are
  opened*.
- `GET /api/admin/storages` and `/{id}` carry `coverage` (any storage whose
  figures are partial) and, for a lazy storage, a `catalogue` block:
  folders catalogued / pending / watched (and the budget), the filler's state
  (`filling`, `paused` — somebody is active, `converged`, `refreshing`, `off`),
  folders held back, reconciles since start. The storage page shows it.
- Prometheus: `filex_lazy_folders{storage,state}`, `filex_lazy_watches{storage}`,
  `filex_lazy_reconciles_total{storage,reason}`,
  `filex_lazy_held_back_total{storage}`.
- Log lines at INFO for filler start, convergence and each held-back deletion;
  at DEBUG per folder.

## Measured

A generated local tree of **100,020 files in 1,041 folders** (40 × 25 folders
of 100 files, plus 20 at the root), the real binary, Chromium through
Playwright, on a Windows 11 development machine with SQLite (2026-09-25):

| | behaviour A | behaviour B | `poll`, during its first sync |
|---|---|---|---|
| root, from the click to the rows on screen | 1.1 s | 1.6 s | 1.6 s — all 60 entries (it used to be the few the sync had reached) |
| a folder, from the double click to its rows (100 files) | 0.2 – 0.35 s | 0.2 – 0.4 s | — |
| the same while the filler works | 0.25 – 0.35 s | — | — |
| the opened folder catalogued (its rows get ids) | 1.0 s after it was on screen | the same | — |
| strip | "still cataloging (N % …)" | "Only the folders people open …" + **Catalog everything** | "first sync has not finished" |

Server side, 36 listings (from disk, merged, and from the catalogue) took
30 ms at the median and 72 ms at worst. Behaviour A converged on its own after
38.6 min (every file and folder catalogued, the strip gone, no folder size
partial). Behaviour B catalogued exactly the three folders that were opened and
nothing more in the following minute. The per-entry catalogue work is the
full scan's own (`catalogueEntry`): the filler ran at ~43 files/s while the
measurement polled the storage's figures every second (each poll sums every
row, and SQLite has one connection), the full scan at ~82 files/s alone. Making
that per-entry work cheaper (a transaction per folder, say) would speed up
both and is outside this change.

## What is refused

- `lazy` on a driver other than `local` (400 on write; an old row falls back to
  polling with a warning). The watch budget and the per-folder listing are local
  disk features.
- A manual folder rescan (`POST …/sync?path=`) is still the old, locked,
  recursive rescan; a full scan (`POST …/sync`) is still the full scan, and on a
  lazy storage it also records every folder it listed as catalogued.
