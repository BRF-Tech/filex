/**
 * 102-touch-tap-opens — issue #26.
 *
 * First report: "On 1st press on mobile devices it selects file/folder … what
 * makes impossible to use on mobile devices. In any browser."
 *
 * Second report (v0.41.1), the reporter's rule for EVERY device:
 *   - "on any device if user presses on folder/file name — it opens"
 *   - "tap and hold on mobile device / second click on desktop — drops menu"
 *   - "click/tap on any device on checkbox — same behaviour as now"
 *
 * The first round made a tap open only while nothing was selected; after a
 * long press every tap toggled the selection, so a name could no longer be
 * opened — that is what "still does not work properly" was. This spec pins the
 * rule on a real touch-emulating Chromium at phone size AND with a mouse at
 * desktop size, because the rule is the same on both:
 *
 *   phone   tap on a name opens (with or without a selection) · long press
 *           selects and opens the menu · a tap on the checkbox selects;
 *   desktop click on a name opens · right click opens the menu · click beside
 *           the name or on the checkbox selects · a habitual double-click on a
 *           folder name opens THAT folder only, never the row the new listing
 *           puts under the pointer.
 */
import { test, expect, type Locator, type Page } from '@playwright/test';
import { loginAs, apiLogin } from '../helpers/auth';
import { seedLocalStorage, dropStorageByName } from '../helpers/seed';

const STORAGE = `e2e-tap-${Date.now()}`;
const MOUNT = `/tmp/filex-${STORAGE}`;
const PNG = Buffer.from(
  'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8DwHwAFBQIAX8jx0gAAAABJRU5ErkJggg==',
  'base64',
);

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
async function longPress(row: Locator) {
  const box = await row.boundingBox();
  if (!box) throw new Error('row has no box');
  const point = { x: box.x + box.width / 2, y: box.y + box.height / 2 };
  const cdp = await row.page().context().newCDPSession(row.page());
  await cdp.send('Input.dispatchTouchEvent', { type: 'touchStart', touchPoints: [point] });
  await row.page().waitForTimeout(800);
  await cdp.send('Input.dispatchTouchEvent', { type: 'touchEnd', touchPoints: [] });
  await cdp.detach();
}

const row = (page: Page, rel: string) => page.locator(`[data-fe-path="${STORAGE}://${rel}"]`);
const nameOf = (r: Locator) => r.locator('.fe-list__name, .fe-grid__label, .fe-gal__label').first();
const checkOf = (r: Locator) => r.locator('.fe-list__check').first();

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

async function openExplorer(page: Page) {
  await page.addInitScript(() => localStorage.setItem('filex.tourDone', '1'));
  await loginAs(page);
  await page.goto(`/admin/explore?storage=${encodeURIComponent(STORAGE)}`);
  await expect(row(page, 'photos')).toBeVisible({ timeout: 15_000 });
}

test.describe('Phone — a tap on the name opens (issue #26)', () => {
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

  test('a long press selects; the checkbox adds; a name still opens while things are selected', async ({ page }) => {
    const png = row(page, 'dot.png');
    const other = row(page, 'other.txt');

    await longPress(png);
    await expect(png).toHaveAttribute('aria-selected', 'true');
    await page.keyboard.press('Escape');

    await checkOf(other).tap();
    await expect(other).toHaveAttribute('aria-selected', 'true');
    await expect(png, 'the first pick survives the second').toHaveAttribute('aria-selected', 'true');
    await expect(page.locator('.fe-viewer')).toHaveCount(0);

    await nameOf(row(page, 'photos')).tap();
    await expect(row(page, 'photos/inner.txt'), 'the name opens even with a selection').toBeVisible({ timeout: 10_000 });
  });
});

/**
 * Third report (v0.41.2): "on mobile … when I tap on folders/files name, it
 * highlights it but do not open, so need to tap second time". Desktop worked.
 *
 * A tap reaches the page as a click only at the end of the browser's emulated
 * mouse sequence, and iOS WebKit stops that sequence when the hover step
 * reveals content (the star column / chip fade in on :hover) — the row
 * highlights and no click follows. Chromium's touch emulation always delivers
 * the click, which is why the specs above passed while the phone did not.
 *
 * These pin the two halves of the fix in a way Chromium can measure: a name
 * tap must open even when NO click ever arrives, and on a screen that cannot
 * hover the reveals must not run at all.
 */
test.describe('Phone — a name tap does not depend on the click (issue #26, third round)', () => {
  test.use({ hasTouch: true, isMobile: true, viewport: { width: 390, height: 844 } });

  test.beforeEach(async ({ page }) => openExplorer(page));

  test('a tap on a folder name opens it even when the browser never delivers the click', async ({ page }) => {
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
    await nameOf(row(page, 'photos')).tap();
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

test.describe('Desktop — a click on the name opens (issue #26)', () => {
  test.use({ hasTouch: false, isMobile: false, viewport: { width: 1280, height: 800 } });

  test.beforeEach(async ({ page }) => openExplorer(page));

  test('a click on a folder name walks into it; a click beside the name selects', async ({ page }) => {
    const other = row(page, 'other.txt');
    const box = await other.boundingBox();
    if (!box) throw new Error('row has no box');
    // Far right of the row: the size/date cells, not the name.
    await page.mouse.click(box.x + box.width - 140, box.y + box.height / 2);
    await expect(other).toHaveAttribute('aria-selected', 'true');
    await expect(page.locator('.fe-viewer')).toHaveCount(0);

    await nameOf(row(page, 'photos')).click();
    await expect(row(page, 'photos/inner.txt')).toBeVisible({ timeout: 10_000 });
  });

  test('a click on a file name opens it; the checkbox selects without opening', async ({ page }) => {
    const png = row(page, 'dot.png');
    await checkOf(png).click();
    await expect(png).toHaveAttribute('aria-selected', 'true');
    await expect(page.locator('.fe-viewer')).toHaveCount(0);

    await nameOf(png).click();
    await expect(page.locator('.fe-viewer')).toBeVisible({ timeout: 10_000 });
  });

  test('a right click opens the menu', async ({ page }) => {
    await nameOf(row(page, 'other.txt')).click({ button: 'right' });
    await expect(page.locator('.fe-ctx, .fe-context-menu, [role="menu"]').first()).toBeVisible({ timeout: 5_000 });
  });

  test('a double-click on a folder name opens that folder only', async ({ page }) => {
    // Human speed, not Playwright's back-to-back dblclick: the new listing must
    // be on screen when the second click lands, or the test passes by accident
    // (the second click would hit the old `photos` row again).
    const box = await nameOf(row(page, 'photos')).boundingBox();
    if (!box) throw new Error('name has no box');
    const x = box.x + box.width / 2;
    const y = box.y + box.height / 2;
    await page.mouse.click(x, y);
    await page.waitForTimeout(250);
    await page.mouse.click(x, y);
    await expect(row(page, 'photos/inner.txt')).toBeVisible({ timeout: 10_000 });
    await page.waitForTimeout(700);
    await expect(row(page, 'photos/albums/deep.txt'), 'the second click must not open the row under the pointer').toHaveCount(0);
    await expect(row(page, 'photos/albums')).toBeVisible();
  });
});
