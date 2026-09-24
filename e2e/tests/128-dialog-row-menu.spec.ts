/**
 * A row's Actions menu, opened from inside a dialog — measured, not inspected.
 *
 * ⚠⚠ THE BUG. The owner, testing v0.43.0: "explore kısmında api anahtarlarında
 * ya da nasıl bağlanılar (yani popup ekranlar) içindeki aksiyon tuşlarının
 * menüsü popup'ın altında kalıyor ve açılmıyor gibi gözüküyor" — in the
 * explorer's pop-up screens (API keys, How to connect) the row Actions menu
 * looks as if it does not open at all.
 *
 * It opened every single time. `ContextMenu` teleports itself to `<body>` so
 * that no ancestor with `overflow: hidden` can clip it — and in doing so it
 * left behind its container's PLACE in the stack. Its backdrop is `z-index: 80`
 * (`.fe-ctx-backdrop`); the explorer's overlay is `z-index: 130`
 * (`.fe-overlay`). The menu was painted underneath the thing that opened it.
 *
 * ⚠ So the assertion is NOT "the menu exists" or "the menu is visible" — both
 * were true while the bug was on screen, which is exactly why the DOM was no
 * help. It is `document.elementFromPoint` at the menu's own centre: does a
 * click there reach the MENU, or something drawn over it. That question has
 * one answer and it is the user's.
 *
 * The fix is in `packages/core/src/lib/popupLayer.ts` — one mechanism, every
 * surface: the menu measures the highest z-index in the ancestry of whatever
 * opened it and sits one above that, so it clears the dialog it was opened
 * from (and, in an embed, whatever the host page's own container is worth)
 * without flattening the orderings the stylesheet sets on purpose.
 */
import { test, expect, type Page, type Locator } from '@playwright/test';
import { loginAs } from '../helpers/auth';
import { rowMenu } from '../helpers/rowMenu';
import { dropStorageByName, seedLocalStorage } from '../helpers/seed';

/**
 * ⚠ A storage, because the DOOR needs one. The dialogs under test are opened
 * from the explorer's navigation panel, and the explorer is only mounted for a
 * caller who can see at least one storage — a bare instance shows "No storages
 * configured yet" and has no panel at all. Removed again afterwards: a second
 * storage left behind takes the explorer out of single-storage mode and fails
 * 75-navigation, in a file that never touched this one.
 */
const STORAGE_NAME = 'menu-escape-e2e';

/** The PWA banner is fixed to the bottom and eats clicks aimed under it. */
async function dismissInstallBanner(page: Page) {
  const btn = page.getByTestId('pwa-install-dismiss');
  if (await btn.isVisible().catch(() => false)) await btn.click().catch(() => {});
}

/**
 * What a click at (x, y) would actually reach, described well enough to read
 * in a failure message: the nearest interesting ancestor's class.
 */
async function whatIsAt(page: Page, x: number, y: number): Promise<string> {
  return page.evaluate(
    ([px, py]) => {
      const hit = document.elementFromPoint(px, py) as HTMLElement | null;
      if (!hit) return 'nothing';
      if (hit.closest('.fe-ctx')) return 'menu';
      if (hit.closest('.fe-ctx-backdrop')) return 'menu-backdrop';
      if (hit.closest('.fe-overlay__card')) return 'dialog-card';
      if (hit.closest('.fe-overlay')) return 'dialog-backdrop';
      return hit.className || hit.tagName.toLowerCase();
    },
    [x, y],
  );
}

/**
 * The measurement itself, run against every dialog that holds one of these
 * tables. ⚠ It asserts the menu is REACHABLE, then that the surface around it
 * belongs to the menu too (so an outside click dismisses instead of pressing a
 * button in the dialog underneath), then that it still closes and still takes
 * the keyboard.
 */
async function measureEscapes(page: Page, where: string, row: Locator) {
  const actions = row.getByRole('button', { name: /^(Actions|Aksiyon)/i });
  await expect(actions, `${where}: the row has no Actions control`).toBeVisible();
  await actions.click();

  const menu = rowMenu(page);
  await expect(menu, `${where}: the menu never rendered`).toBeVisible();

  const box = await menu.boundingBox();
  expect(box, `${where}: the menu has no box`).not.toBeNull();
  const cx = box!.x + box!.width / 2;
  const cy = box!.y + box!.height / 2;

  // ⚠⚠ THE assertion. Before the fix this said "dialog-card".
  expect(
    await whatIsAt(page, cx, cy),
    `${where}: a click at the menu's centre does not reach the menu`,
  ).toBe('menu');

  // Each entry, not merely the box: a menu whose top strip cleared the dialog
  // and whose last entry did not would pass a centre-only check.
  const entries = menu.locator('[role="menuitem"]');
  const n = await entries.count();
  expect(n, `${where}: the menu has no entries`).toBeGreaterThan(0);
  for (let i = 0; i < n; i++) {
    const b = await entries.nth(i).boundingBox();
    if (!b) continue;
    expect(
      await whatIsAt(page, b.x + b.width / 2, b.y + b.height / 2),
      `${where}: entry ${i} is covered`,
    ).toBe('menu');
  }

  // The surface around it is the menu's own backdrop, over the dialog — which
  // is what makes "click outside to dismiss" work rather than pressing
  // whatever button in the dialog happened to be under the pointer.
  const card = page.locator('.fe-overlay__card');
  const cardBox = await card.boundingBox();
  expect(cardBox, `${where}: no dialog card`).not.toBeNull();
  expect(
    await whatIsAt(page, cardBox!.x + cardBox!.width / 2, cardBox!.y + 8),
    `${where}: the dialog is still taking clicks while a menu is open`,
  ).toBe('menu-backdrop');

  // Keyboard: the first entry has focus on open, and the arrows stay inside.
  const focused = await page.evaluate(() => !!document.activeElement?.closest('.fe-ctx'));
  expect(focused, `${where}: opening the menu did not move focus into it`).toBe(true);
  await page.keyboard.press('ArrowDown');
  expect(
    await page.evaluate(() => !!document.activeElement?.closest('.fe-ctx')),
    `${where}: ArrowDown left the menu`,
  ).toBe(true);

  // Escape closes only the menu — the dialog stays.
  await page.keyboard.press('Escape');
  await expect(menu, `${where}: Escape did not close the menu`).toBeHidden();
  await expect(card, `${where}: Escape closed the dialog too`).toBeVisible();

  // An outside click closes it as well.
  await actions.click();
  await expect(rowMenu(page)).toBeVisible();
  await page.mouse.click(cardBox!.x + cardBox!.width / 2, cardBox!.y + 8);
  await expect(rowMenu(page), `${where}: an outside click did not dismiss the menu`).toBeHidden();
}

test.describe('a row menu opened inside a dialog', () => {
  test.beforeAll(async ({ request }) => {
    await dropStorageByName(request, STORAGE_NAME);
    await seedLocalStorage(request, STORAGE_NAME, `/tmp/filex-${STORAGE_NAME}`);
  });

  test.afterAll(async ({ request }) => {
    await dropStorageByName(request, STORAGE_NAME);
  });

  test.beforeEach(async ({ page }) => {
    // The onboarding tour parents its card to <body> at z-index 96 and covers
    // the navigation panel; every spec that clicks a sidenav entry turns it off.
    await page.addInitScript(() => localStorage.setItem('filex.tourDone', '1'));
  });

  test('is on top of the dialog, in every pop-up that holds one', async ({ page }) => {
    await loginAs(page);
    await page.goto('/drive/explore');
    await dismissInstallBanner(page);
    await expect(page.getByTestId('sidenav')).toBeVisible({ timeout: 25_000 });

    // ── 1. "How to connect" → the credential panel the guides need ────────
    await page.getByTestId('sidenav-connect').click();
    await expect(page.getByTestId('connections-panel')).toBeVisible();
    await page.getByTestId('guide-protocol').selectOption('webdav');

    const tokens = page.getByTestId('api-tokens');
    await expect(tokens).toBeVisible();

    // A row to open a menu on. Minted here rather than through the API because
    // this is also the path a person takes to get one.
    await tokens.getByTestId('token-label').first().fill(`menu-escape-${Date.now()}`);
    await tokens.getByTestId('token-mint').first().click();
    await expect(page.getByTestId('token-secret')).toBeVisible({ timeout: 15_000 });
    await page.getByRole('button', { name: /I have copied it|Kopyaladım/i }).click();

    const connRow = tokens.locator('.fe-list__row').first();
    await expect(connRow).toBeVisible();
    await measureEscapes(page, 'How to connect', connRow);

    // The dialog closing takes the menu with it — a teleported node outliving
    // its opener would be a ghost menu floating over the file list.
    await connRow.getByRole('button', { name: /^(Actions|Aksiyon)/i }).click();
    await expect(rowMenu(page)).toBeVisible();
    await page.keyboard.press('Escape');
    await page.getByTestId('connections-close').click();
    await expect(page.getByTestId('explorer-overlay')).toHaveCount(0);
    await expect(page.locator('.fe-ctx')).toHaveCount(0);

    // ── 2. "API keys" → the same table in the other pop-up ────────────────
    await page.getByTestId('sidenav-apikeys').click();
    const keys = page.getByTestId('api-tokens');
    await expect(keys).toBeVisible();
    const keyRow = keys.locator('.fe-list__row').first();
    await expect(keyRow).toBeVisible();
    await measureEscapes(page, 'API keys', keyRow);

    // Clean up the token this spec minted, so a rerun is not reading a pile.
    await page.keyboard.press('Escape');
  });
});
