<img src="https://raw.githubusercontent.com/BRF-Tech/filex/main/docs/logo.png" alt="filex logo" width="72">

# @brftech/filex

Drop-in **`<filex-explorer>` Web Component** for the
[filex](https://github.com/BRF-Tech/filex) file manager. Wraps the
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

## Styles

**No CSS import, no `<link>`.** The bundle carries the explorer's stylesheet
inside the JavaScript and appends it to `<head>` once, the first time an
element mounts — so the two lines above are all a page needs, whether they
come from a CDN or a bundler. It is `@brftech/filex-core`'s sheet byte for
byte, which is what keeps this distribution and the Vue one looking the same.

A `style.css` is published as well, for a host that would rather serve the
sheet itself:

```html
<link rel="stylesheet" href="https://cdn.jsdelivr.net/npm/@brftech/filex/dist/style.css">
```

The look is the `--fe-*` custom properties; set them on any scope above the
element (`:root`, a wrapper div) and the explorer follows. The element
deliberately has **no shadow root** for exactly this reason — the host page's
tokens, dark mode and fonts reach inside. The consequence is that a host
cannot fill a slot in it either, so the product mark in the top bar comes from
`config.brand` (`{ name, markUrl }`).

## Use

### Plain HTML

```html
<filex-explorer
  api-base="https://files.example.com"
  locale="tr"
  theme="auto"
></filex-explorer>

<script type="module">
  const el = document.querySelector('filex-explorer');
  // ⚠ Configure BEFORE the element is registered. A static `import` would be
  // hoisted above these lines, mount the explorer with no credentials and
  // spend its first listing on a request that can only fail.
  el.config = {
    apiBase: 'https://files.example.com',
    auth: { kind: 'bearer', token: '<jwt>' },
    rootPath: 'main://projects/acme',
  };
  // ⚠ The payload is e.detail[0] — see Events below.
  el.addEventListener('error', (e) => console.error(e.detail[0].message));
  el.addEventListener('share-created', (e) => navigator.clipboard.writeText(e.detail[0].url));
  await import('@brftech/filex');   // registers <filex-explorer>
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
| `sidenav` | `config.sideNav` — the navigation panel (the "+ New" menu · Home / Shared with me / Recent / Starred / Trash · the tags in use · the storages this caller can reach). Present or `="true"` is on, `="false"` off; absent keeps the default, which is on. |
| `connections` | `config.connections` — the panel's "How to connect" and "API keys" entries. Default on, except under `ui-profile="simple"` where it is off. ⚠ "API keys" is additionally dropped when the caller is an **app** token — see `config.callerKind` below. |
| `ui-profile` | `config.uiProfile` — `"standard"` (default) or `"simple"` (one pane, list/grid only, no tab strip, no split pane, "How to connect"/"API keys" off). Two values, no third: any other string resolves to `"standard"` and logs one console line naming it, so the `"drive"` profile that was **removed** after v0.40.0 no longer reduces anything — pass `"simple"` instead. ⚠⚠ It does **not** decide the look: the "+ New" menu, the one wide header search field with its ⌘K chip, the Type/People/Modified/Size filter row, the Folders/Files sections in grid (replaced by date headings while sorted by Modified), Details/Activity in the info panel and the storage line are what every embed draws now, with no string passed. |

For anything richer (auth, custom endpoints, share base, …) set the
`config` JS property after element creation. Properties merge on top
of attributes.

`config.callerKind` — `"user"` (a person) or `"app"` (an integration: a host
app's proxy, a bot). `"app"` leaves out the surfaces that belong to one
identity: **API keys**, **Recent**, **Starred**, **Shared with me**. Upload, the
storages, Trash and "How to connect" stay, so an embed is still an embed.
Omit it and the element asks the server (`GET /api/files/capabilities` →
`caller_kind`), which is authoritative because only the server knows a token's
kind; set it when your page already knows, to spare the moment before that
answer lands. ⚠ Proxying every visitor with one shared API token IS the `"app"`
case — see
[docs/MCP.md](https://github.com/BRF-Tech/filex/blob/main/docs/MCP.md#token-kinds--user-vs-app).

## Events

Native `CustomEvent`s — listen with `addEventListener`. ⚠ **The SFC payload is
`event.detail[0]`, not `event.detail`**: Vue dispatches a custom element's emit
as `new CustomEvent(name, { detail: args })`, where `args` is the emit's
argument list. `event.detail.url` is therefore `undefined`, and for
`selection-change` — whose payload is itself an array — `event.detail.length`
is always `1`.

| Event | Payload — `event.detail[0]` |
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

> ⚠⚠ `sideEffects` in `package.json` has to keep matching **every** file the
> build emits (`./dist/*.js`, `./dist/*.cjs`), not a list of names. The entry
> is only a re-export: `customElements.define` and the stylesheet injection
> live in the hashed chunk beside it, and a list that does not cover the chunk
> lets a bundler drop it — the element is then never registered, the sheet
> never injected, and a React or Vite host renders an empty page with no error
> anywhere. `web/tests/deploy/packageLook.test.ts` is the guard.

## License

MIT
