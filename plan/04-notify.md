# Δ Notifications (webhook ingest + in-app bell)

> SPEC.md §4.3 referans.
> Karar: Tek generic JSON webhook + in-app bell + read/unread.

---

## Niye lazım
Burak'ın istekleri:
- Replica fail durumunda kullanıcıya bildirim
- Webhook ingest mantığında (kullanıcı kendi endpoint'ine post eder)
- In-app: bell icon + history + read/unread, kullanıcı kapatabilir
- Webhook her zaman gider (Slack/Discord template değil — generic JSON, kullanıcı kendi sistemine map eder)

---

## Tasarım

### Event listesi

| Event | Severity | Trigger |
|-------|----------|---------|
| `replica_fail` | warning | Tek replica yazımı başarısız |
| `replica_fail_spike` | error | 5 dk içinde >100 fail |
| `replica_reconcile_done` | info | Fix tamamlandı |
| `replica_status_report` | info | Cron tetiklediği rapor |
| `primary_read_fail` | error | Primary read fail, replica fallback |
| `quota_near_full` | warning | %80 doldu |
| `quota_full` | critical | Yazım reddediliyor |
| `queue_stuck` | warning | pending > 1000 |
| `auth_fail_spike` | error | 5 dk'da >50 fail |
| `disk_full` | critical | Local FS dolu |

### Event struct

**Dosya:** `backend/internal/notify/event.go`

```go
package notify

import "time"

type Severity string
const (
    SeverityInfo     Severity = "info"
    SeverityWarning  Severity = "warning"
    SeverityError    Severity = "error"
    SeverityCritical Severity = "critical"
)

type EventType string
const (
    EventReplicaFail          EventType = "replica_fail"
    EventReplicaFailSpike     EventType = "replica_fail_spike"
    EventReplicaReconcileDone EventType = "replica_reconcile_done"
    EventReplicaStatusReport  EventType = "replica_status_report"
    EventPrimaryReadFail      EventType = "primary_read_fail"
    EventQuotaNearFull        EventType = "quota_near_full"
    EventQuotaFull            EventType = "quota_full"
    EventQueueStuck           EventType = "queue_stuck"
    EventAuthFailSpike        EventType = "auth_fail_spike"
    EventDiskFull             EventType = "disk_full"
)

type Event struct {
    Event    EventType      `json:"event"`
    Severity Severity       `json:"severity"`
    Title    string         `json:"title"`
    Body     string         `json:"body"`
    Meta     map[string]any `json:"meta,omitempty"`
    TS       time.Time      `json:"ts"`
}
```

### Service interface

**Dosya:** `backend/internal/notify/service.go`

```go
type Service interface {
    Send(ctx context.Context, e Event) error
    List(ctx context.Context, userID *int64, onlyUnread bool, limit, offset int) ([]Stored, int64, error)
    MarkRead(ctx context.Context, id int64, userID *int64) error
    MarkAllRead(ctx context.Context, userID *int64) error
    UnreadCount(ctx context.Context, userID *int64) (int64, error)
}

type Stored struct {
    Event
    ID            int64
    UserID        *int64
    ReadAt        *time.Time
    WebhookStatus string  // pending | sent | failed | skipped
    WebhookError  string
    CreatedAt     time.Time
}
```

Implementation:
```go
type service struct {
    store         db.Store
    webhookURL    string  // .env veya DB-stored override
    webhookToken  string
    httpClient    *http.Client
}

func (s *service) Send(ctx context.Context, e Event) error {
    // 1. DB INSERT (in-app)
    id, err := s.store.InsertNotification(ctx, /* full record */)
    if err != nil { return err }
    // 2. Webhook (async)
    go s.deliverWebhook(ctx, id, e)
    return nil
}

func (s *service) deliverWebhook(ctx context.Context, id int64, e Event) {
    if s.webhookURL == "" {
        s.store.UpdateWebhookStatus(ctx, id, "skipped", "no URL configured")
        return
    }
    body, _ := json.Marshal(e)
    backoff := []time.Duration{1*time.Second, 3*time.Second, 9*time.Second}
    for attempt := 0; attempt < 3; attempt++ {
        req, _ := http.NewRequestWithContext(ctx, "POST", s.webhookURL, bytes.NewReader(body))
        req.Header.Set("Content-Type", "application/json")
        if s.webhookToken != "" {
            req.Header.Set("Authorization", "Bearer "+s.webhookToken)
        }
        resp, err := s.httpClient.Do(req)
        if err == nil && resp.StatusCode < 400 {
            resp.Body.Close()
            s.store.UpdateWebhookStatus(ctx, id, "sent", "")
            return
        }
        if resp != nil { resp.Body.Close() }
        if attempt < 2 { time.Sleep(backoff[attempt]) }
    }
    s.store.UpdateWebhookStatus(ctx, id, "failed", "exhausted retries")
}
```

### Webhook payload (generic JSON)

```json
{
  "event": "replica_fail",
  "severity": "warning",
  "title": "Replica write failed",
  "body": "Replica write failed for fileman/foo.pdf: connection timeout",
  "meta": {
    "path": "fileman/foo.pdf",
    "op": "write",
    "error_code": "CONN_TIMEOUT",
    "replica_storage_id": 2,
    "primary_storage_id": 1,
    "attempt": 1
  },
  "ts": "2026-05-06T13:42:11Z"
}
```

Header:
- `Content-Type: application/json`
- `Authorization: Bearer <FILEMANAGER_WEBHOOK_TOKEN>` (boş ise atla)

Retry: 3x exp backoff (1s, 3s, 9s). Fail → DB status='failed'.

### DB tabloları

**Dosya:** `backend/db/migrations/sqlite/00007_notifications.sql` (postgres + mysql aynı sıra; queue 00006 alıyorsa)

```sql
-- +goose Up
-- +goose StatementBegin

CREATE TABLE IF NOT EXISTS notifications (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    event TEXT NOT NULL,
    severity TEXT NOT NULL,
    title TEXT NOT NULL,
    body TEXT NOT NULL,
    meta_json TEXT NOT NULL DEFAULT '{}',
    user_id INTEGER,                              -- NULL = broadcast (admin)
    read_at DATETIME,
    webhook_status TEXT DEFAULT 'pending',        -- pending|sent|failed|skipped
    webhook_error TEXT,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_notifications_user_read
    ON notifications (user_id, read_at, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_notifications_event
    ON notifications (event, created_at DESC);

CREATE TABLE IF NOT EXISTS notification_settings (
    user_id INTEGER PRIMARY KEY,
    in_app_enabled INTEGER NOT NULL DEFAULT 1,
    muted_events TEXT NOT NULL DEFAULT '[]'       -- JSON array
);

-- +goose StatementEnd
-- +goose Down
DROP TABLE IF EXISTS notifications;
DROP TABLE IF EXISTS notification_settings;
```

PostgreSQL: `id BIGSERIAL`, `meta_json JSONB`, `created_at TIMESTAMPTZ`.

MySQL: `id BIGINT AUTO_INCREMENT`, `meta_json JSON`, `created_at TIMESTAMP`.

### Store methods

`backend/internal/db/store.go` interface'ine + 3 driver impl'ine ekle:

```go
type Store interface {
    // ... mevcut
    InsertNotification(ctx context.Context, n NotificationInput) (int64, error)
    ListNotifications(ctx context.Context, userID *int64, onlyUnread bool, limit, offset int) ([]Notification, int64, error)
    GetNotification(ctx context.Context, id int64) (Notification, error)
    MarkNotificationRead(ctx context.Context, id int64, userID *int64) error
    MarkAllNotificationsRead(ctx context.Context, userID *int64) error
    UnreadNotificationCount(ctx context.Context, userID *int64) (int64, error)
    UpdateWebhookStatus(ctx context.Context, id int64, status, errMsg string) error
    GetNotificationSettings(ctx context.Context, userID int64) (NotifSettings, error)
    UpdateNotificationSettings(ctx context.Context, userID int64, s NotifSettings) error
}
```

### API endpoint'leri

User-scope (auth gerekli):
```
GET    /api/notifications?unread=true&limit=50&offset=0  → list (user'ın + broadcast)
GET    /api/notifications/unread-count                    → {count: N}
POST   /api/notifications/{id}/read                        → 204
POST   /api/notifications/read-all                         → 204
GET    /api/notifications/settings                         → settings
PATCH  /api/notifications/settings                         → update (in_app_enabled, muted_events)
```

Admin-scope:
```
POST   /admin/api/notifications/test                       → manuel event gönder (smoke)
GET    /admin/api/notifications/webhook-config              → {url, token_set: bool}
PATCH  /admin/api/notifications/webhook-config              → update url+token
GET    /admin/api/notifications?event=&severity=&limit=     → tüm bildirimler (broadcast + user-scoped)
```

### ENV referans

```
FILEMANAGER_WEBHOOK_URL=https://portal.brf.sh/api/notify/v1/ingest
FILEMANAGER_WEBHOOK_TOKEN=bn_...
FILEMANAGER_NOTIFICATIONS_IN_APP=true
```

`.env` bootstrap, Admin UI override eder (DB-stored). Boş değilse DB öncelik.

### Frontend: in-app bell

**Admin web app** (`web/src/components/NotificationBell.vue` — yeni dosya):

```vue
<script setup>
import { ref, onMounted, onUnmounted } from 'vue';
const unread = ref(0);
const open = ref(false);
const items = ref([]);

async function poll() {
  const r = await fetch('/api/notifications/unread-count', { credentials: 'include' });
  if (r.ok) unread.value = (await r.json()).count;
}
async function load() {
  const r = await fetch('/api/notifications?limit=20', { credentials: 'include' });
  if (r.ok) items.value = (await r.json()).items;
}
async function markRead(id) {
  await fetch(`/api/notifications/${id}/read`, { method: 'POST', credentials: 'include' });
  poll(); load();
}

let interval;
onMounted(() => {
  poll(); load();
  interval = setInterval(() => { poll(); if (open.value) load(); }, 15000);
});
onUnmounted(() => clearInterval(interval));
</script>

<template>
  <div class="bell-wrap">
    <button @click="open = !open; if (open) load()" class="bell-btn">
      🔔 <span v-if="unread > 0" class="badge">{{ unread }}</span>
    </button>
    <div v-if="open" class="bell-dropdown">
      <div v-for="n in items" :key="n.id" :class="['notif', n.read_at ? 'read' : 'unread']"
           @click="markRead(n.id)">
        <span :class="['sev-' + n.severity]">{{ severityIcon(n.severity) }}</span>
        <strong>{{ n.title }}</strong>
        <p>{{ n.body }}</p>
        <small>{{ formatTime(n.created_at) }}</small>
      </div>
    </div>
  </div>
</template>
```

App layout'a (üst nav) entegre et: `web/src/App.vue`.

### FileExplorer paket entegrasyonu (V0.2)

V0.1'de in-app bell sadece admin web app'te. Embed FileExplorer paketinde V0.2'de eklenebilir (config'e `notificationsEnabled: true` flag).

### Test

`backend/internal/notify/notify_test.go`:
- Send → DB INSERT (httptest.Server'a webhook gönder, body doğrula)
- Webhook 3x retry (mock server 503 dön, 4. çağrı yok)
- Webhook URL boş → status='skipped', DB INSERT yine yapılır
- ListNotifications: user_id filter (broadcast + own), unread filter
- MarkAllRead: bir kullanıcının tüm okunmamışlarını okundu yap

### Tahmini efor
- Interface + service: 2 saat
- Webhook delivery + retry: 1 saat
- DB migration + store methods (3 dialekt): 2 saat
- API endpoints: 2 saat
- Frontend bell component: 1-2 saat
- Test: 3 saat

**Toplam:** ~12 saat (1.5 gün full-time)

### WIP durumu
Subagent başlatıldı, durumu belirsiz. Lokal'de:
```bash
ls backend/internal/notify/ 2>/dev/null
ls backend/db/migrations/sqlite/00007_*
```
