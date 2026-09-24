# Signing fonts — self-hosted, no CDN

The five typefaces a `pdf-fields` text field and a `signature-pad` "type"
signature may be written in (docs/APP-PLUGINS-API.md → `pdf-fields` props).

⚠⚠ **Nothing here is fetched from a CDN at runtime.** A signature screen is
the one place in filex where a third party would learn, from the mere request
for a font file, that somebody is signing a document — and an air-gapped
install would simply render the signature in the wrong face. The `@font-face`
rules live in `packages/core/src/styles/sign-fonts.css`, which points at the
`.woff2` files in this folder; the bundler emits them next to `style.css`, so
every host of `@brftech/filex-core` (the admin SPA, fm.example.com, the desktop
app, the work.example.com / fishapp embeds) gets the same five faces without any
host-side wiring.

⚠ The browser fetches a `.woff2` only when text is actually drawn in it
(`@font-face` + `unicode-range` are lazy), so the folder costs nothing until
somebody opens a signing screen.

| Key (wire `font`) | Family | Kind | Subsets shipped | Licence |
|---|---|---|---|---|
| `caveat` | Caveat | handwriting | latin, latin-ext | OFL 1.1 |
| `dancing-script` | Dancing Script | handwriting | latin, latin-ext | OFL 1.1 |
| `homemade-apple` | Homemade Apple | handwriting | latin ⚠ | Apache 2.0 |
| `inter` | the interface face (`--fe-font`) | formal | — (no file) | — |
| `source-serif` | Source Serif 4 | formal | latin, latin-ext | OFL 1.1 |

⚠ **Homemade Apple ships latin only** — upstream publishes no latin-ext
subset, so `ş ğ İ` in a Turkish name fall back to the interface face inside
that one signature. It is listed anyway because it is the most
handwriting-looking of the three; `caveat` and `dancing-script` carry the full
Turkish alphabet and are the ones to prefer for a Turkish signer.

⚠ `inter` is deliberately **not** a file: `--fe-font` is already
`Inter, system-ui, …`, so the "formal, matches the rest of the app" choice is
the app's own face and follows an operator who rebrands it.

## Copyright

- Caveat — Copyright 2014 The Caveat Project Authors
  (https://github.com/googlefonts/caveat)
- Dancing Script — Copyright 2016 The Dancing Script Project Authors
  (https://github.com/googlefonts/DancingScript), with Reserved Font Name
  'Dancing Script'
- Source Serif 4 — Copyright 2014 The Source Serif 4 Project Authors
  (https://github.com/adobe-fonts/source-serif)
- Homemade Apple — Copyright (c) 2010 by Font Diner, Inc. All rights reserved.

`LICENSE-OFL-1.1.txt` covers the first three, `LICENSE-Apache-2.0.txt` the
fourth.

## Refreshing them

The `.woff2` files are the subsets Google Fonts serves (`css2?family=…`, a
desktop Chrome user agent, `latin` + `latin-ext` blocks). To refresh, fetch
the stylesheet for the family, take the `src: url(…)` of those two blocks and
save them here under the same names — then check the `unicode-range` values in
`sign-fonts.css` still match what the stylesheet declares.
