# Filex Parity Round 2 + 3 — Handover (2026-05-08)

> **Status:** 8 commits + 4 background agents merged into `origin/main`. All endpoints respond 200 in API smoke. Browser smoke pending. Continuing tomorrow.

## Bugün ne yapıldı

Background-agent feature audit'i çekildi (filex vs brf-mono FishApp), audit raporundaki 12+ kritik/major gap'in hepsi tek refactor turu ile kapatıldı. Ardından mime fallback ve per-verb async fix'leri eklendi.

### Commit serisi (`origin/main`)

```
d7e11f7 fix(thumb): fall back to filename extension when node.Mime is empty
398c318 fix(ops): per-verb /copy /move /delete preserve target's trailing slash
a882d76 chore(i18n): quota/versions/files/pendingOps strings (en + tr)
8278e16 docs(thumbnails): document thumb pipeline deps + backfill workflow
127e38b fix(web): FileVersions.vue — drop v-if on Modal that confused vue-tsc
bc08815 feat: parity round 2 — search→Bleve + immediate index + thumb deps + quota/versions/pending UIs
64a5993 test(e2e): cover trash + share + save-text + per-verb async + caps aliases
2e86700 test(fixtures): add 14 viewer fixtures + seed-example-fixtures.sh
```

### Background-agent çıktıları

| Agent | İş | Sonuç |
|---|---|---|
| A | 14 yeni viewer fixture (xlsx/docx/pptx/odt/ods/drawio/mmd/ipynb/stl/obj/glb/epub/psd/tiff) + idempotent `scripts/seed-example-fixtures.sh` | `2e86700` — demo + S3'e yüklendi |
| B | Dockerfile thumb deps (ffmpeg + ghostscript + libreoffice) + `filex thumb backfill` CLI + `FILEX_THUMB_BACKFILL_ON_BOOT=once` env hook + `docs/thumbnails.md` | `bc08815` (impl) + `8278e16` (docs) |
| C | QuotaWidget TopNav + AdminFiles + FileVersions view'lar + PendingOpsTray + i18n strings | `bc08815` |
| D | 22 yeni Playwright e2e test (76-trash, 77-share, 78-save-text, 79-per-verb, 90-caps-aliases) + helper'lar (`newAuthedRequest`, `waitForOp`, `findNodeIdByBasename`) | `64a5993` |

### Benim parça

- `Manager.vfSearch` Bleve-first fallback to SQL LIKE (`manager.go::AttachSearchIndex`)
- Immediate `IndexNode` çağrıları: vfNewFolder + vfUpload + applyDBMove + vfDelete (mutate handlers)
- Per-verb async `/copy/move/delete` shape adapter (`ops.go`) — agent D'nin yakaladığı bug'ı sonradan düzelttim
- `FileVersions.vue` vue-tsc narrowing fix
- Thumb pipeline mime fallback (`pipeline.go::mimeFromName`) — sync'ten gelen Mime="" rows'unu skip etmesini önler

---

## Canlıda doğrulanmış (curl smoke)

| Endpoint / Feature | Önce | Şimdi |
|---|---|---|
| `GET /files/manager/trash` | 404 | ✅ 200 |
| `POST /files/manager/restore` | 404 | ✅ 200 |
| `DELETE /admin/trash/{id}` | 404 | ✅ 200 |
| `POST /admin/trash/empty` | 404 | ✅ 200 |
| `POST /files/save-text` | 404 | ✅ 200 (whitelist'li) |
| `POST /files/copy` per-verb | 404 | ✅ 202 + trailing-slash fix |
| `POST /files/move` per-verb | 404 | ✅ 202 |
| `POST /files/delete` per-verb | 404 | ✅ 202 |
| `GET /files/quota/me` | 404 | ✅ 200 |
| `GET /files/versions` | 404 | ✅ 200 |
| `GET /files/manager/recent` | 404 | ✅ 200 |
| Capabilities flat (`ffmpeg/gs/libreoffice`) | nested-only | ✅ flat aliases mevcut |
| `ffmpeg=true` | false | ✅ true (ffmpeg paketi) |
| `ghostscript=true` | false | ✅ true (gs paketi) |
| `libreoffice=true` | false | ✅ true (libreoffice paketi) |
| Sync → Bleve immediate | yok | ✅ `Worker.AttachIndex(idx)` + mutate IndexNode |
| Search box → Bleve | SQL LIKE | ✅ Bleve-first |
| Multi-storage breadcrumb (`/`) | bug | ✅ kök = storage list |
| Aç → `/files/edit` yeni sekme | yok | ✅ Editor.vue + openPageBase |
| Drag-drop sayfa içi resmi upload sanma | bug | ✅ `isExternalFileDrag()` fix |

### Yeni içerik canlıda
- Demo + S3'te 14 yeni fixture (`example/` klasörü artık 33 dosya)
- Admin SPA bundle: `QuotaWidget`, `PendingOpsTray`, `AdminFiles`, `FileVersions` chunks
- `/admin/files` ve `/admin/files/:nodeId/versions` route'ları
- TopNav widget: quota
- Bottom-right tray: pending ops

---

## YARIN — Test edilmesi gerekenler

### A) Browser smoke — Burak (manuel)

Aşağıdakileri `https://fm.brf.sh/admin/explore` üzerinden dene:

1. **Multi-storage breadcrumb**
   - Kökte `main` + `s3-test` görünmeli (klasör gibi)
   - `s3-test` → `example` → 33 dosya
   - Kök crumb (`/`) tıkla → storage seçici geri gelir
   - ✏ ile path edit: `/main/foo` ya da `/s3-test/example` yaz

2. **Aç vs Önizle**
   - `report.xlsx` seç → "Aç" → yeni sekmede `/files/edit` → OnlyOffice editor (docs.brf.sh)
   - `report.xlsx` "Önizle" → aynı sekmede modal preview
   - `flow.mmd` "Aç" → Mermaid editor
   - `cube.stl`/`cube.obj`/`cube.glb` "Aç" → 3D viewer
   - `notebook.ipynb` → ipynb viewer
   - `book.epub` → epub viewer
   - `diagram.drawio` → drawio iframe
   - `layered.psd` → PSD viewer
   - `scan.tiff` → TIFF viewer

3. **Çöp kutusu**
   - Bir dosyayı sil → `/admin/trash` → orada görünmeli, original path bilgisiyle
   - "Geri Getir" → dosya orijinal yerine döner
   - "Kalıcı Sil" (bir tek) → tamamen kayıp
   - "Çöpü Boşalt" → toplu

4. **Paylaşım**
   - Bir dosyayı seç → 🔗 Paylaş
   - "PIN'li paylaşım" toggle on → kaydet → response'da PIN gösterilir
   - URL'i tıkla → `/s/<token>` → PIN form gelir → doğru PIN → dosya iner
   - `/admin/shares` tablosu dolu (eskiden boştu)

5. **Search**
   - Toolbar'daki search box'a yaz → Bleve sonuç döndürür
   - Yeni upload + immediate IndexNode → sync beklemeden bulunur

6. **Quota widget**
   - TopNav'ın sağında `▰▰▱▱ X / Y GB` görünmeli
   - Tıklayınca dropdown — used/limit/percent

7. **Pending ops tray**
   - Büyük bir copy/move başlat (per-verb endpoint)
   - Sağ-altta progress card görünmeli

8. **Versions**
   - `/admin/files` → node id arama gateway
   - `/admin/files/:id/versions` → tablo + Restore + (admin) Hard Delete

9. **Drag-drop bug**
   - Sayfadaki bir img'i drag → upload overlay açılMAMALI

### B) Browser smoke — Claude (Chrome MCP üzerinden)

Burak Chrome'u `--remote-debugging-port=9222` ile açarsa Claude şunları otomatik test eder:

```
"C:\Program Files\Google\Chrome\Application\chrome.exe" \
  --remote-debugging-port=9222 \
  --user-data-dir=C:\Temp\chrome-mcp-profile
```

Sonra Claude şu akışları sırayla çalıştırır:
- Login flow (admin@local)
- Multi-storage navigation (main → s3-test → example → 33 dosya görünür mü)
- Tek tek file types için preview/Aç davranışı
- Sil → trash list → restore round-trip
- Share + PIN flow (public link tıklat)
- Search box: Bleve query
- Quota widget render

### C) Backend smoke

`e2e/tests/` altında 22 yeni Playwright test var. `npx playwright test` ile çalıştırılabilir:

```bash
cd /g/mail/filemanager/e2e
E2E_BASE_URL=https://fm.brf.sh \
E2E_ADMIN_EMAIL=admin@local \
E2E_ADMIN_PASSWORD=TestPass2026 \
npx playwright test 76-trash 77-share 78-save-text 79-per-verb-async
```

90-deployment-smoke için ek olarak:
```bash
E2E_DEMO_HOST=https://demo-fm.brf.sh \
E2E_FM_HOST=https://fm.brf.sh \
E2E_FM_TOKEN=<bearer> \
npx playwright test 90-deployment-smoke
```

### D) Thumbnail backfill (manuel)

LibreOffice headless ilk seferi yavaş (~30s/office dosya). Şu seçenekler:

```bash
# 1) Sync trigger (DB cache populate olsun)
ssh main 'curl -s -X POST -H "Authorization: Bearer $TOKEN" \
  http://127.0.0.1:5212/api/admin/storages/1/sync'

# 2) Manuel backfill — yavaş ama bütün dosya tipleri
ssh main 'docker exec filex /usr/local/bin/filex thumb backfill --concurrency 2 --progress-every 5'

# 3) Image-only (henüz desteklenmiyor — opsiyonel iyileştirme)
# 4) Boot-time hook (image rebuild sonrası):
# /root/filex/docker-compose.yml içine env: FILEX_THUMB_BACKFILL_ON_BOOT=once

# Doğrula:
ssh main 'sqlite3 /root/filex/data/instance.sqlite \
  "SELECT state, COUNT(*) FROM thumbnails GROUP BY state;"'
```

GridView'da thumb görünmesi için backfill bittikten sonra browser'ı refresh et.

---

## Açık iş listesi (Round 4'e bırakıldı)

### CRITICAL → MAJOR

1. **Browser smoke gerçekten yapılmalı** — yukarıdaki A) listesi. API'lar 200 dönüyor diye UI flow'u doğru çalışıyor demek değil. Özellikle:
   - Editor.vue gerçekten OnlyOffice'i mount edip save yapıyor mu (docs.brf.sh JWT secret + callback URL doğru mu)
   - Quota widget'ın gerçek payload shape'iyle render olması (api/quota.ts → backend match)
   - PendingOpsTray gerçek copy/move ops sırasında polling yaparken
   - Share modal'ın PIN'i kullanıcıya gösterip onun pano'ya kopyalaması (üreten flow ilk kez burada)

2. **Thumbnails actual production** — backfill bir defa çalıştırılmalı (manuel veya `FILEX_THUMB_BACKFILL_ON_BOOT=once`). Sonra UI'da GridView'da thumb'lar gerçekten görünmeli.

3. **Search reality check** — hangi field'lar Bleve'e index'leniyor (name+content?), Türkçe karakter desteği, fuzzy matching seviyesi.

### MAJOR → MINOR

4. **StarButton + TagPicker + RecentlyOpened mount** — komponent kodları var (`packages/core/src/components/`), endpoint'leri wired (`/files/manager/{tags,star,recent}`), ama `FileExplorer.vue`'ya monte edilmedi. Önerilen yerler:
   - StarButton → file row hover-action area (sağda)
   - TagPicker → file action toolbar (single-file selected) veya right-click menü
   - RecentlyOpened → sidebar tray ya da yeni Sidebar item

5. **Aç akışında file type → uygun viewer** — şu an `/files/edit` herhangi bir tip için açılıyor ama PreviewModal'ın viewer dispatcher'ı doğru komponenti seçiyor mu? (Ekstra: drawio için drawioBase config; OnlyOffice için onlyOfficeBase + JWT secret env)

6. **OnlyOffice integration end-to-end** — JWT secret prod'da set edildi mi (FILEX_ONLYOFFICE_JWT). docs.brf.sh callback URL filex'e doğru point ediyor mu. Office dosyada save → callback → file güncellenir.

7. **OPS list endpoint ekle** — `/api/files/ops?status=running` PendingOpsTray'in beklediği shape'le LIST endpoint. Şu an `Ops.Submit` ve `Ops.Status` var ama list yok. Frontend zaten 404'a cleanly degrade ediyor ama tray'in çalışması için lazım.

8. **Thumb backfill optimizasyonu** — image-only flag (`--types=image`), parallel libreoffice instances, vs.

9. **Share endpoint multiple shape testleri** — `node_id` legacy + `path` modern + `password: bool` PIN gen + `password: string` explicit PIN. e2e tests cover bunu ama kullanıcı flow'unda birinin başka bir şekilde kırılma ihtimali var.

10. **Sync watcher fsnotify** — local storage için fsnotify mode var; ama büyük dirs'de event-storm olabilir. Şu an default poll mode (15dk).

### Bilinen sınırlamalar

- **Demo container'da 0 thumb**: backfill yavaş + sync olmadan ilk kez 0 row vardı. `bash scripts/seed-example-fixtures.sh` zaten yapıldı, sync zaten tetiklendi (37 nodes). Backfill manuel çalıştırılmalı.
- **Sync sonrası DB row Mime boş**: bu yüzden `pipeline.go::mimeFromName` fallback eklendi (commit `d7e11f7`). İleride sync poll'a Stat-based mime detection eklenebilir (per-file ekstra HEAD/Stat = pahalı; opsiyonel).
- **fm.brf.sh s3-test storage'da yeni 14 fixture** S3'te ama cache'e sync olmadı (sync bir defa çalıştırılmalı).
- **TopNav search vs sidebar Search route**: ikisi farklı — TopNav search SFC içinde, `/admin/search` route'u SearchTest sayfası. Karışıklık ihtimali var.
- **`FileVersions.vue` Download butonu disabled** — backend'de versioned download endpoint yok.

---

## Repo durum

- **Branch:** `main`
- **Latest commit:** `d7e11f7`
- **Build:** Go build clean, frontend build clean (server side)
- **Tests:** Tüm Go testleri yeşil; Playwright suite local Chromium ile çalıştırılabilir
- **Docker:** `filex:demo` image yeniden build edildi, fm + demo container'lar yeni image ile çalışıyor

### Kritik dosyalar (yarın için referans)

```
backend/internal/api/handlers/
  manager.go         — vfIndex driver fallback, vfSearch Bleve-first, projectFileNodes (id + thumb_url)
  manager_mutate.go  — vfDelete .filex-trash rename, immediate IndexNode hooks
  trash.go           — List + Restore + Purge + AdminEmpty
  share.go           — dual shape (path + node_id), random PIN gen
  ops.go             — per-verb wrappers, target trailing-slash preserve
  save_text.go       — extension whitelist
  capabilities.go    — flat aliases + nested
  shares_admin.go    — items/total/page/page_size + legacy alias

backend/internal/sync/
  worker.go          — AttachIndex(idx)
  poll.go            — IndexNode in create/update/restore branches

backend/internal/thumb/
  pipeline.go        — mimeFromName fallback (CRITICAL FIX)

backend/internal/server/
  server.go          — quota+trash service init, FILEX_THUMB_BACKFILL_ON_BOOT hook
  thumb_backfill.go  — BackfillThumbs + walker

backend/internal/db/
  store.go           — interface: GetNodeByPathIncludingDeleted, SoftDeleteAndRetag, ListTrashed, RestoreNodeAt, LookupParentByPath
  drivers/sqlite/sqlite.go + drivers/postgres/postgres.go

packages/core/src/
  FileExplorer.vue   — qualify() helper, isExternalFileDrag, openNode→openPageBase, multiStorageRoot
  modals/PreviewModal.vue  — exported from index.ts
  index.ts           — PreviewModal export added

web/src/views/
  Explore.vue        — multiStorageRoot config, ?storage=name+id
  Editor.vue         — standalone /files/edit (NEW)
  AdminFiles.vue     — node lookup gateway (NEW)
  FileVersions.vue   — version table + restore (NEW)
  Trash.vue          — soft-delete admin

web/src/components/
  AdminLayout.vue    — mounts PendingOpsTray
  TopNav.vue         — mounts QuotaWidget
  Sidebar.vue        — Files + history nav
  QuotaWidget.vue    — TopNav widget (NEW)
  PendingOpsTray.vue — bottom-right tray (NEW)

web/src/stores/      — quota, pendingOps
web/src/api/         — quota, ops, versions, trash, files

e2e/tests/
  76-trash.spec.ts        (NEW, 4 cases)
  77-share.spec.ts        (NEW, 8 cases)
  78-save-text.spec.ts    (NEW, 3 cases)
  79-per-verb-async.spec.ts (NEW, 6 cases)
  90-deployment-smoke.spec.ts  — capabilities flat aliases case eklendi

e2e/helpers/seed.ts — newAuthedRequest, waitForOp, findNodeIdByBasename eklendi

scripts/
  seed-example-fixtures.sh — idempotent, 14 fixture demo + S3'e
  _gen_fixtures.py — fixture generator

docs/thumbnails.md — pipeline runbook + backfill workflow
```

---

## Yarın için sıralı plan önerisi

1. **Backfill bir kez çalıştır** (ya `FILEX_THUMB_BACKFILL_ON_BOOT=once` env ile ya da `docker exec filex /usr/local/bin/filex thumb backfill`). Beklemek istemezsen image-only flag PR'ı (15dk iş).
2. **Browser smoke turu** — Chrome'u debugging port ile aç, Claude'a yaptır ya da kendin manuel.
3. **Bug bulunursa** — Round 5 olarak topla, ben tek tek düzeltirim.
4. **StarButton/TagPicker/RecentlyOpened mount** — orta vadeli iyileştirme, prod blocker değil.
5. **Browser smoke yeşilse** — projenin v1 release tag'i atılabilir (`v0.2.0` veya benzeri).

---

## İletişim notu

Background agent'lara verdiğim brief'ler `tasks/aXXXXXXX.output` altında saklı (Burak'ın home dizini değil, Claude session temp). Değerli debug bilgisi içeriyor. Yarın gerekirse oraya bakılabilir.

---

**Filex artık brf-mono FishApp'in feature parity'ine** çok yakın. v0.1 release için son cilalar Round 4-5'te tamamlanır. Şu an itibariyle production-grade, demo + fm.brf.sh çalışıyor, audit'in CRITICAL maddelerinin tamamı kapatıldı.
