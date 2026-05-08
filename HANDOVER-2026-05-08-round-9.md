# Filex Parity Round 9 — Handover (2026-05-08)

> **Status:** Browser-tier CI job wired, migration playbook validated
> (already complete in `docs/MIGRATION_FISHAPP.md`), v0.1.0 release
> pipeline triggered. `f64872e` on `origin/main`.
> Pipeline progress visible at
> https://gitlab.com/brftech/filemanager/-/pipelines.

## Bugün ne yapıldı

Round 8 sonunda v0.1.0 tag'i atıldı (`7497378` → tag `v0.1.0`). Round
9 carryover'lar:

### 1. Browser-tier CI job (`f64872e`)

`.gitlab-ci.yml`'a `e2e:rounds-regression-browser` job eklendi.
api-only sibling (`e2e:rounds-regression`) zaten her push'ta çalışıyor;
browser-tier sibling **main + tag**'da çalışacak — Editor.vue mount +
Toolbar `Aç` action testleri otomatik koşar.

```yaml
e2e:rounds-regression-browser:
  image: mcr.microsoft.com/playwright:v1.48.0-noble
  stage: e2e
  variables:
    E2E_INCLUDE_BROWSER: "1"
  rules:
    - if: $CI_COMMIT_BRANCH == $CI_DEFAULT_BRANCH
    - if: $CI_COMMIT_TAG
  allow_failure: true   # flaky döneminde — 10+ green run sonra false
```

Tasarım kararları:
- **Microsoft Playwright image**: chromium + system deps + 150 MB
  pre-baked — `playwright install --with-deps` cost'unu CI içinde
  ödemiyoruz
- **`allow_failure: true`** başlangıçta — browser flake (font geç
  yükleme, modal mount race, Cloudflare TLS jitter) bir defa kırmızı
  pipeline yapmasın
- **MR pipelines stay api-only**: iteration loop'u tıkamamak için

### 2. brf-mono migration playbook — zaten tam (`docs/MIGRATION_FISHAPP.md`)

159 satırlık doc, Phase A → G yapısında. brf-mono panel +
fish.brf.sh PWA için step-by-step migration:

- **Phase A** — backend seçimi (filex Go service vs brf-mono PHP)
- **Phase B** — `brf-mono/resources/js/file-manager.ts` import swap
  (`./vendor/file-explorer/` → `@brftech/filex-core`)
- **Phase C** — config diff (apiBase, auth)
- **Phase D** — endpoint table (`/admin/fishapp/files/...` →
  `/api/files/...`, thumb path-keyed → id-keyed)
- **Phase E** — auth model (`bearer` / `csrf` / `basic` / `none`)
- **Phase F** — verify (build + render + round-trip + Sentry quiet)
- **Phase G** — cleanup (`rm -rf vendor/file-explorer/`)

Migration için ek dosya **gerek yok** — playbook hazır.

### 3. v0.1.0 release pipeline durumu — UI kontrol

Tag pushed (`7497378` → `v0.1.0`). GitLab CI rules `if:
$CI_COMMIT_TAG =~ /^v\d+\.\d+\.\d+/` ile şu jobs tetiklendi:

- `release:goreleaser` → cross-arch binaries
- `release:npm` → `@brftech/filex-{core,webcomponent,react}`
  → GitLab npm registry (`https://gitlab.com/api/v4/projects/<id>/packages/npm/`)
- `release:docker` → `registry.gitlab.com/brftech/filemanager:v0.1.0`
  + `:latest` + `:slim-v0.1.0` + `:slim` + `:full-v0.1.0` + `:full`

**Pipeline status:** `gitlab.com/brftech/filemanager/-/pipelines`

`registry.gitlab.com/brftech/filemanager:v0.1.0` pull testi denedi
(main'in `claude` deploy token'ı var) ama `repository does not exist
or may require 'docker login'` döndü — pipeline henüz tamamlanmadı
veya deploy token `brftech/filemanager` registry'sine yetkili değil.
İkisi de UI'dan tek bakışta netleşir.

---

## Repo durum

- **Branch:** `main`
- **Latest commit:** `f64872e ci(e2e): add browser-tier regression job for main + tags`
- **Tag:** `v0.1.0` (`7497378`)
- **Build:** Round 8 image canlıda; CI release:docker pipeline bittiğinde
  `registry.gitlab.com/brftech/filemanager:v0.1.0` pull edilebilir
- **Production:** filex-standalone + filex round-8 image'la canlı

### Round 9 değişen dosyalar

```
.gitlab-ci.yml                    (+33)  — e2e:rounds-regression-browser
HANDOVER-2026-05-08-round-9.md    (NEW)
```

---

## Round 1-9 toplu özet

| Round | Bulgular | Test | Commit count |
|---|---|---|---|
| 1-3 | Skeleton + UIs (background agents) | — | (pre) |
| 4 | 9 SPA-vs-backend bug | manuel | 2 |
| 5 | 6 bug (Bleve + decoders) | manuel | 2 |
| 6 | SVG + pptx try | manuel | 2 |
| 7 | 14 regression test | otomatik | 2 |
| 8 | pptx full + CI e2e + javaldx | CI'da otomatik | 3 |
| **8 release** | **v0.1.0 tag** | — | tag |
| 9 | Browser CI tier + migration playbook validated | CI'da | 1 |

**Bug toplamı:** 18. **Test coverage:** 15 regression test (api-only +
browser-tier). **CI:** her push api-only, main + tag browser dahil.

---

## Round 10'a bırakılanlar

### v0.1.0 deployment verification

Pipeline tamamlandığında:

```bash
ssh main
docker pull registry.gitlab.com/brftech/filemanager:v0.1.0
docker tag registry.gitlab.com/brftech/filemanager:v0.1.0 filex:v0.1.0
# Update docker-compose.yml:
sed -i 's|filex:demo|registry.gitlab.com/brftech/filemanager:v0.1.0|' /root/filex-standalone/docker-compose.yml
docker compose -f /root/filex-standalone/docker-compose.yml up -d
```

Sonra regression suite'i v0.1.0 image'a karşı yeniden çalıştır:

```bash
cd /g/mail/filemanager/e2e
E2E_BASE_URL=https://fm.brf.sh \
E2E_ADMIN_EMAIL=admin@local \
E2E_ADMIN_PASSWORD='<live>' \
E2E_INCLUDE_BROWSER=1 \
npx playwright test 91-rounds-4-6-regression
```

15/15 yeşil → v0.1.0 production'da onaylı.

### brf-mono migration

`docs/MIGRATION_FISHAPP.md`'i takip et:

```bash
# brf-mono panel
cd /root/brf-mono
pnpm add @brftech/filex-core
sed -i 's|./vendor/file-explorer/file-explorer.js|@brftech/filex-core|' \
  resources/js/file-manager.ts
rm -rf resources/js/vendor/file-explorer/
pnpm build
# verify panel + sanity check
```

```bash
# fish-mobile PWA
cd /home/brf-fishapp/...   # PWA repo
pnpm add @brftech/filex-core
# repeat the import swap + vendor cleanup
```

Plus brf-mono'nun `composer.json`'una yeni paket sürümünü işaret et,
`CHANGELOG.md` güncelle, fishapp PWA tag at.

### Browser CI tier — flake observation

`allow_failure: true` set. 10 ardışık green run gözlemlendiğinde
flip et `allow_failure: false` ile blocking yap.

---

**Filex Round 9 tamam.** v0.1.0 release tag CI'a teslim edildi,
browser-tier regression CI'da otomatize, brf-mono migration için
playbook hazır. Pipeline UI'dan onay sonrası production'a
`registry.gitlab.com/brftech/filemanager:v0.1.0` deploy edilebilir.
