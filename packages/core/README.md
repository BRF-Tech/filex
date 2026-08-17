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
