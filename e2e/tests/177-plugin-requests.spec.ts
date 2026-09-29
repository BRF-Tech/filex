/**
 * 177-plugin-requests — an API key cannot install a plugin; it leaves a
 * request, and an administrator decides it on the Plugins page. Walked in a
 * real browser (owner's rule, 2026-09-28):
 *
 *   1. The key's install is refused (403 `session_required`, naming where to
 *      leave a request). Its request is left, answered `pending`, and shows on
 *      the Plugins page: which app, who asked through which key, and why.
 *      Review → the frozen SHA-256 and the permissions → "I understand" →
 *      Approve and install: the app is installed, the request leaves the list.
 *   2. A rejected request installs nothing and keeps the administrator's
 *      reason.
 *   3. A source that changed between the request and the approval: nothing is
 *      installed, and the page says the source changed (the request is closed
 *      as superseded) rather than reporting a failure.
 *
 * ⚠ The "source" is a tiny HTTP server on 127.0.0.1 inside this spec (plain
 * http is accepted for loopback only, wasmplugin/fetch.go), serving language
 * packs — an app with no module, so nothing here needs a wasm build.
 */
import { test, expect, type APIRequestContext, type Page } from '@playwright/test';
import { createServer, type Server } from 'node:http';
import { createHash } from 'node:crypto';
import type { AddressInfo } from 'node:net';
import { loginAs } from '../helpers/auth';
import { newAuthedRequest } from '../helpers/seed';

const PACKS = ['lang-e2e-req-a', 'lang-e2e-req-b', 'lang-e2e-req-c'];

const served = new Map<string, Buffer>();
let server: Server;
let origin = '';

function pack(name: string, version: string): Buffer {
  return Buffer.from(
    JSON.stringify({
      manifest_version: 1,
      name,
      version,
      label: { en: `Esperanto (${name})`, tr: `Esperanto (${name})` },
      languages: ['en', 'tr'],
      permissions: [],
      ui_locales: { eo: { 'common.cancel': 'Nuligi' } },
    }),
  );
}

async function removeByName(api: APIRequestContext, name: string) {
  const list = await api.get('/api/admin/app-plugins');
  if (!list.ok()) return;
  for (const p of (await list.json()).plugins ?? []) {
    if (p.name === name) await api.delete(`/api/admin/app-plugins/${p.id}`);
  }
}

async function openPlugins(page: Page) {
  await page.addInitScript(() => localStorage.setItem('filex.tourDone', '1'));
  await loginAs(page);
  await page.goto('/admin/plugins');
  await expect(page.getByTestId('plugin-requests')).toBeVisible();
}

async function openRowMenu(page: Page, id: number) {
  await page.getByTestId(`plugin-request-actions-${id}`).click();
}

test.describe.serial('Plugin requests — an API key asks, an administrator decides', () => {
  let session: APIRequestContext;
  let key: APIRequestContext;

  test.beforeAll(async ({ playwright, baseURL }) => {
    server = createServer((req, res) => {
      const body = served.get((req.url ?? '').split('?')[0]);
      if (!body) {
        res.writeHead(404).end();
        return;
      }
      res.writeHead(200, { 'Content-Type': 'application/json' }).end(body);
    });
    await new Promise<void>((ok) => server.listen(0, '127.0.0.1', ok));
    origin = `http://127.0.0.1:${(server.address() as AddressInfo).port}`;

    session = await newAuthedRequest(playwright, baseURL ?? '');
    for (const n of PACKS) await removeByName(session, n);
    const minted = await session.post('/api/admin/ai-tokens', {
      data: { label: 'e2e-agent', scopes: 'admin,read' },
    });
    expect(minted.status(), await minted.text()).toBe(201);
    const { token } = await minted.json();
    key = await playwright.request.newContext({ baseURL, extraHTTPHeaders: { Authorization: `Bearer ${token}` } });
  });

  test.afterAll(async () => {
    for (const n of PACKS) await removeByName(session, n);
    await key.dispose();
    await session.dispose();
    await new Promise<void>((ok) => server.close(() => ok()));
  });

  test('the key is refused; its request is approved in the panel and the app is installed', async ({ page }) => {
    const name = PACKS[0];
    served.set(`/${name}/filex-app.json`, pack(name, '1.0.0'));
    const manifestURL = `${origin}/${name}/filex-app.json`;

    const refused = await key.post('/api/admin/app-plugins', {
      data: { url: '', manifest_url: manifestURL, permissions: [] },
    });
    expect(refused.status()).toBe(403);
    const why = await refused.json();
    expect(why.error).toBe('session_required');
    expect(why.message).toContain('/api/admin/plugin-requests');

    const asked = await key.post('/api/admin/plugin-requests', {
      data: { kind: 'app', manifest_url: manifestURL, reason: 'The team reads Esperanto' },
    });
    expect(asked.status(), await asked.text()).toBe(201);
    const { request } = await asked.json();
    expect(request.status).toBe('pending');
    const sum = createHash('sha256').update(pack(name, '1.0.0')).digest('hex');
    expect(request.sha256).toBe(sum);

    await openPlugins(page);
    const row = page.getByTestId(`plugin-request-${request.id}`);
    await expect(row).toContainText(`Esperanto (${name})`);
    const panel = page.getByTestId('plugin-requests');
    await expect(panel).toContainText('The team reads Esperanto');
    await expect(panel).toContainText('API key “e2e-agent”');

    await openRowMenu(page, request.id);
    await page.getByTestId(`plugin-request-actions-${request.id}-review`).click();
    const review = page.getByTestId('plugin-request-review');
    await expect(review).toBeVisible();
    await expect(review.getByTestId('plugin-request-sha256')).toHaveText(sum);
    const approve = review.getByTestId('plugin-request-approve');
    await expect(approve, 'nothing is approved without "I understand"').toBeDisabled();
    await review.locator('input[name="plugin-request-understand"]').check();
    const answered = page.waitForResponse((r) => r.url().endsWith(`/api/admin/plugin-requests/${request.id}/approve`));
    await approve.click();
    expect((await answered).status()).toBe(200);
    await expect(review).toBeHidden();
    await expect(row, 'an approved request leaves the waiting list').toBeHidden();

    await page.getByTestId('plugins-tab-apps').click();
    await expect(page.getByTestId(`app-plugin-${name}`)).toBeVisible();
    const listed = await (await session.get('/api/admin/app-plugins')).json();
    const installed = listed.plugins.find((p: { name: string }) => p.name === name);
    expect(installed.sha256, 'exactly the bytes the request froze').toBe(sum);
  });

  test('a rejected request installs nothing and keeps the reason', async ({ page }) => {
    const name = PACKS[1];
    served.set(`/${name}/filex-app.json`, pack(name, '1.0.0'));
    const asked = await key.post('/api/admin/plugin-requests', {
      data: { kind: 'app', manifest_url: `${origin}/${name}/filex-app.json`, reason: 'Nice to have' },
    });
    expect(asked.status(), await asked.text()).toBe(201);
    const { request } = await asked.json();

    await openPlugins(page);
    await openRowMenu(page, request.id);
    await page.getByTestId(`plugin-request-actions-${request.id}-reject`).click();
    const review = page.getByTestId('plugin-request-review');
    await review.locator('textarea[name="plugin-request-reject-reason"]').fill('Not needed now');
    const answered = page.waitForResponse((r) => r.url().endsWith(`/api/admin/plugin-requests/${request.id}/reject`));
    await review.getByTestId('plugin-request-reject').click();
    expect((await answered).status()).toBe(200);
    await expect(page.getByTestId(`plugin-request-${request.id}`)).toBeHidden();

    const after = await (await key.get(`/api/admin/plugin-requests/${request.id}`)).json();
    expect(after.request.status).toBe('rejected');
    expect(after.request.decision_note).toBe('Not needed now');
    const listed = await (await session.get('/api/admin/app-plugins')).json();
    expect(listed.plugins.some((p: { name: string }) => p.name === name)).toBe(false);

    // The history shows it, with its state.
    await page.getByTestId('plugin-requests-history').click();
    await expect(page.getByTestId(`plugin-request-status-${request.id}`)).toHaveText('Rejected');
  });

  test('a source that changed is said as such, and nothing is installed', async ({ page }) => {
    const name = PACKS[2];
    served.set(`/${name}/filex-app.json`, pack(name, '1.0.0'));
    const asked = await key.post('/api/admin/plugin-requests', {
      data: { kind: 'app', manifest_url: `${origin}/${name}/filex-app.json`, reason: 'For the demo' },
    });
    expect(asked.status(), await asked.text()).toBe(201);
    const { request } = await asked.json();
    served.set(`/${name}/filex-app.json`, pack(name, '2.0.0'));

    await openPlugins(page);
    await openRowMenu(page, request.id);
    await page.getByTestId(`plugin-request-actions-${request.id}-review`).click();
    const review = page.getByTestId('plugin-request-review');
    await review.locator('input[name="plugin-request-understand"]').check();
    const answered = page.waitForResponse((r) => r.url().endsWith(`/api/admin/plugin-requests/${request.id}/approve`));
    await review.getByTestId('plugin-request-approve').click();
    expect((await answered).status()).toBe(409);
    await expect(page.getByTestId('toast-layer')).toContainText('has changed since the request was made');

    const after = await (await key.get(`/api/admin/plugin-requests/${request.id}`)).json();
    expect(after.request.status).toBe('superseded');
    const listed = await (await session.get('/api/admin/app-plugins')).json();
    expect(listed.plugins.some((p: { name: string }) => p.name === name)).toBe(false);
  });
});
