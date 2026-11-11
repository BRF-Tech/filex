/**
 * 224 — the rules the clients used to keep copies of come from the server
 * (filex #211, the 0.54 audit's B2, B4, B12, B16, B18, B19, B20 and A11).
 *
 * Measured against a real binary:
 *
 *   - `/api/files/capabilities` publishes how each kind of file is edited
 *     (`edit_kinds`: `.docm` is an office document, `.properties` and
 *     `.graphql` are text), the input limits (`limits`) and the release
 *     apart from the commit and the build time;
 *   - `?action=newfile` with `dry_run` says whether a name is free without
 *     writing anything, and offers the server's own " (2)" name when it is
 *     not - on a case-sensitive local disk "REPORT.md" is free beside
 *     "Report.md", which the dialog used to refuse (a test of its own, run
 *     where the disk under the storage is case-sensitive: caseInsensitiveDisk);
 *   - the New document dialog prefills that free name (`Untitled (2).txt`)
 *     when `Untitled.txt` is already in the folder;
 *   - the Add user form's suggestion is the server's identity rule
 *     (`gözlük@…` → `gozluk`, which the page's own copy spelled `g.zl.k`).
 */
import { mkdtempSync, readdirSync, rmSync, writeFileSync } from 'node:fs';
import path from 'node:path';
import { test, expect, type APIRequestContext, type Page } from '@playwright/test';
import { loginAs } from '../helpers/auth';
import { dropStorageByName, newAuthedRequest, seedLocalStorage, storageRoot } from '../helpers/seed';

const STORE = `e2e-rules-${Date.now()}`;
const MOUNT = `/tmp/filex-${STORE}`;

/**
 * Why the disk this spec's storage lives on cannot answer the case question;
 * '' when it can. The probe does what the test asks the server about: it
 * writes Report.md and REPORT.md side by side, in a folder of its own beside
 * the storage (the local profile runs the server on this machine, on this
 * disk), and counts what is there.
 *
 * ⚠ A case-insensitive disk (NTFS on Windows, APFS by default on a Mac) holds
 * ONE file under both names, so there the server is right to call REPORT.md
 * taken: that is a missing prerequisite, not a failure (2026-11-11, #320 -
 * red on Windows with main's own binary as well).
 */
function caseInsensitiveDisk(): string {
  const dir = mkdtempSync(path.join(path.dirname(storageRoot(MOUNT)), 'filex-case-probe-'));
  try {
    writeFileSync(path.join(dir, 'Report.md'), 'a');
    writeFileSync(path.join(dir, 'REPORT.md'), 'b');
    const held = readdirSync(dir);
    if (held.length === 2) return '';
    return `the disk under ${dir} is case-insensitive: Report.md and REPORT.md are one file there (${held.join(', ')}); run this on a case-sensitive disk (Linux) to measure it`;
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
}

async function newFile(api: APIRequestContext, name: string, type: string, dryRun = false) {
  const r = await api.post('/api/files/manager?action=newfile', {
    data: { path: `${STORE}://`, name, type, exact_name: true, ...(dryRun ? { dry_run: true } : {}) },
  });
  return { status: r.status(), body: (await r.json()) as Record<string, unknown> };
}

async function names(api: APIRequestContext): Promise<string[]> {
  const r = await api.get(`/api/files/manager?action=index&path=${encodeURIComponent(`${STORE}://`)}`);
  expect(r.ok(), `index: ${r.status()}`).toBe(true);
  const body = (await r.json()) as { files: Array<{ basename: string }> };
  return body.files.map((f) => f.basename);
}

async function ensureNav(page: Page) {
  const storage = page.getByTestId(`sidenav-storage-${STORE}`);
  if (await storage.isVisible().catch(() => false)) return;
  await page.getByTestId('toolbar-nav').click();
  await expect(storage).toBeVisible();
}

test.describe('The server’s rules, published and applied (#211)', () => {
  let api: APIRequestContext;

  test.beforeAll(async ({ request, playwright, baseURL }) => {
    await dropStorageByName(request, STORE);
    await seedLocalStorage(request, STORE, MOUNT);
    api = await newAuthedRequest(playwright, baseURL ?? '');
  });

  test.afterAll(async ({ request }) => {
    await api?.dispose();
    await dropStorageByName(request, STORE);
  });

  test('the capabilities carry the edit kinds, the limits and the release apart', async () => {
    const caps = (await (await api.get('/api/files/capabilities')).json()) as Record<string, any>;
    expect(caps.edit_kinds.office).toContain('docx');
    expect(caps.edit_kinds.office, 'the explorer did not count .docm as office').toContain('docm');
    expect(caps.edit_kinds.office, 'filex opens a .txt as text').not.toContain('txt');
    expect(caps.edit_kinds.text).toContain('properties');
    expect(caps.edit_kinds.text).toContain('graphql');
    expect(caps.edit_kinds.text_names).toContain('makefile');
    expect(caps.limits.tag_max_runes).toBe(64);
    expect(caps.limits.comment_max_runes).toBe(5000);
    expect(caps.limits.app_state_max_bytes).toBe(16 * 1024);
    expect(typeof caps.release).toBe('string');
    expect(caps.release).not.toBe('');
    expect(String(caps.version).startsWith(caps.release), 'version still starts with the release').toBe(true);
  });

  test('a dry run says whether a name is free, and which one is, without writing', async () => {
    const made = await newFile(api, 'Report.md', 'md');
    expect(made.status, JSON.stringify(made.body)).toBe(200);

    const taken = await newFile(api, 'Report.md', 'md', true);
    expect(taken.status).toBe(200);
    expect(taken.body).toMatchObject({ dry_run: true, taken: true, code: 'NAME_TAKEN', suggested: 'Report (2).md' });

    const free = await newFile(api, 'fresh.md', 'md', true);
    expect(free.body).toMatchObject({ dry_run: true, taken: false, name: 'fresh.md' });
    expect(await names(api), 'a dry run writes nothing').not.toContain('fresh.md');
  });

  test('on a case-sensitive disk, REPORT.md is free beside Report.md', async () => {
    const insensitive = caseInsensitiveDisk();
    if (insensitive) console.warn(`skipped: ${insensitive}`);
    test.skip(insensitive !== '', insensitive);
    const made = await newFile(api, 'Notes.md', 'md');
    expect(made.status, JSON.stringify(made.body)).toBe(200);
    const other = await newFile(api, 'NOTES.md', 'md', true);
    expect(other.body.taken, 'a case-sensitive disk holds NOTES.md beside Notes.md').toBe(false);
  });

  test('the New document dialog offers the server’s free name', async ({ page }) => {
    const made = await newFile(api, 'Untitled.txt', 'txt');
    expect(made.status, JSON.stringify(made.body)).toBe(200);

    await page.addInitScript(() => localStorage.setItem('filex.tourDone', '1'));
    await loginAs(page);
    await page.goto(`/admin/explore?storage=${encodeURIComponent(STORE)}`);
    await expect(page.getByTestId('toolbar-nav')).toBeVisible();
    await ensureNav(page);
    await page.getByTestId(`sidenav-storage-${STORE}`).click();
    await ensureNav(page);
    await page.getByTestId('sidenav-new').click();
    await page.locator('.fe-ctx__item', { hasText: 'New document' }).click();
    await expect(page.getByTestId('newdoc-modal')).toBeVisible();
    await page.getByTestId('newdoc-type-txt').click();
    await expect(page.getByTestId('newdoc-name')).toHaveValue('Untitled (2).txt');

    // Typing the taken name back says so, from the server's answer.
    const input = page.getByTestId('newdoc-name');
    await input.click();
    await page.keyboard.press('ControlOrMeta+a');
    await page.keyboard.type('Untitled.txt');
    await expect(page.getByTestId('newdoc-collision')).toBeVisible();
    await expect(page.getByTestId('newdoc-create')).toBeDisabled();
  });

  test('the Add user suggestion is the server’s identity rule', async () => {
    const r = await api.get(`/api/admin/users/suggest?email=${encodeURIComponent('gözlük@corp.example')}`);
    expect(r.ok(), `suggest: ${r.status()}`).toBe(true);
    expect(await r.json()).toMatchObject({ username: 'gozluk', name: 'Gözlük' });
  });
});
