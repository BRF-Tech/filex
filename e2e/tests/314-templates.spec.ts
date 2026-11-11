/**
 * 314 - mail and page templates (docs/TEMPLATES.md), in a real browser
 * against the binary:
 *
 *  1. the editor: a text slot written in Admin → Settings → Templates, with a
 *     script and an event handler in it - the preview says what the sanitizer
 *     took out, the save marks the row, the server answers the text back;
 *  2. the public link page: the slot is there, its script is not, and the
 *     page runs under a nonce Content-Security-Policy;
 *  3. a template that never ends is refused at save, with the engine's words;
 *  4. an edited "not available" page is the one a dead link shows.
 *
 * Every template this spec writes is reset at the end: templates are the
 * install's, and the next spec opens links too.
 */
import { test, expect, type APIRequestContext, type Page } from '@playwright/test';
import { writeFileSync } from 'node:fs';
import path from 'node:path';
import { loginAs } from '../helpers/auth';
import { dropStorageByName, newAuthedRequest, seedLocalStorage, storageRoot } from '../helpers/seed';

const STORAGE = 'e2e-templates';
const MOUNT = '/tmp/filex-e2e-templates';
const FILE = 'price-list.pdf';
const WRITTEN = ['slot.share.intro', 'page.error'];

/**
 * Replace what the template editor holds, as a person would: filex's code
 * editor is Monaco (core CodeEditor) where the build has it - click, select
 * all, type - and a textarea where it does not.
 */
async function writeInEditor(page: Page, text: string) {
  const box = page.getByTestId('template-source');
  await expect(box).toHaveAttribute('data-editor', /monaco|textarea/, { timeout: 30_000 });
  if ((await box.getAttribute('data-editor')) === 'textarea') {
    await box.locator('textarea').fill(text);
    return;
  }
  await box.locator('.view-lines').click();
  // Monaco reads its keymap from the user agent, and Playwright's WebKit
  // says Macintosh: there select-all is Meta+A, and Control+A only goes to
  // the line's start (0.60.0's gate: the "fixed" text landed beside the old).
  const webkit = page.context().browser()?.browserType().name() === 'webkit';
  await page.keyboard.press(webkit ? 'Meta+A' : 'Control+A');
  await page.keyboard.press('Delete');
  await expect(box.locator('.view-line')).toHaveCount(1);
  await expect
    .poll(async () => ((await box.locator('.view-lines').textContent()) ?? '').replace(/\u00a0/g, '').trim(), {
      message: 'the editor is empty before it is written',
    })
    .toBe('');
  // One line at a time: Firefox handed Monaco a multi-line insertText twice
  // over (0.60.0's gate: one/two/one/two/nmae, the refused line 5, not 3).
  const lines = text.split('\n');
  for (const [i, line] of lines.entries()) {
    if (i > 0) await page.keyboard.press('Enter');
    if (line) await page.keyboard.insertText(line);
  }
  await expect(box.locator('.view-line')).toHaveCount(lines.length);
}

async function put(api: APIRequestContext, key: string, body: string) {
  const cur = await api.get(`/api/admin/templates/${key}`);
  expect(cur.ok(), await cur.text()).toBeTruthy();
  const { template } = (await cur.json()) as { template: { source: string; revision: number } };
  return api.put(`/api/admin/templates/${key}`, {
    data: { subject: '', body, text: '', revision: template.source === 'default' ? 0 : template.revision, lang: 'en' },
  });
}

test.describe('Templates', () => {
  let api: APIRequestContext;
  let token = '';

  test.beforeAll(async ({ playwright, baseURL, request }) => {
    api = await newAuthedRequest(playwright, baseURL ?? '');
    await dropStorageByName(request, STORAGE);
    await seedLocalStorage(request, STORAGE, MOUNT);
    writeFileSync(path.join(storageRoot(MOUNT), FILE), '%PDF-1.4\n% e2e\n');
    const made = await api.post('/api/files/share', { data: { path: `${STORAGE}://${FILE}`, kind: 'file' } });
    expect(made.ok(), `share ${made.status()} ${await made.text()}`).toBeTruthy();
    const body = (await made.json()) as { token?: string; share?: { token: string } };
    token = body.token ?? body.share?.token ?? '';
    expect(token).not.toBe('');
  });

  test.afterAll(async ({ request }) => {
    for (const key of WRITTEN) await api.delete(`/api/admin/templates/${key}`);
    await dropStorageByName(request, STORAGE);
    await api.dispose();
  });

  test('a slot written in the editor reaches the link page, its script does not', async ({ page }) => {
    await loginAs(page);
    await page.goto('/admin/templates');
    await expect(page.getByTestId('templates-list')).toBeVisible({ timeout: 15_000 });
    await page.getByTestId('template-item-slot.share.intro').click();
    await expect(page.getByTestId('template-editor')).toHaveAttribute('data-key', 'slot.share.intro');

    await writeInEditor(page, '<p class="e2e-intro">Prices for {{ item.name }}.</p><script>window.__slot = 1</script><img src="x" onerror="window.__slot = 2">');
    // The preview is the server's, and it says what it took out.
    await expect(page.getByTestId('template-removed')).toContainText('script');
    await page.getByTestId('template-save').click();
    await expect(page.getByTestId('template-item-slot.share.intro')).toContainText('This server');
    const saved = await api.get('/api/admin/templates/slot.share.intro');
    expect(((await saved.json()) as { current: { body: string } }).current.body).toContain('e2e-intro');

    // ── the link page, as a stranger opens it ─────────────────────────────
    const visitor = await page.context().browser()!.newContext();
    const pub = await visitor.newPage();
    const doc = await pub.goto(`/s/${token}`);
    expect(doc?.status()).toBe(200);
    const intro = pub.getByTestId('public-page-intro');
    await expect(intro).toContainText(`Prices for ${FILE}.`);
    await expect(intro.locator('script')).toHaveCount(0);
    await expect(intro.locator('[onerror]')).toHaveCount(0);
    expect(await pub.evaluate(() => (window as unknown as { __slot?: number }).__slot)).toBeUndefined();
    // The document's policy: only filex's own scripts, by this response's nonce.
    const csp = await pub.locator('meta[http-equiv="Content-Security-Policy"]').getAttribute('content');
    expect(csp).toMatch(/script-src 'nonce-[^']+' 'strict-dynamic'/);
    await visitor.close();
  });

  test('the editor marks the line the engine refused', async ({ page }) => {
    await loginAs(page);
    await page.goto('/admin/templates');
    await expect(page.getByTestId('templates-list')).toBeVisible({ timeout: 15_000 });
    await page.getByTestId('template-item-slot.share.intro').click();
    await writeInEditor(page, '<p>one</p>\n<p>two</p>\n<p>{{ item.nmae }}</p>');
    // The preview's refusal names the line; the editor marks that one.
    await expect(page.getByTestId('template-error')).toContainText('line 3');
    await expect(page.getByTestId('template-source')).toHaveAttribute('data-error-line', '3');
    if ((await page.getByTestId('template-source').getAttribute('data-editor')) === 'monaco') {
      await expect(page.locator('[data-testid="template-source"] .squiggly-error').first()).toBeVisible();
      await expect(page.locator('[data-testid="template-source"] .line-numbers').first()).toBeVisible();
    }
    // Fixed, the mark goes.
    await writeInEditor(page, '<p>one</p>\n<p>two</p>\n<p>{{ item.name }}</p>');
    await expect(page.getByTestId('template-error')).toHaveCount(0);
    await expect(page.getByTestId('template-source')).not.toHaveAttribute('data-error-line', /.+/);
  });

  test('a template that never ends is refused at save', async () => {
    const res = await put(api, 'page.error', '{% for i in (1..1000000000) %}{% endfor %}');
    expect(res.status(), await res.text()).toBe(422);
    expect(((await res.json()) as { error?: string }).error ?? '').toMatch(/^template_(too_slow|stopped)$/);
  });

  test('an edited "not available" page is what a dead link shows', async ({ page }) => {
    const res = await put(api, 'page.error', '<div class="card"><h1 class="e2e-gone">{{ error.title }}</h1><p>Ask the sender for a new link.</p></div>');
    expect(res.ok(), await res.text()).toBeTruthy();
    const answer = await page.goto('/s/e2e-no-such-link?nojs=1');
    expect(answer?.status()).toBe(404);
    await expect(page.locator('.e2e-gone')).toBeVisible();
    await expect(page.locator('body')).toContainText('Ask the sender for a new link.');
  });
});
