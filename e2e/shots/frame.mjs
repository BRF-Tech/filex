// Framing a picture on what it is of: nothing it shows cut at its foot.
//
// ⚠ A viewport picture ends wherever the window ends, and a page, a panel or
// a dialog that is taller than the window ends there mid-sentence: the 0.56
// README had a connection guide stopping half way through its last line, the
// demo landing's "Also included" paragraph sliced in two, and the hero's
// navigation panel showing the Connections heading with both of its entries
// scrolled out of view under it - which reads as a heading over nothing. Each
// was a picture of a window that was too short, not of a product that was
// wrong. These two helpers make the window fit the picture instead:
//
//   growUntilShown(page, ['document', '.fe-overlay__card'])
//       grows the window until none of those scrollers hides anything, and
//       refuses (throws) when that would take more than `max` px;
//   endBelow(page, '<selector>')
//       shrinks or grows the window so the picture ends just under one element:
//       the thing it is of, and not the top edge of the next one.
//
// Both change only the window's height. Put the window back afterwards
// (`page.setViewportSize(before)`) when the scene goes on to other pictures.

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

/** How many px each scroller hides below its fold ('document' = the page). */
async function hiddenBelow(page, selectors) {
  return page.evaluate((sels) => {
    const out = {};
    for (const sel of sels) {
      const el = sel === 'document' ? (document.scrollingElement ?? document.documentElement) : document.querySelector(sel);
      out[sel] = el ? Math.max(0, Math.ceil(el.scrollHeight - el.clientHeight)) : -1;
    }
    return out;
  }, selectors);
}

/**
 * Grows the window until no scroller in `selectors` hides content, measured
 * again after every step (a taller window can wrap text differently).
 * Returns the window's new height. A selector that matches nothing is an
 * error: the picture would be framed on a guess.
 */
export async function growUntilShown(page, selectors, { max = 2600, what = 'the picture' } = {}) {
  for (let i = 0; i < 6; i++) {
    const hidden = await hiddenBelow(page, selectors);
    const missing = Object.entries(hidden).filter(([, px]) => px < 0).map(([sel]) => sel);
    if (missing.length) throw new Error(`${what}: nothing on the page matches ${missing.join(', ')}`);
    const most = Math.max(...Object.values(hidden));
    const view = page.viewportSize();
    if (!view) throw new Error(`${what}: the page has no viewport to grow`);
    if (most <= 1) return view.height;
    const height = view.height + most;
    if (height > max) {
      throw new Error(`${what} would need a ${height}px window to show all of it (${JSON.stringify(hidden)}), more than ${max}px`);
    }
    await page.setViewportSize({ width: view.width, height });
    await sleep(350);
  }
  throw new Error(`${what}: still hides content after six steps (${JSON.stringify(await hiddenBelow(page, selectors))})`);
}

/**
 * Sets the window's height so the picture ends `pad` px under `selector`'s
 * bottom edge. The element must start inside the window.
 */
export async function endBelow(page, selector, { pad = 24, what = 'the picture' } = {}) {
  const box = await page.evaluate((sel) => {
    const r = document.querySelector(sel)?.getBoundingClientRect();
    return r ? { top: r.top, bottom: r.bottom } : null;
  }, selector);
  if (!box) throw new Error(`${what}: nothing on the page matches ${selector}`);
  const view = page.viewportSize();
  if (!view) throw new Error(`${what}: the page has no viewport`);
  if (box.top < 0 || box.top >= view.height) throw new Error(`${what}: ${selector} starts outside the window (${Math.round(box.top)}px)`);
  const height = Math.ceil(box.bottom + pad);
  await page.setViewportSize({ width: view.width, height });
  await sleep(350);
  return height;
}
