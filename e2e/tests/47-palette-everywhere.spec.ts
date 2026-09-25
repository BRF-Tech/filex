/**
 * ⌘K "Everywhere": a hit is something to act on, and only for who may see it
 * — task #47, measured in a real browser against a real server.
 *
 * Before #47 a palette hit had ONE verb: open (navigate to its folder, open
 * the file). Downloading a contract found by a phrase inside it meant opening
 * its folder first; dragging it onto the desktop meant finding it again in the
 * listing. Both verbs exist on a listing row; the hit row now has them through
 * the same code (`downloadSelection`, `handDragOut`).
 *
 * The acceptance the task set, and where each is measured:
 *   · same query, same answer — the palette draws exactly the server's
 *     `/api/files/search` answer for that person, in its order (the desktop
 *     app's twin of this check is desktop/scripts/search-e2e.mjs);
 *   · permission-aware — a user with no grant on a folder never sees a file in
 *     it, by name or by content, in the palette or from the API (NEGATIVE);
 *   · download and drag-out from the hit row, the bytes checked end to end.
 */
import { test, expect, type Download, type Page } from '@playwright/test';
import { loginAs, apiLogin } from '../helpers/auth';
import { seedLocalStorage, dropStorageByName } from '../helpers/seed';

const STORAGE = `e2e-palette47-${Date.now()}`;
const MOUNT = `/tmp/filex-${STORAGE}`;
// One made-up word, in the CONTENT of two files and the NAME of one folder —
// nothing else on the server carries it, so every hit is one of ours.
const WORD = 'karakavak47';
const SECRET = { dir: 'Hukuk', name: 'sözleşme-47.txt', body: `Ceza şartı: ${WORD} maddesi uyarınca.\n` };
const SHARED = { dir: 'Paylasim', name: 'ortak-47.txt', body: `Ortak not — ${WORD} burada da geçiyor.\n` };
const FOLDER = { dir: 'Arsiv', name: `${WORD}-dosyalar` };
// What the administrator's search answers: the folder by NAME, the file inside
// it by PATH, the two files by CONTENT.
const ADMIN_HITS = 4;
// A plain file at the drive's root, without the word: the LISTING row the
// palette's drag rule is compared with.
const PLAIN = { name: 'duz-47.txt', body: 'sıradan bir dosya\n' };
const OUTSIDER = { email: 'palette47-outsider@example.test', password: 'palette47-outsider-pw-2026', name: 'Dışarıdaki Okur' };

type Hit = { name?: string; path?: string; storage?: string; type?: string };

async function upload(request: import('@playwright/test').APIRequestContext, dir: string, name: string, body: string) {
  const up = await request.post('/api/files/manager?action=upload', {
    multipart: { path: `${STORAGE}://${dir}`, 'file[]': { name, mimeType: 'text/plain', buffer: Buffer.from(body, 'utf8') } },
  });
  if (!up.ok()) throw new Error(`upload ${name}: ${up.status()} ${await up.text()}`);
}

async function newFolder(request: import('@playwright/test').APIRequestContext, parent: string, name: string) {
  const mk = await request.post('/api/files/manager?action=newfolder', { data: { path: `${STORAGE}://${parent}`, name } });
  if (!mk.ok()) throw new Error(`newfolder ${parent}/${name}: ${mk.status()} ${await mk.text()}`);
}

/** The server's own answer — the exact call the palette makes. */
async function serverHits(page: Page): Promise<Hit[]> {
  const res = await page.request.get(`/api/files/search?q=${encodeURIComponent(WORD)}&limit=8&scope=all`);
  expect(res.ok(), `search: ${res.status()}`).toBe(true);
  const body = (await res.json()) as { results?: Hit[] };
  return (body.results ?? []).filter((h) => h.storage === STORAGE);
}

async function openExplorer(page: Page, admin: boolean) {
  await page.addInitScript(() => localStorage.setItem('filex.tourDone', '1'));
  if (admin) {
    await loginAs(page);
    await page.goto(`/admin/explore?storage=${encodeURIComponent(STORAGE)}`);
    await page.getByTestId(`sidenav-storage-${STORAGE}`).click();
  } else {
    await page.goto('/admin/login');
    await page.getByLabel(/e-?mail|kullanıcı adı/i).fill(OUTSIDER.email);
    await page.getByLabel(/password|şifre|parola/i).fill(OUTSIDER.password);
    await page
      .getByRole('button', { name: 'Sign in', exact: true })
      .or(page.getByRole('button', { name: 'Giriş yap', exact: true }))
      .or(page.getByRole('button', { name: 'Oturum aç', exact: true }))
      .first()
      .click();
    await page.waitForURL(/\/drive\//);
    await page.goto(`/drive/explore?storage=${encodeURIComponent(STORAGE)}`);
  }
  await expect(page.locator('[data-fe-path]').first()).toBeVisible();
}

/** ⌘K, the word, and the "Everywhere" rows once they have arrived. */
async function paletteRows(page: Page, expected: number) {
  await page.keyboard.press('Control+k');
  const input = page.locator('.fe-cmdp__input');
  await expect(input).toBeVisible();
  await input.fill(WORD);
  const rows = page.locator('.fe-cmdp__item--hit');
  await expect(rows).toHaveCount(expected, { timeout: 10_000 });
  return rows;
}

/** crumb + name of every drawn hit row, in drawn order. */
async function drawn(page: Page): Promise<string[]> {
  return page.locator('.fe-cmdp__item--hit').evaluateAll((els) =>
    els.map((el) => {
      const crumb = el.querySelector('.fe-cmdp__crumb')?.textContent?.trim() ?? '';
      const name = el.querySelector('.fe-cmdp__label')?.textContent?.trim() ?? '';
      return `${crumb}/${name}`;
    }),
  );
}

/** How the server's answer should read on screen: `storage/parent/name`. */
function expectedRow(h: Hit): string {
  const rel = String(h.path ?? '').replace(/^\/+|\/+$/g, '');
  const parent = rel.includes('/') ? rel.slice(0, rel.lastIndexOf('/')) : '';
  return `${parent ? `${h.storage}/${parent}` : h.storage}/${h.name}`;
}

/** What a dragstart on `el` puts on the drag as the browser's own download. */
async function downloadUrlOf(locator: import('@playwright/test').Locator): Promise<string> {
  return locator.evaluate((el) => {
    const dt = new DataTransfer();
    el.dispatchEvent(new DragEvent('dragstart', { bubbles: true, cancelable: true, dataTransfer: dt }));
    return dt.getData('DownloadURL');
  });
}

/** The listing row of the plain file, with the palette closed. */
async function plainRowDownloadUrl(page: Page): Promise<string> {
  await page.keyboard.press('Escape');
  await expect(page.locator('.fe-cmdp')).toHaveCount(0);
  const row = page.locator(`[data-fe-path="${STORAGE}://${PLAIN.name}"]`).first();
  await expect(row).toBeVisible();
  return downloadUrlOf(row);
}

/**
 * The next download, wherever it starts: a single file opens its body in a
 * new tab (`window.open`), a folder navigates a hidden frame on this page.
 */
function nextDownload(page: Page): Promise<Download> {
  return new Promise((resolve) => {
    page.once('download', resolve);
    page.context().once('page', (p) => p.once('download', resolve));
  });
}

async function readDownload(dl: Download): Promise<Buffer> {
  const file = await dl.path();
  const fs = await import('node:fs');
  return fs.readFileSync(file!);
}

test.describe('⌘K Everywhere — hit verbs and permissions (#47)', () => {
  test.describe.configure({ mode: 'serial' });

  test.beforeAll(async ({ request }) => {
    await dropStorageByName(request, STORAGE);
    await seedLocalStorage(request, STORAGE, MOUNT, { rbac_enabled: true });
    await apiLogin(request);
    for (const d of [SECRET.dir, SHARED.dir, FOLDER.dir]) await newFolder(request, '', d);
    await newFolder(request, FOLDER.dir, FOLDER.name);
    await upload(request, SECRET.dir, SECRET.name, SECRET.body);
    await upload(request, SHARED.dir, SHARED.name, SHARED.body);
    // Not empty: the server answers an archive of nothing with 409, not a zip.
    await upload(request, `${FOLDER.dir}/${FOLDER.name}`, 'ic.txt', 'klasörün içindeki dosya\n');
    await upload(request, '', PLAIN.name, PLAIN.body);

    await request.post('/api/admin/users', {
      data: { email: OUTSIDER.email, password: OUTSIDER.password, display_name: OUTSIDER.name, role: 'user' },
    });
    const users = (await (await request.get('/api/admin/users')).json()) as
      | { id: number; email: string }[]
      | { users?: { id: number; email: string }[]; items?: { id: number; email: string }[] };
    const list = Array.isArray(users) ? users : (users.users ?? users.items ?? []);
    const outsider = list.find((u) => u.email === OUTSIDER.email);
    if (!outsider) throw new Error('the outsider account was not created');
    // A grant on ONE folder: Paylasim. Not Hukuk, not Arsiv.
    const g = await request.post('/api/files/permissions', {
      data: { path: `${STORAGE}://${SHARED.dir}`, user_id: outsider.id, level: 'viewer', is_dir: true },
    });
    if (!g.ok()) throw new Error(`grant: ${g.status()} ${await g.text()}`);

    // Content is extracted in the background; wait until the index has it.
    await expect
      .poll(
        async () => {
          const r = await request.get(`/api/files/search?q=${encodeURIComponent(WORD)}&limit=8&scope=all`);
          const b = (await r.json()) as { results?: Hit[] };
          return (b.results ?? []).filter((h) => h.storage === STORAGE).length;
        },
        { timeout: 30_000, message: 'the index never picked the four hits up' },
      )
      .toBe(ADMIN_HITS);
  });

  test.afterAll(async ({ request }) => {
    await dropStorageByName(request, STORAGE);
  });

  test('the palette draws exactly the server’s answer, in its order', async ({ page }) => {
    await openExplorer(page, true);
    const want = (await serverHits(page)).map(expectedRow);
    expect(want).toHaveLength(ADMIN_HITS);
    await paletteRows(page, want.length);
    const got = await drawn(page);
    test.info().annotations.push({ type: 'palette rows (browser, admin)', description: JSON.stringify(got) });
    await page.screenshot({ path: test.info().outputPath('palette-web.png') });
    expect(got).toEqual(want);
  });

  test('a file hit downloads straight from the row, byte for byte', async ({ page }) => {
    await openExplorer(page, true);
    const rows = await paletteRows(page, ADMIN_HITS);
    const row = rows.filter({ hasText: SECRET.name });
    const got = nextDownload(page);
    await row.hover();
    await row.getByTestId('palette-hit-download').click();
    const dl = await got;
    expect(dl.suggestedFilename()).toBe(SECRET.name);
    expect((await readDownload(dl)).toString('utf8')).toBe(SECRET.body);
    // Not an open: the palette is still up, nothing navigated.
    await expect(page.locator('.fe-cmdp')).toBeVisible();
  });

  test('a folder hit downloads as one archive', async ({ page }) => {
    await openExplorer(page, true);
    const rows = await paletteRows(page, ADMIN_HITS);
    const row = rows.filter({ has: page.locator('.fe-cmdp__label', { hasText: FOLDER.name }) });
    const got = nextDownload(page);
    await row.hover();
    await row.getByTestId('palette-hit-download').click();
    const dl = await got;
    expect(dl.suggestedFilename()).toMatch(new RegExp(`^${FOLDER.name}.*\\.zip$`));
    const zip = await readDownload(dl);
    expect(zip.subarray(0, 2).toString('latin1'), 'a zip').toBe('PK');
    expect(zip.includes(Buffer.from('ic.txt')), 'the folder’s file is inside').toBe(true);
  });

  test('bearer session (the admin SPA): a file hit drags out, a folder does not — like a listing row', async ({ page }) => {
    // The admin SPA signs its calls with a bearer (web/src/lib/explorerConfig
    // explorerAuth), which the browser's download stack cannot carry. Until
    // #71 that meant no drag-out at all here; since #71 a file's drag rides a
    // short-lived link the server mints while the pointer rests on the row
    // (lib/dragOut createDragLinks). The drag itself — hover, press, the link
    // on the dataTransfer, the bytes behind it — is measured with a real mouse
    // in 71-admin-drag-out-link.spec.ts; here, only that the hit offers it.
    await openExplorer(page, true);
    const rows = await paletteRows(page, ADMIN_HITS);
    await expect(rows.filter({ hasText: SECRET.name })).toHaveAttribute('draggable', 'true');
    // Download is not a drag: it is still offered.
    await expect(rows.filter({ hasText: SECRET.name }).getByTestId('palette-hit-download')).toHaveCount(1);
    // A folder cannot ride a single-file download, in any session.
    await expect(rows.filter({ has: page.locator('.fe-cmdp__label', { hasText: FOLDER.name }) })).toHaveAttribute('draggable', 'false');
  });

  test('cookie session (an embed): a file hit drags out as the browser’s own download; a folder does not', async ({ page }) => {
    // How an embed such as work.example.com's Files tab runs: the session cookie,
    // no bearer in the page. Same explorer, same palette.
    await page.addInitScript(() => sessionStorage.removeItem('filex.bearer'));
    await openExplorer(page, true);
    const rows = await paletteRows(page, ADMIN_HITS);
    const file = rows.filter({ hasText: SECRET.name });
    await expect(file).toHaveAttribute('draggable', 'true');
    // The browser's own drag-out: `DownloadURL` on the drag, which Chromium
    // downloads into wherever the drop lands (lib/dragOut).
    const payload = await downloadUrlOf(file);
    const m = /^([^:]+):([^:]+):(https?:.*)$/.exec(payload);
    expect(m, `DownloadURL payload: ${payload}`).toBeTruthy();
    expect(m![1]).toMatch(/^text\/plain/);
    expect(m![2]).toBe(SECRET.name);
    // …and that address really is the file, with this session.
    const body = await page.evaluate(async (url) => (await fetch(url, { credentials: 'include' })).text(), m![3]);
    expect(body).toBe(SECRET.body);
    // A folder cannot ride a single-file download: not offered.
    await expect(rows.filter({ has: page.locator('.fe-cmdp__label', { hasText: FOLDER.name }) })).toHaveAttribute('draggable', 'false');
    // The same rule as the listing: its file row carries the same kind of drag.
    expect(await plainRowDownloadUrl(page), 'a listing row under a cookie session').toMatch(new RegExp(`:${PLAIN.name}:https?:`));
  });

  test('a drop let go on the palette is not an upload', async ({ page }) => {
    await openExplorer(page, true);
    await paletteRows(page, ADMIN_HITS);
    const uploads: string[] = [];
    page.on('request', (r) => {
      if (/action=upload|\/upload\/init/.test(r.url())) uploads.push(r.url());
    });
    await page.locator('.fe-cmdp__backdrop').evaluate((el) => {
      const dt = new DataTransfer();
      dt.items.add(new File(['stand-in'], 'sözleşme-47.txt', { type: 'text/plain' }));
      for (const type of ['dragenter', 'dragover', 'drop']) {
        el.dispatchEvent(new DragEvent(type, { bubbles: true, cancelable: true, dataTransfer: dt }));
      }
    });
    await page.waitForTimeout(1500);
    expect(uploads, 'no upload was started').toEqual([]);
    await expect(page.locator('.fe-cmdp')).toBeVisible();
  });

  test('NEGATIVE — a user with no grant on a folder never sees a file in it', async ({ browser }) => {
    const page = await browser.newPage();
    await openExplorer(page, false);
    // The server first: only the file under the granted folder.
    const theirs = await serverHits(page);
    expect(theirs.map((h) => h.name)).toEqual([SHARED.name]);
    // …and the palette draws that and nothing more.
    await paletteRows(page, 1);
    const got = await drawn(page);
    test.info().annotations.push({ type: 'palette rows (browser, outsider)', description: JSON.stringify(got) });
    expect(got).toEqual([`${STORAGE}/${SHARED.dir}/${SHARED.name}`]);
    await expect(page.locator('.fe-cmdp__list')).not.toContainText(SECRET.name);
    await expect(page.locator('.fe-cmdp__list')).not.toContainText(FOLDER.name);
    // The file's own download address answers no for them either.
    const refused = await page.request.get(
      `/api/files/manager?action=download&path=${encodeURIComponent(`${STORAGE}://${SECRET.dir}/${SECRET.name}`)}`,
    );
    expect(refused.status(), 'the outsider cannot fetch the file directly').toBeGreaterThanOrEqual(400);
    await page.close();
  });
});
