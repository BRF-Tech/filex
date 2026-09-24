// Keep the signing faces as FILES rather than as base64 inside style.css.
//
// ⚠⚠ Vite's library mode inlines every asset a stylesheet references, with no
// way to opt out: `shouldInline` answers `true` for `build.lib` BEFORE it ever
// looks at `assetsInlineLimit` (vite 5.4, `dep-*.js` → shouldInline). Measured
// on this tree: the five `@font-face` faces (~440 KB of woff2) came back as
// ~590 KB of base64 and took `style.css` from 208 KB to 796 KB — a cost every
// host of the package pays on every page, signing screen or not, the fishapp
// PWA on a phone included.
//
// As files they cost NOTHING until a face is used: a browser fetches a
// `@font-face` source only when text is actually laid out in it.
//
// ⚠⚠ ONE plugin for BOTH library builds. `@brftech/filex-core` and
// `@brftech/filex` (the web component) ship the same stylesheet, and
// `web/tests/deploy/packageLook.test.ts` compares them BYTE FOR BYTE — so a
// copy of this in one config and not the other is not a style difference, it
// is a red build. A second copy that drifts is the same failure a week later.
//
// ⚠ The rewritten URLs are relative to `style.css` (`./fonts/<name>.woff2`),
// so a consumer must copy `dist/` whole — which the vendoring step (`npm
// pack`, minus the maps) already does.

import { copyFileSync, mkdirSync, readFileSync, readdirSync } from 'node:fs';
import { resolve } from 'node:path';

const DATA_URI = /url\(data:font\/woff2;base64,([A-Za-z0-9+/=]+)\)/g;

/**
 * @param {string} fontDir  where the `.woff2` sources live
 * @param {string} outDir   the build's `dist` (the stylesheet's own folder)
 * @returns {import('vite').Plugin}
 */
export function fontsAsFiles(fontDir, outDir) {
  const byB64 = new Map();
  for (const name of readdirSync(fontDir)) {
    if (name.endsWith('.woff2')) byB64.set(readFileSync(resolve(fontDir, name)).toString('base64'), name);
  }
  return {
    name: 'filex-fonts-as-files',
    apply: 'build',
    enforce: 'post',
    generateBundle(_options, bundle) {
      let made = false;
      for (const [fileName, out] of Object.entries(bundle)) {
        if (out.type !== 'asset' || !fileName.endsWith('.css')) continue;
        out.source = String(out.source).replace(DATA_URI, (whole, b64) => {
          const name = byB64.get(b64);
          // ⚠ An unknown blob stays inlined rather than being dropped: a face
          // that reaches the stylesheet by another route must still work.
          if (!name) return whole;
          if (!made) {
            mkdirSync(resolve(outDir, 'fonts'), { recursive: true });
            made = true;
          }
          copyFileSync(resolve(fontDir, name), resolve(outDir, 'fonts', name));
          return `url(./fonts/${name})`;
        });
      }
    },
  };
}
