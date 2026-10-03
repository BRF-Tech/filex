/**
 * 185-default-apps - which app opens a kind of file (0.50,
 * docs/APP-PLUGINS.md → Default apps), in a real browser and against a real
 * server:
 *
 *   1. the file menu offers every handler that is on for the kind, filex's
 *      own viewer included, and "Choose an app…"; picking filex's viewer with
 *      "Always use this app" keeps it on the ACCOUNT and the next opening
 *      follows it;
 *   2. the administrator switches filex's viewer off for the kind: it is no
 *      longer offered, the person's choice is not used (the app opens the
 *      file), and Settings → Preferences → Default apps says the choice is no longer
 *      available;
 *   3. the server holds the line: an interface switched off for the kind is
 *      refused when it saves (403 handler_off), whatever the explorer did.
 *
 * The app is built here: an interface-only `viewer` for one invented kind.
 */
import { test, expect, type APIRequestContext, type Page } from '@playwright/test';
import { mkdirSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import { loginAs } from '../helpers/auth';
import { dropStorageByName, newAuthedRequest, seedLocalStorage, storageRoot } from '../helpers/seed';
import { zipStored } from '../shots/fixtures.mjs';

const PREFS = '/api/me/prefs?surface=web';

const UI = zipStored([
  {
    name: 'index.html',
    data: Buffer.from('<!doctype html><html><head><meta charset="utf-8"></head><body><h1 id="title">da app</h1></body></html>'),
  },
]);

function manifest(name: string, ext: string) {
  return {
    manifest_version: 1,
    name,
    version: '1.0.0',
    label: { en: `Viewer ${name}` },
    permissions: ['files:read', 'files:write'],
    ui: { bundle: {} },
    views: [{ id: 'view', placement: 'viewer', ui: 'index.html', label: { en: `Viewer ${name}` }, applies: { ext: [ext] } }],
  };
}

async function install(api: APIRequestContext, m: Record<string, unknown>) {
  const files = {
    manifest: { name: 'filex-app.json', mimeType: 'application/json', buffer: Buffer.from(JSON.stringify(m)) },
    ui: { name: 'ui.zip', mimeType: 'application/zip', buffer: UI },
  };
  const dry = await api.post('/api/admin/app-plugins?dry_run=1', { multipart: { ...files, grant: JSON.stringify({ permissions: [] }) } });
  expect(dry.ok(), `dry run: ${dry.status()} ${await dry.text()}`).toBe(true);
  const review = (await dry.json()) as { permissions: Array<{ id: string }>; file_types?: Array<{ ext: string; capability: string }> };
  expect(review.file_types?.map((r) => `${r.capability} .${r.ext}`), 'the review lists the kind it opens').toEqual([`open .${(m.views as Array<{ applies: { ext: string[] } }>)[0].applies.ext[0]}`]);
  const inst = await api.post('/api/admin/app-plugins', {
    multipart: { ...files, grant: JSON.stringify({ permissions: review.permissions.map((p) => p.id) }) },
  });
  expect(inst.ok(), `install: ${inst.status()} ${await inst.text()}`).toBe(true);
}

async function removeApp(api: APIRequestContext, name: string) {
  const list = await api.get('/api/admin/app-plugins');
  if (!list.ok()) return;
  for (const p of ((await list.json()) as { plugins?: Array<{ id: number; name: string }> }).plugins ?? []) {
    if (p.name === name) await api.delete(`/api/admin/app-plugins/${p.id}`);
  }
}

test.describe.serial('Default apps - Open with, the administrator’s order, the person’s choice', () => {
  let api: APIRequestContext;
  let tag = '';
  let store = '';
  let app = '';
  let ext = '';
  let prefsBefore: unknown = {};

  test.beforeAll(async ({ playwright, baseURL }, info) => {
    tag = info.project.name.replace(/[^a-z]/g, '').slice(0, 8) || 'x';
    store = `e2e-da-${tag}-${Date.now()}`;
    app = `da-${tag}`;
    ext = `da${tag.slice(0, 3)}`;
    api = await newAuthedRequest(playwright, baseURL ?? '');
    const got = await api.get(PREFS);
    prefsBefore = got.ok() ? (await got.json()).prefs : {};
    await removeApp(api, app);
    await api.delete(`/api/admin/file-types/${ext}`);
    const mount = `/tmp/filex-${store}`;
    const root = storageRoot(mount);
    mkdirSync(root, { recursive: true });
    writeFileSync(join(root, `plan.${ext}`), 'a plan');
    await seedLocalStorage(api, store, mount);
    await install(api, manifest(app, ext));
  });

  test.afterAll(async () => {
    await api.delete(`/api/admin/file-types/${ext}`).catch(() => undefined);
    await removeApp(api, app);
    await dropStorageByName(api, store);
    await api.put(PREFS, { data: { prefs: prefsBefore } }).catch(() => undefined);
    await api.dispose();
  });

  async function openFolder(page: Page) {
    await page.addInitScript(() => {
      localStorage.setItem('filex.tourDone', '1');
      localStorage.setItem('filex.installPrompt.dismissed', '1');
    });
    await loginAs(page);
    await page.goto(`/admin/explore?storage=${encodeURIComponent(store)}`);
    const row = page.locator(`[data-fe-path="${store}://plan.${ext}"]`).first();
    await row.waitFor();
    return row;
  }

  async function menuOn(page: Page) {
    const row = page.locator(`[data-fe-path="${store}://plan.${ext}"]`).first();
    await row.getByText(`plan.${ext}`, { exact: true }).click({ button: 'right' });
  }

  test('the menu offers each handler and "Choose an app…"; "Always use" is kept on the account', async ({ page }) => {
    await openFolder(page);
    await menuOn(page);
    await expect(page.getByTestId(`ctx-open-with-app:${app}/view`)).toBeVisible();
    await expect(page.getByTestId('ctx-open-with-builtin')).toBeVisible();
    await page.getByTestId('ctx-open-with-choose').click();
    const dialog = page.getByTestId('open-with-dialog');
    await expect(dialog).toBeVisible();
    await expect(dialog).toContainText(`Which app should open plan.${ext}?`);
    await page.getByTestId('open-with-choice-builtin').click();
    await page.getByTestId('open-with-always').check();
    await page.getByTestId('open-with-open').click();
    await expect(page.locator('iframe[data-testid="app-frame"]'), 'filex’s own viewer, not the app').toHaveCount(0);

    await expect
      .poll(async () => JSON.parse(((await (await api.get(PREFS)).json()).prefs?.openWith as string) || '{}')[ext])
      .toBe('builtin');

    await page.keyboard.press('Escape');
    await page.locator(`[data-fe-path="${store}://plan.${ext}"]`).first().getByText(`plan.${ext}`, { exact: true }).dblclick();
    await page.waitForTimeout(1500);
    await expect(page.locator('iframe[data-testid="app-frame"]'), 'the next opening follows the choice').toHaveCount(0);
  });

  test('a handler the administrator switches off is not offered, and the choice is not used', async ({ page }) => {
    const put = await api.put(`/api/admin/file-types/${ext}`, { data: { open: { order: [], off: ['builtin'] } } });
    expect(put.ok(), `${put.status()} ${await put.text()}`).toBe(true);
    await openFolder(page);
    await menuOn(page);
    await expect(page.getByTestId('ctx-open-with-builtin')).toHaveCount(0);
    await expect(page.getByTestId('ctx-open-with-choose'), 'one way to open it: nothing to choose').toHaveCount(0);
    await page.keyboard.press('Escape');
    await page.locator(`[data-fe-path="${store}://plan.${ext}"]`).first().getByText(`plan.${ext}`, { exact: true }).dblclick();
    await expect(page.locator('iframe[data-testid="app-frame"]'), 'the app opens it: the choice is switched off').toHaveCount(1, { timeout: 20_000 });

    await page.goto('/admin/dashboard?settings=1');
    await expect(page.getByTestId('user-settings-dialog')).toBeVisible({ timeout: 15_000 });
    await page.getByTestId('user-settings-tab-preferences').click();
    await expect(page.getByTestId(`user-settings-app-gone-${ext}`)).toContainText('No longer available');
  });

  test('the server refuses an interface switched off for the kind', async () => {
    const put = await api.put(`/api/admin/file-types/${ext}`, { data: { open: { order: [], off: [`app:${app}/view`] } } });
    expect(put.ok(), `${put.status()} ${await put.text()}`).toBe(true);
    const save = await api.put(`/api/files/plugins/ui/${app}/view/save?path=${encodeURIComponent(`${store}://plan.${ext}`)}`, {
      data: 'overwritten',
      headers: { 'Content-Type': 'application/octet-stream' },
    });
    expect(save.status()).toBe(403);
    expect(await save.text()).toContain('handler_off');
  });
});
