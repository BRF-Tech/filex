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
