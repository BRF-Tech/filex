## STL viewer fail (ASCII format)

**File:** s3-test://example/cube.stl (141 bytes, ASCII STL)
**URL:** /admin/files/edit?path=...&type=stl
**Symptom:** Page mounts (title=cube.stl), no canvas rendered (canvasCount=0)
**Console error:**

```
SyntaxError: Unexpected token 's', "solid cube"... is not valid JSON
    at JSON.parse (<anonymous>)
    at Qw.parse (model-viewer-C2won-2y.js:4539:6546)
    at Object.onLoad (model-viewer-C2won-2y.js:4539:5821)
```

**Root cause hypothesis:** STL ASCII files start with `solid <name>` magic string. The viewer is calling `JSON.parse` on the file contents — likely the wrong loader (Object/JSON loader) is being picked up by the dispatcher rather than three.js `STLLoader` which auto-detects ASCII vs binary by checking magic bytes.

**Repro:** mount STL file with ASCII content; observe console.

**Severity:** medium — affects all ASCII-STL files. Binary STL likely OK (no `solid` magic, JSON.parse would still fail but error message differs).

---

## OBJ viewer fail (same dispatcher bug as STL)

**File:** s3-test://example/cube.obj (64 bytes, ASCII OBJ)
**URL:** /admin/files/edit?path=...&type=obj
**Symptom:** Page mounts (title=cube.obj), no canvas rendered
**Console error:** `SyntaxError: Unexpected token '#', "# minimal "... is not valid JSON` at same `Qw.parse` site as STL.

**Same root cause as STL bug:** dispatcher always picks JSON loader regardless of `?type=`.

---

## GLB viewer renders to zero-size canvas

**File:** s3-test://example/cube.glb (104 bytes)
**URL:** /admin/files/edit?path=...&type=glb
**Symptom:** Page mounts (title=cube.glb), no console errors, parser is correct (no JSON.parse fail), but `canvasCount=0` in DOM **and** WebGL warnings:

```
[.WebGL-...] GL_INVALID_FRAMEBUFFER_OPERATION: glClear: Framebuffer is incomplete: Attachment has zero size.
[.WebGL-...] GL_INVALID_FRAMEBUFFER_OPERATION: glDrawElements: Framebuffer is incomplete: Attachment has zero size.
```

**Root cause hypothesis:** WebGL context initialized but the canvas element is mounted into a 0×0 container (parent has no laid-out size). Likely flexbox/grid layout in the viewer page didn't give the model-viewer wrapper any height. Note: `document.querySelectorAll('canvas')` returns 0 — canvas may be inside a shadow DOM (model-viewer custom element is web component) and not picked up by light-DOM query.

**Severity:** medium — affects all GLB/glTF previews. Probably also affects STL/OBJ if they were rendering at all (currently failing earlier at parser).


---

## Share modal frontend not wired (UI dead-end)

**Symptom:** UI Share modal opens, accepts PIN/expiry/max-downloads, "Paylaş" button does nothing. Console warning:

```
[explore] FileExplorer error: { message: "shareCreate endpoint not configured", context: ... }
```

**Backend works fine** when called directly:

```bash
curl -X POST /api/files/share -d '{"path":"s3-test://example/users.csv","pin":"1234","max_downloads":3}'
# 200 → { token, url: "https://fm.brf.sh/s/<token>", has_pin: true, ... }
```

**Root cause:** `@brftech/file-explorer` package config in `Modules/FileManager/resources/admin` (or wherever it mounts) doesn't pass a `shareCreate: '/api/files/share'` mapping into the `apiBase` config.

**Severity:** high — share UI is end-user visible but completely non-functional.

---

## Public share PIN flow: presigned S3 URL signature mismatch

**Setup:** create share via API (works), retrieve `/s/<token>`.

| Step | Status |
|---|---|
| GET `/s/<token>` (no PIN) | 200 HTML form ✓ |
| POST `/s/<token>` `pin=0000` (wrong) | 401 + "Wrong" message ✓ |
| POST `/s/<token>` `pin=1234` (correct) | 302 → presigned S3 URL ✓ |
| Follow redirect to S3 | **403 `SignatureDoesNotMatch`** ✗ |

**Server response (xml):**

```xml
<Error><Code>SignatureDoesNotMatch</Code>...
```

**Presigned URL (decoded):**

```
https://brf.nbg1.your-objectstorage.com/filex-test/example/users.csv
  ?X-Amz-Algorithm=AWS4-HMAC-SHA256
  &X-Amz-Credential=1QRDCWIOTQP9Q0J3KF1L/20260508/nbg1/s3/aws4_request
  &X-Amz-Date=20260508T225758Z
  &X-Amz-Expires=300
  &X-Amz-SignedHeaders=host
  &X-Amz-Signature=6224c3b07abc94f10b2eed22aadd904b...
```

**Hypothesis:** signature canonical-string drift. Common causes:
- Region in credential is `nbg1` but server expects different (Hetzner obj storage canonical region is also `nbg1` — ok)
- `X-Amz-SignedHeaders=host` only — but server may verify additional headers that backend signed (or vice versa)
- Path-style vs virtual-host-style: URL is virtual-host-style (`brf.nbg1.your-objectstorage.com`) but signature might have been computed for path-style (or backend's `s3.endpoint` is `nbg1.your-objectstorage.com` path-style and `s3.use_path_style: false` was applied incorrectly)
- Clock skew in signed timestamp (unlikely; servers within 5 min)

**Severity:** high — public share file delivery is broken end-to-end. Users will get 403 XML when clicking the generated link.

**Repro test:** any share with backend → S3 presigned redirect → 403 SignatureDoesNotMatch.

**Note:** PIN validation logic itself is correct (302 vs 401). The bug is *only* in the AWS Signature V4 generation against the bucket.


---

## Pending ops tray never appears (copy/move frontend wiring missing)

**Symptom:** "Kopyasını Oluştur", "Kopyala" + "Yapıştır", and presumably "Kes" + "Yapıştır" all fail silently from the UI. No tray, no toast, no copy in the grid.

**Console warning:**

```
[explore] FileExplorer error: { message: "copy endpoint not configured", context: ... }
```

**Backend exists** (probed against `/api/files/copy` POST):

```
POST /api/files/copy {} → 400 {"error":"missing source"}
POST /api/files/move {} → 400 {"error":"missing source"}
```

…so the route is wired and accepts JSON; the frontend `@brftech/file-explorer` package's `apiBase` config is missing `copy`/`move` entries (same family of bug as `shareCreate`).

**Body shape** for backend: tried `from/to`, `src/dst`, `source_path/target_path`, `source/target`, `paths/destination` — all returned `missing source` (or `bad json` for `source/target`). Backend probably wants something specific (likely `source` + `dest`, or a different envelope). Frontend integration would have used the right shape, but it never gets called.

**Severity:** high — copy/move/duplicate are core file-manager primitives. Currently 100% UI-broken even though backend works.

**Likely-related missing endpoints** (not verified): `move`, `duplicate` (in dropdown as "Kopyasını Oluştur"), `paste` action. Also likely: drag-and-drop within the grid (move via DnD).


---

## Bug 25 — Surfaced after v0.1.1 fix of bug 24: Kopyasını Oluştur sends source==destination

**Discovered:** 2026-05-09 during v0.1.1 re-verify of bug 24 fix.

**Symptom:** UI right-click → "Kopyasını Oluştur" now reaches the backend (POST /api/files/copy → 202), but the async op fails:

```
ops: step failed op=1 kind=copy src=example/users.csv
err="api error InvalidRequest: This copy request is illegal because
it is trying to copy an object to itself without changing the object's
metadata, storage class, website redirect location or encryption attributes."
```

**Root cause hypothesis:** the duplicate dispatcher sends `source` and `destination` set to the **same path**. Either the auto-suffix (`users-copy.csv`) is meant to be done client-side and isn't, or the backend `copy` handler is meant to apply auto-suffix when source == destination and isn't.

**Severity:** medium — UI now reaches backend (huge improvement over bug 24's silent dead-end), but the duplicate flow still can't complete. Cross-directory copy via Kopyala+Yapıştır may work fine.

**Verification:** confirmed by `docker logs filex-standalone` after clicking Kopyasını Oluştur on users.csv inside `example/`.

**Fix candidates:**
- Backend: when destination == source, append `-copy[N]` to filename until unique.
- Frontend (`@brftech/file-explorer` ContextMenu duplicate handler): construct the destination path with collision-suffix before POSTing.

