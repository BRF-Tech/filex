/**
 * 276 - a Mermaid diagram and an EPUB book ask no other server for anything
 * until the person says so, as a Markdown preview does (274): measured in a
 * real browser against the real binary, with a server of the spec's own on
 * another origin that counts every request (helpers/pictureServer).
 *
 * Mermaid: a .mmd file and Markdown fences name pictures on that server - an
 * image node (`@{ img: … }`, which Mermaid itself would load to measure it), a
 * picture in an HTML label, a themeCSS directive with a background url(). The diagram is drawn with placeholders and the server
 * hears nothing; a click on the image node asks in filex's dialog, and Load
 * image fetches it with no referrer. With "Load remote images in previews" on
 * the image node and the label picture are fetched without asking; a style
 * url() never is.
 *
 * EPUB: a book whose chapter names a picture, a style sheet, a style url()
 * and a font on that server. The chapter is drawn (its own picture, from the
 * book, is there) and the server hears nothing; the reader says pictures
 * were held, and Load all remote images in this file draws the chapter again
 * with its picture and style sheet, with no referrer. A url() of the
 * chapter's own styles and a font from elsewhere never load (in Chromium a
 * frame's CSS request carries filex's origin as its Referer, whatever the
 * frame's referrer policy says - measured).
 *
 * The rules: packages/core/src/lib/mermaidRender.ts, lib/sanitizeHtml.ts
 * (the diagram kind's RemoteImages), lib/epubRemote.ts.
 */
import { test as base, expect, type APIRequestContext, type Page } from '@playwright/test';
import { loginAs } from '../helpers/auth';
import { dropStorageByName, newAuthedRequest, seedLocalStorage } from '../helpers/seed';
import { underBase } from '../helpers/base';
import { readWebPrefs, restoreWebPrefs } from '../helpers/prefs';
import { startPictureServer, type PictureServer } from '../helpers/pictureServer';
import { zip } from '../helpers/zip';

const STORAGE = `e2e-remote-dia-${Date.now()}`;
const MOUNT = `/tmp/filex-${STORAGE}`;

let server: PictureServer;

const at = (name: string, tag: string) => `${server.origin}/${name}?t=${tag}`;

/** A flowchart with an image node and a picture in an HTML label. */
function flow(tag: string): string {
  return [
    'flowchart LR',
    `  A@{ img: "${at('node.png', tag)}", label: "Logo", pos: "t", w: 60, h: 60, constraint: "on" }`,
    `  B["<img src='${at('label.png', tag)}' width='20' height='20'> labelled"]`,
    '  A --> B',
  ].join('\n');
}

/**
 * Markdown with the flowchart and a themeCSS url() directive, each a fence of
 * its own. (A `style` or `classDef` line cannot spell an address: Mermaid's
 * grammar has no token for its `//`, and the fence stays code - measured.)
 */
function markdown(tag: string): string {
  return [
    `# Diagrams ${tag}`,
    '',
    '```mermaid',
    flow(tag),
    '```',
    '',
    '```mermaid',
    `%%{init: {"themeCSS": ".node rect { fill: url(${at('theme.png', tag)}); } .label { background-image: url(${at('theme-label.png', tag)}); }"}}%%`,
    'flowchart LR',
    '  E --> F',
    '```',
    '',
    'The end.',
  ].join('\n');
}

/** An EPUB 3 book of one chapter that names a picture, a style sheet, a style url() and a font elsewhere. */
function book(tag: string): Buffer {
  return zip({
    mimetype: 'application/epub+zip',
    'META-INF/container.xml':
      '<?xml version="1.0"?><container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container">' +
      '<rootfiles><rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/></rootfiles></container>',
    'OEBPS/content.opf':
      '<?xml version="1.0" encoding="UTF-8"?><package xmlns="http://www.idpf.org/2007/opf" version="3.0" unique-identifier="id">' +
      '<metadata xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:identifier id="id">urn:uuid:275</dc:identifier>' +
      '<dc:title>Remote pictures</dc:title><dc:language>en</dc:language><meta property="dcterms:modified">2026-10-16T00:00:00Z</meta></metadata>' +
      '<manifest><item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/>' +
      '<item id="ch1" href="ch1.xhtml" media-type="application/xhtml+xml"/>' +
      '<item id="own" href="own.svg" media-type="image/svg+xml"/></manifest>' +
      '<spine><itemref idref="ch1"/></spine></package>',
    'OEBPS/nav.xhtml':
      '<?xml version="1.0" encoding="UTF-8"?><html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops">' +
      '<head><title>Contents</title></head><body><nav epub:type="toc"><ol><li><a href="ch1.xhtml">Chapter</a></li></ol></nav></body></html>',
    'OEBPS/own.svg': '<svg xmlns="http://www.w3.org/2000/svg" width="40" height="40"><rect width="40" height="40" fill="#08a"/></svg>',
    'OEBPS/ch1.xhtml':
      '<?xml version="1.0" encoding="UTF-8"?><html xmlns="http://www.w3.org/1999/xhtml"><head><title>Chapter</title>' +
      `<link rel="stylesheet" href="${at('book.css', tag)}"/>` +
      `<style>@font-face { font-family: far; src: url(${at('font.woff', tag)}); } h1 { font-family: far, serif; }</style>` +
      '</head><body><h1>Chapter one</h1>' +
      '<p><img id="own" src="own.svg" alt="own"/></p>' +
      `<p><img id="far" src="${at('picture.png', tag)}" alt="far"/></p>` +
      `<p style="background-image: url(${at('style.png', tag)})">styled</p>` +
      '</body></html>',
  });
}

const test = base.extend<{ api: APIRequestContext }>({
  api: async ({ playwright, baseURL }, use) => {
    const ctx = await newAuthedRequest(playwright, baseURL!);
    await use(ctx);
    await ctx.dispose();
  },
});

async function upload(api: APIRequestContext, name: string, mimeType: string, body: Buffer | string) {
  const res = await api.post('/api/files/manager?action=upload', {
    multipart: { path: `${STORAGE}://`, 'file[]': { name, mimeType, buffer: Buffer.isBuffer(body) ? body : Buffer.from(body) } },
  });
  expect(res.ok(), `upload ${name}: ${res.status()} ${await res.text()}`).toBeTruthy();
}

async function openPreview(page: Page, name: string, type: string) {
  // Same origin first, as 170 does: WebKit's cross-origin hop can lose the
  // session token written to sessionStorage a moment before.
  await page.goto(underBase('/healthz'));
  await page.goto(underBase(`/admin/files/edit?path=${encodeURIComponent(`${STORAGE}://${name}`)}&type=${type}&mode=view`));
  await expect(page.locator('.fe-preview')).toBeVisible({ timeout: 15_000 });
}

/** What the picture server was asked for under `tag`, logged before the assertion so a red run says it. */
function askedFor(tag: string): string[] {
  const asked = server.tagged(tag);
  console.log(`[276] ${tag}: ${JSON.stringify(asked.map((a) => (a.referer ? `${a.url} referer=${a.referer}` : a.url)))}`);
  return asked.map((a) => a.url);
}

test.describe('A diagram and a book load no picture from another server until the person asks', () => {
  test.beforeAll(async ({ request, playwright, baseURL }) => {
    server = await startPictureServer();
    await dropStorageByName(request, STORAGE);
    await seedLocalStorage(request, STORAGE, MOUNT);
    const api = await newAuthedRequest(playwright, baseURL!);
    for (const tag of ['mmd', 'mmd-set']) await upload(api, `${tag}.mmd`, 'text/plain', flow(tag));
    await upload(api, 'fences.md', 'text/markdown', markdown('md'));
    for (const tag of ['epub', 'epub-set']) await upload(api, `${tag}.epub`, 'application/epub+zip', book(tag));
    await api.dispose();
  });

  test.afterAll(async ({ request }) => {
    await dropStorageByName(request, STORAGE);
    await server.close();
  });

  test('a .mmd diagram: no request; its image node asks, and Load image fetches it with no referrer', async ({ page }) => {
    await loginAs(page);
    await openPreview(page, 'mmd.mmd', 'mmd');
    const diagram = page.locator('.filex-viewer-mermaid svg').first();
    await expect(diagram).toBeVisible({ timeout: 20_000 });
    await expect(diagram).toContainText('labelled');
    await page.waitForTimeout(2000);
    expect(askedFor('mmd'), 'the diagram asked the picture server').toEqual([]);

    const node = page.locator(`.filex-viewer-mermaid [data-remote-image="${at('node.png', 'mmd')}"]`);
    await expect(node).toHaveCount(1);
    await node.click();
    const dialog = page.locator('.fe-modal__card', { has: page.getByTestId('remote-image-dialog') });
    await expect(dialog.getByTestId('remote-image-host')).toHaveText(new URL(server.origin).host);
    await dialog.getByTestId('remote-image-load').click();
    await expect.poll(() => server.hits('node.png', 'mmd'), { timeout: 10_000 }).toBe(1);
    expect(server.tagged('mmd').every((a) => a.referer === undefined), 'a request carried a Referer').toBe(true);
    await page.waitForTimeout(500);
    expect(server.hits('label.png', 'mmd'), 'the label picture still waits').toBe(0);
  });

  test('Markdown fences: an image node, a label picture and a themeCSS url() ask nothing; Load image fetches the node', async ({ page }) => {
    await loginAs(page);
    await openPreview(page, 'fences.md', 'md');
    const md = page.locator('.fe-preview__md');
    await expect(md.locator('.fe-preview__md-mermaid svg')).toHaveCount(2, { timeout: 20_000 });
    await page.waitForTimeout(2000);
    expect(askedFor('md'), 'a fence asked the picture server').toEqual([]);
    await md.locator(`[data-remote-image="${at('node.png', 'md')}"]`).click();
    await page.getByTestId('remote-image-load').click();
    await expect.poll(() => server.hits('node.png', 'md'), { timeout: 10_000 }).toBe(1);
    expect(server.tagged('md').every((a) => a.referer === undefined)).toBe(true);
    await page.waitForTimeout(500);
    expect(['theme.png', 'theme-label.png'].map((n) => server.hits(n, 'md')), 'a style url() was followed').toEqual([0, 0]);
  });

  test('with the setting on, a diagram fetches its pictures without asking, with no referrer; a style url() never', async ({ page, api }) => {
    const before = await readWebPrefs(api);
    try {
      await restoreWebPrefs(api, { ...before, remoteImages: 'on' });
      await loginAs(page);
      await openPreview(page, 'mmd-set.mmd', 'mmd');
      await expect(page.locator('.filex-viewer-mermaid svg').first()).toBeVisible({ timeout: 20_000 });
      await expect.poll(() => [server.hits('node.png', 'mmd-set') > 0, server.hits('label.png', 'mmd-set') > 0], { timeout: 15_000 }).toEqual([
        true,
        true,
      ]);
      askedFor('mmd-set');
      await expect(page.getByTestId('remote-image-dialog')).toHaveCount(0);
      expect(server.tagged('mmd-set').every((a) => a.referer === undefined), 'a request carried a Referer').toBe(true);
    } finally {
      await restoreWebPrefs(api, before);
    }
  });

  test('an EPUB chapter is drawn without its pictures from elsewhere; Load all draws them, with no referrer', async ({ page }) => {
    await loginAs(page);
    await openPreview(page, 'epub.epub', 'epub');
    const chapter = page.frameLocator('.filex-viewer-epub iframe').first();
    await expect(chapter.locator('h1')).toHaveText('Chapter one', { timeout: 20_000 });
    // The book's own picture is drawn.
    await expect
      .poll(() => chapter.locator('#own').evaluate((img: HTMLImageElement) => img.complete && img.naturalWidth), { timeout: 10_000 })
      .toBe(40);
    await page.waitForTimeout(2000);
    expect(askedFor('epub'), 'the book asked the picture server').toEqual([]);

    await expect(page.getByTestId('epub-remote-notice')).toBeVisible();
    await page.getByTestId('epub-remote-load').click();
    const dialog = page.locator('.fe-modal__card', { has: page.getByTestId('remote-image-dialog') });
    await expect(dialog.getByTestId('remote-image-host')).toHaveText(new URL(server.origin).host);
    await dialog.getByTestId('remote-image-load-all').click();
    await expect.poll(() => server.hits('picture.png', 'epub'), { timeout: 15_000 }).toBeGreaterThan(0);
    await page.waitForTimeout(1000);
    askedFor('epub');
    expect(server.tagged('epub').every((a) => a.referer === undefined), 'a request carried a Referer').toBe(true);
    // A url() of the chapter's own styles and a font from elsewhere never
    // load: a frame's CSS request would carry filex's origin as its Referer.
    expect([server.hits('style.png', 'epub'), server.hits('font.woff', 'epub')]).toEqual([0, 0]);
    await expect(page.getByTestId('epub-remote-notice')).toHaveCount(0);
  });

  test('with the setting on, an EPUB chapter draws its pictures from elsewhere without asking', async ({ page, api }) => {
    const before = await readWebPrefs(api);
    try {
      await restoreWebPrefs(api, { ...before, remoteImages: 'on' });
      await loginAs(page);
      await openPreview(page, 'epub-set.epub', 'epub');
      const chapter = page.frameLocator('.filex-viewer-epub iframe').first();
      await expect(chapter.locator('h1')).toHaveText('Chapter one', { timeout: 20_000 });
      await expect.poll(() => server.hits('picture.png', 'epub-set'), { timeout: 15_000 }).toBeGreaterThan(0);
      await page.waitForTimeout(1000);
      askedFor('epub-set');
      expect([server.hits('style.png', 'epub-set'), server.hits('font.woff', 'epub-set')]).toEqual([0, 0]);
      await expect(page.getByTestId('epub-remote-notice')).toHaveCount(0);
      expect(server.tagged('epub-set').every((a) => a.referer === undefined), 'a request carried a Referer').toBe(true);
    } finally {
      await restoreWebPrefs(api, before);
    }
  });
});
