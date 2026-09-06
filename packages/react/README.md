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
        // The navigation panel (Upload · Recent / Starred / Shared with me /
        // Trash · storages) and the chrome preset. Both are ordinary config
        // keys — there is no React-specific switch for either.
        sideNav: true,
        connections: true,
        // 'standard' (default) · 'simple' (one pane, list/grid only, no tab
        // strip) · 'drive' (that, plus the Drive-shaped shell: a "+ New" menu,
        // one header search field with its ⌘K palette hint, a
        // Type/Modified/Size filter row, Folders/Files sections in grid, and
        // Details/Activity in the info panel).
        uiProfile: 'drive',
      }}
      onError={(e) => console.error(e.detail)}
      onShareCreated={(e) => navigator.clipboard.writeText(e.detail.url)}
      onFileOpened={(e) => console.log('opened', e.detail.basename)}
      onUploadProgress={(e) => console.log(e.detail.percent + '%')}
      onSelectionChange={(e) => console.log(e.detail.length, 'selected')}
    />
  );
}
```

The `config` prop accepts the full `ExplorerConfig` (re-exported from
`@brftech/filex-core` for convenience). Event handlers receive native
`CustomEvent`s — payload is on `event.detail`.

## Build

```bash
pnpm build      # tsc check + vite lib build → dist/filex-react.{js,cjs}
```

## License

MIT
