# filex — embedding / integration handoff

Simple guide for embedding the filex file manager into a host app (Vue, React,
or plain HTML). The explorer is one component; you point it at a filex backend
and pass an auth token. Everything else (uploads, preview, share, move, delete,
trash, convert) is built in.

## 1. Pick a wrapper

| Host app | Package | Component | Stylesheet |
|----------|---------|-----------|------------|
| Plain HTML / any framework | `@brftech/filex` (web component) | `<filex-explorer>` | none — the bundle injects it |
| React 18/19 | `@brftech/filex-react` | `<FileManager>` | none — same bundle, same injection |
| Vue 3 | `@brftech/filex-core` | `FileExplorer` | `import '@brftech/filex-core/style.css'` |

All three render the **same** explorer — they differ only in how you mount it.

⚠ The stylesheet column is not a detail: the look is one global sheet plus the
`--fe-*` tokens on it, and only the Vue wrapper imports it by hand. The web
component carries the same bytes *inside its JavaScript* and appends them to
`<head>` on first mount, and the React adapter wraps that web component — so a
React host imports no CSS and is not missing anything. `style.css` is also
published by both `@brftech/filex-core` and `@brftech/filex` for a host that
would rather serve the file itself.

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

  locale: 'tr',            // 'tr' | 'en' | any language a pack adds; sets this
  theme: 'auto',           // 'light' | 'dark' | 'auto'
  // ⚠ Show a "drives" root listing every storage — and PAIR IT WITH `storages`.
  // The explorer MIRRORS the list you hand it; it does not go and discover the
  // server's storages by itself. Set this to true without `storages` and the
  // root is empty and no listing request is ever made — which looks exactly
  // like a broken connection rather than a missing option.
  multiStorageRoot: true,
  // ⚠ Fill in `uid` wherever you know it. A storage's NAME is editable — that
  // is what a name is for — so it is not a stable address, which is why every
  // protocol also accepts the uid as a path's first segment. Anything keyed on
  // a storage for the long term prefers it: per-folder view memory does, so a
  // renamed storage keeps its folders' remembered views instead of losing them.
  // Absent = the name is used, and a rename costs the memory once.
  storages: [
    { name: 'docs', uid: '0f0a8d6e-1c3f-4f2a-9a4e-6a1b2c3d4e5f' },
    { name: 'media', label: 'Media', readOnly: true },
  ],
  trashVisible: true,      // show the Trash entry (list + restore)

  // Per-folder view memory (view mode + sort), Windows-Explorer style.
  // Default ON — this flag says the feature is AVAILABLE to the person, who
  // then turns the memory itself on in their own settings (default off there).
  // The opt-out is for embeds: a filex in a two-inch panel has one shape it
  // wants and no room to argue with a gallery view arriving from the person's
  // main window. State lives in a server-side per-user document, not
  // localStorage, so it follows them across browsers and never leaks between
  // two accounts looking at the same folder.
  // ⚠ Column widths are NOT covered by this flag — they are a global
  // preference about the reader's screen, not about the folder.
  rememberFolderView: true,

  // Navigation panel, in the order it draws: the primary "+ New" menu (upload
  // files · new folder · new document · request files), the destinations
  // Home · Shared with me · Recent · Starred · Trash (plus "My files" when the
  // caller reaches at most one storage — with several there is a drives root
  // to go back to instead), the tags in use, the storages this caller can
  // reach (in the person's own order — they drag a row or use its menu, and
  // it is kept on their account — else in `storages[].sortOrder`, the
  // administrator's order, else in the order of `storages`; STORAGE.md →
  // Ordering storages), an "Apps" section when an installed app has a home screen
  // (docs/APP-PLUGINS.md), then "How to connect" + "API keys".
  // ON by default on every surface. The viewer collapses it to a 56px
  // icon rail from the control at the FAR LEFT OF THE TOP BAR — above the
  // panel, not inside it, so it is still there when the panel is a rail — and
  // that choice is remembered per browser. Below 560px it is a drawer over the
  // listing rather than a column, and the drawer keeps a dismiss of its own.
  // ⚠ `rootPath` flips the default to off — a confined embed has no storage
  // list, and its views would name files outside the folder you confined it to.
  sideNav: true,

  // The panel's "How to connect" (WebDAV · SFTP · FTPS · S3 · NFS · filex
  // mount guides, built from THIS deployment) and "API keys" (mint/revoke the
  // token three of those protocols sign in with). Default on, except under
  // `uiProfile: 'simple'` where it is off.
  // ⚠ Never gated on role: the backend already decides what a caller sees, and
  // /api/tokens caps every scope against the caller's own role and grants — and,
  // since v0.43.0, against the CALLING CREDENTIAL too: a token cannot mint one
  // wider than itself (`403 token_ceiling`). A browser session has no ceiling.
  connections: true,

  // Is a PERSON behind this explorer, or an integration?
  // 'app' drops the surfaces that belong to one identity — API keys, Recent,
  // Starred, Shared with me — and keeps Upload, the storages, Trash and
  // "How to connect". Read from GET /api/files/capabilities (`caller_kind`)
  // when you omit it; set it only to answer before that request lands.
  // ⚠ Proxying with one shared token (below) is exactly the 'app' case.
  callerKind: 'app',

  // How a MOUSE opens an item. 'double' (default) — a single click selects, a
  // double click opens (Enter opens the selection). 'single' — the first click
  // opens. ⚠ TOUCH is not governed by this: a tap always opens (there is no
  // hover-to-select), and the checkbox always selects, in either mode. A
  // per-viewer preference; the desktop app exposes it as Settings → Open files with.
  openTrigger: 'double',

  // Hand the OPEN of a file to the HOST. When true, opening a file emits the
  // `file-opened` event and the explorer does NOT mount its in-page preview —
  // the host decides what to do with it. Directories still navigate inline,
  // Space quick-look still peeks, and an E2E-encrypted file keeps the in-page
  // decrypted preview. Default false. The desktop app sets it to open each
  // document in its own window.
  openInHost: false,

  // The product mark at the far left of the top bar, beside the navigation
  // panel's collapse control. Both halves optional; neither renders nothing.
  //   name    — wordmark text, printed verbatim, never translated
  //   markUrl — the glyph, as an <img src>: a path, a URL, or a data: URI
  // ⚠⚠ Use this rather than the `#brand` slot in a WEB COMPONENT. A slot
  // cannot be filled in `<filex-explorer>` at all — measured: a
  // `<span slot="brand">` inside the element is discarded, and the element's
  // `setup` sees no slots, with or without a shadow root. Vue projects light
  // DOM into a custom element only through a native `<slot>` inside a shadow
  // root, and this element is deliberately light-DOM so the one global
  // stylesheet reaches it. A host mounting the Vue SFC can use either; the
  // slot wins when it is filled.
  brand: { name: 'filex', markUrl: '/logo.svg' },

  // How much of the explorer to put on screen. A REDUCTION, and only that.
  // 'standard' (default) — everything: tab strip, split pane, list/grid/gallery.
  // 'simple'             — one pane, one folder, list/grid, no tab strip, no
  //                        split, Connections off. Nothing is removed from the
  //                        build; this is a preset for people who do not want a
  //                        power tool.
  // Two values, and no third. ⚠ Anything else resolves to 'standard' and logs
  // one console line naming it — the 'drive' profile was REMOVED after v0.40.0,
  // so pass 'simple' if that is what you were asking for.
  // ⚠⚠ It does NOT decide the look. The shell — one "+ New" menu, one search
  // field in the header with its ⌘K/Ctrl+K chip, the Type/People/Modified/Size
  // filter row, Folders and Files as sections in grid view (replaced by date
  // headings while the listing is sorted by Modified), Details/Activity in the
  // info panel, the storage line under the navigation, and the Home view — used
  // to be gated behind a profile. It is the default now, in the admin app, the
  // desktop app and every embed, with no string passed.
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

The panel's **Tags** section lists every tag the person can see (`GET
/api/files/manager/tags/all`) and opens one as a listing of the files carrying
it (`GET /api/files/manager/tagged`). Since v0.43.0 tags are **personal** (the
person's own, like a star) or **team** (everyone in the tenant who can see the
file) — see [Tags — personal and team](SEARCH.md#tags--personal-and-team).
Notes for embedders:

- The section is two groups, **Personal** then **Team**, each under its kind's
  name and glyph (`TagKindIcon`, exported), and every tag chip on a file, every
  pick in the advanced search and the tag view's crumb carry the same glyph and
  the kind in words. A tag on screen always says who can see it.
- `TagPicker` asks who sees a new tag (personal by default) and offers "team"
  only where the server says the person may change team tags
  (`can_edit_team`); for a viewer it stays visible, disabled, with the reason.
  Its `change` event still sends the names first, then the `{name, kind}`
  items; `open` sends the name, then the kind.
- The list is fetched **after** the first folder listing, not during mount, and
  is cached module-wide for a minute with in-flight de-duplication: several
  explorers on one page cost one query, and navigation costs none. Editing a
  node's tags drops the cache immediately.
- The first eight tags are shown with a "Show N more"; on the 56px icon rail the
  section collapses to a single **Tags** button that opens the panel, because a
  rail of identical tag glyphs names nothing.
- A tag view is a virtual listing like Starred: it parks a sentinel in the
  path, so it is deep-linkable, and every surface that renders a path segment —
  tab strip, breadcrumb, details panel — shows `#invoices`. The sentinel names
  the kind: `.mytag~invoices` (personal, crumb `#invoices · Personal`),
  `.teamtag~invoices` (team), and the pre-v0.43 `.tag~invoices`, which keeps
  working and lists **both** kinds. A hash naming a tag that no longer exists
  opens that tag's empty state, never an error.
- ⚠ Reloading on any virtual view's hash (`#.trash`, `#.starred`, `#.recent`,
  `#.shared`, `#.tag~…`) opens the **view**. Those hashes used to be handed to
  the ordinary folder load, which answered "folder not found" for a trash that
  was simply empty.

### Keyboard shortcuts, and the keys the menus print

Every verb the explorer has is one entry in a registry: an id, a default
combination, a label. The right-click menu and the toolbar tooltips print the
current combination beside the verb, so a user learns the key where they are
already looking rather than by opening a cheat sheet on their own initiative.

Nothing needs configuring for that. What an embedder can rely on:

- the user remaps anything from **Shortcut settings** (the "⋯" menu, or the
  `?` sheet's *Customize*). Overrides live in `localStorage` under
  `filex.shortcuts` as `{ "<actionId>": "<combo>" }` — per browser, never sent
  to the server.
- an action can be left **unbound** (`""`), and a verb with no key prints none
  rather than an empty key cap.
- combinations the browser takes before the page sees them — `Ctrl+W`,
  `Ctrl+T`, `Ctrl+Tab`, `F12` and friends — are refused with a reason. The tab
  actions ship on exactly those, so their rows are badged *Desktop app only*:
  they fire in the desktop app and in an installed PWA, not in a browser tab.
- if your own UI names a key, read it rather than typing it:

```ts
import { shortcutHint, eventMatchesShortcut } from '@brftech/filex-core';

shortcutHint('palette');             // 'Ctrl+K', '⌘+K' on a Mac, or the user's own
eventMatchesShortcut(ev, 'palette'); // true when THIS event fires that action
```

`docs/API.md` has the full list; `SHORTCUT_ACTIONS` is the source of truth.

### Themes

A theme is a map of `--fe-*` custom properties in a light and a dark variant,
not a second stylesheet — picking one is independent of light/dark mode, which
keeps deciding which variant is active. Eight ship (Default, Night blue,
Forest, Amber, Lilac, High contrast, Soft gray and Terminal green), and a host
that wants its own look sets the same tokens on any scope above the explorer.
Every shipped palette clears WCAG 2.1 contrast in both variants, which is a
check in the test suite rather than a claim.

An operator can add **their own** beside those eight on the admin panel's
**Appearance** screen (`/admin/appearance`): named themes with twelve colours
per variant, a corner radius and a font stack, one of which may be made the
instance default. The filex web app reads them from the public
`GET /api/appearance` at boot, so they reach the sign-in page and anonymous
share visitors too — and a signed-out window wears the instance default, never
the palette of whoever last signed in on that browser. A signed-in person's
pick is kept on their **account** (`GET|PUT /api/me/prefs`), not in the
browser. An embed does neither by itself: it offers the built-in eight unless
the host fetches `/api/appearance` and hands the list to `setCustomThemes`, and
a pick made inside it is remembered in that browser.

#### Operator custom CSS

An operator who wants a look no shipped theme gives can paste a stylesheet in
the admin panel: **Appearance -> Custom CSS** (`/admin/appearance`), stored as
the setting `ui.custom_css` and capped at 64 KB (the page counts the same UTF-8
bytes the server enforces).

> ⚠ **It is off until you switch it on, and that includes an installation that
> already had a sheet.** A second setting, `ui.custom_css_enabled`, gates it;
> absent, or anything other than a true-ish value, means off. There is
> deliberately no grandfathering — the rules below changed underneath existing
> sheets, so continuing to apply one silently would be applying something the
> operator never approved. **Upgrading with a custom stylesheet in use means
> re-enabling it.** The editor moved with it: it used to live under *Settings*.

It is served from `GET /api/me/custom-css`, **behind authentication**, with
`Cache-Control: no-store`, and injected as the text of a single
`<style data-filex-custom>` element appended last in `<head>`. Last is
deliberate: a token override ties on specificity with the declaration it
overrides, so source order is what decides, and the element moves back to the
end whenever a lazily loaded route injects its own CSS.

> ⚠⚠ **It is not on the public `GET /api/branding` payload, and the field is
> gone rather than blanked.** It used to ride that payload, which is the
> pre-session appearance fetch — so the sheet reached anonymous visitors and
> the sign-in form itself. A client still reading `branding.custom_css` now
> finds no such key, on purpose: an old client should fail loudly rather than
> quietly render an unstyled page while somebody believes the feature works.

Things worth knowing before you write one:

- **The `--fe-*` custom properties are the supported surface.** They are listed
  above and in `packages/core/src/styles/variables.css`, and setting them on any
  scope above the explorer — `.fe`, `:root`, one wrapper div — is the whole API.
  A sheet that only assigns tokens keeps working across releases.
- **Class names are not a stable API.** `.fe-row`, `.fe-grid-card` and friends
  are internal markup that moves between releases with no deprecation and no
  changelog entry. Target them if you must, and re-check your sheet on every
  upgrade. That includes the admin panel's own chrome, which is built from
  utility classes rather than `--fe-*` tokens: a token-only sheet restyles the
  file surfaces and leaves the panel's sidebar and buttons alone.
- **No anonymous surface wears it.** The sign-in page, share pages, the PIN
  gate and an app's public pages are served to people with no session, and the
  endpoint carrying the sheet refuses them — they cannot download it, let alone
  apply it. Those surfaces follow the instance **theme** instead, which is a
  token set and so cannot hide a control or repaint a sign-in form.
- **It cannot reach the screen that turns it off.** Everything served is
  wrapped in `@scope (:root) to (.fe-css-immune)`, so an operator selector
  cannot match inside a subtree carrying that class — the Appearance panel and
  destructive confirmation dialogs do. On top of that, the Appearance route
  takes the `<style>` element out of the document while it is open, which is
  the guard that survives `:root { display: none }`: scoping decides what a
  rule *matches* and cannot un-apply an inherited or ancestor-level property.
  An engine with no `@scope` support drops the block outright, so the sheet
  simply does nothing — a plain-looking panel rather than an unguarded one.
- **It cannot phone home.** `@import` is stripped outright, and every `url()`
  and `image-set()` that is not a `data:` URI or a same-document `#fragment` is
  rewritten to an inert `url("data:,")`. That closes the CSS exfiltration
  classic — an attribute selector plus a background image reporting which page
  or which filename a viewer is on — because after the pass the sheet has no
  way left to originate a request. Top-level `@font-face` and `@keyframes` are
  hoisted outside the scope wrapper, since both define a *name* rather than
  matching elements; that is safe only because the `url()` pass has already
  run. The Appearance screen names whatever it removed, instead of silently
  serving something other than what you typed.
- **A change reaches other browsers on their next load.** The response is
  `no-store`, so there is no cache window to wait out: this is the setting an
  operator turns off in a hurry, and a sheet still sitting in a shared cache
  after the switch was flipped is the exact failure the switch exists to end.
  The admin who saves sees it immediately — the Appearance page applies its own
  save without waiting for a reload.
- **It applies to everyone signed in to the installation** — it is one
  instance-wide row, not a per-user preference and not a per-tenant one. In
  multi-tenant mode only the supertenant may write it; a tenant admin's write
  is refused, the way every other instance-wide setting is. Clearing the box
  removes the element entirely.

The value is CSS and is only ever assigned as a style element's text, so it
cannot introduce markup; the server additionally refuses a sheet containing
`</style`, which no stylesheet needs. Embedded web-component and React hosts
are NOT styled by this setting — their page is yours, so set the same tokens
there directly.

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
  // ⚠ The payload is e.detail[0]: a custom element's detail is the emit's
  // argument LIST (docs/API.md → Events).
  el.addEventListener('error', (e) => console.error(e.detail[0].message));
  el.addEventListener('file-opened', (e) => console.log(e.detail[0].path));
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
  onError={(e) => console.error(e.detail[0].message)}
  onFileOpened={(e) => console.log(e.detail[0].path)}
/>
```
> One import, no separate stylesheet — see the table in section 1. The
> connections panel has no React wrapper: render `<filex-connections>` in JSX
> and set `config` on the ref, the way you would any non-React element.
>
> ⚠ The optional viewer peers (Monaco, Mermaid, epub, xlsx, CodeMirror, …) are
> reached through dynamic imports that are caught, so a build without them
> falls back to the plain viewer — but a bundler still refuses to resolve an
> import it cannot find. If `vite build` stops with `Rollup failed to resolve
> import "monaco-editor"`, either install the ones you want or list them in
> `build.rollupOptions.external`.

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

1. **Root-scoped API token** (the server-side ceiling). Create a filex API
   token whose `scopes` include `root:<adapter>://<rel>`, e.g.
   `read,write,delete,root:main://projeler/acme`. Proxy `/api/files/*` with it
   as `Authorization: Bearer <token>` (server-side — the browser never sees it).
   Mint it at `POST /api/admin/ai-tokens`, which issues `kind: "app"` by
   default — the right kind here, because this one credential stands in for
   every visitor. ⚠ Confinement and kind are independent: a `root:` scope does
   not make a token an app, and an app token is not confined unless you say so.
   ⚠⚠ Pass a **non-admin** `user_id`. Omitted, the token is bound to the admin
   minting it — and the admin panel's own `/api/admin/*` routes are gated on the
   account's role, so a token is only as limited as the account behind it.
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
   ⚠ **And one for `/api/ws`**, or the embed has no live updates: the ticket is
   minted under `/api/files/ws-ticket`, which that rule covers, but the socket
   itself is at a *different* prefix. See the note under step 4.
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

⚠⚠ **Live updates need one more route than this pattern gives them.**
`POST /api/files/ws-ticket` is proxied by the rule above and returns an
**absolute** `ws_url`, derived from filex's own public origin (the tenant's
host in multi-tenant mode). Two things follow:

- the browser is told to open `wss://<filex host>/api/ws`, which in a
  proxy-only deployment it cannot reach — the embed then falls back to
  re-listing every 12 s, silently, and looks merely sluggish rather than
  broken;
- `/api/ws` is not under `/api/files/`, so `"/your/files/*"` never matched it
  in the first place.

Proxy `/api/ws` alongside the files route, pass the WebSocket upgrade and the
`Host` header through, and set filex's `FILEX_PUBLIC_URL` (or the tenant's
host) to the origin the *browser* uses, so the `ws_url` it hands back points at
your proxy. The contract itself — frames, coalescing, the ceiling your own
debounce needs — is in [REALTIME.md](REALTIME.md).

Pick the confinement strength in §4b: the `X-Filex-Root` header alone is enough
when filex is reachable ONLY through your proxy; a root-scoped token adds
defense-in-depth (the token itself can't escape its folder).


## 4d. Dragging files out (host hook)

Dragging a **single file** out of the explorer onto the desktop works in any
Chromium browser with no host involvement: the component puts a `DownloadURL`
on the drag and the browser downloads it where it was dropped. The browser's
download stack sends cookies but no `Authorization` header, so the URL depends
on the credential:

- **`auth: {kind: 'none' | 'csrf'}` or a cookie session** — the plain download
  URL, through `apiBase` like every other call (so a host proxy that covers
  `/api/files/*` covers it too).
- **A bearer token** — a short-lived, single-use link the server mints for that
  one file (`POST …/archive/download {"mode":"file"}` → `/z/<ticket>` on the
  API's origin; [API.md](API.md#a-single-file-the-drag-out-link)).
  The component asks for it while the pointer rests on the row, because
  `dragstart` cannot wait for the network; a drag that beats it carries
  nothing rather than a URL that would `401`. Against a server older than this
  mode the drag is not offered, as before.

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

A result in the ⌘K palette's **Everywhere** group drags out through the same
path — `dragOut` when the host supplies it, the single-file `DownloadURL`
(plain URL or minted link, as above) otherwise — and carries a download button
of its own. A drag
let go on the palette itself is swallowed there (the explorer's root would take
an OS file drop for an upload) and the host's `cancel` is called.

## 4e. ⌘K across several accounts (host hook)

A host that holds several accounts at once — the desktop app's rail — can have
the palette's **Everywhere** group search all of them. The explorer never talks
to another account's server itself and is never handed its credential; every
call that reaches another account goes through `accountSearch`:

```ts
config.accountSearch = {
  // The account THIS explorer is mounted for — heads its own group.
  self: { id: 'acc-1', label: 'files.example.com', detail: 'ada@example.com', color: '#4f7ce8' },
  // Every OTHER account signed in now. Read on each query.
  others: () => host.accounts().filter((a) => a.id !== 'acc-1'),
  // /api/files/search on that account's server, with ITS credential.
  search: (accountId, query, { limit, scope }) => host.search(accountId, query, { limit, scope }),
  // The hit arrives addressed: { path: 'name://rel', basename, type }.
  open: (accountId, item) => host.switchAndReveal(accountId, item),
  download: (accountId, item) => host.download(accountId, item.path),   // optional
  dragStart: (accountId, items) => host.dragOut(accountId, items),      // optional
};
```

Hits come back grouped under one badge per account (`label`, `detail`, a dot in
`color`), this mount's own account first, each account capped on its own. An
account whose search fails or times out loses its own group, never the others'.
Leave `download` or `dragStart` out and those rows simply do not offer the verb.
Without `accountSearch` — the web app, every single-account embed — the palette
is unchanged: this account's hits, no badges.

⚠ Put the credential on the request yourself. A host that attaches tokens by
**origin** (as the desktop app does for `<img>` and download links) picks one
token per server, and two accounts on one server share an origin — a download
of the other person's file would go out as whoever the window is showing. The
desktop app sets the `Authorization` header on that download explicitly
(`desktop/src/main.ts`, `remote:download`), and keys its prepared drag copies by
account as well as by path.

## 5. Backend side (what the host must provide)

- A reachable filex backend (the Go binary) with the storages you want exposed.
- An auth token the explorer can send as `Authorization: Bearer …` (or a CSRF
  cookie). Issue it from your app's session — the explorer never logs in itself.
- CORS: if the explorer is served from a different origin than the API, allow it.
- A path to the **WebSocket** at `/api/ws`, with the upgrade and the `Host`
  header passed through (the same-origin, cookie-authenticated upgrade is
  origin-checked). Without it the explorer still works and still shows changes
  — on a 12 s poll instead of instantly.
- A path to `/api/files/thumb/{id}`, if you want thumbnails in the grid and
  gallery views. See the note below.

### Thumbnails and `<img>`

The explorer does **not** put `thumb_url` straight into an `<img src>`. It fetches
each thumbnail with the same `fetch()` machinery as every other API call — auth
headers plus `credentials` — and hands the grid a `blob:` URL
(`useThumbs`). That is what makes thumbnails work in an embed at all: an `<img>`
sends no `Authorization` header, and filex's session cookie is `SameSite=Lax`, so
a cross-site `<img>` sends no cookie either.

If your host renders `thumb_url` itself, it still works: the backend stamps every
`thumb_url` it emits with a short-lived signature (`?exp=…&sig=…`) that the
endpoint accepts with no credentials at all. Two consequences worth knowing:

- ⚠ **Pass the URL through verbatim.** Stripping the query string turns a
  working image into a 401.
- ⚠ **The stamp is a capability.** Anyone who gets that URL can fetch that one
  preview until it expires (`FILEX_THUMBS_URL_TTL`, default 24 h) — treat it like
  a share link, not like a private path.

⚠ If your proxy injects a **root-confined** token (§4b/§4c), thumbnails obey the
confinement too: a node outside the token's subtree answers 404 rather than
rendering.

That's it — drop the component in, give it `apiBase` + a token, and the file
manager is live. See `demo/` for runnable references.
