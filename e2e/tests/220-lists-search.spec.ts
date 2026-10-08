/**
 * Lists and search answered by the SERVER (filex 0.54, task #207).
 *
 *   - D5: "Filter in this folder…" is the search's own name rule, asked of the
 *     server (POST /api/files/search/match). The box used to match an
 *     accent-stripped substring of its own: "invoice 2026" did not find
 *     `invoice_2026.pdf` there while the search did, and "musteri" found
 *     `müşteri.pdf` there while the search did not.
 *   - D6: a search's narrowing (size, type, hidden names) is a parameter the
 *     server applies BEFORE it cuts its page; the browser used to narrow the
 *     first page of name hits.
 *   - D4: every listing row says `starred` itself (the explorer used to fetch
 *     the first 500 stars and match ids).
 *   - D3: Recent comes in the order the person OPENED things, with
 *     `opened_at`, `total` and `truncated` (it was drawn in the files'
 *     modification order, and cut at 50 without a word).
 *
 * The per-person checks run as an account of this spec's own (lesson: a
 * shared admin's Recent and stars are written by every spec running beside
 * this one).
 */
import { test, expect, type APIRequestContext, type Page } from '@playwright/test';
import { loginAs, apiLogin } from '../helpers/auth';
import { seedLocalStorage, dropStorageByName, newAuthedRequest, findNodeIdByBasename } from '../helpers/seed';

const STORAGE = `e2e-lists207-${Date.now()}`;
const MOUNT = `/tmp/filex-${STORAGE}`;
const SMALL = ['invoice_2026.pdf', 'invoice-final.pdf', 'müşteri.pdf', 'report-small.txt', '.report-hidden.txt'];
const BIG = 'report-big.bin';
const PASSWORD = 'Lists-207-pass!';

let reader: APIRequestContext;

async function openStorage(page: Page) {
  await page.addInitScript(() => localStorage.setItem('filex.tourDone', '1'));
  await loginAs(page);
  await page.goto(`/admin/explore?storage=${encodeURIComponent(STORAGE)}`);
  await page.getByTestId(`sidenav-storage-${STORAGE}`).click();
  await expect(page.getByText('invoice-final.pdf', { exact: true }).first()).toBeVisible();
}

test.describe('Lists and search from the server (#207)', () => {
  test.beforeAll(async ({ request, playwright, baseURL }) => {
    await dropStorageByName(request, STORAGE);
    await seedLocalStorage(request, STORAGE, MOUNT);
    await apiLogin(request);
    const upload = async (name: string, body: Buffer) => {
      const up = await request.post('/api/files/manager?action=upload', {
        multipart: { path: `${STORAGE}://`, 'file[]': { name, mimeType: 'application/octet-stream', buffer: body } },
      });
      if (!up.ok()) throw new Error(`upload ${name} failed: ${up.status()} ${await up.text()}`);
    };
    for (const name of SMALL) await upload(name, Buffer.from(`${name}\n`));
    await upload(BIG, Buffer.alloc(2 * 1024 * 1024, 7));

    const email = `lists207-${Date.now()}@example.test`;
    const made = await request.post('/api/admin/users', { data: { email, password: PASSWORD, role: 'admin' } });
    expect(made.ok(), `user: ${made.status()} ${await made.text()}`).toBe(true);
    reader = await newAuthedRequest(playwright, baseURL ?? '', email, PASSWORD);
  });

  test.afterAll(async ({ request }) => {
    await reader?.dispose();
    await dropStorageByName(request, STORAGE);
  });

  test('"Filter in this folder" answers by the search\'s rule: separators are one, accents count', async ({ page }) => {
    await openStorage(page);
    const find = page.getByTestId('filter-find');
    await find.fill('invoice 2026');
    await expect(page.getByText('invoice-final.pdf', { exact: true })).toHaveCount(0);
    await expect(page.getByText('invoice_2026.pdf', { exact: true }).first()).toBeVisible();

    await find.fill('musteri');
    await expect(page.getByText('müşteri.pdf', { exact: true }), 'accents count, as in the search').toHaveCount(0);
    await find.fill('müşteri');
    await expect(page.getByText('müşteri.pdf', { exact: true }).first()).toBeVisible();
  });

  test('the server asked: POST /api/files/search/match', async ({ request }) => {
    await apiLogin(request);
    const res = await request.post('/api/files/search/match', {
      data: { q: 'invoice 2026', names: ['invoice_2026.pdf', 'invoice-final.pdf'] },
    });
    expect(res.ok()).toBe(true);
    expect((await res.json()).matches).toEqual([0]);
  });

  test('a search narrows BEFORE it cuts: size, type and hidden names are parameters', async ({ request }) => {
    await apiLogin(request);
    const base = `/api/files/manager?action=search&path=${encodeURIComponent(`${STORAGE}://`)}&filter=report`;
    const big = await (await request.get(`${base}&min_size=1048576`)).json();
    expect(big.files.map((f: { basename: string }) => f.basename)).toEqual([BIG]);

    const visible = await (await request.get(`${base}&hidden=false`)).json();
    const names = visible.files.map((f: { basename: string }) => f.basename);
    expect(names).toContain('report-small.txt');
    expect(names).not.toContain('.report-hidden.txt');

    const bad = await request.get(`${base}&min_size=lots`);
    expect(bad.status(), 'a filter the server cannot read is refused, not ignored').toBe(400);

    const files = await (await request.get(`/api/files/search?q=report&type=file&limit=1&min_size=1048576`)).json();
    expect(files.results).toHaveLength(1);
    expect(files.results[0].name).toBe(BIG);
    expect(typeof files.results[0].score).toBe('number');
    expect(typeof files.total).toBe('number');
  });

  test('a listed row says whether it is starred', async () => {
    const id = await findNodeIdByBasename(reader, `${STORAGE}://`, 'invoice_2026.pdf');
    expect(id).not.toBeNull();
    const star = await reader.post('/api/files/manager/star', { data: { node_id: id, starred: true } });
    expect(star.ok()).toBe(true);
    const listing = await (await reader.get(`/api/files/manager?action=index&path=${encodeURIComponent(`${STORAGE}://`)}`)).json();
    for (const f of listing.files as Array<{ basename: string; starred?: boolean }>) {
      expect(f.starred === true, f.basename).toBe(f.basename === 'invoice_2026.pdf');
    }
  });

  test('Recent is in the order things were opened, and says how many there are', async () => {
    const first = await findNodeIdByBasename(reader, `${STORAGE}://`, 'invoice-final.pdf');
    const second = await findNodeIdByBasename(reader, `${STORAGE}://`, 'report-small.txt');
    expect((await reader.post('/api/files/manager/recent', { data: { node_id: first } })).ok()).toBe(true);
    await new Promise((r) => setTimeout(r, 1100)); // the opening time has second precision
    expect((await reader.post('/api/files/manager/recent', { data: { node_id: second } })).ok()).toBe(true);

    const page = await (await reader.get('/api/files/manager/recent?limit=1')).json();
    expect(page.nodes).toHaveLength(1);
    expect(page.nodes[0].name).toBe('report-small.txt');
    expect(page.nodes[0].opened_at).toBeGreaterThan(0);
    expect(page.total).toBe(2);
    expect(page.truncated).toBe(true);

    const rest = await (await reader.get('/api/files/manager/recent?limit=1&offset=1')).json();
    expect(rest.nodes[0].name).toBe('invoice-final.pdf');
  });

  test('a folder lists folders first, then by name, whichever path answers', async ({ request }) => {
    await apiLogin(request);
    const mk = await request.post('/api/files/manager?action=newfolder', {
      data: { path: `${STORAGE}://`, name: 'zz-folder' },
    });
    expect(mk.ok(), await mk.text()).toBe(true);
    const listing = await (await request.get(`/api/files/manager?action=index&path=${encodeURIComponent(`${STORAGE}://`)}`)).json();
    const kinds = (listing.files as Array<{ type: string }>).map((f) => f.type);
    expect(kinds[0], 'the folder comes first though it sorts last by name').toBe('dir');
  });
});
