/**
 * 211-phone-install-band - task #190.
 *
 * Owner, 2026-10-06: "application filex yani tarayıcı app'i olarak kurulsun
 * bildirimi mobilde gelmiyor" - and then, for the fix: "ekran altı bandımız
 * var ya, aynısı olsun; filex web'e girince Windows için indir yeri gibi yer
 * yapalım, webapp olarak kursun".
 *
 * Measured here at phone size (390 x 844, touch, a phone's user agent), in a
 * real browser and a real layout, because that is exactly what the unit tests
 * cannot see: whether the band fits on the sign-in page without standing on
 * the form, and whether the file list really makes room for it so the upload
 * button (`.fe-fab`, same corner) is above it and still the thing under a tap.
 *
 *   - Android: the sign-in page's band offers the web app, never the desktop
 *     app; before the browser offers an install it says where the browser's
 *     menu has one, then it is the Install button, which calls the browser's
 *     own `prompt()`; accepted, the band is gone.
 *   - The file list wears the same band, above which the upload button sits.
 *   - × closes it for good in this browser (nobody signed in: the browser's
 *     own flag - a signed-in × would be written to the shared admin account
 *     and hide the band from every later run).
 *   - The installed app (display-mode: standalone): no band anywhere.
 *   - iPhone: Share → Add to Home Screen, no Install button.
 *   - A PC keeps its desktop-app band.
 *
 * `beforeinstallprompt` is dispatched by the spec - Chromium only fires it to
 * a page that meets its engagement heuristic, which a test cannot sit out -
 * with the same shape Chromium gives it (cancelable, `prompt`, `userChoice`).
 */
import { test, expect, type Locator, type Page } from '@playwright/test';
import { ADMIN_EMAIL, ADMIN_PASSWORD } from '../helpers/auth';
import { seedLocalStorage, dropStorageByName } from '../helpers/seed';

const STORAGE = `e2e-pwa-${Date.now()}`;
const MOUNT = `/tmp/filex-${STORAGE}`;

const ANDROID =
  'Mozilla/5.0 (Linux; Android 14; Pixel 8) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/129.0.0.0 Mobile Safari/537.36';
const IPHONE =
  'Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Mobile/15E148 Safari/604.1';

const PHONE = { viewport: { width: 390, height: 844 }, hasTouch: true, isMobile: true };

const band = (page: Page) => page.getByTestId('pwa-install-banner');

/** Chromium's install offer, as a page receives it; `prompt()` is counted. */
async function offerInstall(page: Page, outcome: 'accepted' | 'dismissed' = 'accepted') {
  await page.evaluate((o) => {
    const w = window as unknown as { __prompts: number };
    w.__prompts = 0;
    const e = new Event('beforeinstallprompt', { cancelable: true });
    Object.assign(e, {
      platforms: ['web'],
      prompt: async () => {
        w.__prompts += 1;
      },
      userChoice: Promise.resolve({ outcome: o, platform: 'web' }),
    });
    window.dispatchEvent(e);
  }, outcome);
}

/** Sign in on the page's own cookie jar WITHOUT the helper's pre-dismissal
 *  (helpers/auth loginAs closes this very band before the first paint). */
async function signIn(page: Page) {
  const res = await page.request.post('/api/auth/login', { data: { email: ADMIN_EMAIL, password: ADMIN_PASSWORD } });
  expect(res.ok(), `sign-in: ${res.status()}`).toBe(true);
}

/** `el` is what a tap at its centre reaches - nothing stands on it. */
async function reachable(el: Locator) {
  await el.scrollIntoViewIfNeeded();
  await el.click({ trial: true, timeout: 5_000 });
}

test.beforeAll(async ({ request }) => {
  await dropStorageByName(request, STORAGE);
  await seedLocalStorage(request, STORAGE, MOUNT);
});

test.afterAll(async ({ request }) => {
  await dropStorageByName(request, STORAGE);
});

test.beforeEach(async ({ page }) => {
  await page.addInitScript(() => {
    try {
      localStorage.setItem('filex.tourDone', '1');
    } catch {
      /* storage blocked: the tour shows, which the assertions do not touch */
    }
  });
});

test.describe('An Android phone', () => {
  test.use({ ...PHONE, userAgent: ANDROID });
  test.skip(({ browserName }) => browserName !== 'chromium', 'an Android phone is Chromium; the offer is a Chromium event');

  test('the sign-in page: the band offers the web app, never the desktop app, and Install opens the browser’s dialog', async ({ page }) => {
    await page.goto('/admin/login');
    await expect(band(page)).toHaveAttribute('data-place', 'band');
    await expect(page.getByTestId('pwa-menu-instructions')).toBeVisible();
    await expect(page.getByTestId('desktop-download-button')).toHaveCount(0);
    // The form stays reachable: the page makes room for the band.
    await reachable(page.locator('[data-install-clear] button[type="submit"]').first());

    await offerInstall(page);
    const install = page.getByTestId('pwa-install-button');
    await expect(install).toBeVisible();
    await expect(page.getByTestId('pwa-menu-instructions')).toHaveCount(0);
    await install.click();
    await expect.poll(() => page.evaluate(() => (window as unknown as { __prompts: number }).__prompts)).toBe(1);
    // Accepted: installed, nothing more to offer in this tab.
    await expect(band(page)).toHaveCount(0);
  });

  test('the file list: the same band, and the upload button above it is still what a tap reaches', async ({ page }) => {
    await signIn(page);
    await page.goto(`/admin/explore?storage=${encodeURIComponent(STORAGE)}`);
    await expect(band(page)).toHaveAttribute('data-place', 'band');
    await offerInstall(page);
    await expect(page.getByTestId('pwa-install-button')).toBeVisible();

    const fab = page.locator('.fe-fab');
    await expect(fab).toBeVisible();
    const fabBox = await fab.boundingBox();
    const cardBox = await page.getByTestId('pwa-install-card').boundingBox();
    expect(fabBox && cardBox, 'both have a box').toBeTruthy();
    expect(fabBox!.y + fabBox!.height, 'the upload button ends above the band').toBeLessThanOrEqual(cardBox!.y);
    await reachable(fab);
  });

  test('× closes it, and a reload keeps it closed', async ({ page }) => {
    await page.goto('/admin/login');
    await expect(band(page)).toBeVisible();
    await page.getByTestId('pwa-install-dismiss').click();
    await expect(band(page)).toHaveCount(0);
    await page.reload();
    await offerInstall(page);
    await expect(band(page)).toHaveCount(0);
  });

  test('the installed app (display-mode: standalone): no band, on the sign-in page or on the file list', async ({ page }) => {
    await page.addInitScript(() => {
      const real = window.matchMedia.bind(window);
      window.matchMedia = (q: string) =>
        /display-mode:\s*standalone/.test(q)
          ? ({
              matches: true,
              media: q,
              onchange: null,
              addEventListener() {},
              removeEventListener() {},
              addListener() {},
              removeListener() {},
              dispatchEvent: () => false,
            } as unknown as MediaQueryList)
          : real(q);
    });
    await page.goto('/admin/login');
    await offerInstall(page);
    await expect(page.getByTestId('pwa-install-dismiss')).toHaveCount(0);
    await expect(band(page)).toHaveCount(0);

    await signIn(page);
    await page.goto(`/admin/explore?storage=${encodeURIComponent(STORAGE)}`);
    await expect(page.locator('.fe-fab')).toBeVisible();
    await offerInstall(page);
    await expect(band(page)).toHaveCount(0);
  });
});

test.describe('An iPhone', () => {
  test.use({ ...PHONE, userAgent: IPHONE });
  test.skip(({ browserName }) => browserName !== 'chromium', 'the answer is the user agent’s; one engine is enough');

  test('the band says Share → Add to Home Screen, with no Install button and no desktop app', async ({ page }) => {
    await page.goto('/admin/login');
    await expect(band(page)).toHaveAttribute('data-place', 'band');
    await expect(page.getByTestId('pwa-ios-instructions')).toContainText('Add to Home Screen');
    await expect(page.getByTestId('pwa-install-button')).toHaveCount(0);
    await expect(page.getByTestId('desktop-download-button')).toHaveCount(0);
  });
});

test.describe('A PC', () => {
  test.use({ viewport: { width: 1280, height: 800 } });
  test.skip(({ browserName }) => browserName !== 'chromium', 'the platform is the user agent’s; one engine is enough');

  test('keeps its desktop-app band on the sign-in page, and is never offered the web app', async ({ page }) => {
    await page.goto('/admin/login');
    await expect(band(page)).toBeVisible();
    await offerInstall(page);
    // Where the full card would stand on the form (SSO + password at this
    // height) the sign-in page gives a PC the corner chip instead; open it.
    // (The fit is measured a frame after the band is drawn.)
    await page.waitForTimeout(500);
    if ((await band(page).getAttribute('data-place')) !== 'band') await page.getByTestId('pwa-install-toggle').click();
    await expect(page.getByTestId('desktop-download-button').first()).toBeVisible();
    await expect(page.getByTestId('pwa-install-button')).toHaveCount(0);
    await expect(page.getByTestId('pwa-menu-instructions')).toHaveCount(0);
  });
});
