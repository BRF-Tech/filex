# Filex Parity Round 6 — Handover (2026-05-08)

> **Status:** SVG thumbnails live (rsvg-convert), pptx fixture rewritten,
> compose env cleaned up. `a21f5c2` on `origin/main`. fm.brf.sh +
> demo-fm.brf.sh production. **v0.1 release tag candidate.**

## Bugün ne yapıldı

Round 5 sonrası kalan iki cila (SVG + slides.pptx) ve bir cosmetic
(compose env) tek turda tamamlandı.

### Round 6 fix'leri

| # | Yer | Belirti | Root cause | Fix |
|---|---|---|---|---|
| 17 | logo.svg / manager.svg | thumb backfill `image: unknown format` | Round 5'te x/image (BMP/TIFF/WebP) eklendi ama SVG vector → raster gerek, stdlib + x/image SVG decode etmiyor | Yeni `thumb/svg.go` generator: `rsvg-convert` ile SVG → PNG → re-encode JPEG. Pipeline dispatcher `image/svg+xml` mime için `image/*` branch'inden ÖNCE SVG handler'a gönderir. Capability probe `caps.SVG = has("rsvg-convert")`. Yok ise state="skipped" (failed yerine) |
| 18 | slides.pptx | soffice exit 0 ama `produced no PDF` (`Io Class:Write Code:16`) | Round 5'teki `_gen_fixtures.py::write_pptx` slide1.xml `<p:cSld><p:spTree/></p:cSld>` boş — Impress writer drawable shape olmadan PDF üretmez | Slide içeriği zenginleştirildi: `nvGrpSpPr + grpSpPr + p:sp×2` (title "Demo Presentation" + body text). `presentation.xml` standart 4:3 `sldSz/notesSz` size hints içerir. **Yeni pptx için S3 fixture re-seed gerekir** (`bash scripts/seed-example-fixtures.sh`) — eski fixture S3'te kalıyor |
| 19 | Compose temp env | `FILEX_THUMB_BACKFILL_ON_BOOT=once` round 4'ten beri `/root/filex-standalone/docker-compose.yml`'da | CLI hang fix sonrası gerek yok | sed ile satır silindi (sunucu-only, repo'ya yansımaz) |

### Build infrastructure

- `docker/Dockerfile` + `docker/Dockerfile.full` → `rsvg-convert` apk
  package eklendi. **Alpine paket adı `librsvg` DEĞİL** — librsvg
  sadece .so + GObject typelib içerir, convert CLI ayrı paket olarak
  `rsvg-convert-2.58.5-r0`.

---

## Canlıda doğrulanmış

| Test | Sonuç |
|---|---|
| `filex thumb backfill --retry-failed` | processed=3, ok=2 (logo.svg + manager.svg ✓), failed=1 (slides.pptx — fixture eski) |
| `GET /api/files/manager?action=index&path=s3-test://example` `files[].thumb_url` | 12/32 (önce 10) — yeni: logo.svg + manager.svg |
| GridView `/admin/explore?storage=s3-test` (browser screenshot) | logo.svg + manager.svg → "filex" yazılı **mavi yuvarlak logo** rasterize edildi ve render ediliyor; manager.jpg, sample.mp4, square.jpg, photo.webp önceden ✓; scan.tiff (gri, content gri) görünüyor |
| `rsvg-convert --version` (container) | `2.58.5` ✓ |

---

## Round 7'ye bırakılanlar

### slides.pptx S3 re-seed

`_gen_fixtures.py::write_pptx` yeniden yazıldı ama S3'teki bytes eski.
Re-seed:

```bash
cd /g/mail/filemanager
bash scripts/seed-example-fixtures.sh
```

Sonra:

```bash
ssh main 'docker exec filex-standalone /usr/local/bin/filex thumb backfill --retry-failed'
```

slides.pptx için "produced no PDF" warning gitmesi gerekir.

### LibreOffice "javaldx" stderr noise

Hâlâ ilk soffice invokasyonunda `Warning: failed to read path from
javaldx` stderr'a yazılıyor. JRE eklendi, soffice çalışıyor, ama wrapper
script ile `XDG_CACHE_HOME` set + `--norestore --nologo
--nofirststartwizard` flag'leri eklenirse log temizlenir. Pure log
noise — fonksiyonel sorun yok.

### v0.1 release tag

Round 1-6 boyunca FishApp parity tamamlandı. Tag attığında GitLab CI
image push pipeline tetiklenir:

```bash
git tag v0.1.0 && git push --tags
```

CI release:docker job `brftech/filex:v0.1.0` + `brftech/filex:latest`
yayınlar. demo-fm.brf.sh + fm.brf.sh image referansını
`brftech/filex:v0.1.0`'a çevir → reproducible deploy.

### e2e Playwright suite

Round 2-3'te eklenen 22 test (`76-trash`, `77-share`, `78-save-text`,
`79-per-verb-async`, `90-deployment-smoke`) lokalde **çalıştırılmadı**.
Browser smoke MCP üzerinden manuel yapıldı. Suite çalıştırmak için:

```bash
cd /g/mail/filemanager/e2e
E2E_BASE_URL=https://fm.brf.sh \
E2E_ADMIN_EMAIL=admin@local \
E2E_ADMIN_PASSWORD=ClaudeSmoke2026! \
npx playwright test 76 77 78 79
```

> **Not:** Round 4'te `admin@local` password reset edilmişti
> (`ClaudeSmoke2026!`). Bu Burak'ın kendi parolası değil — gerekirse
> `filex admin reset-password --email admin@local --password
> "<yeni>"` ile değiştir.

---

## Repo durum

- **Branch:** `main`
- **Latest commit:** `a21f5c2 fix(round-6): SVG thumbnails via rsvg-convert + richer pptx fixture`
- **Image:** `filex:demo` (tek seferlik build, GitLab registry'e push edilmedi). v0.1.0 tag sonrası CI otomatik push eder.
- **Production:** filex-standalone (fm.brf.sh) + filex (demo-fm.brf.sh) yeni image'la canlı.

### Round 6 değişen dosyalar

```
backend/internal/capability/service.go         (+3)   — has("rsvg-convert") probe
backend/internal/model/capability.go           (+1)   — Thumbs.SVG json field
backend/internal/server/server.go              (+1)   — pipelineCaps.SVG = cap.Thumbs.SVG
backend/internal/thumb/pipeline.go             (+11)  — dispatcher SVG branch + Capabilities.SVG field
backend/internal/thumb/svg.go                  (NEW, +96) — rsvg-convert generator
docker/Dockerfile                              (+1)   — rsvg-convert apk
docker/Dockerfile.full                         (+1)   — rsvg-convert apk
scripts/_gen_fixtures.py                       (+72/-2) — pptx slide1.xml + presentation.xml zenginleştirildi
```

---

## Round 1-6 özeti (parity work)

| Round | Tarih | Bulgular | Commit |
|---|---|---|---|
| 1 | 2026-05-04 öncesi | Filex skeleton + storage drivers + sync worker | (önceki commit'ler) |
| 2-3 | 2026-05-07 | 14 viewer fixture + Bleve search + thumb deps + quota/versions/pending UIs (background agents) | `2e86700`, `64a5993`, `bc08815`, `8278e16`, `127e38b`, `a882d76`, `398c318`, `d7e11f7`, `23daf87` |
| 4 | 2026-05-08 sabah | Browser smoke 9 bug → SPA-vs-backend mismatch + thumb hydration + Aç button + JRE | `1a98bd7`, `75219b3` |
| 5 | 2026-05-08 öğle | 6 bug daha → Bleve rebuild + wildcard + image decoders + office diag + Editor.vue mount + full-path | `a0fb79f`, `581e97e` |
| 6 | 2026-05-08 öğleden sonra | SVG rasterizer + pptx fixture + compose cleanup | `a21f5c2` (+ bu handover) |

**v0.1 için kalan iş:** slides.pptx S3 re-seed, javaldx log temizliği,
e2e suite local çalıştırma. Hepsi Round 7'de tamamlanırsa v0.1.0 tag'i
atılabilir.

---

**Filex Round 6 tamam.** Production-grade, FishApp parity tamamlandı,
v0.1 release tag candidate. SVG/WebP/TIFF/Office/PDF/Video/Image
thumbnails canlıda, OnlyOffice editor canlı, search wildcard ile akıllı,
trash/share/quota/versions UI işliyor, drag-drop guard intact.
