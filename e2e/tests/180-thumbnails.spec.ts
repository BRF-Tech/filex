/**
 * 180 — thumbnails that follow their files (task #129, GitHub #79).
 *
 * The case #79 is about: files filex did not write (put on the storage's disk
 * and found by a sync) had no thumbnail until an operator ran a command, SVGs
 * had none without rsvg-convert, and a file replaced on the disk kept its old
 * picture. What is held here, end to end against a real server:
 *
 *   1. a picture put on disk and synced gets its thumbnail once its folder is
 *      listed, and a new picture (a new `v`) when it is replaced on disk;
 *   2. an SVG gets a thumbnail from the built-in engine (no rsvg-convert is
 *      needed on the machine running this);
 *   3. a folder is drawn with the files that came into it last rising out
 *      of it in the grid (a text file as its first lines), and a mouse
 *      resting on it shows what the folder holds;
 *   4. Admin → Tools → Thumbnail repair repairs a storage and says what it did.
 */
import { test, expect, type APIRequestContext } from '@playwright/test';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { apiLogin, loginAs } from '../helpers/auth';
import { dropStorageByName, seedLocalStorage, storageRoot } from '../helpers/seed';
import { pickOption } from '../helpers/choiceSelect';

const STAMP = Date.now();
const NAME = `e2e-thumbs-${STAMP}`;
const MOUNT = `/tmp/filex-${NAME}`;
const FIXTURES = path.join(path.dirname(fileURLToPath(import.meta.url)), '../fixtures/file-types');

/** Starts a full scan and waits until a run newer than the ones before it is over (as 140 does). */
async function syncNow(request: APIRequestContext, id: number) {
  const runs = async () => {
    const res = await request.get(`/api/admin/storages/${id}/sync-runs?limit=50`);
    expect(res.ok()).toBeTruthy();
    return ((await res.json()) as { entries?: Array<{ id: number; status: string }> }).entries ?? [];
  };
  const before = new Set((await runs()).map((r) => r.id));
  const started = await request.post(`/api/admin/storages/${id}/sync`);
  expect(started.status(), await started.text()).toBeLessThan(300);
  await expect
    .poll(async () => (await runs()).find((r) => !before.has(r.id) && r.status !== 'running')?.status ?? 'pending', {
      timeout: 30_000,
    })
    .toBe('ok');
}

async function thumbOf(request: APIRequestContext, folder: string, name: string): Promise<string> {
  const res = await request.get(`/api/files/manager?action=index&path=${encodeURIComponent(folder)}`);
  const body = (await res.json()) as { files?: Array<{ basename: string; thumb_url?: string }> };
  return body.files?.find((f) => f.basename === name)?.thumb_url ?? '';
}

test.describe('Thumbnails follow their files', () => {
  // A sync, a background render, the renderer's 60 s loop guard and a repair
  // take longer than the suite's 30 s per test, which is for a click and its
  // answer.
  test.describe.configure({ timeout: 180_000 });
  let storageId = 0;

  test.beforeAll(async ({ request }) => {
    await dropStorageByName(request, NAME);
    const root = storageRoot(MOUNT);
    fs.mkdirSync(path.join(root, 'Holiday'), { recursive: true });
    fs.copyFileSync(path.join(FIXTURES, 'landscape.jpg'), path.join(root, 'Holiday', 'coast.jpg'));
    fs.copyFileSync(path.join(FIXTURES, 'square.jpg'), path.join(root, 'Holiday', 'square.jpg'));
    fs.copyFileSync(path.join(FIXTURES, 'landscape.jpg'), path.join(root, 'cover.jpg'));
    fs.writeFileSync(path.join(root, 'Holiday', 'notes.txt'), 'day one\n');
    const st = await seedLocalStorage(request, NAME, MOUNT, { sync_mode: 'ondemand' });
    storageId = st.id;
    await syncNow(request, storageId);
  });

  test.afterAll(async ({ request }) => {
    await dropStorageByName(request, NAME);
  });

  // The API calls below go out with the session cookie this sets.
  test.beforeEach(async ({ request }) => {
    await apiLogin(request);
  });

  test('a picture found on disk is drawn once listed, and drawn again when replaced', async ({ request }) => {
    const folder = `${NAME}://`;
    await expect.poll(() => thumbOf(request, folder, 'cover.jpg'), { timeout: 30_000 }).not.toBe('');
    const first = await thumbOf(request, folder, 'cover.jpg');
    const v1 = new URL(first, 'http://x').searchParams.get('v');
    expect(v1, 'the URL names the render').toBeTruthy();

    // Replaced behind filex's back; the sync records it.
    const root = storageRoot(MOUNT);
    fs.copyFileSync(path.join(FIXTURES, 'square.jpg'), path.join(root, 'cover.jpg'));
    const later = new Date(Date.now() + 5000);
    fs.utimesSync(path.join(root, 'cover.jpg'), later, later);
    await syncNow(request, storageId);
    // The first render is seconds old, so the loop guard (a file attempted in
    // the last 60 s is not asked for again) holds the redraw until it passes;
    // the listing after that draws it.
    await expect
      .poll(async () => new URL((await thumbOf(request, folder, 'cover.jpg')) || '/x', 'http://x').searchParams.get('v'), {
        timeout: 110_000,
        intervals: [2_000, 5_000],
      })
      .not.toBe(v1);
  });

  test('an SVG is drawn by the built-in engine, not as the placeholder card', async ({ page, request }) => {
    // Uploaded, so the catalogue records the content sniff's type (text/xml
    // or text/plain: the sniff has no SVG signature). 0.49 routed by that type
    // and gave every such SVG the extension's placeholder card.
    const up = await request.post('/api/files/manager?action=upload', {
      multipart: {
        path: `${NAME}://`,
        'file[]': { name: 'logo.svg', mimeType: 'image/svg+xml', buffer: fs.readFileSync(path.join(FIXTURES, 'logo.svg')) },
      },
    });
    expect(up.ok(), `upload ${up.status()}`).toBeTruthy();
    await expect.poll(() => thumbOf(request, `${NAME}://`, 'logo.svg'), { timeout: 30_000 }).not.toBe('');
    const url = await thumbOf(request, `${NAME}://`, 'logo.svg');
    const img = await request.get(url);
    expect(img.status()).toBe(200);
    expect(img.headers()['content-type']).toBe('image/jpeg');

    // A quarter of the way in, the logo is its blue gradient; the placeholder
    // card for .svg is one flat green.
    await page.goto('about:blank');
    const [r, g, b] = await page.evaluate(async (b64) => {
      const bytes = Uint8Array.from(atob(b64), (c) => c.charCodeAt(0));
      const bmp = await createImageBitmap(new Blob([bytes], { type: 'image/jpeg' }));
      const canvas = new OffscreenCanvas(bmp.width, bmp.height);
      const ctx = canvas.getContext('2d')!;
      ctx.drawImage(bmp, 0, 0);
      const d = ctx.getImageData(Math.floor(bmp.width / 4), Math.floor(bmp.height / 4), 1, 1).data;
      return [d[0], d[1], d[2]];
    }, (await img.body()).toString('base64'));
    expect(b, `the pixel is rgb(${r}, ${g}, ${b}): the placeholder card, not the logo`).toBeGreaterThan(g + 20);
  });

  test('a folder shows its pictures, and what it holds on hover', async ({ page, request }) => {
    // The Holiday folder's pictures are drawn once it has been listed.
    await expect
      .poll(async () => {
        const res = await request.get(`/api/files/manager?action=index&path=${encodeURIComponent(`${NAME}://Holiday`)}`);
        const body = (await res.json()) as { files?: Array<{ thumb_url?: string }> };
        return (body.files ?? []).filter((f) => f.thumb_url).length;
      }, { timeout: 30_000 })
      .toBeGreaterThanOrEqual(3);
    // The first-run tour would sit over the cards and take the hover.
    await page.addInitScript(() => localStorage.setItem('filex.tourDone', '1'));
    await loginAs(page);
    await page.goto(`/admin/explore?path=${encodeURIComponent(`${NAME}://`)}`);
    await page.locator('.fe-grid__card, .fe-list__row').first().waitFor();
    await page.evaluate(() => {
      const real = [...document.querySelectorAll('.fe-toolbar__view button')].filter(
        (b) => !b.closest('.fe-toolbar__measure') && !b.closest('[aria-hidden="true"]'),
      );
      (real.find((b) => /grid|[Iı]zgara/i.test(b.getAttribute('title') ?? '')) as HTMLElement | undefined)?.click();
    });
    const card = page.locator('.fe-grid__card--folder', { hasText: 'Holiday' });
    // The folder drawn with its three files rising out of it: the two
    // pictures and the text file, drawn as its first lines.
    await expect(card.locator('.fe-fmosaic__print')).toHaveCount(3, { timeout: 20_000 });
    await expect(card.locator('.fe-fmosaic__print img')).toHaveCount(3, { timeout: 20_000 });
    await card.hover();
    const peek = page.locator('[data-fe-peek]');
    await expect(peek).toBeVisible();
    await expect(peek).toContainText('coast.jpg');
    await expect(peek).toContainText('notes.txt');
    await page.mouse.move(2, 2);
    await expect(peek).toHaveCount(0);
  });

  test('Tools → Thumbnail repair repairs a storage and says what it did', async ({ page }) => {
    await loginAs(page);
    await page.goto('/admin/tools?tab=thumbnails');
    await pickOption(page.getByTestId('thumb-repair-storage'), NAME);
    await pickOption(page.getByTestId('thumb-repair-mode'), 'rebuild');
    await page.getByTestId('thumb-repair-start').click();
    const result = page.getByTestId('thumb-repair-result');
    await expect(result).toBeVisible({ timeout: 60_000 });
    await expect(result).toContainText(`${NAME}://`);
    await expect(page.getByTestId('thumb-repair-ok')).not.toHaveText('0');
    await expect(page.getByTestId('thumb-repair-failed')).toHaveText('0');
  });
});
