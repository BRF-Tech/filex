// Appearance — an operator makes filex look like their own product.
//
//   node e2e/shots/appearance.mjs        (from the repo root; `pnpm shots` runs it)
//
// Writes docs/screenshots/<release>/appearance/:
//
//   theme-editor-1440.png     Admin → Appearance: a theme being composed, its
//                             live preview painted by the draft
//   themed-explorer-1440.png  the same theme as the instance default, worn by
//                             the explorer of somebody who never picked a palette
//   themed-signin-1440.png    …and by the sign-in page, where nobody is signed in
//                             yet: a palette that stops at the login is not
//                             branding
//
// Environment: FILEX_BIN, SHOTS_OUT, SHOTS_KEEP (see apps.mjs).

import { mkdtempSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { chromium } from '@playwright/test';
import { seedFixtures } from './fixtures.mjs';
import { addLocalStorage, bootInstance, client, log, newContext, shot, signIn, sleep, uploadTree, waitForThumbs } from './scene.mjs';

const SET = 'appearance';
const ADMIN = { email: 'demo@demo.com', password: 'demo-shots' };

// A brand nothing in the stock palettes is near, so the pictures cannot be
// mistaken for filex's own colours: a deep evergreen on warm paper.
const THEME = {
  name: 'Northwind',
  key: 'northwind',
  radius: '12',
  light: {
    '--fe-bg': '#f7f4ee',
    '--fe-bg-elev': '#fffdf9',
    '--fe-border': '#e2dccf',
    '--fe-text': '#1d2a24',
    '--fe-text-muted': '#5d6b63',
    '--fe-primary': '#1f6f5c',
    '--fe-primary-hover': '#185a4a',
    '--fe-primary-soft': '#e1efe9',
    '--fe-primary-ink': '#134637',
  },
};

async function main() {
  const inst = await bootInstance({ name: SET, admin: ADMIN, env: { FILEX_UPDATE_CHECK: '0' } });
  const browser = await chromium.launch();
  const seed = mkdtempSync(join(tmpdir(), 'filex-shots-appearance-seed-'));
  try {
    const admin = client(inst.url);
    await admin.login(ADMIN.email, ADMIN.password);
    await admin.patch('/api/auth/profile', { locale: 'en', display_name: 'Demo' });

    // Something to look at in the explorer: the screenshot world, uploaded
    // through filex so its photos have thumbnails.
    seedFixtures(seed);
    await addLocalStorage(admin, 'demo', join(inst.files, 'demo'));
    await uploadTree(admin, 'demo://', seed);
    await waitForThumbs(admin, 'demo://Photos', 6);

    // ── 1. composing the theme ───────────────────────────────────────────
    const ctx = await newContext(browser, { height: 1000 });
    const page = await ctx.newPage();
    await signIn(page, inst.url, ADMIN);
    await page.goto(`${inst.url}/admin/appearance`);
    await page.getByTestId('appearance-page').waitFor();
    await page.getByTestId('theme-name').locator('input').fill(THEME.name);
    await page.getByTestId('theme-key').locator('input').fill(THEME.key);
    for (const [token, value] of Object.entries(THEME.light)) {
      await page.getByTestId(`tokhex-${token}`).fill(value);
    }
    await page.getByTestId('theme-radius').locator('input').fill(THEME.radius);
    // The preview is painted by the draft — wait for it to wear the accent.
    await page.waitForFunction(
      (c) => (document.querySelector('[data-testid="theme-preview"]')?.getAttribute('style') ?? '').includes(c),
      THEME.light['--fe-primary'],
    );
    await page.mouse.move(0, 0);
    // ⚠ Filling the last field scrolls the page to IT, and where that leaves
    // the frame is an accident: the first take had the section heading above
    // sliced in half by the sticky bar and the page's own buttons cut off
    // behind it. Frame the picture on purpose instead — the card being
    // composed, flush under the bar, with the live preview beside it — and
    // then MEASURE that nothing is half-covered, because a scroll that lands
    // wrong looks like a broken page rather than a failed script.
    const clipped = await page.evaluate(() => {
      const form = document.querySelector('[data-testid="theme-name"]')?.closest('form');
      if (!form) return 'no composing form on the page';
      const scroller = (() => {
        for (let el = form.parentElement; el && el !== document.body; el = el.parentElement) {
          const s = getComputedStyle(el);
          if (/(auto|scroll)/.test(s.overflowY) && el.scrollHeight > el.clientHeight + 4) return el;
        }
        return document.scrollingElement ?? document.documentElement;
      })();
      const bar = document.querySelector('header');
      const barBottom = bar ? bar.getBoundingClientRect().bottom : 0;
      scroller.scrollBy(0, form.getBoundingClientRect().top - barBottom - 16);
      const top = form.getBoundingClientRect().top;
      return top >= barBottom - 1 ? '' : `the composing card starts ${Math.round(barBottom - top)}px under the sticky bar`;
    });
    if (clipped) throw new Error(`theme-editor-1440.png would be framed wrong: ${clipped}`);
    await sleep(500);
    await shot(page, SET, 'theme-editor-1440.png');
    await page.getByTestId('theme-save').click();
    // Saved means served: the public palette list carries it.
    for (let i = 0; i < 40; i++) {
      const body = await admin.json('/api/appearance');
      if ((body.themes ?? []).some((t) => t.id === `custom:${THEME.key}`)) break;
      if (i === 39) throw new Error('the saved theme never reached /api/appearance');
      await sleep(250);
    }
    await ctx.close();

    // ── 2. the instance wears it ─────────────────────────────────────────
    await admin.patch('/api/admin/settings', { 'ui.default_theme': `custom:${THEME.key}` });
    // The uploads above each rang the bell; a picture about colours should not
    // carry a dozen unread notices in its header.
    await admin.post('/api/notifications/read-all', {});

    // A fresh session: this account never picked a palette, so the
    // instance's default is what paints it.
    const ectx = await newContext(browser);
    const epage = await ectx.newPage();
    await signIn(epage, inst.url, ADMIN);
    await epage.goto(`${inst.url}/admin/explore?storage=demo`);
    const photos = epage.locator('[data-fe-path="demo://Photos"]').first();
    await photos.waitFor({ timeout: 25_000 });
    await photos.dblclick();
    await epage.locator('[data-fe-path="demo://Photos/aurora.png"]').first().waitFor({ timeout: 15_000 });
    await epage.getByTestId('view-grid').click();
    await epage.waitForFunction(
      (c) => getComputedStyle(document.documentElement).getPropertyValue('--fe-primary').trim() === c,
      THEME.light['--fe-primary'],
    );
    await epage.mouse.move(0, 0);
    await sleep(1200);
    await shot(epage, SET, 'themed-explorer-1440.png');
    await ectx.close();

    // ── 3. …and so does the page before anybody signs in ────────────────
    const lctx = await newContext(browser);
    const lpage = await lctx.newPage();
    await lpage.goto(`${inst.url}/admin/login`);
    await lpage.locator('#password').waitFor();
    await lpage.waitForFunction(
      (c) => getComputedStyle(document.documentElement).getPropertyValue('--fe-primary').trim() === c,
      THEME.light['--fe-primary'],
    );
    await sleep(600);
    await shot(lpage, SET, 'themed-signin-1440.png');
    await lctx.close();
    log('the theme was worn by the explorer and the sign-in page');
  } finally {
    rmSync(seed, { recursive: true, force: true });
    await browser.close();
    await inst.stop();
  }
}

main().catch((err) => {
  console.error('✗', err.stack ?? err.message);
  process.exit(1);
});
