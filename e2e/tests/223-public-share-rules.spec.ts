/**
 * 223 — the public link's rules and words are the SERVER's (#210, 0.54).
 *
 * The 0.54 audit of "server work done in the browser" found the public share
 * surface deciding for itself:
 *   · B7  the PIN: the visitor's box stopped at a hard-coded 12 while POST
 *         /api/files/share took a PIN of any length, so a link made through the
 *         API with a long PIN could not be opened from its own page;
 *   · A7  the JavaScript pages said every limit in words of their own,
 *         different from the server's no-JavaScript page and refusals;
 *   · A10 the download command was assembled in the dialog (zip=wait, ?pin=,
 *         the S3 redirect), and an agent over MCP got none;
 *   · B6  "My shares" judged a link by its date on the browser's clock, so a
 *         link whose downloads were used up still read as active.
 *
 * Every assertion is checked against what the SERVER answers.
 */
import { test, expect, type APIRequestContext } from '@playwright/test';
import { dropStorageByName, newAuthedRequest, seedLocalStorage } from '../helpers/seed';

const STORAGE = `e2e-223-${Date.now()}`;
const FOLDER = 'Raporlar';
const FILE = 'ozet.txt';

type Strings = { lang: string; strings: Record<string, string> };

test.describe('public link rules come from the server', () => {
  let api: APIRequestContext;

  test.beforeAll(async ({ playwright, baseURL, request }) => {
    api = await newAuthedRequest(playwright, baseURL ?? '');
    await dropStorageByName(request, STORAGE);
    await seedLocalStorage(request, STORAGE, `/tmp/filex-${STORAGE}`);
    const mk = await api.post('/api/files/manager?action=newfolder', { data: { path: `${STORAGE}://`, name: FOLDER } });
    expect(mk.ok(), `newfolder ${mk.status()} ${await mk.text()}`).toBeTruthy();
    const up = await api.post('/api/files/manager?action=upload', {
      multipart: { path: `${STORAGE}://`, 'file[]': { name: FILE, mimeType: 'text/plain', buffer: Buffer.from('ozet') } },
    });
    expect(up.ok(), `upload ${up.status()}`).toBeTruthy();
  });

  test.afterAll(async ({ request }) => {
    await dropStorageByName(request, STORAGE);
    await api.dispose();
  });

  test('B7: a PIN outside the one rule is refused when the link is made, with the bounds and a sentence', async () => {
    for (const pin of ['123', '1234567890123']) {
      const res = await api.post('/api/files/share', { data: { path: `${STORAGE}://${FILE}`, pin } });
      expect(res.status(), `pin of ${pin.length}`).toBe(400);
      const body = (await res.json()) as { error: string; pin_min: number; pin_max: number; message: string };
      expect(body.error).toBe('pin_length');
      expect(body.pin_min).toBe(4);
      expect(body.pin_max).toBe(12);
      expect(body.message).toContain('12');
    }
    const ok = await api.post('/api/files/share', { data: { path: `${STORAGE}://${FILE}`, pin: '123456789012' } });
    expect(ok.ok(), `a 12-character PIN ${ok.status()}`).toBeTruthy();
  });

  test('A10: the answer carries the download command the server wrote - folder ZIP, PIN, redirects', async () => {
    const res = await api.post('/api/files/share', { data: { path: `${STORAGE}://${FOLDER}`, pin: '4321' } });
    expect(res.ok()).toBeTruthy();
    const { share } = (await res.json()) as { share: { url: string; download_command?: { curl: string; powershell: string } } };
    const cmd = share.download_command;
    expect(cmd, 'download_command').toBeTruthy();
    expect(cmd!.curl).toMatch(/^curl -fSL -o 'Raporlar\.zip' '/);
    expect(cmd!.curl).toContain(share.url);
    expect(cmd!.curl).toContain('zip=wait');
    expect(cmd!.curl).toContain('pin=4321');
    expect(cmd!.powershell).toMatch(/^Invoke-WebRequest -UseBasicParsing -Uri '/);
    expect(cmd!.powershell).toContain("-OutFile 'Raporlar.zip'");

    // A file request has nothing to fetch, so no command.
    const drop = await api.post('/api/files/share', { data: { path: `${STORAGE}://${FOLDER}`, kind: 'drop' } });
    const dropBody = (await drop.json()) as { share: { download_command?: unknown } };
    expect(dropBody.share.download_command ?? null).toBeNull();
  });

  test('A7 + B7: the page says the server sentences and its PIN box stops at pin_max', async ({ page }) => {
    const res = await api.post('/api/files/share', { data: { path: `${STORAGE}://${FOLDER}`, pin: '9876' } });
    const { share } = (await res.json()) as { share: { token: string } };
    const said = (await (await page.request.get('/api/public/strings?lang=en')).json()) as Strings;
    expect(said.lang).toBe('en');
    expect(said.strings.folder_zip).toBeTruthy();

    await page.goto(`/s/${share.token}?lang=en`);
    const box = page.getByTestId('public-page-pin-input');
    await expect(box).toBeVisible();
    await expect(box).toHaveAttribute('maxlength', '12');
    await box.fill('9876');
    await page.getByTestId('public-page-pin-submit').click();
    // The folder's "download everything" is the server's own words for it.
    await expect(page.getByTestId('public-share-zip')).toHaveText(said.strings.folder_zip);
  });

  test('B6: My shares says where each link stands, as the server judges it', async ({ request }) => {
    const res = await api.post('/api/files/share', { data: { path: `${STORAGE}://${FILE}`, max_downloads: 1 } });
    const { share } = (await res.json()) as { share: { id: number; token: string } };
    const before = (await (await api.get('/api/shares')).json()) as { items: Array<{ share: { id: number; state?: string } }> };
    expect(before.items.find((r) => r.share.id === share.id)?.share.state).toBe('active');

    // One download spends the link's only one.
    const dl = await request.get(`/s/${share.token}?download=1`);
    expect(dl.ok(), `download ${dl.status()}`).toBeTruthy();

    const after = (await (await api.get('/api/shares')).json()) as { items: Array<{ share: { id: number; state?: string } }> };
    // Its date is still ahead; it is over all the same.
    expect(after.items.find((r) => r.share.id === share.id)?.share.state).toBe('exhausted');

    const revoke = await api.delete(`/api/files/share/${share.id}`);
    expect(revoke.ok(), `revoke ${revoke.status()}`).toBeTruthy();
    const gone = (await (await api.get('/api/shares')).json()) as { items: Array<{ share: { id: number; state?: string } }> };
    expect(gone.items.find((r) => r.share.id === share.id)?.share.state).toBe('revoked');
  });
});
