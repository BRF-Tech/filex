// A tall picture in the image viewer, at the width of the window (filex 0.57.7,
// PR #131: a long scan or screenshot shrank to a sliver that fit the whole
// window, and its text could not be read):
//
//   node e2e/shots/viewer.mjs     (from the repo root; `pnpm shots` runs it)
//
// Writes e2e/.artifacts/shots/capture/viewer/ (or SHOTS_OUT):
//
//   image-fit-width-1280.png  a 1080 × 7200 page opened, Fit width pressed:
//                             the page as wide as the viewer, scrolling down
//
// Before the picture the scene measures what the controls claim: the page
// opens fitted to the window, Fit width fills the viewer's width and scrolls,
// the page's last line clears the floating controls at the bottom, Original
// size draws it at its own 1080 pixels, and the percentage goes back to the
// window. Nothing outside this machine is reached.
//
// Environment: FILEX_BIN, SHOTS_OUT, SHOTS_KEEP (see apps.mjs).

import { mkdtempSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { chromium } from '@playwright/test';
import { pinTimes } from './clock.mjs';
import { encodePNG } from './fixtures.mjs';
import { addLocalStorage, bootInstance, client, dismissToasts, log, newContext, shot, signIn, sleep, uploadTree } from './scene.mjs';

const SET = 'viewer';
const ADMIN = { email: 'demo@demo.com', password: 'demo-shots' };
const FILE = 'Meeting notes (scan).png';
const WIDTH = 1080;
const HEIGHT = 7200;

// A scanned page of notes: a heading, then paragraphs of grey "words" on
// paper. Readable as text at its own width, a sliver of lines when the whole
// page is fitted into the window - the case the controls are for.
function notesPage() {
  const margin = 96;
  const top = 150;
  const pitch = 34;
  const band = [11, 23];
  const lines = [];
  let seed = 7;
  const rand = () => {
    seed = (seed * 1103515245 + 12345) % 2147483648;
    return seed / 2147483648;
  };
  for (let y = top; y + pitch < HEIGHT - margin; y += pitch) {
    const n = lines.length;
    const ink = new Uint8Array(WIDTH);
    if (n % 10 !== 9) {
      const end = n % 10 === 8 ? margin + (WIDTH - 2 * margin) * (0.3 + rand() * 0.4) : WIDTH - margin;
      let x = margin + (n % 10 === 0 ? 40 : 0);
      while (x < end) {
        const word = 18 + Math.floor(rand() * 90);
        for (let i = x; i < Math.min(x + word, end); i++) ink[i] = 1;
        x += word + 12;
      }
    }
    lines.push(ink);
  }
  return encodePNG(WIDTH, HEIGHT, (x, y) => {
    if (y >= 64 && y < 96 && x >= margin && x < margin + 520) return [32, 58, 110];
    const n = Math.floor((y - top) / pitch);
    const within = (y - top) % pitch;
    if (y >= top && n < lines.length && within >= band[0] && within < band[1] && lines[n][x]) return [92, 96, 104];
    return [250, 249, 245];
  });
}

async function main() {
  const inst = await bootInstance({ name: SET, admin: ADMIN, env: { FILEX_UPDATE_CHECK: '0' } });
  const browser = await chromium.launch();
  const seed = mkdtempSync(join(tmpdir(), 'filex-shots-viewer-'));
  try {
    const admin = client(inst.url);
    await admin.login(ADMIN.email, ADMIN.password);
    await admin.patch('/api/auth/profile', { locale: 'en', display_name: 'Dana Reyes' });
    writeFileSync(join(seed, FILE), notesPage());
    pinTimes(seed);
    await addLocalStorage(admin, 'Scans', inst.storageRoot('scans'), { onHost: !inst.container });
    await uploadTree(admin, 'Scans://', seed);

    const ctx = await newContext(browser, { width: 1280, height: 860 });
    const page = await ctx.newPage();
    await signIn(page, inst.url, ADMIN);
    await page.goto(`${inst.url}/admin/explore?storage=Scans`);
    const row = page.locator(`[data-fe-path="Scans://${FILE}"]`).first();
    await row.waitFor({ timeout: 25_000 });
    await sleep(700);
    await row.dblclick();

    const img = page.locator('img.fe-preview__image').first();
    await img.waitFor({ state: 'visible', timeout: 30_000 });
    await page.waitForFunction(() => {
      const el = document.querySelector('img.fe-preview__image');
      return !!el && el.complete && el.naturalWidth > 0;
    }, null, { timeout: 30_000 });
    const fitWidth = page.getByRole('button', { name: 'Fit width', exact: true });
    const original = page.getByRole('button', { name: 'Original size', exact: true });
    await fitWidth.waitFor({ state: 'visible', timeout: 10_000 });

    // The page, its scroller and the floating controls, as the viewer draws them.
    const measure = () => page.evaluate(() => {
      const el = document.querySelector('img.fe-preview__image');
      let scroller = el.parentElement;
      while (scroller && scroller.scrollHeight <= scroller.clientHeight + 1) scroller = scroller.parentElement;
      const pill = document.querySelector('.fe-viewer__zoom').getBoundingClientRect();
      const box = el.getBoundingClientRect();
      return {
        width: box.width,
        bottom: box.bottom,
        pillTop: pill.top,
        stage: el.parentElement.clientWidth,
        scrolls: !!scroller && scroller !== document.documentElement && scroller !== document.body,
      };
    });
    const opened = await measure();
    if (opened.width > 200) throw new Error(`the page opened ${Math.round(opened.width)} px wide: it should open fitted to the window`);

    await fitWidth.click();
    await sleep(400);
    if ((await fitWidth.getAttribute('aria-pressed')) !== 'true') throw new Error('Fit width is not shown as the selected mode');
    const wide = await measure();
    if (wide.width < wide.stage * 0.9) throw new Error(`Fit width drew the page ${Math.round(wide.width)} px wide in a ${wide.stage} px viewer`);
    if (!wide.scrolls) throw new Error('at Fit width the page does not scroll: its lower part cannot be reached');

    await dismissToasts(page);
    await page.mouse.move(4, 4);
    await sleep(600);
    await shot(page, SET, 'image-fit-width-1280.png');

    // The foot of the page clears the controls that float over it.
    await page.evaluate(() => {
      let s = document.querySelector('img.fe-preview__image').parentElement;
      while (s && s.scrollHeight <= s.clientHeight + 1) s = s.parentElement;
      s.scrollTop = s.scrollHeight;
    });
    await sleep(400);
    const foot = await measure();
    if (foot.bottom > foot.pillTop + 1) throw new Error(`scrolled to the end, the page's foot (${Math.round(foot.bottom)}) is still under the controls (${Math.round(foot.pillTop)})`);

    await original.click();
    await sleep(400);
    const natural = await measure();
    if (Math.abs(natural.width - WIDTH) > 1) throw new Error(`Original size drew the page ${Math.round(natural.width)} px wide, not its own ${WIDTH}`);

    await page.locator('[title="Reset zoom"]').click();
    await sleep(400);
    const back = await measure();
    if (Math.abs(back.width - opened.width) > 1) throw new Error(`the percentage did not take the page back to the window: ${Math.round(back.width)} px, opened at ${Math.round(opened.width)}`);
    if ((await fitWidth.getAttribute('aria-pressed')) !== 'false' || (await original.getAttribute('aria-pressed')) !== 'false') {
      throw new Error('after the reset a mode is still shown as selected');
    }
    log(`a ${WIDTH} × ${HEIGHT} page: fitted ${Math.round(opened.width)} px, Fit width ${Math.round(wide.width)} px and scrolling, Original size ${Math.round(natural.width)} px`);
    await ctx.close();
  } finally {
    await browser.close();
    await inst.stop();
    rmSync(seed, { recursive: true, force: true });
  }
}

main().catch((err) => {
  console.error('✗', err.stack ?? err.message);
  process.exit(1);
});
