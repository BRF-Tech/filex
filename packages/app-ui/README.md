# @brftech/filex-app-ui

The SDK an app's own interface bundles to talk to [filex](https://github.com/BRF-Tech/filex).

An app may bring its own HTML/JS/CSS interface. filex serves it from the app's
approved package and runs it in a sandboxed frame whose policy allows no
connection, no storage and no cookie (a browser cannot fully stop a page
sending data out - see filex's APP-PLUGINS.md). Everything the interface does
goes through filex over one checked channel, and this package is that channel: a few KB, no dependency.

```bash
npm install @brftech/filex-app-ui
```

```js
import { connect } from '@brftech/filex-app-ui';

const fx = await connect();              // the handshake; rejects outside filex
const file = await fx.open();            // { name, ext, size, mime, readOnly, text(), bytes(), stream(), save() }
editor.value = await file.text();
editor.addEventListener('input', () => fx.dirty(true));
fx.onSave(() => editor.value);           // filex's Save, a draft's "Save to disk", Ctrl+S
```

Without a bundler, ship `dist/filex-app-ui.iife.js` in your bundle and load it
with a `<script src>`: it defines `window.FilexAppUI.connect`.

| Call | What it does |
|---|---|
| `fx.session` | the app and view, the reader's language and direction, filex's colours, the person's display name, the files, the grants |
| `fx.open(i)` / `fx.save(data)` / `fx.saveAs(name, data)` | read an opened file; save over it (a new version, or the draft it is); save a new file where the person picks |
| `fx.dirty(on)`, `fx.title(t)`, `fx.toast(t, tone)`, `fx.confirm(opts)`, `fx.close()` | tell filex about unsaved changes; ask filex to draw a line, a question, to close (a toast and a question carry your app's name) |
| `fx.copy(text)` | put text on the clipboard - call it from a click or a key press: without one, filex asks the person first |
| `fx.download(name, data, mime?)` | hand the person a file for their own disk (`"ui": { "download": true }` in the manifest) - from a click, or filex asks; 256 MiB at most; Chromium streams it into the file they pick |
| `fx.call(method, params)` | ask the app's own module (`ui_call`) |
| `fx.submit(action, params)` | queue one of the app's actions on the opened files - like `copy`, from a click, or filex asks |
| `fx.state.get(key)` / `fx.state.set(key, value)` | a small store for this app and this person |
| `fx.license.get()` | the app's license, for a paid app installed from a store (filex 0.52.0): `{status: 'free'}`, or `{status: 'valid', valid_until, updates_until}` - never the key, never the licensee |
| `fx.on('theme' \| 'locale' \| 'file.changed' \| 'close.request' \| 'app.updated', fn)` | listen to filex |

`connect()` paints filex's colours (`--fe-*` custom properties), `lang`, `dir`
and `data-theme` onto `<html>` and keeps them current; pass
`{ applyTheme: false }` to do it yourself.

The whole contract - manifest, serving policy, every message - is
`docs/APP-PLUGINS-API.md` → "An app's own interface"; how to build and package
an interface is `docs/PLUGIN-KIT.md` → "Writing an interface". The protocol
itself is `src/protocol.ts` (also exported as `@brftech/filex-app-ui/protocol`),
which filex's own frame imports, so the two sides cannot drift.

MIT © BRF Tech
