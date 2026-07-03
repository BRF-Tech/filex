/**
 * Capability gating: viewers should refuse to launch a backend that isn't
 * reachable, and surface a clear "this service is offline" pane instead of
 * an opaque 503 fallback or a tab full of stack-traced fetch errors.
 *
 * Regression scope (commit db1a54c — "feat(ui): capability-gated viewers
 * — don't open broken backends"):
 *
 *   - openNode now refuses to spawn /files/edit in a new tab for an
 *     office doc when the OnlyOffice capability probe reports offline.
 *     It falls back to the in-page PreviewModal so the user gets a
 *     single round-trip and a single visible explanation.
 *
 *   - PreviewModal short-circuits OnlyOffice mount when onlyOfficeBase
 *     is null (FileExplorer wipes it on probe failure). No fetch fires;
 *     the user sees 'OnlyOffice yapılandırması yok' + İndir + Kapat.
 *
 *   - DrawioViewer stops silently falling back to the public
 *     embed.diagrams.net when drawioUrl=null — operator-disabled state
 *     now produces a "Drawio yapılandırılmamış" pane.
 *
 * These tests need the demo deployment env (FILEX_ONLYOFFICE_URL unset;
 * FILEX_DRAWIO_URL=https://embed.diagrams.net). If the env enables
 * OnlyOffice (i.e. external.onlyoffice.state='ok'), the UI gating
 * branches are skipped — that's the happy path covered elsewhere.
 */
import { test, expect, type APIRequestContext } from '@playwright/test';
import { ADMIN_EMAIL, ADMIN_PASSWORD, loginAs } from '../helpers/auth';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import fs from 'node:fs';

const __dirname = path.dirname(fileURLToPath(import.meta.url));

const STORAGE_NAME = `caps-gate-${Date.now()}`;
let storageId: number;

async function adminBearer(request: APIRequestContext): Promise<string> {
  const login = await request.post('/api/auth/login', {
    data: { email: ADMIN_EMAIL, password: ADMIN_PASSWORD },
  });
  if (!login.ok()) {
    throw new Error(`bearer login failed: ${login.status()} ${await login.text()}`);
  }
  const { token } = await login.json();
  return token;
}

test.describe('Capability gating', () => {
  test.beforeAll(async ({ playwright, baseURL }) => {
    const ctx = await playwright.request.newContext({ baseURL });
    const token = await adminBearer(ctx);
    const authed = await playwright.request.newContext({
      baseURL,
      extraHTTPHeaders: { Authorization: `Bearer ${token}` },
    });

    // Seed a local-driver storage rooted at the per-suite tmp dir.
    const tmpRoot = path.join(__dirname, '..', '.tmp', STORAGE_NAME);
    fs.mkdirSync(tmpRoot, { recursive: true });
    const docxFixture = path.join(__dirname, '..', 'fixtures', 'file-types', 'demo.md');
    // We don't have a real .docx fixture, but the gating decision is based
    // on the FileNode.extension field, not the file's mime — so a renamed
    // copy of demo.md as `gating.docx` is enough to exercise openNode's
    // office-extension branch. The PreviewModal won't actually try to
    // render it because onlyOfficeBase is null.
    if (fs.existsSync(docxFixture)) {
      fs.copyFileSync(docxFixture, path.join(tmpRoot, 'gating.docx'));
    } else {
      fs.writeFileSync(path.join(tmpRoot, 'gating.docx'), 'stub docx content');
    }

    const create = await authed.post('/api/admin/storages/', {
      data: {
        name: STORAGE_NAME,
        driver: 'local',
        config: { root: tmpRoot },
      },
    });
    if (!create.ok()) {
      throw new Error(`storage create failed: ${create.status()} ${await create.text()}`);
    }
    const created = await create.json();
    storageId = created.id;

    await authed.patch(`/api/admin/storages/${storageId}`, {
      data: { enabled: true },
    });

    // Force one index pass so the .docx row is in DB before the UI loads.
    await authed.get(
      `/api/files/manager?action=index&path=${encodeURIComponent(STORAGE_NAME + '://')}`,
    );

    await ctx.dispose();
    await authed.dispose();
  });

  test.afterAll(async ({ playwright, baseURL }) => {
    if (!storageId) return;
    const ctx = await playwright.request.newContext({ baseURL });
    const token = await adminBearer(ctx);
    const authed = await playwright.request.newContext({
      baseURL,
      extraHTTPHeaders: { Authorization: `Bearer ${token}` },
    });
    await authed.delete(`/api/admin/storages/${storageId}`);
    await ctx.dispose();
    await authed.dispose();
  });

  test('capabilities endpoint exposes the external service shape', async ({ request }) => {
    // Tested logged-out — /api/capabilities is public so embedders can
    // probe the server before deciding whether to spin up the iframe.
    const res = await request.get('/api/capabilities');
    expect(res.ok()).toBeTruthy();
    const caps = await res.json();

    expect(caps).toHaveProperty('external');
    expect(caps.external).toHaveProperty('onlyoffice');
    expect(caps.external).toHaveProperty('drawio');
    expect(caps.external).toHaveProperty('mermaid');

    for (const svc of ['onlyoffice', 'drawio', 'mermaid'] as const) {
      const s = caps.external[svc];
      expect(typeof s.enabled).toBe('boolean');
      expect(typeof s.state).toBe('string');
      expect(['ok', 'error', 'disabled', 'unknown']).toContain(s.state);
    }
  });

  test('office double-click stays in the modal when OnlyOffice is offline', async ({ page, request }) => {
    const caps = await (await request.get('/api/capabilities')).json();
    const onlyofficeUsable =
      caps?.external?.onlyoffice?.enabled === true &&
      caps?.external?.onlyoffice?.state === 'ok';

    test.skip(
      onlyofficeUsable,
      'OnlyOffice is configured in this env — gating branch only fires when the probe reports offline',
    );

    await loginAs(page);

    // Watch for unexpected /onlyoffice/config network calls. The whole
    // point of the gate is to skip the round-trip when we know it would
    // 503 — so we assert by remembering whether one fired during the
    // double-click flow.
    let onlyofficeFetched = false;
    page.on('request', (req) => {
      if (req.url().includes('/api/files/onlyoffice/config')) {
        onlyofficeFetched = true;
      }
    });

    // Watch for new tabs — openNode would normally spawn one for the
    // standalone /files/edit route. Capability gating routes us into
    // the in-page modal instead, so no popup must fire.
    let popupOpened = false;
    page.context().on('page', () => {
      popupOpened = true;
    });

    await page.goto('/admin/explore');
    await page.getByRole('row', { name: new RegExp(`^${STORAGE_NAME}\\b`) }).dblclick();
    await page.getByRole('row', { name: /gating\.docx/ }).dblclick();

    // The in-page PreviewModal shows the operator-friendly Turkish
    // string declared in PreviewModal.vue:598.
    await expect(page.getByText('OnlyOffice yapılandırması yok')).toBeVisible({
      timeout: 5_000,
    });

    expect(onlyofficeFetched).toBe(false);
    expect(popupOpened).toBe(false);
  });

  test('logged-out probe still surfaces drawio + mermaid state', async ({ request }) => {
    // Documentation contract: embedders rely on these flags to render
    // their UI without first logging in to filex. Regressing this would
    // force every consumer to add auth headers to their capability
    // probe, which is exactly what the public endpoint is meant to
    // avoid.
    const res = await request.get('/api/capabilities');
    const caps = await res.json();
    expect(caps.external.drawio).toMatchObject({
      enabled: expect.any(Boolean),
      state: expect.stringMatching(/^(ok|error|disabled|unknown)$/),
    });
    expect(caps.external.mermaid).toMatchObject({
      enabled: expect.any(Boolean),
      state: expect.stringMatching(/^(ok|error|disabled|unknown)$/),
    });
  });
});
