/**
 * 173 — a file's own bytes served from filex's origin, in a real browser.
 *
 * The server answers every active kind (HTML, SVG, XML, unknown) with a
 * script-less sandbox (httpx.ProtectServedFile) on the explorer's preview,
 * `/api/files/read`, a share's `?inline=1` and a shared folder's entries.
 * The Go tests pin the headers; this spec measures what the browser DOES
 * with them, and that the kinds a person looks at every day still show:
 *
 *   - an HTML file opened inline renders its text, and its script does not
 *     run (a marker the script would set stays absent);
 *   - an SVG and a PNG still draw as pictures, in the preview's <img> and
 *     opened on their own;
 *   - a PDF keeps its type and no sandbox (headless Chromium downloads PDFs,
 *     so the answer is checked; the preview's <object> is 100-viewer-audit's);
 *   - viewers that fetch the bytes (code view, draw.io) still read them;
 *   - the same on a public share with `?inline=1`, with no account at all.
 */
import { test, expect, type APIRequestContext } from '@playwright/test';
import { loginAs } from '../helpers/auth';
import { dropStorageByName, newAuthedRequest, seedLocalStorage } from '../helpers/seed';
import { underBase } from '../helpers/base';
import { minimalPDF } from '../helpers/appPlugin';

const STORAGE = `e2e-inert-${Date.now()}`;
const MOUNT = `/tmp/filex-${STORAGE}`;

const HTML = '<!doctype html><html><body><p id="t">inline page</p>'
  + '<script>document.body.setAttribute("data-ran", "1")</script></body></html>';
const SVG = '<svg xmlns="http://www.w3.org/2000/svg" width="40" height="30"><rect width="40" height="30" fill="#36c"/></svg>';
// 1×1 PNG.
const PNG = Buffer.from(
  'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg==',
  'base64',
);

async function upload(api: APIRequestContext, name: string, mimeType: string, body: Buffer | string) {
  const res = await api.post('/api/files/manager?action=upload', {
    multipart: { path: `${STORAGE}://`, 'file[]': { name, mimeType, buffer: Buffer.from(body) } },
  });
  expect(res.ok(), `upload ${name}: ${res.status()} ${await res.text()}`).toBeTruthy();
}

async function share(api: APIRequestContext, name: string): Promise<string> {
  const res = await api.post('/api/files/share', { data: { path: `${STORAGE}://${name}`, password: false } });
  expect(res.ok(), `share ${name}: ${res.status()}`).toBeTruthy();
  return (await res.json()).share.token as string;
}

const previewURL = (name: string) =>
  underBase(`/api/files/manager?action=preview&path=${encodeURIComponent(`${STORAGE}://${name}`)}`);

test.describe('Served files are inert where they could run, unchanged where they cannot', () => {
  let api: APIRequestContext;
  const tokens: Record<string, string> = {};

  test.beforeAll(async ({ request, playwright, baseURL }) => {
    await dropStorageByName(request, STORAGE);
    await seedLocalStorage(request, STORAGE, MOUNT);
    api = await newAuthedRequest(playwright, baseURL ?? '');
    await upload(api, 'page.html', 'text/html', HTML);
    await upload(api, 'pic.svg', 'image/svg+xml', SVG);
    await upload(api, 'dot.png', 'image/png', PNG);
    await upload(api, 'doc.pdf', 'application/pdf', minimalPDF('inert'));
    await upload(api, 'diagram.drawio', 'application/xml',
      '<mxfile><diagram id="d" name="p"><mxGraphModel><root><mxCell id="0"/></root></mxGraphModel></diagram></mxfile>');
    for (const n of ['page.html', 'pic.svg', 'dot.png', 'doc.pdf']) tokens[n] = await share(api, n);
  });

  test.afterAll(async ({ request }) => {
    await api?.dispose();
    await dropStorageByName(request, STORAGE);
  });

  test('the preview route: HTML shows without running, pictures and PDF show', async ({ page }) => {
    await loginAs(page);

    const html = await page.goto(previewURL('page.html'));
    expect(html?.headers()['content-security-policy'] ?? '').toContain('sandbox');
    await expect(page.locator('#t')).toHaveText('inline page');
    expect(await page.locator('body').getAttribute('data-ran'), 'the page script must not run').toBeNull();

    const svg = await page.goto(previewURL('pic.svg'));
    expect(svg?.status()).toBe(200);
    await expect(page.locator('svg rect')).toHaveCount(1);

    const png = await page.goto(previewURL('dot.png'));
    expect(png?.headers()['content-security-policy'] ?? '').not.toContain('sandbox');
    await expect(page.locator('img')).toHaveCount(1);

    // Headless Chromium has no PDF viewer and turns a PDF navigation into a
    // download, so the PDF is judged by its answer (the preview's own <object>
    // is measured by 100-viewer-audit).
    const pdf = await page.request.get(previewURL('doc.pdf'));
    expect(pdf.status()).toBe(200);
    expect(pdf.headers()['content-type'] ?? '').toContain('application/pdf');
    expect(pdf.headers()['content-security-policy'] ?? '').not.toContain('sandbox');
  });

  test('viewers that fetch a file still read it (code view, draw.io)', async ({ page }) => {
    await loginAs(page);
    await page.goto(underBase('/admin/home'));
    const got = await page.evaluate(async (urls) => {
      const out: Record<string, string> = {};
      for (const [k, u] of Object.entries(urls)) out[k] = await (await fetch(u, { credentials: 'include' })).text();
      return out;
    }, { html: previewURL('page.html'), drawio: previewURL('diagram.drawio') });
    expect(got.html).toContain('inline page');
    expect(got.drawio).toContain('<mxfile');
  });

  test('an SVG still draws inside a filex page, where it is an <img>', async ({ page }) => {
    await loginAs(page);
    await page.goto(underBase('/admin/home'));
    const size = await page.evaluate(async (src) => {
      const img = new Image();
      img.src = src;
      await img.decode();
      return { w: img.naturalWidth, h: img.naturalHeight };
    }, previewURL('pic.svg'));
    expect(size).toEqual({ w: 40, h: 30 });
  });

  test('a public share with ?inline=1: HTML inert, picture and PDF as before', async ({ browser, baseURL }) => {
    const ctx = await browser.newContext({ baseURL });
    const page = await ctx.newPage();

    await page.goto(underBase(`/s/${tokens['page.html']}?inline=1`));
    await expect(page.locator('#t')).toHaveText('inline page');
    expect(await page.locator('body').getAttribute('data-ran'), 'the page script must not run').toBeNull();

    const png = await page.goto(underBase(`/s/${tokens['dot.png']}?inline=1`));
    expect(png?.status()).toBe(200);
    await expect(page.locator('img')).toHaveCount(1);

    const pdf = await page.request.get(underBase(`/s/${tokens['doc.pdf']}?inline=1`));
    expect(pdf.status()).toBe(200);
    expect(pdf.headers()['content-type']).toContain('application/pdf');
    expect(pdf.headers()['content-security-policy'] ?? '').not.toContain('sandbox');
    await ctx.close();
  });
});
