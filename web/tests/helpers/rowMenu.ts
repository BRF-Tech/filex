/**
 * Reaching a row's verbs now that they live in a menu.
 *
 * Every admin table row ends in ONE control (core `RowActions`) whose menu is
 * core `ContextMenu` — and ContextMenu teleports itself to <body>, deliberately,
 * so that no ancestor with `overflow: hidden` can clip it. That is why a spec
 * cannot reach an entry through the wrapper that opened it: the entry is not
 * inside the wrapper's tree at all. These four lines are the whole adaptation.
 *
 * ⚠ `openRowMenu` awaits twice. `ContextMenu.show()` is async: it renders, then
 * awaits `nextTick()` before clamping itself inside the viewport, so a single
 * `await` returns before the items exist.
 *
 * ⚠ `closeRowMenus` matters between assertions in one test. A teleported menu
 * is appended to the document, not to the wrapper, so an unclosed one from an
 * earlier row is still in the DOM when the next row's is opened and
 * `menuItem()` would find the wrong one.
 */
import { nextTick } from 'vue';

interface Clickable {
  find(sel: string): { trigger(ev: string): Promise<void>; exists(): boolean };
}

/** Click a row's `Actions` control and let its menu render. */
export async function openRowMenu(w: Clickable, testid: string): Promise<void> {
  await w.find(`[data-testid="${testid}"]`).trigger('click');
  await nextTick();
  await nextTick();
}

/** Every entry currently on screen, in order, as `{ label, danger, disabled }`. */
export function menuEntries(): { label: string; danger: boolean; disabled: boolean }[] {
  return Array.from(document.querySelectorAll('.fe-ctx .fe-ctx__item')).map((el) => ({
    label: (el.querySelector('.fe-ctx__label')?.textContent ?? '').trim(),
    danger: el.classList.contains('is-danger'),
    disabled: (el as HTMLButtonElement).disabled,
  }));
}

/** One entry by its `data-testid` — `<control testid>-<action key>` by default. */
export function menuItem(testid: string): HTMLButtonElement | null {
  const all = document.querySelectorAll(`.fe-ctx [data-testid="${testid}"]`);
  return (all[all.length - 1] as HTMLButtonElement) ?? null;
}

/** Pick an entry, the way a person does. */
export async function pickMenuItem(testid: string): Promise<void> {
  const el = menuItem(testid);
  if (!el) throw new Error(`no menu entry [data-testid="${testid}"] — is the menu open?`);
  el.click();
  await nextTick();
}

/** Drop every teleported menu still in the document. */
export function closeRowMenus(): void {
  document.querySelectorAll('.fe-ctx-backdrop').forEach((el) => el.remove());
}
