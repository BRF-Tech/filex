// Raster copies of the product mark, for the places that cannot draw an SVG.
//
// ⚠⚠ Why this exists at all. `web/public/icons/icon.svg` is the one mark, and
// it is enough for the tab, the PWA manifest and any `<img>`. It is NOT enough
// for a NOTIFICATION: Chromium decodes a notification's `icon`/`badge` through
// its image decoders, which do not include SVG — so an SVG there is not a
// smaller logo or a blurry logo, it is NO logo, and the toast falls back to a
// generic bell with the origin under it. Measured as "the app name and the
// logo do not show up properly" on a real machine; Firefox draws the SVG fine,
// which is exactly why it survived so long.
//
// So: PNG, rasterised FROM the same SVG rather than drawn a second time, with
// Chromium (the browser the suite already installs) as the rasteriser. Run it
// when the mark changes:
//
//   node scripts/make-icon-pngs.mjs
//
// Outputs (committed, because a build must not need a browser):
//   web/public/icons/icon-192.png  — the notification's `icon`, PWA install
//   web/public/icons/icon-512.png  — large PWA / OS install surfaces
//   web/public/icons/badge-96.png  — the Android status-bar `badge`
//
// ⚠ The badge is a MONOCHROME mask on Android: everything but the alpha
// channel is thrown away, so a full-bleed square icon would show as a solid
// blob. It is rendered white-on-transparent for that reason, not as a
// stylistic choice.
import { chromium } from '@playwright/test';
import { mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const ROOT = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const SRC = resolve(ROOT, 'web/public/icons/icon.svg');
const OUT_DIR = resolve(ROOT, 'web/public/icons');

/** The badge drops the blue plate and paints the artwork white. */
function badgeSvg(svg) {
  return svg
    .replace('<rect width="512" height="512" fill="#2f6ceb"/>', '')
    .replaceAll('fill-opacity="0.14"', 'fill-opacity="0"');
}

const targets = [
  { name: 'icon-192.png', size: 192, transform: (s) => s },
  { name: 'icon-512.png', size: 512, transform: (s) => s },
  { name: 'badge-96.png', size: 96, transform: badgeSvg },
];

const svg = readFileSync(SRC, 'utf8');
mkdirSync(OUT_DIR, { recursive: true });

const browser = await chromium.launch();
try {
  for (const t of targets) {
    const page = await browser.newPage({
      viewport: { width: t.size, height: t.size },
      deviceScaleFactor: 1,
    });
    await page.setContent(
      `<!doctype html><meta charset="utf-8">` +
        `<style>html,body{margin:0;padding:0;background:transparent}` +
        `svg{display:block;width:${t.size}px;height:${t.size}px}</style>` +
        t.transform(svg),
      { waitUntil: 'load' },
    );
    const buf = await page.screenshot({ omitBackground: true, type: 'png' });
    writeFileSync(resolve(OUT_DIR, t.name), buf);
    await page.close();
    // eslint-disable-next-line no-console
    console.log(`${t.name}  ${t.size}x${t.size}  ${buf.length} bytes`);
  }
} finally {
  await browser.close();
}
