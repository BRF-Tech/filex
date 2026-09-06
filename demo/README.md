# Demos

Three single-file HTML demos showing the same `filex` UI mounted three
different ways:

| File          | Framework            | Package                        | Notes |
|---------------|----------------------|--------------------------------|-------|
| `index.html`  | none / vanilla JS    | `@brftech/filex` (Web Component) | Drop-in `<filex-explorer>` tag |
| `vue.html`    | Vue 3                | `@brftech/filex-core`            | Mount `<FileExplorer>` SFC |
| `react.html`  | React 18             | `@brftech/filex-react`           | Mount `<FileManager>` |

All three:
- Pull the package off jsdelivr / esm.sh — no build step, no `node_modules`.
- Take an `apiBase` and optional bearer token from a small toolbar.
- Render the file manager full-window.

## How to run

You need a local web server (browsers won't load ES modules off `file://`).
Pick whichever you have:

```bash
# Python
python -m http.server 8000

# Node
npx http-server -p 8000

# Go
go run github.com/julienschmidt/sse-server/cmd/sse-server -p 8000
```

Then open:

- <http://localhost:8000/index.html> — vanilla / WC demo
- <http://localhost:8000/vue.html>   — Vue 3 demo
- <http://localhost:8000/react.html> — React demo

Each page expects a running `filex` backend at the URL you type into the
toolbar (default `http://localhost:5212`). Start one in a separate shell:

```bash
docker run --rm -p 5212:5212 \
  -e FILEX_TRUST_PROXY_HEADERS=false \
  ghcr.io/brf-tech/filex:latest
```

Or if you're developing locally:

```bash
cd ../backend && go run ./cmd/filex serve --listen 127.0.0.1:5212
```

## CORS note

If your demo is on `localhost:8000` and filex on `localhost:5212`, you need
filex to allow CORS from the demo origin:

```bash
docker run -p 5212:5212 \
  -e FILEX_CORS_ALLOWED_ORIGINS="http://localhost:8000" \
  ghcr.io/brf-tech/filex:latest
```

In production you'd serve filex behind the same origin as your app, so
this isn't needed.

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
- Multipart resumable upload
- Right-click context menu / long-press on touch
- Preview: image, video, audio, PDF, text, code (Monaco)
- Sharing — PIN + expiry + max downloads, copy URL
- Sort / filter / search
- Keyboard shortcuts (`Delete`, `F2` rename, `Ctrl+C/X/V`, `Esc`)
- Dark / light / auto theming
- TR / EN locale toggle
