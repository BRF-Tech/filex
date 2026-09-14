<img src="https://raw.githubusercontent.com/BRF-Tech/filex/main/docs/logo.png" alt="filex logo" width="72">

# @brftech/filex-react

React adapter for the [filex](https://github.com/BRF-Tech/filex)
file manager. Thin wrapper around `<filex-explorer>` (the
`@brftech/filex` Web Component) — gives you proper React props and
camelCased event handlers via `@lit/react`'s `createComponent`.

> ⚠ This package wraps the **explorer** only. filex's other surface — the
> connections panel, where storages are added and the credentials for reaching
> the server over S3/SFTP/FTPS/NFS/WebDAV are minted — ships as
> `<filex-connections>` in [`@brftech/filex`](https://www.npmjs.com/package/@brftech/filex)
> and is a plain custom element: render it directly in JSX and set `config` on
> the ref, exactly as you would any non-React element.

## Install

```bash
npm i @brftech/filex-react react react-dom
```

## Use

```jsx
import { FileManager } from '@brftech/filex-react';

export function App() {
  return (
    <FileManager
      config={{
        apiBase: 'https://files.example.com',
        auth: { kind: 'bearer', token: '<jwt>' },
        locale: 'tr',
        theme: 'auto',
        // The navigation panel (the "+ New" menu · Home / Shared with me /
        // Recent / Starred / Trash · tags · storages) and how much of the
        // explorer to draw. Both are ordinary config keys — there is no
        // React-specific switch for either.
        sideNav: true,
        connections: true,
        // How much of the explorer to put on screen — a reduction, and only
        // that. Two values: 'standard' (default) and 'simple' (one pane,
        // list/grid only, no tab strip, no split pane). Anything else resolves
        // to 'standard' with one console line naming it — the 'drive' profile
        // was removed after v0.40.0; pass 'simple' instead. It does NOT decide
        // the look: the "+ New" menu, the header search field with its ⌘K hint,
        // the filter row, the Folders/Files sections and Details/Activity are
        // what every embed draws now, with no string passed.
        uiProfile: 'simple',
      }}
      onError={(e) => console.error(e.detail[0].message)}
      onShareCreated={(e) => navigator.clipboard.writeText(e.detail[0].url)}
      onFileOpened={(e) => console.log('opened', e.detail[0].basename)}
      onUploadProgress={(e) => console.log(e.detail[0].percent + '%')}
      onSelectionChange={(e) => console.log(e.detail[0].length, 'selected')}
    />
  );
}
```

The `config` prop accepts the full `ExplorerConfig` (re-exported from
`@brftech/filex-core` for convenience). Event handlers receive native
`CustomEvent`s, and ⚠ **the payload is `event.detail[0]`**: the underlying custom
element dispatches each emit with `detail` set to the emit's argument list, so
`event.detail.url` is `undefined`. The exported `Filex*Detail` types describe
that first element.

## Styles

**There is no CSS import.** The snippet above is the whole of it: this package
has no stylesheet of its own and you do not need one from anywhere else.

`@brftech/filex-react` wraps the `<filex-explorer>` element from
[`@brftech/filex`](https://www.npmjs.com/package/@brftech/filex), whose bundle
carries the explorer's stylesheet *inside the JavaScript* and appends it to
`<head>` once, the first time an element mounts. The sheet is
`@brftech/filex-core`'s, byte for byte, so a React page and a Vue page render
the same explorer — the Vue wrapper is the only one that imports a
`style.css`, because mounting an SFC runs no registration step that could
inject it.

The look is the `--fe-*` custom properties. Set them on any scope above the
component — `:root`, a wrapper `div` — and the explorer follows:

```css
:root {
  --fe-primary: #2b7a41;
  --fe-radius: 10px;
}
```

Light/dark follows `config.theme` (`'light'` / `'dark'` / `'auto'`, and `auto`
reads the host's `prefers-color-scheme`). The shipped palettes are in
[`docs/INTEGRATION.md`](https://github.com/BRF-Tech/filex/blob/main/docs/INTEGRATION.md#themes).

> ⚠ The product mark in the top bar comes from `config.brand`
> (`{ name, markUrl }`), not from a slot. A host that mounts a custom element
> cannot fill a slot inside it — that is a Vue custom-element limitation, not
> an oversight — so the config entry is the door.

## Bundler note

The rich viewers (Monaco, Mermaid, epub, xlsx, CodeMirror, …) are **optional**
and reached through dynamic imports that are caught, so a build without them
simply shows the plain viewer. A bundler, however, refuses to resolve an
import it cannot find, so `vite build` stops with
`Rollup failed to resolve import "monaco-editor"` unless you either install the
ones you want or tell it they are external:

```js
// vite.config.js
export default {
  build: {
    rollupOptions: {
      external: [
        'monaco-editor', 'jszip', 'mermaid', 'epubjs', 'xlsx',
        '@google/model-viewer', 'codemirror', /^@codemirror\//,
      ],
    },
  },
};
```

## Build

```bash
pnpm build      # tsc check + vite lib build → dist/filex-react.{js,cjs}
```

## License

MIT
