/**
 * 275-office-engine-upload - the install wizard's Upload files for an app
 * that brings the office engine (0.57, docs/APP-PLUGINS.md → Install one;
 * docs/ONLYOFFICE.md → The Office editor app, "From its files"):
 *
 *   1. a manifest with an `engines.office` block and no engine.zip chosen is
 *      said at once - the file it needs, by name - and nothing is uploaded;
 *   2. with engine.zip in its own field the review says the derived
 *      `office-engine` grant, the install takes the engine, and Default apps
 *      lists the app's engine as a back end of the office engine.
 *
 * RED before the fix: the wizard had no engine field and sent the module,
 * the manifest, the interface and the signature only, so the review was
 * refused ("supply engine.zip") and an app like the Office editor 0.4.0
 * could not be installed from its files. 269-office-engine-app installs the
 * same kind of app over the API.
 */
import { test, expect, type APIRequestContext, type Page } from '@playwright/test';
import { createHash } from 'node:crypto';
import { APP_INSTALL_ALLOWANCE_MS, APP_REVIEW_ALLOWANCE_MS } from '../helpers/appPlugin';
import { openSignedIn } from '../helpers/auth';
import { newAuthedRequest } from '../helpers/seed';
import { zipStored } from '../shots/fixtures.mjs';

/** The smallest WebAssembly module: the magic and the version, nothing else. */
const EMPTY_WASM = Buffer.from([0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00]);

const ENGINE = zipStored([
  { name: 'x2t-server.wasm', data: EMPTY_WASM },
  { name: 'layout-host.wasm', data: EMPTY_WASM },
  { name: 'layout/worker.js', data: Buffer.from("print(JSON.stringify({ ready: true }));") },
]);

const UI = zipStored([
  { name: 'index.html', data: Buffer.from('<!doctype html><html><head><meta charset="utf-8"></head><body>editor</body></html>') },
  { name: 'editor/sdkjs/word/sdk-all.js', data: Buffer.from('var sdk;') },
  { name: 'editor/fonts/000', data: Buffer.from('FONT') },
]);

function manifest(name: string) {
  return {
    manifest_version: 1,
    name,
    version: '0.4.0',
    label: { en: 'Office engine upload' },
    permissions: ['files:read', 'files:write'],
    ui: { bundle: {} },
    views: [{ id: 'editor', placement: 'viewer', ui: 'index.html', label: { en: 'Office engine upload' }, applies: { ext: ['oeu'] } }],
    engines: { office: { sha256: createHash('sha256').update(ENGINE).digest('hex') } },
  };
}

async function removeApp(api: APIRequestContext, name: string) {
  const list = await api.get('/api/admin/app-plugins');
  if (!list.ok()) return;
  for (const p of ((await list.json()) as { plugins?: Array<{ id: number; name: string }> }).plugins ?? []) {
    if (p.name === name) await api.delete(`/api/admin/app-plugins/${p.id}`);
  }
}

/** The wizard on its Upload files tab. */
async function openUpload(page: Page) {
  await openSignedIn(page, '/admin/plugins?tab=apps');
  await expect(page.getByTestId('app-plugins')).toBeVisible({ timeout: 20_000 });
  await page.getByTestId('app-plugin-add').click();
  await expect(page.getByTestId('app-plugin-wizard')).toBeVisible();
  await page.getByTestId('app-plugin-source-file').click();
}

/** Every install request the page sends (the review and the install). */
function installRequests(page: Page): string[] {
  const seen: string[] = [];
  page.on('request', (r) => {
    if (r.method() === 'POST' && /\/api\/admin\/app-plugins(\?|$)/.test(r.url())) seen.push(r.url());
  });
  return seen;
}

test.describe('Upload files: an app that brings the office engine', () => {
  let api: APIRequestContext;
  let app = '';

  test.beforeAll(async ({ playwright, baseURL }, info) => {
    const tag = info.project.name.replace(/[^a-z]/g, '').slice(0, 8) || 'x';
    app = `oeu-${tag}`;
    api = await newAuthedRequest(playwright, baseURL ?? '');
    await removeApp(api, app);
  });

  test.afterAll(async () => {
    await removeApp(api, app);
    await api.delete('/api/admin/file-types/engines/office').catch(() => undefined);
    await api.dispose();
  });

  const files = () => ({
    manifest: { name: 'filex-app.json', mimeType: 'application/json', buffer: Buffer.from(JSON.stringify(manifest(app))) },
    ui: { name: 'ui.zip', mimeType: 'application/zip', buffer: UI },
    engine: { name: 'engine.zip', mimeType: 'application/zip', buffer: ENGINE },
  });

  test('a manifest that brings an engine and no engine.zip chosen is said at once, and nothing is uploaded', async ({ page }) => {
    const sent = installRequests(page);
    await openUpload(page);
    await page.getByTestId('app-plugin-manifest').setInputFiles(files().manifest);
    await page.getByTestId('app-plugin-ui').setInputFiles(files().ui);
    await page.getByTestId('app-plugin-review').click();
    const said = page.getByTestId('app-plugin-wizard-error');
    await expect(said).toContainText('engine.zip');
    await expect(said).toContainText('Office engine');
    await expect(page.getByTestId('app-plugin-permissions')).toHaveCount(0);
    expect(sent, 'no review was asked for').toEqual([]);
  });

  test('with engine.zip in its own field the review says the engine, and the install takes it', async ({ page }) => {
    test.info().setTimeout(test.info().timeout + APP_REVIEW_ALLOWANCE_MS + APP_INSTALL_ALLOWANCE_MS);
    const sent = installRequests(page);
    await openUpload(page);
    await page.getByTestId('app-plugin-manifest').setInputFiles(files().manifest);
    await page.getByTestId('app-plugin-ui').setInputFiles(files().ui);
    await page.getByTestId('app-plugin-engine').setInputFiles(files().engine);
    await page.getByTestId('app-plugin-review').click();

    const grant = page.getByTestId('perm-office-engine');
    await expect(grant, 'the engine is a grant of its own').toBeVisible({ timeout: APP_REVIEW_ALLOWANCE_MS });
    await expect(grant).toContainText('office engine');
    await page.getByLabel(/I understand|Anladım/).check();
    await page.getByTestId('app-plugin-install').click();
    await expect(page.getByTestId(`app-plugin-${app}`)).toBeVisible({ timeout: APP_INSTALL_ALLOWANCE_MS });
    expect(sent.length, 'one review and one install').toBe(2);

    const res = await api.get('/api/admin/file-types');
    expect(res.ok(), `file-types: ${res.status()}`).toBe(true);
    const body = (await res.json()) as { engines?: Array<{ engine: string; on: Array<{ id: string }> }> };
    const row = body.engines?.find((e) => e.engine === 'office');
    expect(row?.on.map((h) => h.id), 'the uploaded engine is a back end of the office engine').toContain(`app:${app}`);
  });
});
