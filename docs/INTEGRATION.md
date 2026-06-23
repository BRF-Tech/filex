# filex — embedding / integration handoff

Simple guide for embedding the filex file manager into a host app (Vue, React,
or plain HTML). The explorer is one component; you point it at a filex backend
and pass an auth token. Everything else (uploads, preview, share, move, delete,
trash, convert) is built in.

> Status (2026-06-24): move / delete→trash / copy / folder-share-zip / trash UI /
> menu parity bugs are fixed and deployed (filex v0.1.27, demo + prod). The
> three wrappers below all build against the current core.

## 1. Pick a wrapper

| Host app | Package | Component |
|----------|---------|-----------|
| Plain HTML / any framework | `@brftech/filex` (web component) | `<filex-explorer>` |
| React 18/19 | `@brftech/filex-react` | `<FileManager>` |
| Vue 3 | `@brftech/filex-core` | `FileExplorer` |

All three render the **same** explorer — they differ only in how you mount it.
Working end-to-end examples live in [`demo/`](../demo): `index.html` (vanilla),
`vue.html`, `react.html`. They load from CDN (no build step) — open one and
point it at a backend to see the exact wiring.

## 2. The config object (identical for all three)

```ts
const config = {
  // Backend origin. Either apiBase (we derive /api/files/... from it) …
  apiBase: 'https://files.example.com',
  // …or set explicit endpoints if your routes differ (optional).
  // endpoint: 'https://files.example.com/api/files/manager',

  auth: { kind: 'bearer', token: '<JWT or API token>' },
  //   or { kind: 'csrf', csrf: '<token>' }  for cookie-session hosts
  //   or { kind: 'none' }                    for an open/dev backend

  locale: 'tr',            // 'tr' | 'en'
  theme: 'auto',           // 'light' | 'dark' | 'auto'
  multiStorageRoot: true,  // show a "drives" root listing every storage
  trashVisible: true,      // show the Trash entry (list + restore)
};
```

`auth.token` may also be a function returning a fresh token (sync or async) —
use that when the token rotates.

## 3. Embed snippets

### Vanilla / Web Component
```html
<script type="module" src="https://cdn.jsdelivr.net/npm/@brftech/filex/dist/filex.js"></script>
<filex-explorer id="fx" style="display:block;height:100vh"></filex-explorer>
<script type="module">
  const el = document.getElementById('fx');
  el.config = {
    apiBase: 'https://files.example.com',
    auth: { kind: 'bearer', token: TOKEN },
    multiStorageRoot: true, trashVisible: true, locale: 'tr',
  };
  el.addEventListener('error', (e) => console.error(e.detail));
  el.addEventListener('file-opened', (e) => console.log(e.detail));
</script>
```
(For npm builds: `import '@brftech/filex';` once registers `<filex-explorer>`.)

### React
```tsx
import { FileManager } from '@brftech/filex-react';

<FileManager
  config={{ apiBase: 'https://files.example.com',
            auth: { kind: 'bearer', token } }}
  onError={(e) => console.error(e.detail)}
  onFileOpened={(e) => console.log(e.detail)}
/>
```

### Vue 3
```vue
<script setup>
import { FileExplorer } from '@brftech/filex-core';
import '@brftech/filex-core/style.css';
const config = { apiBase: 'https://files.example.com',
                 auth: { kind: 'bearer', token } };
</script>
<template>
  <FileExplorer :config="config" @error="onError" @file-opened="onOpen" />
</template>
```
> Vue note: `@brftech/filex-core` is the source SFC — mount it directly (this IS
> the Vue wrapper). Its rich viewers (Monaco/PDF/3D/…) are **optional peer deps**;
> install only the ones you want, the rest degrade gracefully.

## 4. Events (same names everywhere; React camelCases them)

`error`, `file-opened`, `share-created`, `upload-progress`, `selection-change`.

## 5. Backend side (what the host must provide)

- A reachable filex backend (the Go binary) with the storages you want exposed.
- An auth token the explorer can send as `Authorization: Bearer …` (or a CSRF
  cookie). Issue it from your app's session — the explorer never logs in itself.
- CORS: if the explorer is served from a different origin than the API, allow it.

That's it — drop the component in, give it `apiBase` + a token, and the file
manager is live. See `demo/` for runnable references.
