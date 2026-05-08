# Filex Parity Round 5 — Handover (2026-05-08)

> **Status:** 5 daha bug bulundu + kapatıldı. `a0fb79f` on `origin/main`.
> Editor.vue OnlyOffice end-to-end **canlıda çalışıyor**, search box
> Bleve wildcard ile partial matches yapıyor, WebP+TIFF thumbnails
> gerçek görsel render. Office için `slides.pptx` minimal fixture
> nedeniyle hâlâ fail; round 6'ya bırakıldı.

## Bugün ne yapıldı

Round 4 sonrası deploy + browser smoke turunda 5 yeni bug çıktı, hepsi
tek refactor turunda kapatıldı. Plus 1 fix Editor.vue mount lifecycle'ını
tamamladı (round 4'te `immediate: true` denemiştim, TDZ patladı,
`onMounted + nextTick` ile yeniden yapılandırıldı).

### Round 5 bug'ları

| # | Yer | Belirti | Root cause | Fix |
|---|---|---|---|---|
| 11 | Bleve `/api/admin/search/rebuild` | endpoint 202 dönüyor ama `document_count` 0 kalıyor | Handler `go func() { idx.RebuildAll(r.Context(), ...) }()` — chi handler return olunca `r.Context()` cancel oluyor → goroutine ilk `ctx.Err()` check'inde exit | `context.Background()` kullan, slog Info/Warn ekle |
| 12 | Bleve `/api/files/search` | `q=square` → `[]` ama `q=square.jpg` → 1 result | Default `MatchQuery + standard analyzer` filename'i tek token tutuyor (nokta tokenize değil) → substring miss | Match + Wildcard(name) + Wildcard(path) **disjunction**. Wildcard term lower-cased (Bleve wildcard'ları analyse etmez) |
| 13 | Thumb pipeline | photo.webp / scan.tiff / logo.svg → `image: unknown format` | Go stdlib sadece PNG/JPEG/GIF kayıtlı | `golang.org/x/image/{bmp,tiff,webp}` blank import. **SVG round 6'da** (vector→raster, librsvg gerek) |
| 14 | Office thumb fail | `slides.pptx` → `produced no PDF: …slides.pdf` (bare error, log boş) | LibreOffice exit=0 ama PDF yazamadı → diagnostic yok | office.go: `cmd.CombinedOutput()`'u her zaman slog.Warn'a yaz, `os.ReadDir(tmpDir)` ile dizin listele, ANY .pdf'i fallback olarak yakala (soffice bazen embedded title ile rename ediyor) |
| 15 | Editor.vue OnlyOffice mount | `/files/edit` mount oldu, modal açıldı, ama OnlyOffice iframe yok; round 4'te `immediate: true` denedim → `ReferenceError: Cannot access 'G' before initialization` (TDZ) | `immediate: true` setup() tamamlanmadan callback çağırıyor → `let officeEditor` declaration'ı henüz init değil | Watcher non-immediate, body'i `runOrchestration()` named function'a çıkar, `onMounted + nextTick` ile manuel ilk trigger |
| 16 | OnlyOffice config 404 | round-5 image'da POST 404 (round-4'te 200'dü) | PreviewModal `body: stripAdapter(file.path)` → backend'a "example/report.xlsx" gidiyor (adapter prefix yok) → resolver `storages[0]=main`'e fallback → not found | PreviewModal `body: file.path` (full adapter-qualified) gönder. Backend zaten `splitAdapterPath` yapıyor, prefix dahil resolve eder |

### Build infrastructure

- `backend/go.mod` `go 1.24` → `go 1.25` (golang.org/x/image v0.39+ Go 1.25 toolchain ister)
- `backend/go.mod` `require golang.org/x/image v0.39.0` eklendi (üst-level dep)
- `docker/Dockerfile` `FROM golang:1.24-alpine` → `golang:1.25-alpine`

---

## Canlıda doğrulanmış

| Test | Sonuç |
|---|---|
| `POST /api/admin/search/rebuild` → wait 3s → `/stats` | `document_count: 35` ✓ (önce 0) |
| `GET /api/files/search?q=square` | 1 match (square.jpg) ✓ |
| `GET /api/files/search?q=jpg` | 2 matches (manager.jpg + square.jpg) ✓ |
| `GET /api/files/search?q=main` | 1 match (main.go) ✓ |
| `GET /api/files/search?q=config` | 2 matches (config.json + config.yaml) ✓ |
| `GET /api/files/manager?action=index&path=s3-test://example` files[].thumb_url | 10/32 dosyada (önce 7) — yeni: photo.webp + scan.tiff |
| `filex thumb backfill --retry-failed` | 5 file processed, 2 ok (webp+tiff yeni başarılı), 3 fail (svg×2 + slides.pptx) |
| Editor.vue `/files/edit?path=s3-test://example/report.xlsx` | OnlyOffice spreadsheet iframe **canlı render** — ribbon, sheet sekmesi, A1=`Demo Report` cell, Save/Print/Undo butonları ✓ |
| `POST /api/files/onlyoffice/config` body=`{path:"s3-test://example/report.xlsx",mode:"edit"}` | 200 + full editor config (documentServerUrl, config.document.fileType, ...) ✓ |

---

## Açık kalan iş (Round 6)

### SVG thumbnail desteği

WebP + TIFF + BMP eklendi (golang.org/x/image), ama SVG vector → raster
gerektiriyor. İki yol:

1. **librsvg + rsvg-convert** alpine package + thumb/svg.go yeni
   generator (`exec.Command("rsvg-convert", "-w", "320", "-o", out, src)`)
2. Pure-Go: `github.com/srwiley/oksvg + rasterx` — kütüphane minimal
   ama Go ekosisteminde mainline değil

(1) production-grade, Dockerfile'a tek satır apk add yeter.

### Office fixture sparse → soffice fail

`slides.pptx` (1.49 KB) + benzeri minimal fixtures soffice'i `Io Class:Write
Code:16` ile patlatıyor. JRE eklendi, soffice headless OK
(plain text → PDF çalışıyor). Sorun fixture içeriği — soffice'in Impress
yazıcısı için yetersiz şema. Çözüm: `_gen_fixtures.py` `write_pptx`
fonksiyonunda gerçek içerik üret (slide layout XML, theme1.xml, vs).

### LibreOffice "javaldx not found" ilk invokasyon log noise

`first-run` sırasında soffice javaldx'i arıyor, alpine-side path'te
yok, `Warning: failed to read path from javaldx` stderr'a yazılıyor.
Functional problem yok — sadece log noise. Çözüm: `XDG_CACHE_HOME`
veya `HOME` set edilen wrapper script + `--norestore --nologo
--nofirststartwizard` flags.

### `FILEX_THUMB_BACKFILL_ON_BOOT=once` env temizliği

Hâlâ `/root/filex-standalone/docker-compose.yml` içinde — round 4-5
boyunca recreate sonrası boot backfill'i denemek için duruyordu.
Artık CLI çalıştığı için kalıcı bırakmaya gerek yok. Bir sonraki
recreate öncesi:

```bash
ssh main "sed -i '/FILEX_THUMB_BACKFILL_ON_BOOT/d' /root/filex-standalone/docker-compose.yml"
```

---

## Repo durum

- **Branch:** `main`
- **Latest commit:** `a0fb79f fix(round-5): editor mount + search wildcard + image decoders + office diag`
- **Build:** `filex:demo` rebuilt (Go 1.25 toolchain)
- **Image deployed:** filex-standalone (fm.brf.sh) running new image

### Değişen dosyalar (round-5)

```
backend/go.mod                                  (+5/-1)   — go 1.25 + x/image v0.39
backend/internal/api/handlers/search_admin.go   (+13/-2)  — Background ctx + slog
backend/internal/search/index.go                (+36/-7)  — wildcard + match disjunction
backend/internal/thumb/image.go                 (+12/-2)  — bmp/tiff/webp decoders
backend/internal/thumb/office.go                (+36/-4)  — diagnostic + filename fallback
docker/Dockerfile                               (+4/-1)   — golang:1.25-alpine
packages/core/src/modals/PreviewModal.vue       (+119/-92) — TDZ-safe orchestration + full path
```

---

## Yarın için sıralı plan önerisi (Round 6)

1. **SVG rasterizer** — `apk add librsvg` + thumb/svg.go generator. 1-2 saat.
2. **Office fixture rewrite** — `_gen_fixtures.py::write_pptx` real content;
   testlerde valid pptx olduğunu doğrula. 1-2 saat.
3. **e2e Playwright suite** — round 2-3'te 22 test eklenmişti, lokalde çalıştır,
   regression varsa fix.
4. **`FILEX_THUMB_BACKFILL_ON_BOOT` compose temizliği** (cosmetic).
5. **v0.1 release tag** — Round 1-5 boyunca FishApp parity tamamlandı,
   `git tag v0.1.0 && git push --tags` sonrası GitLab CI image push pipeline
   tetiklenir.

---

**Filex Round 5 tamam.** OnlyOffice editor canlıda render, search Bleve
wildcard ile akıllı, WebP/TIFF thumb'lar GridView'da görünüyor. v0.1
release için kalan iki cila (SVG + office fixture) round 6'da kapanır.
fm.brf.sh + demo-fm.brf.sh production-grade.
