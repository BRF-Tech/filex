// Lists every control the WINDOW offers, from both halves, so "does this app
// have two controls for one job" is answered by reading the two lists rather
// than by remembering.
//
// Half one: the explorer's own header (gorunum:v2 — search, refresh, AI, "⋯",
// and the `header-actions` slot the embedder fills). Half two: the shell's rail
// and its settings surface.
//
// Run: node scripts/diag-duplicates.mjs  (same env as look-chrome.mjs)

import path from 'node:path';
import { SHOTS, launchApp, signIn, sleep } from './lib/harness.mjs';

const OUT = process.env.LOOK_OUT ?? SHOTS;
const TAG = process.env.LOOK_TAG ?? 'dup';

const { app } = await launchApp();
try {
  const { win } = await signIn(app);
  await sleep(5000);
  await win.evaluate(() => {
    const skip = [...document.querySelectorAll('button')].find((b) =>
      /Turu atla|Skip|Atla/i.test(b.textContent ?? ''));
    skip?.click();
  });
  await sleep(900);

  const header = await win.evaluate(() => {
    const tail = document.querySelector('.fe-toolbar__tail');
    return {
      tailChildren: tail
        ? [...tail.children].map((c) => ({
            tag: c.tagName.toLowerCase(),
            title: c.getAttribute('title') ?? c.getAttribute('aria-label') ?? '',
            testid: c.getAttribute('data-testid') ?? '',
            disabled: c.hasAttribute('disabled'),
          }))
        : null,
      // Anything the host put in the slot would be a trailing element with no
      // fe- class; an embed that fills nothing leaves the cluster at 3.
      headerActionsFilled: tail
        ? [...tail.children].filter((c) => !c.className.toString().includes('fe-')).length
        : 0,
    };
  });
  console.log('HEADER TAIL:', JSON.stringify(header, null, 2));

  // The "⋯" menu — where density and theme live in every profile.
  await win.evaluate(() => document.querySelector('[data-testid="drive-more"]')?.click());
  await sleep(700);
  const more = await win.evaluate(() =>
    [...document.querySelectorAll('.fe-ctx__item, [role="menuitem"]')]
      .map((e) => (e.textContent ?? '').trim())
      .filter(Boolean));
  console.log('MORE MENU:', JSON.stringify(more, null, 2));
  await win.screenshot({ path: path.join(OUT, `${TAG}-more-menu.png`), timeout: 60_000 });
  await win.keyboard.press('Escape');
  await sleep(400);

  const rail = await win.evaluate(() =>
    [...document.querySelectorAll('#rail > *')].map((e) => ({
      cls: e.className,
      title: e.getAttribute('title') ?? (e.querySelector('button')?.getAttribute('title') ?? ''),
    })));
  console.log('RAIL:', JSON.stringify(rail, null, 2));

  await win.evaluate(() => {
    const b = [...document.querySelectorAll('#rail .rail-btn')];
    b[b.length - 1].click();
  });
  await sleep(900);
  const settings = await win.evaluate(() => {
    const s = document.querySelector('#settings');
    return {
      headings: [...s.querySelectorAll('h1,h2')].map((h) => h.textContent.trim()),
      controls: [...s.querySelectorAll('button')].map((b) => b.textContent.trim()).filter(Boolean),
    };
  });
  console.log('SETTINGS:', JSON.stringify(settings, null, 2));
} catch (e) {
  console.log('ERR', e.stack?.split('\n').slice(0, 3).join(' | '));
} finally {
  await app.close().catch(() => {});
}
