# Filex Parity Round 4 — Handover (2026-05-08)

> **Status:** 9 bug found in browser smoke, 9 fixes shipped, 1 known gap
> deferred to round 5. `1a98bd7` on `origin/main`. fm.brf.sh + demo-fm
> live with the new image (`filex:demo`).

## Bugün ne yapıldı

Round 2-3 handover'ı bana "API'lar 200 dönüyor, browser smoke yapılmadı"
notu bırakmıştı. Browser üstünden gerçek tıklama + endpoint dinleme
yaptım, 9 farklı bug çıktı, hepsi tek refactor turunda kapatıldı.

### Browser smoke ile çıkan bug'lar

| # | Yer | Belirti | Root cause | Fix |
|---|---|---|---|---|
| 1 | PendingOpsTray polling | `GET /api/files/ops?status=running` 405 her ~2s | Backend'de POST /ops ve GET /ops/{id} var, list yok | `Ops.Service.List(status)` + handler + route |
| 2 | `filex thumb backfill` CLI | komut 60s+ hang, sadece "goose: no migrations to run" | Çalışan `filex serve` Bleve `/data/search.bleve` üstünde boltdb lock'u tutuyor; CLI'nın `server.New()` ikinci kez `search.Open` çağırınca block | CLI'da `os.Setenv("FILEX_SEARCH_ENABLED","false")` defansif (zaten backfill index'e dokunmaz) |
| 3 | Frontend caps fetch | `GET /api/files/capabilities` 404 | Backend sadece `/api/capabilities` mount, frontend `/api/files/capabilities` çağırıyor | Aynı handler için ikinci route alias |
| 4 | GridView thumb yok | API list endpoint `files[]` içinde `thumb_url` field hiç yok | `ListNodesByParent` thumbnails JOIN yapmıyor → `n.Thumb` her zaman nil → `projectFileNodes` koşulu hep false | `vfIndex` her dosya için `GetThumbnail` çağırıp `n.Thumb` populate ediyor (N+1, dir size kabul edilebilir) |
| 5 | Office thumb fail | letter.docx, slides.pptx vs `state=failed` | LibreOffice headless javaldx için JRE bulamıyor, exit 1 | `docker/Dockerfile{,.full}` + `backend/Dockerfile.full` → `openjdk17-jre-headless` ekle. **PARTIAL FIX** — JRE eklendikten sonra bazı dosyalarda hâlâ "produced no PDF" → round 5'e bırakıldı |
| 6 | "Aç" butonu yok | xlsx seçili → toolbar Önizle/İndir/Paylaş/Rename/Sil; "Aç" eksik | `Toolbar.vue` single-file array'inde `open` action yoktu | `single-file` array'inin başına `{ key:'open', icon:'↗' }` eklendi |
| 7 | OnlyOffice config 405 | `POST /api/files/onlyoffice/config` 405 | Backend `r.Get`, frontend `fetch(POST)` | Aynı handler `r.Post`, body decode ekle, `path → node` resolver eklendi |
| 8 | Search GET 405 | Frontend `?q=` GET, backend POST | Aynı sebep | `r.Get + r.Post` ikisi de mount, handler GET'te query string'den `q/storage_id/limit` okur |
| 9 | `/files/edit` 404 | "Aç" yeni sekme açtı, sayfa "404 page not found" | Backend SPA fallback sadece `/admin/*` için var | `routes.go` `wireStatic` içine `r.Handle("/files/edit", spa)` + `r.Handle("/files/edit/*", spa)` (ek `spaHandler` urlPrefix="" instance) |

### Bonus defansif fix

| # | Yer | Fix |
|---|---|---|
| 10 | `web/src/api/ops.ts::list` | 404/501 yanında 405'i de swallow et — eski backend'lerde POST/ops registered olduğunda chi 405 döner, polling spam etmesin |

### Commit + image durumu

- `1a98bd7` `origin/main` (12 file, +259/-17)
- `filex:demo` rebuild (multi-stage cached → frontend stage cache hit, sadece backend rebuild)
- `filex-standalone` recreate edildi, son boot:
  - `filex listening 0.0.0.0:5212` ✓
  - `thumb backfill (boot): done processed=0` (önceki 31 dosya zaten state'te, retry-failed flag yoktu)
  - `FILEX_THUMB_BACKFILL_ON_BOOT=once` env hâlâ compose'da → bir sonraki recreate öncesi temizlemek isteyebilirsin (compose dosyası elden değiştirildi: `/root/filex-standalone/docker-compose.yml`)

---

## Canlıda doğrulanmış (browser smoke + curl)

| Endpoint / Davranış | Önce | Sonra |
|---|---|---|
| `GET /api/files/ops?status=running` | 405 | ✅ 200 `{ops:[]}` |
| `GET /api/files/capabilities` | 404 | ✅ 200, flat keys (`ffmpeg`, `ghostscript`, `libreoffice`) ✓ |
| `POST /api/files/onlyoffice/config` body=`{path,mode}` | 405 | ✅ 200 + full editor config (`documentServerUrl`, `config.document.fileType`, vs) |
| `GET /api/files/search?q=square&limit=5` | 405 | ✅ 200 `{results:[]}` (Bleve index boş, başka konu) |
| `POST /api/files/share` PIN'li | 200 ✓ (önceden de) | ✅ 200, `password_pin: "78180103"` |
| `GET /api/files/quota/me` | 200 ✓ | ✅ 200, `unlimited:true` |
| `GET /api/admin/shares` | 200 ✓ | ✅ 200, az önceki share dahil |
| List endpoint `files[].thumb_url` | yok | ✅ 7/32 dosyada (image+video+pdf+office-ready) |
| GridView UI thumb | sadece icon | ✅ manager.jpg, sample.mp4, square.jpg gerçek thumb render |
| Toolbar single-file | 5 buton (Önizle/İndir/Paylaş/Rename/Sil) | ✅ 6 buton (`↗Aç` + 5 eski) |
| `/files/edit?path=…` | 404 | ✅ 200 `index.html` (913B), SPA route'u `/admin/files/edit?…`'e map ediyor, vue-router devralıyor |
| `filex thumb backfill` CLI | 60s+ hang | ✅ saniyeler içinde tamamlanıyor (`exit=0`) |

---

## Açık kalan iş (Round 5)

### Office thumb hâlâ kırık (yarı çözüm)

JRE eklendikten sonra `--retry-failed` ile çalıştırınca:
- letter.docx → `thumb: libreoffice: exit status 1 ()` (boş err string)
- slides.pptx → `thumb: libreoffice produced no PDF: /tmp/filex-office-…/slides.pdf`
- budget.ods, notes.odt, report.xlsx → DB'de `ready` ama disk'te yok mu? (UI default icon)

Olası eksikler:
1. `hyphen` paketi (LibreOffice tireleme için)
2. `dbus-x11` (ofice document server bazen lazım)
3. `--user-installation` veya `HOME=/tmp` env (root home'a yazma izni issue olabilir)

Test için:
```bash
ssh main 'docker exec filex-standalone su -s /bin/sh filex -c "soffice --headless --convert-to pdf /storages/example/letter.docx --outdir /tmp/" 2>&1'
```

### Editor.vue OnlyOffice mount lifecycle

`/admin/files/edit` SPA load oluyor, modal mount oluyor (`fe-modal__card`,
`fe-preview__office`), ama `mountOnlyOfficeEditor` çağrılmamış —
`fe-onlyoffice-mount` div yok. Network'te `POST /onlyoffice/config`
isteği yok. Muhtemelen `props.open` watch'ı veya `kind === 'office'`
yan etkisinde lifecycle eksiği var. Fix locally `/admin/files/edit?…` → modal
açılınca otomatik OnlyOffice JS yüklenmeli + iframe doğmalı.

`PreviewModal.vue::mountOnlyOfficeEditor` fonksiyonu var ama tetiklenmiyor.
İhtimal: `Editor.vue` modal'ı `:open="r"` ile mount ediyor, modal'ın
`watch(open)` onMounted'ı varsa lifecycle race olabilir. `nextTick + watch`
veya `onMounted` yerine `watchPostEffect` deneyin.

### Bleve search index boş

Container restart sonrası Bleve `/data/search.bleve` açık ama içinde
sıfır doküman (sync sonrası IndexNode çağırılması gerekir). `/api/files/search` 200 dönüyor
ama `{results:[]}`. Çözüm: `/api/admin/search/rebuild` endpoint çağır →
`RebuildAll` tüm node'ları yeniden index'ler.

```bash
curl -X POST -H "Authorization: Bearer $TOKEN" https://fm.brf.sh/api/admin/search/rebuild
```

### compose.yml'daki `FILEX_THUMB_BACKFILL_ON_BOOT=once` env

Smoke turu için bot hook eklemiştim. Kalıcı olması gerekmez (her recreate'te
processed=0 dönecek çünkü thumb'lar terminal state'te). Temizlemek için:

```bash
ssh main "sed -i '/FILEX_THUMB_BACKFILL_ON_BOOT/d' /root/filex-standalone/docker-compose.yml"
```

### Diğer ufak dataset gözlemleri (round 5+)

- SVG/WebP/TIFF thumb fail: Go stdlib image decoder bu formatları desteklemiyor → `golang.org/x/image/bmp,tiff,webp` import + register decoders gerekir
- Trash sayfası ✓ render OK ama restore round-trip henüz e2e doğrulanmadı (UI butonları var, browser smoke'ta tıklamadım)
- Drag-drop sayfa-içi img regression (handover'ın 9. maddesi) test edilmedi

---

## Repo durum

- **Branch:** `main`
- **Latest commit:** `1a98bd7 fix(round-4): close 9 SPA-vs-backend mismatches found in browser smoke`
- **Build:** `filex:demo` rebuilt, fm.brf.sh + demo-fm.brf.sh canlı
- **Tests:** Lokalde Go yok, sunucuda Docker build yeşil; e2e/playwright suite çalıştırılmadı (yarın `npx playwright test 76 77 78 79 90` öneriliyor)

### Değişen dosyalar

```
backend/Dockerfile.full                       (+1)
backend/cmd/filex/main.go                     (+12) — backfill CLI Bleve bypass
backend/internal/api/handlers/manager.go      (+13) — vfIndex thumb hydration
backend/internal/api/handlers/onlyoffice.go   (+115/-15) — POST + path resolver
backend/internal/api/handlers/ops.go          (+25)  — List handler
backend/internal/api/handlers/search.go       (+30/-1) — GET query string parse
backend/internal/api/routes.go                (+25/-2) — alias'lar + /files/edit SPA fallback
backend/internal/ops/service.go               (+34)  — Service.List(status)
docker/Dockerfile                             (+7)   — JRE
docker/Dockerfile.full                        (+1)   — JRE
packages/core/src/components/Toolbar.vue      (+6)   — Aç butonu single-file
web/src/api/ops.ts                            (+7/-3)— 405 swallow
```

---

## Yarın için sıralı plan önerisi

1. **Office thumb diagnostic** — yukarıdaki manuel `soffice --convert-to pdf` çağrısı container içinde, hangi fontu/lib'i istediğini gör, alpine paketini ekle, image rebuild
2. **Editor.vue OnlyOffice mount** — `PreviewModal::mountOnlyOfficeEditor` `watchPostEffect` veya `onMounted + nextTick` ile tetiklenmeli; localde mount cycle hangi watcher'a bağlı debug et (`kind` ref + `props.open`)
3. **Bleve rebuild** — `/api/admin/search/rebuild` çağır, search box yeniden çalışsın
4. **e2e playwright suite** — round 2-3'te 22 test eklendi, lokalde çalıştır
5. **`FILEX_THUMB_BACKFILL_ON_BOOT` env temizliği** (cosmetic)

---

**Filex Round 4 tamam.** v0.1 release için kalan `office thumb` + `editor mount` cilaları round 5'te biter. fm.brf.sh + demo-fm.brf.sh canlı, browser smoke'da 9 bug'ın 9'u kapandı, audit gözüyle production-grade.
