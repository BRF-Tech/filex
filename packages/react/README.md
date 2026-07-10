<img src="https://raw.githubusercontent.com/BRF-Tech/filex/main/docs/logo.png" alt="filex logo" width="72">

# @brftech/filex-react

React adapter for the [filex](https://github.com/brf-tech/filex)
file manager. Thin wrapper around `<filex-explorer>` (the
`@brftech/filex` Web Component) — gives you proper React props and
camelCased event handlers via `@lit/react`'s `createComponent`.

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
