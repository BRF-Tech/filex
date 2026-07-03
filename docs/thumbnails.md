# Thumbnails

filex renders preview thumbnails **server‑side** for images, video, audio, PDFs,
office documents and SVGs, and a coloured placeholder card for everything else.
Thumbnails are **on by default** — the grid view shows a real preview where one
exists and falls back to a per‑type icon where it doesn't.

The image and placeholder generators are pure Go and always work. The richer
kinds (video, audio, PDF, office, SVG) each shell out to an **external tool**
that filex **auto‑detects on `PATH`** at startup — if the tool is missing, that
kind degrades gracefully instead of erroring.

- [How it works](#how-it-works)
- [Generators & required tools](#generators--required-tools)
- [Configuration](#configuration)
- [The Docker image & bundled tools](#the-docker-image--bundled-tools)
- [Serving](#serving)
- [Backfill — catching up existing files](#backfill--catching-up-existing-files)
- [What happens if it isn't configured / a tool is missing](#what-happens-if-it-isnt-configured--a-tool-is-missing)
- [Failure modes & troubleshooting](#failure-modes--troubleshooting)
- [See also](#see-also)

---

## How it works

The pipeline (`backend/internal/thumb/`) is a **dispatcher**: it inspects each
file node's MIME type — falling back to the file extension when the MIME is
empty, which is the common case for files discovered by a storage sync — and
routes it to exactly one generator.

Every generator writes a **JPEG** to the cache directory as
`<cache_dir>/<nodeID>.jpg` (regardless of source kind, the cache file is always
`<id>.jpg`, roughly **320 px** on the long edge) and updates a row in the
`thumbnails` table with a **state**:

| State | Meaning |
|---|---|
| `pending` | Dispatched, not finished yet (or left over from a crash). |
| `ready` | A JPEG is cached and servable. |
| `skipped` | No generator applied (e.g. SVG with no `rsvg-convert`). Not an error. |
| `failed` | A generator ran but errored (broken file, tool crash). Logged at WARN with the error stored on the row. |

Generation is triggered two ways:

1. **After upload** — the moment an upload (or a public file‑drop) commits, filex
   dispatches the pipeline in a **detached background goroutine** with a **90‑second
   timeout**. The HTTP request returns immediately; a client disconnect can't abort
   an in‑flight office→PDF conversion. Errors are swallowed (the pipeline logs its
   own).
2. **Backfill** — a one‑shot pass over files that already exist in the cache (see
   [Backfill](#backfill--catching-up-existing-files)).

---

## Generators & required tools

| Kind | Source types | Generator | External binary (auto‑detected on `PATH`) |
|---|---|---|---|
| **Image** | `image/*` — jpg, png, gif, bmp, tiff, webp | Built‑in Go (stdlib + `x/image`) | **none** |
| **Video** | `video/*` — mp4, webm, mov, mkv, avi, … | `ffmpeg` — first frame at ~1 s, scaled to 320 wide | `ffmpeg` |
| **Audio** | `audio/*` — mp3, wav, ogg, flac, m4a, aac, opus | `ffmpeg` — a 320×120 waveform image (`showwavespic`) | `ffmpeg` |
| **PDF** | `application/pdf` | Ghostscript renders page 1 at 96 dpi (falls back to poppler) | `gs` **or** `pdftoppm` |
| **Office** | doc, docx, xls, xlsx, ppt, pptx, odt, ods, odp | LibreOffice headless → PDF → page 1 rendered like a PDF | `libreoffice` (or `soffice`) **and** one of `gs` / `pdftoppm` |
| **SVG** | `image/svg+xml` | librsvg rasterises to PNG → re‑encoded to JPEG | `rsvg-convert` |
| **Placeholder** | everything else — archives, 3D models, code, markdown, rtf, raw docs, … | Built‑in Go — a tinted card with the extension centred (colour hashed from the extension) | **none** |

Notes:

- **Images** decode with the Go standard library plus `golang.org/x/image`
  (BMP / TIFF / WebP), capped at **~50 MB** of decoded input, and are downscaled
  to fit **320×320** (aspect preserved; larger sources only) and encoded at JPEG
  quality **80**. Formats Go can't decode — e.g. **HEIC / AVIF** — will `state=failed`.
- **SVG is checked before the generic `image/*` branch**, because Go's decoder
  can't parse SVG. If `rsvg-convert` isn't present the SVG is cleanly
  **`skipped`**, never failed.
- **Office** goes through **two** tools: LibreOffice to make a PDF, then
  Ghostscript/poppler to rasterise page 1. It also wants a **JRE** and **fonts**
  present for reliable conversion (the stock full image ships both).

---

## Configuration

| Setting | Default | Where | Meaning |
|---|---|---|---|
| `FILEX_THUMBS_ENABLED` | `true` | env | Master switch. Accepts `1` or `true` (case‑insensitive) as **on**; any other value is off. |
| `FILEX_THUMB_BACKFILL_ON_BOOT` | *(unset)* | env | Set `once` (or `true` / `1`) to run one background backfill on startup. See [Backfill](#backfill--catching-up-existing-files). |
| `thumbs.cache_dir` | `<data_dir>/thumbs` | **config.yaml only** | Directory the cached `<id>.jpg` files live in. No env override. |
| `thumbs.formats` | `[image, video, pdf, office]` | **config.yaml only** | Declares the kind list. No env override. |

There is **no env var or config key for the external tools** — filex probes
`PATH` at boot (`ffmpeg`, `gs`, `pdftoppm`, `libreoffice`/`soffice`,
`rsvg-convert`) and enables each kind accordingly. In practice a kind renders
when its MIME type matches **and** its tool is present; `cache_dir` is the
`thumbs.*` value read at runtime.

---

## The Docker image & bundled tools

Image thumbnails and placeholder cards work on **any** image, including the
smaller **`:slim`** image, because they need no external binary.

The default **`ghcr.io/brf-tech/filex:latest`** image bundles the tools that
unlock the richer kinds:

```
ffmpeg          → video + audio thumbnails
ghostscript     → PDF (page 1)  ┐ office docs render via
poppler-utils   → PDF fallback  ┘ LibreOffice → PDF → these
libreoffice     → doc/docx/xls/xlsx/ppt/pptx/odt/ods/odp
openjdk17-jre   → LibreOffice's conversion pipeline
fonts (noto/liberation/dejavu)  → so office/PDF text isn't rendered as boxes
```

> ⚠ The stock `full` image does **not** ship `rsvg-convert` (librsvg), so **SVG
> thumbnails are `skipped`** on it. If you need SVG previews, add librsvg to the
> image (`apk add rsvg-convert`) and rebuild. Whatever image you run, the
> definitive check for what's actually present is the
> [capabilities probe](#serving) (`thumbs.svg`, `thumbs.video`, …).

If you build your own leaner image, drop tools from the install list — the
capability probe will report `video=false` / `pdf=false` / etc. and the pipeline
routes around the missing generators automatically.

---

## Serving

```
GET /api/files/thumb/{id}
```

- Returns **404** unless the node's thumbnail state is **`ready`** and the cached
  JPEG exists on disk.
- On success: `Content-Type: image/jpeg` and `Cache-Control: private, max-age=86400`
  (cache for **1 day**).
- **Auth‑light.** The endpoint accepts either a normal authenticated **session**
  (the SPA's grid uses this) **or** an optional **signed URL** — `?sig=<hex hmac>`,
  an HMAC‑SHA256 of the id under the daily‑rotated `thumb_signing_key` setting.
  A bad id returns **400**; a bad signature returns **403**.
- File listings include a `thumb_url` per node so the grid knows where to fetch.

Capabilities (used by the UI and handy for debugging) are exposed at
`GET /api/files/capabilities` (legacy alias `GET /api/capabilities`) under
`thumbs`:

```bash
curl https://files.example.com/api/files/capabilities | jq .thumbs
```
```json
{ "image": true, "imagemagick": true, "video": true, "audio": true,
  "pdf": true, "office": true, "svg": false }
```

(The probe result is cached for 1 hour.)

---

## Backfill — catching up existing files

New uploads get a thumbnail automatically. Files that entered the cache another
way — a storage **sync**, or an install that previously ran **without** the
tools — do **not**, so their rows stay empty. The `thumb backfill` command walks
every file node and (re)dispatches the pipeline:

```bash
filex thumb backfill                     # every enabled storage
filex thumb backfill --storage local     # one storage, by name
filex thumb backfill --storage 2         # one storage, by id
filex thumb backfill --limit 100         # stop after 100 files (across all storages)
filex thumb backfill --retry-failed      # also re-run rows in state=failed
filex thumb backfill --retry-skipped     # also re-run rows in state=skipped
filex thumb backfill --concurrency 8     # worker pool size (default 4)
filex thumb backfill --progress-every 50 # progress line every N files (default 25)
```

Which files are (re)processed:

| Existing state | Re‑run? |
|---|---|
| *(no row)* / `pending` | Always. |
| `ready` | Never (idempotent). |
| `skipped` | Only with `--retry-skipped`. |
| `failed` | Only with `--retry-failed`. |

The walk skips trashed and soft‑deleted nodes. It ends with a summary line —
`{processed: N, ok: M, failed: K, skipped: S}` — and exits **non‑zero only on
infrastructure errors** (DB unreachable, unknown `--storage`, …); per‑file
failures are counted into `failed` but don't abort the run.

> ⚠ **Search index lock.** A running `filex serve` holds an exclusive lock on the
> Bleve (boltdb) search index. Backfill never touches search, so it **disables
> the index for its run** (sets `FILEX_SEARCH_ENABLED=false` unless you've
> already set it) — otherwise it would block indefinitely acquiring that lock.
> Only override `FILEX_SEARCH_ENABLED=true` when running backfill on a stopped
> node.

### Boot‑time backfill

For containers where you want each restart to make sure the grid is painted:

```
FILEX_THUMB_BACKFILL_ON_BOOT=once
```

(values `once`, `true`, `1` are equivalent; anything else leaves it off). When
set, `serve` launches **one** background backfill a couple of seconds **after**
the HTTP listener is up — so the boot path stays fast — and logs progress at INFO
via `slog` (`thumb backfill (boot): starting one-shot backfill`). It's off by
default; most operators prefer to trigger backfills explicitly.

---

## What happens if it isn't configured / a tool is missing

- **Thumbnails are on by default.** With zero external tools you still get real
  image previews plus placeholder cards for everything else.
- **Missing tool for video / audio / PDF / office** → that kind can't be enabled,
  so the dispatcher routes the file to the **generic placeholder card**. The state
  is **`ready`**, *not* `failed` — the grid shows a legible tinted card with the
  extension, just not a real preview.
- **SVG with no `rsvg-convert`** → state **`skipped`** (reason: `rsvg-convert not
  in PATH`). No placeholder is drawn; the UI shows its own SVG icon.
- **A generator that runs but errors** (tool present, but the file is broken /
  truncated / unsupported) → state **`failed`**, a WARN is logged, and the error
  text is stored on the row.
- **Unsupported / other kinds** (archives, 3D models, code, markdown, rtf, raw
  docs, …) always get the placeholder card (`ready`).

---

## Failure modes & troubleshooting

### The grid shows icons, not previews
The thumbnail isn't `ready`. Inspect the `thumbnails` table:

```bash
sqlite3 <data_dir>/instance.sqlite \
  "SELECT node_id, state, error FROM thumbnails ORDER BY node_id DESC LIMIT 20;"
```

`ready` rows serve a JPEG; `failed` rows carry the generator error in `error`;
`skipped`/absent rows fall back to the per‑type icon.

### Existing files never got thumbnails after I added the tools
Uploads generate automatically, but files already in the cache don't. Run
`filex thumb backfill` (or set `FILEX_THUMB_BACKFILL_ON_BOOT=once`). If a whole
storage is empty, run a **sync** first — backfill only walks nodes already in the
cache.

### Office documents land in `state=failed`
LibreOffice converted but the second stage failed, or LibreOffice itself did.
Usual causes: **no PDF renderer** (`gs`/`pdftoppm` missing → LibreOffice succeeds
but there's nothing to rasterise the PDF), a **missing JRE** or **fonts**, or a
**corrupt/truncated** source doc. The stock `full` image already ships the JRE
and fonts; check the `error` column for the LibreOffice/Ghostscript output.

### SVGs never render
`rsvg-convert` isn't on `PATH` — the capabilities probe shows `thumbs.svg:false`
and rows are `skipped`. The stock `full` image omits librsvg; install it
(`apk add rsvg-convert`) and re‑run with `--retry-skipped`.

### PDF or video previews are blank / missing
If the tool is entirely absent the file becomes a **placeholder** (`ready`), not
a failure. If the tool is present but the row is **`failed`**, read the stored
error — a broken PDF, an unreadable codec, or a permissions issue on the temp dir.

### HEIC / AVIF images fail
Go's decoder only handles JPEG, PNG, GIF, BMP, TIFF and WebP. HEIC/AVIF sources
end up `failed`. Convert them, or add an external converter upstream.

### Backfill seems to hang
It's almost certainly the search‑index lock — see the callout above. Backfill
disables search for its run by design; don't force `FILEX_SEARCH_ENABLED=true`
while `filex serve` is live.

### A regenerated file shows the old thumbnail
The cache lives at `<data_dir>/thumbs/<id>.jpg` and is safe to delete. Remove the
stale file (or the whole `thumbs/` dir) and re‑run `filex thumb backfill
--retry-failed` — the cache is regenerated lazily.

---

## See also

- [CONFIGURATION.md](CONFIGURATION.md) — full config/env reference
- [DOCKER.md](DOCKER.md) — image variants (slim vs full) and compose profiles
- [STORAGE.md](STORAGE.md) — storages and sync (where uploaded/synced files come from)
- [INSTALLATION.md](INSTALLATION.md) — running filex
