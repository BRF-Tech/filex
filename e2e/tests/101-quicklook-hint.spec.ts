/**
 * 101-quicklook-hint — the Space quick-look legend has to stay a pill.
 *
 * Issue #22: in the web UI the hint bar ("Space close · ↑↓ previous/next ·
 * Enter open") filled the whole window as a giant rounded blob over the
 * preview. Nothing was wrong with the component — the hint carries the `fe`
 * root class so it can read the `--fe-*` theme variables, and Explore.vue
 * sized the embedded explorer with `.explore-host[data-v-…] .fe { height:
 * 100% }`. That selector reached every descendant carrying the root class,
 * and a host selector outranks the package's own single class, so the pill
 * inherited the viewport height. The desktop app has no such wrapper, which
 * is why the same build looked right there and wrong here.
 *
 * ⚠ This is a CASCADE bug, so only a real browser with the real built CSS can
 * see it: a unit test that mounts the component renders the same DOM either
 * way. That is what this spec is for.
 *
 * Two tests:
 *   1. the hint is a pill (a single text line) at the bottom edge, teleported
 *      under <body> so no host container's `.fe` rule can reach it again;
 *   2. it names the key that is actually bound. The legend used to spell
 *      "Space" out by hand while the overlay's handler compared against the
 *      default key, so after a remap the peek opened on the new key, closed
 *      on the old one, and the pill named the old one.
 */
import { test, expect } from '@playwright/test';
import { loginAs, apiLogin } from '../helpers/auth';
import { seedLocalStorage, dropStorageByName } from '../helpers/seed';

const STORAGE = `e2e-ql-${Date.now()}`;
const MOUNT = `/tmp/filex-${STORAGE}`;
const FILE_NAME = 'quicklook.txt';

test.describe('Quick look — the hint bar stays a pill', () => {
  test.beforeAll(async ({ request }) => {
    await dropStorageByName(request, STORAGE);
    await seedLocalStorage(request, STORAGE, MOUNT);
    await apiLogin(request);
    const up = await request.post('/api/files/manager?action=upload', {
      multipart: {
        path: `${STORAGE}://`,
        'file[]': {
          name: FILE_NAME,
          mimeType: 'text/plain',
          buffer: Buffer.from('quick look me\n'),
        },
      },
    });
    if (!up.ok()) throw new Error(`upload failed: ${up.status()} ${await up.text()}`);
  });

  test.afterAll(async ({ request }) => {
    await dropStorageByName(request, STORAGE);
  });

  /**
   * The onboarding tour opens over a first-visit explorer and its dialog
   * swallows the click on the row, which reads exactly like a broken listing.
   * A returning user has seen it once; set the same flag the tour sets.
   */
  async function skipTour(page: import('@playwright/test').Page) {
    await page.addInitScript(() => localStorage.setItem('filex.tourDone', '1'));
  }

  test('Space opens quick look and the legend is one line tall', async ({ page }) => {
    await loginAs(page);
    await skipTour(page);
    await page.goto(`/admin/explore?storage=${encodeURIComponent(STORAGE)}`);

    const row = page.locator(`[data-fe-path="${STORAGE}://${FILE_NAME}"]`);
    await expect(row).toBeVisible({ timeout: 15_000 });
    await row.click();
    await page.keyboard.press('Space');

    const hint = page.locator('.fe-ql-hint');
    await expect(hint).toBeVisible({ timeout: 10_000 });

    const box = await hint.boundingBox();
    const viewport = page.viewportSize();
    expect(box, 'the hint has no box').not.toBeNull();
    expect(viewport, 'no viewport size').not.toBeNull();

    // A one-line pill at 12px with 6px padding measures ~29px. 60px leaves
    // room for a font or a wrapped legend without letting the regression
    // through: the bug produced the full viewport height.
    expect(
      box!.height,
      `hint is ${Math.round(box!.height)}px tall in a ${viewport!.height}px window — ` +
        'a host `.fe` rule is reaching it again (#22)',
    ).toBeLessThan(60);

    // It is a hint BAR: it sits at the bottom edge. With the viewport height
    // inherited, the pill started 14px ABOVE the top of the window.
    expect(box!.y, 'the hint left the bottom edge').toBeGreaterThan(viewport!.height / 2);

    // The mechanism, not just the symptom: teleported out of the explorer
    // tree, so a host container's `.fe` override cannot select it.
    const parentIsBody = await hint.evaluate((el) => el.parentElement === document.body);
    expect(
      parentIsBody,
      'the hint is back inside the explorer tree — host CSS can reach it again',
    ).toBe(true);
  });

  /**
   * The other half of an honest hint: it has to name the key that is bound,
   * not the key that shipped. Shortcuts are remappable (`filex.shortcuts` in
   * localStorage), and until 0.39.1 the legend spelled "Space" out by hand
   * while the overlay's own handler compared against the default key — so
   * after a remap the peek opened on the new key, closed on the old one, and
   * the pill named the old one.
   */
  test('the legend names the remapped key, and that key closes the peek', async ({ page }) => {
    await loginAs(page);
    await skipTour(page);
    await page.addInitScript(() => {
      localStorage.setItem('filex.shortcuts', JSON.stringify({ quicklook: 'Q' }));
    });
    await page.goto(`/admin/explore?storage=${encodeURIComponent(STORAGE)}`);

    const row = page.locator(`[data-fe-path="${STORAGE}://${FILE_NAME}"]`);
    await expect(row).toBeVisible({ timeout: 15_000 });
    await row.click();
    await page.keyboard.press('q');

    const hint = page.locator('.fe-ql-hint');
    await expect(hint, 'the remapped key did not open the peek').toBeVisible({ timeout: 10_000 });
    const text = (await hint.innerText()).replace(/\s+/g, ' ');
    expect(text, `legend reads "${text}"`).toContain('Q');
    expect(text, 'the legend still names the key that shipped').not.toContain('Space');

    // And the key it names is the one that closes it.
    await page.keyboard.press('q');
    await expect(hint, 'the remapped key did not close the peek').toBeHidden({ timeout: 10_000 });
  });
});
