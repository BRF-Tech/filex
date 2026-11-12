/**
 * 315 - setting an app up (0.60, docs/APP-PLUGINS.md → Setting an app up),
 * in a Turkish panel, in a real browser.
 *
 * Three faults found while the Short links app was set up on a live 0.60.0
 * install, each pinned here where a person meets it:
 *
 *  1. The Apps table's State cell of an app waiting for its setup read
 *     "Kurulum bekliyor waiting to be set up": the label in the reader's
 *     language, then the server's English reason under it, drawn as an
 *     error. The cell says the state, in Turkish, and nothing else.
 *  2. In the wizard's Addresses step a row's Actions menu opened and could
 *     not be clicked: the "Add address" box under it took the click
 *     (Playwright: "subtree intercepts pointer events"). The wizard is a
 *     native <dialog> opened with showModal(), the browser's top layer, and
 *     the menu teleported to <body>, under it. Measured the way 128 measures
 *     the overlay case: what a click at the entry's centre reaches. Escape
 *     closes the menu and leaves the wizard open.
 *  3. Switching the kind of address carried the own-address prefix ("l",
 *     the route's default) into the optional prefix of a host of its own:
 *     l.example.com became https://l.example.com/l. A host of its own starts
 *     with no prefix; "l" is only the placeholder.
 *
 * The app is the `echo` fixture (scripts/build-wasm-fixture.sh; its module
 * answers `route` and `setup`, backend/internal/wasmplugin/testdata/echo)
 * with a manifest widened by one route, the way the Go suite's
 * routesManifest widens it. Installed and removed through the API: the
 * screens under test are the list and the wizard, not the install.
 */
import { readFileSync } from 'node:fs';
import { test, expect, type APIRequestContext, type Locator, type Page } from '@playwright/test';
import { dismissInstallBanner } from '../helpers/auth';
import { newAuthedRequest } from '../helpers/seed';
import { guardFixture } from '../helpers/appPlugin';
import { echoFixture } from '../helpers/echoFixture';

const ECHO = echoFixture();

const RUN = Date.now();
const TR_ADMIN = { email: `kurulum-${RUN}@example.com`, password: 'Kurulum-sihirbazi-2026' };

let api: APIRequestContext;
let adminId = 0;

async function removeEcho() {
  const list = await api.get('/api/admin/app-plugins');
  if (!list.ok()) return;
  for (const p of (await list.json()).plugins ?? []) {
    if (p.name !== 'echo') continue;
    // With the name as confirm: an app that keeps data answers 409 without it
    // and would stay for the next spec (filex lesson #1879).
    const res = await api.delete(`/api/admin/app-plugins/${p.id}?confirm=${encodeURIComponent(p.name)}`);
    expect([200, 204, 404], `removing echo: ${res.status()} ${await res.text()}`).toContain(res.status());
  }
}

/** echo, with one public route: an app that waits for its setup. */
function routedManifest(): Record<string, unknown> {
  const m = JSON.parse(readFileSync(ECHO.manifestPath, 'utf8')) as Record<string, unknown>;
  m.permissions = [...((m.permissions as string[]) ?? []), 'public_routes'];
  m.routes = [{ id: 'go', label: { en: 'Short links', tr: 'Kısa bağlantılar' }, default_prefix: 'l' }];
  return m;
}

async function installRouted() {
  const manifest = routedManifest();
  const form = {
    grant: JSON.stringify({ permissions: manifest.permissions }),
    wasm: { name: 'plugin.wasm', mimeType: 'application/wasm', buffer: readFileSync(ECHO.wasm) },
    manifest: { name: 'filex-app.json', mimeType: 'application/json', buffer: Buffer.from(JSON.stringify(manifest)) },
  };
  const res = await api.post('/api/admin/app-plugins', { multipart: form, timeout: 120_000 });
  expect(res.ok(), `install: ${res.status()} ${await res.text()}`).toBe(true);
}

/** What a click at (x, y) reaches, said well enough to read in a failure. */
async function whatIsAt(page: Page, x: number, y: number): Promise<string> {
  return page.evaluate(
    ([px, py]) => {
      const hit = document.elementFromPoint(px, py) as HTMLElement | null;
      if (!hit) return 'nothing';
      if (hit.closest('.fe-ctx')) return 'menu';
      if (hit.closest('.fe-ctx-backdrop')) return 'menu-backdrop';
      const id = hit.closest('[data-testid]')?.getAttribute('data-testid');
      return id ? `[data-testid=${id}]` : hit.className || hit.tagName.toLowerCase();
    },
    [x, y],
  );
}

async function centreOf(el: Locator): Promise<[number, number]> {
  const b = await el.boundingBox();
  expect(b, 'no box').not.toBeNull();
  return [b!.x + b!.width / 2, b!.y + b!.height / 2];
}

/**
 * Admin → Apps, signed in as the Turkish administrator: the echo row.
 *
 * ⚠ Signed in through the page's own cookie jar, and the Apps page is the
 * tab's first page: leaving a filex page that is still asking the server for
 * things is where Playwright's WebKit drops its network process (filex
 * lesson #1541, the 179 pattern).
 */
async function openApps(page: Page): Promise<Locator> {
  await dismissInstallBanner(page);
  await page.addInitScript(() => localStorage.setItem('filex.tourDone', '1'));
  const login = await page.request.post('/api/auth/login', { data: TR_ADMIN });
  expect(login.ok(), `sign-in: ${login.status()}`).toBe(true);
  await page.goto('/admin/plugins');
  await page.getByTestId('plugins-tab-apps').click();
  const row = page.locator('.fe-list__row').filter({ has: page.getByTestId('app-plugin-actions-echo') });
  await expect(row).toBeVisible({ timeout: 15_000 });
  return row;
}

/** The setup wizard, from the row's own verb, on its Addresses step. */
async function openWizard(page: Page): Promise<Locator> {
  await openApps(page);
  await page.getByTestId('app-plugin-actions-echo').click();
  await page.locator('.fe-ctx [data-testid="app-plugin-actions-echo-setup"]').click();
  const wizard = page.getByTestId('app-setup-wizard');
  await expect(wizard).toBeVisible();
  await expect(page.getByTestId('app-setup-addresses')).toBeVisible();
  return wizard;
}

test.describe('setting an app up, in Turkish', () => {
  guardFixture(ECHO, test.skip);

  test.beforeAll(async ({ playwright, baseURL }) => {
    api = await newAuthedRequest(playwright, baseURL ?? '');
    await removeEcho();
    const made = await api.post('/api/admin/users', {
      data: { email: TR_ADMIN.email, password: TR_ADMIN.password, role: 'admin', locale: 'tr', display_name: 'Kurulum' },
    });
    expect(made.ok(), await made.text()).toBe(true);
    const body = (await made.json()) as { id?: number; user?: { id?: number } };
    adminId = Number(body.id ?? body.user?.id ?? 0);
    await installRouted();
  });

  test.afterAll(async () => {
    await removeEcho();
    if (adminId) await api.delete(`/api/admin/users/${adminId}`).catch(() => undefined);
    await api.dispose();
  });

  test('1. the Apps table says the state in the language of the reader, and nothing else', async ({ page }) => {
    const row = await openApps(page);
    const state = row.locator('[data-col="state"]');
    // ⚠ On 0.60.0: "Kurulum bekliyorwaiting to be set up".
    await expect(state).toHaveText('Kurulum bekliyor');
    await expect(row.getByTestId('app-plugin-error')).toHaveCount(0);
    expect(await state.locator('[title]').evaluateAll((els) => els.map((e) => e.getAttribute('title')).filter(Boolean))).toEqual([]);
  });

  test('2. the Actions menu of an address row takes its clicks, and Escape closes only the menu', async ({ page }) => {
    const wizard = await openWizard(page);
    await expect(page.locator('input[name="app-setup-prefix-go"]')).toHaveValue('l');
    await page.getByTestId('app-setup-add-button-go').click();
    const base = page.locator('button.tbl-rowactions__btn[data-testid^="app-setup-base-actions-"]').first();
    await expect(base).toBeVisible({ timeout: 10_000 });

    await base.click();
    const menu = page.locator('.fe-ctx').last();
    await expect(menu).toBeVisible();
    const remove = menu.locator('[data-testid$="-remove"]');
    await expect(remove).toHaveText(/Kaldır/);
    const [rx, ry] = await centreOf(remove);
    // ⚠⚠ THE assertion. On 0.60.0 this said [data-testid=app-setup-add-go].
    expect(await whatIsAt(page, rx, ry), 'a click at "Kaldır" does not reach the menu').toBe('menu');
    for (const entry of await menu.locator('[role="menuitem"]').all()) {
      const [x, y] = await centreOf(entry);
      expect(await whatIsAt(page, x, y), `entry "${(await entry.textContent())?.trim()}" is covered`).toBe('menu');
    }
    // The keyboard is the menu's: Escape closes it, not the wizard.
    expect(await page.evaluate(() => !!document.activeElement?.closest('.fe-ctx')), 'the menu took no focus').toBe(true);
    await page.keyboard.press('Escape');
    await expect(menu).toBeHidden();
    await expect(wizard, 'Escape closed the wizard with the menu').toBeVisible();

    await base.click();
    await page.locator('.fe-ctx').last().locator('[data-testid$="-remove"]').click();
    await expect(page.getByTestId('app-setup-route-empty-go')).toBeVisible();
  });

  test('3. a host of its own starts with no prefix, and each kind keeps its own', async ({ page }) => {
    const wizard = await openWizard(page);
    const prefix = page.locator('input[name="app-setup-prefix-go"]');
    await expect(prefix).toHaveValue('l');
    await page.getByTestId('app-setup-kind-go-host').click();
    // ⚠ On 0.60.0 the own-address "l" came along, and l.example.com became https://l.example.com/l.
    await expect(prefix, 'the own-address prefix came along').toHaveValue('');
    await expect(prefix).toHaveAttribute('placeholder', 'l');
    await page.locator('input[name="app-setup-host-go"]').fill('l.example.com');
    const preview = page.getByTestId('app-setup-preview-go');
    await expect(preview).toContainText('https://l.example.com/kod');
    await expect(preview).not.toContainText('l.example.com/l/');
    await page.getByTestId('app-setup-add-button-go').click();
    const hostRow = page.locator('span.font-mono[data-testid^="app-setup-base-"]').filter({ hasText: 'l.example.com' });
    await expect(hostRow).toHaveText(/^https:\/\/l\.example\.com\/?$/, { timeout: 10_000 });

    // The other kind kept its own value.
    await page.getByTestId('app-setup-kind-go-own').click();
    await expect(prefix).toHaveValue('l');

    await page.getByTestId('app-setup-later').click();
    await expect(wizard).toBeHidden();
  });
});
