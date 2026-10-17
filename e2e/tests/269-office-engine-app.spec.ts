/**
 * 269-office-engine-app - an app that brings the office engine (0.57,
 * docs/APP-PLUGINS-API.md → An app's office engine; docs/ONLYOFFICE.md → The
 * office engine without a Document Server), against the real server binary:
 *
 *   1. the install review says what the engine is (the derived
 *      `office-engine` grant, in a sentence), and the install takes the
 *      `engine` part beside the interface bundle;
 *   2. Default apps lists the app's engine as a back end of the office
 *      engine, first by default;
 *   3. filex prepares it in a process of its own (`filex office-engine`):
 *      this engine is two empty WebAssembly modules, which compile but have
 *      no WASI entry, so preparing it fails - and the screen says so, with
 *      why, instead of leaving the administrator to guess.
 *
 * The engine is built here: what x2t and the layout really are is the
 * integration test's (backend internal/officeengine real_test.go).
 */
import { test, expect, type APIRequestContext } from '@playwright/test';
import { createHash } from 'node:crypto';
import { loginAs } from '../helpers/auth';
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
    label: { en: 'Office engine test' },
    permissions: ['files:read', 'files:write'],
    ui: { bundle: {} },
    views: [{ id: 'editor', placement: 'viewer', ui: 'index.html', label: { en: 'Office engine test' }, applies: { ext: ['oex'] } }],
    engines: { office: { sha256: createHash('sha256').update(ENGINE).digest('hex') } },
  };
}

interface EngineRow {
  engine: string;
  on: Array<{ id: string }>;
  off: Array<{ id: string }>;
  states: Record<string, { state: string; error?: string }>;
}

async function engineRow(api: APIRequestContext): Promise<EngineRow> {
  const res = await api.get('/api/admin/file-types');
  expect(res.ok(), `file-types: ${res.status()}`).toBe(true);
  const body = (await res.json()) as { engines?: EngineRow[] };
  const row = body.engines?.find((e) => e.engine === 'office');
  expect(row, 'the office engine is listed').toBeTruthy();
  return row as EngineRow;
}

async function removeApp(api: APIRequestContext, name: string) {
  const list = await api.get('/api/admin/app-plugins');
  if (!list.ok()) return;
  for (const p of ((await list.json()) as { plugins?: Array<{ id: number; name: string }> }).plugins ?? []) {
    if (p.name === name) await api.delete(`/api/admin/app-plugins/${p.id}`);
  }
}

test.describe.serial('An app that brings the office engine', () => {
  let api: APIRequestContext;
  let app = '';

  test.beforeAll(async ({ playwright, baseURL }, info) => {
    const tag = info.project.name.replace(/[^a-z]/g, '').slice(0, 8) || 'x';
    app = `oe-${tag}`;
    api = await newAuthedRequest(playwright, baseURL ?? '');
    await removeApp(api, app);
    await api.delete('/api/admin/file-types/engines/office');
  });

  test.afterAll(async () => {
    await removeApp(api, app);
    await api.delete('/api/admin/file-types/engines/office').catch(() => undefined);
    await api.dispose();
  });

  test('the review says it, the install takes the engine, Default apps lists it', async () => {
    const files = {
      manifest: { name: 'filex-app.json', mimeType: 'application/json', buffer: Buffer.from(JSON.stringify(manifest(app))) },
      ui: { name: 'ui.zip', mimeType: 'application/zip', buffer: UI },
      engine: { name: 'engine.zip', mimeType: 'application/zip', buffer: ENGINE },
    };
    const dry = await api.post('/api/admin/app-plugins?dry_run=1', { multipart: { ...files, grant: JSON.stringify({ permissions: [] }) } });
    expect(dry.ok(), `dry run: ${dry.status()} ${await dry.text()}`).toBe(true);
    const review = (await dry.json()) as { permissions: Array<{ id: string; label: string }> };
    const grant = review.permissions.find((p) => p.id === 'office-engine');
    expect(grant, 'the engine is a grant of its own').toBeTruthy();
    expect(grant?.label).toContain('office engine');

    const inst = await api.post('/api/admin/app-plugins', {
      multipart: { ...files, grant: JSON.stringify({ permissions: review.permissions.map((p) => p.id) }) },
    });
    expect(inst.ok(), `install: ${inst.status()} ${await inst.text()}`).toBe(true);

    const row = await engineRow(api);
    expect(row.on[0]?.id, 'the app’s engine goes first by default').toBe(`app:${app}`);
  });

  test('preparing an engine that cannot run fails, and the screen says why', async ({ page }) => {
    await expect
      .poll(async () => (await engineRow(api)).states[`app:${app}`]?.state, { timeout: 90_000, message: 'prepared in the background' })
      .toBe('failed');
    const row = await engineRow(api);
    expect(row.states[`app:${app}`]?.error ?? '').toContain('_start');

    await page.addInitScript(() => {
      localStorage.setItem('filex.tourDone', '1');
      localStorage.setItem('filex.installPrompt.dismissed', '1');
    });
    await loginAs(page);
    await page.goto('/admin/plugins?tab=defaults');
    await expect(page.getByTestId('default-apps-engine')).toBeVisible({ timeout: 20_000 });
    await expect(page.getByTestId(`default-apps-engine-row-app:${app}`)).toBeVisible();
    await expect(page.getByTestId(`default-apps-engine-error-app:${app}`)).toContainText('_start');
  });
});
