/**
 * Picking from a list in the browser, now that filex draws no native select
 * element (#160).
 *
 * Every list is core's `ChoiceSelect` (the admin panel's `ui/Select` is a
 * labelled frame around it): a combobox button and a listbox TELEPORTED to
 * <body> (or into the open <dialog>). Playwright's `selectOption()` only
 * drives a native select, and an option is not inside the control that
 * opened it, so a spec does what a person does: click the field, click the
 * option. This is the e2e twin of `web/tests/helpers/choiceSelect.ts`.
 *
 * A target may be the combobox itself (core passes `testid` to it, so
 * `page.getByTestId('guide-protocol')` IS the control), or anything that
 * contains one (ui/Select's wrapper carries the page's `data-testid`; a
 * `getByLabel(…)` resolves to the combobox through its `<label for>`).
 */
import { expect, type Locator } from '@playwright/test';

/** The ChoiceSelect combobox at or inside `target`. */
export async function comboOf(target: Locator): Promise<Locator> {
  return (await target.getAttribute('role')) === 'combobox' ? target : target.locator('.fe-select__trigger').first();
}

/** A pattern that matches `text` and nothing longer. */
function exactly(text: string): RegExp {
  const literal = text.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
  return new RegExp('^\\s*' + literal + '\\s*$');
}

/** The open list the combobox points at. */
async function openList(box: Locator): Promise<Locator> {
  if ((await box.getAttribute('aria-expanded')) !== 'true') await box.click();
  const list = box.page().locator(`[id="${await box.getAttribute('aria-controls')}"]`);
  await expect(list).toBeVisible();
  return list;
}

/** Every option as the open list draws it, `{ value, label }`, in order (closed again after). */
export async function listedOptions(target: Locator): Promise<{ value: string; label: string }[]> {
  const box = await comboOf(target);
  const list = await openList(box);
  const out = await list.locator('[role="option"]').evaluateAll((els) =>
    els.map((e) => ({
      value: e.getAttribute('data-value') ?? '',
      label: (e.querySelector('.fe-select__text')?.textContent ?? '').trim(),
    })),
  );
  // Escape closes the list only: the control keeps it from the dialog around it.
  await box.press('Escape');
  await expect(box).toHaveAttribute('aria-expanded', 'false');
  return out;
}

/** Choose an option by its value, or by the words on it (`{ label }`). */
export async function pickOption(target: Locator, choice: string | { label: string }): Promise<void> {
  const box = await comboOf(target);
  const list = await openList(box);
  const option =
    typeof choice === 'string'
      ? list.locator(`[role="option"][data-value="${choice}"]`)
      : list.locator('[role="option"]').filter({
          has: box.page().locator('.fe-select__text', { hasText: exactly(choice.label) }),
        });
  await option.click();
  await expect(box).toHaveAttribute('aria-expanded', 'false');
}

/** The control shows `value` as chosen. */
export async function expectChosen(target: Locator, value: string): Promise<void> {
  await expect(await comboOf(target)).toHaveAttribute('data-value', value);
}
