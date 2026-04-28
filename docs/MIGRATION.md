# Migration: `@brftech/file-explorer` → `@brftech/filex-core`

`@brftech/file-explorer` was the in-tree, brf-mono-only file manager. It lived
inside the brf-mono repo, depended on FishApp's Laravel API, and its config
shape was fishapp-specific.

`@brftech/filex-core` is the standalone successor. It has the same UX but
talks to the new generic Go backend (`filex`). This guide covers what
changed and how to switch.

- [Conceptual changes](#conceptual-changes)
- [Package rename + entry points](#package-rename--entry-points)
- [Config shape](#config-shape)
- [API base / endpoint](#api-base--endpoint)
- [Auth](#auth)
- [Events / props rename map](#events--props-rename-map)
- [Step-by-step example](#step-by-step-example)

---

## Conceptual changes

| Topic                  | `@brftech/file-explorer`                  | `@brftech/filex-core`                       |
|------------------------|-------------------------------------------|---------------------------------------------|
| Backend                | brf-mono (FishApp module, PHP/Laravel)    | filex (Go binary)                           |
| Storage                | One per panel (per FishApp instance)      | Many per filex instance, each = top folder  |
| Auth                   | Sanctum bearer + brf-mono session         | Cookie / Bearer / API-key, multi-driver     |
| Config DSN             | `endpoint?q=...` query mode               | RESTful `apiBase` + path                    |
| Bundling target        | Vendor-shipped (`resources/js/vendor/...`)| Published npm package + CDN                 |
| Self-hosted            | Yes — embedded in brf-mono                | Yes — single binary or Docker               |
| Multi-framework        | No (Vue 3 + Ionic only)                   | Vue 3, React, Web Component                 |
| Realtime collab        | n/a                                       | OnlyOffice (when configured)                |
| Sharing                | brf-mono Share table                      | filex Share table — same UX, new API        |
| Thumbnails             | Server-side via FishApp                   | Server-side via filex pipeline              |

Behaviourally the UI is unchanged — keyboard shortcuts, modals, copy/move
flows, and the right-click menu match exactly.

---

## Package rename + entry points

### Old

```bash
# Vendor-only, no npm install — vendored into the host app
# Path: brf-mono/resources/js/vendor/file-explorer/
```

### New

```bash
pnpm add @brftech/filex-core      # Vue 3 SFC + composables
# OR
pnpm add @brftech/filex           # Web Component
# OR
pnpm add @brftech/filex-react     # React adapter
```

### Entry imports

| Old (vendor)                                     | New (npm)                            |
|--------------------------------------------------|--------------------------------------|
| `import { FileExplorer } from '@/vendor/file-explorer'` | `import { FileExplorer } from '@brftech/filex-core'` |
| (vendor CSS auto-injected)                        | `import '@brftech/filex-core/style.css'` |
| `import type { ... } from '@/vendor/file-explorer/types'` | `import type { ... } from '@brftech/filex-core'` |

The web-component package self-registers on import:
```ts
import '@brftech/filex';
// <filex-explorer> is now a valid HTML element
```

---

## Config shape

### Old
```ts
{
  endpoint: '/api/files/manager',     // brf-mono Laravel endpoint
  uploadEndpoint: '/api/files/upload',
  shareEndpoint: '/api/files/share',
  bearer: localStorage.getItem('sanctum_token'),
  fishappPanel: 'admin',
  locale: 'tr',
}
```

### New
```ts
{
  apiBase: 'https://files.example.com',     // single base; routes derived
  auth: { kind: 'bearer', token: '...' },   // structured auth descriptor
  startPath: '/storage1',                   // optional starting location
  locale: 'tr',
  theme: 'auto',
}
```

Why: filex backend exposes a stable REST surface under `/api/files/*` and
`/api/admin/*`; the client only needs the base. Per-route overrides aren't
needed.

---

## API base / endpoint

### Old query-mode endpoints

```
GET  /api/files/manager?q=index&path=/foo
POST /api/files/manager?q=upload
POST /api/files/manager?q=move
POST /api/files/manager?q=archive&action=extract
```

### New REST endpoints

```
GET  /api/files/manager?path=/foo
POST /api/files/upload/init
POST /api/files/move
POST /api/files/archive/extract
```

The component does the URL building for you — you only pass `apiBase`. If
you wrote custom code that called the old endpoints by hand, see
[BACKEND.md](BACKEND.md) for the new surface.

---

## Auth

### Old
- Always Sanctum bearer in `Authorization: Bearer …`.
- Cookies were brf-mono session.

### New — pick your scheme

```ts
// Same-origin / behind reverse proxy
auth: { kind: 'cookie' }

// Cross-origin SPA with a JWT
auth: { kind: 'bearer', token }

// Service / kiosk
auth: { kind: 'apikey', header: 'X-API-Key', value: '...' }
```

`cookie` is the default if `auth` is omitted.

---

## Events / props rename map

| Old                          | New                          | Notes |
|------------------------------|------------------------------|-------|
| `@file-explorer-error`       | `@error`                     | Vue event |
| `@file-explorer-share`       | `@share-created`             | Vue event |
| `@file-explorer-upload`      | `@upload-progress` / `@upload-done` | split into two |
| `@file-explorer-select`      | `@select`                    | payload now `{ items: FileNode[] }` |
| `:fish-panel="..."`          | (removed)                    | filex is panel-agnostic |
| `:initial-path="..."`        | `:config="{ startPath }"`    | folded into config |
| `:bearer="..."`              | `:config="{ auth: { kind: 'bearer', token } }"` | structured |
| Slot `extra-actions`         | Slot `toolbar-extra`         | rename + signature changed |
| Method `reload()`            | Method `refresh()`           | rename |
| `<file-explorer>` (kebab WC) | `<filex-explorer>`           | new tag, same semantics |

---

## Step-by-step example

### Old (vendor) Vue component

```vue
<script setup>
import { FileExplorer } from '@/vendor/file-explorer';
import '@/vendor/file-explorer/style.css';

function onShare(payload) {
  navigator.clipboard.writeText(payload.url);
}
</script>

<template>
  <FileExplorer
    :endpoint="'/api/files/manager'"
    :bearer="bearer"
    :initial-path="'/'"
    :fish-panel="'admin'"
    locale="tr"
    @file-explorer-share="onShare"
    @file-explorer-error="onError"
  />
</template>
```

### New (npm) equivalent

```vue
<script setup lang="ts">
import { FileExplorer } from '@brftech/filex-core';
import '@brftech/filex-core/style.css';
import type { ExplorerConfig, ShareCreatedEvent } from '@brftech/filex-core';

const config: ExplorerConfig = {
  apiBase: 'https://files.example.com',
  auth: { kind: 'bearer', token: bearer },
  startPath: '/',
  locale: 'tr',
  theme: 'auto',
};

function onShareCreated(e: ShareCreatedEvent) {
  navigator.clipboard.writeText(e.share.url);
}
</script>

<template>
  <FileExplorer
    :config="config"
    @share-created="onShareCreated"
    @error="onError"
  />
</template>
```

### Cheatsheet diff

```diff
-import { FileExplorer } from '@/vendor/file-explorer';
-import '@/vendor/file-explorer/style.css';
+import { FileExplorer } from '@brftech/filex-core';
+import '@brftech/filex-core/style.css';

-<FileExplorer
-  :endpoint="'/api/files/manager'"
-  :bearer="bearer"
-  :initial-path="'/'"
-  :fish-panel="'admin'"
-  locale="tr"
-  @file-explorer-share="onShare"
-  @file-explorer-error="onError"
-/>
+<FileExplorer
+  :config="{
+    apiBase: 'https://files.example.com',
+    auth: { kind: 'bearer', token: bearer },
+    startPath: '/',
+    locale: 'tr',
+  }"
+  @share-created="onShareCreated"
+  @error="onError"
+/>
```

### Step-by-step

1. **Stand up filex** — `docker run -p 5212:5212 -v ./data:/data brftech/filex:latest`.
2. **Add a storage** in `/admin/storages` matching what your old app exposed
   (local path, S3 bucket, …).
3. **Migrate users** — either keep them in filex's local DB, or wire OIDC.
4. **Update imports** — search/replace `@/vendor/file-explorer` →
   `@brftech/filex-core`.
5. **Update config props** — collapse `endpoint`, `bearer`, `initial-path`,
   `fish-panel`, into the single `:config="..."` object.
6. **Rename events** — see the table above.
7. **Drop vendor dir** — delete `resources/js/vendor/file-explorer/` and
   prune the autoload paths.
8. **Run E2E** — keyboard shortcuts, drag-drop, share flow.

If you embedded via Web Component (`<file-explorer>`), the only changes are
the tag name and attribute mapping:

```diff
-<file-explorer
-  endpoint="/api/files/manager"
-  bearer="..."
-></file-explorer>
+<filex-explorer
+  api-base="https://files.example.com"
+></filex-explorer>
+<script>
+  document.querySelector('filex-explorer').config = {
+    auth: { kind: 'bearer', token: '...' },
+  };
+</script>
```
