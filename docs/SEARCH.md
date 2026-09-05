# Full-text search

filex ships an embedded **full-text index** so you can find files by name across
**every mounted storage** at once, without walking the backends. Type a fragment
of a filename and matching rows come back in a few milliseconds — the same fast
path the explorer's toolbar search uses.

Name matching is **forgiving**: `.`, `-`, `_` and a space all count as the same
separator, every word of a multi-word query has to match, and a typo still finds
the file. Results come back in a **defined rank order**, exact filename matches
first. A query can also carry a **[`tag:` filter](#query-syntax)**.

The index covers file **metadata** — name, path, mime type, and node type — and,
since v0.2, the extracted **contents** of text-like files (see
[Content search](#content-search)). It is powered by
[Bleve](https://blevesearch.com/), a pure-Go search library, so there is nothing
external to run: the index is a directory on disk next to filex's other data.

The index also **keeps itself current across upgrades**: when a new filex
indexes something the documents on disk do not carry, it rebuilds in the
background and swaps the result in, without a search outage and without losing
the text it already extracted. See
[Upgrading an existing index](#upgrading-an-existing-index).

- [How it works](#how-it-works)
- [Query syntax](#query-syntax)
- [Ranking](#ranking)
- [Content search](#content-search)
- [Configuration](#configuration)
- [Searching — endpoints](#searching--endpoints)
- [Admin — stats & rebuild](#admin--stats--rebuild)
- [Upgrading an existing index](#upgrading-an-existing-index)
- [Failure modes & troubleshooting](#failure-modes--troubleshooting)
- [See also](#see-also)

---

## How it works

The index is a **Bleve database** stored at `<data_dir>/search.bleve`. It's
opened **lazily** on first use and shared by the whole server.

Each indexed document is one filesystem node, keyed by the node's database ID:

| Field | Source | Used for |
|---|---|---|
| `storage_id` | mount the node lives on | scoping results to one storage |
| `name` | filename, verbatim | the primary match target |
| `path` | full path within the mount | substring matches on folders |
| `name_norm` | the filename lower-cased, with every run of non-alphanumeric characters collapsed to one space (`invoice_2026.pdf` becomes `invoice 2026 pdf`) | separator-blind and typo-tolerant matching |
| `path_norm` | the same treatment applied to the path | separator-blind folder matches |
| `mime` | detected mime type | stored, available to queries |
| `type` | `file` / `dir` | stored |
| `content` | extracted plain text (async, capped at 200 KiB) | content matches + snippets |
| `content_sig` | fingerprint (etag, or size+mtime) of the bytes the content came from | skipping re-extraction when nothing changed |

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

**How a query runs.** Filenames tokenise awkwardly. Bleve's standard analyzer
keeps `square.jpg` as one token, because a dot between two letters is not a word
boundary — but it splits `invoice_2026.pdf` into `invoice_2026` + `pdf`, and
`foo-bar.txt` into `foo` + `bar.txt`. Which separator a file happened to use
therefore decided whether a search found it, and that is not a distinction any
user can predict. So filex does not leave the decision to the analyzer: it
indexes a **normalised** copy of the name and normalises the query the same way.

A name search runs a **disjunction** of these:

- a **match** query on `name` — exact-token and word-prefix hits (ranks full
  filenames like `square.jpg` well);
- a **wildcard** `*term*` on `name` — mid-string substrings, `squ` →
  `square.jpg`;
- a **wildcard** `*term*` on `path` — folder segments;
- a **match** query on `name_norm` with **operator AND** — every word of the
  normalised query has to be present. This is what makes `invoice 2026` find
  `invoice_2026.pdf`;
- one **`*word*` wildcard per word** on `name_norm` and on `path_norm`, all
  required. The whole-term wildcards above cannot match a multi-word query at
  all, because no indexed token contains a space — so before this, the first
  space you typed switched half the search off. These two only run where they
  can add something (a multi-word query, or a word normalisation changed), since
  a leading `*` costs a full term-dictionary walk.

The term is lower-cased for the wildcard sides (Bleve stores tokens lower-cased
but does **not** analyse wildcard queries, so an upper-case term would otherwise
miss every row).

**Typo tolerance.** If that pass comes back with fewer **surviving** hits than
the requested `limit`, a second, **fuzzy** pass runs: one edit-distance query per
word, all required. Words of 3 characters or fewer must match exactly (one edit
on a short word matches half a term dictionary and means nothing), 4–7 allow one
edit, 8 and over allow two. A transposition counts as **one** edit, which is why
`mian.go` finds `main.go`. Fuzzy hits always rank below literal ones — see
[Ranking](#ranking).

⚠ **A word that is all digits is never fuzzy**, whatever its length. `2026` does
not match `2025`, and an invoice number does not match its neighbour. Edit
distance models a finger landing on the wrong key in a *word*, where the reader
still recognises what was meant; in a number there is nothing left to recognise
— a near-miss digit is a different year, a different invoice, a different order,
and returning it is a wrong answer wearing the clothes of a helpful one.
Measured before the exemption: `2026` returned `annual report 2025.docx` as its
**second** hit.

This turns off only the *fuzzy* pass for such a word. Exact, prefix, substring
and separator-blind matching are untouched, so `2026` still finds
`invoice_2026.pdf` and `Budget-2026.csv`, and `invoice 2026` still ranks the
file matching both words first.

**Mixed words stay fuzzy.** `v2024x` or `report2024` are names with a number
inside them; their letters carry the same typo risk any word does, and the
normaliser has already split the numbers that stand on their own into their own
words (`invoice_2026.pdf` becomes `invoice`, `2026`, `pdf`). Exempting mixed
tokens would take typo tolerance away from a whole class of real filenames to
protect a number the normaliser never isolated.

Surviving, not returned: the candidates the first pass produced are put through
the [scorer](#scoring--how-candidates-are-ordered-and-filtered) before they are
counted, so a pass that came back with fifty half-matches no longer looks like a
full result set and no longer keeps this pass switched off.

The fuzzy pass is deliberately conditional. It is the cheap half of the query
(measured on a 20 000-document index: **1.0 ms**, against **7.8 ms** for the
wildcard scans), but always-on fuzziness pads a perfectly good result list with
near-misses nobody asked for.

The default result cap is **50**. Internally the index is asked for up to four
times that (capped at 500), so the ranking has a real candidate pool to order
rather than re-shuffling a window Bleve's raw scores had already chosen. Scoring
that pool costs about **2 µs per candidate** — 0.4 ms for the default 200, 1.0 ms
at the 500 cap — against the 7.8 ms the wildcard scans cost to produce it.

**SQL LIKE fallback.** If the Bleve index is disabled or returns **zero** hits
*and* the request is scoped to a specific storage, filex falls back to the
`nodes.name` column. The fallback is a different code path, not a different
product, so it is separator-blind too: the most selective word of the query goes
to the database as `LIKE '%word%'`, and every row that comes back is re-checked
in Go by the **same scorer** the index path uses, and ranked into the same tiers.
`invoice 2026` finds `invoice_2026.pdf` with the index switched off, and `Code
main` drops the `Code` folder there exactly as it does with the index on.

Two things the fallback does not do. **Typo tolerance** — edit distance is not
something a `LIKE` can express, and faking it with more patterns would turn one
scan into many. And the LIKE itself runs against the **name** column only, so a
query whose words appear solely in a folder name will not be *retrieved* this
way — though once a row is retrieved, its folders are scored like anywhere else.

**RBAC filtering.** Whichever path produced the hits, results are filtered
through the caller's [RBAC](RBAC.md) grants before they're returned — a user
never learns that a file exists via search if they couldn't see it by browsing.
Snippets ride on the hit and are dropped with it, so content search can never
leak text from a file the caller couldn't open.

---

## Query syntax

A query is free text, optionally carrying tag filters.

| You type | You get |
|---|---|
| `main go` | `main.go` — separators do not matter |
| `invoice 2026` | `invoice_2026.pdf` |
| `foo bar` | `foo-bar.txt` |
| `mian.go` | `main.go` — one typo forgiven |
| `2026` | `invoice_2026.pdf`, `Budget-2026.csv` — **not** `annual report 2025.docx`: numbers are matched literally |
| `report 2025` | `annual report 2025.docx` — both words must match |
| `tag:invoice` | every file tagged `invoice` |
| `main go tag:source` | `main.go`, but only if it carries the `source` tag |
| `report -tag:archive` | `report…` files that are **not** tagged `archive` |

**Multi-word queries narrow.** Every word has to match somewhere in the name
(or, at a lower rank, the path). Adding a word never widens the result set.

### Filtering by tag

Tags are the ones you apply from the explorer and browse on the **Tagged files**
page; the API is `POST /api/files/manager/tags`. In a search they are a
**filter**, not a search term: `main go tag:source` does not also look for files
called "source".

| Rule | Behaviour |
|---|---|
| Case | Both the `tag:` prefix and the value are case-insensitive. Tags are stored lower-cased, so `TAG:Source` and `tag:source` are the same filter. |
| Several tags | ANDed. `tag:invoice tag:2026` is the files carrying **both**. A filter narrows. |
| Exclusion | `-tag:archive` drops any file carrying that tag. Exclusions apply after inclusions. |
| Spaces | Quote them: `tag:"quarterly report"`. |
| A tag that does not exist | Returns **nothing**. It is not ignored — a filter that matched nothing has an answer, and it is the empty set. A typo in a tag name shows up immediately as an empty result rather than as the whole storage. |
| A bare `tag:` with no value | Not a filter. It stays part of the free text, so a file actually named `tag:` is still findable. |
| Text with no tags | Unchanged behaviour. |
| `tag:` alone, no free text | A listing of the tagged files, newest first — there is no text to rank by. |

The filter is resolved against the **database**, not the search index, so a tag
you applied a second ago filters correctly with no reindex. It is then pushed
into the index as a document-ID set, which means `limit` counts filtered
results: asking for 10 hits under a tag gives you 10 hits under that tag, not
10 unfiltered hits of which some happen to qualify.

> **Limit.** One tag contributes at most **10 000** nodes to a filter. Past
> that the newest 10 000 are used (the order the tag listing returns). No
> hand-applied tag reaches this; a machine-applied one might.

Tag filtering is applied by `/api/files/search`, the explorer toolbar and the
MCP `file_search` tool alike. Results are still passed through the caller's
tenant scope and [RBAC](RBAC.md) grants afterwards, exactly like any other hit —
a tag cannot be used to learn that a file exists.

---

## Ranking

The issue that prompted the forgiving matching also asked that exact filename
matches keep ranking first. With fuzziness in the query that stopped being
something merged relevance scores can be trusted to deliver — a two-edit fuzzy
hit on a short filename can out-score an exact hit on a long one — so the order
is decided explicitly and is covered by a test.

Hits are sorted by **tier** first, then by score within the tier, then by the
**shorter path**, then by node id so the same query always answers in the same
order.

| # | Tier | Means |
|---|---|---|
| 1 | **exact** | The filename equals the query, ignoring case and separators — or the query is the whole path. The extension is compared both ways, so `report` is an exact hit on `report.txt` and `main go` is an exact hit on `main.go`. |
| 2 | **prefix** | The filename starts with the query (`report` → `report-final.txt`). |
| 3 | **name** | Every query piece is answered by the **filename** (`report` → `q1-report.txt`). |
| 4 | **path** | At least one piece needed a **folder** to answer it (`Code main` → `Code/main.go`). |
| 5 | **fuzzy** | Only the typo-tolerant pass produced it (`report` → `reprot.txt`). An all-digit query word never lands anything here. |
| 6 | **content** | Matched inside the file, not in its name. |

Tier 6 sorting last is also how the pre-v0.2 contract — **name hits before
content-only hits** — survives: a document that matched on both keeps its name
tier, is reported once, and carries `matched: "both"` plus its snippet.

The tier is internal; it is not on the wire. The response shape is unchanged.

The SQL LIKE fallback applies the same tiers in Go, so an index-less deployment
answers in the same order rather than in `ORDER BY name`.

### Scoring — how candidates are ordered and filtered

Bleve decides which documents are worth *looking at*. What makes one of them a
better answer than another is decided afterwards, in Go, by a **subsequence
scorer** ported from VS Code's Quick Open
([`fuzzyScorer.ts`](https://github.com/microsoft/vscode/blob/main/src/vs/base/common/fuzzyScorer.ts),
MIT). A relevance score cannot express any of this: it does not know *where* in
the filename the match landed, whether the file or its folder answered, or
whether every word you typed was answered at all.

Three things follow from it, and all three were asked for in issue #15:

- **Word order does not matter.** The query is split on spaces into pieces, and
  each piece is matched independently. `main code` and `Code main` both find
  `Code/main.go`.
- **The filename outweighs the folder.** The filename and the folders above it
  are scored *separately*, and a piece answered by the filename is worth an
  order of magnitude more than one answered by a folder. `Code/main.go` and
  `example/main.go` are no longer the same thing to the search: naming one
  folder excludes the other.
- **Every piece must be answered, or the candidate is dropped.** The scorer is
  the filter as well as the ranking. A query of `Code main` used to return the
  `Code` *folder* too, because it answered "code" — and, at the default scope,
  seven more files that merely contained the word "code". It now returns the
  file.

Within a match, characters score by *position*, cumulatively: +8 at the start of
the name, +5 straight after a `/`, +4 after `_ - . space : ' "`, +2 on a
camelCase hump, plus a bonus for each consecutive character in a run. That is
why `report` prefers `report-final.txt` over `q1-report.txt` even though both
contain the word.

**Multi-word content search narrows too.** Extra words used to *widen* the
content side (it was an OR), which is where most of that noise came from. A
two-word query now requires both words in the text.

#### What fuzzy does not mean here

Two deliberate limits, both worth knowing before filing a bug:

- **Subsequence scoring is not subsequence recall.** The scorer only ever sees
  candidates the index produced, and no Bleve query retrieves `main.go` for a
  query of `mgo`. Making it do so needs a whole-filename keyword field and a
  full reindex, not a scoring change. What the scorer buys is *ordering and
  precision* over candidates that were already found.
- **Edit distance stays, and it is not VS Code's behaviour.** `mian.go` is not a
  subsequence of `main.go`, so Quick Open would find nothing for it; filex still
  finds the file, via the typo pass, ranked below every subsequence match. That
  pass now fires when the *surviving* hits are fewer than the limit, rather than
  when the raw candidates are — before the scorer, a candidate list full of
  half-matches counted as a full result set and kept it switched off.

---

## Content search

Since v0.2 ("Bul"), filex also indexes what's **inside** files, fully
**asynchronously** — the write path never waits on (or fails because of)
extraction.

**Pipeline.** Every time a file's metadata is (re)indexed — upload, save,
rename, sync upsert — filex compares the file's content fingerprint (`etag`,
falling back to size+mtime) against what the index already holds. On drift, a
`content_index` job is enqueued on the persistent queue
([`FILEX_QUEUE_DRIVER`](CONFIGURATION.md)). The worker
reads the file from its storage driver, runs the matching extractor, and
updates the node's document with the text — metadata fields are preserved.
Unchanged files never re-extract; errors are logged and skipped.

**What gets extracted** (built-in extractors, `internal/search/extract`):

| Family | Types | How |
|---|---|---|
| Plain text & code | `txt` `md` `csv` `tsv` `json` `log` `xml` `html` `css` `go` `js` `ts` `py` `php` `sh` `sql` `yaml` `toml` `ini` … (plus any `text/*` mime) | direct read; invalid UTF-8 and NUL bytes dropped |
| PDF | `pdf` | text layer via a pure-Go reader; **scanned/image-only PDFs yield no text** (that's OCR's job, a separate optional extractor) |
| Office | `docx` `xlsx` `pptx` | native OOXML (zip+XML) walk — document body / shared strings / slide text |

**Limits.** Source files larger than `FILEX_SEARCH_CONTENT_MAX` (default
**5 MiB**) are skipped entirely; extracted text is always capped at
**200 KiB** per file. Corrupt or unextractable files index as "no content" —
never as job failures.

**Query surface.** Search endpoints take a `scope` parameter —
`name` | `content` | `all` (default `all`) — and every hit carries two new
fields: `matched` (`"name"` / `"content"` / `"both"`) and `snippet`, a short
plain-text fragment around the content match with the matched terms wrapped in
`«` `»` (never HTML). With `scope=all`, name hits rank first, so pre-v0.2
clients see the ordering they always did.

**Turning it off / tuning:**

```bash
FILEX_SEARCH_CONTENT=0        # kill-switch: no extraction jobs are enqueued (default: on)
FILEX_SEARCH_CONTENT_MAX=5242880  # per-file source-size cap in bytes (default 5 MiB)
```

```yaml
# config.yaml equivalent
search:
  content: true
  content_max_bytes: 5242880
```

Content extraction also requires the persistent **queue** to be enabled (it is
by default); with `FILEX_QUEUE_ENABLED=false` there is no worker to run the
jobs, so search silently stays name-only.

**Rebuild interaction.** A rebuild **carries extracted text across** — it
copies the `content` field of every document into the replacement index, so
content search is not interrupted and nothing has to be re-derived. Pass
**`?content=1`** to re-extract anyway, for when you have added an extractor or
raised `FILEX_SEARCH_CONTENT_MAX` and want the text derived again rather than
copied; expect a burst of queue jobs proportional to your text-like file count.

> Before v0.30 a rebuild started from an **empty** index and extracted content
> was gone until each file next changed. That is why filex would not rebuild on
> its own — and why the [upgrade](#upgrading-an-existing-index) it shipped for
> issue #15 reached nobody who already had an index.

---

## Configuration

Search is **on by default**:

| Setting | Where | Default | Meaning |
|---|---|---|---|
| `FILEX_SEARCH_ENABLED` | env | `true` | Master switch. Accepts `1` / `true`. When off, no index is opened and search uses the LIKE fallback only. |
| `search.enabled` | `config.yaml` | `true` | Same switch in YAML form. |
| `search.index_path` | `config.yaml` **only** | `<data_dir>/search.bleve` | Where the Bleve directory lives. **No env override** — set it in the file if you want the index somewhere else (e.g. a faster disk). |
| `FILEX_SEARCH_CONTENT` / `search.content` | env / yaml | `true` | [Content search](#content-search) kill-switch — `0` stops enqueueing extraction jobs (already-indexed content keeps matching). |
| `FILEX_SEARCH_CONTENT_MAX` / `search.content_max_bytes` | env / yaml | `5242880` (5 MiB) | Source files above this size are never content-extracted. |
| `FILEX_SEARCH_AUTO_REBUILD` / `search.auto_rebuild` | env / yaml | `true` | Repair an index written by an older document schema, in the background, at startup. `0` leaves it alone — the index keeps reporting `needs_rebuild` and you rebuild when it suits you. See [Upgrading an existing index](#upgrading-an-existing-index). |

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
| `query` | string | — | The search text. May carry `tag:` / `-tag:` filters — see [Query syntax](#query-syntax). |
| `storage_id` | int | `0` (all) | Restrict to one storage. **Required to enable the LIKE fallback** (see below). |
| `limit` | int | `50` | Max results. |
| `scope` | string | `all` | `name` \| `content` \| `all` — which fields to consult (see [Content search](#content-search)). |

Response: `{ "results": [ { …node…, "snippet": "…«term»…", "matched": "name|content|both" }, … ] }`,
already RBAC-filtered and in [rank order](#ranking). `snippet` is `""` for
name-only hits.

```bash
# free text plus a tag filter
curl -X POST https://files.example.com/api/files/search \
  -H 'Content-Type: application/json' -b cookies.txt \
  -d '{ "query": "main go tag:source", "storage_id": 3 }'
```

### `GET /api/files/search?q=…` — same handler

Convenience form for the SPA's toolbar (`?q=`, `?storage_id=`, `?limit=`,
`?scope=`). `q` and `query` are both accepted. Behaves identically to the POST
form.

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

The MCP tool **`file_search`** additionally accepts a boolean `content`
argument (default `true`): content hits come back with `snippet` + `matched`
fields, filtered through the same confinement-root and RBAC checks as the
name search. `content=false` restores the pre-v0.2 name-only behavior.

`q` on this surface speaks the **same query language** as the HTTP endpoints —
separator-blind text and `tag:` / `-tag:` filters — so an agent does not have to
learn a second, smaller syntax. Typo tolerance needs the index: with
`content=false` or no live index, the tool answers from the same LIKE path the
HTTP fallback uses, which is separator-blind but not fuzzy.

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
  "last_updated_at": "",
  "needs_rebuild": false,
  "rebuilding": false
}
```

- `enabled` — `false` when the index isn't wired (search is LIKE-only). The
  other counters are `0`.
- `document_count` — number of indexed nodes.
- `index_size_bytes` — on-disk size of the `search.bleve` directory.
- `last_updated_at` — best-effort timestamp; may be blank.
- `needs_rebuild` — `true` when the index on disk was written by an older
  filex that did not index every field this build queries. Search still works,
  and filex normally repairs this by itself at startup, so seeing `true` means
  either the repair is still running (`rebuilding: true`) or it is switched off
  (`FILEX_SEARCH_AUTO_REBUILD=0`) or it failed — the log says which. See
  [Upgrading an existing index](#upgrading-an-existing-index).
- `rebuilding` — `true` while a replacement index is being built, whether it
  was started by this endpoint or by the automatic repair. It stays `true`
  until the new index is live, and it is why an admin UI can say "rebuilding"
  instead of showing a `needs_rebuild` banner over an index that is already
  being fixed.

### `POST /api/admin/search/rebuild`

Reindexes **every node row** from the database into a replacement index and
swaps it in when it is finished. Returns immediately; the work runs in the
background, and **search keeps answering from the current index the whole
time**. Add **`?content=1`** to also re-enqueue
[content extraction](#content-search) for every eligible file — text already in
the index is carried across either way, so this is for re-deriving it, not for
getting it back.

```bash
curl -X POST https://files.example.com/api/admin/search/rebuild -b cookies.txt
curl -X POST 'https://files.example.com/api/admin/search/rebuild?content=1' -b cookies.txt
```

| Status | Meaning |
|---|---|
| **202 Accepted** | `{ "ok": true, "note": "rebuild started in background" }` — rebuild launched. |
| **400 Bad Request** | `search index disabled` — the index isn't enabled, so there's nothing to rebuild. |
| **409 Conflict** | `rebuild already in progress` — one rebuild at a time, and that includes the automatic repair; wait for it to finish (`rebuilding` on the stats endpoint). |

Internally the rebuild builds a **second index** in `<index_path>.rebuilding`,
verifies it, then swaps it into place under the index lock and deletes the old
directory. Nothing observes a half-built index: a query that arrives during the
swap waits for two directory renames and an index open, then runs against the
new one. It runs on a detached (background) context so it survives the HTTP
request returning — a large tree keeps reindexing to completion.

Two things are worth knowing before you run it on a big instance:

- **Disk.** Two indexes exist at once. Measured on a 20 202-document corpus
  (11.4 MB index): peak 36.2 MB across both directories, i.e. about 2.2x the
  old index in *additional* space. filex refuses to start a rebuild when the
  filesystem cannot hold roughly 4x the current index — it fails loudly, logs
  what it needed and what was free, and **keeps serving the old index**.
- **Time.** The same corpus rebuilt in **3.0 s**. Watch for
  `search: rebuilt index is live` in the log, or `rebuilding` on the stats
  endpoint.

Writes that arrive during a rebuild (uploads, renames, deletes, content
extraction) go into **both** indexes, so nothing that happened while it ran is
lost — or resurrected — at the swap.

Both actions are also exposed to admin tokens as the MCP tools
`admin_search_stats` and `admin_search_rebuild`.

---

## Upgrading an existing index

The forgiving name matching added two indexed fields, `name_norm` and
`path_norm`. Documents written by an older filex do not have them.

**An upgrade needs no action, and search does not get worse.** The
pre-existing sub-queries are still part of every query, precisely so that a
document without the new fields keeps answering exactly what it answered
before; the change adds recall, it never removes any. The separator-blind SQL
fallback covers storage-scoped queries in the meantime, and every document
filex writes or syncs from then on carries the new fields, so recall improves
on its own as files change.

What an un-rebuilt index cannot do for its **existing** documents is the
typo-tolerant pass, and multi-word matching on an unscoped (`storage_id` = 0)
query, where the fallback does not fire.

**filex repairs this by itself.** At startup it compares the document schema
stamped inside the index with the one this build writes, and when they differ it
rebuilds in the background — building the replacement alongside the live index
and swapping it in when it is complete. You do not have to do anything, and
search does not go dark while it happens.

What you see in the log:

```
WARN  search: index document schema is out of date; separator-blind and typo-tolerant
      name matching cannot reach existing files until it is rebuilt
      found_schema="1 (pre-0.29, unstamped)" want_schema=2
INFO  search: the index was built by an older filex; rebuilding it in the background.
      The current index keeps answering every query until the replacement is ready
INFO  search: building a replacement index alongside the current one
      current_index_bytes=11437145 free_bytes=842927378432
INFO  search: rebuilt index is live  reason=schema-upgrade documents=20202
      index_bytes=14894776 took=2.657245423s
```

`needs_rebuild` on the stats endpoint stays `true` until the new index is live —
the index answering queries really is the old one until then — and `rebuilding`
is `true` in the meantime.

Extracted **content is carried across**, document by document, so content search
keeps working throughout and nothing is re-extracted that has not changed. This
is what makes an automatic rebuild safe: before v0.30 a rebuild started from an
empty index, which is exactly why filex would not run one on its own.

### If it does not finish

- **It fails.** The old index keeps serving, `needs_rebuild` stays `true`, and
  the log says why (`search: rebuild failed …`). The half-built directory is
  removed.
- **The disk cannot take two indexes.** Same outcome, refused before anything is
  written: `search: not enough free disk space to rebuild the index`, with the
  bytes it needed and the bytes it found.
- **The container is killed mid-rebuild.** The half-built index is discarded on
  the next start (`search: discarded a half-built index left by an interrupted
  rebuild`) and the repair starts over. It is never swapped in.

### Turning it off

```bash
FILEX_SEARCH_AUTO_REBUILD=0
```

The index is then left exactly as it is: still perfectly usable, still reporting
`needs_rebuild: true`, and you rebuild when it suits you:

```bash
curl -X POST 'https://files.example.com/api/admin/search/rebuild?content=1' -b cookies.txt
```

---

## Failure modes & troubleshooting

### A file I just uploaded doesn't show up in search
Indexing is **best-effort and asynchronous** — a write commits before (and
regardless of whether) the index update lands, and files added straight to a
backend only appear after a sync. Normally the lag is sub-second. If a file is
persistently missing, run a [storage sync](STORAGE.md#sync) or a
`POST /api/admin/search/rebuild` and it will reappear.

### Search feels slow, or a typo finds nothing
You're on the **SQL LIKE fallback**. That happens when `FILEX_SEARCH_ENABLED` is
off, or the Bleve index failed to open at startup (check the logs for
`search index open failed; falling back to SQL LIKE`). The fallback is
separator-blind and handles multi-word queries, but it scans the `name` column
only and cannot do typo tolerance. Fix the index (see next) to get the fast,
multi-field, fuzzy path back.

### `search index open failed` in the logs
The Bleve directory is unreadable, corrupt, or **locked** by another process.
Confirm only one filex instance points at that `index_path`, that filex can
write it, then either restart, or delete the `search.bleve` directory and run a
**rebuild** to recreate it cleanly.

### Substring, separator or typo searches miss rows
Substring and case are handled by the wildcard sub-queries, separators by the
normalised fields, typos by the fuzzy pass (numbers excepted — see
[How it works](#how-it-works)). If those are silently missing while
whole-name matches work, check `needs_rebuild` on the stats endpoint: an index
carried over from an older filex has documents without the normalised fields.
filex repairs that on its own at startup, so `needs_rebuild: true` after a
restart means the repair is still running (`rebuilding: true`), was switched off
with `FILEX_SEARCH_AUTO_REBUILD=0`, or failed — the log says which. See
[Upgrading an existing index](#upgrading-an-existing-index). Otherwise the index
is simply **stale** — trigger a rebuild.

### A `tag:` filter returns nothing
That is what a filter matching nothing looks like, and it is deliberate — the
alternative is answering a mistyped tag with the entire storage. Check the tag
exists (`GET /api/files/manager/tags?storage_id=…`, or the **Tagged files**
page) and remember that several tags in one query are ANDed.

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
