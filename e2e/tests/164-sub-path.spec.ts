/**
 * 164 — filex served under a sub-path (FILEX_BASE_PATH, issue #70).
 *
 * The whole suite runs under a base with `node e2e/run.mjs local --base-path
 * /filex` (a proxy that passes the full path, and a verdict that fails the run
 * when the APP asks for anything outside the base — lib/subpath-proxy.mjs).
 * This spec names the journeys that matter most for a sub-path deployment and
 * asserts WHERE each of them lands, which the other specs do not look at:
 * sign-in, the explorer (list, upload, download, a selection ZIP), a share
 * link opened by a stranger, the admin pages, a hard reload on a deep link,
 * and the installable app's manifest and service worker.
 *
 * At the root (E2E_BASE_PATH unset) the same journeys run and assert the root
 * addresses — the deployment every existing install has, unchanged.
 */
import { test, expect, type Page } from '@playwright/test';
import { loginAs } from '../helpers/auth';
import { dropStorageByName, seedLocalStorage } from '../helpers/seed';
import { setAccountViewMode } from '../helpers/prefs';
import { settled } from '../helpers/stable';
import { nextHandedFile } from '../helpers/download';
import { BASE_PATH as BASE } from '../helpers/base';

const STAMP = Date.now();
const STORAGE = `e2e-subpath-${STAMP}`;
const pathOf = (page: Page) => new URL(page.url()).pathname;

function row(page: Page, name: string) {
  return page.locator(`[data-fe-path="${STORAGE}://${name}"]`).first();
}

async function openStorage(page: Page) {
  await page.addInitScript(() => localStorage.setItem('filex.tourDone', '1'));
  await loginAs(page);
  await setAccountViewMode(page.request, 'list');
  await page.goto(`${BASE}/drive/explore?storage=${encodeURIComponent(STORAGE)}`);
}

test.describe.serial('Sub-path deployment — every journey stays under the base', () => {
  test.beforeAll(async ({ request }) => {
    await dropStorageByName(request, STORAGE);
    await seedLocalStorage(request, STORAGE, `/tmp/filex-${STORAGE}`);
  });

  test.afterAll(async ({ request }) => {
    await dropStorageByName(request, STORAGE);
  });

  test('sign-in lands under the base, the app knows it, the session is scoped to it', async ({ page, context }) => {
    await loginAs(page);
    expect(pathOf(page).startsWith(`${BASE}/admin/`), pathOf(page)).toBe(true);
    const meta = page.locator('meta[name="filex-base"]');
    if (BASE) await expect(meta).toHaveAttribute('content', BASE);
    else await expect(meta).toHaveCount(0);
    const session = (await context.cookies()).find((c) => c.name === 'filex_session');
    expect(session, 'a session cookie').toBeTruthy();
    expect(session!.path).toBe(BASE || '/');
  });

  test('the admin pages load, and a hard reload on a deep link stays on it', async ({ page }) => {
    await loginAs(page);
    for (const p of ['storages', 'users', 'settings', 'audit']) {
      await page.goto(`${BASE}/admin/${p}`);
      await expect(page.locator('main h1, h1').first()).toBeVisible();
      expect(pathOf(page)).toBe(`${BASE}/admin/${p}`);
    }
    await page.goto(`${BASE}/admin/storages`);
    await page.reload();
    expect(pathOf(page)).toBe(`${BASE}/admin/storages`);
    await expect(page.getByText(STORAGE).first()).toBeVisible();
  });

  test('explorer: upload, list, download one file and a selection', async ({ page }) => {
    // A click SELECTS here (the product default): the suite pins single-click
    // open (playwright config), and a selection is what this test builds.
    await page.addInitScript(() => localStorage.setItem('filex.openTrigger', 'double'));
    await openStorage(page);
    expect(pathOf(page)).toBe(`${BASE}/drive/explore`);
    const names = [`a-${STAMP}.txt`, `b-${STAMP}.txt`];
    await page.locator('input[type="file"]').first().setInputFiles(
      names.map((name) => ({ name, mimeType: 'text/plain', buffer: Buffer.from(`body of ${name}`) })),
    );
    for (const n of names) await expect(row(page, n)).toBeVisible({ timeout: 20_000 });

    // A hard reload on the explorer's deep link lists the same folder.
    await page.reload();
    for (const n of names) await expect(row(page, n)).toBeVisible({ timeout: 20_000 });

    // One file, from its row menu.
    const one = await settled(row(page, names[0]));
    await one.click({ button: 'right' });
    await one.dispose();
    const menu = page.getByRole('menu').first();
    await expect(menu).toBeVisible();
    // One file opens its body in a new tab (helpers/download: and WebKit
    // shows a text file there rather than saving it).
    const [dl] = await Promise.all([
      nextHandedFile(page),
      menu.getByRole('menuitem', { name: /^(Download|İndir)$/ }).click(),
    ]);
    expect(new URL(dl.url).pathname.startsWith(`${BASE}/`), dl.url).toBe(true);
    expect(dl.filename).toBe(names[0]);

    // The selection as one ZIP: minted by the API, fetched from a /z/ ticket —
    // both under the base (the ticket is the address that used to lose it).
    await page.keyboard.press('Escape');
    const first = await settled(row(page, names[0]));
    await first.click();
    await first.dispose();
    const second = await settled(row(page, names[1]));
    await second.click({ modifiers: ['Control'] });
    await second.click({ button: 'right' });
    await second.dispose();
    const selMenu = page.getByRole('menu').first();
    await expect(selMenu).toBeVisible();
    const minted = page.waitForResponse((r) => r.url().includes('/api/files/archive/download'));
    const [zip] = await Promise.all([
      page.waitForEvent('download'),
      selMenu.getByRole('menuitem', { name: /^(Download|İndir)/ }).first().click(),
    ]);
    expect(new URL((await minted).url()).pathname).toBe(`${BASE}/api/files/archive/download`);
    expect(new URL(zip.url()).pathname.startsWith(`${BASE}/z/`), zip.url()).toBe(true);
    expect(zip.suggestedFilename()).toMatch(/\.zip$/);
  });

  test('a share link names the base, and a stranger can open it', async ({ page, browser }) => {
    await loginAs(page);
    const res = await page.request.post(`${BASE}/api/files/share`, {
      data: { path: `${STORAGE}://a-${STAMP}.txt` },
    });
    expect(res.ok(), `share: ${res.status()}`).toBeTruthy();
    const body = (await res.json()) as { url?: string; link?: string; token?: string };
    const link = body.url ?? body.link ?? '';
    expect(new URL(link).pathname).toBe(`${BASE}/s/${body.token}`);

    const stranger = await browser.newContext();
    const anon = await stranger.newPage();
    await anon.goto(link);
    expect(new URL(anon.url()).pathname).toBe(`${BASE}/s/${body.token}`);
    await expect(anon.getByText(`a-${STAMP}.txt`).first()).toBeVisible({ timeout: 15_000 });
    // The button navigates this page (helpers/download: WebKit shows a text
    // file there rather than saving it).
    const [dl] = await Promise.all([nextHandedFile(anon), anon.getByTestId('public-share-download').click()]);
    expect(new URL(dl.url).pathname.startsWith(`${BASE}/s/`), dl.url).toBe(true);
    await stranger.close();
  });

  test('the installable app: manifest and service worker under the base', async ({ page }) => {
    await loginAs(page);
    const res = await page.request.get(`${BASE}/admin/manifest.webmanifest`);
    expect(res.ok()).toBeTruthy();
    const m = (await res.json()) as { id: string; start_url: string; scope: string };
    expect(m.id).toBe(`${BASE}/admin/`);
    expect(m.start_url).toBe(`${BASE}/admin/`);
    expect(m.scope).toBe(`${BASE}/`);

    /* ⚠ The REGISTRATION is the claim, not its activation. `serviceWorker.ready`
       waits until the worker has precached every asset, and Firefox under load
       was still writing that cache when the test's 30 s ran out (0.50 run 3,
       on a disk with seconds of write latency). Where the worker was
       registered, and from which script, is known the moment it registers. */
    const reg = () =>
      page.evaluate(async () => {
        const r = await navigator.serviceWorker.getRegistration();
        const w = r && (r.active ?? r.waiting ?? r.installing);
        return r && w ? { scope: new URL(r.scope).pathname, script: new URL(w.scriptURL).pathname } : null;
      });
    await expect
      .poll(reg, { timeout: 20_000 })
      .toEqual({ scope: `${BASE}/admin/`, script: `${BASE}/admin/sw.js` });
  });
});
