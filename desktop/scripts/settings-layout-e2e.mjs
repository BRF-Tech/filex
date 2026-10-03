// The settings cards hold their controls: no segmented strip runs off its card.
//
// 2026-09-25, the Snap store screenshots (1440×900, English): on Settings →
// Synced folders the "Download limit" strip cut "5 MB/s" at the card's right
// edge and the "When to sync" strip cut "Evenings & nights". The strip is
// `overflow: hidden` (for its rounded corners), which also lets a flex item
// shrink below its content, so beside a long description it shrank and
// clipped its last button. Turkish texts are longer, so Turkish is measured
// too, and the window's narrowest size (720 px, `minWidth`) as well as 1440.
//
// For every card with a segmented strip, at each size and language:
//   • the strip is not clipped: its content is no wider than its box, and no
//     button's label is wider than the button;
//   • every button ends inside the card (its padding included);
//   • the strip does not sit on its description: a strip that does not fit
//     beside the text goes UNDER it, never over it.
//
// Run: FILEX_EMAIL=… FILEX_PASSWORD=… node scripts/settings-layout-e2e.mjs

import path from 'node:path';
import fs from 'node:fs';
import { SHOTS, check, finish, launchApp, signIn, sleep } from './lib/harness.mjs';

fs.mkdirSync(SHOTS, { recursive: true });
const { app } = await launchApp({ lang: 'en-US' });

/** Every segmented card's geometry, measured in the page. */
const measure = (win) => win.evaluate(() => {
  const out = [];
  for (const strip of document.querySelectorAll('#settings .card .segmented')) {
    const card = strip.closest('.card');
    const row = strip.parentElement;
    const text = row?.firstElementChild && row.firstElementChild !== strip ? row.firstElementChild : null;
    const cs = getComputedStyle(card);
    const c = card.getBoundingClientRect();
    const inner = { left: c.left + parseFloat(cs.paddingLeft), right: c.right - parseFloat(cs.paddingRight) };
    const s = strip.getBoundingClientRect();
    const buttons = [...strip.querySelectorAll('button')].map((b) => {
      const r = b.getBoundingClientRect();
      return { label: (b.textContent || '').trim(), right: r.right, clipped: b.scrollWidth > b.clientWidth + 1 };
    });
    let overlap = false;
    if (text) {
      const t = text.getBoundingClientRect();
      overlap = !(s.right <= t.left || s.left >= t.right || s.bottom <= t.top || s.top >= t.bottom);
    }
    out.push({
      label: (card.querySelector('.label')?.textContent || '').trim(),
      stripClipped: strip.scrollWidth > strip.clientWidth + 1,
      outside: buttons.filter((b) => b.right > inner.right + 0.5).map((b) => b.label),
      labelsCut: buttons.filter((b) => b.clipped).map((b) => b.label),
      overlap,
    });
  }
  return out;
});

try {
  const { win } = await signIn(app, { label: 'filex desktop - settings layout e2e' });

  // Settings the way a user opens it: the gear in the rail.
  await win.evaluate(() => [...document.querySelectorAll('.rail-btn')].pop()?.click());
  await sleep(800);
  const opened = await win.evaluate(() => document.querySelector('#settings')?.classList.contains('open') === true);
  check('Settings opens', opened);

  const resize = (w, h) => app.evaluate(({ BrowserWindow }, [width, height]) => {
    const bw = BrowserWindow.getAllWindows().find((x) => x.webContents.getURL().startsWith('app://filex'));
    if (bw.isMaximized()) bw.unmaximize();
    bw.setSize(width, height);
  }, [w, h]);

  for (const lang of ['en', 'tr']) {
    await win.evaluate((l) => document.querySelector(`#settings [data-locale="${l}"]`)?.click(), lang);
    await sleep(1200);
    for (const [w, h] of [[720, 600], [1440, 900]]) {
      await resize(w, h);
      await sleep(700);
      const cards = await measure(win);
      check(`${lang} ${w}px: the segmented settings are on screen`, cards.length >= 4, `${cards.length} cards`);
      // The two rows of the report, named so a regression says which one.
      const want = lang === 'en' ? ['Download limit', 'When to sync'] : ['İndirme sınırı', 'Ne zaman eşitlensin'];
      for (const name of want) {
        check(`${lang} ${w}px: "${name}" is measured`, cards.some((c) => c.label === name),
          cards.map((c) => c.label).join(' | '));
      }
      for (const c of cards) {
        check(`${lang} ${w}px: "${c.label}" - the strip is not cut`,
          !c.stripClipped && c.labelsCut.length === 0,
          c.stripClipped ? 'the strip is narrower than its buttons' : `cut: ${c.labelsCut.join(', ')}`);
        check(`${lang} ${w}px: "${c.label}" - every button ends inside the card`,
          c.outside.length === 0, `outside: ${c.outside.join(', ')}`);
        check(`${lang} ${w}px: "${c.label}" - the strip does not sit on its description`, !c.overlap);
      }
      await win.screenshot({ path: path.join(SHOTS, `settings-layout-${lang}-${w}.png`) }).catch(() => {});
    }
  }
} catch (e) {
  check('flow completed', false, String(e && e.message).split('\n')[0]);
} finally {
  await app.close().catch(() => {});
}

finish();
