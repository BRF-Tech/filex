/**
 * Which layer a teleported popup has to sit on to be above whatever opened it.
 *
 * ⚠⚠ THE BUG THIS EXISTS FOR. A menu that teleports to `<body>` leaves its
 * container's stacking context behind — that is the point, it is how a menu
 * escapes `overflow: hidden` and a transformed ancestor. But it also leaves
 * behind that container's PLACE in the stack. The explorer's Connections /
 * API-keys overlay is `.fe-overlay`, `position: fixed; z-index: 130`; the
 * context menu's backdrop is `z-index: 80`. So a row's "Actions" menu opened
 * inside that overlay was painted UNDER it — the owner, testing v0.43.0:
 * "explore kısmında api anahtarlarında ya da nasıl bağlanılar (yani popup
 * ekranlar) içindeki aksiyon tuşlarının menüsü popup'ın altında kalıyor ve
 * açılmıyor gibi gözüküyor". It opened every time; nobody could see it.
 *
 * ⚠ Why not simply raise `.fe-ctx-backdrop` to a big constant. Because the
 * stack has deliberate orderings in it that a constant would flatten — the
 * onboarding tour sits at 96 ABOVE the filter popovers at 90 on purpose, so a
 * tour step is never hidden behind one. And in an EMBED the number we would
 * have to beat is not ours at all: the host page decides what its own
 * containers are worth, and no constant we pick is guaranteed to clear it.
 *
 * So the layer is MEASURED from the thing that opened the popup: walk the
 * opener's ancestors, take the highest z-index any positioned one carries, and
 * sit one above it — or stay on the base layer when nothing in the chain is
 * raised at all, which is the ordinary case and leaves every existing ordering
 * exactly as it was.
 */

/** The layer transient popups live on when nothing pushes them higher. */
export const POPUP_BASE_Z = 80;

/**
 * The z-index for a popup opened from `openers`.
 *
 * Several openers may be passed because the caller usually has more than one
 * imperfect clue about what was clicked (the focused element, the element under
 * the pointer); the highest answer wins, and a null is simply ignored.
 */
export function popupLayer(
  openers: Array<Element | null | undefined> | Element | null | undefined,
  base: number = POPUP_BASE_Z,
): number {
  if (typeof window === 'undefined' || typeof document === 'undefined') return base;
  const list = Array.isArray(openers) ? openers : [openers];
  let top = base;
  for (const opener of list) {
    let el: Element | null = opener ?? null;
    /* A cycle is impossible through parentElement, but a shadow host hop is a
     * jump the loop cannot prove terminates; 200 is far past any real tree. */
    for (let guard = 0; el && guard < 200; guard++) {
      if (el === document.body || el === document.documentElement) break;
      let z = Number.NaN;
      let positioned = false;
      try {
        const cs = window.getComputedStyle(el);
        /* ⚠ `|| 'static'`: a DOM that has not resolved the cascade answers
         * with the empty string rather than the initial value (happy-dom does,
         * and so does a detached node in some engines). Reading that as "not
         * static" would honour every inert `z-index` in the tree — the pinned
         * Actions cell carries one — and shove the menu onto a layer nothing
         * asked for. */
        positioned = (cs.position || 'static') !== 'static';
        z = Number.parseInt(cs.zIndex, 10);
      } catch {
        /* A detached or cross-document node: nothing to learn, keep climbing. */
      }
      /* ⚠ `position !== static` as well as a number: `z-index` on a static
       * element is inert — honouring it would push the menu above a dialog
       * because some unrelated cell in the row carried a leftover number. */
      if (positioned && Number.isFinite(z) && z + 1 > top) top = z + 1;
      el =
        el.parentElement ??
        /* Out the top of a shadow tree, if the host put us in one. */
        ((el.getRootNode() as ShadowRoot | null)?.host ?? null);
    }
  }
  return top;
}

/**
 * The best guesses at what opened a popup, at the moment it opens.
 *
 * `document.activeElement` is the control a click just focused, and the element
 * under the pointer is what a right-click or a synthetic open landed on. Either
 * can be wrong on its own — a button that does not take focus, a coordinate on
 * a border — and both are in the same subtree as the real opener whenever they
 * are right, which is all `popupLayer` needs.
 *
 * ⚠ Call this BEFORE the popup renders. Once the backdrop is in the document it
 * covers the viewport, and `elementFromPoint` answers with the backdrop.
 */
export function openerCandidates(x?: number, y?: number): Array<Element | null> {
  if (typeof document === 'undefined') return [];
  const out: Array<Element | null> = [document.activeElement];
  if (typeof x === 'number' && typeof y === 'number' && document.elementFromPoint) {
    try {
      out.push(document.elementFromPoint(x, y));
    } catch {
      /* Out-of-viewport coordinates throw in some engines; the focus clue stands. */
    }
  }
  return out;
}
