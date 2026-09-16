# Component API

Three published packages, all built from one Vue 3 source of truth.

| Package                      | Use case                | Tech            |
|------------------------------|-------------------------|-----------------|
| `@brftech/filex-core`        | Vue 3 apps              | SFC + composables |
| `@brftech/filex`             | Any framework / vanilla | Web Component (`<filex-explorer>`) |
| `@brftech/filex-react`       | React apps              | `@lit/react` adapter |

All three take the **same `ExplorerConfig` object**; only the syntax to hand it
over differs. This page is the reference — every attribute, event, slot, export
and type, as the code defines them. [INTEGRATION.md](INTEGRATION.md) is the
guide: what to set, and why.

⚠ **Stylesheets differ by package, and only here.** The Vue package ships
`style.css` and you import it. The web-component and React packages have
**nothing to import** — the sheet travels inside the bundle and is appended to
`<head>` once, the first time an element mounts. (`@brftech/filex` also
publishes `dist/style.css` for a host that would rather serve the sheet
itself.)

- [`<filex-explorer>` (Web Component)](#filex-explorer-web-component)
- [`<FileExplorer>` (Vue 3)](#fileexplorer-vue-3)
- [`<FileManager>` (React)](#filemanager-react)
- [Shared TypeScript types](#shared-typescript-types)
- [The HTTP surface the component calls](#the-http-surface-the-component-calls)

---

## `<filex-explorer>` (Web Component)

Tag: `<filex-explorer>` (kebab; the package registers it on import). The
connections surface is a second element, `<filex-connections>`, registered by
the same import.

```html
<script type="module" src="https://cdn.jsdelivr.net/npm/@brftech/filex/dist/filex.js"></script>

<filex-explorer
  api-base="https://files.example.com"
  locale="en"
  theme="auto"
  sidenav
  ui-profile="simple"
></filex-explorer>
```

For anything an attribute cannot carry — auth, `brand`, `storages`, per-route
overrides — set the `config` **property** in JS:

```html
<filex-explorer id="fx"></filex-explorer>
<script type="module">
  const fx = document.getElementById('fx');
  fx.config = {
    apiBase: 'https://files.example.com',
    auth: { kind: 'bearer', token: localStorage.getItem('filex_token') },
    initialPath: 'main://projects',
    locale: 'tr',
  };
  await import('@brftech/filex');   // side effect: registers the element
</script>
```

⚠⚠ **Assign `config` BEFORE the import that registers the element**, as above.
Registering upgrades and mounts the element, and the explorer loads its first
folder on mount; a config assigned afterwards misses that one request, which
then goes out unauthenticated against the default adapter. The element renders
perfectly and the file list says "Could not load this folder", which sends
everybody looking at the backend.

### Attributes (string-only, simple cases)

Only these eight map to config keys. Everything else lives on the `config`
property.

| Attribute      | Type   | Config key | Notes |
|----------------|--------|------------|-------|
| `api-base`     | string | `apiBase`  | base URL of the filex backend; required unless `endpoint` is set |
| `endpoint`     | string | `endpoint` | legacy explicit manager URL, for hosts with their own routes |
| `locale`       | string | `locale`   | `tr \| en`. Unset ⇒ the browser's language, falling back to `en` |
| `theme`        | string | `theme`    | `light \| dark \| auto` (default `auto`) — the **host's** mode, used while the viewer has not pinned one of their own |
| `trash-visible`| bool   | `trashVisible` | show the Trash entry |
| `sidenav`      | bool   | `sideNav`  | the navigation panel. Absent leaves the core default (on) alone |
| `connections`  | bool   | `connections` | the panel's "How to connect" + "API keys" entries |
| `ui-profile`   | string | `uiProfile` | `standard \| simple`. Resolved by the core's own rule, so an unrecognised value becomes `standard` and says so once in the console |

Boolean attributes follow the DOM convention: present (or `="true"`) is true,
`="false"` is false, **absent leaves the core default alone** — writing
`sidenav="false"` is not the same as omitting it.

### Properties

| Property | Type             | Description |
|----------|------------------|-------------|
| `config` | `ExplorerConfig` | the full config object. Merged over the attributes, so the property wins on any key it carries |

### Events (CustomEvent on the element)

| Event              | Payload — `e.detail[0]`                                     | Fires on |
|--------------------|-------------------------------------------------------------|----------|
| `error`            | `{ message, context? }`                                      | Any error the explorer surfaces |
| `file-opened`      | `{ path, basename }`                                         | A file was opened |
| `share-created`    | `{ path, url, pin }`                                         | A share link was minted (`pin` is `null` when there is none) |
| `upload-progress`  | `{ uploadId, percent, done }`                                | Upload progress |
| `selection-change` | `Array<{ path, basename, type }>`                            | Selection changed |

Names are plain, **not** prefixed — Vue dispatches exactly what the wrapper
emits.

⚠⚠ **`e.detail` is an array, and the payload is its first element.** Vue's
`defineCustomElement` dispatches every emit as
`new CustomEvent(name, { detail: args })`, where `args` is the emit's
argument list — so `e.detail.url` is `undefined` and `e.detail[0].url` is the
link. `selection-change` emits an array, so its selection is `e.detail[0]`
(and `e.detail.length` is always `1`). Measured with the bundle in a real
browser: an `error` arrives as `detail: [{ message, context }]`.

```js
fx.addEventListener('error', (e) => console.error(e.detail[0].message));
fx.addEventListener('share-created', (e) => navigator.clipboard.writeText(e.detail[0].url));
fx.addEventListener('selection-change', (e) => console.log(e.detail[0].length, 'selected'));
```

⚠ The SFC's `navigate` and `refresh` emits are not forwarded through the custom
element. A host that needs them mounts the Vue SFC.

⚠ **The element exposes no imperative methods.** Drive it by reassigning
`config` (it is watched deeply, so a new `initialPath` re-navigates).

⚠⚠ **A host cannot fill a `<slot>` in `<filex-explorer>`**, and no version of
this package will change that. Vue projects light DOM into a custom element
only through a native `<slot>` inside a shadow root, and this element
deliberately has none — its whole look is one global stylesheet, so a shadow
root would leave every embed unstyled. Measured, 2026-09-13, with Vue's own
`defineCustomElement`: a `<span slot="brand">` inside the element leaves
`Object.keys(slots)` **empty** in the element's `setup`, with slot forwarding
and without it. Use `config.brand` (`{ name, markUrl }`) for the product mark.
Our own desktop app is in this position too — it mounts the web component.

### `<filex-connections>`

The storage-connection surface as an element, so a host with no bundler mounts
the same component the admin SPA imports as an SFC.

| Property / attribute | Notes |
|---|---|
| `config` | same `ExplorerConfig`; **configure through this**, not the attributes |
| `initial-tab` | `storages \| connect` — which half to open on |
| `closable` | render the close affordance |

Events: `changed`, `close`, `error`.

⚠ Set `el.config = { ...el.config, locale: 'tr' }`. Setting `el.locale = 'tr'`
changes a property nothing renders from — the merge is `{...attributes,
...config}` and the config object wins, so an attribute is only ever a fallback
for a key the config does not carry. That exact mistake shipped in v0.19.0: the
shell went Turkish while the file list stayed English, and the element reported
`locale === 'tr'` the whole time.

---

## `<FileExplorer>` (Vue 3)

```vue
<script setup lang="ts">
import { FileExplorer } from '@brftech/filex-core';
import '@brftech/filex-core/style.css';
import type { ExplorerConfig, FileNode } from '@brftech/filex-core';

const config: ExplorerConfig = {
  apiBase: 'https://files.example.com',
  auth: { kind: 'bearer', token: 'eyJ...' },
  initialPath: 'main://storage1',
  locale: 'tr',
  theme: 'auto',
  brand: { name: 'Acme Files', markUrl: '/logo.svg' },
};

function onError(e: { message: string; context?: unknown }) {
  console.error('filex error', e);
}
</script>

<template>
  <FileExplorer :config="config" @error="onError" @file-opened="onOpen">
    <template #brand><AcmeLogo /></template>
  </FileExplorer>
</template>
```

### Props

| Prop     | Type              | Notes |
|----------|-------------------|-------|
| `config` | `ExplorerConfig`  | **the only prop.** Everything the explorer can be told is a key on it |

### Emits

| Event              | Payload                                                        |
|--------------------|----------------------------------------------------------------|
| `error`            | `{ message: string; context?: unknown }`                       |
| `file-opened`      | `{ path: string; basename: string }`                           |
| `share-created`    | `{ path: string; url: string; pin: string \| null }`           |
| `upload-progress`  | `{ uploadId: string; percent: number; done: boolean }`         |
| `selection-change` | `Array<{ path: string; basename: string; type: 'file' \| 'dir' }>` |
| `navigate`         | `{ path: string }` — the viewed folder changed |
| `refresh`          | *(none)* — the viewer asked for a refresh |

`refresh` is a **notification, not a request**: the explorer reloads the listing
itself and does not wait for the host. It exists for the half it cannot know
about — `config.storages` is the host's answer to "which drives may I show
you", computed before mount, so a drive added elsewhere stayed invisible until
the whole page was reloaded. An embedder with a fixed storage list ignores it.

### Slots

| Slot             | Use |
|------------------|-----|
| `brand`          | the product mark at the far left of the top bar. Wins over `config.brand` when filled |
| `header-actions` | extra controls in the header cluster |

⚠ These are reachable **only** when you mount the SFC. See the web-component
section above for why, and use `config.brand` everywhere else.

### Exposed

```ts
const fx = ref<InstanceType<typeof FileExplorer>>();
fx.value?.reload();   // re-fetch the current listing
```

### Composables (advanced)

Real signatures — each takes what it needs rather than reaching for a global:

```ts
import {
  useFileApi,
  useUploadChunked,
  useSelection,
  useKeyboardShortcuts,
  useLocale,
} from '@brftech/filex-core';

// The backend wrapper. `index` lists, `newFolder` creates, `deleteItems`
// removes — the names follow the manager verbs, not the POSIX ones.
const api = useFileApi(config);
await api.index('main://projects');
await api.newFile('main://projects', 'Q3 report', 'docx');

// Uploads need the api instance: the staged protocol is several calls.
const { uploadFile, shouldChunk, threshold } = useUploadChunked(config, api);

// Selection is computed against the rows on screen, so it takes a getter.
const { selected, click, clear, selectAll, nodes } = useSelection(() => rows.value);

// Locale takes a Ref or a getter, never a bare string — it has to stay
// reactive when the host changes language.
const { t, formatSize } = useLocale(() => resolveLocale(config.locale));

// Shortcuts are bound to a root element so they stay inside the explorer, and
// the handler keys are `on…` names, not the combos (which are remappable).
useKeyboardShortcuts(rootEl, {
  onDelete: () => api.deleteItems(cwd.value, [...selected.value]),
});
```

`FileApi` is `ReturnType<typeof useFileApi>`; read `composables/useFileApi.ts`
for the full verb list (shares, versions, comments, permissions, archives, the
E2E escrow calls).

#### Naming a key on screen

Shortcuts are remappable (the user edits them in the shortcut settings; the
overrides live in `filex.shortcuts` in `localStorage`), so a hint that spells a
key out by hand is true only until somebody changes that key. Read the binding
instead:

```ts
import { shortcutHint, eventMatchesShortcut } from '@brftech/filex-core';

shortcutHint('palette');             // 'Ctrl+K' — or '⌘+K' on a Mac, or the
                                     // user's own combo, or '' when unbound
eventMatchesShortcut(ev, 'palette'); // true when THIS event fires that action
```

`shortcutHint` returns an empty string for an unbound action, so a caller can
drop the whole segment rather than draw an empty key cap. Action ids come from
`SHORTCUT_ACTIONS`.

### Components and helpers the package exports

A host that draws its own chrome should mount **these** rather than grow a
private copy — that is what keeps the admin app, the desktop app and every
embed one product.

| Export | What it is |
|---|---|
| `FileExplorer`, `PreviewModal`, `QuickLook` | the explorer, the viewer dispatch, the space-bar preview |
| `FilePane`, `TabBar` | one pane of the split view, and the tab strip |
| `NewDocumentModal` | the "+ New → document" picker. Exported because the entry belongs on every surface, not just the admin app |
| `DestinationPickerModal` + `destinationTree` helpers | the **one** folder chooser, spanning every storage. "Move to" and "Copy to" both mount it; its rules (`destinationRows`, `blockedReason`, `permAllowsWrite`, `isAtOrInside`, …) are pure functions so a host can reuse the decisions without the dialog |
| `downloadArchive`, `requestArchive`, `triggerFileNavigation`, `absoluteTicketUrl`, `archiveTicketUrl` | "download the selection as one archive" — the real two-step flow, for a host that draws its own selection bar |
| `ConnectionsPanel`, `StorageFields`, `TokensPanel`, `S3KeysPanel`, `SSHKeysPanel`, `NFSExportsPanel` | the connection surfaces, and the guide builders behind them |
| `ThemeGallery`, `ThemePalette`, `THEMES`, `setTheme`, `setThemeMode` | the palette gallery and the light/dark mode, for hosts whose appearance settings live in their own pane |
| `viewPrefs` (`attachViewPrefsStore`, `folderMemoryEnabled`, `setFolderMemoryEnabled`, `COLUMNS`, `tableLayout`, …) | per-folder view memory and the table configuration. The host owns the settings control and the transport; the rest is the explorer's |
| `dateGroups` (`groupByDate`, `dateBucketFor`, `groupingActive`) | the Today / Yesterday / This week ladder every listing view draws its headings from |
| `timezone` (`activeTimeZone`, `setTimeZone`, `supportedTimeZones`, …) | the viewer's clock, so dates outside the explorer are formatted against the same value |
| `uiProfile` (`UI_PROFILES`, `DEFAULT_UI_PROFILE`, `resolveUiProfile`) | the two profiles and the rule for everything that is not one of them |
| `actionIconSvg`, `actionIconKeys` | the action glyph vocabulary, so a host row drawn beside ours does not arrive in a different icon set |
| `useOperations`, `OperationsCenter`, `usePendingOps` | the operations centre |
| E2E encryption (`createEncryptedFolder`, `unlockWithPassword`, `EncryptedFolderModal`, …) | see [E2E-ENCRYPTION.md](E2E-ENCRYPTION.md) |

---

## `<FileManager>` (React)

A `@lit/react` wrapper around the Web Component, so behaviour is identical to
`<filex-explorer>` with idiomatic React props.

```bash
pnpm add @brftech/filex-react
```

```tsx
import { FileManager } from '@brftech/filex-react';
import type { ExplorerConfig } from '@brftech/filex-react';

export function MyFiles() {
  const config: ExplorerConfig = {
    apiBase: 'https://files.example.com',
    auth: { kind: 'bearer', token },
    locale: 'en',
  };

  return (
    <FileManager
      config={config}
      onError={(e) => console.error(e.detail[0].message)}
      onSelectionChange={(e) => console.log('selection:', e.detail[0])}
      onShareCreated={(e) => navigator.clipboard.writeText(e.detail[0].url)}
    />
  );
}
```

### Props

| Prop                | Type                              | Notes |
|---------------------|-----------------------------------|-------|
| `config`            | `ExplorerConfig`                  | the whole configuration |
| `apiBase` / `endpoint` | `string`                       | the attribute shortcuts, as props |
| `locale` / `theme`  | `string`                          | as above |
| `trashVisible` / `sidenav` / `connections` | `boolean \| string` | as above |
| `uiProfile`         | `'standard' \| 'simple'`          | as above |
| `className` / `style` | React's own                     | applied to the host element |
| `onError`           | `(e: CustomEvent) => void`        | `e.detail[0]` is `FilexErrorDetail` |
| `onFileOpened`      | `(e: CustomEvent) => void`        | `e.detail[0]` is `FilexFileOpenedDetail` |
| `onShareCreated`    | `(e: CustomEvent) => void`        | `e.detail[0]` is `FilexShareCreatedDetail` |
| `onUploadProgress`  | `(e: CustomEvent) => void`        | `e.detail[0]` is `FilexUploadProgressDetail` |
| `onSelectionChange` | `(e: CustomEvent) => void`        | `e.detail[0]` is `FilexSelectionChangeDetail` |

Handlers receive the **event**, not the payload. The payload is
**`e.detail[0]`** — the same array-wrapped `detail` the element dispatches
([Events](#events-customevent-on-the-element)). The detail interfaces exported
from the package describe that first element, not `e.detail` itself.

⚠ The prop list is read from the registered element's own definition
(`elementClass.def.props`) at import time, so adding a prop to
`@brftech/filex` cannot leave this package one behind. That indirection is not
decoration: handing `createComponent` the registered class instead produced
`config="[object Object]"` on the element and a blank page, because Vue's
`defineCustomElement` defines props on each *instance*, leaving nothing but
`constructor` on the class prototype `@lit/react` inspects.

⚠ There is **no imperative ref handle**. `ref` reaches the underlying custom
element, which has no methods either (above).

⚠ There is no React wrapper for the connections panel. Render
`<filex-connections>` in JSX and set `config` on the ref, the way you would any
non-React element.

---

## Shared TypeScript types

Exported from every package (`@brftech/filex-core`, `@brftech/filex`,
`@brftech/filex-react`). `types/ExplorerConfig.ts` and `types/FileNode.ts` are
the source of truth; what follows is the shape a host most often touches.

```ts
export type AuthConfig =
  | { kind: 'bearer'; token: string | (() => string | Promise<string>) }
  | { kind: 'csrf'; csrf: string }        // X-CSRF-TOKEN + credentials: include
  | { kind: 'basic'; user: string; pass: string }
  | { kind: 'none' };                     // development / public sandbox

export type ThemeMode = 'light' | 'dark' | 'auto';
export type LocaleCode = 'tr' | 'en';
export type UiProfile  = 'standard' | 'simple';
export type ViewMode   = 'list' | 'grid' | 'gallery';

export interface ExplorerConfig {
  /** Backend origin: `${apiBase}/api/files/manager`, and so on. */
  apiBase?: string;
  /** Legacy explicit manager URL, for hosts with their own routes. Any
   *  explicit endpoint field overrides the URL derived from apiBase. */
  endpoint?: string;

  auth?: AuthConfig;
  locale?: LocaleCode;
  theme?: ThemeMode;

  /** Initial path, storage-qualified (`main://projects`). A VIEW is
   *  addressable here too, by its sentinel: `'.home'` opens the overview,
   *  `'.recent'` / `'.starred'` / `'.shared'` / `'.trash'` open theirs. */
  initialPath?: string;

  /** Confine the explorer to one folder (`main://projects/acme`): it opens
   *  here, hides the drives root and blocks navigation above it.
   *  ⚠ SECURITY IS NOT THIS — enforce it server-side with a root-scoped token
   *  or the X-Filex-Root header. This is the clean-embed UX. */
  rootPath?: string;

  /** The product mark at the far left of the top bar. Both halves optional
   *  and independent. ⚠ An `<img src>`, never markup — there is no `v-html`
   *  on the path. Use this instead of the `#brand` slot in a web component,
   *  which cannot be filled at all. */
  brand?: { name?: string; markUrl?: string };

  /** How much of the explorer to put on screen. A REDUCTION, and only that:
   *  'simple' turns off the tab strip, the split pane and the gallery view
   *  mode, and defaults the Connections entries off. It removes nothing from
   *  the build and it does NOT decide the look — the "+ New" menu, the header
   *  search, the filter row, the Folders/Files sections, the Details/Activity
   *  tabs and the storage line are what every embed draws with no string
   *  passed. Two values; anything else resolves to 'standard' and logs one
   *  console line naming it. */
  uiProfile?: UiProfile;

  /** The navigation panel. Default on everywhere — except alongside
   *  `rootPath`, where a confined embed has no storage list to show. */
  sideNav?: boolean;

  /** The panel's "How to connect" + "API keys" entries. Default on, except
   *  under `uiProfile: 'simple'`. Never gated on role. */
  connections?: boolean;

  /** Is a PERSON behind this explorer, or an integration? 'app' suppresses the
   *  surfaces that belong to ONE identity — API keys, Recent, Starred, Shared
   *  with me — and keeps Upload, the storages, Trash and "How to connect".
   *  Omit it and the explorer asks the server (GET /api/files/capabilities →
   *  `caller_kind`), which is authoritative because only the server knows a
   *  token's kind; set it when the host already knows, to spare the flash of a
   *  Starred row that then disappears. See docs/MCP.md → Token kinds. */
  callerKind?: 'user' | 'app';

  /** How a mouse opens an item. `'double'` (default) — a single click selects
   *  and a double click opens (Enter opens the selection); `'single'` — the
   *  first click opens. Touch is unaffected: a tap always opens, the checkbox
   *  always selects. A per-viewer preference (the desktop app exposes it as
   *  Settings → Open files with). */
  openTrigger?: 'single' | 'double';

  /** Hand the OPEN of a file to the host: opening a file emits `file-opened`
   *  and the explorer skips its in-page preview. Directories still navigate
   *  inline; Space quick-look and the E2E decrypted preview stay in-page.
   *  Default false; the desktop app uses it to open each document in its own
   *  window. */
  openInHost?: boolean;

  /** Remember how each folder was last viewed (view mode + sort), Windows
   *  Explorer style. Default on — the opt-out is for an embed with one shape
   *  it wants. The state lives in a per-user document on the server, not in
   *  localStorage, so it follows the person between browsers and never leaks
   *  between accounts on a shared machine. Column widths are NOT covered: they
   *  are a global preference about the reader's screen. */
  rememberFolderView?: boolean;

  /** Default view mode. */
  viewMode?: 'list' | 'grid';

  /** When the tab strip is on screen. Default `'always'` — the SAME on every
   *  surface on purpose. `'auto'` is a deliberate opt-out for an embed too
   *  short to spend a row on. */
  tabStrip?: 'auto' | 'always';

  /** Show the virtual `.trash/` entry in the root listing. */
  trashVisible?: boolean;

  /** Multi-storage root: the explorer's "/" lists every entry in `storages`
   *  as a clickable directory. ⚠ Pair it with `storages` — the explorer
   *  MIRRORS the list you hand it and does not discover the server's. */
  multiStorageRoot?: boolean;
  storages?: Array<{
    name: string;
    /** The storage's immutable uid. A NAME is editable, so it is not a stable
     *  address; anything keyed on a storage for the long term should prefer
     *  this (per-folder view memory does). */
    uid?: string;
    label?: string;
    driver?: string;
    readOnly?: boolean;
    /** Bytes this storage holds, drawn as the caption on the Home storage
     *  card. ⚠ It must be the same quantity for every caller who gets it. */
    usedBytes?: number;
  }>;

  /** Where to persist the current path across reloads. */
  pathPersist?: 'hash' | 'localStorage' | 'hash+localStorage' | 'none';
}
```

⚠ `maxFileSizeMb`, `acceptTypes`, `shareBase` and `parallelChunks` are declared
and **never read**. They are kept so existing embeds keep compiling; setting
them restricts and changes nothing.

```ts
export interface FileNode {
  /** DB node id — needed by the per-user meta routes (starred, tags, recent).
   *  Only client-synthesized rows (virtual storage folders) lack one. */
  id?: number;
  /** Adapter-qualified path: `local://receipts/2024/invoice.pdf` */
  path: string;
  /** Basename: `invoice.pdf` */
  basename: string;
  relativePath?: string;
  type: 'file' | 'dir';
  /** Lowercased, no dot. */
  extension?: string;
  size?: number;
  /** Unix ms. */
  last_modified?: number;
  mime_type?: string;
  thumb_url?: string | null;
  visibility?: 'private' | 'public';
  /** File count, for directories. */
  count?: number;
  starred?: boolean;
  color?: string | null;
  trashed?: boolean;
  /** RBAC level for the current user on this entry, when the storage has RBAC
   *  on. Empty/absent = not enforced. */
  perm?: 'none' | 'viewer' | 'editor' | 'owner';
  /** Directory rows: the folder is E2E-encrypted. */
  e2e?: boolean;
  [k: string]: unknown;
}

export interface ShareInfo {
  uuid: string;
  url: string;
  password_pin?: string | null;
  expires_at?: string | null;
  /** The server shortened the expiry to honour its max-TTL setting, so the UI
   *  shows the real date rather than the one that was asked for. */
  expiry_clamped?: boolean;
  max_downloads?: number | null;
  downloads?: number;
  created_at?: string;
}

/** One document type the SERVER can create, from `capabilities.newdoc_types`. */
export interface NewDocType {
  /** Extension without the dot. Also the key the create call sends. */
  ext: string;
  group: 'document' | 'text' | 'diagram';
  mime: string;
  /** External service the EDITOR needs; absent = a built-in editor. The client
   *  crosses this against `external` so it never offers a .docx nobody on this
   *  deployment can then open. */
  requires?: 'onlyoffice' | 'drawio';
}

export type ExternalServiceState = 'ok' | 'error' | 'disabled' | 'unknown';
export interface ExternalServiceStatus {
  enabled: boolean;
  state: ExternalServiceState;
  url?: string;
  last_check?: string;
  detail?: string;
}

export interface Capabilities {
  /** Document types this build can create. Absent on a server older than the
   *  "New document" feature — treat that as "offer nothing". */
  newdoc_types?: NewDocType[];
  ffmpeg?: boolean;
  ghostscript?: boolean;
  libreoffice?: boolean;
  onlyoffice_url?: string | null;
  drawio_url?: string | null;
  convert_url?: string | null;
  max_chunk_mb?: number;
  upload_limit_mb?: number;
  /** Longest life a new share link may be given, in days (0 = no ceiling). */
  share_max_ttl_days?: number;
  /** 'user' for a session or a person's own token, 'app' for an integration. */
  caller_kind?: 'user' | 'app';
  external?: {
    onlyoffice?: ExternalServiceStatus;
    drawio?: ExternalServiceStatus;
    mermaid?: ExternalServiceStatus;
  };
  /** Whether this installation holds an escrow key for E2E folders, and the
   *  public half. Published on purpose: escrow means the operator can open the
   *  folders you create here, and you are entitled to know before you create
   *  one. */
  e2e_escrow?: { enabled: boolean; kid?: string; alg?: string; public_key?: string };
}
```

`isExternalUsable(s)` is the single answer to "is that service ready?" — both
`enabled` and `state === 'ok'`. `enabled` with `state: 'error'` means an
operator turned it on and a probe just failed, and an entry hidden beats a
button that 500s on click.

⚠ An **anonymous** `GET /api/files/capabilities` is answered without the `url`
fields: a caller with no credential is told *whether* a capability is on, never
*where* it lives. Every consumer that needs a host is behind a login already.

### Auth examples

```ts
// 1. Cross-origin SPA with a JWT or an API token
{ apiBase: 'https://files.example.com', auth: { kind: 'bearer', token } }

// 2. A token that refreshes — pass a function, sync or async
{ apiBase: 'https://files.example.com', auth: { kind: 'bearer', token: () => getFreshToken() } }

// 3. Same-origin cookie session (Laravel / Filament and friends)
{ apiBase: '/files', auth: { kind: 'csrf', csrf: window.csrfToken } }

// 4. Open / development backend
{ apiBase: 'http://localhost:5212', auth: { kind: 'none' } }
```

---

## The HTTP surface the component calls

[BACKEND.md](BACKEND.md) is the complete route reference. What follows is the
handful a *host* has to know about — because they have to survive a proxy
allow-list, and because two of them are not shaped like the rest.

| Route | Why it is here |
|---|---|
| `GET \| PUT /api/files/manager/view-prefs` | one opaque JSON document per user: view mode, sort, column widths/order/visibility. On the user row rather than in the browser, because `localStorage` is per-BROWSER and a shared machine would hand the next account the previous one's arrangements. Capped at 128 KB, server-side |
| `GET /api/files/quota/storages` | per-storage usage, RBAC-filtered — "how full is this drive" for somebody who is not an administrator. `{ storages: [{ name, used_bytes, file_count }] }`. It is the right source for `config.storages[].usedBytes` in an embed; `/api/admin/storages` is the operator's |
| `POST /api/files/manager?action=newfile` | create a document: `{ path, name, type }`, where `type` is an `ext` from `newdoc_types`. Answers `{ path, name, ext, size, mime }` — deliberately **not** the re-rendered listing, because a create is followed by "open the thing I just made" and the one fact the client cannot reconstruct is the final path (the name may have gained an extension). `409` on a collision: creation is the one verb where replacing is never the intent |
| `GET /api/files/capabilities` → `newdoc_types` | the document types **this build** can create, from a template registry compiled into the binary. Each row is `{ ext, group, mime, requires }`. Published to anonymous callers too: it is a static property of the build and names no host |
| `GET /api/branding` → `sso_label` | the operator's text for the sign-in page's SSO button (settings key `branding.sso_label`, tenant-overlaid like the rest of branding). Empty means the translated default |
| `GET /api/branding` → `custom_css` | the operator stylesheet (settings key `ui.custom_css`). It rides this payload because `/api/branding` is the appearance fetch the SPA already makes at boot, before a session exists, so the login screen is styled too |

### Downloading a selection is two requests

One streamed archive, minted and then fetched — and it is split in two for a
reason that is not going away. A download has to be a **navigation**: fetching
an archive and handing the browser a Blob buffers the whole thing in the tab,
which a multi-gigabyte selection cannot survive. But a navigation is a `GET`,
a `GET` cannot carry 300 paths in its URL, and it cannot carry an
`Authorization` header either — which is how a proxied embed authenticates.

```
POST /api/files/archive/download   { "paths": ["main://a", "main://b/"], "name": "Invoices" }
  → { url: "/z/<ticket>", ticket, name, files, bytes, expires_at }
GET  /z/<ticket>                   ← a navigation; streams the ZIP
```

- **The mint holds all the authority.** Every path is resolved, checked against
  the caller's tenancy and their ≥ viewer grant, and every selected folder is
  walked *server-side* with that same grant applied to each descendant. What
  the ticket carries is the finished member list; the client's list is an
  opening request, never the answer.
- **The redeem is public and credential-free by design** — the same reasoning
  as `/u/{ticket}` uploads. The ticket is not a credential for filex: it is
  unguessable, it authorizes exactly one archive, it expires in minutes and it
  is consumed on use. Nothing is written into storage and nothing is buffered
  in the tab; a 700 MB archive costs the server under a megabyte of memory.
- Refusals at the mint: `403` a named path is not readable by this caller ·
  `404` the storage is not this tenant's · `409` the selection resolved to no
  readable file at all · `413` more members than the cap. The `409` matters —
  an empty ZIP arriving as a "successful" download is the kind of thing people
  file bugs about six months later.

`downloadArchive(api, paths, { name })` does both halves, and navigates through
a hidden iframe rather than `window.open` (a popup by then — blocked) or
`location.href` (which walks the user off the page if the server ever answers
with an error body instead of an attachment).
