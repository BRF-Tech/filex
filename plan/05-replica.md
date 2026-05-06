# Δ Replica storage layer + rules + reconciliation

> SPEC.md §4.1, §4.4 referans.
> **Bağımlılık:** Queue (03-queue.md) + Notify (04-notify.md) önce gelmeli.

---

## Niye lazım
Burak'ın isteği (özet):
- A1: Replica yazımı **async** (fire-and-forget). Kullanıcı tanımlı cron ile status raporu — kaç dosya replicada problem yaşamış. UI'da "Fix" butonu.
- A2: **Read fallback** — primary fail ederse replica'dan oku, kullanıcıya bildirim (webhook + opsiyonel in-app).
- A3: **Mirror** delete (primary'de silinen replica'da da silinir). Kullanıcı path-based **kural** ile bunu kontrol edebilir.
- A4: Reconciliation = "Fix" butonu (cron status report + manuel fix bütünleşik).
- E2: Default kural yokken = mirror.
- F2: Sadece son rapor DB'de. Kullanıcı isterse webhook ingest ile kendi sistemine post eder.
- F3: UI özet (3k file failed), DB tekil detay (paginated), webhook payload tam detay.
- I2: Tek primary + tek replica. Multi-replica V0.3+.
- J1: Primary ve replica ikisi de alt klasör (root klasör yasak).

---

## Tasarım

### Wrapper

**Dosya:** `backend/internal/storage/replicated.go`

```go
package storage

import (
    "context"
    "io"
    "log/slog"
)

type ReplicatedStorage struct {
    primary  Driver
    replica  Driver           // nil iff disabled
    rules    RuleEngine
    failures FailureRecorder
    notifier Notifier
    queue    Enqueuer        // for async retry/reconcile
}

type RuleEngine interface {
    Match(path string) ReplicaMode  // mirror | append_only | skip
}

type ReplicaMode string
const (
    ModeMirror     ReplicaMode = "mirror"
    ModeAppendOnly ReplicaMode = "append_only"
    ModeSkip       ReplicaMode = "skip"
)

type FailureRecorder interface {
    Record(ctx context.Context, path, op, errCode, errMsg string) error
    Resolve(ctx context.Context, path, op string) error
    ListUnresolved(ctx context.Context, limit, offset int) ([]Failure, int64, error)
    Count(ctx context.Context) (int64, error)
}

type Notifier interface {
    Notify(ctx context.Context, e notify.Event)
}

type Enqueuer interface {
    Enqueue(ctx context.Context, op queue.Op) (string, error)
}

// Driver methodları wrap edilir:

func (r *ReplicatedStorage) Read(ctx context.Context, p string) (io.ReadCloser, error) {
    rc, err := r.primary.Read(ctx, p)
    if err == nil { return rc, nil }
    if r.replica == nil { return nil, err }
    rc2, err2 := r.replica.Read(ctx, p)
    if err2 != nil { return nil, err }
    // Read fallback başarılı:
    r.notifier.Notify(ctx, notify.Event{
        Event: notify.EventPrimaryReadFail,
        Severity: notify.SeverityError,
        Title: "Primary read failed, served from replica",
        Body:  fmt.Sprintf("Path %s read from replica after primary failure: %v", p, err),
        Meta:  map[string]any{"path": p, "primary_error": err.Error()},
        TS:    time.Now(),
    })
    return rc2, nil
}

func (r *ReplicatedStorage) Write(ctx context.Context, p string, body io.Reader, size int64) error {
    // Primary blocking
    if err := r.primary.(Writer).Write(ctx, p, body, size); err != nil {
        return err
    }
    if r.replica == nil { return nil }
    mode := r.rules.Match(p)
    if mode == ModeSkip { return nil }
    // Async fire-and-forget — Burak A1
    // Body okunmuş, replica için tekrar oku gerekiyor → hatırla:
    // ÖNEMLİ: Streaming write'larda body sadece bir kez okunur.
    // Çözüm 1: Primary'ye write ederken aynı zamanda io.TeeReader ile buffer'a kopyala.
    // Çözüm 2: Primary write sonrası replica için path'i tekrar primary'den oku → replica'ya yaz.
    // Çözüm 2 daha güvenli (truncated write riski yok). Async olduğu için latency önemsiz.
    go r.replicateWriteAsync(p)
    return nil
}

func (r *ReplicatedStorage) replicateWriteAsync(path string) {
    ctx := context.Background()  // independent — request bağlamı kapanabilir
    rc, err := r.primary.Read(ctx, path)
    if err != nil {
        r.failures.Record(ctx, path, "write", "PRIMARY_READBACK_FAIL", err.Error())
        r.notifyFailure(ctx, path, "write", err)
        return
    }
    defer rc.Close()
    stat, _ := r.primary.Stat(ctx, path)
    if err := r.replica.(Writer).Write(ctx, path, rc, stat.Size); err != nil {
        r.failures.Record(ctx, path, "write", "REPLICA_WRITE_FAIL", err.Error())
        r.notifyFailure(ctx, path, "write", err)
        return
    }
    r.failures.Resolve(ctx, path, "write")  // önceki fail varsa resolve et
}

func (r *ReplicatedStorage) Delete(ctx context.Context, p string) error {
    if err := r.primary.(Deleter).Delete(ctx, p); err != nil {
        return err
    }
    if r.replica == nil { return nil }
    mode := r.rules.Match(p)
    if mode == ModeAppendOnly || mode == ModeSkip { return nil }
    // mirror (Burak A3)
    go r.replicateDeleteAsync(p)
    return nil
}

func (r *ReplicatedStorage) replicateDeleteAsync(path string) {
    ctx := context.Background()
    if err := r.replica.(Deleter).Delete(ctx, path); err != nil {
        r.failures.Record(ctx, path, "delete", "REPLICA_DELETE_FAIL", err.Error())
        r.notifyFailure(ctx, path, "delete", err)
        return
    }
    r.failures.Resolve(ctx, path, "delete")
}

func (r *ReplicatedStorage) notifyFailure(ctx context.Context, path, op string, err error) {
    r.notifier.Notify(ctx, notify.Event{
        Event:    notify.EventReplicaFail,
        Severity: notify.SeverityWarning,
        Title:    "Replica " + op + " failed",
        Body:     fmt.Sprintf("Replica %s failed for %s: %v", op, path, err),
        Meta:     map[string]any{"path": path, "op": op, "error": err.Error()},
        TS:       time.Now(),
    })
}

// Move + Copy + Mkdir + Stat + List → primary'den, async replica'ya
// (yukarıdaki pattern'larla aynı)
```

### Capabilities

`ReplicatedStorage.Capabilities()` primary'nin capabilities'ini döndürür (replica desteklemeyen feature'ları unioned'lamayı düşünme — primary kontrol).

### Storage hiyerarşisi

Mevcut storage'lar `storages` tablosunda. Tek bir "primary" + "replica" yerine, tabloda yeni kolon:

```sql
ALTER TABLE storages ADD COLUMN role TEXT NOT NULL DEFAULT 'primary';  -- primary | replica
ALTER TABLE storages ADD COLUMN replica_of_id INTEGER REFERENCES storages(id);
ALTER TABLE storages ADD COLUMN replica_mode TEXT DEFAULT 'async';     -- async | sync (V0.2)
```

V0.1: tek primary (`role='primary'`), opsiyonel tek replica (`role='replica', replica_of_id=primaryID`). Çoklu primary V0.3+.

### Rule engine

**Dosya:** `backend/internal/storage/rules.go`

```go
type rule struct {
    ID       int64
    Pattern  string  // glob (path/filepath.Match) veya custom
    Mode     ReplicaMode
    Priority int
    Enabled  bool
}

type ruleEngine struct {
    store db.Store
    cache atomic.Value // []rule cached
    mu    sync.Mutex
}

func (e *ruleEngine) Match(p string) ReplicaMode {
    rules := e.cached()
    for _, r := range rules {  // priority asc sıralı
        if !r.Enabled { continue }
        ok, _ := filepath.Match(r.Pattern, p)
        if ok { return r.Mode }
        // Glob double-star desteği için doublestar lib eklenebilir
    }
    return ModeMirror  // default (Burak E2)
}

func (e *ruleEngine) Reload(ctx context.Context) error {
    rules, err := e.store.ListReplicaRules(ctx)
    if err != nil { return err }
    sort.Slice(rules, func(i, j int) bool { return rules[i].Priority < rules[j].Priority })
    e.cache.Store(rules)
    return nil
}
```

Rule INSERT/UPDATE/DELETE sonrası `Reload()` çağrılmalı. Cache invalidation 30 sn periyodik olarak da yapılabilir (eventual consistency).

### DB tabloları

**Dosya:** `backend/db/migrations/sqlite/00008_replica.sql` (queue=00006, notify=00007 ise)

```sql
-- +goose Up
-- +goose StatementBegin

CREATE TABLE IF NOT EXISTS replica_rules (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    path_pattern TEXT NOT NULL,
    mode TEXT NOT NULL,            -- mirror | append_only | skip
    priority INTEGER NOT NULL DEFAULT 100,
    enabled INTEGER NOT NULL DEFAULT 1,
    description TEXT DEFAULT '',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_replica_rules_priority
    ON replica_rules (priority, enabled);

CREATE TABLE IF NOT EXISTS replica_failures (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    path TEXT NOT NULL,
    op TEXT NOT NULL,              -- write | delete | move | copy
    error_code TEXT,
    error_msg TEXT,
    attempts INTEGER NOT NULL DEFAULT 1,
    last_attempt_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    resolved_at DATETIME,
    UNIQUE(path, op)               -- aynı path+op tek kayıt, attempts++
);
CREATE INDEX IF NOT EXISTS idx_replica_failures_unresolved
    ON replica_failures (resolved_at, last_attempt_at);

CREATE TABLE IF NOT EXISTS replica_status_reports (
    id INTEGER PRIMARY KEY DEFAULT 1 CHECK (id = 1),  -- singleton
    generated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    total_files INTEGER NOT NULL DEFAULT 0,
    failed_count INTEGER NOT NULL DEFAULT 0,
    repaired_count INTEGER NOT NULL DEFAULT 0,
    summary_json TEXT NOT NULL DEFAULT '{}'
);

CREATE TABLE IF NOT EXISTS replica_settings (
    id INTEGER PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    report_cron TEXT DEFAULT '',                -- '0 3 * * *' veya boş
    report_enabled INTEGER NOT NULL DEFAULT 0,
    default_mode TEXT NOT NULL DEFAULT 'mirror'  -- catch-all (Burak E2)
);

storages tablosu ALTER:
ALTER TABLE storages ADD COLUMN role TEXT NOT NULL DEFAULT 'primary';
ALTER TABLE storages ADD COLUMN replica_of_id INTEGER REFERENCES storages(id);
ALTER TABLE storages ADD COLUMN replica_mode TEXT DEFAULT 'async';

-- +goose StatementEnd
-- +goose Down
DROP TABLE IF EXISTS replica_rules;
DROP TABLE IF EXISTS replica_failures;
DROP TABLE IF EXISTS replica_status_reports;
DROP TABLE IF EXISTS replica_settings;
-- ALTER ROLLBACK SQLite'da TEDBİRLİ
```

PostgreSQL: `meta_json JSONB`, BIGSERIAL, TIMESTAMPTZ.
MySQL: `meta_json JSON`, BIGINT AUTO_INCREMENT.

### Reconciliation handler

**Dosya:** `backend/internal/storage/replica_reconcile.go`

```go
// "Fix" butonu — tüm unresolved failures'ı queue'ya at, retry et
func (r *ReplicatedStorage) ReconcileAll(ctx context.Context) (queued int, err error) {
    failures, _, err := r.failures.ListUnresolved(ctx, 10000, 0)
    if err != nil { return 0, err }
    for _, f := range failures {
        op := queue.Op{
            Type: "replica_retry",
            Payload: map[string]any{
                "path": f.Path,
                "op":   f.Op,
            },
            MaxAttempts: 3,
            Priority:    50,
        }
        r.queue.Enqueue(ctx, op)
        queued++
    }
    return queued, nil
}

// Queue handler (queue.Pool.Register("replica_retry", ...))
func (r *ReplicatedStorage) HandleReplicaRetry(ctx context.Context, op queue.Op) error {
    path, _ := op.Payload["path"].(string)
    opType, _ := op.Payload["op"].(string)
    switch opType {
    case "write":
        // primary'den oku, replica'ya yaz
        rc, err := r.primary.Read(ctx, path)
        if err != nil {
            r.failures.Record(ctx, path, opType, "PRIMARY_READBACK_FAIL", err.Error())
            return err
        }
        defer rc.Close()
        stat, _ := r.primary.Stat(ctx, path)
        if err := r.replica.(Writer).Write(ctx, path, rc, stat.Size); err != nil {
            r.failures.Record(ctx, path, opType, "REPLICA_WRITE_FAIL", err.Error())
            return err
        }
    case "delete":
        if err := r.replica.(Deleter).Delete(ctx, path); err != nil {
            r.failures.Record(ctx, path, opType, "REPLICA_DELETE_FAIL", err.Error())
            return err
        }
    }
    r.failures.Resolve(ctx, path, opType)
    return nil
}
```

Reconcile sonrası özet bildirim:

```go
func (r *ReplicatedStorage) emitReconcileSummary(ctx context.Context, before, after int64) {
    r.notifier.Notify(ctx, notify.Event{
        Event:    notify.EventReplicaReconcileDone,
        Severity: notify.SeverityInfo,
        Title:    "Replica reconciliation done",
        Body:     fmt.Sprintf("Reconciliation complete: %d failures resolved out of %d", before-after, before),
        Meta:     map[string]any{"before": before, "after": after, "resolved": before - after},
        TS:       time.Now(),
    })
}
```

### Cron status report

**Dosya:** `backend/internal/storage/replica_report.go`

Kullanıcı cron ayarlar (`replica_settings.report_cron`). Boot zamanında `github.com/robfig/cron/v3` ile schedule kurulur:

```go
import "github.com/robfig/cron/v3"

func (r *ReplicatedStorage) ScheduleReport(ctx context.Context) error {
    s, err := r.store.GetReplicaSettings(ctx)
    if err != nil { return err }
    if !s.ReportEnabled || s.ReportCron == "" { return nil }
    c := cron.New()
    _, err = c.AddFunc(s.ReportCron, func() {
        r.GenerateReport(ctx)
    })
    if err != nil { return err }
    c.Start()
    return nil
}

func (r *ReplicatedStorage) GenerateReport(ctx context.Context) error {
    // 1. Tüm primary objeleri count
    total, _ := r.countPrimaryObjects(ctx)
    // 2. Unresolved failures count
    failed, _ := r.failures.Count(ctx)
    // 3. Last 24h resolved count
    repaired, _ := r.failures.RecentlyResolved(ctx, 24*time.Hour)
    // 4. Detayları al (ilk 100 path örnek olarak; webhook payload'ında tam liste)
    sample, _, _ := r.failures.ListUnresolved(ctx, 100, 0)

    summary := map[string]any{
        "total_files":    total,
        "failed_count":   failed,
        "repaired_count": repaired,
        "sample_failed_paths": sample,  // örnek
    }
    // DB'de singleton: eski rapor SİL, yeni rapor INSERT
    r.store.UpsertReplicaStatusReport(ctx, total, failed, repaired, summary)

    // Webhook ingest: tam detay
    fullList, _, _ := r.failures.ListUnresolved(ctx, 100000, 0)
    r.notifier.Notify(ctx, notify.Event{
        Event:    notify.EventReplicaStatusReport,
        Severity: notify.SeverityInfo,
        Title:    fmt.Sprintf("Replica status: %d failures of %d files", failed, total),
        Body:     "Cron-triggered replica status report",
        Meta: map[string]any{
            "total_files":    total,
            "failed_count":   failed,
            "repaired_count": repaired,
            "failed_paths":   fullList, // tüm path'ler webhook payload'ında
        },
        TS: time.Now(),
    })
    return nil
}
```

`replica_settings.report_cron` UI'dan değiştirilince cron schedule yeniden yüklenmeli (`Reload`).

### API endpoints (admin)

```
GET    /admin/api/replica/rules                    → rule list
POST   /admin/api/replica/rules                    → create rule
PATCH  /admin/api/replica/rules/{id}                → update
DELETE /admin/api/replica/rules/{id}                → delete

GET    /admin/api/replica/failures?limit=50&offset=0 → []Failure (paginated)
GET    /admin/api/replica/failures/count             → {count: N}

POST   /admin/api/replica/fix                      → reconcile all (queue'ya at)
POST   /admin/api/replica/fix/{path}                → tek path'i fix et

GET    /admin/api/replica/report                    → son rapor
POST   /admin/api/replica/report/run-now            → şimdi çalıştır

GET    /admin/api/replica/settings                  → settings
PATCH  /admin/api/replica/settings                   → update (cron, default_mode)
```

### Frontend Admin UI sayfaları

`web/src/views/Replica.vue` (yeni):

Sekmeler:
- **Rules** — path glob + mode + priority + enabled toggle. CRUD form.
- **Failures** — paginated tablo (path, op, error_code, attempts, last_attempt). Toplu "Fix All" butonu + tekil "Fix" butonu.
- **Status Report** — son rapor özeti (total/failed/repaired) + "Run Now" butonu + cron config.
- **Settings** — cron preset dropdown (saatlik/günlük/haftalık) + advanced raw cron input + default_mode dropdown.

Cron preset → raw mapping:
- "Hourly" → `0 * * * *`
- "Every 6 hours" → `0 */6 * * *`
- "Daily 3am" → `0 3 * * *`
- "Weekly Sunday 3am" → `0 3 * * 0`
- "Custom" → raw input

### Test

`backend/internal/storage/replicated_test.go`:
- Write primary OK + replica OK → no failures
- Write primary OK + replica FAIL → DB'de failure record + notify
- Read primary FAIL + replica OK → fallback işliyor + notify
- Delete mirror mode → primary delete + replica delete
- Delete append-only mode → primary delete only
- Delete skip mode → primary delete only
- Rule precedence: priority asc, ilk match
- Reconcile → queue'ya at, retry handler resolve eder

### Bağımlılık paketleri

```
go get github.com/robfig/cron/v3
go get github.com/bmatcuk/doublestar/v4    # ** glob desteği için (opsiyonel)
```

### Tahmini efor
- Wrapper + interface'ler: 4 saat
- Rule engine + cache: 2 saat
- Failure recorder + DB store methods: 3 saat
- Reconciliation + queue handler: 2 saat
- Cron status report + scheduler: 2-3 saat
- Migration (3 dialekt): 2 saat
- API endpoints (admin): 3 saat
- Frontend Replica.vue (4 sekme): 4-5 saat
- Test: 4 saat

**Toplam:** ~25 saat (3 gün full-time)

### WIP durumu
Subagent başlatılmadı (Coder durdu). Lokal'de sıfırdan yaz.
