# Thumbnails

Filex generates thumbnails on the backend and serves them via
`GET /api/files/thumb/{nodeID}`. The frontend SFC reads `thumb_url`
from the `vfIndex` payload and falls back to a per-mime icon when the
endpoint 404s.

## Supported formats

The pipeline (in `backend/internal/thumb/`) dispatches by MIME:

| Source                                    | Generator      | Required binary       |
| ----------------------------------------- | -------------- | --------------------- |
| `image/*` (jpg/png/gif/webp/…)            | Go stdlib + GD | none                  |
| `video/*` (mp4/mkv/mov/webm/…)            | ffmpeg         | `ffmpeg`              |
| `application/pdf`                         | Ghostscript    | `gs` *or* `pdftoppm`  |
| Office docs (doc/docx/xls/xlsx/ppt/pptx/odt/ods/odp) | LibreOffice headless → PDF → Ghostscript | `libreoffice` (or `soffice`) **and** one of `gs` / `pdftoppm` |
| Anything else                             | (skipped)      | —                     |

Image thumbs always work — the Go binary ships everything it needs.
The other three rely on tools probed at runtime through
`exec.LookPath`. The probe results power `GET /api/capabilities`
(`thumbs.image|video|pdf|office`) and the dashboard cards.

## Docker image

`docker/Dockerfile` (the canonical recipe) installs every dependency in
a single `apk add` layer:

```
ffmpeg ghostscript poppler-utils libreoffice
ttf-liberation ttf-dejavu font-noto font-noto-cjk
```

Result: `brftech/filex:latest` is ~600–700 MB (was ~40 MB without the
toolchain). The bulk is libreoffice; the fonts are required so
non-ASCII glyphs in office docs and PDFs render instead of tofu.

If you build your own slim variant for a specific subset (e.g.
ffmpeg-only), drop the others from the apk list and the capability
probe will report `office=false` etc. — the pipeline routes around
missing generators automatically and marks unsupported nodes
`state=skipped`.

## Backfill

When you upgrade an existing deployment from a binary that lacked
the deps, the existing nodes already have empty `thumbnails` rows
(or none at all). New uploads get thumbs automatically because
`upload.go::Finalize` dispatches the pipeline; existing rows do
not. Use the `thumb backfill` subcommand to catch up:

```bash
# Walk every enabled storage, dispatch pipeline for files
# without a usable thumbnail.
filex thumb backfill

# Restrict to one storage (by id OR logical name).
filex thumb backfill --storage demo
filex thumb backfill --storage 2

# Cap the number of files processed (across all storages).
filex thumb backfill --limit 100

# Re-run thumbnails currently in state=failed (e.g. after fixing
# a font, upgrading libreoffice, or installing missing fonts).
filex thumb backfill --retry-failed

# Wider worker pool / quieter / louder.
filex thumb backfill --concurrency 8 --progress-every 50
```

Behaviour:

- Files whose `thumbnails` row is `state=ready` or `skipped` are
  left alone (idempotent).
- Files in `state=failed` are skipped by default; pass
  `--retry-failed` to re-run them.
- Files in `state=pending` (left over from a crash) and files
  with no row at all are always re-run.
- The producer walks the cache via `ListNodesByParent`, so pre-sync
  storages are not touched. Run a sync first if you want a brand-new
  storage included.
- Output ends with `{processed: N, ok: M, failed: K, skipped: S}`.
  Exit code is non-zero only on infra errors (DB unreachable,
  unknown `--storage`, …); per-file failures are counted into
  `failed` but do not abort the run.

## Boot-time backfill

For containerised deployments where you want every restart to make
sure the cache is painted, set:

```
FILEX_THUMB_BACKFILL_ON_BOOT=once
```

(values `once`, `true`, `1` are equivalent; anything else disables
it). When set, `serve` launches a background goroutine **after** the
HTTP listener is up — boot path stays fast — and runs a full
backfill exactly once per process start. Progress is logged at
INFO level via `slog`:

```
thumb backfill (boot): starting one-shot backfill
…
thumb backfill (boot): done processed=19 ok=14 failed=0 skipped=5
```

The flag is intentionally off by default — most operators want to
trigger backfills explicitly via `filex thumb backfill`.

## Verifying thumbnails

1. **Capabilities probe**

   ```bash
   curl http://127.0.0.1:5212/api/capabilities | jq .thumbs
   ```

   All four flags should be `true` on the canonical image:

   ```json
   { "image": true, "video": true, "pdf": true, "office": true }
   ```

   If `pdf` or `office` is `false`, the corresponding binary is
   missing from `$PATH` inside the container (`gs` / `pdftoppm` or
   `libreoffice` / `soffice`).

2. **One-off generation**

   Drop a sample file into a configured storage, sync, and check:

   ```bash
   sqlite3 /data/instance.sqlite \
     "SELECT node_id, state, error FROM thumbnails ORDER BY node_id DESC LIMIT 10;"
   ```

   Healthy rows are `state=ready`. `failed` rows include the
   generator error in `error` (libreoffice stderr, gs failure, …).

3. **HTTP**

   ```bash
   curl -I http://127.0.0.1:5212/api/files/thumb/<node_id>
   ```

   Returns `200` with `Content-Type: image/jpeg` when the cache file
   exists, `404` otherwise.

4. **GridView in the SFC**

   Switch a folder to grid layout. Files with a generated thumb
   render the JPEG; files without fall back to the per-extension
   icon. Backfilling against a folder of 19 demo files should paint
   every supported MIME (image / video / pdf / office) immediately.
