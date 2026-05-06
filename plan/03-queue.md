# Δ Persistent queue (driver-based)

> Mevcut `CountQueueDepth (sync_runs stand-in)` yerine gerçek queue.
> SPEC.md §4.2 referans. Karar: sqlite default, redis, postgres üç driver.

---

## Niye lazım
Burak: "kesinlikle queue persistance önemli bir olay basitçe sqlite üzerinde çözebiliriz. Yada belirtilirse queue driver redise yada postgresql ede queue işlerini atabilir."

Async ops:
- Copy/move/delete (uzun süren batch ops)
- Replica retry (replica fail → retry)
- Replica reconciliation (manuel "Fix")
- Replica status report (cron tetiklenince)
- Thumbnail generation
- Search index rebuild

Restart sonrası yarım kalan op'lar pickup edilmeli. Persistence şart.

---

## Tasarım

### Interface

**Dosya:** `backend/internal/queue/driver.go`

```go
package queue

import (
    "context"
    "errors"
    "time"
)

var ErrEmpty = errors.New("queue: empty")
var ErrNotFound = errors.New("queue: op not found")

type Driver interface {
    Init(ctx context.Context, cfg map[string]any) error
    Name() string
    Enqueue(ctx context.Context, op Op) (id string, err error)
    Dequeue(ctx context.Context, types []string) (Op, error) // ErrEmpty when none
    Ack(ctx context.Context, id string) error
    Fail(ctx context.Context, id string, errMsg string, retry bool) error
    List(ctx context.Context, status string, limit int, offset int) ([]Op, int64, error)
    Get(ctx context.Context, id string) (Op, error)
    Stats(ctx context.Context) (Stats, error)
    Cancel(ctx context.Context, id string) error
    Close() error
}

type Op struct {
    ID          string
    Type        string         // copy | move | delete | replica_retry | reconcile | thumb | replica_report
    Payload     map[string]any
    Status      string         // pending | running | done | failed | cancelled
    Priority    int            // higher first
    Attempts    int
    MaxAttempts int            // default 3
    LastError   string
    EnqueuedAt  time.Time
    StartedAt   *time.Time
    FinishedAt  *time.Time
    NotBefore   *time.Time     // delayed dispatch
}

type Stats struct {
    Pending int64
    Running int64
    Failed  int64
    Done24h int64
}
```

### Registry

**Dosya:** `backend/internal/queue/registry.go`

`backend/internal/storage/registry.go` ile aynı pattern (Factory + Register + Get + Names).

### Driver: SQLite (default)

**Dosya:** `backend/internal/queue/drivers/sqlite/sqlite.go`

```go
func init() {
    queue.Register("sqlite", func() queue.Driver { return &Driver{} })
}

func (d *Driver) Dequeue(ctx context.Context, types []string) (queue.Op, error) {
    // BEGIN IMMEDIATE → SELECT pending → UPDATE status='running'
    // SQLite RETURNING (3.35+) ile tek atomik sorgu:
    q := `
      UPDATE ops_queue
      SET status='running', started_at=CURRENT_TIMESTAMP, attempts=attempts+1
      WHERE id = (
        SELECT id FROM ops_queue
        WHERE status='pending'
          AND (not_before IS NULL OR not_before <= CURRENT_TIMESTAMP)
          AND type IN (?...)
        ORDER BY priority DESC, enqueued_at ASC
        LIMIT 1
      )
      RETURNING *
    `
    // Eğer types boşsa, type IN clause'unu kaldır
}
```

Polling interval: 1 saniye (worker pool busy-loop yerine bu).

### Driver: PostgreSQL

**Dosya:** `backend/internal/queue/drivers/postgres/postgres.go`

`SELECT FOR UPDATE SKIP LOCKED LIMIT 1` üretim grade pattern:

```sql
WITH next AS (
  SELECT id FROM ops_queue
  WHERE status='pending'
    AND (not_before IS NULL OR not_before <= NOW())
  ORDER BY priority DESC, enqueued_at ASC
  LIMIT 1
  FOR UPDATE SKIP LOCKED
)
UPDATE ops_queue SET status='running', started_at=NOW(), attempts=attempts+1
WHERE id = (SELECT id FROM next)
RETURNING *;
```

Bonus: `LISTEN/NOTIFY` ile worker uyandırma (1s polling yerine push). V0.2.

### Driver: Redis

**Dosya:** `backend/internal/queue/drivers/redis/redis.go`

`go-redis/v9` paketi (go.mod'a `github.com/redis/go-redis/v9` ekle).

Klasik work queue:
- `ops_pending` (LIST): yeni op'lar
- `ops_running` (LIST): processing
- `ops_failed` (LIST): retry edilemeyen
- `ops_data:{id}` (HASH): op detayları

Dequeue: `BRPOPLPUSH ops_pending ops_running 5s` — atomik.
Ack: `LREM ops_running 1 {id}` + delete data hash.
Fail: `LREM ops_running 1 {id}` + (retry ? `LPUSH ops_pending {id}` : `LPUSH ops_failed {id}`).

### Migration

**Dosya:** `backend/db/migrations/sqlite/00006_queue.sql` (postgres + mysql aynı sırada)

```sql
-- +goose Up
-- +goose StatementBegin

CREATE TABLE IF NOT EXISTS ops_queue (
    id TEXT PRIMARY KEY,
    type TEXT NOT NULL,
    payload TEXT NOT NULL DEFAULT '{}',
    status TEXT NOT NULL DEFAULT 'pending',
    priority INTEGER NOT NULL DEFAULT 0,
    attempts INTEGER NOT NULL DEFAULT 0,
    max_attempts INTEGER NOT NULL DEFAULT 3,
    last_error TEXT,
    enqueued_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    started_at DATETIME,
    finished_at DATETIME,
    not_before DATETIME
);
CREATE INDEX IF NOT EXISTS idx_ops_queue_status_priority
    ON ops_queue (status, priority DESC, enqueued_at);
CREATE INDEX IF NOT EXISTS idx_ops_queue_type_status
    ON ops_queue (type, status);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS ops_queue;
-- +goose StatementEnd
```

PostgreSQL versiyonunda `id UUID DEFAULT gen_random_uuid()` (TEXT yerine), MySQL'de `id VARCHAR(36)` + `enqueued_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP`.

### Worker Pool

**Dosya:** `backend/internal/queue/worker.go`

```go
type Handler func(ctx context.Context, op Op) error

type Pool struct {
    drv      Driver
    handlers map[string]Handler
    workers  int
    stop     chan struct{}
    wg       sync.WaitGroup
}

func NewPool(drv Driver, workers int) *Pool {
    return &Pool{drv: drv, handlers: map[string]Handler{}, workers: workers, stop: make(chan struct{})}
}

func (p *Pool) Register(opType string, h Handler) {
    p.handlers[opType] = h
}

func (p *Pool) Start(ctx context.Context) {
    types := make([]string, 0, len(p.handlers))
    for t := range p.handlers { types = append(types, t) }
    for i := 0; i < p.workers; i++ {
        p.wg.Add(1)
        go p.loop(ctx, types)
    }
}

func (p *Pool) loop(ctx context.Context, types []string) {
    defer p.wg.Done()
    backoff := time.Second
    for {
        select {
        case <-p.stop: return
        case <-ctx.Done(): return
        default:
        }
        op, err := p.drv.Dequeue(ctx, types)
        if errors.Is(err, ErrEmpty) {
            time.Sleep(backoff)
            continue
        }
        if err != nil { /* log */ time.Sleep(backoff); continue }
        h, ok := p.handlers[op.Type]
        if !ok {
            p.drv.Fail(ctx, op.ID, "no handler for type "+op.Type, false)
            continue
        }
        if err := h(ctx, op); err != nil {
            retry := op.Attempts < op.MaxAttempts
            p.drv.Fail(ctx, op.ID, err.Error(), retry)
        } else {
            p.drv.Ack(ctx, op.ID)
        }
    }
}

func (p *Pool) Stop() {
    close(p.stop)
    p.wg.Wait()
}
```

### Bootstrap

`backend/cmd/filex/main.go` veya `backend/internal/server/server.go`:

```go
qDriver := os.Getenv("FILEMANAGER_QUEUE_DRIVER")
if qDriver == "" { qDriver = "sqlite" }
q, err := queue.Get(qDriver)
if err != nil { log.Fatal(err) }
qCfg := buildQueueConfig() // env'den
if err := q.Init(ctx, qCfg); err != nil { log.Fatal(err) }

workers, _ := strconv.Atoi(os.Getenv("FILEMANAGER_QUEUE_WORKERS"))
if workers == 0 { workers = 4 }

pool := queue.NewPool(q, workers)
// Handler'ları register et:
pool.Register("copy", opsHandler.Copy)
pool.Register("move", opsHandler.Move)
pool.Register("delete", opsHandler.Delete)
pool.Register("replica_retry", replicaHandler.Retry)
pool.Register("reconcile", replicaHandler.Reconcile)
pool.Register("replica_report", replicaHandler.GenerateReport)
pool.Register("thumb", thumbHandler.Generate)
pool.Start(ctx)
defer pool.Stop()
```

### ENV referans

```
FILEMANAGER_QUEUE_DRIVER=sqlite              # sqlite | redis | postgres
FILEMANAGER_QUEUE_REDIS_URL=redis://localhost:6379/0
FILEMANAGER_QUEUE_PG_DSN=postgres://...
FILEMANAGER_QUEUE_WORKERS=4
```

SQLite default → ana DB (`FILEMANAGER_DB_DSN`) ile aynı dosyayı kullan (migration'lar zaten oraya gidiyor). Postgres / MySQL aynı şekilde ana DB'yi kullanabilir, ya da ayrı bir DSN ile farklı DB'ye yönlendirilebilir (Burak iki yöne de gitmiş olabilir).

### Admin API endpoint'leri

```
GET    /admin/api/queue/stats                → Stats JSON
GET    /admin/api/queue/list?status=&limit=  → []Op (paginated)
POST   /admin/api/queue/retry/{id}            → reset failed → pending
DELETE /admin/api/queue/{id}                  → cancel
```

Mevcut admin handler dosyasına ekle (büyük olasılıkla `backend/internal/api/handlers/`).

### Test

`backend/internal/queue/queue_test.go`:
- SQLite driver round-trip (Enqueue → Dequeue → Ack)
- Fail + retry (attempts++, max'a ulaşınca status=failed)
- Worker pool: 5 op enqueue, hepsi pickup edilip Ack'lenmeli
- Cancel: pending → cancelled
- Restart: işlemde "running" kalmış op'lar (graceful shutdown sırasında) restart sonrası nasıl? → V0.1: 5dk sonra pending'e geri al (orphan recovery cron). V0.2: process heartbeat tablosu.

### Tahmini efor
- Interface + registry: 1 saat
- SQLite driver: 3-4 saat
- Postgres driver: 2-3 saat
- Redis driver: 2-3 saat (go-redis öğrenme dahil değil)
- Worker pool: 2 saat
- Bootstrap entegrasyonu: 1-2 saat
- Migration (3 dialekt): 1 saat
- Admin endpoint: 1-2 saat
- Test: 3-4 saat

**Toplam:** ~20 saat (2.5 gün full-time)

### WIP durumu
Subagent başlatıldı ama Coder workspace'i durduğu için tamamlanma durumu belirsiz. Lokal'de:
```bash
ls backend/internal/queue/
ls backend/db/migrations/sqlite/00006_*
```

Yoksa sıfırdan yaz.
