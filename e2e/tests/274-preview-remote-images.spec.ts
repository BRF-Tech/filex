/**
 * 274 - a file's preview asks no other server for anything until the person
 * says so: measured in a real browser, against the real binary, with a server
 * of the spec's own on another origin that counts every request it is sent.
 *
 * A Markdown file names pictures on that server - a Markdown image, a
 * one-pixel <img>, a <picture> with a <source>, a style url(). Opened in the
 * preview it draws placeholders and the server hears nothing; a click asks in
 * filex's own dialog, which names the server, and "Load image" fetches that
 * one picture, with no referrer; "Load all remote images in this file" fetches
 * the rest of them for this opening only (the next opening asks again); with
 * "Load remote images in previews" on, the preview fetches them without
 * asking. The explorer's grid and the thumbnails never fetch them.
 *
 * The rule is packages/core/src/lib/sanitizeHtml.ts (RemoteImages) and
 * composables/useRemoteImages.ts; the unit tests are
 * web/tests/lib/sanitizeRemoteImages.test.ts and
 * web/tests/components/previewRemoteImages.test.ts.
 */
import { test as base, expect, type APIRequestContext, type Page } from '@playwright/test';
import { loginAs } from '../helpers/auth';
import { dropStorageByName, newAuthedRequest, seedLocalStorage } from '../helpers/seed';
import { underBase } from '../helpers/base';
import { readWebPrefs, restoreWebPrefs } from '../helpers/prefs';
import { startPictureServer, type PictureServer } from '../helpers/pictureServer';

const STORAGE = `e2e-remote-img-${Date.now()}`;
const MOUNT = `/tmp/filex-${STORAGE}`;

/** The other server (helpers/pictureServer): what it was asked, with each request's Referer. */
let server: PictureServer;
let origin = '';
let asked: PictureServer['asked'] = [];
const hits = (name: string, tag: string): number => server.hits(name, tag);

/** One Markdown file per test, its addresses tagged so the counts never mix. */
function readme(tag: string): string {
  const at = (name: string) => `${origin}/${name}?t=${tag}`;
  return [
    `# Remote pictures ${tag}`,
    '',
    `![Quarterly chart](${at('chart.png')})`,
    '',
    `<img src="${at('pixel.png')}" alt="" width="1" height="1">`,
    '',
    `<picture><source srcset="${at('source.png')}"><img src="${at('fallback.png')}" alt="framed"></picture>`,
    '',
    `<p style="background: url(${at('style.png')})">styled</p>`,
    '',
    'Closing words.',
  ].join('\n');
}

const test = base.extend<{ api: APIRequestContext }>({
  api: async ({ playwright, baseURL }, use) => {
    const ctx = await newAuthedRequest(playwright, baseURL!);
    await use(ctx);
    await ctx.dispose();
  },
});

async function upload(api: APIRequestContext, name: string, body: string) {
  const res = await api.post('/api/files/manager?action=upload', {
    multipart: { path: `${STORAGE}://`, 'file[]': { name, mimeType: 'text/markdown', buffer: Buffer.from(body) } },
  });
  expect(res.ok(), `upload ${name}: ${res.status()} ${await res.text()}`).toBeTruthy();
}

async function openPreview(page: Page, name: string) {
  // Same origin first, as 170 does: WebKit's cross-origin hop can lose the
  // session token written to sessionStorage a moment before.
  await page.goto(underBase('/healthz'));
  await page.goto(
    underBase(`/admin/files/edit?path=${encodeURIComponent(`${STORAGE}://${name}`)}&type=md&mode=view`),
  );
  await expect(page.locator('.fe-preview__md h1')).toBeVisible({ timeout: 15_000 });
}

test.describe('A preview loads no picture from another server until the person asks', () => {
  test.beforeAll(async ({ request, playwright, baseURL }) => {
    server = await startPictureServer();
    origin = server.origin;
    asked = server.asked;
    await dropStorageByName(request, STORAGE);
    await seedLocalStorage(request, STORAGE, MOUNT);
    const api = await newAuthedRequest(playwright, baseURL!);
    for (const tag of ['ask', 'all', 'setting']) await upload(api, `${tag}.md`, readme(tag));
    await api.dispose();
  });

  test.afterAll(async ({ request }) => {
    await dropStorageByName(request, STORAGE);
    await server.close();
  });

  test('placeholders, no request; a click asks, naming the server; Load image fetches that one, with no referrer', async ({ page }) => {
    await loginAs(page);
    await openPreview(page, 'ask.md');
    const md = page.locator('.fe-preview__md');
    await expect(md.locator('.fe-remote-img[data-remote-image]')).toHaveCount(3);
    await expect(md.locator('.fe-remote-img', { hasText: 'Quarterly chart' })).toContainText('127.0.0.1');
    // Time for anything that was going to load to have gone.
    await page.waitForTimeout(1500);
    expect(asked.filter((a) => a.url.endsWith('?t=ask')), 'the preview asked the picture server').toEqual([]);

    // Cancel loads nothing.
    await md.locator('.fe-remote-img', { hasText: 'Quarterly chart' }).click();
    const dialog = page.locator('.fe-modal__card', { has: page.getByTestId('remote-image-dialog') });
    await expect(dialog).toBeVisible();
    await expect(dialog.getByTestId('remote-image-host')).toHaveText(new URL(origin).host);
    await dialog.getByTestId('remote-image-cancel').click();
    await expect(dialog).toHaveCount(0);
    await page.waitForTimeout(500);
    expect(hits('chart.png', 'ask')).toBe(0);

    // Load image: that picture, once, with no Referer.
    await md.locator('.fe-remote-img', { hasText: 'Quarterly chart' }).click();
    await dialog.getByTestId('remote-image-load').click();
    await expect.poll(() => hits('chart.png', 'ask'), { timeout: 10_000 }).toBe(1);
    const chart = md.locator(`img[src="${origin}/chart.png?t=ask"]`);
    await expect(chart).toHaveAttribute('referrerpolicy', 'no-referrer');
    await expect.poll(() => chart.evaluate((img: HTMLImageElement) => img.complete && img.naturalWidth)).toBe(1);
    expect(asked.find((a) => a.url === '/chart.png?t=ask')?.referer, 'the request carried a Referer').toBeUndefined();
    // The others still wait.
    await page.waitForTimeout(500);
    expect(['pixel.png', 'fallback.png', 'source.png', 'style.png'].map((n) => hits(n, 'ask'))).toEqual([0, 0, 0, 0]);
    await expect(md.locator('.fe-remote-img[data-remote-image]')).toHaveCount(2);
  });

  test('Load all fetches every picture of the file for this opening; the next opening asks again', async ({ page }) => {
    await loginAs(page);
    await openPreview(page, 'all.md');
    const md = page.locator('.fe-preview__md');
    await md.locator('.fe-remote-img[data-remote-image]').first().click();
    await page.getByTestId('remote-image-load-all').click();
    await expect.poll(() => ['chart.png', 'pixel.png', 'fallback.png'].map((n) => hits(n, 'all')), { timeout: 10_000 }).toEqual([1, 1, 1]);
    await expect(md.locator('.fe-remote-img')).toHaveCount(0);
    // A <source> and a style url() are gone from the markup: never fetched.
    expect([hits('source.png', 'all'), hits('style.png', 'all')]).toEqual([0, 0]);
    expect(asked.filter((a) => a.url.endsWith('?t=all')).every((a) => a.referer === undefined)).toBe(true);

    // Another opening of the same file: the placeholders are back, nothing more is fetched.
    const again = await page.context().newPage();
    await openPreview(again, 'all.md');
    await expect(again.locator('.fe-preview__md .fe-remote-img[data-remote-image]')).toHaveCount(3);
    await again.waitForTimeout(1500);
    expect(['chart.png', 'pixel.png', 'fallback.png'].map((n) => hits(n, 'all'))).toEqual([1, 1, 1]);
    await again.close();
  });

  test('with "Load remote images in previews" on, the preview fetches them without asking, with no referrer', async ({ page, api }) => {
    const before = await readWebPrefs(api);
    try {
      await restoreWebPrefs(api, { ...before, remoteImages: 'on' });
      await loginAs(page);
      await openPreview(page, 'setting.md');
      // The <picture> is drawn as its author wrote it: the browser takes its
      // <source>, which the setting lets through, over the fallback <img>.
      await expect
        .poll(() => [hits('chart.png', 'setting'), hits('pixel.png', 'setting'), hits('source.png', 'setting') + hits('fallback.png', 'setting')], {
          timeout: 15_000,
        })
        .toEqual([1, 1, 1]);
      await expect(page.getByTestId('remote-image-dialog')).toHaveCount(0);
      await expect(page.locator('.fe-preview__md .fe-remote-img')).toHaveCount(0);
      expect(asked.filter((a) => a.url.endsWith('?t=setting')).every((a) => a.referer === undefined)).toBe(true);
      // A style url() is never followed, whatever the setting says.
      expect(hits('style.png', 'setting')).toBe(0);
    } finally {
      await restoreWebPrefs(api, before);
    }
  });

  test('the explorer’s grid and the thumbnails fetch none of them', async ({ page, api }) => {
    const already = asked.length;
    // The thumbnails of the three files are drawn (a Markdown file's is its text).
    await expect
      .poll(
        async () => {
          const res = await api.get(`/api/files/manager?action=index&path=${encodeURIComponent(`${STORAGE}://`)}`);
          if (!res.ok()) return -1;
          const body = (await res.json()) as { files?: Array<{ thumb_url?: string }> };
          return (body.files ?? []).filter((f) => f.thumb_url).length;
        },
        { timeout: 30_000 },
      )
      .toBe(3);
    await page.addInitScript(() => localStorage.setItem('filex.tourDone', '1'));
    await loginAs(page);
    await page.goto(underBase(`/admin/explore?path=${encodeURIComponent(`${STORAGE}://`)}`));
    await page.locator('.fe-grid__card, .fe-list__row').first().waitFor();
    await page.evaluate(() => {
      const real = [...document.querySelectorAll('.fe-toolbar__view button')].filter(
        (b) => !b.closest('.fe-toolbar__measure') && !b.closest('[aria-hidden="true"]'),
      );
      (real.find((b) => /grid|[Iı]zgara/i.test(b.getAttribute('title') ?? '')) as HTMLElement | undefined)?.click();
    });
    await expect(page.locator('.fe-grid__card')).toHaveCount(3, { timeout: 15_000 });
    await page.locator('.fe-grid__card').first().hover();
    await page.waitForTimeout(2000);
    expect(asked.slice(already), 'a thumbnail or a card asked the picture server').toEqual([]);
  });
});
