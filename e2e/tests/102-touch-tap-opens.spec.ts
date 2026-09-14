/**
 * 102-touch-tap-opens — issue #26, "On 1st press on mobile devices it selects
 * file/folder … what makes impossible to use on mobile devices. In any
 * browser."
 *
 * The explorer speaks the desktop grammar: a click selects, a double-click
 * opens. A finger has no double-click — the browser either swallows the second
 * tap into a zoom or never delivers `dblclick` at all — so on a phone nothing
 * could be opened. The grammar a phone expects is the one every mobile file
 * manager uses, and it is what this spec pins, on a real touch-emulating
 * Chromium at phone size:
 *
 *   1. a tap on a folder walks into it;
 *   2. a tap on a file opens it;
 *   3. a long press selects (and opens the menu), and while something is
 *      selected a tap adds to the selection instead of opening — so picking
 *      several files still works.
 *
 * A mouse keeps the desktop grammar: the decision is made from the gesture
 * that produced the click, not from the screen size, so a touch laptop's
 * trackpad and its screen each behave as themselves.
 */
import { test, expect, type Locator } from '@playwright/test';
import { loginAs, apiLogin } from '../helpers/auth';
import { seedLocalStorage, dropStorageByName } from '../helpers/seed';

const STORAGE = `e2e-tap-${Date.now()}`;
const MOUNT = `/tmp/filex-${STORAGE}`;
const PNG = Buffer.from(
  'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8DwHwAFBQIAX8jx0gAAAABJRU5ErkJggg==',
  'base64',
);

test.use({ hasTouch: true, isMobile: true, viewport: { width: 390, height: 844 } });

async function upload(request: import('@playwright/test').APIRequestContext, dir: string, name: string, mimeType: string, buffer: Buffer) {
  const up = await request.post('/api/files/manager?action=upload', {
    multipart: { path: `${STORAGE}://${dir}`, 'file[]': { name, mimeType, buffer } },
  });
  if (!up.ok()) throw new Error(`upload ${name} failed: ${up.status()} ${await up.text()}`);
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

test.describe('Touch — a tap opens (issue #26)', () => {
  test.beforeAll(async ({ request }) => {
    await dropStorageByName(request, STORAGE);
    await seedLocalStorage(request, STORAGE, MOUNT);
    await apiLogin(request);
    const mk = await request.post('/api/files/manager?action=newfolder', {
      data: { path: `${STORAGE}://`, name: 'photos' },
    });
    if (!mk.ok()) throw new Error(`mkdir failed: ${mk.status()} ${await mk.text()}`);
    await upload(request, 'photos', 'inner.txt', 'text/plain', Buffer.from('inside\n'));
    await upload(request, '', 'dot.png', 'image/png', PNG);
    await upload(request, '', 'other.txt', 'text/plain', Buffer.from('other\n'));
  });

  test.afterAll(async ({ request }) => {
    await dropStorageByName(request, STORAGE);
  });

  test.beforeEach(async ({ page }) => {
    await page.addInitScript(() => localStorage.setItem('filex.tourDone', '1'));
    await loginAs(page);
    await page.goto(`/admin/explore?storage=${encodeURIComponent(STORAGE)}`);
  });

  test('a tap on a folder walks into it', async ({ page }) => {
    const folder = page.locator(`[data-fe-path="${STORAGE}://photos"]`);
    await expect(folder).toBeVisible({ timeout: 15_000 });
    await folder.tap();
    await expect(page.locator(`[data-fe-path="${STORAGE}://photos/inner.txt"]`)).toBeVisible({ timeout: 10_000 });
  });

  test('a tap on a file opens it', async ({ page }) => {
    const file = page.locator(`[data-fe-path="${STORAGE}://dot.png"]`);
    await expect(file).toBeVisible({ timeout: 15_000 });
    await file.tap();
    await expect(page.locator('.fe-viewer')).toBeVisible({ timeout: 10_000 });
  });

  test('a long press selects, and then a tap adds to the selection instead of opening', async ({ page }) => {
    const png = page.locator(`[data-fe-path="${STORAGE}://dot.png"]`);
    const other = page.locator(`[data-fe-path="${STORAGE}://other.txt"]`);
    await expect(png).toBeVisible({ timeout: 15_000 });

    await longPress(png);
    await expect(png).toHaveAttribute('aria-selected', 'true');
    await page.keyboard.press('Escape');

    await other.tap();
    await expect(other).toHaveAttribute('aria-selected', 'true');
    await expect(png, 'the first pick survives the second').toHaveAttribute('aria-selected', 'true');
    await expect(page.locator('.fe-viewer')).toHaveCount(0);
  });
});
