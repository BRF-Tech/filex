# HANDOVER — Burak'ın lokal'inden devam (2026-05-06)

> Bu dosya: Coder workspace'i Go derleme yükünden patladığı için planı dosyalara döküp Burak'ın lokal makinesine bırakıyoruz. Burak lokalden devam edecek.

---

## TL;DR

1. `git pull origin main`
2. `cat plan/00-ROADMAP.md` (yol haritası)
3. `cat SPEC.md` (tüm karar matrisi + mimari)
4. `cd backend && go build ./...` — patlarsa Round A WIP düzelt
5. WIP düzeldikten sonra Round B'den devam (queue → notify → replica)

---

## Bu commit ne içeriyor

### Yeni dosyalar
- `SPEC.md` (root) — Tek dosyada tüm spec
- `plan/00-ROADMAP.md` — Yol haritası + tamamlanan/kalan tablosu
- `plan/01-storage-deltas.md` — FTP, root guard, prefix UI
- `plan/02-auth-deltas.md` — Proxy-header, role-based admin
- `plan/03-queue.md` — Persistent queue (driver-based)
- `plan/04-notify.md` — Notifications (webhook + in-app)
- `plan/05-replica.md` — Replica storage layer + rules + reconcile + cron
- `plan/06-admin-ui.md` — Admin UI delta sayfaları
- `plan/07-integration-and-release.md` — brf-mono, fishapp, v0.1.0, demo deploy
- `plan/HANDOVER.md` — bu dosya

### WIP delta kodları (subagent'lardan, build doğrulanmamış)
- `backend/internal/storage/drivers/ftp/ftp.go` + `ftp_test.go`
- `backend/internal/auth/drivers/proxyheader/proxyheader.go` + `proxyheader_test.go`
- `backend/internal/storage/validate.go` + `validate_test.go`
- `backend/cmd/filex/main.go` (modifiye — yeni driver blank import'ları eklenmiş olabilir)
- `backend/internal/api/handlers/storages.go` (modifiye — root guard çağrısı?)
- `backend/internal/server/server.go` (modifiye — bilmiyorum, kontrol et)
- `backend/go.mod` + `backend/go.sum` (jlaffaye/ftp dep eklenmiş)

### Demo URL rename (tamam)
- `deploy/files.brf.sh.compose.yml` → `demo-fm.brf.sh.compose.yml`
- `deploy/nginx.files.brf.sh.conf` → `nginx.demo-fm.brf.sh.conf`
- `deploy/keycloak-client-filex.json` (içerik update)
- `docs/DEPLOY_BRF.md` (içerik update)
- `docs/MIGRATION_FISHAPP.md` (URL update)
- `deploy/.env.example` (yorum update)
- `deploy/README.md` (tablo update)

---

## Kararlar (Burak onayladı, SPEC.md §1)

| # | Konu | Karar |
|---|------|-------|
| 1 | License | Şimdilik ertelendi |
| 2 | Container Registry | GitLab private (sonra GitHub public) |
| 3 | Demo URL | `demo-fm.brf.sh` (brkip Caddy DR-site) |
| 4 | Auth | Basic + OIDC. Hazır paketler: `coreos/go-oidc`, `chi BasicAuth` |
| 5 | Queue persistence | ZORUNLU, driver-based (sqlite/redis/postgres) |
| 6 | Admin yetki | Role-based: JWT claim `roles:["admin"]` veya local user.role |
| 7 | Yapım | Subagent paralel iş bölümü |
| 8 | DB | Driver-based: sqlite default, postgres, mysql |
| 9 | Migration consumer'lar | brf-mono ilk, fishapp ikinci |
| 10 | Mevcut paketten geçiş | Standalone uygulama hedefi |
| A1 | Replica yazımı | Async + kullanıcı tanımlı cron status raporu (preset+raw) |
| A2 | Read fallback | Primary fail → replica + bildirim (webhook + opsiyonel in-app) |
| A3 | Replica delete | Mirror default; path-based rule (mirror/append-only/skip) |
| A4 | Reconciliation | UI'da "Fix" butonu (replica_failures'tan retry) |
| B | Storage drivers V0.1 | S3, Local, FTP, SFTP, WebDAV (5 driver) |
| C | Storage prefix | Driver config'inde zorunlu, root yasak |
| D | Admin role | JWT claim/local user.role/env CSV |
| E1 | Replica rules | DB tablo + Admin UI form |
| E2 | Default rule yokken | Mirror |
| F1 | Cron formatı | Preset + advanced raw |
| F2 | Status persistence | Sadece son rapor DB; webhook ingest tam detay |
| F3 | Rapor içeriği | UI özet, DB tekil paginated, webhook tam |
| G1 | Webhook payload | Generic JSON (Slack/Discord template yok) |
| G2 | In-app | Bell + history + read/unread |
| G3 | Event listesi | Tümü ingest'e echo |
| H1 | WebDAV auth | Basic only |
| H2 | FTP/SFTP | İki ayrı driver |
| H3 | GCS/Azure | Hayır, S3 yeterli |
| I1 | Storage config | .env bootstrap + Admin UI override |
| I2 | Multi-storage | Tek primary; çift istiyorsa iki kurulum |
| J1 | Root klasör | YASAK; primary+replica alt klasör zorunlu |

---

## Lokal'de ilk kontrol

```bash
cd /home/coder/filemanager   # veya senin lokal yolun
git pull origin main

# Subagent'ların yarım bıraktığı durumu gör:
git log -1 --stat
git status

# Backend build:
cd backend
go mod tidy
go build ./...
go test ./...

# Frontend build:
cd ..
pnpm install
pnpm -r build
pnpm -r test

# E2E (storage erişimi gerek):
pnpm e2e
```

### Build patlarsa
Subagent'lar tüm raporlarını bitirmeden Coder durdu. Yarı yazılmış kod olabilir.

**Yapacakların:**
1. `git diff backend/cmd/filex/main.go` — yeni driver blank import'ları doğru mu?
2. `git diff backend/internal/api/handlers/storages.go` — `storage.ValidateNonRootPath` çağrısı doğru import edilmiş mi?
3. `git diff backend/internal/server/server.go` — neye dokunulmuş?
4. `cat backend/internal/storage/drivers/ftp/ftp.go` — Driver methodları tam mı (List/Stat/Read/Write/Move/Copy/Delete/Mkdir)?
5. `cat backend/internal/auth/drivers/proxyheader/proxyheader.go` — auth.Driver interface uyumlu mu?

İhtiyaç olursa **plan dosyalarındaki kod örneklerini referans al**:
- FTP → `plan/01-storage-deltas.md` §1
- Proxy-header → `plan/02-auth-deltas.md` §1
- Root guard → `plan/01-storage-deltas.md` §2

### Hızlı yapım yolu (Round A doğrulandıktan sonra)

1. **Round B — Queue** (`plan/03-queue.md`):
   - `backend/internal/queue/` klasörünü sıfırdan yaz
   - Migration `00006_queue.sql`
   - Bootstrap `cmd/filex/main.go`'da
   - Test: SQLite round-trip

2. **Round B — Notify** (`plan/04-notify.md`):
   - `backend/internal/notify/` klasörünü sıfırdan yaz
   - Migration `00007_notifications.sql`
   - Webhook delivery + retry
   - DB store methods (3 dialekt)
   - API endpoints

3. **Round B — Replica** (`plan/05-replica.md`):
   - Queue + Notify hazır olduktan sonra
   - `backend/internal/storage/replicated.go` wrapper
   - Migration `00008_replica.sql`
   - Rule engine + reconcile + cron report
   - API endpoints

4. **Round C — Admin UI** (`plan/06-admin-ui.md`):
   - `web/src/views/{Replica,Notifications,Queue}.vue`
   - `NotificationBell.vue` component
   - Stores + API clients + i18n + router

5. **Round D — Release** (`plan/07-integration-and-release.md`):
   - brf-mono A planı (frontend swap)
   - fishapp PWA
   - v0.1.0 tag + GitLab release
   - demo-fm.brf.sh brkip Caddy deploy

---

## Subagent durumu (referans)

Coder workspace'inde 6 paralel subagent başlatıldı. Sadece 1 tanesinden rapor alındı (demo rename — tamam). Diğerleri kod üretti ama rapor vermeden Coder durdu.

| Subagent | Rapor | Çıktı (lokal'de doğrula) |
|----------|-------|--------------------------|
| Demo URL rename | ✅ | deploy/ + docs/ commit'te |
| FTP driver | ❌ rapor yok | `backend/internal/storage/drivers/ftp/` mevcut |
| Proxy-header auth | ❌ rapor yok | `backend/internal/auth/drivers/proxyheader/` mevcut |
| Root path guard | ❌ rapor yok | `backend/internal/storage/validate*.go` mevcut |
| Persistent queue | ❌ output yok | `backend/internal/queue/` muhtemelen yok — sıfırdan |
| Notifications | ❌ output yok | `backend/internal/notify/` muhtemelen yok — sıfırdan |

---

## Build guard notları (CLAUDE.md'den hatırlatma)

- Türkçe konuş
- Direkt çöz, gevezelik yok
- Pre-push hook + GitLab CI smoke test (build guard'ları)
- Yeni Laravel 13 / Filament v5 tuzakları çıkarsa CLAUDE.md tablosuna ekle (consumer projeler için)
- Filex Go bir backend olduğu için Laravel guard'ları doğrudan uygulanmaz, AMA brf-mono/fishapp consumer entegrasyonunda dikkat

---

## Sentry / izleme

- Filex repo'su Sentry'ye bağlı değil (henüz). brf-mono `brftech/brf-mono` projesi var (`memory/sentry_access.md`).
- V0.2'de filex Sentry SDK entegrasyonu — `getsentry/sentry-go`.

---

## Soruların olursa

Lokalde Claude Code çalıştır, `cat SPEC.md && cat plan/00-ROADMAP.md && cat plan/HANDOVER.md` ile başla. Sonra hangi delta'yı yapıyorsan ilgili plan dosyasını oku.

İyi şanslar 🍀
