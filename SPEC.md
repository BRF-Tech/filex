# filex — SPEC v0.1.0

> Self-hosted file manager. Tek paket, tek binary, tek Docker image.
> Multi-framework frontend (Vue / Web Component / React) + Go backend.

**Repo:** `git@gitlab.com:brftech/filemanager.git` (private GitLab şimdilik)
**Demo URL:** `demo-fm.brf.sh` (brkip Caddy, DR-site, readonly)
**Versiyon:** v0.1.0 hedef · şu an v0.1.0-dev (commit `cc65864`)

---

## 1. Karar matrisi (Burak onayladı)

| # | Konu | Karar |
|---|------|-------|
| 1 | License | Erteli, sonra konuşulacak |
| 2 | Container Registry | GitLab private (sonra GitHub public mirror) |
| 3 | Demo URL | `demo-fm.brf.sh` (brkip Caddy DR-site) |
| 4 | Auth | Basic + OIDC (V0.1.0). Hazır paketler: `coreos/go-oidc`, `chi BasicAuth` |
| 5 | Queue persistence | **ZORUNLU**, driver-based (sqlite default, redis, postgres) |
| 6 | Admin yetki | Role-based: JWT claim `roles:["admin"]` veya local user.role; UI'da admin butonu |
| 7 | Yapım | Hepsini bu Claude oturumu yapacak |
| 8 | DB | driver-based: sqlite default, postgres, mysql |
| 9 | Migration consumer'lar | brf-mono ilk, fishapp ikinci |
| 10 | Mevcut paketten geçiş | Standalone uygulama hedefi; konsumer migration ayrı |
| A1 | Replica yazımı | **Async** + kullanıcı tanımlı cron ile status raporu (preset+raw) |
| A2 | Read fallback | Primary fail → replica'dan oku + bildirim (webhook + opsiyonel in-app) |
| A3 | Replica delete | Mirror default; path-based rule sistemi ile kullanıcı override eder |
| A4 | Reconciliation | UI'da "Fix" butonu (replica_failures DB tablosundan retry) |
| B | Storage drivers | V0.1.0: S3, Local, FTP, SFTP, WebDAV (5 driver). Adapter modüler |
| C | Storage prefix | Storage config'inde `prefix`/`path` alanı zorunlu, root yasak |
| D | Admin role mekanizması | JWT claim ya da local user.role; en basit yol |
| E1 | Replica rules | DB tablosu + Admin UI form (path glob + mode) |
| E2 | Default rule yokken | Mirror |
| F1 | Cron formatı | Preset (saatlik/günlük/haftalık) + advanced raw cron |
| F2 | Status persistence | Sadece son rapor DB'de; webhook ingest ile kullanıcı kendi sistemine post eder |
| F3 | Rapor içeriği | UI özet, DB tekil detay (paginated), webhook payload tam |
| G1 | Webhook payload | Generic JSON `{event, severity, title, body, meta}`. Preset (Slack/Discord) yok |
| G2 | In-app | Bell + history + read/unread |
| G3 | Event listesi | Tümü ingest'e echo: replica_fail, reconcile_done, quota_near, queue_stuck, auth_fail_spike, disk_full |
| H1 | WebDAV | Sadece Basic auth (OAuth kullanan kendi token üretir) |
| H2 | FTP/SFTP | İki ayrı driver |
| H3 | GCS/Azure | Hayır, S3 yeterli. Adapter modüler, isteyen yazar |
| I1 | Storage config kaynağı | `.env` bootstrap + Admin UI override |
| I2 | Multi-storage | Tek primary; çift istiyorsa iki kurulum |
| J1 | Root klasör | YASAK. Primary ve replica ikisi de alt klasör (örn. `fileman/`) zorunlu |

---

## 2. Mimari özet

```
┌─────────────────────────────────────────────────────────────────┐
│  filex (single Go binary, ~40 MB slim / ~250 MB w/ thumbnails) │
├─────────────────────────────────────────────────────────────────┤
│  HTTP API (chi)              │  Admin UI (Vue 3, embedded)       │
│  Auth Drivers:               │  local · oidc · proxy-header      │
│  Storage Drivers:            │  local · s3 · ftp · sftp · webdav │
│  DB Drivers:                 │  sqlite (default) · mysql · pg    │
│  Queue Drivers:              │  sqlite (default) · redis · pg    │
│  Sync Worker:                │  ETag diff + tombstone guard      │
│  Search:                     │  Bleve (full-text, embedded)      │
│  Thumbnails:                 │  image · video · pdf · office     │
│  Plug & Play:                │  OnlyOffice · Drawio · Mermaid    │
│  REPLICA layer:              │  primary→replica wrapper          │
│  Notifications:              │  webhook ingest + in-app bell     │
└─────────────────────────────────────────────────────────────────┘
                              ▲
                              │ HTTP API
       ┌──────────────────────┼──────────────────────┐
       │                      │                      │
   @brftech/             @brftech/             @brftech/
   filex-core            filex                 filex-react
   (Vue 3 SFC)           (Web Component)       (React adapter)
       │                      │                      │
       ▼                      ▼                      ▼
   Vue 3 apps           Any framework            React apps
                        (vanilla, Angular, Svelte, Solid, …)
```

---

## 3. Mevcut durum (commit `cc65864`)

✅ **Tamam:**
- Monorepo (pnpm-workspace), `packages/{core,webcomponent,react}`, `web/`, `e2e/`, `demo/`
- Backend: chi router, auth (local+oidc+ldap), storage (local+s3+sftp+webdav), db (sqlite+mysql+postgres), sync worker, search (Bleve), trash, quota, ops, audit, capability
- Driver registry pattern (storage/auth/db) — interface based, AList-style sub-interfaces
- E2E (Playwright) tests scaffolded
- Docs (README, INSTALLATION, CONFIGURATION, API, BACKEND, DOCKER, ARCHITECTURE, MIGRATION, MIGRATION_FISHAPP, DEPLOY_BRF, CONTRIBUTING)
- Docker (slim + full Dockerfile)
- GitLab CI pipeline + goreleaser

❌ **Eksik (delta):**
- Replica storage layer (wrapper, rules, reconciliation, status report)
- Real persistent queue (driver-based) — şu an `sync_runs` stand-in
- Notifications (webhook ingest + in-app bell + read/unread)
- FTP driver (sftp var, ftp ayrıca yok)
- Proxy-header auth driver (README'de var, kod yok)
- Storage root path guard ("/" yasak)
- Storage prefix UI (S3 backend desteği var, UI form alanı eksik diğer driver'lar için)
- Role-based admin button (FileExplorer toolbar'da)
- demo-fm.brf.sh isim güncellemesi (deploy/ klasöründe `files.brf.sh` yazıyor)

---

## 4. Yeni modüller — detaylı tasarım

### 4.1 Replica storage layer

#### Wrapper
```go
// backend/internal/storage/replicated.go
package storage

type ReplicatedStorage struct {
    primary Driver
    replica Driver  // optional, nil iff disabled
    rules   RuleEngine
    failures FailureRecorder
    notifier Notifier
}

func (r *ReplicatedStorage) Write(ctx, path, body, size) error {
    if err := r.primary.(Writer).Write(ctx, path, body, size); err != nil {
        return err
    }
    if r.replica == nil { return nil }
    mode := r.rules.Match(path)  // mirror | append-only | skip
    if mode == "skip" { return nil }
    // Async (Burak A1):
    go func() {
        if err := r.replica.(Writer).Write(ctx, path, body, size); err != nil {
            r.failures.Record(ctx, path, "write", err)
            r.notifier.Notify(ctx, EventReplicaFail, ...)
        }
    }()
    return nil
}

func (r *ReplicatedStorage) Read(ctx, path) (ReadCloser, error) {
    rc, err := r.primary.Read(ctx, path)
    if err == nil { return rc, nil }
    if r.replica == nil { return nil, err }
    // Read fallback (Burak A2):
    rc2, err2 := r.replica.Read(ctx, path)
    if err2 == nil {
        r.notifier.Notify(ctx, EventPrimaryReadFail, ...)  // bildirim
        return rc2, nil
    }
    return nil, err
}

func (r *ReplicatedStorage) Delete(ctx, path) error {
    if err := r.primary.(Deleter).Delete(ctx, path); err != nil {
        return err
    }
    if r.replica == nil { return nil }
    mode := r.rules.Match(path)
    if mode == "append-only" { return nil }
    if mode == "skip" { return nil }
    // mirror (default Burak A3):
    go r.replica.(Deleter).Delete(ctx, path)
    return nil
}
```

#### DB tabloları
```sql
CREATE TABLE replica_rules (
  id          INTEGER PRIMARY KEY,
  path_pattern VARCHAR(512) NOT NULL,    -- glob pattern
  mode        VARCHAR(16) NOT NULL,       -- mirror | append_only | skip
  priority    INTEGER NOT NULL DEFAULT 100,
  enabled     BOOLEAN NOT NULL DEFAULT TRUE,
  created_at  TIMESTAMP NOT NULL,
  updated_at  TIMESTAMP NOT NULL
);
CREATE INDEX idx_replica_rules_priority ON replica_rules (priority, enabled);

CREATE TABLE replica_failures (
  id          INTEGER PRIMARY KEY,
  path        VARCHAR(1024) NOT NULL,
  op          VARCHAR(16) NOT NULL,       -- write | delete
  error_code  VARCHAR(64),
  error_msg   TEXT,
  attempts    INTEGER NOT NULL DEFAULT 1,
  last_attempt_at TIMESTAMP NOT NULL,
  resolved_at TIMESTAMP NULL,
  UNIQUE(path, op)                        -- idempotent — aynı path+op tek kayıt, attempts++
);
CREATE INDEX idx_replica_failures_unresolved ON replica_failures (resolved_at) WHERE resolved_at IS NULL;

CREATE TABLE replica_status_reports (
  id          INTEGER PRIMARY KEY,
  generated_at TIMESTAMP NOT NULL,
  total_files  INTEGER,
  failed_count INTEGER,
  repaired_count INTEGER,
  summary_json TEXT,
  -- Sadece son rapor (Burak F2): yeni rapor üretildiğinde eskileri DELETE
);
```

#### Reconciliation ("Fix")
```
POST /admin/api/replica/fix
  → replica_failures'tan unresolved tüm rows enqueue queue (op=replica_retry)
  → queue worker her path için replica.Write() retry
  → başarılıysa replica_failures.resolved_at = NOW()
  → bildirim: replica_reconcile_done (özet)

POST /admin/api/replica/report/run-now
  → cron işini hemen tetikle
```

#### Cron status report
- Kullanıcı cron ayarlar (`replica_settings.report_cron`).
- Job: tüm storage'ı listele, primary'de var ama replica'da yok olanları say.
- Sonuç: replica_status_reports (yalnız 1 satır — yeni geldiğinde eskisi silinir).
- Webhook ingest event: `replica_status_report` payload'ında özet + tüm path listesi.

### 4.2 Queue (driver-based persistent)

```go
// backend/internal/queue/driver.go
package queue

type Driver interface {
    Init(ctx context.Context, cfg map[string]any) error
    Enqueue(ctx context.Context, op Op) (string, error)
    Dequeue(ctx context.Context, types []string) (Op, error)  // blocking
    Ack(ctx context.Context, id string) error
    Fail(ctx context.Context, id string, err error) error
    List(ctx context.Context, status string, limit int) ([]Op, error)
    Stats(ctx context.Context) (Stats, error)  // pending, running, failed
}

type Op struct {
    ID         string
    Type       string             // copy | move | delete | replica_retry | reconcile | thumb
    Payload    map[string]any
    Status     string             // pending | running | done | failed
    Attempts   int
    LastError  string
    EnqueuedAt time.Time
    StartedAt  *time.Time
    FinishedAt *time.Time
}
```

3 driver:
- **sqlite** (default): `ops_queue` tablosu, `SELECT FOR UPDATE SKIP LOCKED` yerine `BEGIN IMMEDIATE; UPDATE ... WHERE status='pending' LIMIT 1 RETURNING ...`.
- **redis**: `LPUSH/RPOPLPUSH` (work queue pattern), running list ve dead-letter list.
- **postgres**: `SELECT FOR UPDATE SKIP LOCKED` (üretim grade).

ENV:
```
FILEMANAGER_QUEUE_DRIVER=sqlite|redis|postgres
FILEMANAGER_QUEUE_REDIS_URL=redis://...
FILEMANAGER_QUEUE_PG_DSN=postgres://...
```

Worker pool: `FILEMANAGER_QUEUE_WORKERS=4` (default 4).

### 4.3 Notifications

```go
// backend/internal/notify/types.go
type Event struct {
    Event    string         `json:"event"`     // replica_fail | quota_near_full | ...
    Severity string         `json:"severity"`  // info | warning | error | critical
    Title    string         `json:"title"`
    Body     string         `json:"body"`
    Meta     map[string]any `json:"meta"`      // extra (paths, counts, ts, etc.)
    TS       time.Time      `json:"ts"`
}
```

Event listesi:
| Event | Severity | Trigger |
|-------|----------|---------|
| `replica_fail` | warning | tek replica yazımı başarısız |
| `replica_fail_spike` | error | 5 dk içinde >100 fail |
| `replica_reconcile_done` | info | Fix tamamlandı |
| `replica_status_report` | info | Cron tetiklediği rapor |
| `primary_read_fail` | error | primary read fail, replica fallback |
| `quota_near_full` | warning | %80 doldu |
| `quota_full` | critical | yazım reddediliyor |
| `queue_stuck` | warning | pending > 1000 |
| `auth_fail_spike` | error | 5 dk'da >50 fail (brute force?) |
| `disk_full` | critical | local FS dolu |

Webhook: `POST FILEMANAGER_WEBHOOK_URL` JSON body. Retry 3x exp backoff.

In-app: `notifications` tablo (id, event, severity, title, body, meta_json, read_at, user_id NULL=broadcast). Bell icon `/api/notifications/unread` count, `/api/notifications` list, `POST /api/notifications/{id}/read`. Kullanıcı setting: `notifications.in_app_enabled` (default true). Webhook her zaman gönderilir (kapatılamaz — Burak G1).

### 4.4 Replica rules (path-based)

```sql
INSERT INTO replica_rules (path_pattern, mode, priority) VALUES
  ('fileman/sensitive/*', 'append_only', 10),
  ('fileman/temp/*', 'skip', 20),
  ('*', 'mirror', 999);  -- catch-all default
```

Rule engine: priority artan sırada eşleş, ilk match kazanır. Hiç kural yoksa = `mirror` (Burak E2).

### 4.5 Storage root guard

```go
// backend/internal/storage/registry.go (yeni helper)
func ValidatePrefix(driver string, cfg map[string]any) error {
    var p string
    switch driver {
    case "s3":     p, _ = cfg["prefix"].(string)
    case "local":  p, _ = cfg["path"].(string)
    case "ftp", "sftp", "webdav": p, _ = cfg["remote_path"].(string)
    }
    p = strings.Trim(p, "/")
    if p == "" {
        return errors.New("ROOT_PATH_FORBIDDEN: storage prefix/path cannot be empty or root '/'")
    }
    return nil
}
```

Storage create/update endpoint'inde validation. Burak J1.

### 4.6 Role-based admin

- OIDC: `id_token` claim'inde `roles: ["admin"]` veya `realm_access.roles: ["admin"]` (Keycloak).
- Local: `users.role` enum (`admin` | `user`).
- Basic env: `FILEMANAGER_ADMIN_USERS=burak,gokcil` (CSV).
- Endpoint `GET /api/me` → `{id, email, name, roles: ["admin", "user"]}`.
- Frontend FileExplorer toolbar'da admin role'i görünce ⚙ butonu, tıklayınca `/admin`'e yönlendir.

---

## 5. Storage drivers — V0.1.0

| Driver | Lib | Status |
|--------|-----|--------|
| local | stdlib `os` | ✅ |
| s3 | `aws-sdk-go-v2` | ✅ |
| sftp | `github.com/pkg/sftp` | ✅ |
| webdav | `github.com/studio-b12/gowebdav` | ✅ |
| **ftp** | `github.com/jlaffaye/ftp` | ❌ **eklenecek** |

Capabilities (sub-interface implementation):

| Driver | List | Read | Write | Move | Copy | Delete | Mkdir | Presign | Multipart | Watch |
|--------|------|------|-------|------|------|--------|-------|---------|-----------|-------|
| local | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | — | — | ✅ (fsnotify) |
| s3 | ✅ | ✅ | ✅ | ✅ (copy+del) | ✅ (server-side) | ✅ | — (placeholder) | ✅ | ✅ | — (poll) |
| sftp | ✅ | ✅ | ✅ | ✅ | ✅ (copy via stream) | ✅ | ✅ | — | — | — |
| webdav | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | — | — | — |
| ftp | ✅ | ✅ | ✅ | ✅ (RNFR/RNTO) | ✅ (download+upload) | ✅ | ✅ | — | — | — |

---

## 6. Frontend mimari

3 paket:
- `@brftech/filex-core` — Vue 3 SFC, peer dep `vue`
- `@brftech/filex` — Web Component (`<file-manager>`), `defineCustomElement(..., { shadowRoot: false })`
- `@brftech/filex-react` — `@lit/react` adapter

Tek SFC kaynak (`packages/core/src/FileExplorer.vue`); WC + React WC'yi sarar. Bug fix tek yerde.

Admin UI ayrı Vue app (`web/`), backend'e embed (`/admin/...`).

---

## 7. Endpoint kontratı (high-level)

```
# Browser-facing (FileExplorer)
GET    /api/files?q=index&path=…
POST   /api/files?q=newfolder
POST   /api/files?q=rename
POST   /api/files?q=delete
POST   /api/files/copy
POST   /api/files/move
GET    /api/files/preview?path=…
GET    /api/files/download?path=…
GET    /api/files/thumb?path=…&exp=&sig=
POST   /api/files/upload/init
POST   /api/files/upload/finalize
POST   /api/files/upload/abort
POST   /api/files/share
GET    /api/files/share?path=…
DELETE /api/files/share/{id}
GET    /api/files/capabilities
GET    /api/files/edit?path=…  (DocEditor config)
POST   /api/files/save-text

# Admin (role:admin gerek)
GET    /admin/api/storages
POST   /admin/api/storages
PATCH  /admin/api/storages/{id}
DELETE /admin/api/storages/{id}
GET    /admin/api/replica/rules
POST   /admin/api/replica/rules
PATCH  /admin/api/replica/rules/{id}
DELETE /admin/api/replica/rules/{id}
GET    /admin/api/replica/failures?status=unresolved&page=1
POST   /admin/api/replica/fix
GET    /admin/api/replica/report/latest
POST   /admin/api/replica/report/run-now
GET    /admin/api/replica/settings
PATCH  /admin/api/replica/settings  (cron, mode defaults)
GET    /admin/api/queue/stats
GET    /admin/api/queue/list?status=pending|running|failed
POST   /admin/api/queue/retry/{id}
DELETE /admin/api/queue/{id}
GET    /admin/api/notifications
POST   /admin/api/notifications/{id}/read
PATCH  /admin/api/notifications/settings  (in_app_enabled, webhook_url)

# Public
GET    /shared/{uuid}
POST   /shared/{uuid}/unlock
GET    /shared/{uuid}/download

# System
GET    /healthz
GET    /readyz
GET    /api/me
```

---

## 8. Yol haritası

| Faz | İş | Status |
|-----|------|--------|
| 0 | SPEC.md (bu dosya) | ⏳ |
| 1 | Monorepo iskelet + core | ✅ (mevcut) |
| 2 | WC + React adapter | ✅ (mevcut) |
| 3a | Backend iskelet + auth + storage | ✅ (mevcut) |
| 3b | API endpoint'leri | ✅ (mevcut, audit eksikleri Faz 11) |
| 3c | DB metadata cache | ✅ (mevcut, sync worker) |
| Δ-FTP | FTP driver | ❌ |
| Δ-Root | Root path guard | ❌ |
| Δ-Replica | ReplicatedStorage + rules + reconcile | ❌ |
| Δ-Queue | Driver-based persistent queue | ❌ |
| Δ-Notify | Webhook + in-app | ❌ |
| Δ-Role | Role-based admin button | ❌ |
| Δ-Prefix | Storage prefix UI | ⚠️ kısmen (S3 backend'de var, UI eksik) |
| Δ-Demo | demo-fm.brf.sh rename | ❌ |
| 4 | Admin UI panel (replica + notify sayfaları) | ⚠️ (mevcut, replica/notify sayfaları yok) |
| 5 | Docker | ✅ (mevcut) |
| 6 | Docs | ✅ (mevcut, replica/notify ekle) |
| 7 | CI/CD | ✅ (mevcut, audit) |
| 8 | v0.1.0 release + demo-fm.brf.sh | ❌ |
| 9 | brf-mono entegrasyon | ❌ |
| 10 | fishapp PWA entegrasyon | ❌ |
| 11 | Audit + tests + smoke | ❌ |

---

## 9. ENV referans (örnek)

```bash
# Server
FILEMANAGER_LISTEN=0.0.0.0:5212
FILEMANAGER_BASE_URL=https://demo-fm.brf.sh
FILEMANAGER_DATA_DIR=/data

# DB
FILEMANAGER_DB_DRIVER=sqlite                       # sqlite | mysql | postgres
FILEMANAGER_DB_DSN=/data/filex.db                  # sqlite path; for pg/mysql: connection string

# Queue
FILEMANAGER_QUEUE_DRIVER=sqlite                    # sqlite | redis | postgres
FILEMANAGER_QUEUE_REDIS_URL=redis://localhost:6379/0
FILEMANAGER_QUEUE_WORKERS=4

# Auth
FILEMANAGER_AUTH_DRIVERS=local,oidc                # CSV
FILEMANAGER_OIDC_ISSUER=https://auth.brf.sh/realms/brf
FILEMANAGER_OIDC_CLIENT_ID=filex
FILEMANAGER_OIDC_CLIENT_SECRET=...
FILEMANAGER_ADMIN_USERS=burak@brf.sh,gokcil@brf.sh # local mode admin list

# Storage (primary; .env bootstrap, Admin UI override eder)
FILEMANAGER_STORAGE_DRIVER=s3
FILEMANAGER_S3_ENDPOINT=https://nbg1.your-objectstorage.com
FILEMANAGER_S3_BUCKET=brf-files
FILEMANAGER_S3_REGION=eu-central
FILEMANAGER_S3_ACCESS_KEY=...
FILEMANAGER_S3_SECRET_KEY=...
FILEMANAGER_S3_PATH_STYLE=true
FILEMANAGER_S3_PREFIX=fileman                       # !!! ROOT yasak (Burak J1)

# Replica (opsiyonel)
FILEMANAGER_REPLICA_DRIVER=s3                      # boş = devre dışı
FILEMANAGER_REPLICA_S3_BUCKET=brf-files-replica
FILEMANAGER_REPLICA_S3_PREFIX=fileman               # !!! ROOT yasak
FILEMANAGER_REPLICA_MODE=async                     # async | sync (default async)
FILEMANAGER_REPLICA_REPORT_CRON=0 3 * * *          # boş = otomatik rapor üretme

# Notifications
FILEMANAGER_WEBHOOK_URL=https://portal.brf.sh/api/notify/v1/ingest
FILEMANAGER_WEBHOOK_TOKEN=bn_...                   # opsiyonel: Authorization: Bearer
FILEMANAGER_NOTIFICATIONS_IN_APP=true              # default

# Misc
FILEMANAGER_ONLYOFFICE_URL=https://docs.brf.sh
FILEMANAGER_ONLYOFFICE_JWT_SECRET=...
FILEMANAGER_DRAWIO_URL=https://embed.diagrams.net
```

---

## 10. Ölçütler

- ✅ Tek paket (`brftech/filex`), 3 framework
- ✅ Standalone Docker container — `docker run -p 5212:5212 brftech/filex` → demo görür
- ✅ Go backend: auth + storage + db + queue + notify driver-based
- ✅ Replica + reconciliation + status report + path rules
- ✅ Role-based admin UI
- ✅ Root klasör yasak guard
- ✅ Demo: `demo-fm.brf.sh` (brkip Caddy)
- ✅ V0.1.0 release; semver disiplini
