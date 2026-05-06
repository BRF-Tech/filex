# Δ Admin UI delta sayfaları

> Mevcut admin web app'e (`web/`) eklenmesi gereken sayfalar.

---

## Mevcut sayfalar (cc65864)

`web/src/views/`:
- About, Audit, AuthProviders, Dashboard, External, Login, Profile, SearchTest, Settings, Shares, Storages, StorageEdit, StorageNew, Sync, Trash, UserEdit, Users.

## Eklenecek sayfalar

### 1. `Replica.vue`
4 sekmeli tek sayfa: Rules / Failures / Status Report / Settings.
Detaylar: `05-replica.md` "Frontend Admin UI sayfaları" bölümü.

### 2. `Notifications.vue`
Admin için tüm bildirimleri görüntüleme + webhook config:
- Bildirim listesi (broadcast + tüm user-scoped). Filter: severity, event type, read/unread.
- Webhook config form (URL + token + test gönder butonu).
- Settings: `notification_settings` tablo defaults.

### 3. `Queue.vue`
Queue stats + op listesi:
- Stats kartları (Pending / Running / Failed / Done24h).
- Op tablosu (id, type, status, attempts, last_error, enqueued_at, started_at). Status filter.
- Action: Retry (failed → pending) + Cancel (pending → cancelled).

### 4. `NotificationBell.vue` (component)
App layout'ta üst sağda. 04-notify.md §Frontend bell component'inde detayı var.
App.vue'ye entegre et.

## Router güncelleme

`web/src/router/index.ts` veya `routes.ts`:

```ts
{
  path: '/replica',
  name: 'Replica',
  component: () => import('@/views/Replica.vue'),
  meta: { requiresAuth: true, requiresAdmin: true }
},
{
  path: '/notifications',
  name: 'Notifications',
  component: () => import('@/views/Notifications.vue'),
  meta: { requiresAuth: true, requiresAdmin: true }
},
{
  path: '/queue',
  name: 'Queue',
  component: () => import('@/views/Queue.vue'),
  meta: { requiresAuth: true, requiresAdmin: true }
},
```

## Pinia stores

`web/src/stores/`:
- `replica.ts` — rules CRUD, failures list, fix all, status report, settings
- `notifications.ts` — list, mark read, settings, webhook config
- `queue.ts` — stats, list, retry, cancel

## API client

`web/src/api/`:
- `replica.ts` — admin endpoints (`/admin/api/replica/*`)
- `notifications.ts` — user (`/api/notifications/*`) + admin (`/admin/api/notifications/*`)
- `queue.ts` — admin (`/admin/api/queue/*`)

## i18n

`web/src/i18n/` → tr.json + en.json:
- `replica.*` (rules, failures, fix, settings)
- `notifications.*` (bell, list, settings, webhook)
- `queue.*` (stats, list, retry)

## Sidebar/menu güncelleme

`web/src/App.vue` veya nav component'i:
- "Replica" linki (admin role gerektirir)
- "Notifications" linki
- "Queue" linki
- Üst sağda `NotificationBell.vue` mount

## Tahmini efor
- Replica.vue (4 sekme): 4-5 saat
- Notifications.vue: 2-3 saat
- Queue.vue: 2 saat
- NotificationBell.vue: 1-2 saat
- Stores + API clients: 3 saat
- Router + nav + i18n: 2 saat
- Test (vitest): 3 saat

**Toplam:** ~17 saat (2 gün full-time)

## Bağımlılık
- 03-queue.md ve 04-notify.md ve 05-replica.md backend'lerinin endpoint'leri hazır olmalı.
- Yoksa mock API ile başla, backend gelince swap.
