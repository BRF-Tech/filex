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

> ⚠ **`@brftech/filex-core` 0.54 needs a filex 0.54 server.** Which files open for editing
> (`edit_kinds`), the input limits (`limits`) and the version line
> (`release`, `commit`, `built`) come from the server's
> `/api/files/capabilities`; the package keeps no list of its own to fall
> back on, so against an older server nothing is offered Edit and nothing
> opens as an office document. Update the server with the package
> ([API.md](https://github.com/BRF-Tech/filex/blob/main/docs/API.md)).

`vue ^3.4` is a peer dependency. The following are *optional* peers -
features degrade gracefully if missing:

| Peer | Used for |
|---|---|
| `monaco-editor` | Code edit/view (top-tier IDE-grade) |
| `highlight.js` | Read-only code colour fallback while Monaco loads, or as the permanent renderer when Monaco isn't installed |
| `markdown-it` | Markdown preview |
| `codemirror` + `@codemirror/lang-*` | Lighter-weight editor alternative |

Every preview that draws a file as HTML (Markdown, notebooks, highlighted
code, Mermaid, KaTeX) sanitizes it with
[DOMPurify](https://github.com/cure53/DOMPurify), a regular dependency of this
package (bundled unmodified; licensed Apache-2.0 OR MPL-2.0 at your choice).

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
  // uploadBegin: '/api/files/upload/begin',
  // …

  locale: 'tr',
  theme: 'auto',
  trashVisible: true,
  sideNav: true,          // the navigation panel (default on)
  connections: true,      // its "How to connect" + "API keys" entries
  uiProfile: 'standard',  // 'simple' - one pane, list/grid, no tabs
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

⚠ **A cookie session from another origin** (`csrf` or `none`, riding on
filex's own session cookie): since filex 0.50 a change the browser sends that
way is refused (`403 cross_origin_refused`) unless the host page's origin is in
the server's `FILEX_CORS_ALLOWED_ORIGINS` by name - `*` does not grant it, and
a sibling subdomain is another origin. A bearer token, or a host that proxies
with a key, needs nothing
([CONFIGURATION.md → Requests from other origins](https://github.com/BRF-Tech/filex/blob/main/docs/CONFIGURATION.md#requests-from-other-origins)).

**What the account may do.** Against a filex server with roles and per-user
permissions (0.49+), the explorer reads the signed-in account's permissions
from `GET /api/auth/me` itself and hides what its role refuses - Delete,
Rename, Share and the rest - so an embed hides exactly what the filex web app
hides, with nothing to wire. Pass `permissions` (and `permissionsByFolder`) in
the config only if your host already holds the answer, or `me` to point the
request elsewhere. An API token behind the embed is held to its own verbs as
well: `read` alone is a read-only explorer
([PERMISSIONS.md](https://github.com/BRF-Tech/filex/blob/main/docs/PERMISSIONS.md),
[RBAC.md → API tokens](https://github.com/BRF-Tech/filex/blob/main/docs/RBAC.md#api-tokens-verbs-on-every-surface)).

## API surface

```ts
import {
  FileExplorer,
  useFileApi, useUploadChunked, useSelection, useKeyboardShortcuts,
  useLocale, usePendingOps, useMonacoLoader,
  preloadEditor, ensureMonaco,
  // naming a key on screen - read the binding, never type it out:
  // shortcuts are remappable, so a hardcoded "Ctrl+K" stops being true
  shortcutHint, eventMatchesShortcut,
  // how far a queued copy/move/delete has got - bytes when a transfer between
  // two storages reports them, `null` when there is no honest percentage
  opPercent,
  // types
  type ExplorerConfig, type AuthConfig, type FileNode, type ShareInfo,
  type Capabilities,
} from '@brftech/filex-core';
```

The composables are stable - feel free to compose your own UI without
touching the SFC. The end-to-end encryption building blocks the explorer uses
(encrypted names, the STREAM format for large files, converting a folder,
a `.fxe`'s new password) are exported too
([E2E-ENCRYPTION.md → Using the building blocks](https://github.com/BRF-Tech/filex/blob/main/docs/E2E-ENCRYPTION.md#using-the-building-blocks)),
as are the draft helpers
([API.md → Composables](https://github.com/BRF-Tech/filex/blob/main/docs/API.md#composables-advanced))
and `markdownToSafeHtml`, the sanitised Markdown the explorer's preview draws
(nothing in it runs). Since 0.51 the admin panel's mega menu is exported as
`MegaMenu` with its helpers (`pruneMegaMenu`, `entryIsCurrent`, the
`MegaMenuEntry` types): the host passes the entries it lets the reader open
and does the navigating itself
([ADMIN-PANEL.md](https://github.com/BRF-Tech/filex/blob/main/docs/ADMIN-PANEL.md)).
The two choice controls every filex screen draws are exported for a host's own
UI as well: `ChoiceSelect` (one answer from a list, without the browser's
native dropdown; options are `SelectOption`) and `ChoiceButtons` (two to four
answers on screen; `segmented` draws them as one inline strip) - filex itself
draws no native dropdown
([CONTRIBUTING.md → No native dropdown](https://github.com/BRF-Tech/filex/blob/main/docs/CONTRIBUTING.md#no-native-dropdown---one-list-control-in-core)).

The panel's search is exported the same way, as `PanelSearch` with its rules
(`parsePanelQuery`, `panelSearchGroups`, the `PanelSearchItem` types) and
`foldText`, the one rule a typed word is compared with a name by: the host
passes what may be found and where the recent searches live, and gets the
chosen row back
([ADMIN-PANEL.md → Search](https://github.com/BRF-Tech/filex/blob/main/docs/ADMIN-PANEL.md#search)).

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

The explorer ships a left navigation panel - the primary **+ New** menu
(upload files · new folder · new document · request files), the destinations
**Home · Shared with me · My shares · Recent · Starred · Trash** (plus **My
files** when the caller reaches at most one storage), your tags in two groups
(**Personal** and **Team**), an **Apps** section with one row per installed
app's own page, and the storages the
caller can see (a storage reached through a grant is marked *Shared*). It is on
by default on every surface; the viewer collapses it to an icon rail from the
control at the far left of the **top bar** - above the panel rather than inside
it, so it is still reachable once the panel is a rail - and that choice is
remembered per browser. Under 560px it becomes a drawer over the listing instead
of a column.

```ts
const config = {
  apiBase: 'https://files.example.com',
  auth: { kind: 'bearer', token },
  sideNav: true,          // default; `rootPath` flips it off
  uiProfile: 'simple',    // 'standard' (default) | 'simple'
  mySharesVisible: true,  // draw the "My shares" row - only if you handle @open-my-shares
  appHomePage: true,      // an app's home view opens as YOUR page - handle @open-app-home
  appStorePage: true,     // you have the store screen - handle @open-app-store; the explorer asks the server who sees it
};
```

⚠ `mySharesVisible`, `appHomePage` and `appStorePage` are **off by
default**, and not because the surfaces are optional: each needs the host to
take an event and open a page of its own - `@open-my-shares` for the links
this person made, `@open-app-home` for an app's `home` view
(`{base}app/{plugin}/{view}`, with the open section in `?section=`), and
`@open-app-store` for the store screen. For the store the explorer also asks
the server (`GET /api/app-store`) and draws the row only for a person it is
shown to: the host says only that it has the page. `<filex-explorer>` and
`<FileManager>` forward `open-app-store` (the desktop app listens to it). Only this Vue component emits them; the web
component and the React adapter do not forward them yet, so switching the keys
on there would draw a row that goes nowhere.

The panel's last section is how **How to connect** (the per-protocol guides,
built from your deployment) and **API keys** (mint and revoke the tokens
WebDAV, FTPS and `filex mount` sign in with) become reachable from inside the
explorer at all - `ConnectionsPanel` and `TokensPanel` were exported from this
package long before anything opened them, so an embedded explorer's users had
to be told to ask an administrator. Set `connections: false` to leave them out.
⚠ Never gated on role in the UI: `/api/tokens` caps every scope against the
caller's own account, and the panel renders what the API returns.

⚠ **API keys is dropped for an app token** - along with Recent, Starred and
Shared with me - because those surfaces belong to one person and an app token
belongs to none. `ConnectionsPanel` degrades with it: the guides stay, and
`S3KeysPanel` / `SSHKeysPanel` / `NFSExportsPanel` / `TokensPanel` show their
existing "cannot mint" note instead of a form, driven by the server's 403
through the `canMint` / `canAdd` flag each composable already reports. That is `callerKind`, read from
`GET /api/files/capabilities` (`caller_kind`) and overridable per embed; it is a
credential-kind check, not the role check the paragraph above forbids. "How to
connect", Upload, the storages and Trash stay. See
[docs/MCP.md → Token kinds](https://github.com/BRF-Tech/filex/blob/main/docs/MCP.md#token-kinds---user-vs-app).

`uiProfile: 'simple'` is a preset, not a feature switch - nothing is removed
from the build. It turns off the tab strip and the split pane, reduces the view
switcher to list + grid, and defaults the panel's "How to connect" / "API keys"
entries off, for the people who want a file drive rather than a file manager.
It does not gate the navigation panel: that ships in every profile, and only
the viewer's own collapse choice moves it.

There are **two profiles and no third**. ⚠ Anything else that reaches
`uiProfile` - a typo, or the `'drive'` profile that was **removed** after
v0.40.0 - resolves to `'standard'` and logs one console line naming it. **If
you were passing `'drive'`, pass `'simple'`.** It was only ever `simple` plus a
look, and the look below is now what *every* embed draws, with no string
passed:

- one primary **+ New** menu in the panel (upload files · new folder · new
  document · request files) instead of the Upload / New folder pair,
- one **search field across the header** with a ⌘K / Ctrl+K chip that hands the
  query to the command palette - the field searches the folder you are in, the
  palette is where "everywhere", saved searches and commands live,
- a **filter row** under the breadcrumb: Type · Owner · Modified · Size,
- **Folders** and **Files** as labelled sections in grid view - replaced by
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
built from *this* deployment - its host, its port, their login - rather than a
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
filex desktop app all render **this** component - a surface that mints
credentials one of them cannot see or revoke is the failure mode the shared
package exists to prevent.

See [docs/PROTOCOLS.md](https://github.com/BRF-Tech/filex/blob/main/docs/PROTOCOLS.md).

### Live updates

An embedded explorer keeps itself current over a WebSocket: it mints a
short-lived ticket, opens the socket and re-lists a folder when something in it
changes. Two things are worth knowing before you host it.

**Proxy `/api/ws`.** If your page reaches filex through your own backend, the
ticket route is under `/api/files/` and the socket is **not** - a proxy rule
that only forwards `/api/files/*` leaves the explorer with no socket, and it
falls back to re-listing every 12 s. It keeps working, quietly, which is why
this is easy to ship without noticing.

**A burst is one frame per window, not one frame per file.** The server sends
the first change in a quiet folder immediately and merges everything after it
into one frame per window (200 ms, stretching to 1.5 s while the burst
continues); a merged frame carries `count`. Nothing is dropped - the last frame
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

- `dist/filex-core.js` (ESM) and the chunks beside it (`dist/<name>-<hash>.js`)
  that it imports: the explorer, and the parts it loads when it needs them -
  the viewers, the vault, and the dialogs a person opens (the viewer, sharing,
  settings, search, the encryption dialogs; `lazySurfaces.ts`). They are
  imported relative to `filex-core.js`, so `dist/` is served or bundled whole.
- `dist/filex-core.umd.cjs` (UMD, one file: every chunk inlined)
- `dist/style.css`
- `dist/index.d.ts` (rolled-up declarations)

## License

MIT
