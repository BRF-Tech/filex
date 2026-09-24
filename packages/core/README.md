<img src="https://raw.githubusercontent.com/BRF-Tech/filex/main/docs/logo.png" alt="filex logo" width="72">

# @brftech/filex-core

Vue 3 source of truth for the **filex** file manager. Ships the
`<FileExplorer>` SFC, the `<ConnectionsPanel>` surface, the composables that
drive them, and the type definitions consumers (Vue apps, the `@brftech/filex`
Web Component wrapper, the `@brftech/filex-react` adapter) build against.

> Looking for a drop-in `<filex-explorer>` HTML tag? Use
> [`@brftech/filex`](https://www.npmjs.com/package/@brftech/filex).
> React idiom? Use
> [`@brftech/filex-react`](https://www.npmjs.com/package/@brftech/filex-react).

## Install

```bash
npm i @brftech/filex-core vue
```

`vue ^3.4` is a peer dependency. The following are *optional* peers —
features degrade gracefully if missing:

| Peer | Used for |
|---|---|
| `monaco-editor` | Code edit/view (top-tier IDE-grade) |
| `highlight.js` | Read-only code colour fallback while Monaco loads, or as the permanent renderer when Monaco isn't installed |
| `markdown-it` | Markdown preview |
| `codemirror` + `@codemirror/lang-*` | Lighter-weight editor alternative |

## Use

```vue
<script setup lang="ts">
import { FileExplorer } from '@brftech/filex-core';
import '@brftech/filex-core/style.css';

const config = {
  // Modern API (RESTful):
  apiBase: 'https://files.example.com',
  auth: { kind: 'bearer', token: '<jwt>' },

  // Or legacy Vuefinder-compat:
  // endpoint: '/api/files/manager',
  // uploadInit: '/api/files/upload/init',
  // …

  locale: 'tr',
  theme: 'auto',
  trashVisible: true,
  sideNav: true,          // the navigation panel (default on)
  connections: true,      // its "How to connect" + "API keys" entries
  uiProfile: 'standard',  // 'simple' — one pane, list/grid, no tabs
};
</script>

<template>
  <FileExplorer
    :config="config"
    @error="(e) => console.error(e)"
    @file-opened="(f) => console.log('opened', f)"
    @share-created="(s) => navigator.clipboard.writeText(s.url)"
  />
</template>
```

## Auth

```ts
type AuthConfig =
  | { kind: 'bearer'; token: string | (() => string | Promise<string>) }
  | { kind: 'csrf'; csrf: string }
  | { kind: 'basic'; user: string; pass: string }
  | { kind: 'none' };
```

Function-token bearers are awaited on every request so silent JWT
refresh just works.

## API surface

```ts
import {
  FileExplorer,
  useFileApi, useUploadChunked, useSelection, useKeyboardShortcuts,
  useLocale, usePendingOps, useMonacoLoader,
  preloadEditor, ensureMonaco,
  // naming a key on screen — read the binding, never type it out:
  // shortcuts are remappable, so a hardcoded "Ctrl+K" stops being true
  shortcutHint, eventMatchesShortcut,
  // how far a queued copy/move/delete has got — bytes when a transfer between
  // two storages reports them, `null` when there is no honest percentage
  opPercent,
  // types
  type ExplorerConfig, type AuthConfig, type FileNode, type ShareInfo,
  type Capabilities,
} from '@brftech/filex-core';
```

The composables are stable — feel free to compose your own UI without
touching the SFC.

If your own UI names a keyboard shortcut, render `shortcutHint('<action>')`
rather than the key itself: the user may remap any action from the shortcut
settings, and `''` comes back for one they unbound so you can drop the hint
instead of drawing an empty key cap. See
[docs/API.md](https://github.com/BRF-Tech/filex/blob/main/docs/API.md#naming-a-key-on-screen).

If you draw your own progress for queued operations (`usePendingOps`), take the
percentage from `opPercent(op)` rather than `done / total`: those count the
selected items, so moving one large file reads `0 / 1` until it ends. A `null`
means draw a moving indicator, not 0%.

### Navigation panel

The explorer ships a left navigation panel — the primary **+ New** menu
(upload files · new folder · new document · request files), the destinations
**Home · Shared with me · My shares · Recent · Starred · Trash** (plus **My
files** when the caller reaches at most one storage), your tags in two groups
(**Personal** and **Team**), an **Apps** section with one row per installed
app's own page, and the storages the
caller can see (a storage reached through a grant is marked *Shared*). It is on
by default on every surface; the viewer collapses it to an icon rail from the
control at the far left of the **top bar** — above the panel rather than inside
it, so it is still reachable once the panel is a rail — and that choice is
remembered per browser. Under 560px it becomes a drawer over the listing instead
of a column.

```ts
const config = {
  apiBase: 'https://files.example.com',
  auth: { kind: 'bearer', token },
  sideNav: true,          // default; `rootPath` flips it off
  uiProfile: 'simple',    // 'standard' (default) | 'simple'
  mySharesVisible: true,  // draw the "My shares" row — only if you handle @open-my-shares
  appHomePage: true,      // an app's home view opens as YOUR page — handle @open-app-home
};
```

⚠ `mySharesVisible` and `appHomePage` are **off by default**, and not because
the surfaces are optional: each needs the host to take an event and open a
page of its own — `@open-my-shares` for the links this person made, and
`@open-app-home` for an app's `home` view (`{base}app/{plugin}/{view}`, with
the open section in `?section=`). Only this Vue component emits them; the web
component and the React adapter do not forward them yet, so switching the keys
on there would draw a row that goes nowhere.

The panel's last section is how **How to connect** (the per-protocol guides,
built from your deployment) and **API keys** (mint and revoke the tokens
WebDAV, FTPS and `filex mount` sign in with) become reachable from inside the
explorer at all — `ConnectionsPanel` and `TokensPanel` were exported from this
package long before anything opened them, so an embedded explorer's users had
to be told to ask an administrator. Set `connections: false` to leave them out.
⚠ Never gated on role in the UI: `/api/tokens` caps every scope against the
caller's own account, and the panel renders what the API returns.

⚠ **API keys is dropped for an app token** — along with Recent, Starred and
Shared with me — because those surfaces belong to one person and an app token
belongs to none. `ConnectionsPanel` degrades with it: the guides stay, and
`S3KeysPanel` / `SSHKeysPanel` / `NFSExportsPanel` / `TokensPanel` show their
existing "cannot mint" note instead of a form, driven by the server's 403
through the `canMint` / `canAdd` flag each composable already reports. That is `callerKind`, read from
`GET /api/files/capabilities` (`caller_kind`) and overridable per embed; it is a
credential-kind check, not the role check the paragraph above forbids. "How to
connect", Upload, the storages and Trash stay. See
[docs/MCP.md → Token kinds](https://github.com/BRF-Tech/filex/blob/main/docs/MCP.md#token-kinds--user-vs-app).

`uiProfile: 'simple'` is a preset, not a feature switch — nothing is removed
from the build. It turns off the tab strip and the split pane, reduces the view
switcher to list + grid, and defaults the panel's "How to connect" / "API keys"
entries off, for the people who want a file drive rather than a file manager.
It does not gate the navigation panel: that ships in every profile, and only
the viewer's own collapse choice moves it.

There are **two profiles and no third**. ⚠ Anything else that reaches
`uiProfile` — a typo, or the `'drive'` profile that was **removed** after
v0.40.0 — resolves to `'standard'` and logs one console line naming it. **If
you were passing `'drive'`, pass `'simple'`.** It was only ever `simple` plus a
look, and the look below is now what *every* embed draws, with no string
passed:

- one primary **+ New** menu in the panel (upload files · new folder · new
  document · request files) instead of the Upload / New folder pair,
- one **search field across the header** with a ⌘K / Ctrl+K chip that hands the
  query to the command palette — the field searches the folder you are in, the
  palette is where "everywhere", saved searches and commands live,
- a **filter row** under the breadcrumb: Type · People · Modified · Size,
- **Folders** and **Files** as labelled sections in grid view — replaced by
  **date headings** (Today · Yesterday · This Week · This Month · *September
  2026*) in all three views while the listing is sorted by Modified,
- the details panel split into **Details** and **Activity**, with "People with
  access" and a share-link row,
- a **storage line** under the navigation (`GET /api/files/quota/me`).

The **People** chip and the **Owner** column are real now: migration `00038`
put the owner on the node itself, so a listing row carries one, the chip offers
only the people who actually own something in the rows on screen, and quota is
counted against the owner rather than whoever last touched the file.

### Connections

A second surface, for reaching the same server *without* a browser. filex can
be spoken to as **S3**, **SFTP**, **FTPS**, **NFSv3** and **WebDAV**, and
mounted with `filex mount`; `<ConnectionsPanel>` is where a user manages
storages, mints the credential each protocol takes, and reads instructions
built from *this* deployment — its host, its port, their login — rather than a
template with angle brackets in it.

```ts
import {
  ConnectionsPanel,          // the whole surface: storages + guides
  S3KeysPanel,               // or mount the pieces yourself
  SSHKeysPanel,
  NFSExportsPanel,
  TokensPanel,               // FTPS / WebDAV / filex mount sign in with a token
  ConnectionGuideView,
  buildGuide, guideProtocols,
  useS3Keys, useSSHKeys, useNFSExports, useTokens,
  type ProtocolGuide, type ApiToken,
} from '@brftech/filex-core';
```

⚠ Mount the panel, not a copy of it. The admin panel, the web explorer and the
filex desktop app all render **this** component — a surface that mints
credentials one of them cannot see or revoke is the failure mode the shared
package exists to prevent.

See [docs/PROTOCOLS.md](https://github.com/BRF-Tech/filex/blob/main/docs/PROTOCOLS.md).

### Live updates

An embedded explorer keeps itself current over a WebSocket: it mints a
short-lived ticket, opens the socket and re-lists a folder when something in it
changes. Two things are worth knowing before you host it.

**Proxy `/api/ws`.** If your page reaches filex through your own backend, the
ticket route is under `/api/files/` and the socket is **not** — a proxy rule
that only forwards `/api/files/*` leaves the explorer with no socket, and it
falls back to re-listing every 12 s. It keeps working, quietly, which is why
this is easy to ship without noticing.

**A burst is one frame per window, not one frame per file.** The server sends
the first change in a quiet folder immediately and merges everything after it
into one frame per window (200 ms, stretching to 1.5 s while the burst
continues); a merged frame carries `count`. Nothing is dropped — the last frame
of a burst always reflects the final state. If you debounce on your side as
well, give your debounce a **ceiling**: a plain trailing debounce starves under
a sustained stream, because every arriving frame cancels the pending reload.
The package's own `burstDebounce` does exactly that.

Full contract: [docs/REALTIME.md](https://github.com/BRF-Tech/filex/blob/main/docs/REALTIME.md).

## Build

```bash
pnpm build       # vue-tsc + vite lib build → dist/
pnpm typecheck
```

Output:

- `dist/filex-core.js` (ESM)
- `dist/filex-core.umd.cjs` (UMD)
- `dist/style.css`
- `dist/index.d.ts` (rolled-up declarations)

## License

MIT
