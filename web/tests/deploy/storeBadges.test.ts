// The Microsoft Store and Snap Store badges, on every surface that shows them.
//
// ⚠ Both are the stores' own artwork, and both sets of terms forbid changing
// it. Microsoft's badge guidelines: "Only use the artwork provided here. Never
// create your own badges or alter artwork in any way", at least 32 px tall on
// a screen, a quarter of the height kept clear around it, and a link to the
// product. Canonical licenses the Snap Store badge CC BY-ND 2.0 UK — no
// derivatives. So the files are pinned by digest: an edit (a recolour, a
// trimmed border, an "optimised" SVG) fails here instead of shipping.
//
// ⚠ One copy. `docs/badges/` is what the README and docs/DESKTOP.md show and
// what the public repository carries; filex.sh is deployed from `site/` alone,
// so `site/assets/badges/` is a mirror of it — scripts/sync-site-assets.mjs
// copies, siteAssets.test.ts compares.
//
// ⚠ Each badge must lead to its own listing, and the colour variant must
// suit the ground it sits on: `dark`/`black` on a light page, `light`/`white`
// on a dark one (Microsoft's own rule for its generator's two variants). A
// swapped pair is a black badge on a black page — legible to nobody, and
// invisible in any review done in the other colour scheme.
//
// When a store publishes a new badge: download it into docs/badges/, run
// `node scripts/sync-site-assets.mjs`, and update PINNED in the same commit.

import { createHash } from 'node:crypto';
import { existsSync, readFileSync, readdirSync } from 'node:fs';
import path from 'node:path';
import { describe, expect, it } from 'vitest';

const REPO = path.resolve(__dirname, '..', '..', '..');
const BADGES = path.join(REPO, 'docs', 'badges');

/** sha256 of each badge as the store serves it, line endings normalised to LF
 *  (Microsoft's files are CRLF; the repository stores text as LF, so a fresh
 *  checkout and the public export carry LF bytes of the same drawing).
 *  Sources: https://get.microsoft.com/images/en-us%20dark.svg and
 *  …%20light.svg (the images microsoft/app-store-badge serves);
 *  https://snapcraft.io/static/images/badges/en/snap-store-black.svg and
 *  …-white.svg (snapcore/snap-store-badges). Downloaded 2026-09-27. */
const PINNED: Record<string, string> = {
  'ms-store-dark.svg': 'e7e6f8b2df6b51d5343f86000b387b176145efb0ab82e2ef1954cc04f922e21e',
  'ms-store-light.svg': '8b7f0a12403ecda4827eece2f31a4419d5277a1cff61296fa9a699a163dbb422',
  'snap-store-black.svg': '047ed4b1c7487630a044df7fb570b0c2835db5280dda1bd856c3889023b3dc63',
  'snap-store-white.svg': '1deddae4115987244bdda164c2eebbac33127631c82c143f2280f379dadd4be3',
};

function digest(file: string): string {
  const text = readFileSync(file).toString('utf8').replace(/\r\n/g, '\n');
  return createHash('sha256').update(text, 'utf8').digest('hex');
}

const channel = readFileSync(path.join(REPO, 'desktop', 'src', 'channel.ts'), 'utf8');
const MSSTORE_ID = /msstore:\s*'([0-9A-Z]{12})'/.exec(channel)?.[1] ?? '';
const SNAP_NAME = /LINUX_APP_NAME\s*=\s*'([a-z0-9-]+)'/.exec(channel)?.[1] ?? '';

/** What each badge must say and where it must lead. `onLight` is the variant
 *  for a light page (the <img>), `onDark` the one for a dark page (the
 *  <source media="(prefers-color-scheme: dark)">). */
const KINDS = [
  {
    store: 'Microsoft Store',
    onLight: 'ms-store-dark.svg',
    onDark: 'ms-store-light.svg',
    alt: 'Download from the Microsoft Store',
    href: `https://apps.microsoft.com/detail/${MSSTORE_ID}`,
  },
  {
    store: 'Snap Store',
    onLight: 'snap-store-black.svg',
    onDark: 'snap-store-white.svg',
    alt: 'Get it from the Snap Store',
    href: `https://snapcraft.io/${SNAP_NAME}`,
  },
];

// `site/` is withheld from the public export; there the page is simply not a
// surface. README and DESKTOP.md are in both trees.
const SURFACES = ['README.md', 'docs/DESKTOP.md', 'site/index.html'].filter((f) =>
  existsSync(path.join(REPO, f)),
);

interface Badge {
  href: string;
  img: string;
  dark: string | null;
  alt: string;
  height: number | null;
}

/** Every `<a …><picture>…</picture></a>` on a page. */
function badgesOn(rel: string): Badge[] {
  const html = readFileSync(path.join(REPO, rel), 'utf8');
  const out: Badge[] = [];
  for (const m of html.matchAll(/<a\s[^>]*href="([^"]+)"[^>]*>\s*<picture>([\s\S]*?)<\/picture>\s*<\/a>/g)) {
    const inner = m[2];
    const img = /<img\s[^>]*src="([^"]+)"/.exec(inner)?.[1] ?? '';
    const alt = /<img\s[^>]*alt="([^"]*)"/.exec(inner)?.[1] ?? '';
    const height = /<img\s[^>]*height="(\d+)"/.exec(inner)?.[1];
    const dark = /<source\s[^>]*media="\(prefers-color-scheme:\s*dark\)"[^>]*srcset="([^"]+)"/.exec(inner)?.[1] ?? null;
    out.push({ href: m[1], img, dark, alt, height: height ? Number(height) : null });
  }
  return out;
}

describe("the store badges are the stores' own artwork", () => {
  it('reads the product ids it checks against', () => {
    expect(MSSTORE_ID, 'desktop/src/channel.ts no longer declares STORE_IDS.msstore').toMatch(/^[0-9A-Z]{12}$/);
    expect(SNAP_NAME, 'desktop/src/channel.ts no longer declares LINUX_APP_NAME').toBeTruthy();
  });

  it('docs/badges holds exactly the pinned files', () => {
    const files = existsSync(BADGES) ? readdirSync(BADGES).filter((f) => !f.startsWith('.')).sort() : [];
    expect(files).toEqual(Object.keys(PINNED).sort());
  });

  it.each(Object.entries(PINNED))('%s is unaltered', (name, sum) => {
    const file = path.join(BADGES, name);
    expect(existsSync(file), `docs/badges/${name} is missing`).toBe(true);
    expect(
      digest(file),
      `docs/badges/${name} is not the file the store publishes. The artwork may not be altered — ` +
        'restore the download (see the sources above PINNED), or, if the store published a new ' +
        'badge, update PINNED in the same commit.',
    ).toBe(sum);
  });
});

describe('every surface shows both badges, each leading to its listing', () => {
  it('finds the surfaces', () => {
    // README and DESKTOP.md exist in both trees; without them nothing below runs.
    expect(SURFACES).toEqual(expect.arrayContaining(['README.md', 'docs/DESKTOP.md']));
  });

  it.each(SURFACES)('%s', (rel) => {
    const badges = badgesOn(rel);
    for (const k of KINDS) {
      const mine = badges.filter((b) => path.posix.basename(b.img) === k.onLight);
      expect(mine.length, `${rel}: no ${k.store} badge (an <a><picture>…<img src=".../${k.onLight}">)`).toBe(1);
      const b = mine[0];
      expect(b.href, `${rel}: the ${k.store} badge must lead to the product's own listing`).toBe(k.href);
      expect(
        b.dark && path.posix.basename(b.dark),
        `${rel}: on a dark page the ${k.store} badge must switch to ${k.onDark}`,
      ).toBe(k.onDark);
      expect(b.alt, `${rel}: the ${k.store} badge's alt text must say what the badge says`).toBe(k.alt);
      // The files must resolve from where the page is served: next to the
      // README on GitHub, next to DESKTOP.md in docs/, under site/ for filex.sh.
      for (const src of [b.img, b.dark!]) {
        expect(src, `${rel}: a badge is hotlinked (${src}); serve the local copy`).not.toMatch(/^https?:/);
        const resolved = path.resolve(path.dirname(path.join(REPO, rel)), src);
        expect(existsSync(resolved), `${rel}: ${src} does not resolve to a file`).toBe(true);
      }
      if (b.height !== null) {
        expect(b.height, `${rel}: the ${k.store} badge is under the 32 px Microsoft allows`).toBeGreaterThanOrEqual(32);
      }
    }
  });
});

describe('the badges are never fetched from the stores', () => {
  // Hotlinked artwork is a request to a third party on every page view — on
  // filex.sh against its own CSP — and a picture that changes under us.
  const HOTLINK = /get\.microsoft\.com\/images|snapcraft\.io\/static\/images\/badges/;
  const pages = [
    'README.md',
    ...readdirSync(path.join(REPO, 'docs'))
      .filter((f) => f.endsWith('.md'))
      .map((f) => `docs/${f}`),
    'desktop/README.md',
    'site/index.html',
  ].filter((f) => existsSync(path.join(REPO, f)));

  it('has pages to scan', () => {
    expect(pages.length).toBeGreaterThan(10);
  });

  it.each(pages)('%s', (rel) => {
    expect(readFileSync(path.join(REPO, rel), 'utf8')).not.toMatch(HOTLINK);
  });
});

describe.skipIf(!existsSync(path.join(REPO, 'site', 'index.html')))('filex.sh draws them as the guidelines ask', () => {
  // ⚠ Guarded read: describe.skipIf still runs this callback.
  const site = existsSync(path.join(REPO, 'site', 'index.html'))
    ? readFileSync(path.join(REPO, 'site', 'index.html'), 'utf8')
    : '';

  it('at least 32 px tall, with a quarter of that height between them', () => {
    const height = Number(/\.install-badge img\{[^}]*height:(\d+)px/.exec(site)?.[1]);
    const gap = Number(/\.install-row\{[^}]*gap:(\d+)px/.exec(site)?.[1]);
    expect(height, '.install-badge img height').toBeGreaterThanOrEqual(32);
    expect(gap, '.install-row gap').toBeGreaterThanOrEqual(Math.ceil(height / 4));
  });

  it('credits the artwork it shows', () => {
    expect(site).toMatch(/trademarks of the\s+Microsoft group of companies/);
    expect(site).toMatch(/Canonical Ltd\.,\s+licensed/);
    expect(site).toContain('https://creativecommons.org/licenses/by-nd/2.0/uk/');
  });
});
