// keepClear — where a standing advert may stand without covering a control.
//
// ⚠⚠ Why this exists (2026-09-21, a tester with the owner's eye): the
// desktop-app chip in the bottom-right corner stood on the signing page's
// final "İmzala" button. At 1366×768 the button was completely hidden; at
// 1440×1000 `document.elementFromPoint` over the button's centre returned the
// chip's icon (`ip-mark`); at 390×844 its head. The sign-in page had been
// guarded against exactly this (`[data-install-clear]` in Login.vue) — and
// only the sign-in page. The rule is the product's, not one page's: an
// advert never covers something a person has to press.
//
// So the chip asks, wherever it is, "what is under me?", and:
//
//   · nothing to press  → it stands in its corner (HOME);
//   · a bar pinned to the viewport edge (a sticky footer of actions, a
//     fixed toolbar) → it stands on top of that bar (LIFT), which is a
//     position that does not move while the page scrolls;
//   · anything else — a button in the flow of the page, or a bar so tall
//     that standing on it would put the chip in the middle of the screen —
//     → it steps ASIDE (hidden, not focusable) until the spot is clear again.
//     Nothing is lost by that: the same offer lives in the settings panel
//     (the owner's ruling of 2026-09-13), and a person who never scrolls the
//     button away never needed the chip on that page.
//
// ⚠ Why not reserve room at the bottom of every page instead, the way the
// sign-in page does: a reserved band is blank space across the full width for
// something standing in one corner (InstallPrompt's own comment on
// `--filex-install-banner-h`), and it does nothing for a bar that is pinned
// to the viewport — padding under the content never moves a sticky footer.
//
// Pure geometry with the DOM passed in as two callbacks, so the decision is
// unit-tested without a layout engine (`web/tests/lib/keepClear.test.ts`).

export interface Box {
  top: number;
  bottom: number;
  left: number;
  right: number;
}

/** Something to press under a probed box. `pinnedTop` is the top edge of the
 *  fixed/sticky bar that carries it, or null when it scrolls with the page. */
export interface Hit {
  pinnedTop: number | null;
}

export interface Placement {
  /** How far above its home the chip stands, in px (0 = home). */
  lift: number;
  /** True when no clear spot was found: the chip steps aside. */
  aside: boolean;
}

/** Gap between the chip and the bar it stands on. */
export const LIFT_GAP = 8;
/** The chip never climbs above this share of the viewport's height: past it,
 *  it would be standing on the content rather than beside it. */
export const MAX_LIFT_SHARE = 0.4;
/** ...and only climbs at all while it is a corner thing: a chip wider than
 *  this share of the viewport stands on the content whatever its height. */
export const MAX_LIFT_WIDTH_SHARE = 0.5;

/**
 * Decide where the chip stands.
 *
 * `home` is the chip's box in its corner (lift 0); `probe(box)` answers what
 * could be pressed under a box; `viewportH` bounds the climb.
 */
export function placeChip(home: Box, probe: (b: Box) => Hit[], viewportH: number, viewportW = Infinity): Placement {
  const under = probe(home);
  if (under.length === 0) return { lift: 0, aside: false };
  // A control in the flow of the page moves as the page scrolls; chasing it
  // would make the chip jump with every wheel tick. Step aside instead.
  if (under.some((h) => h.pinnedTop === null)) return { lift: 0, aside: true };
  // ⚠ On a phone the chip is nearly as wide as the screen: standing on the
  // bar there put it across the page's own lines (measured at 390×844 on the
  // signing page's last step — the date the signer typed was under it).
  if (home.right - home.left > viewportW * MAX_LIFT_WIDTH_SHARE) return { lift: 0, aside: true };
  const barTop = Math.min(...under.map((h) => h.pinnedTop as number));
  const lift = Math.max(0, home.bottom - (barTop - LIFT_GAP));
  const lifted: Box = { ...home, top: home.top - lift, bottom: home.bottom - lift };
  if (viewportH - lifted.top > viewportH * MAX_LIFT_SHARE) return { lift: 0, aside: true };
  // Standing on the bar must not put it on something else.
  if (probe(lifted).length > 0) return { lift: 0, aside: true };
  return { lift, aside: false };
}

/** What a person can press — the same list the sign-in page's fit check used,
 *  plus the ARIA roles a component library paints as controls, plus anything a
 *  page marks `data-install-keep` (text that must stay readable). */
export const CONTROL_SELECTOR = [
  'button',
  'a[href]',
  'input',
  'select',
  'textarea',
  'summary',
  '[role="button"]',
  '[role="link"]',
  '[role="tab"]',
  '[role="menuitem"]',
  '[role="checkbox"]',
  '[role="switch"]',
  '[contenteditable="true"]',
  '[data-install-keep]',
].join(', ');

/**
 * The DOM half of `probe`: sample a grid of points inside `box` and collect
 * the controls under them, ignoring `self` (the chip) and anything inside it.
 *
 * ⚠ `elementsFromPoint`, not `elementFromPoint`: the chip is ON TOP, so the
 * first element at every point of its box is the chip itself — the question is
 * what lies BENEATH it.
 */
export function probeDom(box: Box, self: Element | null, doc: Document = document): Hit[] {
  if (typeof doc.elementsFromPoint !== 'function') return [];
  const hits = new Map<Element, Hit>();
  const cols = 6;
  const rows = 3;
  const w = box.right - box.left;
  const h = box.bottom - box.top;
  if (w <= 0 || h <= 0) return [];
  for (let c = 0; c < cols; c++) {
    for (let r = 0; r < rows; r++) {
      const x = box.left + 2 + ((w - 4) * c) / (cols - 1);
      const y = box.top + 2 + ((h - 4) * r) / (rows - 1);
      for (const el of doc.elementsFromPoint(x, y)) {
        if (self && (el === self || self.contains(el))) continue;
        const ctl = el.closest(CONTROL_SELECTOR);
        if (!ctl || hits.has(ctl)) continue;
        hits.set(ctl, { pinnedTop: pinnedTop(ctl) });
      }
    }
  }
  return [...hits.values()];
}

/** The top edge of the nearest fixed/sticky ancestor (or the element itself),
 *  or null when the element scrolls with the page. */
function pinnedTop(el: Element): number | null {
  for (let n: Element | null = el; n && n !== n.ownerDocument?.body; n = n.parentElement) {
    const pos = n.ownerDocument?.defaultView?.getComputedStyle(n).position;
    if (pos === 'fixed' || pos === 'sticky') return n.getBoundingClientRect().top;
  }
  return null;
}
