// Photographs the app's OWN chrome, surface by surface — gorunum:v2-desktop.
//
// The other look-* scripts photograph the FILE surfaces (the listing, the share
// dialog). This one exists for the opposite question: the explorer's look
// changed underneath the shell (gorunum:v1/v2 — new palette, Inter, the 13px
// scale, --fe-h-* control heights, the wide header), and the shell is the part
// the shared package cannot repaint. So every surface this window draws ITSELF
// gets its own photograph:
//
//   01 connect        the sign-in window, empty
//   02 waiting        the sign-in window, code half
//   03 files          the main window (rail + explorer)
//   04 rail           the rail, cropped
//   05 settings       the app's own settings surface, top
//   06 settings-mid   … scrolled to the sync/app rows
//   07 settings-end   … scrolled to updates
//   08 picker         the remote folder picker
//   09 connections    the shared storage-connections surface
//   10 boot-error     the "can't reach the server" surface
//   11 dark           the whole window in dark mode
//   12 settings-dark  the app's settings in dark mode
//
// Run:  node scripts/look-chrome.mjs
// Env:  FILEX_SERVER / FILEX_EMAIL / FILEX_PASSWORD (a local instance is fine),
//       LOOK_OUT (directory for the PNGs), LOOK_TAG (filename prefix).

import fs from 'node:fs';
import path from 'node:path';
import { SHOTS, _electron, launchApp, signIn, sleep } from './lib/harness.mjs';

const OUT = process.env.LOOK_OUT ?? SHOTS;
const TAG = process.env.LOOK_TAG ?? 'chrome';
fs.mkdirSync(OUT, { recursive: true });

const shot = async (page, name, opts = {}) => {
  const file = path.join(OUT, `${TAG}-${name}.png`);
  await page.screenshot({ path: file, timeout: 60_000, animations: 'disabled', ...opts });
  console.log(`  ${file}`);
};

const { app } = await launchApp();

try {
  // ── the sign-in window ────────────────────────────────────────────
  const connect = await app.firstWindow();
  await connect.waitForLoadState('domcontentloaded');
  await sleep(500);
  await shot(connect, '01-connect');

  const { win } = await signIn(app);
  await sleep(5000);

  // The tour is the explorer's, not the shell's — out of the way.
  await win.evaluate(() => {
    const skip = [...document.querySelectorAll('button')].find((b) =>
      /Turu atla|Skip|Atla/i.test(b.textContent ?? ''));
    skip?.click();
  });
  await sleep(1200);
  await shot(win, '03-files');

  const rail = await win.locator('#rail').boundingBox();
  if (rail) {
    await shot(win, '04-rail', {
      clip: { x: 0, y: 0, width: Math.ceil(rail.width) + 10, height: Math.min(460, Math.ceil(rail.height)) },
    });
  }

  // ── the app's own settings ────────────────────────────────────────
  await win.evaluate(() => {
    const btns = [...document.querySelectorAll('#rail .rail-btn')];
    btns[btns.length - 1].click();
  });
  await sleep(900);
  await shot(win, '05-settings');
  await win.evaluate(() => { document.querySelector('#settings').scrollTop = 620; });
  await sleep(400);
  await shot(win, '06-settings-mid');
  await win.evaluate(() => { const s = document.querySelector('#settings'); s.scrollTop = s.scrollHeight; });
  await sleep(400);
  await shot(win, '07-settings-end');

  // ── Turkish ───────────────────────────────────────────────────────
  // ⚠ Not a nicety. Every label on this surface exists in both languages, and
  // a half-translated settings page is the one defect a screenshot catches and
  // a test does not.
  await win.evaluate(() => { document.querySelector('#settings').scrollTop = 0; });
  await win.evaluate(() => document.querySelector('#settings [data-locale="tr"]')?.click());
  await sleep(1200);
  await shot(win, '05b-settings-tr');
  await win.evaluate(() => document.querySelector('#settings [data-locale="en"]')?.click());
  await sleep(1000);

  // ── the remote folder picker ──────────────────────────────────────
  await win.evaluate(() => { document.querySelector('#settings').scrollTop = 0; });
  await win.evaluate(() => document.querySelector('#add-sync')?.click());
  await sleep(1800);
  await shot(win, '08-picker');
  await win.evaluate(() => document.querySelector('#pk-close')?.click());
  await sleep(400);

  // ── what sync removed from this computer ──────────────────────────
  await win.evaluate(() => document.querySelector('#sync-trash')?.click());
  await sleep(1500);
  await shot(win, '14-trash');
  await win.evaluate(() => document.querySelector('#tr-close')?.click());
  await sleep(400);

  // ── storage connections (the shared component in the shell) ───────
  await win.evaluate(() => document.querySelector('#conn-open')?.click());
  await sleep(2500);
  await shot(win, '09-connections');
  await win.evaluate(() => {
    document.querySelector('#conn').classList.remove('open');
    document.querySelector('#explorer-host').style.display = 'flex';
  });
  await sleep(300);

  // ── dark mode ─────────────────────────────────────────────────────
  // Through the SAME preference the explorer's theme control writes, so the
  // photograph shows what a user gets, not what a devtools override gets.
  await win.evaluate(() => {
    localStorage.setItem('filex.thememode', 'dark');
    window.dispatchEvent(new StorageEvent('storage', { key: 'filex.thememode', newValue: 'dark' }));
  });
  await sleep(500);
  await win.reload();
  await sleep(6000);
  await win.evaluate(() => {
    const skip = [...document.querySelectorAll('button')].find((b) =>
      /Turu atla|Skip|Atla/i.test(b.textContent ?? ''));
    skip?.click();
  });
  await sleep(1000);
  await shot(win, '11-dark');
  await win.evaluate(() => {
    const btns = [...document.querySelectorAll('#rail .rail-btn')];
    btns[btns.length - 1].click();
  });
  await sleep(900);
  await shot(win, '12-settings-dark');

  // ── a palette, to prove the chrome follows it ─────────────────────
  await win.evaluate(() => {
    localStorage.setItem('filex.palette', 'forest');
    window.dispatchEvent(new StorageEvent('storage', { key: 'filex.palette', newValue: 'forest' }));
  });
  await sleep(400);
  await win.reload();
  await sleep(6000);
  await win.evaluate(() => {
    const skip = [...document.querySelectorAll('button')].find((b) =>
      /Turu atla|Skip|Atla/i.test(b.textContent ?? ''));
    skip?.click();
    const btns = [...document.querySelectorAll('#rail .rail-btn')];
    btns[btns.length - 1].click();
  });
  await sleep(1200);
  await shot(win, '13-palette-forest');
} catch (e) {
  console.log('FAILED:', String(e && e.stack).split('\n').slice(0, 4).join('\n'));
} finally {
  await app.close().catch(() => {});
}

// ── the waiting half of the sign-in, on a throwaway instance ────────
// Photographed on its own run because a successful sign-in CLOSES this window,
// so the surface cannot be caught on the way past.
try {
  const second = await launchApp();
  const c = await second.app.firstWindow();
  await c.waitForLoadState('domcontentloaded');
  await c.locator('#server').fill(process.env.FILEX_SERVER ?? 'http://127.0.0.1:5399');
  await c.locator('#go').click();
  await c.locator('#authurl').waitFor({ timeout: 15_000 });
  await sleep(600);
  await shot(c, '02-waiting');
  await second.app.close().catch(() => {});
} catch (e) {
  console.log('waiting-shot FAILED:', String(e && e.message).split('\n')[0]);
}

// ── the "can't reach the server" boot surface ───────────────────────
//
// ⚠ Only when the operator can actually take a server away, which is why it is
// behind an env var instead of being faked. The first attempt monkey-patched
// `window.filexApp.storages` in the page and then reloaded — throwing the patch
// away with the document, so the shot came back showing a perfectly healthy
// file list. A surface photographed by faking its failure is not a photograph
// of that surface.
//
// Point LOOK_DEAD_SERVER at an instance you are willing to stop: sign-in runs
// against it, then this waits LOOK_DEAD_PAUSE ms for you to kill it.
if (process.env.LOOK_DEAD_SERVER) try {
  const third = await launchApp({ env: { FILEX_SERVER: process.env.LOOK_DEAD_SERVER } });
  const { win } = await signIn(third.app, { label: 'filex desktop — look (boot)' });
  const pause = Number(process.env.LOOK_DEAD_PAUSE ?? 15000);
  console.log(`  stop ${process.env.LOOK_DEAD_SERVER} now — waiting ${pause}ms`);
  await sleep(pause);
  await win.reload();
  await sleep(5000);
  await shot(win, '10-boot-error');
  await third.app.close().catch(() => {});
} catch (e) {
  console.log('boot-error-shot FAILED:', String(e && e.message).split('\n')[0]);
}

console.log('\nthe point of this script is the PNGs — look at them.');
