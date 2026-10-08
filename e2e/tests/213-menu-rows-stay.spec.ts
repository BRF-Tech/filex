/**
 * 213 - the right-click menu holds still, and does not wait (#196).
 *
 * The 0.53 release run (full-20261007-013728Z, e2e 115-tag-kinds, Firefox):
 * a file's menu opened before the server had answered "may this file be
 * encrypted?" (POST /api/files/e2e/allowed). The answer landed 101 ms later,
 * "Encrypt with E2EE…" was drawn under "Download" and every row below it moved
 * one place down - between the press and the release of a click on "Tags".
 * The release landed on "Star", the menu stayed open and nothing opened.
 *
 * The first round made the menu wait for its answers, up to 400 ms. The
 * maintainers' rule since (2026-10-08): a menu does not wait on the network.
 * The answers are asked when a folder is listed (one batched request per
 * question, FileExplorer prefetchMenuAnswers) and remembered per person and
 * storage in this browser (lib/menuAnswers), so a menu opens on them at once;
 * only a right-click faster than the folder's own questions waits, about
 * 40 ms. An open menu never moves a row (ContextMenu + lib/heldMenuRows): an
 * answer slower than that adds its row at the end. A sign-out forgets them.
 *
 * The server's answer is staged with `page.route`, installed before the
 * folder is listed, and always says `allowed`: what is under test is when the
 * menu opens and what it draws, not the tenant's policy (197-e2e-policy-approval
 * changes that on the same server). The time a menu took to open is measured
 * in the page (from the `contextmenu` event to the menu's element), not by the
 * test runner, whose own round trips are larger than what is measured.
 */
import { test, expect, type Page, type Route } from '@playwright/test';
import { loginAs, logout } from '../helpers/auth';
import { dropStorageByName, seedLocalStorage } from '../helpers/seed';
import { settled } from '../helpers/stable';

const STAMP = Date.now();
const STORAGE = `e2e-hold-${STAMP}`;
const ALLOWED = '**/api/files/e2e/allowed';
const ENCRYPT = /^(Encrypt with E2EE…|E2EE ile şifrele…)$/;
const DOWNLOAD = /^(Download|İndir)$/;
const TAGS = /^(Tags…|Etiketler…)$/;
const MENU_KEYS = 'filex.menuAnswers.v1|';

async function upload(page: Page, name: string) {
  const res = await page.request.post('/api/files/manager?action=upload', {
    multipart: { path: `${STORAGE}://`, 'file[]': { name, mimeType: 'text/plain', buffer: Buffer.from('hold still') } },
  });
  expect(res.ok(), `upload ${name}: ${res.status()}`).toBeTruthy();
}

function row(page: Page, rel: string) {
  return page.locator(`[data-fe-path="${STORAGE}://${rel}"]`).first();
}

/** The answer to the encryption question that names `name`. */
function answerFor(page: Page, name: string, timeout = 20_000) {
  return page.waitForResponse(
    (r) => r.url().includes('/api/files/e2e/allowed') && (r.request().postData() ?? '').includes(name),
    { timeout },
  );
}

/** Signed in, in the storage, with a file of its own that this browser has
 *  never asked about. `answer` stages the encryption question BEFORE the
 *  folder is listed with the file in it: the listing is what asks it now.
 *  Resolves to the answer the listing asked for the file (a promise: it may
 *  still be on its way). */
async function freshFile(page: Page, name: string, answer: (route: Route) => Promise<void>) {
  await page.addInitScript(() => localStorage.setItem('filex.tourDone', '1'));
  await loginAs(page);
  await page.goto(`/drive/explore?storage=${encodeURIComponent(STORAGE)}`);
  await upload(page, name);
  await page.route(ALLOWED, answer);
  const asked = answerFor(page, name);
  // Not unhandled when a test never waits for it.
  asked.catch(() => undefined);
  await page.reload();
  await expect(row(page, name)).toBeVisible({ timeout: 15_000 });
  return { asked };
}

/** Answer the encryption question `allowed`, for every path it names. */
async function answerAllowed(route: Route) {
  const items = (route.request().postDataJSON()?.items ?? []) as unknown[];
  await route.fulfill({
    status: 200,
    contentType: 'application/json',
    body: JSON.stringify({ encrypt: items.map(() => 'allowed') }),
  });
}

/** Answer after `ms`. */
const answerAfter = (ms: number) => async (route: Route) => {
  await new Promise((r) => setTimeout(r, ms));
  await answerAllowed(route);
};

/** Each entry of the open menu, in order, with where it is on screen. */
async function menuRows(page: Page): Promise<Array<{ label: string; top: number }>> {
  return page.evaluate(() =>
    Array.from(document.querySelectorAll('.fe-ctx .fe-ctx__item')).map((b) => ({
      label: (b.querySelector('.fe-ctx__label')?.textContent ?? '').trim(),
      top: Math.round(b.getBoundingClientRect().top),
    })),
  );
}

/** Start the in-page clock for the next right-click: it stops when the menu's
 *  element is in the document. */
async function armMenuClock(page: Page) {
  await page.evaluate(() => {
    const w = window as unknown as { __ctxAt?: number; __menuAt?: number };
    w.__ctxAt = undefined;
    w.__menuAt = undefined;
    document.addEventListener('contextmenu', () => (w.__ctxAt = performance.now()), { capture: true, once: true });
    const seen = new MutationObserver(() => {
      if (w.__ctxAt === undefined || w.__menuAt !== undefined) return;
      if (document.querySelector('.fe-ctx, .fe-sheet')) {
        w.__menuAt = performance.now();
        seen.disconnect();
      }
    });
    seen.observe(document.body, { childList: true, subtree: true });
  });
}

/** How long the last right-click took to open its menu, in the page. */
async function menuOpenedIn(page: Page): Promise<number> {
  await expect
    .poll(() => page.evaluate(() => (window as unknown as { __menuAt?: number }).__menuAt !== undefined))
    .toBe(true);
  return page.evaluate(() => {
    const w = window as unknown as { __ctxAt: number; __menuAt: number };
    return w.__menuAt - w.__ctxAt;
  });
}

async function rightClick(page: Page, name: string, { settle = true } = {}) {
  const target = await settled(row(page, name));
  await armMenuClock(page);
  await target.click({ button: 'right' });
  await target.dispose();
  const menu = page.locator('.fe-ctx');
  await expect(menu).toBeVisible();
  // Its place is set once (clamped into the window): read it after that.
  if (settle) await (await settled(menu, { samples: 3, interval: 80 })).dispose();
  return menu;
}

/** The keys this browser keeps the menu answers under. */
function menuKeys(page: Page): Promise<string[]> {
  return page.evaluate((prefix) => Object.keys(localStorage).filter((k) => k.startsWith(prefix)), MENU_KEYS);
}

test.describe('The right-click menu holds still, and does not wait (#196)', () => {
  test.describe.configure({ mode: 'serial' });

  test.beforeAll(async ({ request }) => {
    await dropStorageByName(request, STORAGE);
    await seedLocalStorage(request, STORAGE, `/tmp/filex-${STORAGE}`);
  });

  test.afterAll(async ({ request }) => {
    await dropStorageByName(request, STORAGE);
  });

  test('an answer the listing already has: the menu opens at once with every row in its place', async ({ page }) => {
    const file = `zamaninda-${STAMP}.txt`;
    // Slower than a menu opens on its own: a menu that asked only now would
    // wait for this, or draw the row late.
    const { asked } = await freshFile(page, file, answerAfter(300));
    await asked;

    const menu = await rightClick(page, file, { settle: false });
    expect(await menuOpenedIn(page), 'the menu did not wait for the network').toBeLessThan(200);
    const labels = (await menuRows(page)).map((r) => r.label);
    const download = labels.findIndex((l) => DOWNLOAD.test(l));
    expect(download, `Download in ${JSON.stringify(labels)}`).toBeGreaterThan(-1);
    expect(labels[download + 1], `the row after Download in ${JSON.stringify(labels)}`).toMatch(ENCRYPT);
    await expect(menu.getByRole('menuitem', { name: ENCRYPT })).toBeVisible();
  });

  test('a second visit opens on the answers this browser remembered, while they are asked again', async ({ page }) => {
    const file = `ikinci-${STAMP}.txt`;
    const { asked } = await freshFile(page, file, answerAllowed);
    await asked;
    await expect.poll(() => menuKeys(page), 'the answers are remembered').not.toEqual([]);

    // The next page's questions take seconds: the menu does not wait for them.
    await page.unroute(ALLOWED);
    await page.route(ALLOWED, answerAfter(4_000));
    await page.reload();
    await expect(row(page, file)).toBeVisible({ timeout: 15_000 });

    const menu = await rightClick(page, file, { settle: false });
    expect(await menuOpenedIn(page)).toBeLessThan(200);
    const labels = (await menuRows(page)).map((r) => r.label);
    const download = labels.findIndex((l) => DOWNLOAD.test(l));
    expect(labels[download + 1], `the remembered row is in its place: ${JSON.stringify(labels)}`).toMatch(ENCRYPT);
    await expect(menu.getByRole('menuitem', { name: ENCRYPT })).toBeVisible();
  });

  test('a right-click faster than the listing’s questions waits about 40 ms; the late row is added at the end', async ({ page }) => {
    const file = `gec-${STAMP}.txt`;
    const { asked: answered } = await freshFile(page, file, answerAfter(4_000));

    const menu = await rightClick(page, file);
    expect(await menuOpenedIn(page), 'not the 400 ms of the first round').toBeLessThan(250);
    const first = await menuRows(page);
    expect(first.some((r) => ENCRYPT.test(r.label)), 'the answer is not in yet').toBe(false);

    await answered;
    await expect(menu.getByRole('menuitem', { name: ENCRYPT })).toBeVisible();
    const after = await menuRows(page);
    expect(after.slice(0, first.length), 'every row is where it was drawn').toEqual(first);
    expect(after[after.length - 1]?.label).toMatch(ENCRYPT);
  });

  test('a click pressed on Tags while a late answer lands still opens Tags', async ({ page }) => {
    const file = `etiket-${STAMP}.txt`;
    // The answer is held until the click is half done.
    let release!: () => void;
    const gate = new Promise<void>((r) => (release = r));
    const { asked } = await freshFile(page, file, async (route) => {
      await gate;
      await answerAllowed(route);
    });

    const menu = await rightClick(page, file);
    const tags = menu.getByRole('menuitem', { name: TAGS });
    const box = await tags.boundingBox();
    expect(box, 'Tags is on screen').not.toBeNull();
    await page.mouse.move(box!.x + box!.width / 2, box!.y + box!.height / 2);
    await page.mouse.down();

    // The answer lands between the press and the release, as in the 0.53 run.
    release();
    await asked;
    await expect(menu.getByRole('menuitem', { name: ENCRYPT })).toBeVisible();
    expect(await tags.boundingBox(), 'Tags did not move under the pointer').toEqual(box);

    await page.mouse.up();
    await expect(page.locator('.filex-tag-picker').last(), 'the click landed on Tags').toBeVisible();
  });

  test('signing out forgets the remembered answers', async ({ page }) => {
    const file = `cikis-${STAMP}.txt`;
    const { asked } = await freshFile(page, file, answerAllowed);
    await asked;
    await expect.poll(() => menuKeys(page)).not.toEqual([]);

    await logout(page);
    await expect.poll(() => menuKeys(page), 'nothing of the person is left in this browser').toEqual([]);
  });
});
