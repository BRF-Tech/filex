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

## 4b. Multi-tenant root confinement (lock to a sub-folder)

For multi-tenant hosts (e.g. one explorer per project) you must confine each
caller to its own folder. **Do it server-side — the frontend `rootPath` below is
only cosmetic.** filex enforces confinement on `/api/files` from two sources
(narrowest wins):

1. **Root-scoped API token** (hard ceiling, un-bypassable). Create a filex API
   token whose `scopes` include `root:<adapter>://<rel>`, e.g.
   `read,write,delete,root:main://projeler/acme`. Proxy `/api/files/*` with it
   as `Authorization: Bearer <token>` (server-side — the browser never sees it).
2. **`X-Filex-Root` header** (per-request, narrows within the token root). Your
   proxy sets `X-Filex-Root: main://projeler/acme` per request. A stray client
   header can only narrow, never escape the token root.

Any request touching a path outside the root → `403`. A root/empty path snaps to
the confined folder, so listings open there. This covers manager / move / copy /
delete / upload / download / share / archive / trash.

Recommended: one root-scoped token **per tenant/folder** (or a single service
token + a per-request `X-Filex-Root`), injected by your proxy.

**Frontend `rootPath` (clean UX, optional):** set `config.rootPath:
'main://projeler/acme'` so the explorer opens there, hides the drives root, and
can't navigate above it. This is presentation only — keep the backend
confinement above regardless.

## 4c. Recommended production pattern — host-proxied + confined

The robust, secure way to embed filex (any host app — a project workspace, a
customer portal, a per-team drive). The browser only ever talks to YOUR app;
your app proxies to filex and owns auth + confinement, so it can never be
bypassed from the client.

```
Browser ── /your/files/* ──▶  Your app (proxy)  ── /api/files/* ──▶  filex
   (your session, no                │ injects, server-side:
    filex creds at all)             │   Authorization: Bearer <filex token>
                                     │   X-Filex-Root: main://<tenant-root>
                                     │ strips any client-sent Authorization
                                     │   and X-Filex-Root (never trust them)
```

1. **Vendor the web component** (no build): copy `packages/webcomponent/dist/`
   into your app's assets and load `filex.js`. Or `import '@brftech/filex'`.
2. **Add a proxy route** in your backend, `"/your/files/*" → "<filex>/api/files/*"`.
   On every request it MUST:
   - add the filex auth (a Bearer API token — ideally root-scoped per §4b, or a
     filex session) so the browser never holds filex credentials;
   - add `X-Filex-Root: <adapter>://<tenant-root>` for the current tenant;
   - **strip** any incoming `Authorization` / `X-Filex-Root` from the browser.
3. **Mount the component** against the proxy:
   ```js
   el.config = {
     apiBase: '/your/files',          // your proxy, NOT filex directly
     auth: { kind: 'none' },          // auth is injected by the proxy
     rootPath: 'main://projeler/acme',// clean UI floor (cosmetic)
     locale: 'tr', theme: 'auto',
   };
   ```
4. **Verify isolation:** while scoped to tenant A, a request for tenant B's path
   must return `403`. Because the browser can't set the token or the header
   (the proxy controls both), a tenant cannot reach another's files — even by
   crafting requests by hand.

Pick the confinement strength in §4b: the `X-Filex-Root` header alone is enough
when filex is reachable ONLY through your proxy; a root-scoped token adds
defense-in-depth (the token itself can't escape its folder).

## 5. Backend side (what the host must provide)

- A reachable filex backend (the Go binary) with the storages you want exposed.
- An auth token the explorer can send as `Authorization: Bearer …` (or a CSRF
  cookie). Issue it from your app's session — the explorer never logs in itself.
- CORS: if the explorer is served from a different origin than the API, allow it.

That's it — drop the component in, give it `apiBase` + a token, and the file
manager is live. See `demo/` for runnable references.
