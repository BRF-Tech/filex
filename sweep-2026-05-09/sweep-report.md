# Filex v0.1.0 Production Sweep — 2026-05-09

**Live image:** `registry.gitlab.com/brftech/filemanager:v0.1.0`
(slim variant, sha256:`3e3f8aeca48b59d65899df226ae3d3cb81624be4ce608e19bb86003d854435d3`,
retag of `slim-v0.1.3`)
**Live host:** https://fm.brf.sh (filex-standalone container, healthy)
**Git tag:** `v0.1.0` on commit `10d2334`

## TL;DR

Plan A (deploy + tag) ✓ done. Plan C (manual sweep) executed via Playwright MCP; **6 net-new bugs discovered**, none of which are regressions of the 18 fixed in rounds 1-13. The backend is mostly solid; the **frontend `@brftech/file-explorer` package's `apiBase` mapping is incomplete** — three end-user features (share, copy, move) are dead-ends in the UI even though the backend implementations exist. Two viewer bugs (STL/OBJ, GLB) and one cross-cutting infra bug (S3 presign signature mismatch) round out the list.

## Sweep matrix

| # | Test | Result | Notes |
|---|---|---|---|
| 1 | mp4 viewer | ✓ | 1920×1080, dur 5.76s, ready=4 |
| 2 | mp3 viewer | ✓ | dur 2.36s, ready=4 |
| 3 | stl viewer | 🐛 | ASCII STL → `JSON.parse` SyntaxError |
| 4 | obj viewer | 🐛 | Same dispatcher bug |
| 5 | glb viewer | 🐛 | Zero-size canvas (WebGL `Attachment has zero size`) |
| 6 | ipynb viewer | ✓ | 12 cells rendered |
| 7 | epub viewer | ✓ | iframe "Chapter 1" |
| 8 | drawio viewer | ✓ | embed.diagrams.net iframe |
| 9 | mmd viewer | ✓ | SVG 1048×576 |
| 10 | psd viewer | ⚠ | Mounts, but canvas 1×1 (fixture is 43 B → degenerate) |
| 11 | tiff viewer | ⚠ | Mounts, but canvas 1×1 (fixture is 123 B → degenerate) |
| 12 | trash sil → restore | ✓ | 32→31 cards, tombstone `1778280908-3b7e77__users.csv`, 30-day retention; restore brings users.csv back, 31→32 |
| 13 | share PIN backend | ✓ | `POST /api/files/share` returns token + URL |
| 14 | share PIN frontend | 🐛 | UI Paylaş button does nothing — `shareCreate endpoint not configured` |
| 15 | share PIN public flow | 🐛 | Wrong PIN ✓ 401, correct PIN ✓ 302 → S3 presign — but presigned URL gives **`SignatureDoesNotMatch`** at S3 |
| 16 | OnlyOffice editor mount | ✓ | docs.brf.sh iframe loaded; backend `/onlyoffice/{config,callback,fetch}` all 200 in container log |
| 17 | drag-drop in-page img regression | ✓ | Dragging a thumbnail does NOT open the upload overlay |
| 18 | pending ops tray (copy/move) | 🐛 | UI silent — `copy endpoint not configured`. Backend `POST /api/files/copy` exists |

✓ = pass · ⚠ = fixture-driven, behavior is plausibly correct · 🐛 = real bug

## Bug list (6)

Detailed in `bugs.md` next to this file. Numbered per Round-style convention,
continuing from the round 1-13 series:

| # | Bug | Severity | Surface |
|---|---|---|---|
| 19 | STL viewer dispatcher routes ASCII STL to JSON loader | medium | viewer |
| 20 | OBJ viewer same dispatcher bug | medium | viewer |
| 21 | GLB viewer mounts in zero-size container | medium | viewer / CSS |
| 22 | Share modal "Paylaş" does nothing — `shareCreate` not wired | high | frontend wiring |
| 23 | Public share follow-redirect → S3 `SignatureDoesNotMatch` | high | backend (S3 sigv4) |
| 24 | Copy/Paste/Duplicate UI silent — `copy` not wired | high | frontend wiring |

Bugs 22+24 are the same family — the consuming app's mount config for
`@brftech/file-explorer` skips several `apiBase` keys. A one-line fix per
endpoint should clear both. Bug 23 is the more serious infra concern (broken
public file delivery). Bugs 19/20 are likely a single-line dispatcher fix
(switch on `?type=` properly). Bug 21 is the most CSS-flavored.

## Things that the e2e regression suite (15/15) covered correctly

- mp4/mp3/ipynb/epub/drawio/mmd viewers
- trash round-trip
- OnlyOffice integration (live container log shows the trio: config/callback/fetch)
- drag-drop overlay regression (the original round-7 fix is intact)
- backend share, copy, move endpoints exist and respond

## Things the suite missed

- The frontend never **calls** `share`/`copy`/`move`, so backend-only assertions kept the suite green
- 3D viewers (STL/OBJ/GLB) — likely no fixture-driven assertion that the canvas actually renders (only that the page mounts without throwing)
- S3 presign correctness against the real Hetzner endpoint

## Recommended follow-up order

1. **Bug 22+24+other apiBase mappings** — audit the consuming admin SPA's mount config for `@brftech/file-explorer` and add **all** missing keys (share, copy, move, duplicate, …). Single-PR fix, unblocks three end-user features.
2. **Bug 23** — investigate Hetzner Object Storage presign generation. Likely a `path-style` vs `virtual-host-style` flag mismatch in the storage driver, or a missing canonical header in sigv4. Repro is trivial (any share with a real bucket).
3. **Bugs 19+20** — fix the model-viewer dispatcher to honor `?type=` instead of falling through to JSON loader.
4. **Bug 21** — give the model-viewer wrapper a concrete CSS height (or use the model-viewer custom-element's recommended `style="height: …"`).
5. Optional: re-seed PSD/TIFF fixtures with non-degenerate content, then assert pixel dimensions in e2e.

## Deploy artifacts

- compose backup: `ssh main 'ls /root/filex-standalone/*.bak*'`
- registry tags now: `slim-v0.1.3`, `slim`, `full-v0.1.3`, `full`, `v0.1.3`, `v0.1.0`, `latest`
- registry login on main: `claude / glpat-…` (PAT) — superset of original deploy token
- pipeline #85 was canceled (load on main + manual deploy made tag pipeline redundant)

## Browser sweep methodology note

Used `playwright-host` MCP. Each viewer test = navigate + 2-4 s wait + JS state probe (canvas count, video readyState, errors). Stale Chromium instances locked
the user-data-dir between sessions; `Stop-Process -Force` against `*mcp-chrome-4afef79*` resolved every time. ~30 min total wall-clock for the sweep.
