# Component API

Three published packages, all built from one Vue 3 source of truth.

| Package                      | Use case                | Tech            |
|------------------------------|-------------------------|-----------------|
| `@brftech/filex-core`        | Vue 3 apps              | SFC + composables |
| `@brftech/filex`             | Any framework / vanilla | Web Component (`<filex-explorer>`) |
| `@brftech/filex-react`       | React apps              | `@lit/react` adapter |

All three take the **same `ExplorerConfig` object**; only the syntax to hand it
over differs. This page is the reference - every attribute, event, slot, export
and type, as the code defines them. [INTEGRATION.md](INTEGRATION.md) is the
guide: what to set, and why.

⚠ **Stylesheets differ by package, and only here.** The Vue package ships
`style.css` and you import it. The web-component and React packages have
**nothing to import** - the sheet travels inside the bundle and is appended to
`<head>` once, the first time an element mounts. (`@brftech/filex` also
publishes `dist/style.css` for a host that would rather serve the sheet
itself.)

⚠ **@brftech/filex 0.54 needs a filex 0.54 server.** Which files are office
documents or text a person edits (`edit_kinds`), the input limits (`limits`)
and the version line's parts (`release`, `commit`, `built`) come from the
server's `/api/files/capabilities`; the packages keep no list of their own to
fall back on. Against an older server nothing is offered Edit, nothing opens
as an office document and the version line is not drawn - update the server
with the packages ([BACKEND.md → Rules the server publishes](BACKEND.md#rules-the-server-publishes)).

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

For anything an attribute cannot carry - auth, `brand`, `storages`, per-route
overrides - set the `config` **property** in JS:

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
| `locale`       | string | `locale`   | `tr`, `en`, or any language an installed **language pack** adds (`GET /api/public/branding` → `ui_locales` lists them). ⚠ With a person signed in, the explorer speaks the **account's** language instead - this is only the fallback and a starting value ([The explorer's language](#the-explorers-language)). Unset ⇒ the browser's language, falling back to `en`. An embed takes its **text direction** from this value, not from the host page - see [RTL](RTL.md). The ONLYOFFICE editor it opens follows it too, unless the administrator fixed the editor's language ([ONLYOFFICE.md → The editor's language](ONLYOFFICE.md#the-editors-language)) |
| `theme`        | string | `theme`    | `light \| dark \| auto` (default `auto`) - the **host's** mode, used while the viewer has not pinned one of their own |
| `trash-visible`| bool   | `trashVisible` | show the Trash entry |
| `sidenav`      | bool   | `sideNav`  | the navigation panel. Absent leaves the core default (on) alone |
| `connections`  | bool   | `connections` | the panel's "How to connect" + "API keys" entries |
| `ui-profile`   | string | `uiProfile` | `standard \| simple`. Resolved by the core's own rule, so an unrecognised value becomes `standard` and says so once in the console |

Boolean attributes follow the DOM convention: present (or `="true"`) is true,
`="false"` is false, **absent leaves the core default alone** - writing
`sidenav="false"` is not the same as omitting it.

### Properties

| Property | Type             | Description |
|----------|------------------|-------------|
| `config` | `ExplorerConfig` | the full config object. Merged over the attributes, so the property wins on any key it carries |

### Events (CustomEvent on the element)

| Event              | Payload - `e.detail[0]`                                     | Fires on |
|--------------------|-------------------------------------------------------------|----------|
| `error`            | `{ message, context? }`                                      | Any error the explorer surfaces |
| `file-opened`      | `{ path, basename }`                                         | A file was opened |
| `share-created`    | `{ path, url, pin }`                                         | A share link was minted (`pin` is `null` when there is none) |
| `upload-progress`  | `{ uploadId, percent, done }`                                | Upload progress |
| `selection-change` | `Array<{ path, basename, type }>`                            | Selection changed |

Names are plain, **not** prefixed - Vue dispatches exactly what the wrapper
emits.

⚠⚠ **`e.detail` is an array, and the payload is its first element.** Vue's
`defineCustomElement` dispatches every emit as
`new CustomEvent(name, { detail: args })`, where `args` is the emit's
argument list - so `e.detail.url` is `undefined` and `e.detail[0].url` is the
link. `selection-change` emits an array, so its selection is `e.detail[0]`
(and `e.detail.length` is always `1`). Measured with the bundle in a real
browser: an `error` arrives as `detail: [{ message, context }]`.

```js
fx.addEventListener('error', (e) => console.error(e.detail[0].message));
fx.addEventListener('share-created', (e) => navigator.clipboard.writeText(e.detail[0].url));
fx.addEventListener('selection-change', (e) => console.log(e.detail[0].length, 'selected'));
```

**A refused change is said by the explorer AND emitted.** When the server
refuses a rename, a move or a copy (a drag, a paste, **Move to…**), a
duplicate, a delete, a new folder or a permanent delete, the explorer says so in
a toast in the reader's words - the lock sentence for a `423` - and emits
`error` with `{ message, context: { op } }`. A dialog still open over the
refused change shows the words in the dialog instead of a toast. A host that
shows its own message for `error` sets **`config.refusalToasts: false`**: the
explorer then says nothing itself and the event still fires (the dialog's own
words stay). The flag behaves the same in the web component, the Vue and React
wrappers and the desktop app.

⚠ The SFC's `navigate` and `refresh` emits are not forwarded through the custom
element. A host that needs them mounts the Vue SFC.

⚠ **The element exposes no imperative methods.** Drive it by reassigning
`config` (it is watched deeply, so a new `initialPath` re-navigates).

⚠⚠ **A host cannot fill a `<slot>` in `<filex-explorer>`**, and no version of
this package will change that. Vue projects light DOM into a custom element
only through a native `<slot>` inside a shadow root, and this element
deliberately has none - its whole look is one global stylesheet, so a shadow
root would leave every embed unstyled. Measured, 2026-09-13, with Vue's own
`defineCustomElement`: a `<span slot="brand">` inside the element leaves
`Object.keys(slots)` **empty** in the element's `setup`, with slot forwarding
and without it. Use `config.brand` (`{ name, markUrl }`) for the product mark.
Our own desktop app is in this position too - it mounts the web component.

### `<filex-connections>`

The "how to connect" surface as an element, so a host with no bundler mounts
the same component the admin SPA imports as an SFC.

| Property / attribute | Notes |
|---|---|
| `config` | same `ExplorerConfig`; **configure through this**, not the attributes |
| `closable` | render the close affordance |

Events: `close`, `error`.

⚠ `initial-tab` and the `changed` event were **removed in v0.43.0** with the
Storages tab they belonged to. The element no longer creates, edits or deletes
a storage, so there is no second half to open on and nothing that could make a
host's own storage list stale. Storages are managed in the admin panel
(Storages), and the storages a caller may browse are listed by the explorer's
navigation panel.

⚠ Set `el.config = { ...el.config, locale: 'tr' }`. Setting `el.locale = 'tr'`
changes a property nothing renders from - the merge is `{...attributes,
...config}` and the config object wins, so an attribute is only ever a fallback
for a key the config does not carry. That exact mistake shipped in v0.19.0: the
shell went Turkish while the file list stayed English, and the element reported
`locale === 'tr'` the whole time.

### The explorer's language

**The account's** (0.54, #191). The server says every notification - the
bell, a push to the person's phone, an email - in the language of the
person's account, so an explorer whose chrome spoke its host page's language
showed one person two languages. With a person's credential (the capabilities'
`caller_kind` is `user`), the explorer asks `GET /api/auth/me` and draws the
account's `locale`; the host's `locale` is:

- what it draws until the account answers;
- what it keeps when nobody is signed in (a public link) or the credential is
  an app's token (`caller_kind: app` - nobody's account);
- the **starting value of an account that has no language**: the explorer
  writes it to the account (`PATCH /api/auth/profile {"locale": ...}`), so the
  account's notifications and every other surface agree with what it shows;
- followed when the host changes it while the explorer is mounted (the filex
  web panel and desktop app write the account first, so the two agree).

A host that wants a different language for its own page draws its own page
in it; the explorer inside follows the person.

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
| `navigate`         | `{ path: string }` - the viewed folder changed |
| `refresh`          | *(none)* - the viewer asked for a refresh |
| `open-my-shares`   | *(none)* - the navigation panel's **My shares** row was pressed. The row is drawn only with `config.mySharesVisible: true` (default off); the page it leads to is the host's, so set the flag only if you handle this. Not forwarded by `<filex-explorer>` or `<FileManager>` |
| `open-app-store`   | *(none)* - the navigation panel's **App store** row was pressed (0.53, the store screen). `config.appStorePage: true` (default off) says the host has the page - the SPA's `app-store` route, the desktop app's store window; the explorer then draws the row only for a person whom the server shows the screen (`GET /api/app-store` → `visible`), the one rule for every host. Forwarded by `<filex-explorer>` (`open-app-store`) and `<FileManager>` (`onOpenAppStore`) |

`refresh` is a **notification, not a request**: the explorer reloads the listing
itself and does not wait for the host. It exists for the half it cannot know
about - `config.storages` is the host's answer to "which drives may I show
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

Real signatures - each takes what it needs rather than reaching for a global:

```ts
import {
  useFileApi,
  useUploadChunked,
  useSelection,
  useKeyboardShortcuts,
  useLocale,
} from '@brftech/filex-core';

// The backend wrapper. `index` lists, `newFolder` creates, `deleteItems`
// removes - the names follow the manager verbs, not the POSIX ones.
const api = useFileApi(config);
await api.index('main://projects');
await api.newFile('main://projects', 'Q3 report', 'docx');   // → Q3 report.docx
// `exactName`: the name is the whole file name (#56). Text types take it as
// is; a type with `ext_required` (office, diagrams) still gains its extension.
await api.newFile('main://projects', 'LICENSE', 'txt', { exactName: true });

// Uploads need the api instance: the staged protocol is several calls.
const { uploadFile, shouldChunk, threshold } = useUploadChunked(config, api);

// Selection is computed against the rows on screen, so it takes a getter.
const { selected, click, clear, selectAll, nodes } = useSelection(() => rows.value);

// Locale takes a Ref or a getter, never a bare string - it has to stay
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

**Drafts** (#71, [BACKEND.md → Drafts](BACKEND.md#drafts)) are `api.drafts`:
`create`, `list`, `count`, `get`, `save(key, as?)` and `discard`, bound to the
same endpoints and transport as every other verb. That is the way in; the
`draftsClient` factory behind it is not exported. `save` answers a
`DraftSaveOutcome` (a taken name comes back with a suggested free one, never a
replace), and `isDraftLimit` / `draftLimitOf` read the refusal of a caller at
their draft limit:

```ts
try {
  const { draft } = await api.drafts.create('main://projects', 'Q3 report', 'docx');
  const out = await api.drafts.save(draft.key);
} catch (e) {
  if (isDraftLimit(e)) console.warn(`at most ${draftLimitOf(e)} drafts`);
}
```

#### Naming a key on screen

Shortcuts are remappable (the user edits them in the shortcut settings; the
overrides live in `filex.shortcuts` in `localStorage`), so a hint that spells a
key out by hand is true only until somebody changes that key. Read the binding
instead:

```ts
import { shortcutHint, eventMatchesShortcut } from '@brftech/filex-core';

shortcutHint('palette');             // 'Ctrl+K' - or '⌘+K' on a Mac, or the
                                     // user's own combo, or '' when unbound
eventMatchesShortcut(ev, 'palette'); // true when THIS event fires that action
```

`shortcutHint` returns an empty string for an unbound action, so a caller can
drop the whole segment rather than draw an empty key cap. Action ids come from
`SHORTCUT_ACTIONS`.

### Components and helpers the package exports

A host that draws its own chrome should mount **these** rather than grow a
private copy - that is what keeps the admin app, the desktop app and every
embed one product.

| Export | What it is |
|---|---|
| `FileExplorer`, `PreviewModal`, `QuickLook` | the explorer, the viewer dispatch, the space-bar preview |
| `FilePane`, `TabBar` | one pane of the split view, and the tab strip |
| `NewDocumentModal` | the "+ New → document" picker. Exported because the entry belongs on every surface, not just the admin app |
| `DestinationPickerModal` + `destinationTree` helpers | the **one** folder chooser, spanning every storage. "Move to" and "Copy to" both mount it; its rules (`destinationRows`, `blockedReason`, `permAllowsWrite`, `isAtOrInside`, …) are pure functions so a host can reuse the decisions without the dialog |
| `downloadArchive`, `requestArchive`, `triggerFileNavigation`, `absoluteTicketUrl`, `archiveTicketUrl` | "download the selection as one archive" - the real two-step flow, for a host that draws its own selection bar |
| `ConnectionsPanel`, `StorageFields`, `TokensPanel`, `S3KeysPanel`, `SSHKeysPanel`, `NFSExportsPanel` | the connection surfaces, and the guide builders behind them |
| `ThemeGallery`, `ThemePalette`, `THEMES`, `setTheme`, `setThemeMode`, `setCustomThemes` | the palette gallery and the light/dark mode, for hosts whose appearance settings live in their own pane; `setCustomThemes` adds an operator's own themes (the `themes` of `GET /api/appearance`) beside the built-in ones |
| `viewPrefs` (`attachViewPrefsStore`, `folderMemoryEnabled`, `setFolderMemoryEnabled`, `COLUMNS`, `tableLayout`, …) | per-folder view memory and the table configuration. The host owns the settings control and the transport; the rest is the explorer's |
| `dateGroups` (`groupByDate`, `dateBucketFor`, `groupingActive`) | the Today / Yesterday / This week ladder every listing view draws its headings from |
| `timezone` (`activeTimeZone`, `setTimeZone`, `supportedTimeZones`, …) | the viewer's clock, so dates outside the explorer are formatted against the same value |
| `uiProfile` (`UI_PROFILES`, `DEFAULT_UI_PROFILE`, `resolveUiProfile`) | the two profiles and the rule for everything that is not one of them |
| `actionIconSvg`, `actionIconKeys` | the action glyph vocabulary, so a host row drawn beside ours does not arrive in a different icon set |
| `useOperations`, `OperationsCenter`, `usePendingOps` | the operations centre |
| E2E encryption (`createEncryptedFolder`, `unlockWithPassword`, `EncryptedFolderModal`, …) | see [E2E-ENCRYPTION.md](E2E-ENCRYPTION.md) |
| E2E building blocks: encrypted names (`encryptName`, `decryptStoredName`, `unlockNameKey`), the STREAM format for files over 200 MB (`encryptFolderFileStream`, `createStreamEncryptor`), converting a folder (`runConversion`), the names pass (`runNamePass`), a `.fxe`'s password (`changeFxePassword`) | what the explorer runs, for an integrator that writes into an encrypted folder without it: [E2E-ENCRYPTION.md → Using the building blocks](E2E-ENCRYPTION.md#using-the-building-blocks) |
| `isDraftLimit`, `isDraftFolderGone`, `draftLimitOf`, `DRAFT_*`, `DraftDto`, `DraftList`, `DraftSaveOutcome` | reading the answers of `api.drafts` (below) |

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
**`e.detail[0]`** - the same array-wrapped `detail` the element dispatches
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
export type LocaleCode = 'tr' | 'en' | (string & {});  // + any language a pack adds
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
   *  ⚠ SECURITY IS NOT THIS - enforce it server-side with a root-scoped token
   *  or the X-Filex-Root header. This is the clean-embed UX. */
  rootPath?: string;

  /** The product mark at the far left of the top bar. Both halves optional
   *  and independent. ⚠ An `<img src>`, never markup - there is no `v-html`
   *  on the path. Use this instead of the `#brand` slot in a web component,
   *  which cannot be filled at all. */
  brand?: { name?: string; markUrl?: string };

  /** How much of the explorer to put on screen. A REDUCTION, and only that:
   *  'simple' turns off the tab strip, the split pane and the gallery view
   *  mode, and defaults the Connections entries off. It removes nothing from
   *  the build and it does NOT decide the look - the "+ New" menu, the header
   *  search, the filter row, the Folders/Files sections, the Details/Activity
   *  tabs and the storage line are what every embed draws with no string
   *  passed. Two values; anything else resolves to 'standard' and logs one
   *  console line naming it. */
  uiProfile?: UiProfile;

  /** The navigation panel. Default on everywhere - except alongside
   *  `rootPath`, where a confined embed has no storage list to show. */
  sideNav?: boolean;

  /** The panel's "How to connect" + "API keys" entries. Default on, except
   *  under `uiProfile: 'simple'`. Never gated on role. On a multi-tenant
   *  server the guides read the top-level `realm` of `GET /api/auth/me`: a
   *  host that proxies that route must pass it through, or a tenant account's
   *  logins are printed without their `realm/` (docs/PROTOCOLS.md). */
  connections?: boolean;

  /** Is a PERSON behind this explorer, or an integration? 'app' suppresses the
   *  surfaces that belong to ONE identity - API keys, Recent, Starred, Shared
   *  with me - and keeps Upload, the storages, Trash and "How to connect".
   *  Omit it and the explorer asks the server (GET /api/files/capabilities →
   *  `caller_kind`), which is authoritative because only the server knows a
   *  token's kind; set it when the host already knows, to spare the flash of a
   *  Starred row that then disappears. See docs/MCP.md → Token kinds. */
  callerKind?: 'user' | 'app';

  /** What the signed-in account may do (filex `internal/perm` -
   *  "files.delete", "share.links", …). Omit it and the explorer reads
   *  `GET /api/auth/me` itself (the `me` endpoint, derived from apiBase) and
   *  hides the actions the server would refuse with `403 permission_denied` -
   *  so the desktop app and every embed hide what the web app hides. An
   *  administrator is not narrowed; an answer without permissions changes
   *  nothing. Account-wide: `permissionsByFolder` names the permissions whose
   *  answer differs by folder, which the explorer asks the server about for
   *  the selected paths (`POST …/manager?action=allowed`). */
  permissions?: string[];
  permissionsByFolder?: string[];
  /** Override for the endpoint above (`/api/auth/me`). */
  me?: string;

  /** How a mouse opens an item. `'double'` (default) - a single click selects
   *  and a double click opens (Enter opens the selection); `'single'` - the
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
   *  Explorer style. Default on - the opt-out is for an embed with one shape
   *  it wants. The state lives in a per-user document on the server, not in
   *  localStorage, so it follows the person between browsers and never leaks
   *  between accounts on a shared machine. Column widths are NOT covered: they
   *  are a global preference about the reader's screen. */
  rememberFolderView?: boolean;

  /** Default view mode. */
  viewMode?: 'list' | 'grid';

  /** When the tab strip is on screen. Default `'always'` - the SAME on every
   *  surface on purpose. `'auto'` is a deliberate opt-out for an embed too
   *  short to spend a row on. */
  tabStrip?: 'auto' | 'always';

  /** Show the virtual `.trash/` entry in the root listing. */
  trashVisible?: boolean;

  /** Say a refused change (rename, move, copy, paste, duplicate, delete, new
   *  folder, delete permanently) as a toast inside the explorer. Default
   *  `true`. It is emitted as `error` either way; `false` for a host that
   *  shows its own message for that event. See Events below. */
  refusalToasts?: boolean;

  /** Multi-storage root: the explorer's "/" lists every entry in `storages`
   *  as a clickable directory. ⚠ Pair it with `storages` - the explorer
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
     *  card, and summed into the storage line under the navigation for a
     *  person without a quota (a storage left without it is measured by the
     *  explorer itself, `GET /api/files/quota/storages`).
     *  ⚠ It must be the same quantity for every caller who gets it. */
    usedBytes?: number;
    /** `usedBytes` counts only part of the storage (the server's `coverage`
     *  beside the figure is not complete). Drawn as a lower bound: "at least
     *  1.2 GB used", on the card and in the storage line. */
    usedPartial?: boolean;
    /** The administrator's position (the server's `sort_order`, 1 = first;
     *  null/absent = not placed). The navigation panel and Home draw the
     *  storages in the person's own order when they have one, else in this
     *  one, else in the order of this array - see STORAGE.md → Ordering
     *  storages. A host that sends no positions keeps its own order. */
    sortOrder?: number | null;
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
  /** DB node id - needed by the node meta routes (starred, recent, and the
   *  personal + team tags - SEARCH.md#tags---personal-and-team).
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
  /** Stamped thumbnail URL, root-relative; `v=<render time>` changes with
   *  every new render (0.50), so key a cache on it. */
  thumb_url?: string | null;
  /** Why a file has no thumbnail, when the reason is the file's own
   *  (0.50): `corrupt`, `encrypted` (a password, or end-to-end) or
   *  `too_large`. Absent when it has one, while it is drawn, and for a reason
   *  that may pass. docs/thumbnails.md → Why a file has no thumbnail. */
  thumb_note?: 'corrupt' | 'encrypted' | 'too_large';
  /** A folder's newest files (0.50): up to three of the files directly in
   *  it, newest first by the later of when each came in and when it last
   *  changed; `thumb_url` when its thumbnail is ready (else draw its type
   *  icon). Absent for a folder with no files of its own, and on every folder
   *  while folder previews are off (capabilities `folder_previews: false`).
   *  docs/thumbnails.md → Folder previews. */
  preview?: { name: string; thumb_url?: string }[] | null;
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
  /** A symlink the server will NOT follow - it cannot be opened. A link inside
   *  a `local` storage's folder is followed and arrives as its target, so this
   *  never means merely "is a link". `type` stays `'file' | 'dir'`. */
  symlink?: boolean;
  /** Why: `outside_root` | `broken` | `unresolved`, as the driver says it
   *  now or as the last sync recorded it (0.54+). Absent when no reason is
   *  known (read it through `linkStateOf`). See docs/STORAGE.md → Symlinks. */
  link_state?: string;
  /** An entry the storage could not answer for (0.50, issue #104): listed,
   *  and every operation on it or inside it answers 409 ENTRY_UNAVAILABLE.
   *  See docs/PLUGINS.md → An entry your Stat cannot answer for. */
  unavailable?: boolean;
  /** What the storage answered (its own words). */
  unavailable_reason?: string;
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
  /** Extension without the dot. The key the create call sends, unless `key`
   *  names another. */
  ext: string;
  /** An app's row (`new_documents`): what the create call sends as `type`
   *  (`app:<plugin>:<ext>`). Absent for the built-in kinds. */
  key?: string;
  group: 'document' | 'text' | 'diagram' | 'app';
  mime: string;
  /** External service the EDITOR needs; absent = a built-in editor; `app` =
   *  the app's interface, listed only while the app runs. The client crosses
   *  this against `external` so it never offers a .docx nobody on this
   *  deployment can then open. */
  requires?: 'onlyoffice' | 'drawio' | 'app';
  /** An app's row: the app, its `viewer` view that opens the new file, and
   *  the app's own label for the row. */
  app?: { plugin: string; view: string; label: Record<string, string> };
  /** Must the file carry this extension? `true` for office documents and
   *  diagrams (their editors find them by it), `false` for text, which may be
   *  named anything (#56). Absent on a server from before #56, which appends
   *  the extension to every type - treat that as `true`. */
  ext_required?: boolean;
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
   *  "New document" feature - treat that as "offer nothing". */
  newdoc_types?: NewDocType[];
  /** 0.54: the release, the commit and the build time apart (`version`
   *  keeps the one-line `v0.54.0 (<commit>, <built>)`). */
  release?: string;
  commit?: string;
  built?: string;
  /** 0.54: how each kind of file is edited - the server's rule
   *  (docs/BACKEND.md → Rules the server publishes). */
  edit_kinds?: {
    office: string[];
    text: string[];
    text_names: string[];
    text_mime_prefixes: string[];
    text_mimes: string[];
  };
  /** 0.54: the numbers an input is held to, in characters or bytes. */
  limits?: {
    tag_max_runes: number;
    comment_max_runes: number;
    e2e_request_reason_max_runes: number;
    app_state_max_bytes: number;
    app_ui_save_chunk_bytes: number;
  };
  /** 0.54, signed-in callers: the notification events that cannot happen
   *  here - why, whether this caller could change it, and the sentence. */
  event_off?: Record<string, { reason: string; fixable: boolean; text: string }>;
  ffmpeg?: boolean;
  ghostscript?: boolean;
  /* No `libreoffice` since 0.50: filex runs no LibreOffice; office
     thumbnails and conversions are the connected ONLYOFFICE's
     (`thumbs.office`, `external.onlyoffice`). */
  /** Folder previews are on (0.50; setting `thumbs.folder_previews`, on by
   *  default): folder rows carry `preview` and the explorer shows what a
   *  folder holds on a resting pointer. Absent (an older server): on. */
  folder_previews?: boolean;
  onlyoffice_url?: string | null;
  drawio_url?: string | null;
  max_chunk_mb?: number;
  upload_limit_mb?: number;
  /** Longest life a new share link may be given on this install, in days (0 = no ceiling). */
  share_max_ttl_days?: number;
  /** Longest life a new link made by THIS caller may be given, in days (0 =
   *  no ceiling): the install's ceiling or their permission rules', whichever
   *  is shorter. The share dialog's expiry choices come from this one. */
  share_link_max_days?: number;
  /** Present when New document makes DRAFTS for this caller (#71): a signed-in
   *  person, not an app token or a caller confined to a root. `limit` is how
   *  many one person may keep. Absent → New document creates the file. */
  drafts?: { limit: number };
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
  /** Who may START encrypting here - the caller's own tenant's row:
   *  `available` is the platform operator's switch, `policy` the tenant's
   *  choice (`off` | `admins` | `permitted` | `approval`). A signed-in caller's
   *  only, and absent on a server older than the policy (read as "offer
   *  encryption as before"). What the explorer offers where is asked per path:
   *  `POST /api/files/e2e/allowed` ([BACKEND.md](BACKEND.md#encryption-policy)). */
  e2e_policy?: { available: boolean; policy: string };
  /** Whether this server serves the vault, encryption level 3
   *  (`/api/files/e2e/vault`, [BACKEND.md](BACKEND.md#vault-encryption-level-3),
   *  [E2E-VAULT-FORMAT.md](E2E-VAULT-FORMAT.md)): always present, `true` only
   *  where `FILEX_E2E_VAULT` is on (off by default). Only then does the explorer
   *  offer level 3, and only for a new folder. */
  e2e_vault?: boolean;
}
```

`isExternalUsable(s)` is the single answer to "is that service ready?" - both
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

// 2. A token that refreshes - pass a function, sync or async
{ apiBase: 'https://files.example.com', auth: { kind: 'bearer', token: () => getFreshToken() } }

// 3. Same-origin cookie session (Laravel / Filament and friends)
{ apiBase: '/files', auth: { kind: 'csrf', csrf: window.csrfToken } }

// 4. Open / development backend
{ apiBase: 'http://localhost:5212', auth: { kind: 'none' } }
```

---

## The HTTP surface the component calls

The same routes, with their parameters and bodies, as OpenAPI 3.1:
[`backend/internal/api/openapi.json`](../backend/internal/api/openapi.json)
([BACKEND.md](BACKEND.md) says what it covers).

[BACKEND.md](BACKEND.md) is the route reference; sharing grants, groups and
tenants are covered in full in [RBAC.md](RBAC.md), [GROUPS.md](GROUPS.md) and
[MULTI-TENANCY.md](MULTI-TENANCY.md). What follows is the
handful a *host* has to know about - because they have to survive a proxy
allow-list, and because two of them are not shaped like the rest.

Every route below is under the server's [base path](CONFIGURATION.md#base-path)
when it has one (`/filex/api/files/manager` for a filex at
`https://example.com/filex/`) - which is why the component takes the server
root, path included, as `apiBase`.

| Route | Why it is here |
|---|---|
| `GET \| PUT /api/files/manager/view-prefs` | one opaque JSON document per user: view mode, sort, column widths/order/visibility. On the user row rather than in the browser, because `localStorage` is per-BROWSER and a shared machine would hand the next account the previous one's arrangements. Capped at 128 KB, server-side |
| `GET /api/files/manager?action=index&path=` → `storage_info[].sort_order` | the administrator's position of each drive the caller can open (absent = not placed), beside `read_only`; the `storages` names come in that order. The right source for `config.storages[].sortOrder` in an embed. A person's own order is on their account (`/api/me/prefs`, `storageOrder`), and the explorer applies it itself - [STORAGE.md → Ordering storages](STORAGE.md#ordering-storages) |
| `GET /api/files/quota/storages` | per-storage usage, RBAC-filtered - "how full is this drive" for somebody who is not an administrator. `{ storages: [{ name, used_bytes, file_count }] }`. It is the right source for `config.storages[].usedBytes` in an embed; `/api/admin/storages` is the operator's |
| `POST /api/files/manager?action=newfile` | create a document: `{ path, name, type, exact_name? }`, where `type` is an `ext` from `newdoc_types` - or, for an app's row, its `key` (`app:<plugin>:<ext>`, made of the app's template, `400 UNSUPPORTED_TYPE` while the app is not running or its grant lacks the kind). Without `exact_name` the type's extension is appended when the name lacks it; with `exact_name: true` the name is the whole file name - a text type is created under exactly it (`LICENSE`, `test.conf`), and only a type with `ext_required` still gains its extension (#56). A text type named with an `ext_required` type's extension (`x.docx` as `txt`) is `400 EXT_NEEDS_TYPE`. Answers `{ path, name, ext, size, mime }`, where `ext` is the **type** the bytes were made from, not the name's extension - deliberately **not** the re-rendered listing, because a create is followed by "open the thing I just made" and the one fact the client cannot reconstruct is the final path (the name may have gained an extension). `409 { code: "NAME_TAKEN", name, suggested }` on a collision - creation is the one verb where replacing is never the intent - where `suggested` is the first free `name (n).ext` beside it (the numbering a draft saved beside a file of its name gets). With `dry_run: true` nothing is written: every refusal is the same, and a name that passes them answers `200 { dry_run: true, path, name, taken, code?, suggested? }` - `taken` is the create's own existence check (byte for byte on a case-sensitive store), and `suggested` the free name when taken. The New document dialog asks it while a name is typed (0.54) |
| `GET /api/files/manager?action=changes&path=<storage>://<folder>&since=<cursor>` | `{ cursor, changed }`: has anything under this folder changed since the cursor this caller got last time? No `since` (or a cursor from before a server restart) is always `changed`. One request instead of re-listing a tree; the sync client and the desktop app ask it every round. Same visibility rules as `index`, and a change counts only if the caller can see what it touched. Servers before it answer `501` - walk instead |
| `POST /api/files/manager?action=rename` | rename one item in place: `{ path, item, name }`. `409 { code: "NAME_TAKEN", name }` when anything already has the name - a rename never replaces it, and is not given a `-copy` name either, because the client's undo assumes the item landed exactly where it was asked to. `503 { code: "EXISTS_CHECK_FAILED" }` when the backend cannot tell. A case-only rename is allowed. With `queued=1` the rename that passes those checks is a job of the operations queue instead: `202 { op }` (kind `rename`); the explorer asks for that for a folder, from a server that lists `rename` under `capabilities.queued` |
| `GET /api/files/capabilities` → `queued` | the changes this server runs as jobs of its operations queue when asked with `queued=1`: `rename` (above), `restore` (`POST /api/files/manager/restore?queued=1` with `{ node_ids }`) and `purge` (`DELETE /api/admin/trash/{id}?queued=1`), see [TRASH-VERSIONING.md](TRASH-VERSIONING.md#trash-endpoints). Absent on an older server, which changes inside the request: ask it the old way |
| `/api/files/e2e/vault/*` and `GET /api/files/capabilities` → `e2e_vault` | the vault, encryption level 3 ([E2E-VAULT-FORMAT.md](E2E-VAULT-FORMAT.md)): `create`, `state`, `list`, `lock` (`renew`, `release`, `break`), `PUT pack`, `PUT index`, `delete` and `prefs`, with the write lock's token in `X-Filex-Vault-Lock`. A pack is up to 16 MiB and an index file up to 64 MiB of request body - a proxy that caps bodies must let those two through. `e2e_vault` is always present and `true` only where the API answers (`FILEX_E2E_VAULT`); while it is `false` every route answers `404 VAULT_DISABLED`. Full reference: [BACKEND.md → Vault](BACKEND.md#vault-encryption-level-3) |
| `/api/files/drafts` | a new document is a **draft** until its first save (#71): `POST` makes one (the `newfile` body), `GET` lists the caller's own (`{ drafts, count, limit }`), `GET …/count` is the panel's badge, `POST …/{key}/save` puts it in its folder - `409 TARGET_TAKEN` with a `suggested` free `name (2).ext` when the name is taken, never a replace - and `DELETE …/{key}` discards it into the trash. A person's own, only: an app token or a caller confined to a root gets `403 DRAFTS_UNAVAILABLE` and keeps using `newfile`, and `capabilities.drafts` says which a caller is. Full reference: [BACKEND.md → Drafts](BACKEND.md#drafts); what a person sees: [ONLYOFFICE.md → Drafts](ONLYOFFICE.md#drafts-nothing-is-in-the-folder-until-you-save) |
| `GET /api/files/capabilities` → `newdoc_types` | the document types **this build** can create, from a template registry compiled into the binary. Each row is `{ ext, group, mime, requires, ext_required }` - `ext_required` is `true` where the editor finds the file by its extension (office, diagrams) and `false` for text, which may be named anything (#56). Published to anonymous callers too: it is a static property of the build and names no host. A **signed-in person** is also told the rows running apps add (`new_documents`): `{ ext, key, group: "app", requires: "app", ext_required: true, app: { plugin, view, label } }` - never an anonymous caller or an app token |
| `GET /api/branding` → `sso_label` | the operator's text for the sign-in page's SSO button (settings key `branding.sso_label`, tenant-overlaid like the rest of branding). Empty means the translated default |
| `GET /api/me/custom-css` | the operator stylesheet (settings key `ui.custom_css`), **behind authentication** and `no-store`: `{ css, enabled }`, already sanitised and already wrapped in its `@scope` guard. ⚠ It used to ride `GET /api/branding`, which is public - so it reached anonymous visitors and the sign-in form. The `custom_css` field is **removed** from that payload rather than emptied, so a client still reading it fails loudly instead of quietly rendering nothing. Off by default (`ui.custom_css_enabled`); see [INTEGRATION.md](INTEGRATION.md#operator-custom-css) |

### Downloading a selection is two requests

One streamed archive, minted and then fetched - and it is split in two for a
reason that is not going away. A download has to be a **navigation**: fetching
an archive and handing the browser a Blob buffers the whole thing in the tab,
which a multi-gigabyte selection cannot survive. But a navigation is a `GET`,
a `GET` cannot carry 300 paths in its URL, and it cannot carry an
`Authorization` header either - which is how a proxied embed authenticates.

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
- **The redeem is public and credential-free by design** - the same reasoning
  as `/u/{ticket}` uploads. The ticket is not a credential for filex: it is
  unguessable, it authorizes exactly one archive, it expires in minutes and it
  is consumed on use. Nothing is written into storage and nothing is buffered
  in the tab; a 700 MB archive costs the server under a megabyte of memory.
- Refusals at the mint: `403` a named path is not readable by this caller, or
  lies outside a `root:`-confined token's folder · `404` the storage is not
  this tenant's · `409` the selection resolved to no readable file at all ·
  `413` more members than the cap. The `409` matters - an empty ZIP arriving as
  a "successful" download is the kind of thing people file bugs about six
  months later.
- The answer says what was minted: `mode: "zip"`, and `ttl_seconds` - the
  ticket's life from now, so a client never compares its clock with the
  server's.
- ⚠ `url` is **relative to the server root** - the address `/api/…` hangs
  off - not to the host. Join it onto your API base without dropping its
  path: `https://example.com/filex` + `/z/<ticket>` for a filex served under
  `/filex` ([base path](CONFIGURATION.md#base-path)), `/your/files` +
  `/z/<ticket>` behind a host proxy. `new URL(url, apiBase)` gets this wrong -
  a root-relative path replaces the base's whole path. The same holds for a
  listing's `thumb_url`.

#### A single file: the drag-out link

```
POST /api/files/archive/download   { "paths": ["main://docs/a.pdf"], "mode": "file" }
  → { url: "/z/<ticket>", ticket, name: "a.pdf", files: 1, bytes, expires_at,
      mode: "file", ttl_seconds: 60 }
GET  /z/<ticket>                   ← the file's own bytes; attachment, no-store
```

The same mint, store and redeem, for the one thing a plain download URL cannot
do: a page that signs with a **bearer** handing a `DownloadURL` to the browser's
drag-out, whose download stack sends no `Authorization` header. What differs
from an archive ticket:

- **Exactly one path, and a file.** A folder is `409 {"code":"IS_FOLDER"}`:
  the explorer mints these speculatively (below), and minting a folder's
  archive walks its whole subtree - work nobody asked for, on every folder a
  pointer crosses. Download gives a folder as an archive.
- **A minute, once.** `ttl_seconds` is at most 60; `expires_in_seconds` may
  shorten it, never lengthen it. A lapsed link is `410`, a used one `404`.
- **Re-judged at the redeem, as its owner.** An archive ticket carries the
  member list its mint authorized; a file link asks again at the drop: the
  account can still sign in (enabled, its tenant not suspended), the token it
  was minted with is not revoked, the request arrives on the tenant host it was
  minted on (`403`), the storage is still the owner's tenant's (`404`) and the
  owner still has ≥ viewer on the file (`403`). A refusal consumes the link; a
  server-side failure (`5xx`) does not, so the same drop can be retried.
- **Audited at the redeem**: `file.download_link` when the download starts,
  `file.download_link_refused` with a `reason` (`account` · `token` · `host` ·
  `storage` · `acl`) when it is refused. The link itself never goes into the
  row. Minting is not audited - it happens on hover.
- ⚠ **Check `mode` in the answer.** A server that predates it ignores the field
  and mints a ZIP of the one file; `requestFileLink(api, path)` (core
  `lib/downloadSelection`) answers `null` for that rather than hand the desktop
  a zip named like the file.

Why the explorer mints on hover: `dragstart` must fill the dataTransfer
synchronously - it is writable during that event and never after - so a link
asked for when the drag starts is always too late. `createDragLinks` (core
`lib/dragOut`) asks when the pointer rests on a file row or a ⌘K result and
again on the press, keeps one mint in flight (a sweep across forty rows is two
requests), hands each link out once, and drops one in its last seconds. A drag
that beats the mint carries no `DownloadURL` - never a URL that would 401.

`downloadArchive(api, paths, { name })` does both halves, and navigates through
a hidden iframe rather than `window.open` (a popup by then - blocked) or
`location.href` (which walks the user off the page if the server ever answers
with an error body instead of an attachment).
