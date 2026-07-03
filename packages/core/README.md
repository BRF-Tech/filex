# @brftech/filex-core

Vue 3 source of truth for the **filex** file manager. Ships a single
`<FileExplorer>` SFC, the composables that drive it, and the type
definitions consumers (Vue apps, the `@brftech/filex` Web Component
wrapper, the `@brftech/filex-react` adapter) build against.

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
