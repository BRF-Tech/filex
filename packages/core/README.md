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
  uiProfile: 'standard',  // or 'simple' — one pane, list/grid, no tabs
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
  // types
  type ExplorerConfig, type AuthConfig, type FileNode, type ShareInfo,
  type Capabilities,
} from '@brftech/filex-core';
```

The composables are stable — feel free to compose your own UI without
touching the SFC.

### Navigation panel

The explorer ships a left navigation panel — a prominent **Upload**, the views
**Recent · Starred · Shared with me · Trash**, and the storages the caller can
see (a storage reached through a grant is marked *Shared*). It is on by default
on every surface; the viewer collapses it to an icon rail and that choice is
remembered per browser. Under 560px it becomes a drawer over the listing instead
of a column.

```ts
const config = {
  apiBase: 'https://files.example.com',
  auth: { kind: 'bearer', token },
  sideNav: true,          // default; `rootPath` flips it off
  uiProfile: 'simple',    // 'standard' (default) | 'simple'
};
```

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
switcher to list + grid, and starts the navigation panel expanded, for the
people who want a file drive rather than a file manager.

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
  buildGuide, guideProtocols, guideName,
  useS3Keys, useSSHKeys, useNFSExports, useTokens,
  type ProtocolGuide, type ApiToken,
} from '@brftech/filex-core';
```

⚠ Mount the panel, not a copy of it. The admin panel, the web explorer and the
filex desktop app all render **this** component — a surface that mints
credentials one of them cannot see or revoke is the failure mode the shared
package exists to prevent.

See [docs/PROTOCOLS.md](https://github.com/BRF-Tech/filex/blob/main/docs/PROTOCOLS.md).

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
