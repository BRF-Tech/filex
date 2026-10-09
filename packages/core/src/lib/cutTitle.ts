/**
 * A PILL CUT SHORT BY ITS COLUMN SAYS ITS WHOLE TEXT ON HOVER.
 *
 * In a table cell a pill (`tbl-pill`: a request's "Waiting", an app's
 * "Installed", a role, a source) keeps its border at the cell's width and
 * ends its words in an ellipsis (`styles/base.css`, "a pill in a cell"). The
 * words that were cut have to be somewhere: a plain cell gives its whole text
 * as its title (DataTable `cellTitle`), and a pill a page drew in a slot gets
 * the same from here, when the pointer reaches it - measured then, because
 * whether it is cut depends on a column width the person can drag at any time.
 *
 * ⚠ A title the page gave the pill itself is never touched (the explorer's
 * lock and link chips carry a sentence, which says more than the word). The
 * value this module writes is remembered beside it (`data-fe-cut-title`), so
 * it can take back exactly its own title once the pill fits again, and so a
 * title the page sets LATER is recognised as the page's.
 */

const MARK = 'data-fe-cut-title';

/** Is this box's content wider than the box (cut by its `overflow`)? */
export function isCut(el: Element): boolean {
  const h = el as HTMLElement;
  return h.scrollWidth > h.clientWidth;
}

/** The pill's words, as one line. */
function wordsOf(pill: Element): string {
  return (pill.textContent ?? '').replace(/\s+/g, ' ').trim();
}

/**
 * For the pill under the pointer (`target` or an ancestor of it, inside
 * `root`): give it its whole text as its title while it is cut, and take
 * back a title this put there once it is not.
 */
export function syncCutTitle(target: EventTarget | null, root: Element | null): void {
  if (!root || typeof Element === 'undefined' || !(target instanceof Element)) return;
  const pill = target.closest('.tbl-pill');
  if (!pill || !root.contains(pill)) return;
  const ours = pill.getAttribute(MARK);
  const title = pill.getAttribute('title');
  if (title !== null && title !== ours) return;
  const texts = Array.from(pill.querySelectorAll('.tbl-pill__text'));
  const cut = isCut(pill) || texts.some(isCut);
  const words = wordsOf(pill);
  if (cut && words) {
    pill.setAttribute('title', words);
    pill.setAttribute(MARK, words);
  } else if (ours !== null) {
    pill.removeAttribute('title');
    pill.removeAttribute(MARK);
  }
}
