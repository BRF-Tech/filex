// Thumbnails that follow their files (0.50, task #129, GitHub #79): SVG
// drawn by the built-in engine, folders drawn with the files that came into
// them last (grid, gallery, list), text files as their first lines,
// transparency on a checkerboard, and Admin → Tools → Thumbnail repair.
//
//   node e2e/shots/thumbnails.mjs       (from the repo root; `pnpm shots` runs it)
//
// Writes docs/screenshots/<release>/thumbnails/:
//
//   folders-grid-1440.png     the explorer's grid over a storage whose files
//                             were put on its disk behind filex's back (a sync
//                             found them): each folder drawn with up to three
//                             of its pictures rising out of it, the SVG logos
//                             real thumbnails (their transparent corners on
//                             the checkerboard) on a host with no rsvg-convert
//   folders-grid-dark-1440.png the same grid in the dark theme
//   folders-gallery-1440.png  the gallery: each folder drawn large behind, its
//                             newest files fanned out in front of it
//   folders-list-1440.png     the list: each folder row's small folder with its
//                             newest file rising out of it
//   folder-peek-1440.png      the pointer resting on a folder: how many things
//                             are inside and the first few, with thumbnails
//   thumbnail-repair-1440.png Admin → Tools → Thumbnail repair after a Fix run
//                             of the whole storage: its counts, the thumbnail
//                             settings card (folder previews, SVG limits), and
//                             the files without a thumbnail with the reason (a
//                             map over the SVG size limit; a phone's HEIC
//                             photo where ImageMagick is not installed)
//
// ⚠ The storage is filled ON DISK and synced, not uploaded: that is the case
// #79 is about (files filex did not write get no thumbnail until something
// draws them). The repair draws them here, through the API, and the picture
// shows its result.
//
// Environment: FILEX_BIN, SHOTS_OUT, SHOTS_KEEP (see apps.mjs).

import { copyFileSync, mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import { join, resolve } from 'node:path';
import { chromium } from '@playwright/test';
import { addLocalStorage, bootInstance, client, index, log, mustSay, newContext, shot, signIn, sleep } from './scene.mjs';
import { encodePNG } from './fixtures.mjs';

const SET = 'thumbnails';
const ADMIN = { email: 'demo@demo.com', password: 'demo-shots' };
const REPO = resolve(import.meta.dirname, '..', '..');

/** A picture that reads as a picture at 184x108: a sky, a horizon, a sun. */
function scene(width, height, sky, ground, sun) {
  return encodePNG(width, height, (x, y) => {
    const h = y / height;
    const dx = x - width * sun[0];
    const dy = y - height * sun[1];
    if (dx * dx + dy * dy < (height * 0.11) ** 2) return [255, 214, 90];
    if (h > 0.62 + 0.05 * Math.sin(x / 37)) return ground.map((c) => Math.round(c * (0.8 + 0.2 * (1 - h))));
    return sky.map((c, i) => Math.round(c + (i === 2 ? 40 : 10) * (1 - h)));
  });
}

function svgBadge(label, fill) {
  return `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 240 160">
<rect width="240" height="160" rx="22" fill="${fill}"/>
<circle cx="64" cy="80" r="34" fill="#ffffff" fill-opacity="0.9"/>
<path d="M112 108 L150 52 L188 108 Z" fill="#ffffff" fill-opacity="0.75"/>
<text x="120" y="146" text-anchor="middle" font-family="sans-serif" font-size="18" fill="#ffffff">${label}</text>
</svg>
`;
}

/** An SVG of machine-made paths, over the 1 MB limit the scene sets. */
function bigMap() {
  let body = '<svg xmlns="http://www.w3.org/2000/svg" width="1000" height="600">';
  for (let i = 0; body.length < 1.3 * (1 << 20); i++) {
    body += `<path d="M${(i * 7) % 1000} ${(i * 13) % 600} l40 20 l-15 30 z" fill="#2a6f97" fill-opacity="0.2"/>`;
  }
  return body + '</svg>\n';
}

async function main() {
  const inst = await bootInstance({ name: SET, admin: ADMIN, env: { FILEX_UPDATE_CHECK: '0' } });
  const browser = await chromium.launch();
  try {
    const admin = client(inst.url);
    await admin.login(ADMIN.email, ADMIN.password);
    await admin.patch('/api/auth/profile', { locale: 'en', display_name: 'Dana Reyes' });

    // On disk, behind filex's back.
    const root = inst.storageRoot('photos');
    const dir = (p) => {
      const d = join(root, p);
      mkdirSync(d, { recursive: true });
      return d;
    };
    const summer = dir('Summer 2026');
    writeFileSync(join(summer, 'beach.png'), scene(480, 320, [120, 180, 230], [226, 200, 150], [0.75, 0.3]));
    writeFileSync(join(summer, 'forest.png'), scene(480, 320, [150, 190, 210], [60, 120, 70], [0.2, 0.25]));
    writeFileSync(join(summer, 'harbour.png'), scene(480, 320, [90, 140, 200], [40, 80, 120], [0.5, 0.35]));
    writeFileSync(join(summer, 'sunset.png'), scene(480, 320, [240, 150, 110], [70, 50, 60], [0.45, 0.55]));
    writeFileSync(join(summer, 'itinerary.txt'), 'Day 1 - arrive\nDay 2 - boat\n');
    const logos = dir('Logos');
    copyFileSync(join(REPO, 'e2e', 'fixtures', 'file-types', 'logo.svg'), join(logos, 'filex.svg'));
    writeFileSync(join(logos, 'harbour-club.svg'), svgBadge('Harbour Club', '#0f766e'));
    writeFileSync(join(logos, 'north-trail.svg'), svgBadge('North Trail', '#7c3aed'));
    const docs = dir('Minutes');
    writeFileSync(join(docs, 'april.txt'), 'Minutes, April\n');
    writeFileSync(join(docs, 'may.md'), '# Minutes, May\n');
    const maps = dir('Maps');
    writeFileSync(join(maps, 'coast.svg'), bigMap());
    writeFileSync(join(root, 'cover.png'), scene(640, 360, [110, 170, 225], [210, 190, 140], [0.8, 0.28]));
    // A phone's photo: HEIC, tiled the way a phone writes it (the pipeline's
    // own test photo). Drawn through ImageMagick where it is installed,
    // listed with the program it needs where it is not.
    const phone = dir('Phone');
    writeFileSync(join(phone, 'IMG_0142.heic'), readFileSync(join(REPO, 'backend', 'internal', 'thumb', 'testdata', 'grid.heic')));
    const caps = await admin.json('/api/files/capabilities');
    const heicDrawn = caps.thumbs?.imagemagick === true;

    const photos = await addLocalStorage(admin, 'Photos', root, { onHost: !inst.container });
    await admin.post(`/api/admin/storages/${photos.id}/sync`, {});
    for (let i = 0; i < 120; i++) {
      const runs = await admin.json(`/api/admin/storages/${photos.id}/sync-runs?limit=3`);
      if ((runs.entries ?? []).some((r) => r.finished_at)) break;
      await sleep(250);
    }

    // The SVG size limit lowered to 1 MB, so the map is over it; then a Fix
    // repair of the whole storage, followed to its end.
    await admin.patch('/api/admin/tools/thumbnails/settings', { svg_max_mb: 1 });
    let run = await admin.post('/api/admin/tools/thumbnails/repair', { path: 'Photos://', mode: 'fix' });
    for (let i = 0; run.running && i < 240; i++) {
      await sleep(500);
      run = await admin.json('/api/admin/tools/thumbnails/repair');
    }
    if (run.running) throw new Error('the repair did not end within 2 minutes');
    log(`repair: ${run.processed} looked at, ${run.ok} drawn, ${run.failed} failed, ${run.skipped} skipped`);
    if (run.failed) throw new Error(`the repair failed ${run.failed} file(s) — the picture would show a problem that is not the point`);
    const listed = await index(admin, 'Photos://');
    const summerRow = (listed.files ?? []).find((f) => f.basename === 'Summer 2026');
    if ((summerRow?.preview ?? []).length !== 3) throw new Error(`Summer 2026 shows ${(summerRow?.preview ?? []).length} pictures, not 3`);
    const logosRow = (listed.files ?? []).find((f) => f.basename === 'Logos');
    if ((logosRow?.preview ?? []).length !== 3) throw new Error('the three SVG logos are not on their folder: the built-in engine did not draw them');
    await admin.post('/api/notifications/read-all', {});

    const ctx = await newContext(browser, { height: 900 });
    const page = await ctx.newPage();
    await signIn(page, inst.url, ADMIN);

    // 1. The grid.
    await page.goto(`${inst.url}/admin/explore?path=${encodeURIComponent('Photos://')}`);
    await page.locator('.fe-grid__card, .fe-list__row').first().waitFor({ timeout: 20_000 });
    await page.evaluate(() => {
      const real = [...document.querySelectorAll('.fe-toolbar__view button')].filter(
        (b) => !b.closest('.fe-toolbar__measure') && !b.closest('[aria-hidden="true"]'),
      );
      real.find((b) => /grid/i.test(b.getAttribute('title') ?? ''))?.click();
    });
    await page.locator('.fe-grid.has-folder-previews').waitFor({ timeout: 15_000 });
    // Every print has its picture, not its fallback.
    await page.waitForFunction(() => document.querySelectorAll('.fe-fmosaic__print img').length >= 6, null, { timeout: 30_000 });
    await mustSay(page.locator('main'), 'the grid', ['Summer 2026', 'Logos', 'Minutes', 'Maps', 'Phone', 'cover.png']);
    await page.mouse.move(4, 4);
    await sleep(800);
    await shot(page, SET, 'folders-grid-1440.png');

    // 2. The peek.
    const summerCard = page.locator('.fe-grid__card--folder', { hasText: 'Summer 2026' });
    await summerCard.hover();
    const peek = page.locator('[data-fe-peek]');
    await peek.waitFor({ timeout: 10_000 });
    await page.waitForFunction(() => {
      const el = document.querySelector('[data-fe-peek]');
      return !!el && !el.textContent?.includes('Looking inside') && el.querySelectorAll('img').length >= 4;
    }, null, { timeout: 15_000 });
    await mustSay(peek, 'the peek', ['Summer 2026', '5 items', 'beach.png', 'itinerary.txt']);
    await sleep(400);
    await shot(page, SET, 'folder-peek-1440.png');
    await page.mouse.move(4, 4);
    await ctx.close();

    // 3. The same grid, dark. The theme is the account's answer, read once
    // there is a session: said after signing in, then the page reloads.
    const dctx = await newContext(browser, { scheme: 'dark', height: 900 });
    const dpage = await dctx.newPage();
    await signIn(dpage, inst.url, ADMIN);
    await dpage.evaluate(() => localStorage.setItem('filex.thememode', 'dark'));
    await dpage.goto(`${inst.url}/admin/explore?path=${encodeURIComponent('Photos://')}`);
    await dpage.locator('.fe-grid__card, .fe-list__row').first().waitFor({ timeout: 20_000 });
    await dpage.locator('.fe-grid.has-folder-previews').waitFor({ timeout: 15_000 });
    await dpage.waitForFunction(() => document.querySelectorAll('.fe-fmosaic__print img').length >= 6, null, { timeout: 30_000 });
    const dark = await dpage.evaluate(() => {
      const n = (getComputedStyle(document.querySelector('.fe')).backgroundColor.match(/\d+/g) ?? []).map(Number);
      return n.length >= 3 && (n[0] + n[1] + n[2]) / 3 < 128;
    });
    if (!dark) throw new Error('the dark grid came out light');
    await dpage.mouse.move(4, 4);
    await sleep(800);
    await shot(dpage, SET, 'folders-grid-dark-1440.png');
    await dctx.close();

    // 4. The same folders in the gallery and in the list.
    for (const [view, file] of [['gallery', 'folders-gallery-1440.png'], ['list', 'folders-list-1440.png']]) {
      const vctx = await newContext(browser, { height: 900 });
      const vpage = await vctx.newPage();
      await signIn(vpage, inst.url, ADMIN);
      await vpage.goto(`${inst.url}/admin/explore?path=${encodeURIComponent('Photos://')}`);
      await vpage.locator('.fe-grid__card, .fe-list__row, .fe-gal__card, [data-fe-path]').first().waitFor({ timeout: 20_000 });
      await vpage.evaluate((v) => {
        const re = new RegExp(v, 'i');
        const real = [...document.querySelectorAll('.fe-toolbar__view button')].filter(
          (b) => !b.closest('.fe-toolbar__measure') && !b.closest('[aria-hidden="true"]'),
        );
        real.find((b) => re.test(b.getAttribute('title') ?? ''))?.click();
      }, view);
      const shape = view === 'gallery' ? 'fan' : 'mini';
      await vpage.waitForFunction((cls) => document.querySelectorAll(`.fe-fmosaic--${cls}`).length >= 5, shape, { timeout: 20_000 });
      await vpage.waitForFunction(() => document.querySelectorAll('.fe-fmosaic__print img').length >= 3, null, { timeout: 30_000 });
      await vpage.mouse.move(4, 4);
      await sleep(800);
      await shot(vpage, SET, file);
      await vctx.close();
    }

    // 5. Admin → Tools → Thumbnail repair.
    const ctx2 = await newContext(browser, { height: 1300 });
    const page2 = await ctx2.newPage();
    await signIn(page2, inst.url, ADMIN);
    await page2.goto(`${inst.url}/admin/tools?tab=thumbnails`);
    await page2.getByTestId('thumb-repair-result').waitFor({ timeout: 20_000 });
    await page2.getByTestId('thumb-problem-reason').first().waitFor({ timeout: 20_000 });
    await mustSay(page2.locator('main'), 'the repair tab', [
      'Thumbnail repair', 'Last repair', 'Drawn', 'Thumbnail settings', 'Folder previews', 'SVG limits',
      'Files without a thumbnail', 'coast.svg', 'Larger than the SVG size limit (1 MB)',
      ...(heicDrawn ? [] : ['IMG_0142.heic', 'ImageMagick with HEIC support is not installed']),
    ]);
    await page2.mouse.move(4, 4);
    await sleep(600);
    await shot(page2, SET, 'thumbnail-repair-1440.png');
    await ctx2.close();
  } finally {
    await browser.close();
    await inst.stop();
  }
}

main().catch((err) => {
  console.error(err);
  process.exit(1);
});
