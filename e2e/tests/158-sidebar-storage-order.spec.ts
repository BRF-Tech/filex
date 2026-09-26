/**
 * 158 — the storages in the navigation panel can be put in the person's own
 * order (GitHub #57: "Sidebar Storage sorting or manual reordering").
 *
 * What is measured here, in a real browser against a real instance:
 *
 *   1. with no order of their own, the panel shows the host's order — nothing
 *      changes for anybody who never reorders;
 *   2. a MOUSE drag moves a row, shows a drop marker while it is in the air,
 *      and does not open the storage it dropped (the release is not a click);
 *   3. the order survives a reload, is on the ACCOUNT (`/api/me/prefs`,
 *      `storageOrder`), and a second browser with empty storage gets it;
 *   4. the row's menu — right click — moves one step down;
 *   5. the KEYBOARD: Shift+F10 on a focused row opens the same menu, Enter on
 *      "Move up" moves it, and focus stays on the row that moved;
 *   6. "Sort by name" writes a name-sorted order and "Use default order" returns to
 *      the host's;
 *   7. a FINGER: a long press then a move drags the row (the panel does not
 *      scroll away under it); a long press lifted in place opens the menu;
 *   8. the ADMINISTRATOR's order (the admin Storages table: a drag by the
 *      handle, Move up / Move down, the arrow keys on a handle) is what a
 *      person with no order of their own sees; that person's own order wins
 *      over it; "Use default order" brings the administrator's back; "Reset to
 *      default order" puts everybody back in creation order;
 *   9. nothing overflows sideways at 1280 and 390 px, light and dark, with the
 *      menu open — in the navigation panel and on the admin Storages page.
 *
 * The storages are created in an order that is NOT alphabetical, so "sort by
 * name" and "reset" are two different answers and each is seen to happen.
 */
import { test, expect, type Browser, type Locator, type Page } from '@playwright/test';
import { apiLogin, loginAs } from '../helpers/auth';
import { dropStorageByName, seedLocalStorage } from '../helpers/seed';

const STAMP = Date.now();
const PREFIX = `e2e-so-${STAMP}`;
/* Creation (= host) order: charlie, alpha, bravo. Name order: alpha, bravo, charlie. */
const C = `${PREFIX}-charlie`;
const A = `${PREFIX}-alpha`;
const B = `${PREFIX}-bravo`;
const HOST_ORDER = [C, A, B];

const row = (page: Page, name: string) => page.getByTestId(`sidenav-storage-${name}`);

/* A plain user — the person an administrator's order is FOR. */
const USER = { email: `so-${STAMP}@example.com`, password: 'storage-order-pw-2026' };

async function signInUser(page: Page) {
  await page.addInitScript(() => {
    localStorage.setItem('filex.tourDone', '1');
    localStorage.setItem('filex.installPrompt.dismissed', '1');
  });
  await page.goto('/admin/login');
  await page.getByLabel(/e-?mail|kullanıcı adı/i).fill(USER.email);
  await page.getByLabel(/password|şifre|parola/i).fill(USER.password);
  await page
    .getByRole('button', { name: 'Sign in', exact: true })
    .or(page.getByRole('button', { name: 'Oturum aç', exact: true }))
    .first()
    .click();
  await page.waitForURL(/\/drive\//, { timeout: 15_000 });
  await page.goto('/drive/explore');
  await expect(row(page, B)).toBeVisible();
  await settled(page);
}

/** name → id of this spec's storages, from the admin list. */
async function storageIds(page: Page): Promise<Record<string, number>> {
  const res = await page.request.get('/api/admin/storages');
  expect(res.ok()).toBe(true);
  const rows = (await res.json()) as Array<{ id: number; name: string }>;
  return Object.fromEntries(rows.filter((r) => r.name.startsWith(PREFIX)).map((r) => [r.name, r.id]));
}

/** The admin Storages table's order of this spec's storages. */
async function tableOrder(page: Page): Promise<string[]> {
  return page
    .locator('[data-storage-row]')
    .evaluateAll((els, prefix) =>
      els
        .map((e) => (e.querySelector('a')?.textContent ?? '').trim())
        .filter((n) => n.startsWith(prefix as string)),
    PREFIX);
}

/** What the server stores: name → sort_order. */
async function adminPositions(page: Page): Promise<Record<string, number | null>> {
  const res = await page.request.get('/api/admin/storages');
  const rows = (await res.json()) as Array<{ name: string; sort_order?: number | null }>;
  return Object.fromEntries(rows.filter((r) => r.name.startsWith(PREFIX)).map((r) => [r.name, r.sort_order ?? null]));
}

/** The panel's order of THIS spec's storages (a full run has others). */
async function panelOrder(page: Page): Promise<string[]> {
  return page
    .locator('[data-testid="sidenav-storages"] > li > button')
    .evaluateAll((els, prefix) =>
      els
        .map((e) => (e.getAttribute('data-testid') ?? '').replace(/^sidenav-storage-/, ''))
        .filter((n) => n.startsWith(prefix as string)),
    PREFIX);
}

async function openExplorer(page: Page) {
  await page.addInitScript(() => localStorage.setItem('filex.tourDone', '1'));
  await loginAs(page);
  await page.goto('/admin/explore');
  await expect(row(page, B)).toBeVisible();
  await settled(page);
}

/**
 * Wait until the panel has stopped moving. ⚠ The Tags section is drawn only
 * once the tag list has come back, and it pushes the storages ~50px down: a
 * gesture aimed at coordinates measured before that lands on the row ABOVE
 * (measured — the drag started on the "Storages" caption and never armed).
 */
async function settled(page: Page) {
  let last = '';
  await expect
    .poll(async () => {
      const box = await row(page, B).boundingBox();
      const now = box ? `${Math.round(box.x)},${Math.round(box.y)}` : '';
      const same = now !== '' && now === last;
      last = now;
      return same;
    }, { intervals: [250, 250, 250, 500, 500, 1000] })
    .toBe(true);
}

/** The account's copy of the order, as the server stores it. */
async function accountOrder(page: Page): Promise<string> {
  const res = await page.request.get('/api/me/prefs?surface=web');
  expect(res.ok()).toBe(true);
  const body = (await res.json()) as { prefs?: { storageOrder?: string } };
  return body.prefs?.storageOrder ?? '';
}

async function centre(l: Locator) {
  const box = await l.boundingBox();
  if (!box) throw new Error('no box');
  return { x: box.x + box.width / 2, y: box.y + box.height / 2, box };
}

/** Nothing on the page or in the panel scrolls sideways; the menu is inside the window. */
async function noSidewaysOverflow(page: Page) {
  return page.evaluate(() => {
    const doc = document.documentElement;
    const scroller = document.querySelector('.fe-sidenav__scroll');
    const menu = document.querySelector('.fe-ctx, .fe-sheet');
    const r = menu?.getBoundingClientRect();
    return {
      page: doc.scrollWidth - doc.clientWidth,
      panel: scroller ? scroller.scrollWidth - scroller.clientWidth : -1,
      menuInside: r ? r.left >= 0 && r.right <= window.innerWidth && r.bottom <= window.innerHeight : null,
    };
  });
}

test.describe('sidebar storage order (#57)', () => {
  test.describe.configure({ mode: 'serial' });

  test.beforeAll(async ({ request }) => {
    for (const name of HOST_ORDER) {
      await dropStorageByName(request, name);
      await seedLocalStorage(request, name, `/tmp/filex-${name}`);
    }
    await apiLogin(request);
    const made = await request.post('/api/admin/users', {
      data: { email: USER.email, password: USER.password, role: 'user', locale: 'en' },
    });
    expect(made.ok(), await made.text()).toBe(true);
  });

  test.afterAll(async ({ request }) => {
    for (const name of HOST_ORDER) await dropStorageByName(request, name);
    // The administrator's order is instance-wide: leave none behind for the
    // specs after this one.
    await apiLogin(request);
    await request.put('/api/admin/storages/order', { data: { ids: [] } });
    // Leave the admin's account as it was for the specs after this one.
    await apiLogin(request);
    const got = await request.get('/api/me/prefs?surface=web');
    const prefs = ((await got.json()) as { prefs?: Record<string, string> }).prefs ?? {};
    delete prefs.storageOrder;
    await request.put('/api/me/prefs?surface=web', { data: { prefs } });
  });

  test('no order of your own = the host order', async ({ page }) => {
    await openExplorer(page);
    expect(await panelOrder(page)).toEqual(HOST_ORDER);
  });

  test('a mouse drag reorders, shows where it lands, and does not open the storage', async ({ page }) => {
    await openExplorer(page);
    const from = await centre(row(page, B));
    const to = await centre(row(page, C));
    await page.mouse.move(from.x, from.y);
    await page.mouse.down();
    // In steps, so the drag passes its slop and the gap is re-read on the way.
    const targetY = to.box.y + 4;
    for (let i = 1; i <= 12; i++) await page.mouse.move(from.x, from.y + ((targetY - from.y) * i) / 12);
    // In the air: the row in hand is marked and there is ONE drop line.
    await expect(row(page, B)).toHaveClass(/is-dragging/);
    await expect(page.locator('[data-testid="sidenav-storages"] > li.is-drop-before')).toHaveCount(1);
    await page.mouse.up();

    expect(await panelOrder(page)).toEqual([B, C, A]);
    await expect(page.locator('[data-testid="sidenav-storages"] > li.is-drop-before')).toHaveCount(0);
    // The release was not a click: bravo was dropped, not opened.
    await expect(row(page, B)).not.toHaveClass(/is-active/);
    await expect.poll(() => accountOrder(page)).toContain(B);
  });

  test('the order survives a reload, and a second browser gets it from the account', async ({ page, browser, baseURL }) => {
    await openExplorer(page);
    expect(await panelOrder(page)).toEqual([B, C, A]);
    await page.reload();
    await expect(row(page, B)).toBeVisible();
    expect(await panelOrder(page)).toEqual([B, C, A]);

    const other = await (browser as Browser).newContext({ baseURL });
    try {
      // A browser that has never seen this person: its storage is empty, so
      // the only place the order can come from is the account.
      const p2 = await other.newPage();
      await openExplorer(p2);
      await expect.poll(() => panelOrder(p2)).toEqual([B, C, A]);
    } finally {
      await other.close();
    }
  });

  test('the row menu moves a storage one step down', async ({ page }) => {
    await openExplorer(page);
    await expect.poll(() => panelOrder(page)).toEqual([B, C, A]);
    await row(page, C).click({ button: 'right' });
    await page.getByRole('menuitem', { name: 'Move down', exact: true }).click();
    await expect.poll(() => panelOrder(page)).toEqual([B, A, C]);
  });

  test('the keyboard: Shift+F10 opens the menu, Enter moves up, focus stays on the row', async ({ page }) => {
    await openExplorer(page);
    // The account's order (the previous test's) — act on the state asserted.
    await expect.poll(() => panelOrder(page)).toEqual([B, A, C]);
    await row(page, A).focus();
    await page.keyboard.press('Shift+F10');
    const up = page.getByRole('menuitem', { name: 'Move up', exact: true });
    await expect(up).toBeVisible();
    // The menu takes focus on its first live entry — "Move up" here.
    await expect(up).toBeFocused();
    await page.keyboard.press('Enter');
    await expect.poll(() => panelOrder(page)).toEqual([A, B, C]);
    await expect(row(page, A)).toBeFocused();
    // And Escape closes a menu without moving anything.
    await page.keyboard.press('Shift+F10');
    await expect(page.getByRole('menu')).toBeVisible();
    await page.keyboard.press('Escape');
    await expect(page.getByRole('menu')).toHaveCount(0);
    expect(await panelOrder(page)).toEqual([A, B, C]);
  });

  test('sort by name writes a name order; reset returns to the host order', async ({ page }) => {
    await openExplorer(page);
    await expect.poll(() => panelOrder(page)).toEqual([A, B, C]);
    // Scramble first, so sorting has something to do.
    await row(page, A).click({ button: 'right' });
    await page.getByRole('menuitem', { name: 'Move down', exact: true }).click();
    await expect.poll(() => panelOrder(page)).toEqual([B, A, C]);

    await row(page, C).click({ button: 'right' });
    await page.getByRole('menuitem', { name: 'Sort by name', exact: true }).click();
    await expect.poll(() => panelOrder(page)).toEqual([A, B, C]);

    await row(page, C).click({ button: 'right' });
    await page.getByRole('menuitem', { name: 'Use default order', exact: true }).click();
    await expect.poll(() => panelOrder(page)).toEqual(HOST_ORDER);
    await expect.poll(() => accountOrder(page)).toBe('');
    // With no order of your own, "Use default order" has nothing to let go of.
    await row(page, C).click({ button: 'right' });
    await expect(page.getByRole('menuitem', { name: 'Use default order', exact: true })).toBeDisabled();
    await page.keyboard.press('Escape');
  });

  test('a finger: long press + move drags, long press alone opens the menu', async ({ browser, baseURL }) => {
    const ctx = await (browser as Browser).newContext({
      baseURL,
      viewport: { width: 390, height: 844 },
      hasTouch: true,
      isMobile: true,
    });
    try {
      const page = await ctx.newPage();
      await page.addInitScript(() => localStorage.setItem('filex.tourDone', '1'));
      await loginAs(page);
      await page.goto('/admin/explore');
      // Below 560px the panel is a drawer; the top bar opens it.
      await page.getByTestId('toolbar-nav').click();
      await expect(row(page, B)).toBeVisible();
      await settled(page);
      expect(await panelOrder(page)).toEqual(HOST_ORDER);

      const cdp = await ctx.newCDPSession(page);
      const from = await centre(row(page, B));
      const to = await centre(row(page, C));
      const scrollBefore = await page.evaluate(() => document.querySelector('.fe-sidenav__scroll')?.scrollTop ?? 0);
      await cdp.send('Input.dispatchTouchEvent', { type: 'touchStart', touchPoints: [{ x: from.x, y: from.y }] });
      await page.waitForTimeout(750);
      await expect(row(page, B)).toHaveClass(/is-dragging/);
      const targetY = to.box.y + 4;
      for (let i = 1; i <= 12; i++) {
        await cdp.send('Input.dispatchTouchEvent', {
          type: 'touchMove',
          touchPoints: [{ x: from.x, y: from.y + ((targetY - from.y) * i) / 12 }],
        });
      }
      await cdp.send('Input.dispatchTouchEvent', { type: 'touchEnd', touchPoints: [] });
      await expect.poll(() => panelOrder(page)).toEqual([B, C, A]);
      expect(await page.evaluate(() => document.querySelector('.fe-sidenav__scroll')?.scrollTop ?? 0)).toBe(scrollBefore);

      // A long press lifted where it was: the row's menu, as the bottom sheet.
      const a = await centre(row(page, A));
      await cdp.send('Input.dispatchTouchEvent', { type: 'touchStart', touchPoints: [{ x: a.x, y: a.y }] });
      await page.waitForTimeout(750);
      await cdp.send('Input.dispatchTouchEvent', { type: 'touchEnd', touchPoints: [] });
      await expect(page.locator('.fe-sheet')).toBeVisible();
      await expect(page.getByRole('menuitem', { name: 'Move down', exact: true })).toBeDisabled();
      const up = page.getByRole('menuitem', { name: 'Move up', exact: true });
      await up.tap();
      await expect.poll(() => panelOrder(page)).toEqual([B, A, C]);
      await cdp.detach();
    } finally {
      await ctx.close();
    }
  });

  test('the administrator drags a storage to the top of the Storages table', async ({ page }) => {
    await page.addInitScript(() => localStorage.setItem('filex.tourDone', '1'));
    await loginAs(page);
    await page.goto('/admin/storages');
    const ids = await storageIds(page);
    const handle = (n: string) => page.getByTestId(`storage-order-handle-${ids[n]}`);
    await expect(handle(A)).toBeVisible();
    expect(await tableOrder(page)).toEqual(HOST_ORDER);

    const from = await centre(handle(A));
    const to = await centre(page.locator(`[data-storage-row="${ids[C]}"]`));
    await page.mouse.move(from.x, from.y);
    await page.mouse.down();
    const targetY = to.box.y + 4;
    for (let i = 1; i <= 12; i++) await page.mouse.move(from.x, from.y + ((targetY - from.y) * i) / 12);
    await expect(page.locator(`[data-storage-row="${ids[A]}"]`)).toHaveClass(/fe-list__row--dragging/);
    await expect(page.locator('.fe-list__row--drop-before')).toHaveCount(1);
    await page.mouse.up();

    await expect.poll(() => tableOrder(page)).toEqual([A, C, B]);
    await expect.poll(() => adminPositions(page)).toEqual({ [A]: 1, [C]: 2, [B]: 3 });
    // The release was a drop, not a click on the storage's link.
    expect(new URL(page.url()).pathname).toBe('/admin/storages');
  });

  test('Move down in a row menu, and the arrow keys on a handle, move a storage', async ({ page }) => {
    await page.addInitScript(() => localStorage.setItem('filex.tourDone', '1'));
    await loginAs(page);
    await page.goto('/admin/storages');
    const ids = await storageIds(page);
    await expect.poll(() => tableOrder(page)).toEqual([A, C, B]);

    await page.getByTestId(`storage-actions-${ids[A]}`).click();
    await page.getByRole('menuitem', { name: 'Move down', exact: true }).click();
    await expect.poll(() => tableOrder(page)).toEqual([C, A, B]);

    const handleB = page.getByTestId(`storage-order-handle-${ids[B]}`);
    await handleB.focus();
    await page.keyboard.press('ArrowUp');
    await expect.poll(() => tableOrder(page)).toEqual([C, B, A]);
    // Focus stays on the handle of the row that moved, so ↑ can be pressed again.
    await expect(handleB).toBeFocused();
    await expect.poll(() => adminPositions(page)).toEqual({ [C]: 1, [B]: 2, [A]: 3 });
  });

  test('a person with no order of their own sees the administrator order', async ({ browser, baseURL }) => {
    const ctx = await (browser as Browser).newContext({ baseURL });
    try {
      const page = await ctx.newPage();
      await signInUser(page);
      await expect.poll(() => panelOrder(page)).toEqual([C, B, A]);
    } finally {
      await ctx.close();
    }
  });

  test('the person reorders: theirs wins; "Use default order": the administrator order again', async ({ browser, baseURL }) => {
    const ctx = await (browser as Browser).newContext({ baseURL });
    try {
      const page = await ctx.newPage();
      await signInUser(page);
      await expect.poll(() => panelOrder(page)).toEqual([C, B, A]);
      await row(page, A).click({ button: 'right' });
      await page.getByRole('menuitem', { name: 'Move up', exact: true }).click();
      await expect.poll(() => panelOrder(page)).toEqual([C, A, B]);
      await page.reload();
      await expect(row(page, B)).toBeVisible();
      await expect.poll(() => panelOrder(page)).toEqual([C, A, B]);

      await row(page, B).click({ button: 'right' });
      await page.getByRole('menuitem', { name: 'Use default order', exact: true }).click();
      await expect.poll(() => panelOrder(page)).toEqual([C, B, A]);
    } finally {
      await ctx.close();
    }
  });

  test('Reset to default order puts everybody back in creation order', async ({ page, browser, baseURL }) => {
    await page.addInitScript(() => localStorage.setItem('filex.tourDone', '1'));
    await loginAs(page);
    await page.goto('/admin/storages');
    await expect.poll(() => tableOrder(page)).toEqual([C, B, A]);
    await page.getByTestId('storages-order-reset').click();
    await expect.poll(() => tableOrder(page)).toEqual(HOST_ORDER);
    await expect.poll(() => adminPositions(page)).toEqual({ [C]: null, [A]: null, [B]: null });
    await expect(page.getByTestId('storages-order-reset')).toBeDisabled();

    const ctx = await (browser as Browser).newContext({ baseURL });
    try {
      const p2 = await ctx.newPage();
      await signInUser(p2);
      await expect.poll(() => panelOrder(p2)).toEqual(HOST_ORDER);
    } finally {
      await ctx.close();
    }
  });

  for (const scheme of ['light', 'dark'] as const) {
    for (const width of [1280, 390]) {
      test(`the admin Storages page does not overflow sideways at ${width}px, ${scheme}, with a row menu open`, async ({ browser, baseURL }) => {
        const ctx = await (browser as Browser).newContext({
          baseURL,
          viewport: { width, height: width > 600 ? 800 : 844 },
          colorScheme: scheme,
        });
        try {
          const page = await ctx.newPage();
          await page.addInitScript(() => localStorage.setItem('filex.tourDone', '1'));
          await loginAs(page);
          await page.goto('/admin/storages');
          const ids = await storageIds(page);
          await expect(page.getByTestId(`storage-order-handle-${ids[A]}`)).toBeVisible();
          await page.getByTestId(`storage-actions-${ids[A]}`).click();
          await expect(page.getByRole('menuitem', { name: 'Move up', exact: true })).toBeVisible();
          const m = await noSidewaysOverflow(page);
          expect(m.page, 'the page scrolls sideways').toBeLessThanOrEqual(0);
          expect(m.menuInside, 'the menu leaves the window').toBe(true);
          await page.keyboard.press('Escape');
        } finally {
          await ctx.close();
        }
      });
    }
  }

  for (const scheme of ['light', 'dark'] as const) {
    for (const width of [1280, 390]) {
      test(`nothing overflows sideways at ${width}px, ${scheme}, with the menu open`, async ({ browser, baseURL }) => {
        const ctx = await (browser as Browser).newContext({
          baseURL,
          viewport: { width, height: width > 600 ? 800 : 844 },
          colorScheme: scheme,
        });
        try {
          const page = await ctx.newPage();
          await page.addInitScript(() => localStorage.setItem('filex.tourDone', '1'));
          await loginAs(page);
          await page.goto('/admin/explore');
          if (width < 560) await page.getByTestId('toolbar-nav').click();
          await expect(row(page, B)).toBeVisible();
          await row(page, B).focus();
          await page.keyboard.press('Shift+F10');
          await expect(page.getByRole('menu')).toBeVisible();
          const m = await noSidewaysOverflow(page);
          expect(m.page, 'the page scrolls sideways').toBeLessThanOrEqual(0);
          expect(m.panel, 'the panel scrolls sideways').toBeLessThanOrEqual(0);
          expect(m.menuInside, 'the menu leaves the window').toBe(true);
          await page.keyboard.press('Escape');
        } finally {
          await ctx.close();
        }
      });
    }
  }
});
