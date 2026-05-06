# Δ Integration + Release

> brf-mono entegrasyon (consumer #1), fishapp PWA entegrasyon (consumer #2),
> v0.1.0 tag + demo-fm.brf.sh deploy.

---

## §1 — brf-mono entegrasyon (consumer #1)

### Niye
Burak: "brf-mono bu plugin'i ilk entegre eden proje olacak."

### Mevcut durum
brf-mono'nun `Modules/FishApp/` modülünde:
- Frontend: `brf-mono/resources/js/file-manager.ts` + `Modules/FishApp/resources/views/filament/file-manager.blade.php`
- Vendor paket: `brf-mono/resources/js/vendor/file-explorer/` (eski `@brftech/file-explorer` build'i)
- Backend: `Modules/FishApp/app/{Http/Controllers/FilesController.php, Services/{VuefinderService, ChunkedUploadService, ArchiveService, CapabilityService}.php, Jobs/GenerateThumbJob.php}`
- Routes: `routes/api.php` + `routes/web.php` (12+ endpoint)

### Strateji
**Burak (#9):** "brf-mono bu plugin'i ilk entegre eden proje olacak fish-app'te ikinci proje olacak. yani olan yerleri kaldırıp bu uygulamaya alacağız (içinde olan dosyaları da dahil edeceğiz tabii.)"

**Yorum:** Mevcut PHP backend (FishApp/app/Services/...) kalır mı, Go backend'e mi geçilir?

İki yol var:

**A) Frontend-only swap** (hafif, hızlı):
- `vendor/file-explorer/` SİL.
- `npm install @brftech/filex-core` veya GitLab npm registry'den.
- `file-manager.ts` import path değiş (`./vendor/file-explorer/...` → `@brftech/filex-core`).
- Backend PHP aynı kalır. Filex paketi sadece UI olarak swap edilir.
- Avantaj: Mevcut auth (Keycloak), share sistemi, rate limit korunur.
- Dezavantaj: Standalone filex'in Go backend'i kullanılmaz. brf-mono ile filex aynı backend'i konuşmuyor.

**B) Full backend swap** (ağır, güçlü):
- brf-mono `FilesController.php`, `VuefinderService.php` vb. SİL.
- brf-mono `Modules/FishApp` artık filex Go binary'sine reverse proxy.
- Filex Go binary brf-mono container'ı yanında ayrı servis olarak çalışır (örn. `filex` adlı container).
- File-manager UI doğrudan filex backend'iyle konuşur.
- Avantaj: Tek backend, tek codebase. Replica + queue + notify paylaşılır.
- Dezavantaj: Auth bridge gerek (Keycloak token → filex auth driver). Mevcut share sistemi farklı (Modules/FishApp `file_shares` tablosu vs filex `shares`).

**Önerim:** **A → B faz aktarım**. V0.1.0 release'de A. Filex stable olunca V0.2'de B.

### A planının adımları

1. Filex npm paket yayınla (`@brftech/filex-core` GitLab npm registry):
   ```bash
   cd /home/coder/filemanager/packages/core
   npm publish --registry=https://gitlab.com/api/v4/projects/<projID>/packages/npm/
   ```

2. brf-mono'da:
   ```bash
   cd /home/coder/brf-mono
   npm config set @brftech:registry https://gitlab.com/api/v4/projects/<projID>/packages/npm/
   npm install @brftech/filex-core
   ```

3. `resources/js/file-manager.ts`:
   ```ts
   - import FileExplorer from './vendor/file-explorer/file-explorer.js';
   + import { FileExplorer } from '@brftech/filex-core';
   + import '@brftech/filex-core/style.css';
   ```

4. `vendor/file-explorer/` SİL.

5. **API kontrat farkı varsa shim**: Eski paket Vuefinder kontratı kullanıyordu. Filex kontratı şu an farklı olabilir. `FilesController.php`'de Filex paketinin beklediği endpoint shape'ine göre response adapt et (gerekirse minor fix).

6. `npm run build` + deploy.

### B planının adımları (V0.2)

1. brf-mono'ya filex container'ı ekle (`docker-compose.yml`):
   ```yaml
   filex:
     image: gitlab.com/brftech/filemanager:v0.1.0
     environment:
       FILEMANAGER_OIDC_ISSUER: https://auth.brf.sh/realms/brf
       FILEMANAGER_OIDC_CLIENT_ID: filex
       FILEMANAGER_OIDC_CLIENT_SECRET: ...
       FILEMANAGER_DB_DSN: postgresql://filex:...@db/filex
       FILEMANAGER_QUEUE_DRIVER: postgres
       FILEMANAGER_STORAGE_DRIVER: s3
       FILEMANAGER_S3_PREFIX: fileman      # !!! root yasak
       FILEMANAGER_REPLICA_DRIVER: s3
       FILEMANAGER_REPLICA_S3_PREFIX: fileman
   ```

2. `Modules/FishApp/Filament/Pages/FileManager.php` page'i filex iframe'ine ya da Vue mount'una çevir.

3. `FilesController.php` SİL.

4. Mevcut `file_shares` tablosu → filex `shares` tablosuna migrate et (one-time data migration script).

### Tahmini efor
- A planı: 3-4 saat
- B planı: 2-3 gün

---

## §2 — fishapp PWA entegrasyon (consumer #2)

### Mevcut durum
`fishprogram/` (WSL `/home/buhac/webstorm/fishprogram`):
- `src/views/FilesPage.vue` (Ionic ion-page)
- `src/vendor/file-explorer/` (eski build)
- Sanctum bearer auth

### Adımlar
1. `npm install @brftech/filex-core` (GitLab registry config)
2. `vendor/file-explorer/` SİL
3. `FilesPage.vue`:
   ```ts
   - import FileExplorer from '@/vendor/file-explorer/file-explorer.js';
   + import { FileExplorer } from '@brftech/filex-core';
   ```
4. PWA Sanctum token → filex `auth: { kind: 'bearer', token }` config alanı
5. `npm run build` + WSL → main rsync `dist/` → `fish.brf.sh`

### Bağımlılık
brf-mono entegrasyonu sonra (test edilmiş frontend pakedi olsun).

### Tahmini efor
- 2-3 saat

---

## §3 — v0.1.0 release

### Adımlar
1. **Audit** — tüm delta'lar tamam mı:
   ```bash
   cd /home/coder/filemanager
   go build ./...
   go test ./...
   pnpm -r build
   pnpm -r test
   pnpm e2e
   ```

2. **CHANGELOG.md** doldur:
   ```markdown
   ## v0.1.0 - 2026-XX-XX
   ### Added
   - Standalone Go binary + monorepo (Vue/WC/React)
   - Auth drivers: local, oidc, ldap, proxy-header
   - Storage drivers: local, s3, ftp, sftp, webdav
   - DB drivers: sqlite (default), mysql, postgres
   - Queue drivers: sqlite (default), redis, postgres
   - Replica storage layer (mirror/append-only/skip rules)
   - Reconciliation + cron status report
   - Notifications (webhook ingest + in-app bell)
   - Role-based admin button
   - Storage prefix UI (root path forbidden)
   - OnlyOffice + Drawio integration
   - Bleve search
   - Trash, quota, audit, capability
   ```

3. **Tag**:
   ```bash
   git tag v0.1.0
   git push --tags
   ```

4. **Release page** (GitLab Release):
   - Asset: Docker image link, npm package link, binary download (goreleaser)
   - Notes: CHANGELOG kopya

5. **GitLab CI tag pipeline** çalışsın:
   - npm publish (private GitLab registry)
   - Docker push (private GitLab Container Registry)
   - Goreleaser binary

---

## §4 — demo-fm.brf.sh deploy

### Mevcut durum
- `deploy/demo-fm.brf.sh.compose.yml` hazır (rename agent commit'inde)
- `deploy/nginx.demo-fm.brf.sh.conf` hazır
- `deploy/keycloak-client-filex.json` hazır

### Burak'ın isteği
"caddy(brkip) tarafında demoya ihtiyaç yok drsite olarak koyabiliriz"

### Adımlar (brkip Caddy tarafında)
1. **DNS** — `demo-fm.brf.sh` A record → brkip public IP (proxied:false). brf.sh zone'da:
   ```bash
   curl -X POST -H "X-Auth-Email: ..." -H "X-Auth-Key: $CF_TOKEN" \
     "https://api.cloudflare.com/client/v4/zones/15a5559714ccad6709385b135d89efd3/dns_records" \
     -H "Content-Type: application/json" \
     -d '{"type":"A","name":"demo-fm","content":"88.228.71.208","proxied":false,"ttl":1}'
   ```

2. **brkip Caddy config** — `/opt/brkip-stack/caddy/Caddyfile.d/demo-fm.brf.sh`:
   ```
   demo-fm.brf.sh {
     tls internal
     reverse_proxy 127.0.0.1:5212
   }
   ```
   (Caddy internal CA brkip'te kullanılıyor.)

3. **Compose deploy** — `/opt/brkip-stack/filex-demo/docker-compose.yml`:
   ```yaml
   services:
     filex-demo:
       image: registry.gitlab.com/brftech/filemanager:v0.1.0
       restart: unless-stopped
       ports: ["127.0.0.1:5212:5212"]
       environment:
         FILEMANAGER_LISTEN: 0.0.0.0:5212
         FILEMANAGER_BASE_URL: https://demo-fm.brf.sh
         FILEMANAGER_DB_DRIVER: sqlite
         FILEMANAGER_DB_DSN: /data/filex.db
         FILEMANAGER_STORAGE_DRIVER: local
         FILEMANAGER_LOCAL_PATH: /data/files
         FILEMANAGER_AUTH_DRIVERS: local
         FILEMANAGER_DEMO_MODE: true            # readonly - demo amaçlı
         FILEMANAGER_DEMO_USER: demo
         FILEMANAGER_DEMO_PASS: demo
       volumes:
         - ./data:/data
   ```

4. **Sample dosyalar** (`./data/files/demo-fm/`):
   - 5-10 örnek dosya (image, pdf, code, office sample)
   - Kullanıcılar readonly browse + preview yapabilsin

5. **Caddy reload** + test:
   ```bash
   ssh brkip "docker compose -f /opt/brkip-stack/filex-demo/docker-compose.yml up -d"
   ssh brkip "docker exec brkip-caddy caddy reload --config /etc/caddy/Caddyfile"
   curl -I https://demo-fm.brf.sh/healthz
   ```

6. **Notify Uptime monitor ekle**:
   - brf-mono Modules/Uptime'a yeni monitor: `demo-fm.brf.sh /healthz` (60s interval, group `demo`)

7. **Burak doğrula**: tarayıcıdan https://demo-fm.brf.sh aç, login: demo/demo, sample dosyaları gör.

### Tahmini efor
- 1-2 saat

---

## Toplam yol haritası özet

| Faz | Süre |
|-----|------|
| Round A doğrulama (FTP, root guard, proxy-header) | 2-4 saat |
| Round B (queue → notify → replica) | 6 gün |
| Round C (frontend admin UI delta sayfaları + role button) | 2 gün |
| Round D §1 (brf-mono A planı) | 0.5 gün |
| Round D §2 (fishapp PWA) | 0.5 gün |
| Round D §3 (release v0.1.0) | 0.5 gün |
| Round D §4 (demo-fm.brf.sh deploy) | 0.5 gün |

**Toplam:** ~10 gün full-time, lokalde.
