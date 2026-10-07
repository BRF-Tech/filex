/**
 * 199-csv-onlyoffice - a .csv opens in ONLYOFFICE (filex 0.51, GitHub #81,
 * docs/ONLYOFFICE.md → CSV files), against a real document server:
 *
 *   with ONLYOFFICE configured (capabilities external.onlyoffice "ok"):
 *     1. a double-click opens a semicolon CSV in ONLYOFFICE's spreadsheet, a
 *        look first (view mode), WITHOUT ONLYOFFICE's "Choose CSV options"
 *        dialog: the config carries UTF-8 and the semicolon;
 *     2. its Edit button opens the editor tab in ONLYOFFICE (`app=onlyoffice`),
 *        which says what a save as CSV keeps; a cell changed there and the
 *        tab closed comes back to the storage as the SAME kind of file -
 *        semicolons, CRLF, no byte order mark - with the new value, every
 *        cell nobody changed as it was (`007`, `05320000001`, `01.02.2026`,
 *        which ONLYOFFICE writes as `7`, `5320000001`, `1/2/2026`; filex 0.52,
 *        GitHub #88), and never as XLSX bytes;
 *     3. "Open with" offers ONLYOFFICE and filex's table; "Choose an app…"
 *        names ONLYOFFICE; "Always use" the table is kept on the account and
 *        followed;
 *     4. Default apps lists .csv with ONLYOFFICE first (app plugins on);
 *     5. a choice of ONLYOFFICE kept on the account, and ONLYOFFICE switched
 *        off: the table opens the file, no error, and an administrator sees
 *        "Open with ONLYOFFICE" greyed with where to set it up;
 *   without one (a run where it is not configured):
 *     6. the table, as before, and the greyed row for an administrator.
 *
 * ⚠ The document server is the harness's: start filex with
 * FILEX_ONLYOFFICE_URL / FILEX_ONLYOFFICE_JWT and FILEX_ONLYOFFICE_CALLBACK_URL
 * when the document server reaches filex at another address (the save comes
 * back through it). Written against onlyoffice/documentserver (Docs 9.4),
 * JWT on.
 */
import { test, expect, type APIRequestContext, type Page } from '@playwright/test';
import { mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import { loginAs } from '../helpers/auth';
import { dropStorageByName, newAuthedRequest, seedLocalStorage, storageRoot } from '../helpers/seed';

const PREFS = '/api/me/prefs?surface=web';
const CONFIG = '/api/files/onlyoffice/config';
// Cells ONLYOFFICE writes another way when nobody touched them: leading
// zeros, a phone number, a date it reads month first in English.
const ORIGINAL =
  'ad;adet;not;kod;tel;tarih\r\n' +
  'elma;3;"a; b";007;05320000001;01.02.2026\r\n' +
  'armut;5;şeker;042;05330000002;15.03.2026\r\n';
const EDITED =
  'ad;adet;not;kod;tel;tarih\r\n' +
  'elma;42;"a; b";007;05320000001;01.02.2026\r\n' +
  'armut;5;şeker;042;05330000002;15.03.2026\r\n';

/** ONLYOFFICE is configured and answering, by the server's own probe. */
async function documentServer(api: APIRequestContext): Promise<boolean> {
  const res = await api.get('/api/files/capabilities');
  if (!res.ok()) return false;
  const caps = (await res.json()) as { external?: Record<string, { state?: string }> };
  return caps.external?.onlyoffice?.state === 'ok';
}

/** Back to how the run started (as 116 leaves it). */
async function restoreOnlyOffice(api: APIRequestContext) {
  const url = process.env.FILEX_ONLYOFFICE_URL ?? '';
  const secret = process.env.FILEX_ONLYOFFICE_JWT ?? '';
  if (url && secret) await api.patch('/api/admin/external/onlyoffice', { data: { enabled: true, url, secret } });
}

test.describe.serial('a .csv opens in ONLYOFFICE', () => {
  let api: APIRequestContext;
  let tag = '';
  let store = '';
  let mount = '';
  let name = '';
  let path = '';
  let prefsBefore: unknown = {};
  let ds = false;

  test.beforeAll(async ({ playwright, baseURL }, info) => {
    tag = info.project.name.replace(/[^a-z]/g, '').slice(0, 8) || 'x';
    store = `e2e-csvoo-${tag}-${Date.now()}`;
    mount = `/tmp/filex-${store}`;
    name = `liste-${tag}.csv`;
    path = `${store}://${name}`;
    api = await newAuthedRequest(playwright, baseURL ?? '');
    const got = await api.get(PREFS);
    prefsBefore = got.ok() ? (await got.json()).prefs : {};
    await api.delete('/api/me/open-with/csv');
    await api.delete('/api/admin/file-types/csv');
    const root = storageRoot(mount);
    mkdirSync(root, { recursive: true });
    writeFileSync(join(root, name), ORIGINAL);
    await seedLocalStorage(api, store, mount);
    ds = await documentServer(api);
  });

  test.afterAll(async () => {
    await restoreOnlyOffice(api).catch(() => undefined);
    await api.delete('/api/me/open-with/csv').catch(() => undefined);
    await api.delete('/api/admin/file-types/csv').catch(() => undefined);
    await dropStorageByName(api, store).catch(() => undefined);
    await api.put(PREFS, { data: { prefs: prefsBefore } }).catch(() => undefined);
    await api.dispose();
  });

  const onDisk = () => readFileSync(join(storageRoot(mount), name), 'utf8');

  async function openFolder(page: Page) {
    await page.addInitScript(() => {
      localStorage.setItem('filex.tourDone', '1');
      localStorage.setItem('filex.installPrompt.dismissed', '1');
    });
    await loginAs(page);
    await page.goto(`/admin/explore?storage=${encodeURIComponent(store)}`);
    const row = page.locator(`[data-fe-path="${path}"]`).first();
    await row.waitFor();
    return row;
  }

  const fileName = (page: Page) => page.locator(`[data-fe-path="${path}"]`).first().getByText(name, { exact: true });

  /** ONLYOFFICE's spreadsheet is drawn: its frame, and its cell name box inside. */
  async function spreadsheetReady(page: Page) {
    await expect(page.locator('iframe[name^="frameEditor"]')).toBeVisible({ timeout: 30_000 });
    const frame = page.frameLocator('iframe[name^="frameEditor"]');
    await expect(frame.locator('#ce-cell-name')).toBeVisible({ timeout: 60_000 });
    return frame;
  }

  test('a double-click opens it in ONLYOFFICE, a look first, without asking for the delimiter', async ({ page }) => {
    test.skip(!ds, 'ONLYOFFICE is not configured on this run (6 covers it)');
    await openFolder(page);
    const configAnswer = page.waitForResponse((r) => r.url().endsWith(CONFIG) && r.request().method() === 'POST');
    await fileName(page).dblclick();
    const res = await configAnswer;
    expect(res.ok(), await res.text()).toBe(true);
    expect(JSON.parse(res.request().postData() ?? '{}')).toMatchObject({ path, mode: 'view' });
    const body = (await res.json()) as { config: { document: { fileType: string; options?: unknown } } };
    expect(body.config.document.fileType).toBe('csv');
    expect(body.config.document.options, 'UTF-8 and the semicolon go along').toEqual({ codePage: 65001, delimiter: 2 });

    const frame = await spreadsheetReady(page);
    await expect(frame.getByText('Choose CSV options'), 'ONLYOFFICE did not ask').toHaveCount(0);
    await expect(page.locator('.filex-viewer-csv'), 'not the table').toHaveCount(0);
    await expect(page.getByTestId('office-csv-note'), 'a look saves nothing').toHaveCount(0);
    await expect(page.locator('button.fe-viewer__act[title="Edit"]')).toBeVisible();
  });

  test('Edit opens it in ONLYOFFICE; the saved file is the same kind of CSV with the new value', async ({ page, context }) => {
    test.skip(!ds, 'ONLYOFFICE is not configured on this run');
    test.setTimeout(180_000);
    await openFolder(page);
    await fileName(page).dblclick();
    await spreadsheetReady(page);

    const [tab] = await Promise.all([context.waitForEvent('page'), page.locator('button.fe-viewer__act[title="Edit"]').click()]);
    await tab.waitForLoadState();
    expect(tab.url()).toContain('app=onlyoffice');
    expect(tab.url()).toContain('mode=edit');
    await expect(tab.getByTestId('office-csv-note')).toContainText('Saved as CSV: only the values of the active sheet are kept.');
    const frame = await spreadsheetReady(tab);
    await expect(frame.getByText('Choose CSV options')).toHaveCount(0);

    // B2 ("3") becomes 42, through the cell name box. Typed key by key, and
    // read back before typing the value: on Firefox, fill() set the box
    // without the key events ONLYOFFICE listens for, the box never moved,
    // the Enter went to the grid (A1 -> A2) and 42 landed in A2 (GitHub
    // run 37606303144, v0.53.0).
    const nameBox = frame.locator('#ce-cell-name');
    await nameBox.click();
    await nameBox.press('ControlOrMeta+a');
    await nameBox.pressSequentially('B2');
    await nameBox.press('Enter');
    await expect(nameBox, 'the name box went to B2').toHaveValue('B2');
    await tab.waitForTimeout(500);
    await tab.keyboard.type('42');
    await tab.keyboard.press('Enter');
    await tab.waitForTimeout(2_000);

    // Closing the last editor ends the session: the document server assembles
    // the file and calls filex back (status 2, ~10 s later).
    await page.getByTestId('viewer-close').click();
    await tab.close();
    await expect.poll(onDisk, { timeout: 90_000, intervals: [1_000] }).not.toBe(ORIGINAL);
    const saved = readFileSync(join(storageRoot(mount), name));
    expect(saved.subarray(0, 2).toString('latin1'), 'never XLSX bytes under the .csv name').not.toBe('PK');
    expect(saved.toString('utf8')).toBe(EDITED);
  });

  test('"Open with" offers ONLYOFFICE and the table; "Always use" the table is followed', async ({ page }) => {
    test.skip(!ds, 'ONLYOFFICE is not configured on this run');
    await openFolder(page);
    await fileName(page).click({ button: 'right' });
    await expect(page.getByTestId('ctx-open-with-onlyoffice')).toContainText('Open with ONLYOFFICE');
    await expect(page.getByTestId('ctx-open-with-builtin')).toBeVisible();
    await page.getByTestId('ctx-open-with-choose').click();
    const dialog = page.getByTestId('open-with-dialog');
    await expect(dialog).toBeVisible();
    await expect(page.getByTestId('open-with-choice-onlyoffice')).toContainText('ONLYOFFICE (spreadsheet editor)');
    await expect(page.getByTestId('open-with-choice-onlyoffice')).toContainText('Opens it now');
    await page.getByTestId('open-with-choice-builtin').click();
    await page.getByTestId('open-with-always').check();
    await page.getByTestId('open-with-open').click();
    await expect(page.locator('.filex-viewer-csv')).toBeVisible();
    await expect(page.locator('iframe[name^="frameEditor"]')).toHaveCount(0);
    await expect
      .poll(async () => JSON.parse(((await (await api.get(PREFS)).json()).prefs?.openWith as string) || '{}').csv)
      .toBe('builtin');

    await page.getByTestId('viewer-close').click();
    await fileName(page).dblclick();
    await expect(page.locator('.filex-viewer-csv'), 'the next opening follows the choice').toBeVisible();
    await expect(page.locator('iframe[name^="frameEditor"]')).toHaveCount(0);
    await page.getByTestId('viewer-close').click();
    expect((await api.delete('/api/me/open-with/csv')).ok()).toBe(true);
  });

  test('Default apps lists .csv: ONLYOFFICE first, the table second', async () => {
    test.skip(!ds, 'ONLYOFFICE is not configured on this run');
    const res = await api.get('/api/admin/file-types');
    expect(res.ok(), await res.text()).toBe(true);
    const body = (await res.json()) as { enabled: boolean; kinds: Array<{ ext: string; open: { on: Array<{ id: string }> } }> };
    test.skip(!body.enabled, 'app plugins are off on this run: Default apps is not editable');
    const csv = body.kinds.find((k) => k.ext === 'csv');
    expect(csv, '.csv is listed').toBeTruthy();
    expect(csv!.open.on.map((h) => h.id)).toEqual(['onlyoffice', 'builtin']);
  });

  test('ONLYOFFICE switched off: a choice of it falls to the table, no error, and the row is greyed', async ({ page }) => {
    test.skip(!ds, 'ONLYOFFICE is not configured on this run');
    const put = await api.put('/api/me/open-with/csv', { data: { handler: 'onlyoffice' } });
    expect(put.ok(), `${put.status()} ${await put.text()}`).toBe(true);
    const off = await api.patch('/api/admin/external/onlyoffice', { data: { enabled: false } });
    expect(off.ok(), await off.text()).toBe(true);
    try {
      await expect.poll(() => documentServer(api)).toBe(false);
      await openFolder(page);
      await fileName(page).dblclick();
      await expect(page.locator('.filex-viewer-csv')).toBeVisible();
      await expect(page.locator('iframe[name^="frameEditor"]')).toHaveCount(0);
      await expect(page.getByTestId('office-fallback'), 'no "not configured" in place of the file').toHaveCount(0);
      await page.getByTestId('viewer-close').click();
      await fileName(page).click({ button: 'right' });
      const row = page.getByTestId('ctx-open-with-onlyoffice');
      await expect(row).toBeVisible();
      await expect(row).toBeDisabled();
      await expect(row).toHaveAttribute('title', /ONLYOFFICE is not set up/);
      await page.keyboard.press('Escape');
      expect(JSON.parse(((await (await api.get(PREFS)).json()).prefs?.openWith as string) || '{}').csv, 'the choice is kept for when it is back').toBe(
        'onlyoffice',
      );
    } finally {
      await restoreOnlyOffice(api);
      await api.delete('/api/me/open-with/csv');
    }
  });

  test('without ONLYOFFICE: the table, and the greyed row for an administrator', async ({ page }) => {
    test.skip(ds, 'ONLYOFFICE is configured on this run (5 covers it switched off)');
    await openFolder(page);
    await fileName(page).dblclick();
    await expect(page.locator('.filex-viewer-csv')).toBeVisible();
    await expect(page.locator('iframe[name^="frameEditor"]')).toHaveCount(0);
    await page.getByTestId('viewer-close').click();
    await fileName(page).click({ button: 'right' });
    await expect(page.getByTestId('ctx-open-with-onlyoffice')).toBeDisabled();
  });
});
