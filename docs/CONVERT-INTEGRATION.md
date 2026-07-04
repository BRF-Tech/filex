# File conversion (converter)

filex can convert files between formats **entirely in the browser** — no server
runs the conversion and the file is never uploaded to a third party. A
**Convert** action (right‑click a file, or the selection toolbar) opens a format
picker; you choose a target format; the bytes are converted client‑side and the
result is written back into the current folder.

It is an **optional** integration: filex embeds a self‑hosted build of
[p2r3/convert](https://github.com/p2r3/convert) (an in‑browser WASM converter)
in a hidden iframe. When it isn't configured, the Convert action simply doesn't
appear.

- [Enable it](#enable-it)
- [How it works](#how-it-works)
- [Notes & limits](#notes--limits)
- [License](#license)

---

## Enable it

Two steps: **run the converter service**, then **point filex at it**.

filex loads the converter from `FILEX_CONVERT_URL` in an iframe, so that URL must
be reachable from the **browser**. The simplest layout serves it under
**`/convert`** on the same host as filex.

The converter image is
[`ghcr.io/brf-tech/converter-filex`](https://github.com/BRF-Tech/converter-filex)
— a small fork of p2r3/convert that adds the embed bridge filex drives. It's a
static site behind nginx on port 80.

### Docker Compose

The **full** stack already bundles it — turn on the `convert` profile and set the
URL in your `.env`:

```dotenv
COMPOSE_PROFILES=convert          # plus any other add-ons you run
FILEX_CONVERT_URL=https://files.example.com/convert
```

The bundled Caddy already routes `/convert/*` to the converter container. See
[`deploy/compose/`](../deploy/compose/).

### Standalone / any reverse proxy

Run the container and route a `/convert` subpath to it:

```bash
docker run -d --name converter --restart unless-stopped \
  -p 127.0.0.1:8080:80 ghcr.io/brf-tech/converter-filex:latest
```

Then in your reverse proxy send `/convert/*` to it and everything else to filex
(Caddy example):

```caddy
files.example.com {
    @conv path /convert /convert/*
    handle @conv { reverse_proxy 127.0.0.1:8080 }
    handle       { reverse_proxy 127.0.0.1:5212 }
}
```

and set `FILEX_CONVERT_URL=https://files.example.com/convert`. (The converter is
served with a `/convert` base path, so no URL rewrite is needed.)

### Helm

```yaml
convert:
  enabled: true
  url: https://files.example.com/convert
```

Run the `converter-filex` image as its own Deployment + Service and route the
`/convert` path to it through your Ingress.

---

## How it works

```
filex (Vue)  ──hidden iframe──►  <FILEX_CONVERT_URL>/?embed=1   (converter-filex)
     │  postMessage: listFormats / convert
     │  ◄──────────── ready / formats / converted bytes
     ├─ reads the source file's bytes → iframe
     └─ writes the converted bytes back into the current folder
```

- Everything runs **client‑side (WASM)** inside the iframe. filex never sends the
  file anywhere for conversion.
- The GPL‑2.0 converter stays a **separate service**, embedded only via an iframe
  — so filex itself stays MIT‑licensed and its code never links the converter.
- filex advertises the feature through its capabilities probe: the Convert action
  appears only when `FILEX_CONVERT_URL` is set and reachable.

---

## Notes & limits

- **The first conversion for a given format is slower** — that format's WASM tool
  loads once per browser session; subsequent conversions are fast.
- **Conversion happens in the browser tab's memory**, so very large files can be
  slow or memory‑heavy. Great for documents and images; not for multi‑GB media.
- **Available formats** come from the converter build's precached format list;
  exotic formats a build doesn't include won't show in the picker.

---

## License

The converter is **GPL‑2.0** (inherited from p2r3/convert). filex embeds it as a
separate, iframe‑isolated service and never links its code, so filex stays
MIT‑licensed. Fork + build details:
[github.com/BRF-Tech/converter-filex](https://github.com/BRF-Tech/converter-filex).

---

## See also

- [CONFIGURATION.md](CONFIGURATION.md#external-services) — the `FILEX_CONVERT_URL` setting
- [INSTALLATION.md](INSTALLATION.md) · [`deploy/compose/`](../deploy/compose/) · [`deploy/helm/filex/`](../deploy/helm/filex/)
