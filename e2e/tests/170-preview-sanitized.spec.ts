/**
 * 170 - a preview draws a file as HTML inside filex's own page, so the markup
 * must be inert: measured in a real browser, against the real binary.
 *
 * A Markdown file carrying inline HTML payloads, an HTML file and a notebook
 * with an HTML output are opened in the preview. Every payload only sets a
 * marker on `window`; after the preview has rendered (and had time for image
 * errors and `<details>` toggles to fire) the marker must still be unset —
 * while the ordinary parts of the same documents (heading, table, code block,
 * picture, link, details) are there as before.
 *
 * The sanitizer is packages/core/src/lib/sanitizeHtml.ts (DOMPurify); the
 * vector list it is unit-tested against is web/tests/fixtures/previewVectors.ts.
 */
import { test as base, expect, type APIRequestContext, type Page } from '@playwright/test';
import { ADMIN_EMAIL, ADMIN_PASSWORD, loginAs } from '../helpers/auth';
import { dropStorageByName, seedLocalStorage } from '../helpers/seed';
import { underBase } from '../helpers/base';

const STORAGE = `e2e-sanitize-${Date.now()}`;
const MOUNT = `/tmp/filex-${STORAGE}`;

const PAYLOADS = [
  '<img src="x-missing-1.png" onerror="window.__fxPreview=1">',
  '<img/src="x-missing-2.png"/onerror=window.__fxPreview=2>',
  '<svg onload="window.__fxPreview=3"><circle r="1"></circle></svg>',
  '<details open ontoggle="window.__fxPreview=4"><summary>toggle</summary>x</details>',
  '<a id="jsLink" href="javascript:window.__fxPreview=5">js link</a>',
  '<iframe srcdoc="<script>parent.__fxPreview=6</script>"></iframe>',
  '<math><mtext><table><mglyph><style><!--</style><img title="--&gt;&lt;img src=x-missing-3.png onerror=window.__fxPreview=7&gt;">',
  '<form><math><mtext></form><form><mglyph><svg><mtext><style><path id="</style><img onerror=window.__fxPreview=8 src=x-missing-4.png>">',
  '<object data="data:text/html,<script>parent.__fxPreview=9</script>"></object>',
  '<p style="background: url(x-missing-5.png)">styled</p>',
].join('\n\n');

const README = [
  '# Readme title',
  '',
  'A [docs link](https://example.test/docs) and `inline code`.',
  '',
  '| Key | Value |',
  '| --- | ----- |',
  '| a   | 1     |',
  '',
  '```js',
  'const answer = 42;',
  '```',
  '',
  '<details><summary>More</summary>hidden body</details>',
  '',
  PAYLOADS,
  '',
  'The end.',
].join('\n');

const HTML_FILE = '<!doctype html><html><body><h1>Page</h1><script>window.__fxPreview=20</script>'
  + '<img src="x-missing-6.png" onerror="window.__fxPreview=21"></body></html>';

const NOTEBOOK = JSON.stringify({
  nbformat: 4,
  nbformat_minor: 5,
  metadata: { kernelspec: { language: 'python', name: 'python3' } },
  cells: [
    { cell_type: 'markdown', metadata: {}, source: ['# Notebook title'] },
    {
      cell_type: 'code',
      metadata: {},
      execution_count: 1,
      source: ['df'],
      outputs: [
        {
          output_type: 'display_data',
          metadata: {},
          data: { 'text/html': ['<table><tr><td>cell value</td></tr></table>\n', PAYLOADS] },
        },
      ],
    },
  ],
});

const test = base.extend<{ api: APIRequestContext }>({
  api: async ({ playwright, baseURL }, use) => {
    const ctx = await playwright.request.newContext({ baseURL });
    const login = await ctx.post('/api/auth/login', { data: { email: ADMIN_EMAIL, password: ADMIN_PASSWORD } });
    const { token } = await login.json();
    await ctx.dispose();
    const authed = await playwright.request.newContext({ baseURL, extraHTTPHeaders: { Authorization: `Bearer ${token}` } });
    await use(authed);
    await authed.dispose();
  },
});

async function upload(api: APIRequestContext, name: string, mimeType: string, body: string) {
  const res = await api.post('/api/files/manager?action=upload', {
    multipart: { path: `${STORAGE}://`, 'file[]': { name, mimeType, buffer: Buffer.from(body) } },
  });
  expect(res.ok(), `upload ${name}: ${res.status()} ${await res.text()}`).toBeTruthy();
}

async function openPreview(page: Page, name: string, type: string) {
  await loginAs(page);
  // Same origin, not about:blank: WebKit's cross-origin hop can lose the
  // session token written to sessionStorage a moment before (100-viewer-audit).
  await page.goto(underBase('/healthz'));
  await page.goto(
    underBase(`/admin/files/edit?path=${encodeURIComponent(`${STORAGE}://${name}`)}&type=${type}&mode=view`),
  );
  await expect(page.locator('.fe-preview')).toBeVisible({ timeout: 15_000 });
}

/** The marker any payload would set, after events have had time to fire. */
async function marker(page: Page): Promise<unknown> {
  await page.waitForTimeout(1500);
  return page.evaluate(() => (window as unknown as { __fxPreview?: unknown }).__fxPreview ?? null);
}

test.describe('A preview never runs what the file carries', () => {
  test.beforeAll(async ({ request, api }) => {
    await dropStorageByName(request, STORAGE);
    await seedLocalStorage(request, STORAGE, MOUNT);
    await upload(api, 'README.md', 'text/markdown', README);
    await upload(api, 'page.html', 'text/html', HTML_FILE);
    await upload(api, 'analysis.ipynb', 'application/json', NOTEBOOK);
  });

  test.afterAll(async ({ request }) => {
    await dropStorageByName(request, STORAGE);
  });

  test('Markdown: the payloads stay inert and the README still reads as one', async ({ page }) => {
    await openPreview(page, 'README.md', 'md');
    const md = page.locator('.fe-preview__md').first();
    await expect(md.locator('h1')).toHaveText('Readme title');
    await expect(md.locator('table td').first()).toHaveText('a');
    await expect(md.locator('pre code')).toContainText('const answer = 42;');
    await expect(md.locator('a', { hasText: 'docs link' })).toHaveAttribute('href', 'https://example.test/docs');
    await expect(md.locator('details summary', { hasText: 'More' })).toHaveCount(1);
    // ⚠ Nothing is asserted AFTER the payloads: the mutation vectors are
    // deliberately unclosed markup, and the elements they open swallow the
    // text that follows — gone with them, as it should be.

    await page.locator('#user-content-jsLink, a:has-text("js link")').first().click({ trial: false }).catch(() => undefined);
    expect(await marker(page), 'no payload may run').toBeNull();
    expect(await md.locator('iframe, object, form, script, svg, math').count()).toBe(0);
    expect(await md.locator('[onerror], [onload], [ontoggle]').count()).toBe(0);
  });

  test('HTML file: shown as its source, never run', async ({ page }) => {
    await openPreview(page, 'page.html', 'html');
    await expect(page.locator('.fe-preview__code-editor, .fe-preview__pre').first()).toBeVisible({ timeout: 15_000 });
    await expect(page.locator('.fe-preview')).toContainText('window.__fxPreview=20');
    expect(await marker(page), 'the file’s script must not run').toBeNull();
  });

  test('Notebook: an HTML output keeps its table and runs nothing', async ({ page }) => {
    await openPreview(page, 'analysis.ipynb', 'ipynb');
    await expect(page.locator('.fe-preview')).toContainText('Notebook title', { timeout: 15_000 });
    await expect(page.locator('.fe-preview td', { hasText: 'cell value' })).toHaveCount(1);
    expect(await marker(page), 'no output payload may run').toBeNull();
    expect(await page.locator('.fe-preview iframe, .fe-preview object, .fe-preview form, .fe-preview math').count()).toBe(0);
  });
});
