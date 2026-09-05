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
  // ⚠ Show a "drives" root listing every storage — and PAIR IT WITH `storages`.
  // The explorer MIRRORS the list you hand it; it does not go and discover the
  // server's storages by itself. Set this to true without `storages` and the
  // root is empty and no listing request is ever made — which looks exactly
  // like a broken connection rather than a missing option.
  multiStorageRoot: true,
  storages: [{ name: 'docs' }, { name: 'media', label: 'Media', readOnly: true }],
  trashVisible: true,      // show the Trash entry (list + restore)

  // Navigation panel: Upload · Recent / Starred / Shared with me / Trash · the
  // tags in use · the storage list. ON by default on every surface. The viewer collapses it to an
  // icon rail with the toggle in the panel head (or the one in the toolbar) and
  // that choice is remembered per browser. Below 560px it is a drawer over the
  // listing rather than a column.
  // ⚠ `rootPath` flips the default to off — a confined embed has no storage
  // list, and its views would name files outside the folder you confined it to.
  sideNav: true,

  // The panel's "How to connect" (WebDAV · SFTP · FTPS · S3 · NFS · filex
  // mount guides, built from THIS deployment) and "API keys" (mint/revoke the
  // token three of those protocols sign in with). Default on, except under
  // `uiProfile: 'simple'` where it is off.
  // ⚠ Never gated on role: the backend already decides what a caller sees, and
  // /api/tokens caps every scope against the caller's own role and grants.
  connections: true,

  // Is a PERSON behind this explorer, or an integration?
  // 'app' drops the surfaces that belong to one identity — API keys, Recent,
  // Starred, Shared with me — and keeps Upload, the storages, Trash and
  // "How to connect". Read from GET /api/files/capabilities (`caller_kind`)
  // when you omit it; set it only to answer before that request lands.
  // ⚠ Proxying with one shared token (below) is exactly the 'app' case.
  callerKind: 'app',

  // 'standard' (default) — tab strip, split pane, list/grid/gallery.
  // 'simple'             — one pane, one folder, list/grid, no tab strip, no
  //                        split. Nothing is removed from the build; this is a
  //                        preset for people who do not want a power tool.
  uiProfile: 'simple',
};
```

### Connections, from inside the explorer

`ConnectionsPanel` and `TokensPanel` have always been in this package, and
`<filex-connections>` has always been a registerable element — but nothing in
the explorer opened either, so an embedder's users had no path to a protocol
guide or to the API token those guides tell them to use. The navigation panel's
last section is that path: **How to connect** opens the guides in an overlay
inside the explorer, **API keys** opens the full self-service key manager
(scopes, folder confinement, expiry). Both are `config.connections`.

⚠ **If you proxy with one shared API token, API keys is not shown** and neither
are Recent / Starred / Shared with me. That token is `kind: "app"`
([docs/MCP.md](MCP.md#token-kinds--user-vs-app)), every visitor authenticates as
its owner, and "your keys" would have meant the credential your embed itself
runs on. "How to connect" stays — mount instructions belong to nobody in
particular, and your users may still need them — but the credential forms
*inside* it (S3 access keys, SSH keys, NFS exports) are replaced by a line
saying this session cannot mint them, because those are bound to a person too.
Your users read the guide and get their key from you.

`<filex-connections>` still earns its own registration: a host that wants the
connections surface on a page of its own — a settings tab, an onboarding step —
mounts the element (or the `ConnectionsPanel` SFC) without an explorer around
it. What changed is that it is no longer the *only* way in.

### Starring and tags

Starring is an **action**, not a read-out. `Star` / `Unstar` sits beside `Tags…`
in the context menu of every view — list, grid, gallery and the split pane — it
follows a multi-selection, and `S` does the same from the keyboard (remappable
like every other shortcut). Grid and gallery cards additionally carry a star
chip in the corner: it appears on hover or keyboard focus, and stays painted
once the file is starred, so the Starred view's contents are visible without
hovering every tile. All of it is the one `StarButton` component over the one
`POST /api/files/manager/star` call — there is no second starring path to drift.

The panel's **Tags** section lists every tag in use (`GET
/api/files/manager/tags/all`) and opens one as a listing of the files carrying
it (`GET /api/files/manager/tagged`). Notes for embedders:

- The list is fetched **after** the first folder listing, not during mount, and
  is cached module-wide for a minute with in-flight de-duplication: several
  explorers on one page cost one query, and navigation costs none. Editing a
  node's tags drops the cache immediately.
- The first eight tags are shown with a "Show N more"; on the 56px icon rail the
  section collapses to a single **Tags** button that opens the panel, because a
  rail of identical tag glyphs names nothing.
- A tag view is a virtual listing like Starred: it parks the sentinel
  `.tag~<name>` in the path, so it is deep-linkable (`#.tag~invoices`) and every
  surface that renders a path segment — tab strip, breadcrumb, details panel —
  shows `#invoices`. A hash naming a tag that no longer exists opens that tag's
  empty state, never an error.
- ⚠ Reloading on any virtual view's hash (`#.trash`, `#.starred`, `#.recent`,
  `#.shared`, `#.tag~…`) opens the **view**. Those hashes used to be handed to
  the ordinary folder load, which answered "folder not found" for a trash that
  was simply empty.

### The navigation panel and the simple profile

Both are ordinary `config` keys, so all three wrappers set them the same way —
and the web component additionally exposes them as attributes for host pages
that never touch JavaScript:

```vue
<!-- Vue -->
<FileExplorer :config="{ ...config, sideNav: true, connections: true, uiProfile: 'simple' }" />
```

```tsx
// React
<FileManager config={{ ...config, sideNav: true, connections: true, uiProfile: 'simple' }} />
```

```html
<!-- Web component: attributes, or the same keys on the config property -->
<filex-explorer sidenav connections ui-profile="simple"></filex-explorer>
```

Boolean attributes follow the DOM convention: `sidenav` (present) and
`sidenav="true"` are true, `sidenav="false"` is false, and leaving the attribute
off keeps the default rather than forcing `false`. `connections` behaves the
same way — and because its default is derived from `uiProfile`, leaving it off a
`ui-profile="simple"` element means no Connections entries, while adding it is
the whole opt-in.

⚠ The `config` **property wins over an attribute**, key by key. `<filex-explorer
sidenav>` plus `el.config = { sideNav: false }` gives you no panel; the same
element with a `config` that never mentions `sideNav` keeps the attribute's
answer. Do not set the same thing in both places and expect the attribute to
have the last word.

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
    sideNav: true, connections: true, uiProfile: 'simple',
  };
  el.addEventListener('error', (e) => console.error(e.detail));
  el.addEventListener('file-opened', (e) => console.log(e.detail));
</script>
```

⚠ **Assign `config` before the module that registers the element loads** — that
is why the `<script src>` above is a plain tag and the assignment happens after
it, and why the npm form below awaits the import *after* setting the property.
Registering the element upgrades and mounts it, and the explorer loads its first
folder on mount. A `config` assigned after that arrives too late for that one
request, which then goes out with no credentials against the default adapter:
the panel and the toolbar render perfectly and the file list says "Could not
load this folder".

```html
<filex-explorer id="fx" api-base="https://files.example.com" sidenav ui-profile="simple">
</filex-explorer>
<script type="module">
  const el = document.getElementById('fx');
  el.config = { auth: { kind: 'bearer', token: TOKEN }, apiBase: 'https://files.example.com' };
  await import('@brftech/filex');   // registers <filex-explorer>
</script>
```
(For npm builds: `import '@brftech/filex';` once registers `<filex-explorer>`.)

### React
```tsx
import { FileManager } from '@brftech/filex-react';

<FileManager
  config={{ apiBase: 'https://files.example.com',
            auth: { kind: 'bearer', token },
            sideNav: true, connections: true, uiProfile: 'simple' }}
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
                 auth: { kind: 'bearer', token },
                 sideNav: true, connections: true, uiProfile: 'simple' };
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
   Mint it at `POST /api/admin/ai-tokens`, which issues `kind: "app"` by
   default — the right kind here, because this one credential stands in for
   every visitor. ⚠ Confinement and kind are independent: a `root:` scope does
   not make a token an app, and an app token is not confined unless you say so.
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


## 4d. Dragging files out (host hook)

Dragging a **single file** out of the explorer onto the desktop works in any
Chromium browser with no host involvement: the component puts a `DownloadURL`
on the drag and the browser downloads it where it was dropped. It is offered
only when the credential travels on its own — a cookie session or `auth: {kind:
'none' | 'csrf'}`. With a bearer token the browser's download stack would send
no Authorization header, so the drop would produce a `401` page named like the
file; the component leaves the drag alone instead.

A host that CAN hand the OS real paths (the desktop app) supplies `dragOut`, and
folders and multi-selections then drop as separate real files:

```ts
config.dragOut = {
  // Make local copies. Called as a drag begins, and for small selections as
  // soon as they are selected. The ordinary HTML5 drag keeps running in the
  // meantime, so an internal move never waits for a download.
  prepare: (items) => shell.prepare(items),   // → { ready: boolean, error?: string }
  // Begin the OS drag. Only called for a selection `prepare` already answered
  // `ready` for — a native drag replaces the HTML5 one and cannot be undone
  // mid-gesture.
  start: (items) => shell.start(items),
  onProgress: (cb) => { /* … */ },
};
```

⚠ While a native drag is in flight the component's own drop targets no longer
see `application/x-brf-files` — they see an OS file drag. The component keeps the
payload on its side and every drop target reads it through the same helper, so a
row dropped on a folder inside the app is still a server-side move rather than a
re-upload of the temp copy. A host implementing `dragOut` does not have to do
anything about that; a host writing its own drop targets does.

## 5. Backend side (what the host must provide)

- A reachable filex backend (the Go binary) with the storages you want exposed.
- An auth token the explorer can send as `Authorization: Bearer …` (or a CSRF
  cookie). Issue it from your app's session — the explorer never logs in itself.
- CORS: if the explorer is served from a different origin than the API, allow it.

That's it — drop the component in, give it `apiBase` + a token, and the file
manager is live. See `demo/` for runnable references.
