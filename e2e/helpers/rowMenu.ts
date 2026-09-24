/**
 * Reaching a row's verbs in the browser, now that they live in a menu.
 *
 * Every admin row ends in ONE pinned control — core `RowActions`, labelled
 * "Actions" / "Aksiyon" — and its menu is core `ContextMenu`, the same menu
 * the explorer's ⋮ opens. The loose `Revoke` / `Remove` icon buttons a row
 * used to draw are gone; the verb is a menu entry now.
 *
 * This is the e2e twin of `web/tests/helpers/rowMenu.ts` (the unit-test one,
 * which drives a mounted component). The adaptation is the same and it is
 * needed for the same reason: `ContextMenu` teleports itself to `<body>` so
 * that no ancestor with `overflow: hidden` can clip it, which means an entry
 * is NOT inside the row that opened it. `row.getByRole('button', …)` cannot
 * find it, and never will.
 *
 * ⚠ Entries are addressed by the WORDS a person reads, not by a `data-testid`.
 * Half the point of the move was that an admin verb has to be named rather
 * than drawn as a glyph, and a spec that matched an id would go green over a
 * menu full of blank entries.
 *
 * ⚠ A two-step confirmation now costs two menu trips. The panels kept their
 * confirmation exactly as it was — the first pick arms it and the entry's
 * label becomes "Sure?" — but the menu closes on every pick, so the second
 * pick needs the menu opened again. `confirmRowAction` is that pair.
 */
import { expect, type Locator, type Page } from '@playwright/test';

/** The label on the control itself, in either language. */
const ACTIONS_LABEL = /^(Actions|Aksiyon)/i;

/**
 * The menu currently on screen. Teleported to `<body>`, so it is looked up
 * from the page — `.last()` because a menu left open by an earlier row would
 * otherwise be the one a `getByRole` found.
 */
export function rowMenu(page: Page): Locator {
  return page.locator('.fe-ctx, .fe-sheet').last();
}

/** Click a row's one `Actions` control and wait for its menu to be drawn. */
export async function openRowMenu(row: Locator): Promise<Locator> {
  await row.getByRole('button', { name: ACTIONS_LABEL }).click();
  const menu = rowMenu(row.page());
  await expect(menu).toBeVisible();
  return menu;
}

/** Every verb this row offers, in order, as the person reads them. */
export async function rowMenuVerbs(row: Locator): Promise<string[]> {
  const menu = await openRowMenu(row);
  const names = (await menu.locator('[role="menuitem"] .fe-ctx__label').allInnerTexts()).map((s) =>
    s.trim(),
  );
  await row.page().keyboard.press('Escape');
  await expect(menu).toBeHidden();
  return names;
}

/** Open the row's menu and pick one verb by the words on it. */
export async function pickRowAction(row: Locator, verb: string | RegExp): Promise<void> {
  const menu = await openRowMenu(row);
  const entry = menu.getByRole('menuitem', { name: verb });
  await expect(entry, `no "${verb}" in this row's Actions menu`).toBeVisible();
  await entry.click();
  // The menu closes on a pick; waiting for that is what keeps a second trip
  // from clicking through a menu that is still fading out.
  await expect(menu).toBeHidden();
}

/**
 * A verb behind a two-step confirmation: pick it, then pick the confirmation
 * the entry turned into.
 *
 * `confirm` defaults to the wording the connections panels use in both
 * languages.
 */
export async function confirmRowAction(
  row: Locator,
  verb: string | RegExp,
  confirm: string | RegExp = /^(Sure\?|Emin misiniz\?)$/,
): Promise<void> {
  await pickRowAction(row, verb);
  await pickRowAction(row, confirm);
}
