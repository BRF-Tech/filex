/**
 * Driving a list now that filex draws no native select element (#160).
 *
 * Every list in the product is core's `ChoiceSelect` (the admin panel's
 * `ui/Select` is a labelled frame around it): a combobox button that keeps
 * the focus and a listbox TELEPORTED to <body> (or into the open <dialog>).
 * So a test cannot `setValue()` a select or read its `.options` any more, and
 * an option is not inside the wrapper that opened it. These helpers are the
 * whole adaptation, and they do what a person does: open the list, read it,
 * click an option.
 *
 * A target may be the combobox itself (core passes `testid` to it), anything
 * that contains one (ui/Select's wrapper, which carries the page's
 * `data-testid`), or the hidden form input of one (`input[name=…]`).
 *
 * ⚠ Only `nextTick` is awaited, never `flushPromises`: several suites run
 * under fake timers, where a timer-based flush never resolves.
 */
import { nextTick } from 'vue';

type Target = Element | { element: Element };

function elementOf(t: Target): Element {
  return t instanceof Element ? t : t.element;
}

/** The ChoiceSelect combobox at, inside or around `target`. */
export function comboOf(target: Target): HTMLElement {
  const el = elementOf(target);
  if (el.getAttribute('role') === 'combobox') return el as HTMLElement;
  const inside = el.querySelector<HTMLElement>('.fe-select__trigger[role="combobox"]');
  if (inside) return inside;
  const around = el.closest('.fe-select')?.querySelector<HTMLElement>('.fe-select__trigger[role="combobox"]');
  if (around) return around;
  throw new Error(`no ChoiceSelect combobox at, inside or around <${el.tagName.toLowerCase()}>`);
}

/** The value the control shows as chosen (`''` when none). */
export function chosenValue(target: Target): string {
  return comboOf(target).getAttribute('data-value') ?? '';
}

/** The text the control shows (the chosen label, or the placeholder). */
export function shownText(target: Target): string {
  return (comboOf(target).querySelector('.fe-select__value')?.textContent ?? '').trim();
}

/** Whether the list is open. */
export function isListOpen(target: Target): boolean {
  return comboOf(target).getAttribute('aria-expanded') === 'true';
}

/** The listbox the combobox points at — present only while it is open. */
export function listOf(target: Target): HTMLElement | null {
  const id = comboOf(target).getAttribute('aria-controls');
  return id ? document.getElementById(id) : null;
}

/** Open the list (a click on the field) and return it. */
export async function openList(target: Target): Promise<HTMLElement> {
  const combo = comboOf(target);
  if (!isListOpen(combo)) {
    combo.click();
    await nextTick();
    await nextTick();
  }
  const list = listOf(combo);
  if (!list) throw new Error('the list did not open — is the control disabled?');
  return list;
}

/** Close the list without choosing (a second click on the field). */
export async function closeList(target: Target): Promise<void> {
  const combo = comboOf(target);
  if (!isListOpen(combo)) return;
  combo.click();
  await nextTick();
}

export interface ListedOption {
  value: string;
  label: string;
  help: string;
  disabled: boolean;
  selected: boolean;
}

/** Every option, in order, as the open list draws it (the list is closed again after). */
export async function listedOptions(target: Target): Promise<ListedOption[]> {
  const list = await openList(target);
  const out = Array.from(list.querySelectorAll('[role="option"]')).map((o) => ({
    value: o.getAttribute('data-value') ?? '',
    label: (o.querySelector('.fe-select__text')?.textContent ?? '').trim(),
    help: (o.querySelector('.fe-select__help')?.textContent ?? '').trim(),
    disabled: o.getAttribute('aria-disabled') === 'true',
    selected: o.getAttribute('aria-selected') === 'true',
  }));
  await closeList(target);
  return out;
}

/** The options' labels, in order. */
export async function optionLabels(target: Target): Promise<string[]> {
  return (await listedOptions(target)).map((o) => o.label);
}

/** The options' values, in order. */
export async function optionValues(target: Target): Promise<string[]> {
  return (await listedOptions(target)).map((o) => o.value);
}

/**
 * The first list under `root` that offers `value` — for a page with several
 * unnamed lists, found the way a person would: by what they offer. Each
 * enabled list is opened to look and closed again.
 */
export async function listOffering(value: string | number, root: ParentNode = document.body): Promise<HTMLElement | null> {
  const combos = Array.from(root.querySelectorAll<HTMLButtonElement>('.fe-select__trigger[role="combobox"]'));
  for (const combo of combos) {
    if (combo.disabled) continue;
    const values = (await listedOptions(combo)).map((o) => o.value);
    if (values.includes(String(value))) return combo;
  }
  return null;
}

/** Choose the option whose value is `value`: open the list, click it. */
export async function pickOption(target: Target, value: string | number): Promise<void> {
  const combo = comboOf(target);
  const list = await openList(combo);
  const options = Array.from(list.querySelectorAll<HTMLElement>('[role="option"]'));
  const option = options.find((o) => o.getAttribute('data-value') === String(value));
  if (!option) {
    const has = options.map((o) => o.getAttribute('data-value')).join(', ');
    await closeList(combo);
    throw new Error(`no option with the value "${value}" (the list has: ${has})`);
  }
  option.click();
  await nextTick();
  await nextTick();
}
