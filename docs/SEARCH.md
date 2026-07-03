# Full-text search

filex ships an embedded **full-text index** so you can find files by name across
**every mounted storage** at once, without walking the backends. Type a fragment
of a filename and matching rows come back in a few milliseconds — the same fast
path the explorer's toolbar search uses.

The index covers file **metadata** — name, path, mime type, and node type — not
file *contents*. It is powered by [Bleve](https://blevesearch.com/), a pure-Go
search library, so there is nothing external to run: the index is a directory on
disk next to filex's other data.

- [How it works](#how-it-works)
- [Configuration](#configuration)
- [Searching — endpoints](#searching--endpoints)
- [Admin — stats & rebuild](#admin--stats--rebuild)
- [Failure modes & troubleshooting](#failure-modes--troubleshooting)
- [See also](#see-also)

---

## How it works

The index is a **Bleve database** stored at `<data_dir>/search.bleve`. It's
opened **lazily** on first use and shared by the whole server.

Each indexed document is one filesystem node, keyed by the node's database ID,
holding five fields:

| Field | Source | Used for |
|---|---|---|
| `storage_id` | mount the node lives on | scoping results to one storage |
| `name` | filename | the primary match target |
| `path` | full path within the mount | substring matches on folders |
| `mime` | detected mime type | stored, available to queries |
| `type` | `file` / `dir` | stored |

**How documents get in.** Two paths keep the index populated, both **best
effort** — a failure never blocks the underlying operation:

1. **On every write / mutation.** When a node is created, uploaded, moved, or
   renamed, filex re-indexes it (`indexNode`); deletes remove it
   (`removeFromIndex`). Search staleness is never worth failing a write, so any
   index error is swallowed.
2. **During storage sync.** The background sync worker feeds every upsert it
   discovers into the index (`AttachIndex`), so files that appear on a backend
   **outside** filex (e.g. dropped straight into an S3 bucket) become
   searchable after the next [sync](STORAGE.md#sync).

**How a query runs.** Filenames tokenise awkwardly — Bleve's standard analyzer
treats `square.jpg` as a single token because the dot isn't a word boundary. So
a search runs **three sub-queries at once** (a disjunction) and unions the hits:

- a **match** query on `name` — exact-token and word-prefix hits (ranks full
  filenames like `square.jpg` well);
- a **wildcard** `*term*` on `name` — catches mid-string substrings like `squ`
  → `square.jpg`;
- a **wildcard** `*term*` on `path` — matches folder segments.

The term is lower-cased for the wildcard sides (Bleve stores tokens lower-cased
but does **not** analyse wildcard queries, so an upper-case term would otherwise
miss every row). The default result cap is **50**.

**SQL LIKE fallback.** If the Bleve index is disabled or returns **zero** hits
*and* the request is scoped to a specific storage, filex falls back to a plain
`LIKE '%term%'` scan of the `nodes.name` column. This fallback is **name-only**
and slower, but guarantees a result path even when the index is unavailable.

**RBAC filtering.** Whichever path produced the hits, results are filtered
through the caller's [RBAC](RBAC.md) grants before they're returned — a user
never learns that a file exists via search if they couldn't see it by browsing.

---

## Configuration

Search is **on by default**. There are only two knobs:

| Setting | Where | Default | Meaning |
|---|---|---|---|
| `FILEX_SEARCH_ENABLED` | env | `true` | Master switch. Accepts `1` / `true`. When off, no index is opened and search uses the LIKE fallback only. |
| `search.enabled` | `config.yaml` | `true` | Same switch in YAML form. |
| `search.index_path` | `config.yaml` **only** | `<data_dir>/search.bleve` | Where the Bleve directory lives. **No env override** — set it in the file if you want the index somewhere else (e.g. a faster disk). |

```yaml
# config.yaml
search:
  enabled: true
  index_path: /var/lib/filex/search.bleve   # optional; defaults under data_dir
```

```bash
# Disable the index entirely (LIKE-only search)
FILEX_SEARCH_ENABLED=false
```

At startup, when `search.enabled` is true, filex opens (or creates) the index at
`index_path`. If that **open fails** — corrupt directory, bad permissions, a
stale lock — filex logs a warning and **degrades to the SQL LIKE fallback**
rather than refusing to boot. Search keeps working, just slower and name-only.

> **Single-writer lock.** Bleve takes an exclusive lock on the index directory,
> so only one process may hold it. This is why offline/maintenance commands that
> don't need search skip opening it — a running `filex serve` already owns the
> lock.

---

## Searching — endpoints

### `POST /api/files/search` — canonical

Body-carrying form used by the app. Requires a normal user session.

```bash
curl -X POST https://files.example.com/api/files/search \
  -H 'Content-Type: application/json' -b cookies.txt \
  -d '{ "query": "invoice", "storage_id": 3, "limit": 50 }'
```

| Field | Type | Default | Meaning |
|---|---|---|---|
| `query` | string | — | The search term. |
| `storage_id` | int | `0` (all) | Restrict to one storage. **Required to enable the LIKE fallback** (see below). |
| `limit` | int | `50` | Max results. |

Response: `{ "results": [ { …node… }, … ] }`, already RBAC-filtered.

### `GET /api/files/search?q=…` — same handler

Convenience form for the SPA's toolbar (`?q=`, `?storage_id=`, `?limit=`). `q`
and `query` are both accepted. Behaves identically to the POST form.

```bash
curl -G https://files.example.com/api/files/search \
  --data-urlencode 'q=report' --data-urlencode 'storage_id=3' -b cookies.txt
```

> **Note.** The SQL LIKE fallback only fires when Bleve returns **0** results
> **and** you passed a non-zero `storage_id`. An all-storages query (`storage_id`
> = 0) that the index can't answer returns empty rather than scanning every
> mount.

### `GET /api/ai/search?path=<adapter://>&q=…` — token / agent surface

The programmatic search used by API tokens and the MCP/AI integration. Requires
a token with the **`read`** scope. `path` addresses the adapter root to search
within, `q` is the term. Results are confined to the token's root, so a scoped
token can't enumerate outside its grant.

```bash
curl -G https://files.example.com/api/ai/search \
  -H 'Authorization: Bearer <token>' \
  --data-urlencode 'path=s3://projects' --data-urlencode 'q=budget'
```

Response: `{ "entries": [ … ] }`.

---

## Admin — stats & rebuild

Both endpoints require an **admin** session/token.

### `GET /api/admin/search/stats`

Reports the index state:

```json
{
  "enabled": true,
  "document_count": 18423,
  "index_size_bytes": 5242880,
  "last_updated_at": ""
}
```

- `enabled` — `false` when the index isn't wired (search is LIKE-only). The
  other counters are `0`.
- `document_count` — number of indexed nodes.
- `index_size_bytes` — on-disk size of the `search.bleve` directory.
- `last_updated_at` — best-effort timestamp; may be blank.

### `POST /api/admin/search/rebuild`

Drops the index and reindexes **every node row** from the database. Returns
immediately; the work runs in the background.

```bash
curl -X POST https://files.example.com/api/admin/search/rebuild -b cookies.txt
```

| Status | Meaning |
|---|---|
| **202 Accepted** | `{ "ok": true, "note": "rebuild started in background" }` — rebuild launched. |
| **400 Bad Request** | `search index disabled` — the index isn't enabled, so there's nothing to rebuild. |
| **409 Conflict** | `rebuild already in progress` — one rebuild at a time; wait for it to finish. |

Internally the rebuild **closes** the current index, **removes** the directory,
**reopens** a fresh one, then re-indexes all nodes. It runs on a detached
(background) context so it survives the HTTP request returning — a large tree
keeps reindexing to completion. Watch `document_count` on the stats endpoint
climb back up to confirm it finished. During a rebuild queries still work; they
just see a partially populated index until it catches up.

Both actions are also exposed to admin tokens as the MCP tools
`admin_search_stats` and `admin_search_rebuild`.

---

## Failure modes & troubleshooting

### A file I just uploaded doesn't show up in search
Indexing is **best-effort and asynchronous** — a write commits before (and
regardless of whether) the index update lands, and files added straight to a
backend only appear after a sync. Normally the lag is sub-second. If a file is
persistently missing, run a [storage sync](STORAGE.md#sync) or a
`POST /api/admin/search/rebuild` and it will reappear.

### Search feels slow, or only matches whole filenames
You're on the **SQL LIKE fallback**. That happens when `FILEX_SEARCH_ENABLED` is
off, or the Bleve index failed to open at startup (check the logs for
`search index open failed; falling back to SQL LIKE`). The fallback scans the
`name` column only — no path/substring ranking. Fix the index (see next) to get
the fast, multi-field path back.

### `search index open failed` in the logs
The Bleve directory is unreadable, corrupt, or **locked** by another process.
Confirm only one filex instance points at that `index_path`, that filex can
write it, then either restart, or delete the `search.bleve` directory and run a
**rebuild** to recreate it cleanly.

### Substring / case searches miss rows
The multi-field disjunction (match + wildcards, lower-cased) handles most of
this. If partial matches are silently missing while whole-name matches work,
the most likely cause is a **stale index** — trigger a rebuild.

### After a bulk import, lots of files are unsearchable
Bulk imports that bypass filex's write path (rsync into a local mount, mass S3
upload) only enter the index via **sync**. Wait for the next sync of that
storage, trigger an on-demand sync, or run a **rebuild** to index everything at
once.

### Nothing comes back for an all-storages query
Remember the fallback needs a scope: an unscoped query (`storage_id` = 0) that
the index can't answer returns empty instead of doing a full-database LIKE scan.
Pass a `storage_id`, or rebuild the index so Bleve can answer it directly.

---

## See also

- [STORAGE.md](STORAGE.md) — storages and the sync worker that feeds the index
- [RBAC.md](RBAC.md) — the per-storage/per-file grants that filter results
- [CONFIGURATION.md](CONFIGURATION.md) — full config/env reference
- [API.md](API.md) — HTTP API overview
