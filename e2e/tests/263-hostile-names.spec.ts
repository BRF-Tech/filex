/**
 * 263 - the hostile file names in the browser (task #238).
 *
 * The names are the ONE list (backend/internal/testutil/hostilenames/
 * names.json, through helpers/hostile-names.mjs): Turkish and CJK letters,
 * right-to-left scripts, emoji, the same name composed and decomposed, spaces
 * and dots at the ends, URL characters, Windows device names, names at the
 * 255-byte limit, invisible characters, case twins. Every one the server's
 * disk can hold is written straight into the storage (the exact bytes - a
 * browser could not send a decomposed name or a line break) and catalogued by
 * a scan; then, in a real browser:
 *
 *   1. every name is listed, and drawn exactly as it is (a tab or a line
 *      break no door may create any more included: a file that already has
 *      one is still shown) - except the name
 *      with a RIGHT-TO-LEFT OVERRIDE, whose control is drawn as U+FFFD so the
 *      row reads `invoice?fdp.exe`, and whose Type column says EXE, never
 *      `invoiceexe.pdf`;
 *   2. a file renamed through the dialog to another hostile name, and back;
 *   3. the composed spelling a keyboard types finds the decomposed name, and
 *      the spelling a person sees finds the name with a zero-width space;
 *   4. an upload keeps a double quote in its name (a browser escapes it in a
 *      multipart filename; the explorer sends the name again as a field);
 *   5. the viewer opens a few of them with their contents.
 *
 * ⚠ Language pinned to English on the ACCOUNT (lesson #616) and restored.
 */
import { test, expect, type APIRequestContext, type Page } from '@playwright/test';
import fs from 'node:fs';
import path from 'node:path';
import { loginAs } from '../helpers/auth';
import { setAccountViewMode } from '../helpers/prefs';
import { dropStorageByName, newAuthedRequest, seedLocalStorage, storageRoot } from '../helpers/seed';
import { cataloguedIn, scanNow } from '../helpers/scan';
import {
  CREATABLE_NAMES,
  REFUSED_NAMES,
  hostileName,
  utf8Bytes,
  type HostileName,
} from '../helpers/hostile-names.mjs';

const STORE = `e2e-hostile263-${Date.now()}`;
const MOUNT = `/tmp/filex-${STORE}`;
const PREFS = '/api/me/prefs?surface=web';
const RLO = String.fromCodePoint(0x202e);
const SHOWN_AS = String.fromCodePoint(0xfffd);

/** Kept off the disk: the rename's target and the upload's name. */
const RENAME_TO = hostileName('percent-alone');
const UPLOADED = hostileName('double-quote');

/**
 * The names this machine's disk holds side by side in one folder - the
 * server writes there as it is told: no name over 255 bytes (Linux, macOS),
 * none Windows refuses (on Windows), and one of each twin pair where the file
 * system folds case or normalisation (Windows, macOS). Mirrors
 * hostilenames.ForLocalDisk on the Go side.
 */
function forThisDisk(): HostileName[] {
  const win = process.platform === 'win32';
  const folds = win || process.platform === 'darwin';
  const kept = new Set<string>();
  return CREATABLE_NAMES.filter((n) => {
    if (n.id === RENAME_TO.id || n.id === UPLOADED.id) return false;
    if (win ? n.windowsInvalid : utf8Bytes(n.name) > 255) return false;
    if (folds && n.twin && kept.has(n.twin)) return false;
    kept.add(n.id);
    return true;
  });
}

const SEEDED = forThisDisk();

/**
 * Names no door may create any more but a disk can hold - a tab, a line
 * break (the owner's decision for 0.57): a file that already has one, put
 * there by something else, is still listed and drawn as it is. Not on
 * Windows, which refuses them; never NUL, which no disk holds.
 */
const PLANTED: HostileName[] =
  process.platform === 'win32'
    ? []
    : REFUSED_NAMES.filter((n) => n.tags.includes('control') && !n.name.includes(String.fromCodePoint(0)));
const body = (n: HostileName) => `content of ${n.id}\n`;
const wire = (name: string) => `${STORE}://${name}`;

let prefsBefore: Record<string, unknown> = {};

/** Every row the list draws: its real path, the name it shows, its type. */
async function rows(page: Page): Promise<Array<{ path: string; name: string; type: string }>> {
  return page.$$eval('.fe-list__row[data-fe-path]', (els) =>
    els.map((e) => ({
      path: e.getAttribute('data-fe-path') ?? '',
      name: e.querySelector('.fe-list__name bdi')?.textContent ?? '',
      type: (e.querySelector('[data-col="type"]')?.textContent ?? '').trim(),
    })),
  );
}

async function paths(page: Page): Promise<string[]> {
  return (await rows(page)).map((r) => r.path);
}

/** A row by its real path - for names CSS can quote as they are. */
function rowAt(page: Page, name: string) {
  return page.locator(`.fe-list__row[data-fe-path="${wire(name)}"]`).first();
}

async function openStorage(page: Page) {
  await page.addInitScript(() => localStorage.setItem('filex.tourDone', '1'));
  await loginAs(page);
  await setAccountViewMode(page.request, 'list');
  await page.goto(`/drive/explore?storage=${encodeURIComponent(STORE)}`);
  await expect(page.getByTestId('sidenav-new').first()).toBeVisible({ timeout: 30_000 });
  await expect
    .poll(async () => (await paths(page)).length, { message: 'the listing is drawn', timeout: 30_000 })
    .toBeGreaterThanOrEqual(SEEDED.length);
}

async function renameRow(page: Page, from: string, to: string) {
  const target = rowAt(page, from);
  await target.click({ button: 'right' });
  const menu = page.getByRole('menu').first();
  await expect(menu).toBeVisible();
  await menu.getByRole('menuitem', { name: /^Rename$/ }).click();
  const dialog = page.getByRole('dialog', { name: /^Rename$/ });
  await expect(dialog).toBeVisible();
  await expect(dialog.getByRole('textbox'), 'the dialog offers the real name').toHaveValue(from);
  await dialog.getByRole('textbox').fill(to);
  await dialog.getByRole('button', { name: /^Save$/ }).click();
  await expect(dialog).toBeHidden();
  await expect.poll(() => paths(page), { message: `renamed to ${to}`, timeout: 15_000 }).toContain(wire(to));
  expect(await paths(page)).not.toContain(wire(from));
}

test.describe.serial('263 - hostile file names in the browser', () => {
  let api: APIRequestContext;

  test.beforeAll(async ({ request, playwright, baseURL }) => {
    await dropStorageByName(request, STORE);
    const root = storageRoot(MOUNT);
    fs.mkdirSync(root, { recursive: true });
    for (const n of [...SEEDED, ...PLANTED]) fs.writeFileSync(path.join(root, n.name), body(n));
    const storeId = (await seedLocalStorage(request, STORE, MOUNT)).id;
    api = await newAuthedRequest(playwright, baseURL ?? '');
    const got = await api.get(PREFS);
    const doc = got.ok() ? (await got.json()).prefs : {};
    prefsBefore = doc && typeof doc === 'object' && !Array.isArray(doc) ? doc : {};
    expect((await api.put(PREFS, { data: { prefs: { ...prefsBefore, locale: 'en' } } })).ok()).toBe(true);

    await scanNow(api, storeId);
    const want = [...SEEDED, ...PLANTED].map((n) => n.name).sort();
    await expect
      .poll(async () => (await cataloguedIn(api, STORE, '')).map(([, name]) => name).sort(), {
        message: 'the scan catalogues every hostile name with its own bytes',
        timeout: 60_000,
      })
      .toEqual(want);
  });

  test.afterAll(async ({ request }) => {
    await api?.put(PREFS, { data: { prefs: prefsBefore } }).catch(() => undefined);
    await api?.dispose();
    await dropStorageByName(request, STORE);
  });

  test('every name is listed and drawn as it is; the override cannot hide an extension', async ({ page }) => {
    test.setTimeout(120_000);
    await openStorage(page);
    const drawn = new Map((await rows(page)).map((r) => [r.path, r]));
    for (const n of SEEDED) {
      const r = drawn.get(wire(n.name));
      expect(r, `${n.id}: no row for ${JSON.stringify(n.name)}`).toBeTruthy();
      if (!r) continue;
      if (n.tags.includes('bidi-control')) {
        expect(r.name, `${n.id}: the override reached the screen`).not.toContain(RLO);
        expect(r.name, `${n.id}: drawn with its true extension last`).toBe(`invoice${SHOWN_AS}fdp.exe`);
        expect(r.type, `${n.id}: the Type column names the real kind`).toBe('EXE');
      } else {
        expect(r.name, `${n.id}: drawn as another name`).toBe(n.name);
      }
    }
    for (const n of PLANTED) {
      const r = drawn.get(wire(n.name));
      expect(r, `${n.id}: a file that already has the name is not listed`).toBeTruthy();
      expect(r?.name, `${n.id}: drawn as another name`).toBe(n.name);
    }
  });

  test('renamed through the dialog to another hostile name, and back', async ({ page }) => {
    test.setTimeout(120_000);
    await openStorage(page);
    const from = hostileName('turkish').name;
    await renameRow(page, from, RENAME_TO.name);
    expect(fs.existsSync(path.join(storageRoot(MOUNT), RENAME_TO.name)), 'on disk under the new name').toBe(true);
    await renameRow(page, RENAME_TO.name, from);
    expect(fs.readFileSync(path.join(storageRoot(MOUNT), from), 'utf8')).toBe(body(hostileName('turkish')));
  });

  test('the composed spelling finds the decomposed name; the seen spelling finds the invisible one', async ({ page }) => {
    test.setTimeout(120_000);
    const nfd = hostileName('nfd-butce');
    const zwsp = hostileName('zero-width-space');
    test.skip(!SEEDED.includes(nfd), 'this disk folds the two spellings into one file');
    await openStorage(page);
    const field = page.locator('[data-testid="drive-search"] input').first();
    for (const [query, want] of [
      [hostileName('nfc-butce').name.replace(/\.txt$/, ''), nfd],
      ['zerowidth', zwsp],
    ] as const) {
      await field.fill(query);
      await field.press('Enter');
      await expect
        .poll(() => paths(page), { message: `${JSON.stringify(query)} finds ${want.id}`, timeout: 20_000 })
        .toContain(wire(want.name));
    }
    await field.fill('');
    await field.press('Enter');
  });

  test('an upload keeps a double quote in its name', async ({ page }) => {
    test.skip(process.platform === 'win32', 'Windows cannot hold the name on the server disk');
    test.setTimeout(120_000);
    await openStorage(page);
    const answered = page.waitForResponse(
      (r) => r.request().method() === 'POST' && new URL(r.url()).searchParams.get('action') === 'upload',
    );
    await page.locator('input[type="file"]').first().setInputFiles({
      name: UPLOADED.name,
      mimeType: 'text/plain',
      buffer: Buffer.from(body(UPLOADED), 'utf8'),
    });
    expect((await answered).ok(), 'the upload is answered').toBe(true);
    // A name with a line break is not tried here: no file chooser hands one
    // to a page, so the browser surface cannot meet it (the API tests do).
    expect(fs.existsSync(path.join(storageRoot(MOUNT), UPLOADED.name)), 'stored with its quotes, not %22').toBe(true);
    await expect.poll(() => paths(page), { timeout: 15_000 }).toContain(wire(UPLOADED.name));
  });

  test('the viewer opens them with their contents', async ({ page }) => {
    test.setTimeout(180_000);
    await openStorage(page);
    for (const id of ['percent-20', 'hash', 'emoji', 'korean-hangul', 'nfd-butce', 'german-eszett']) {
      const n = hostileName(id);
      if (!SEEDED.includes(n)) continue;
      await rowAt(page, n.name).dblclick();
      await expect(page.locator('.fe-viewer__name').first(), `${id}: the viewer names it`).toHaveText(n.name, {
        timeout: 15_000,
      });
      await expect(page.getByTestId('viewer-stage'), `${id}: its contents`).toContainText(`content of ${id}`, {
        timeout: 15_000,
      });
      await page.getByTestId('viewer-close').click();
      await expect(page.getByTestId('viewer-stage')).toBeHidden();
      // FilePane ignores a second open within 500 ms (lesson #502).
      await page.waitForTimeout(700);
    }
  });
});
