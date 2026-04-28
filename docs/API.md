# Component API

Three published packages, all built from one Vue 3 source of truth.

| Package                      | Use case                | Tech            |
|------------------------------|-------------------------|-----------------|
| `@brftech/filex-core`        | Vue 3 apps              | SFC + composables |
| `@brftech/filex`             | Any framework / vanilla | Web Component (`<filex-explorer>`) |
| `@brftech/filex-react`       | React apps              | `@lit/react` adapter |

All three accept the same logical configuration; only the syntax to pass it
differs.

- [`<filex-explorer>` (Web Component)](#filex-explorer-web-component)
- [`<FileExplorer>` (Vue 3)](#fileexplorer-vue-3)
- [`<FileManager>` (React)](#filemanager-react)
- [Shared TypeScript types](#shared-typescript-types)

---

## `<filex-explorer>` (Web Component)

Tag: `<filex-explorer>` (kebab; the package registers it on import).

```html
<script type="module" src="https://cdn.jsdelivr.net/npm/@brftech/filex/dist/filex.js"></script>

<filex-explorer
  api-base="https://files.example.com"
  locale="en"
  theme="auto"
  start-path="/storage1"
></filex-explorer>
```

For complex config, set the `config` property in JS instead of attributes:

```html
<filex-explorer id="fx"></filex-explorer>
<script type="module">
  const fx = document.getElementById('fx');
  fx.config = {
    apiBase: 'https://files.example.com',
    auth: { kind: 'bearer', token: localStorage.getItem('filex_token') },
    startPath: '/projects',
    locale: 'tr',
    onError: (e) => console.error(e),
  };
</script>
```

### Attributes (string-only, simple cases)

| Attribute      | Type   | Default | Notes |
|----------------|--------|---------|-------|
| `api-base`     | string | (required) | full base URL of filex backend |
| `locale`       | string | `auto`     | `tr \| en \| auto` |
| `theme`        | string | `auto`     | `light \| dark \| auto` |
| `start-path`   | string | `/`        | initial path on mount |
| `view`         | string | `list`     | `list \| grid` |
| `readonly`     | bool   | `false`    | disable all write actions |
| `embed-mode`   | string | `panel`    | `panel \| modal \| fullscreen` |

### Properties (object-friendly)

| Property | Type             | Description |
|----------|------------------|-------------|
| `config` | `ExplorerConfig` | full config object; takes precedence over attributes |

### Events (CustomEvent on the element)

| Event              | `detail` shape                                | Fires on |
|--------------------|-----------------------------------------------|----------|
| `filex-ready`      | `{ apiBase, capabilities }`                   | Component finished bootstrapping |
| `filex-navigate`   | `{ path }`                                    | Path changed |
| `filex-select`     | `{ items: FileNode[] }`                       | Selection changed |
| `filex-error`      | `{ code, message, details? }`                 | Any non-recoverable error |
| `filex-upload-progress` | `{ uploadId, loaded, total }`            | Multipart upload progress |
| `filex-upload-done` | `{ uploadId, file: FileNode }`               | Upload complete |
| `filex-share-created` | `{ share: ShareInfo }`                     | New share link created |
| `filex-action`     | `{ name, payload }`                           | User clicked a custom toolbar action |

```js
fx.addEventListener('filex-error', (e) => console.error(e.detail));
fx.addEventListener('filex-share-created', (e) => navigator.clipboard.writeText(e.detail.share.url));
```

### Methods

```ts
fx.refresh()                       // re-fetch current dir
fx.navigate(path: string)          // programmatic nav
fx.select(paths: string[])         // programmatic select
fx.getSelection(): FileNode[]
```

---

## `<FileExplorer>` (Vue 3)

```vue
<script setup lang="ts">
import { FileExplorer } from '@brftech/filex-core';
import '@brftech/filex-core/style.css';
import type { ExplorerConfig, FileNode, ShareInfo } from '@brftech/filex-core';

const config: ExplorerConfig = {
  apiBase: 'https://files.example.com',
  auth: { kind: 'bearer', token: 'eyJ...' },
  startPath: '/storage1',
  locale: 'tr',
  theme: 'auto',
};

function onError(e: { code: string; message: string }) {
  console.error('filex error', e);
}
</script>

<template>
  <FileExplorer
    :config="config"
    :readonly="false"
    @ready="onReady"
    @error="onError"
    @select="onSelect"
    @navigate="onNavigate"
    @upload-progress="onProgress"
    @share-created="onShare"
  >
    <template #toolbar-extra="{ selection }">
      <button v-if="selection.length === 1" @click="convertToPdf(selection[0])">
        Convert to PDF
      </button>
    </template>
  </FileExplorer>
</template>
```

### Props

| Prop        | Type              | Default | Notes |
|-------------|-------------------|---------|-------|
| `config`    | `ExplorerConfig`  | (required) | the only required prop |
| `readonly`  | `boolean`         | `false`    | disable writes |
| `view`      | `'list' \| 'grid'`| `'list'`   | initial view |
| `selection` | `FileNode[]`      | `[]`       | controlled selection (v-model:selection) |
| `path`      | `string`          | start-path | controlled current path (v-model:path) |

### Emits

| Event              | Payload                                       |
|--------------------|-----------------------------------------------|
| `ready`            | `{ apiBase: string; capabilities: Capabilities }` |
| `navigate`         | `{ path: string }`                            |
| `select`           | `{ items: FileNode[] }`                       |
| `error`            | `{ code: string; message: string; details?: unknown }` |
| `upload-progress`  | `{ uploadId: string; loaded: number; total: number }` |
| `upload-done`      | `{ uploadId: string; file: FileNode }`        |
| `share-created`    | `{ share: ShareInfo }`                        |
| `action`           | `{ name: string; payload: unknown }`          |

### Slots

| Slot              | Slot props                          | Use |
|-------------------|-------------------------------------|-----|
| `toolbar-extra`   | `{ selection: FileNode[] }`         | Append custom buttons to the toolbar |
| `breadcrumb-extra`| `{ path: string }`                  | Right side of breadcrumb |
| `empty`           | `{ path: string }`                  | Override empty-folder placeholder |
| `preview-extra`   | `{ file: FileNode }`                | Right pane addition in preview modal |

### Composables (advanced)

```ts
import {
  useFileApi,
  useUploadChunked,
  useSelection,
  useKeyboardShortcuts,
  useLocale,
} from '@brftech/filex-core';

const { list, move, copy, mkdir, rename, remove, search } = useFileApi(config);
const { upload, abort, progress } = useUploadChunked(config);
const { selected, toggle, clear } = useSelection();
const { t, locale } = useLocale('tr');
useKeyboardShortcuts({ Delete: () => remove(selected.value.map(x => x.path)) });
```

---

## `<FileManager>` (React)

Implemented as a `@lit/react` wrapper around the Web Component, so behaviour is
identical to `<filex-explorer>` but with idiomatic React props.

```bash
pnpm add @brftech/filex-react
```

```tsx
import { FileManager } from '@brftech/filex-react';
import type { ExplorerConfig, FileNode } from '@brftech/filex-react';

export function MyFiles() {
  const config: ExplorerConfig = {
    apiBase: 'https://files.example.com',
    auth: { kind: 'cookie' },
    locale: 'en',
  };

  return (
    <FileManager
      config={config}
      readonly={false}
      onError={(e) => console.error(e)}
      onSelect={(items: FileNode[]) => console.log('selection:', items)}
      onNavigate={({ path }) => console.log('moved to:', path)}
      onShareCreated={({ share }) => navigator.clipboard.writeText(share.url)}
    />
  );
}
```

### Props

| Prop                | Type                              | Notes |
|---------------------|-----------------------------------|-------|
| `config`            | `ExplorerConfig`                  | required |
| `readonly`          | `boolean`                         | default `false` |
| `view`              | `'list' \| 'grid'`                | default `'list'` |
| `className`         | `string`                          | passed to the root element |
| `style`             | `React.CSSProperties`             | inline styles |
| `onReady`           | `(e: ReadyEvent) => void`         | Bootstrapped |
| `onNavigate`        | `(e: NavigateEvent) => void`      | Path changed |
| `onSelect`          | `(items: FileNode[]) => void`     | Selection changed |
| `onError`           | `(e: ApiError) => void`           | Any error |
| `onUploadProgress`  | `(e: UploadProgressEvent) => void`| Chunked upload progress |
| `onUploadDone`      | `(e: UploadDoneEvent) => void`    | Upload complete |
| `onShareCreated`    | `(e: ShareCreatedEvent) => void`  | Share link created |
| `onAction`          | `(e: ActionEvent) => void`        | Toolbar custom action |

### Imperative ref

```tsx
import { useRef } from 'react';
import { FileManager, type FileManagerHandle } from '@brftech/filex-react';

const ref = useRef<FileManagerHandle>(null);
// ref.current?.refresh()
// ref.current?.navigate('/projects')
// ref.current?.getSelection()
```

---

## Shared TypeScript types

Exported from every package (`@brftech/filex-core`, `@brftech/filex`,
`@brftech/filex-react`).

```ts
export interface ExplorerConfig {
  /** Backend base URL, e.g. https://files.example.com (no trailing slash). */
  apiBase: string;

  /** Auth scheme. */
  auth?:
    | { kind: 'cookie' }                              // default — relies on session cookie
    | { kind: 'bearer'; token: string }               // header auth
    | { kind: 'apikey'; header: string; value: string };

  /** Initial path. */
  startPath?: string;

  /** UI locale: 'tr' | 'en' | 'auto'. Default 'auto' (browser). */
  locale?: 'tr' | 'en' | 'auto';

  /** Light/dark/auto. */
  theme?: 'light' | 'dark' | 'auto';

  /** Hide all write controls. */
  readonly?: boolean;

  /** Initial view. */
  view?: 'list' | 'grid';

  /**
   * Where to render the explorer.
   * - 'panel'      : inline (default)
   * - 'modal'      : self-mounting modal
   * - 'fullscreen' : occupy 100vw/100vh
   */
  embedMode?: 'panel' | 'modal' | 'fullscreen';

  /** Optional callback list (also exposed as events). */
  onError?: (e: ApiError) => void;
  onReady?: (e: ReadyEvent) => void;
}

export interface FileNode {
  id: number;
  /** Absolute path including storage root, e.g. /storage1/sub/a.txt */
  path: string;
  name: string;
  type: 'file' | 'dir';
  size: number;
  /** ISO8601 */
  modified: string;
  mime?: string;
  etag?: string;
  isImage?: boolean;
  isVideo?: boolean;
  /** Pre-signed thumbnail URL (HMAC token included). */
  thumbUrl?: string;
}

export interface ShareInfo {
  id: number;
  url: string;
  token: string;
  path: string;
  expiresAt: string;
  maxDownloads: number;
  downloads: number;
}

export interface Capabilities {
  version: string;
  thumbs: { enabled: boolean; image: boolean; video: boolean; pdf: boolean; office: boolean };
  external: { onlyoffice_url: string; drawio_url: string; mermaid: boolean };
  auth: { drivers: string[]; allow_signup: boolean };
  limits: { max_upload_bytes: number; max_archive_bytes: number };
}

export interface ApiError {
  code: string;
  message: string;
  details?: unknown;
}

/** Event payloads (used by both Web Component CustomEvents and Vue/React handlers). */
export interface ReadyEvent { apiBase: string; capabilities: Capabilities }
export interface NavigateEvent { path: string }
export interface UploadProgressEvent { uploadId: string; loaded: number; total: number }
export interface UploadDoneEvent { uploadId: string; file: FileNode }
export interface ShareCreatedEvent { share: ShareInfo }
export interface ActionEvent { name: string; payload: unknown }
```

### Auth examples

```ts
// 1. Same-origin / behind reverse-proxy → session cookie (default)
{ apiBase: '/files', auth: { kind: 'cookie' } }

// 2. Cross-origin SPA with JWT
{ apiBase: 'https://files.example.com', auth: { kind: 'bearer', token } }

// 3. Service-to-service / kiosk
{ apiBase: 'https://files.example.com', auth: { kind: 'apikey', header: 'X-API-Key', value: '...' } }
```
