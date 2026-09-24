/**
 * 122 — the public drop page (`/d/<token>`), as a stranger meets it.
 *
 * QA sweep, 2026-09-21 (#19):
 *   · "Ask uploader name" is ON by default and the page had no name field, so
 *     every submission arrived as `<date>_anon`;
 *   · a file of a type the link does not take was SENT and came back as "The
 *     app returned an error" — the app-plugin runtime's words;
 *   · each file of a drop went up in a request of its own: one drop of three
 *     files, three submission folders.
 *
 * Checked on the SERVER as well as on the page: the submission folder the owner
 * finds is named after the person, and holds exactly the file the link takes.
 */
import { test, expect, type APIRequestContext } from '@playwright/test';
import { dropStorageByName, newAuthedRequest, seedLocalStorage } from '../helpers/seed';

const STORAGE = `e2e-drop-${Date.now()}`;
const FOLDER = 'Gelen';

test.describe('public drop page', () => {
  let api: APIRequestContext;
  let token = '';

  test.beforeAll(async ({ playwright, baseURL, request }) => {
    api = await newAuthedRequest(playwright, baseURL ?? '');
    await dropStorageByName(request, STORAGE);
    await seedLocalStorage(request, STORAGE, `/tmp/filex-${STORAGE}`);
    const mk = await api.post('/api/files/manager?action=newfolder', { data: { path: `${STORAGE}://`, name: FOLDER } });
    expect(mk.ok(), `newfolder ${mk.status()} ${await mk.text()}`).toBeTruthy();
    const made = await api.post('/api/files/share', {
      data: {
        path: `${STORAGE}://${FOLDER}`,
        kind: 'drop',
        drop_settings: { ask_name: true, max_file_size_mb: 1, allowed_ext: ['pdf'] },
      },
    });
    expect(made.ok(), `drop link ${made.status()}`).toBeTruthy();
    token = (await made.json()).share.token;
  });

  test.afterAll(async ({ request }) => {
    await dropStorageByName(request, STORAGE);
    await api.dispose();
  });

  test('asks the name, refuses the wrong type in words, sends the rest in ONE submission', async ({ page }) => {
    const uploads: string[] = [];
    page.on('request', (r) => {
      if (r.method() === 'POST' && r.url().includes(`/api/public/d/${token}/upload`)) uploads.push(r.url());
    });
    await page.goto(`/d/${token}`);
    await expect(page.getByTestId('public-request-drop')).toBeVisible();
    // The limits are said before anything is picked.
    await expect(page.getByTestId('public-request-limits')).toContainText('PDF');
    // The name the link asks for, above the drop area.
    const name = page.getByTestId('public-request-name-input');
    await expect(name).toBeVisible();
    await name.fill('Ayşe Yılmaz');

    await page.getByTestId('public-request-input').setInputFiles([
      { name: 'notlar.txt', mimeType: 'text/plain', buffer: Buffer.from('not a pdf') },
      { name: 'fatura.pdf', mimeType: 'application/pdf', buffer: Buffer.from('%PDF-1.4\n%%EOF\n') },
      { name: 'ek.pdf', mimeType: 'application/pdf', buffer: Buffer.from('%PDF-1.4\n%%EOF\n') },
    ]);
    const list = page.getByTestId('public-request-uploads');
    await expect(list.locator('[data-state="done"]')).toHaveCount(2);
    await expect(list.locator('[data-state="refused"]')).toHaveCount(1);
    await expect(list.locator('[data-state="refused"]')).toContainText(/type of file|türde dosya/);
    await expect(list).not.toContainText(/app returned an error|Uygulama hata döndürdü/i);
    expect(uploads).toHaveLength(1);

    // The owner's side: one submission folder, named after the person.
    const listing = await api.get(`/api/files/manager?action=index&path=${encodeURIComponent(`${STORAGE}://${FOLDER}`)}`);
    const files = ((await listing.json()) as { files: Array<{ basename: string; type: string; path: string }> }).files;
    const subs = files.filter((f) => f.type === 'dir');
    expect(subs).toHaveLength(1);
    expect(subs[0].basename).toMatch(/_Ayşe Yılmaz$/);
    const inside = await api.get(`/api/files/manager?action=index&path=${encodeURIComponent(subs[0].path)}`);
    const names = ((await inside.json()) as { files: Array<{ basename: string }> }).files.map((f) => f.basename).sort();
    expect(names).toEqual(['ek.pdf', 'fatura.pdf']);
  });
});
