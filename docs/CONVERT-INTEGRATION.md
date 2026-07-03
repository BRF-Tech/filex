# filex ↔ Universal Converter (p2r3/convert) — Integration & Handover

**Status:** deployed to demo + prod (2026-06-17, filex v0.1.25). Core wiring works;
**several rough edges remain — see "Known issues / TODO" at the bottom.** This doc is
the reference for finishing/fixing it later.

---

## What it does

A **Convert** action (right-click context menu **and** the selection toolbar) on any
file. Clicking it opens `ConvertModal`, which drives a **headless, hidden iframe** to a
self-hosted fork of [p2r3/convert](https://github.com/p2r3/convert) over `postMessage`.
The user searches/picks a target format; the conversion runs **in-browser (WASM)** inside
that iframe; the resulting bytes are uploaded back into the **current folder**.

No server-side conversion — everything happens in the browser. GPL-2.0 stays isolated
(convert is a separate deployed service; filex only embeds it via iframe).

## Architecture

```
filex frontend (Vue)
  ConvertModal.vue  ──hidden iframe──►  fm.example.com/convert/?embed=1   (convert fork)
       │  postMessage: {target:'convert-embed', cmd:'listFormats'|'convert', ...}
       │  ◄──────────  {source:'convert-embed', event:'ready' | ok/formats/bytes}
       │
       ├─ fetchArrayBuffer(file.path)        → source bytes  → iframe
       └─ uploadMultipart(currentDir, [File]) ← converted bytes (new ext)
```

### convert fork (separate repo/service)
- Upstream: `github.com/p2r3/convert` (TypeScript/Bun/Vite web app, nginx-served).
- **Our fork change** lives in `src/main.ts`:
  - `setupEmbedBridge()` — active only with `?embed=1`. postMessage protocol:
    - `listFormats` → `{formats:[{index,ext,format,mime,name,from,to}]}` (index = allOptions idx)
    - `convert {name, bytes, fromIndex, toIndex}` → `{name, ext, bytes}` (transferable)
    - posts `{event:'ready'}` once initialised.
  - `buildOptionsFromCache()` — embed-mode fast path: builds `allOptions` + the
    traversion graph from the precached `cache.json` **without** `handler.init()` per
    handler (that was the loading hang). Handlers init lazily during the actual convert.
  - rAF→setTimeout shim (in `setupEmbedBridge`) — hidden iframes throttle
    `requestAnimationFrame`, which stalled conversions (the converter awaits rAF between
    steps). Redirected to `setTimeout(0)` so it runs at full speed headlessly.
- Core engine API used: `window.tryConvertByTraversing(files, fromNode, toNode)`,
  `window.traversionGraph`, `window.supportedFormatCache` (loaded from `cache.json`).

### filex side (this repo)
- **Backend (Go):** `FILEX_CONVERT_URL` env → `config.go` (`ExtServices.Convert`) →
  `server.go` seeds external-service `convert` → `capabilities.go` flattens `convert_url`
  (only when the external service row is `Enabled`). Mirrors the existing drawio wiring.
- **Frontend (`packages/core`):**
  - `modals/ConvertModal.vue` — the modal + searchable picker + iframe bridge + upload.
  - `FileExplorer.vue` — `effectiveConvertUrl` computed; `openConvert()`; context-menu
    `convert` action; toolbar `convert` action; `<ConvertModal>` mount; `onConvertDone`.
  - `components/Toolbar.vue` — `convert` ToolbarAction + `convertEnabled` prop.
  - `types/FileNode.ts` (`Capabilities.convert_url`), `types/ExplorerConfig.ts`
    (`convertBase`), `composables/useFileApi.ts` (default), `locales/{en,tr}.ts` (`ctx.convert`).
  - ⚠ filex's `t(key, vars?)` signature ≠ ConvertModal's `(key, fallback)`, so `:t` is
    **not** passed → ConvertModal uses its built-in **Turkish** fallback strings only.

### Bonus fix shipped alongside
**Large-file upload "missing fields" (e.g. 35 MB):** chunked upload (≥10 MB) needs S3
multipart, which the **local** storage driver doesn't support, and the frontend never
sent `storage_id` → backend `400 "missing fields"`. Fix (`FileExplorer.vue`): chunked
upload now returns a boolean and **falls back to the legacy single-POST upload** when it
fails — works for any storage/size. (The full S3 chunked path — `storage_id` + response
shape `part_urls`↔`parts` + finalize `upload_id` casing — is still mismatched; see TODO.)

## Deploy procedure

### convert fork → fm.example.com/convert  (subpath, both demo + prod vhosts)
```
# on main (Hetzner):
git clone --recursive --depth 1 https://github.com/p2r3/convert /root/convert
# apply our src/main.ts embed patch (scp from the working copy), then:
cd /root/convert && docker build -f docker/Dockerfile -t convert-fork:latest .
docker rm -f convert; docker run -d --name convert --restart unless-stopped \
  -p 127.0.0.1:8080:80 convert-fork:latest
```
Caddy — in your filex site's vhost, route the converter subpath to the
converter container and everything else to filex:
```
@conv path /convert /convert/*
handle @conv { reverse_proxy 127.0.0.1:8080 }
handle { reverse_proxy 127.0.0.1:5212 }
```
Then reload Caddy (`docker exec caddy caddy reload --config /etc/caddy/Caddyfile`). (⚠ `handle` takes a
single matcher — multiple paths need a **named matcher**.) convert nginx serves at
`/convert` (vite base `/convert/`), so the subpath aligns with no rewrite.

### filex → demo + prod  (image built locally on main, NOT pulled)
```
cd /root/filex-src && git fetch --tags && git checkout -f vX.Y.Z
docker build -t filex:vX.Y.Z -f docker/Dockerfile .
# demo  /root/filex (5212, demo-fm.example.com):  image bump + FILEX_CONVERT_URL=https://demo-fm.example.com/convert
# prod  /root/filex-standalone (5213, fm.example.com): image bump + FILEX_CONVERT_URL=https://fm.example.com/convert
cd /root/filex && docker compose up -d
cd /root/filex-standalone && docker compose up -d
```
Release = `git tag vX.Y.Z && git push --tags` (GitLab CI also builds, but deploy uses the
locally-built `filex:vX.Y.Z` tag). Current: **v0.1.25**.

## Known issues / TODO (fix later)

1. **Conversion reliability not yet verified end-to-end.** After the speed fixes
   (cache fast-path + rAF shim) the loading + small-doc hang should be resolved — but the
   actual cross-format conversions need real testing (which handlers work, which paths
   dead-end, error surfacing in the modal).
2. **Format picker coverage:** the embed fast path lists only formats present in
   `cache.json`. Handlers not covered by the precache won't appear. Verify `cache:build`
   covers the formats you care about; otherwise the picker is incomplete.
3. **First conversion per handler is still slow** — loading that tool's WASM is inherent
   (one-time per handler per session). Consider warming common handlers, or a progress
   indicator during `convert` (currently just "Dönüştürülüyor…").
4. **i18n:** ConvertModal is Turkish-only (the `:t` mismatch). To localise, change the
   prop to `(key:string)=>string`, pass filex `t`, and add `convert.*` keys to locales.
5. **S3 chunked upload still broken** (only local-fallback works). If S3 storages are
   ever used, fix the init payload (`storage_id`) + response shape (`part_urls` vs
   `parts`) + finalize field casing (`upload_id`).
6. **Menu parity is action-level, not visual** — context menu (popup) vs toolbar (row)
   look different by design; right-click also has cut/copy/duplicate/tags that the
   compact toolbar omits.
7. **Big files through convert:** conversion holds the whole file in memory (browser) +
   transfers via postMessage. Large media may be slow/OOM in the tab.
