<img src="https://raw.githubusercontent.com/BRF-Tech/filex/main/docs/logo.png" alt="filex logo" width="72">

# @brftech/filex

Drop-in **`<filex-explorer>` Web Component** for the
[filex](https://github.com/brf-tech/filex) file manager. Wraps the
Vue 3 `<FileExplorer>` SFC from `@brftech/filex-core` and ships with
the Vue runtime bundled in — load it from any CDN, embed in any
framework, no peers required.

## Install

### npm

```bash
npm i @brftech/filex
```

```js
// Side-effect import registers the element.
import '@brftech/filex';
```

### CDN (no build step)

```html
<script type="module" src="https://cdn.jsdelivr.net/npm/@brftech/filex/dist/filex.js"></script>
```

## Use

### Plain HTML

```html
<filex-explorer
  api-base="https://files.example.com"
  locale="tr"
  theme="auto"
></filex-explorer>

<script type="module">
  import '@brftech/filex';
  const el = document.querySelector('filex-explorer');
  el.config = {
    auth: { kind: 'bearer', token: '<jwt>' },
    shareBase: 'https://files.example.com/shared',
  };
  el.addEventListener('error', (e) => console.error(e.detail));
  el.addEventListener('share-created', (e) => navigator.clipboard.writeText(e.detail.url));
</script>
```

### Inside a non-Vue framework

The element is just a normal DOM custom element — Angular, Svelte,
plain JS, no problem.

### `<filex-connections>` — reaching the server without a browser

The package registers a **second** element. filex can be spoken to as **S3**,
**SFTP**, **FTPS**, **NFSv3** and **WebDAV**, and mounted with `filex mount`;
this is where a user manages storages, mints the credential each protocol
takes, and reads instructions built from *that* deployment rather than a
template with angle brackets in it. It is the same component the filex admin
panel and the filex desktop app render — there is no second form and no second
set of instructions anywhere.

```html
<filex-connections></filex-connections>

<script type="module">
  import '@brftech/filex';
  const el = document.querySelector('filex-connections');
  el.initialTab = 'connect';           // 'storages' | 'connect'
  el.setAttribute('closable', '');     // show a close button, emits `close`
  el.config = {
    apiBase: 'https://files.example.com',
    auth: { kind: 'bearer', token: '<jwt>' },
    locale: 'tr',
  };
  el.addEventListener('changed', () => refreshMyFileList());
</script>
```

> ⚠⚠ Configure it through the `config` **property**, not attributes —
> `el.config = { ...el.config, locale: 'tr' }`. `buildConfig` merges
> `{...attributes, ...config}` and the config object wins, so an attribute is
> only ever a fallback for a key the config does not carry. That exact mistake
> shipped in v0.19.0: the shell went Turkish while the file list stayed
> English, and the element reported `locale === 'tr'` the whole time.

See [docs/PROTOCOLS.md](https://github.com/BRF-Tech/filex/blob/main/docs/PROTOCOLS.md).

## Attributes

Simple attributes are auto-parsed into the underlying `config` prop:

| Attribute | Maps to |
|---|---|
| `api-base` | `config.apiBase` |
| `endpoint` | `config.endpoint` (legacy Vuefinder-compat) |
| `locale` | `config.locale` (`tr` / `en`) |
| `theme` | `config.theme` (`light` / `dark` / `auto`) |
| `trash-visible` | `config.trashVisible` |

For anything richer (auth, custom endpoints, share base, …) set the
`config` JS property after element creation. Properties merge on top
of attributes.

## Events

Native `CustomEvent`s — listen with `addEventListener`. The original
SFC payload is on `event.detail`.

| Event | Detail shape |
|---|---|
| `error` | `{ message, context? }` |
| `share-created` | `{ path, url, pin }` |
| `file-opened` | `{ path, basename }` |
| `upload-progress` | `{ uploadId, percent, done }` |
| `selection-change` | `Array<{ path, basename, type }>` |

## Build

```bash
pnpm build      # vue-tsc + vite lib build → dist/filex.js + dist/style.css
```

## License

MIT
