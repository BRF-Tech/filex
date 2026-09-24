/**
 * 102-touch-tap-opens — issue #26.
 *
 * First report: "On 1st press on mobile devices it selects file/folder … what
 * makes impossible to use on mobile devices. In any browser."
 *
 * Second report (v0.41.1): "on any device if user presses on folder/file name
 * — it opens · tap and hold on mobile device / second click on desktop — drops
 * menu · click/tap on any device on checkbox — same behaviour as now".
 *
 * Third report (v0.41.2): on a phone a tap on a name still only highlighted
 * the row. iOS WebKit ends a tap without a click when its hover step reveals
 * content; Chromium's touch emulation always delivers the click, so the
 * phone-size specs passed while the phone did not.
 *
 * Fourth report (v0.41.2, desktop): "need to click precisely on name, if a lil
 * bit on the right then it selects file, change it so if only clicking on
 * checkbox it selects it, any other click will open."
 *
 * So the rule this spec pins, on a touch-emulating Chromium at phone size AND
 * with a mouse at desktop size, in the list and on grid cards:
 *
 *   - the checkbox is the only click or tap that selects (shift extends);
 *   - any other click or tap on an item opens it — beside the name, with a
 *     selection, with Ctrl held;
 *   - a long press (finger) or a right click (mouse) opens the menu;
 *   - a tap opens even when the browser never delivers the click;
 *   - a habitual double-click opens the first item only, never the one the new
 *     listing puts under the pointer.
 */
import { test, expect, type Locator, type Page } from '@playwright/test';
import { loginAs, apiLogin } from '../helpers/auth';
import { seedLocalStorage, dropStorageByName } from '../helpers/seed';
import { setAccountViewMode, VIEW_MODE_LS_KEY } from '../helpers/prefs';

const STORAGE = `e2e-tap-${Date.now()}`;
const MOUNT = `/tmp/filex-${STORAGE}`;
const PNG = Buffer.from(
  'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8DwHwAFBQIAX8jx0gAAAABJRU5ErkJggg==',
  'base64',
);
/** FileExplorer's FIRST-PAINT cache of the view mode. ⚠ Not the preference —
 *  that is on the account, see `helpers/prefs`. */
const VIEW_MODE_KEY = VIEW_MODE_LS_KEY;

async function upload(request: import('@playwright/test').APIRequestContext, dir: string, name: string, mimeType: string, buffer: Buffer) {
  const up = await request.post('/api/files/manager?action=upload', {
    multipart: { path: `${STORAGE}://${dir}`, 'file[]': { name, mimeType, buffer } },
  });
  if (!up.ok()) throw new Error(`upload ${name} failed: ${up.status()} ${await up.text()}`);
}

async function mkdir(request: import('@playwright/test').APIRequestContext, dir: string, name: string) {
  const mk = await request.post('/api/files/manager?action=newfolder', {
    data: { path: `${STORAGE}://${dir}`, name },
  });
  if (!mk.ok()) throw new Error(`mkdir ${name} failed: ${mk.status()} ${await mk.text()}`);
}

/**
 * A real long press: touchStart, hold, touchEnd, through the same CDP input
 * pipeline `tap()` uses. `locator.dispatchEvent('touchstart')` is not one — it
 * builds an event without Touch objects, so the handler's `touches[0]` is
 * undefined and the gesture never happens.
 */
async function longPress(target: Locator) {
  const box = await target.boundingBox();
  if (!box) throw new Error('target has no box');
  const point = { x: box.x + box.width / 2, y: box.y + box.height / 2 };
  const cdp = await target.page().context().newCDPSession(target.page());
  await cdp.send('Input.dispatchTouchEvent', { type: 'touchStart', touchPoints: [point] });
  await target.page().waitForTimeout(800);
  await cdp.send('Input.dispatchTouchEvent', { type: 'touchEnd', touchPoints: [] });
  await cdp.detach();
}

const row = (page: Page, rel: string) => page.locator(`[data-fe-path="${STORAGE}://${rel}"]`);
const nameOf = (r: Locator) => r.locator('.fe-list__name, .fe-grid__label, .fe-gal__label').first();
const checkOf = (r: Locator) => r.locator('.fe-list__check').first();
/** The name cell, not the name: the reporter's "a lil bit on the right". */
const nameCellOf = (r: Locator) => r.locator('.fe-list__col--name').first();

/** A point inside `el`, `inset` px from its right edge. */
async function nearRightEdge(el: Locator, inset = 6) {
  await el.scrollIntoViewIfNeeded();
  const box = await el.boundingBox();
  if (!box) throw new Error('element has no box');
  return { x: box.x + box.width - inset, y: box.y + box.height / 2 };
}

/** Assert the point hits `el` itself and not the name inside it. */
async function expectBesideName(page: Page, point: { x: number; y: number }) {
  const hit = await page.evaluate(({ x, y }) => {
    const el = document.elementFromPoint(x, y);
    return { onName: !!el?.closest('.fe-list__name'), inRow: !!el?.closest('[data-fe-path]') };
  }, point);
  expect(hit, 'the point is on the row, beside the name').toEqual({ onName: false, inRow: true });
}

test.beforeAll(async ({ request }) => {
  await dropStorageByName(request, STORAGE);
  await seedLocalStorage(request, STORAGE, MOUNT);
  await apiLogin(request);
  await mkdir(request, '', 'photos');
  // First row inside `photos`: sits where `photos` sat, so the second click of a
  // double-click on `photos` lands on it.
  await mkdir(request, 'photos', 'albums');
  await upload(request, 'photos/albums', 'deep.txt', 'text/plain', Buffer.from('deep\n'));
  await upload(request, 'photos', 'inner.txt', 'text/plain', Buffer.from('inside\n'));
  await upload(request, '', 'dot.png', 'image/png', PNG);
  await upload(request, '', 'other.txt', 'text/plain', Buffer.from('other\n'));
});

test.afterAll(async ({ request }) => {
  await dropStorageByName(request, STORAGE);
});

/**
 * Open the explorer in `view`.
 *
 * ⚠⚠ The view mode is set in TWO places, and both are needed for different
 * reasons. `localStorage` is the first-paint cache, so the listing paints in
 * the right mode instead of flashing the other one; the ACCOUNT is the
 * preference, and it is what the explorer settles on a moment after boot
 * (`packages/core/src/lib/viewPrefs.ts`). Seeding only the browser key is what
 * made both grid cases here red on 2026-09-20: the page opened as a grid, the
 * account's document landed saying "list", and `.fe-grid__card` was gone
 * before the assertion ran. The browser key is a cache of the account's
 * answer — never the answer.
 */
async function openExplorer(page: Page, view: 'list' | 'grid' = 'list') {
  await page.addInitScript(
    ([key, mode]) => {
      localStorage.setItem('filex.tourDone', '1');
      localStorage.setItem(key, mode);
    },
    [VIEW_MODE_KEY, view] as const,
  );
  await loginAs(page);
  // `page.request` rides the browser context's own cookie jar, so this is the
  // signed-in account and not a second session.
  await setAccountViewMode(page.request, view);
  await page.goto(`/admin/explore?storage=${encodeURIComponent(STORAGE)}`);
  await expect(row(page, 'photos')).toBeVisible({ timeout: 15_000 });
  if (view === 'grid') await expect(page.locator('.fe-grid__card').first()).toBeVisible();
}

test.describe('Phone — a tap anywhere on an item opens it (issue #26)', () => {
  test.use({ hasTouch: true, isMobile: true, viewport: { width: 390, height: 844 } });

  test.beforeEach(async ({ page }) => openExplorer(page));

  test('a tap on a folder name walks into it', async ({ page }) => {
    await nameOf(row(page, 'photos')).tap();
    await expect(row(page, 'photos/inner.txt')).toBeVisible({ timeout: 10_000 });
  });

  test('a tap on a file name opens it', async ({ page }) => {
    await nameOf(row(page, 'dot.png')).tap();
    await expect(page.locator('.fe-viewer')).toBeVisible({ timeout: 10_000 });
  });

  test('a tap beside the name opens it too', async ({ page }) => {
    const point = await nearRightEdge(nameCellOf(row(page, 'photos')));
    await expectBesideName(page, point);
    await page.touchscreen.tap(point.x, point.y);
    await expect(row(page, 'photos/inner.txt')).toBeVisible({ timeout: 10_000 });
  });

  test('a long press selects; the checkbox adds; a tap on a row still opens while things are selected', async ({ page }) => {
    const png = row(page, 'dot.png');
    const other = row(page, 'other.txt');

    await longPress(png);
    await expect(png).toHaveAttribute('aria-selected', 'true');
    await page.keyboard.press('Escape');

    await checkOf(other).tap();
    await expect(other).toHaveAttribute('aria-selected', 'true');
    await expect(png, 'the first pick survives the second').toHaveAttribute('aria-selected', 'true');
    await expect(page.locator('.fe-viewer')).toHaveCount(0);

    const point = await nearRightEdge(nameCellOf(row(page, 'photos')));
    await page.touchscreen.tap(point.x, point.y);
    await expect(row(page, 'photos/inner.txt'), 'a row tap opens even with a selection').toBeVisible({ timeout: 10_000 });
  });
});

/**
 * The third report's failure, made measurable in Chromium: a tap must open
 * even when NO click ever arrives, and on a screen that cannot hover the
 * reveals must not run at all.
 */
test.describe('Phone — a tap does not depend on the click (issue #26, third round)', () => {
  test.use({ hasTouch: true, isMobile: true, viewport: { width: 390, height: 844 } });

  test.beforeEach(async ({ page }) => openExplorer(page));

  async function blockClicks(page: Page) {
    // What iOS does when a hover reveals content: the tap ends without a click.
    await page.evaluate(() => {
      document.addEventListener(
        'click',
        (e) => {
          e.stopImmediatePropagation();
          e.preventDefault();
        },
        true,
      );
    });
  }

  test('a tap on a folder name opens it even when the browser never delivers the click', async ({ page }) => {
    await blockClicks(page);
    await nameOf(row(page, 'photos')).tap();
    await expect(row(page, 'photos/inner.txt')).toBeVisible({ timeout: 10_000 });
  });

  test('a tap beside the name opens it even when the browser never delivers the click', async ({ page }) => {
    const point = await nearRightEdge(nameCellOf(row(page, 'photos')));
    await blockClicks(page);
    await page.touchscreen.tap(point.x, point.y);
    await expect(row(page, 'photos/inner.txt')).toBeVisible({ timeout: 10_000 });
  });

  test('one tap on a folder name opens exactly that folder, not the row the new listing puts under the finger', async ({ page }) => {
    await nameOf(row(page, 'photos')).tap();
    await expect(row(page, 'photos/inner.txt')).toBeVisible({ timeout: 10_000 });
    await page.waitForTimeout(700);
    await expect(row(page, 'photos/albums/deep.txt')).toHaveCount(0);
  });

  test('a screen that cannot hover never runs the hover reveals', async ({ page }) => {
    expect(await page.evaluate(() => matchMedia('(hover: hover)').matches), 'touch emulation reports no hover').toBe(false);
    const other = row(page, 'other.txt');
    const star = other.locator('.fe-list__col--star');
    test.skip((await star.count()) === 0, 'no star column at this width');
    const box = await other.boundingBox();
    if (!box) throw new Error('row has no box');
    await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2);
    await page.waitForTimeout(300);
    expect(await star.evaluate((el) => getComputedStyle(el).opacity)).toBe('0');
  });
});

test.describe('Phone — grid cards (issue #26, fourth round)', () => {
  test.use({ hasTouch: true, isMobile: true, viewport: { width: 390, height: 844 } });

  test.beforeEach(async ({ page }) => openExplorer(page, 'grid'));

  test('a card checkbox stays out of the way until something is selected, then ticks; a tap on a card opens', async ({ page }) => {
    const png = row(page, 'dot.png');
    const other = row(page, 'other.txt');
    const otherCheck = other.locator('.fe-item-check');

    await expect(otherCheck, 'no box on a card before anything is selected').toHaveCSS('opacity', '0');

    await longPress(png);
    await expect(png).toHaveAttribute('aria-selected', 'true');
    await page.keyboard.press('Escape');

    await expect(otherCheck, 'every card shows its box once something is selected').toHaveCSS('opacity', '1');
    await checkOf(other).tap();
    await expect(other).toHaveAttribute('aria-selected', 'true');
    await expect(png).toHaveAttribute('aria-selected', 'true');
    await expect(page.locator('.fe-viewer')).toHaveCount(0);

    await nameOf(row(page, 'photos')).tap();
    await expect(row(page, 'photos/inner.txt'), 'a card tap opens even with a selection').toBeVisible({ timeout: 10_000 });
  });
});

test.describe('Desktop — only the checkbox selects, any other click opens (issue #26)', () => {
  test.use({ hasTouch: false, isMobile: false, viewport: { width: 1280, height: 800 } });

  test.beforeEach(async ({ page }) => openExplorer(page));

  test('a click on a folder name walks into it', async ({ page }) => {
    await nameOf(row(page, 'photos')).click();
    await expect(row(page, 'photos/inner.txt')).toBeVisible({ timeout: 10_000 });
  });

  test('a click a little to the right of the name opens the folder', async ({ page }) => {
    const point = await nearRightEdge(nameCellOf(row(page, 'photos')));
    await expectBesideName(page, point);
    await page.mouse.click(point.x, point.y);
    await expect(row(page, 'photos/inner.txt')).toBeVisible({ timeout: 10_000 });
  });

  test('a click on the size and date cells opens the file instead of selecting it', async ({ page }) => {
    const other = row(page, 'other.txt');
    const box = await other.boundingBox();
    if (!box) throw new Error('row has no box');
    // Far right of the row: the size/date cells, not the name.
    const point = { x: box.x + box.width - 140, y: box.y + box.height / 2 };
    await expectBesideName(page, point);
    await page.mouse.click(point.x, point.y);
    await expect(page.locator('.fe-viewer')).toBeVisible({ timeout: 10_000 });
    await expect(other).toHaveAttribute('aria-selected', 'false');
  });

  test('a Ctrl-click on a row opens it as well — the checkbox is the only click that selects', async ({ page }) => {
    const other = row(page, 'other.txt');
    await nameCellOf(other).click({ modifiers: ['Control'], position: { x: 4, y: 4 } });
    await expect(page.locator('.fe-viewer')).toBeVisible({ timeout: 10_000 });
    await expect(other).toHaveAttribute('aria-selected', 'false');
  });

  test('the checkbox selects without opening, and shift on a checkbox extends the range', async ({ page }) => {
    const photos = row(page, 'photos');
    const png = row(page, 'dot.png');
    const other = row(page, 'other.txt');

    await checkOf(photos).click();
    await expect(photos).toHaveAttribute('aria-selected', 'true');
    await expect(row(page, 'photos/inner.txt'), 'ticking a folder does not walk into it').toHaveCount(0);

    await checkOf(other).click({ modifiers: ['Shift'] });
    await expect(other).toHaveAttribute('aria-selected', 'true');
    await expect(png, 'shift-tick fills the range between').toHaveAttribute('aria-selected', 'true');
    await expect(page.locator('.fe-viewer')).toHaveCount(0);

    await checkOf(png).click();
    await expect(png, 'a second tick takes it back out').toHaveAttribute('aria-selected', 'false');
  });

  test('a notification link ticks the file it names and does not open it', async ({ page }) => {
    // The bell's deep link selects its row from outside the component, by a
    // synthetic click. A click on the row itself now OPENS it, so the link has
    // to tick the checkbox — measured: before this it opened the file instead.
    await page.goto(`/admin/explore?select=${STORAGE}://other.txt#${STORAGE}`);
    const other = row(page, 'other.txt');
    await expect(other).toHaveAttribute('aria-selected', 'true', { timeout: 15_000 });
    await page.waitForTimeout(1200);
    await expect(other, 'the selection sticks').toHaveAttribute('aria-selected', 'true');
    await expect(page.locator('.fe-viewer'), 'the linked file is pointed at, not opened').toHaveCount(0);
    await expect(page.locator('[data-fe-path][aria-selected="true"]')).toHaveCount(1);
  });

  test('a right click opens the menu', async ({ page }) => {
    await nameOf(row(page, 'other.txt')).click({ button: 'right' });
    await expect(page.locator('.fe-ctx, .fe-context-menu, [role="menu"]').first()).toBeVisible({ timeout: 5_000 });
  });

  test('a double-click on a folder opens that folder only', async ({ page }) => {
    // Human speed, not Playwright's back-to-back dblclick: the new listing must
    // be on screen when the second click lands, or the test passes by accident
    // (the second click would hit the old `photos` row again).
    const { x, y } = await nearRightEdge(nameCellOf(row(page, 'photos')));
    await page.mouse.click(x, y);
    await page.waitForTimeout(250);
    await page.mouse.click(x, y);
    await expect(row(page, 'photos/inner.txt')).toBeVisible({ timeout: 10_000 });
    await page.waitForTimeout(700);
    await expect(row(page, 'photos/albums/deep.txt'), 'the second click must not open the row under the pointer').toHaveCount(0);
    await expect(row(page, 'photos/albums')).toBeVisible();
    await expect(row(page, 'photos/albums'), 'nor tick its box').toHaveAttribute('aria-selected', 'false');
  });
});

test.describe('Desktop — grid cards (issue #26, fourth round)', () => {
  test.use({ hasTouch: false, isMobile: false, viewport: { width: 1280, height: 800 } });

  test.beforeEach(async ({ page }) => openExplorer(page, 'grid'));

  test('a card shows its checkbox on hover; the checkbox selects; a click on the card opens', async ({ page }) => {
    const other = row(page, 'other.txt');
    const otherCheck = other.locator('.fe-item-check');
    const pngCheck = row(page, 'dot.png').locator('.fe-item-check');

    await page.mouse.move(5, 5);
    await expect(otherCheck, 'no box at rest').toHaveCSS('opacity', '0');
    await other.hover();
    await expect(otherCheck, 'the box shows on hover').toHaveCSS('opacity', '1');

    await checkOf(other).click();
    await expect(other).toHaveAttribute('aria-selected', 'true');
    await expect(page.locator('.fe-viewer')).toHaveCount(0);

    await page.mouse.move(5, 5);
    await expect(pngCheck, 'with a selection every card shows its box').toHaveCSS('opacity', '1');

    const png = row(page, 'dot.png');
    await nameOf(png).click();
    await expect(page.locator('.fe-viewer'), 'a click on a card opens it, selection or not').toBeVisible({ timeout: 10_000 });
    await expect(png).toHaveAttribute('aria-selected', 'false');
  });
});
