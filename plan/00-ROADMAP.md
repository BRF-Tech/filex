# filex v0.1.0 — Yol Haritası

> Yazılma tarihi: 2026-05-06 (akşam, lokal devam sonrası güncellendi)
> **v0.1.0 release-candidate**: backend + frontend + docs + CI hazır,
> tek eksik `git tag v0.1.0` (Burak'a kaldı).
>
> **Önce oku:** `SPEC.md` (root) — tüm karar matrisi + mimari + ENV + endpoint kontratı.
> Detaylı release planı: `plan/HANDOVER.md`.

## Branch durumu

- `main` — Round A doğrulama + B + C + D hazırlığı tamamlandı, 8 commit pushlanmaya hazır.
- Hiçbir kalan WIP yok. `go build ./... && go test ./... && pnpm -r build` üçü de yeşil.

## Bu commit'te ne var

1. **`SPEC.md`** — Tek dosyada tüm spec (370 satır)
2. **`plan/`** klasörü — Delta'lar için detaylı uygulama notları (bu dosyalar)
3. **WIP delta kodları** — subagent'lar bitirmeden Coder workspace'i durduğu için yarı doğrulanmış kodlar:
   - `backend/internal/storage/drivers/ftp/` (FTP driver)
   - `backend/internal/auth/drivers/proxyheader/` (Proxy-header auth driver)
   - `backend/internal/storage/validate.go` + `validate_test.go` (Root path guard)
   - `backend/internal/api/handlers/storages.go` (root guard çağrısı eklenmiş olabilir)
   - `backend/cmd/filex/main.go` (yeni driver blank import'ları eklenmiş olabilir)
   - `backend/internal/server/server.go` (?)
   - `backend/go.mod` + `backend/go.sum` (jlaffaye/ftp dep eklenmiş)
   - `deploy/` rename + içerik update (`files.brf.sh` → `demo-fm.brf.sh`)

   **Kontrol:** `cd backend && go build ./... && go test ./...` — lokal'de ilk iş

## Tamamlanan

| # | Faz | Commit |
|---|-----|--------|
| 1 | Monorepo iskelet + core paket | cc65864 |
| 2 | Web Component + React adapter | cc65864 |
| 3a | Go backend iskelet + auth + storage | cc65864 |
| 3b | API endpoint'leri | cc65864 |
| 3c | DB + sync worker (ETag + tombstone + fsnotify) | cc65864 |
| 4a | Admin UI panel (mevcut sayfalar) | cc65864 |
| 5 | Docker (slim + full) | cc65864 → 120becf (Go 1.24 bump) |
| 6 | Docs (11 .md) | cc65864 → 120becf (DEPLOY_BRF Round B/C ekleri) |
| 7 | GitLab CI/CD + goreleaser | cc65864 → 120becf (Go 1.24 bump) |
| 27 | Δ files.brf.sh → demo-fm.brf.sh rename | e40000c |
| Round A.1 | FTP driver | e40000c (subagent), test 235d437 |
| Round A.2 | Proxy-header auth | e40000c (subagent), test 235d437 |
| Round A.3 | Storage root path guard | e40000c (subagent), test 235d437 |
| Round A.4 | testutil import cycle düzeltme | 235d437 |
| Round B.1 | Persistent queue (sqlite/postgres/redis) | b7cf68c |
| Round B.2 | Notifications (webhook + bell) | be021f7 |
| Round B.3 | Replica wrapper + rules + reconcile + cron | 448523c |
| Round C | Admin UI: Replica/Notifications/Queue + Bell | 9fa5872 |
| Round D | CHANGELOG + DEPLOY_BRF + Caddyfile + HANDOVER | 120becf, bb1da90 |

## Bekleyen — Burak'ın yapacakları (operatör adımları, plan/HANDOVER.md detaylı)

### v0.1.0 release

```bash
git push origin main
git tag v0.1.0 -m "v0.1.0 — first public release"
git push --tags
```

GitLab CI tag pipeline (`.gitlab-ci.yml` rules `^v\d+\.\d+\.\d+`):
- `release:goreleaser` — 8-binary matrix (Linux/macOS/Windows × amd64/arm64)
- `release:npm` — `@brftech/{filex-core,filex,filex-react}` GitLab npm registry
- `release:docker` — `:slim`, `:full`, `:vX.Y.Z`, `:latest` GitLab container registry

### Standalone demo deploy (brkip)

Bkz. `deploy/README.md` — DNS + compose + Caddyfile + Keycloak client. Mecbur olunan tek adım `.env`'i doldurmak.

### V0.2 — brf-mono / fishapp entegrasyon

`plan/07-integration-and-release.md` §1 (A planı = frontend swap, B planı = full backend swap) ve §2. v0.1.0 stable çalıştıktan + birkaç hafta gözlemden sonra geçilir.

## Doğrulama (yapılması gereken son smoke testleri)

```bash
git pull origin main

# Backend
cd backend && go build ./... && go test ./...   # exit 0 bekleniyor

# Frontend
cd .. && pnpm -r build   # vue-tsc + vite build — exit 0

# (opsiyonel) Lokal Docker build
docker build -t filex:demo -f docker/Dockerfile .
docker run --rm -p 5212:5212 filex:demo
# /healthz → {"status":"ok"}
```

Hepsi yeşilse `git tag v0.1.0` aşamasına geçilebilir.
