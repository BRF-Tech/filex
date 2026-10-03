/**
 * 195 - office thumbnails through OnlyOffice, and why a file has none (0.50).
 *
 * Office documents are drawn by the OnlyOffice document server filex is
 * configured with and by nothing else (docs/thumbnails.md → Office through
 * OnlyOffice). A file whose thumbnail will not come because of the file itself
 * keeps its type icon with a marker that says why (→ Why a file has no
 * thumbnail). Held here, against a real server:
 *
 *   with a document server (capabilities external.onlyoffice.state "ok"):
 *     1. a docx, an xlsx and a pptx put on the storage's disk and synced get a
 *        picture drawn by OnlyOffice ("Who drew the thumbnails": onlyoffice);
 *     2. a password-protected docx is marked Encrypted, a document over the
 *        office size limit Too large (never sent), ciphertext under a .docx
 *        name Encrypted; the repair tab says why in words;
 *     3. the list, the grid and the gallery draw the markers on the type
 *        icon; a resting pointer and a tap show the sentence, and the tap
 *        neither selects nor opens the file;
 *   without one (any run where it is not configured):
 *     4. an office document gets no picture and the repair tab says
 *        "OnlyOffice is not configured"; About says so too; ciphertext under
 *        an office name is still marked Encrypted.
 *
 * ⚠ The document server is the harness's: start filex with
 * FILEX_ONLYOFFICE_URL / FILEX_ONLYOFFICE_JWT (and FILEX_ONLYOFFICE_CALLBACK_URL
 * when the document server reaches filex at another address). It was written
 * against onlyoffice/documentserver (Docs 9.4) with JWT_ENABLED=true and the
 * same secret.
 */
import { test, expect, type APIRequestContext, type Page } from '@playwright/test';
import fs from 'node:fs';
import path from 'node:path';
import crypto from 'node:crypto';
import { fileURLToPath } from 'node:url';
import { apiLogin, loginAs } from '../helpers/auth';
import { dropStorageByName, seedLocalStorage, storageRoot } from '../helpers/seed';

const STAMP = Date.now();
const NAME = `e2e-oothumb-${STAMP}`;
const MOUNT = `/tmp/filex-${NAME}`;
const FIXTURES = path.join(path.dirname(fileURLToPath(import.meta.url)), '../fixtures/file-types');

const DRAWN = ['letter.docx', 'report.xlsx', 'slides.pptx'];
const MARKED: Record<string, string> = {
  'parolali.docx': 'encrypted',
  'buyuk.pptx': 'too_large',
  'gizli.docx': 'encrypted',
};

/** The document server is configured and answers (the capabilities probe). */
async function documentServer(request: APIRequestContext): Promise<boolean> {
  const res = await request.get('/api/files/capabilities');
  if (!res.ok()) return false;
  const caps = (await res.json()) as { external?: Record<string, { state?: string }> };
  return caps.external?.onlyoffice?.state === 'ok';
}

/** Starts a full scan and waits until a run newer than the ones before it is over (as 180 does). */
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

interface Row {
  basename: string;
  thumb_url?: string;
  thumb_note?: string;
}

async function listing(request: APIRequestContext): Promise<Row[]> {
  const res = await request.get(`/api/files/manager?action=index&path=${encodeURIComponent(NAME + '://')}`);
  expect(res.ok()).toBeTruthy();
  return ((await res.json()) as { files?: Row[] }).files ?? [];
}

/** Lists until every name has a picture or a note (the listing hands what is missing to the refresher). */
async function settle(request: APIRequestContext, names: string[]): Promise<Map<string, Row>> {
  let rows = new Map<string, Row>();
  await expect
    .poll(
      async () => {
        rows = new Map((await listing(request)).map((r) => [r.basename, r]));
        return names.filter((n) => !rows.get(n)?.thumb_url && !rows.get(n)?.thumb_note);
      },
      { timeout: 150_000, intervals: [2_000] },
    )
    .toEqual([]);
  return rows;
}

function writeFixtures(root: string) {
  fs.mkdirSync(root, { recursive: true });
  for (const f of DRAWN) fs.copyFileSync(path.join(FIXTURES, f), path.join(root, f));
  // An office document protected by a password (the document server: -5).
  fs.copyFileSync(path.join(FIXTURES, 'password.docx'), path.join(root, 'parolali.docx'));
  // Over the office size limit this spec sets (1 MB): never sent.
  fs.writeFileSync(path.join(root, 'buyuk.pptx'), crypto.randomBytes(1_200_000));
  // filex's end-to-end ciphertext under an office name.
  fs.writeFileSync(path.join(root, 'gizli.docx'), Buffer.concat([Buffer.from('filexe2e'), crypto.randomBytes(256)]));
}

async function openList(page: Page, view: 'list' | 'grid' | 'gallery') {
  await page.goto(`/admin/explore?storage=${encodeURIComponent(NAME)}`);
  await page.getByTestId(`view-${view}`).first().click();
}

test.describe('Office thumbnails through OnlyOffice', () => {
  test.describe.configure({ timeout: 240_000 });
  let storageId = 0;
  let withDS = false;
  let savedMaxMB = 25;

  test.beforeAll(async ({ request }) => {
    await apiLogin(request);
    withDS = await documentServer(request);
    await dropStorageByName(request, NAME);
    writeFixtures(storageRoot(MOUNT));
    const st = await seedLocalStorage(request, NAME, MOUNT, { sync_mode: 'ondemand' });
    storageId = st.id;
    const cur = await request.get('/api/admin/tools/thumbnails/settings');
    savedMaxMB = ((await cur.json()) as { office_max_mb?: number }).office_max_mb ?? 25;
    const set = await request.patch('/api/admin/tools/thumbnails/settings', { data: { office_max_mb: 1 } });
    expect(set.ok(), await set.text()).toBeTruthy();
    await syncNow(request, storageId);
  });

  test.afterAll(async ({ request }) => {
    await apiLogin(request);
    await request.patch('/api/admin/tools/thumbnails/settings', { data: { office_max_mb: savedMaxMB } });
    await dropStorageByName(request, NAME);
  });

  // The API calls below go out with the session cookie this sets.
  test.beforeEach(async ({ request }) => {
    await apiLogin(request);
  });

  test('OnlyOffice draws the pages, and each file without one says why', async ({ request }) => {
    test.skip(!withDS, 'no OnlyOffice document server configured and reachable here (capabilities external.onlyoffice.state)');
    const rows = await settle(request, [...DRAWN, ...Object.keys(MARKED)]);
    for (const f of DRAWN) {
      expect(rows.get(f)?.thumb_url, `${f} has a picture`).toBeTruthy();
      expect(rows.get(f)?.thumb_note, `${f} has no marker`).toBeUndefined();
      const img = await request.get(rows.get(f)!.thumb_url!);
      expect(img.ok()).toBeTruthy();
      expect(img.headers()['content-type']).toContain('image/jpeg');
    }
    for (const [f, note] of Object.entries(MARKED)) {
      expect(rows.get(f)?.thumb_url, `${f} has no picture`).toBeFalsy();
      expect(rows.get(f)?.thumb_note, f).toBe(note);
    }

    const gens = (await (await request.get('/api/admin/tools/thumbnails/generators')).json()) as {
      generators?: Array<{ generator: string; count: number }>;
    };
    expect(gens.generators?.find((g) => g.generator === 'onlyoffice')?.count ?? 0).toBeGreaterThanOrEqual(DRAWN.length);

    const problems = (await (await request.get('/api/admin/tools/thumbnails/problems')).json()) as {
      items?: Array<{ name: string; code: string; limit?: number; note?: string }>;
    };
    const byName = new Map((problems.items ?? []).map((p) => [p.name, p]));
    expect(byName.get('parolali.docx')?.code).toBe('oo_password');
    expect(byName.get('parolali.docx')?.note).toBe('encrypted');
    expect(byName.get('buyuk.pptx')?.code).toBe('oo_too_large');
    expect(byName.get('buyuk.pptx')?.limit).toBe(1 << 20);
  });

  test('the markers sit on the type icon in every view, and say why', async ({ page, request }) => {
    test.skip(!withDS, 'no OnlyOffice document server configured and reachable here (capabilities external.onlyoffice.state)');
    await settle(request, [...DRAWN, ...Object.keys(MARKED)]);
    // ⚠ No product tour: its first card opens over the grid a few seconds
    // into the explorer and took the tap meant for the marker (0.50 full
    // round with a document server, all three engines).
    await page.addInitScript(() => localStorage.setItem('filex.tourDone', '1'));
    await loginAs(page);
    for (const view of ['list', 'grid', 'gallery'] as const) {
      await openList(page, view);
      const marks = page.getByTestId('thumb-note');
      await expect(marks).toHaveCount(Object.keys(MARKED).length);
      const tooLarge = page.locator('[data-testid="thumb-note"][data-note="too_large"]').first();
      await expect(tooLarge).toHaveAttribute('aria-label', /^Too large\. No thumbnail: /);
      // A resting pointer shows the sentence…
      await tooLarge.hover();
      await expect(page.getByTestId('thumb-note-tip')).toHaveText(/^Too large\. No thumbnail: /);
      await page.mouse.move(2, 2);
      await expect(page.getByTestId('thumb-note-tip')).toHaveCount(0);
    }
    // …and so does a tap, which neither selects nor opens the file.
    await openList(page, 'grid');
    const enc = page.locator('[data-testid="thumb-note"][data-note="encrypted"]').first();
    const at = page.url();
    await enc.click();
    await expect(page.getByTestId('thumb-note-tip')).toHaveText(/^Encrypted\. No thumbnail: /);
    await expect(page.locator('.fe-grid__card[aria-selected="true"]'), 'the tap did not select the card').toHaveCount(0);
    await expect(page.getByTestId('viewer-close'), 'nor open it').toHaveCount(0);
    expect(page.url()).toBe(at);
  });

  test('without OnlyOffice an office document says so, and ciphertext is still Encrypted', async ({ page, request }) => {
    test.skip(withDS, 'an OnlyOffice document server is configured here; the run without one measures this');
    // The listing hands each office document to the refresher, which
    // records why it drew nothing; the repair tab reads that back.
    type Problem = { name: string; code: string; tool?: string };
    let problems: Problem[] = [];
    await expect
      .poll(
        async () => {
          await listing(request);
          const res = await request.get('/api/admin/tools/thumbnails/problems');
          problems = ((await res.json()) as { items?: Problem[] }).items ?? [];
          return problems.find((p) => p.name === 'letter.docx')?.code ?? 'pending';
        },
        { timeout: 120_000, intervals: [2_000] },
      )
      .toBe('no_tool');
    expect(problems.find((p) => p.name === 'letter.docx')?.tool).toBe('office');
    const rows = await settle(request, ['gizli.docx']);
    expect(rows.get('gizli.docx')?.thumb_note, 'ciphertext is the file\'s reason').toBe('encrypted');
    const letter = (await listing(request)).find((r) => r.basename === 'letter.docx');
    expect(letter?.thumb_url).toBeFalsy();
    expect(letter?.thumb_note, 'not the file\'s reason: no marker').toBeUndefined();

    await loginAs(page);
    await page.goto('/admin/about');
    await expect(page.getByTestId('about-tool-Office')).toHaveText('Not found');
    await expect(page.getByTestId('about-office-hint')).toContainText('External services');
  });
});
