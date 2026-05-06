# HANDOVER — v0.1.0-rc (2026-05-06, eveninɡ)

> Önceki Coder workspace patladı, plan dosyaları lokal'e taşındı (commit
> `e40000c`). Bu HANDOVER lokalde Round A doğrulama + B + C + D
> hazırlığı bitirildikten sonra yazıldı.

---

## TL;DR — neresi tamam, neresi kaldı

| Round | Durum | Commit |
|-------|-------|--------|
| A — testutil cycle fix | ✅ | `235d437` |
| A — FTP / proxy-header / root guard validation | ✅ (commit'te) | `e40000c` (önceden) |
| B — persistent queue | ✅ | `b7cf68c` |
| B — notifications | ✅ | `be021f7` |
| B — replica + rules + reconcile + cron | ✅ | `448523c` |
| C — admin UI delta pages | ✅ | `9fa5872` |
| D — release prep (CHANGELOG + deploy env + docs) | ✅ | `120becf` |
| D — git tag v0.1.0 + GitLab Release | ⏳ Burak'a kaldı |
| D — brf-mono A planı (frontend swap) | ⏳ Burak'a kaldı |
| D — fishapp PWA frontend swap | ⏳ Burak'a kaldı |
| D — demo-fm.brf.sh deploy (brkip Caddy) | ⏳ Burak'a kaldı |

`go build ./...` + `go test ./...` + `pnpm -r build` hepsi yeşil.

---

## Burak'ın yapacakları (ortam-spesifik, lokal Claude tutamadı)

### 1. v0.1.0 release tag

```bash
cd /g/mail/filemanager
git push origin main
git tag v0.1.0 -m "v0.1.0 — first public release"
git push --tags
```

GitLab CI tag pipeline:
- `npm publish` (private GitLab npm registry — `@brftech/filex-core`,
  `@brftech/filex`, `@brftech/filex-react`).
- Docker `docker push registry.gitlab.com/brftech/filemanager:v0.1.0`.
- Goreleaser binary matrix (Linux / macOS / Windows × amd64 / arm64).

GitLab Release sayfasına `CHANGELOG.md` `[0.1.0]` bölümü kopyala.

### 2. brf-mono entegrasyon (A planı — frontend swap)

`plan/07-integration-and-release.md` §1.A.

```bash
cd /g/mail/brf-mono
pnpm config set @brftech:registry https://gitlab.com/api/v4/projects/<projID>/packages/npm/
pnpm install @brftech/filex-core
```

`resources/js/file-manager.ts`:

```diff
- import FileExplorer from './vendor/file-explorer/file-explorer.js';
+ import { FileExplorer } from '@brftech/filex-core';
+ import '@brftech/filex-core/style.css';
```

`resources/js/vendor/file-explorer/` SİL.

`Modules/FishApp/Http/Controllers/FilesController.php` shim'i — Filex
paketinin beklediği endpoint shape'ine göre response adapt et (eski
Vuefinder kontratı yerleşkten farklı olabilir; mevcut endpoint'leri
mock POST ile smoke test et).

### 3. fishapp PWA

`plan/07-integration-and-release.md` §2.

```bash
cd ~/webstorm/fishprogram
npm install @brftech/filex-core
```

`src/views/FilesPage.vue`:

```diff
- import FileExplorer from '@/vendor/file-explorer/file-explorer.js';
+ import { FileExplorer } from '@brftech/filex-core';
```

`src/vendor/file-explorer/` SİL. Sanctum bearer token Filex `auth: {
kind: 'bearer', token }` config alanıyla geçer.

`pnpm build` + WSL → main rsync → `fish.brf.sh/dist/`.

### 4. demo-fm.brf.sh deploy (brkip Caddy)

`plan/07-integration-and-release.md` §4.

```bash
# DNS — brf.sh zone
curl -X POST -H "X-Auth-Email: ..." -H "X-Auth-Key: $CF_TOKEN" \
  "https://api.cloudflare.com/client/v4/zones/15a5559714ccad6709385b135d89efd3/dns_records" \
  -H "Content-Type: application/json" \
  -d '{"type":"A","name":"demo-fm","content":"88.228.71.208","proxied":false,"ttl":1}'

# Caddy
ssh brkip "cat > /opt/brkip-stack/caddy/Caddyfile.d/demo-fm.brf.sh.caddy <<'EOF'
demo-fm.brf.sh {
  tls internal
  reverse_proxy 127.0.0.1:5212
}
EOF"

# Compose
ssh brkip "mkdir -p /opt/brkip-stack/filex-demo && cd /opt/brkip-stack/filex-demo && \
  curl -s https://gitlab.com/brftech/filemanager/-/raw/v0.1.0/deploy/demo-fm.brf.sh.compose.yml \
    > docker-compose.yml && \
  docker compose up -d"

# Caddy reload
ssh brkip "docker exec brkip-caddy caddy reload --config /etc/caddy/Caddyfile"

# Smoke
curl -fsS https://demo-fm.brf.sh/healthz
```

Notify ekle: brf-mono Modules/Uptime → yeni monitor `demo-fm.brf.sh`
(`/healthz`, 60s, group `demo`).

---

## Test sonuçları

```bash
$ cd /g/mail/filemanager/backend
$ go build ./...      # exit 0
$ go test ./...       # all packages OK
$ cd ../web
$ pnpm build          # ✓ built in ~9s
```

Yeni paket eklemeleri:

| Paket | Commit | Boyut (gzip JS) |
|-------|--------|-----------------|
| `internal/queue` | `b7cf68c` | 3 driver + worker pool (1.4 kB asset added to admin) |
| `internal/notify` | `be021f7` | webhook+bell+settings (Bell component bundled into vue-vendor) |
| `internal/storage/replicated.go` + `internal/replica` | `448523c` | rules engine + reconcile + cron |
| `web/src/views/{Replica, Notifications, Queue}.vue` | `9fa5872` | Replica 4.78 kB / Notifications 2.61 kB / Queue 2.63 kB |

---

## v0.2 yapılacaklar (CHANGELOG'daki "Known Gaps")

1. **brf-mono B planı** — filex Go binary'yi tek backend olarak çalıştır.
   PHP `Modules/FishApp/Services/{VuefinderService, ChunkedUploadService,
   ArchiveService, CapabilityService}.php` SİL. brf-mono ↔ filex auth
   bridge (Keycloak token → filex auth driver). `file_shares` tablosu
   → filex `shares` tablosu one-time data migration.
2. **Replica auto-pairing** — `storages.role` + `replica_of_id` admin
   UI'dan ayarlanmalı (şu an SQL ile manuel set ediyor; bootstrap
   ReplicatedDriver'ı buna göre wrap'lasın).
3. **E2E Playwright** suite genişlet — yeni admin sayfaları (Replica,
   Queue, Notifications) flow testleri.
4. **Sentry SDK** entegrasyonu (`getsentry/sentry-go`) — relay.brf.sh
   üzerinden TR DPI bypass'lı + DSN env'de.
5. **Çoklu replica** desteği (V0.3+) — şu an tek primary + tek replica
   (Burak I2). DB'de `replica_of_id` çoklu replikalar için zaten foreign
   key, sadece bootstrap çoklu fan-out'a girmiyor.

---

## Subagent durumu (Round B'deki postgres + redis driver'lar)

Round B persistent queue'da subagent'lar paralel çalıştırıldı:

| Subagent | Çıktı | Doğrulanma |
|----------|-------|------------|
| postgres queue driver | `internal/queue/drivers/postgres/postgres.go` | `go build` + raporda 469 satır, JSONB::text scan trick |
| redis queue driver | `internal/queue/drivers/redis/redis.go` | `go build` + raporda 889 satır, BRPOPLPUSH + scheduled ZSET promoter |

Her iki driver da blank-import edildi (server.go), `go.mod` jackc/pgx
+ go-redis/v9 pin'lendi. SQLite test suite kontratı da bağlayıcı —
postgres/redis canlı entegrasyon testleri V0.2'de eklenecek (CI matrix
gerek).

---

## Soruların olursa

`SPEC.md` + `plan/00-ROADMAP.md` + bu HANDOVER kombosu yeter. Round
B/C komutları + dosya yolları her commit'in body'sinde detaylı.
Round D §1-§4 adımları yukarıda; her biri `plan/07-integration-and-
release.md`'ye işaret ediyor.

İyi şanslar — v0.1.0 hazır 🚀
