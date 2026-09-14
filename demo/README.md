# Demos

Three single-file HTML demos showing the same `filex` UI mounted three
different ways:

| File          | Framework            | Package                        | Notes |
|---------------|----------------------|--------------------------------|-------|
| `index.html`  | none / vanilla JS    | `@brftech/filex` (Web Component) | Drop-in `<filex-explorer>` tag |
| `vue.html`    | Vue 3                | `@brftech/filex-core`            | Mount `<FileExplorer>` SFC |
| `react.html`  | React 18             | `@brftech/filex-react`           | Mount `<FileManager>` |

All three are meant to pull the package off jsdelivr / esm.sh — no build step,
no `node_modules` — take an `apiBase` and optional bearer token from a small
toolbar, and render the file manager full-window.

> ⚠⚠ **None of the three renders an explorer today.** Measured 2026-09-14 by
> serving this folder as described below and loading each page in Chromium
> against the published `@latest` (0.40.0) packages:
>
> | Page | What happens |
> |---|---|
> | `index.html` | The bundle registers `<filex-explorer>` before the page assigns `config`, and the element throws `config requires either apiBase or endpoint`. The page also listens for `filex-ready`, `filex-error`, `filex-share-created` and `filex-navigate`, none of which the element emits — its events are `error`, `share-created`, `file-opened`, `upload-progress` and `selection-change`, with the payload in `e.detail[0]` ([docs/API.md](../docs/API.md#events-customevent-on-the-element)). |
> | `vue.html` | The app never mounts: the module script's import of `@brftech/filex-core` failed in the measurement, so `{{ status }}` stays on screen uncompiled. It would not render once that loads either — the component is written `<FileExplorer … />` in an in-DOM template, which the browser lowercases to `<fileexplorer>` before Vue sees it — and it listens for a `ready` event the component does not have. |
> | `react.html` | Babel's JSX transform imports `react/jsx-runtime`, which the page's import map does not provide, so the script fails before React starts. The page also passes `onReady` and `onNavigate`, which are not props of `<FileManager>`. |
>
> All three also pass `startPath`, `locale: 'auto'` and `auth: { kind: 'cookie' }`,
> none of which `ExplorerConfig` accepts. Until the pages are repaired, the
> working references are the snippets in [docs/INTEGRATION.md](../docs/INTEGRATION.md)
> and [docs/API.md](../docs/API.md).

## How to run

You need a local web server (browsers won't load ES modules off `file://`).
Pick whichever you have:

```bash
# Python
python -m http.server 8000

# Node
npx http-server -p 8000
```

Then open:

- <http://localhost:8000/index.html> — vanilla / WC demo
- <http://localhost:8000/vue.html>   — Vue 3 demo
- <http://localhost:8000/react.html> — React demo

Each page expects a running `filex` backend at the URL you type into the
toolbar (default `http://localhost:5212`). Start one in a separate shell:

```bash
docker run --rm -p 5212:5212 \
  ghcr.io/brf-tech/filex:latest
```

Or if you're developing locally:

```bash
# from the repo root; the placeholders are needed once, on a fresh clone
mkdir -p backend/embed/admin backend/embed/web
touch backend/embed/admin/.placeholder backend/embed/web/.placeholder
cd backend && FILEX_LISTEN=127.0.0.1:5212 go run ./cmd/filex serve
```

## CORS note

The demo on `localhost:8000` talks to filex on `localhost:5212`, which is a
cross-origin page. filex allows every origin by default
(`FILEX_CORS_ALLOWED_ORIGINS=*`), so listing, previews and small uploads work
as they are; to allow only the demo origin, set it explicitly:

```bash
docker run -p 5212:5212 \
  -e FILEX_CORS_ALLOWED_ORIGINS="http://localhost:8000" \
  ghcr.io/brf-tech/filex:latest
```

⚠ **Files above the 8 MiB chunk size need `Content-Range` in the CORS
allow-list.** Each chunk is a `PUT` carrying that header. It is in the default
list since filex 0.41.1; on an older server, or if you set your own list in
`config.yaml`, include it:

```yaml
cors:
  allowed_headers: [Authorization, Content-Type, X-Filex-Pin, Content-Range]
```

In production you'd serve filex behind the same origin as your app, so
none of this applies.

## Switching package versions

The demos use `@latest` by default. To pin a version, edit the
`<script type="importmap">` block in each HTML file:

```html
<script type="importmap">
{
  "imports": {
    "@brftech/filex-core": "https://cdn.jsdelivr.net/npm/@brftech/filex-core@0.1.0/dist/filex-core.js"
  }
}
</script>
```

## What you can demo

- Upload (drag-drop and dialog)
- Resumable chunked upload (a dropped connection costs one chunk)
- **+ New** → new document: text and code always, Word/Excel/PowerPoint/OpenDocument
  when the backend has an OnlyOffice server connected (the templates are compiled into
  the binary, so nothing needs LibreOffice installed)
- Select several files → Download, and they come back as one streamed ZIP
- Right-click context menu / long-press on touch
- Preview: image, video, audio, PDF, text, code (Monaco)
- Sharing — PIN + expiry + max downloads, copy URL
- Sort / filter / search
- Keyboard shortcuts (`Delete`, `F2` rename, `Ctrl+C/X/V`, `Esc`)
- Dark / light / auto theming
- TR / EN locale toggle
