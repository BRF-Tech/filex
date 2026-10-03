/**
 * 172 — encrypted names: what the SERVER keeps, measured on its own disk.
 *
 * docs/E2E-ENCRYPTION.md → "Encrypted names". The unit suites prove the
 * cipher (web/tests/lib/e2enames.test.ts) and the name view
 * (web/tests/lib/e2eNameView.test.ts). This file proves the part only a real
 * browser against a real binary can: after a person creates an encrypted
 * folder, uploads, makes a folder, renames, moves, downloads, searches, signs
 * out and back in — the storage directory the server writes to never holds a
 * single plaintext name, and the person sees every one.
 *
 *   1. a new folder: every name the explorer writes is ciphertext on disk, and
 *      every surface (listing, breadcrumb, destination picker, search, Recent,
 *      download) shows the plaintext — or "🔒 Encrypted item" while locked;
 *   2. an existing folder moves from level 1 (contents) to level 2
 *      (contents + names) from its encryption settings — never from an offer
 *      after an unlock — and an interrupted change resumes to the end;
 *   3. `filex decrypt` — the Go CLI — opens the zip of what the browser wrote.
 *
 * ⚠ Language pinned to English on the ACCOUNT (lesson #616) and restored to
 * exactly what it was.
 */
import { test, expect, type APIRequestContext, type Page } from '@playwright/test';
import { spawnSync } from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { loginAs } from '../helpers/auth';
import { dropStorageByName, newAuthedRequest, seedLocalStorage, storageRoot } from '../helpers/seed';
import { setAccountViewMode } from '../helpers/prefs';
import { settled } from '../helpers/stable';

const STORE = `e2e-names-${Date.now()}`;
const MOUNT = `/tmp/filex-${STORE}`;
const PW = 'correct horse battery staple';
/** Kasa's password after the password test changes it, and after the reset. */
const PW2 = 'a second, changed passphrase';
const PW3 = 'set after the recovery key';
const PREFS = '/api/me/prefs?surface=web';
const MARKER = '.filex-e2e.json';
/** What the browser stores: base64url, at least 23 characters — a folder
 *  followed by a dot and its 22-character folder id. */
const STORED = /^[A-Za-z0-9_-]{23,}(\.[A-Za-z0-9_-]{22})?$/;
const STORED_DIR = /^[A-Za-z0-9_-]{23,}\.([A-Za-z0-9_-]{22})$/;

const SECRET_NAME = 'Bütçe 2027 — gizli.txt';
const SECRET_BODY = 'içerik: çok gizli, 2027\n';

let prefsBefore: Record<string, unknown> = {};
/** Kasa's recovery key, shown once when the first test creates it. */
let kasaRecoveryKey = '';

/** The entries of `rel` AS THE SERVER STORES THEM — its storage directory. */
function disk(rel: string): string[] {
  return fs
    .readdirSync(path.join(storageRoot(MOUNT), rel))
    .filter((n) => n !== MARKER && n !== '.keepdir' && !n.startsWith('.filex-'))
    .sort();
}

/** Every entry under `rel` but the key file, with its bytes (hex): what the
 *  server holds, byte for byte. */
function snapshot(rel: string): Record<string, string> {
  const out: Record<string, string> = {};
  const walk = (dir: string, prefix: string) => {
    for (const e of fs.readdirSync(dir, { withFileTypes: true })) {
      if (e.name === MARKER) continue;
      const full = path.join(dir, e.name);
      if (e.isDirectory()) walk(full, `${prefix}${e.name}/`);
      else out[`${prefix}${e.name}`] = fs.readFileSync(full).toString('hex');
    }
  };
  walk(path.join(storageRoot(MOUNT), rel), '');
  return out;
}

function markerOnDisk(folder: string): Record<string, unknown> {
  return JSON.parse(fs.readFileSync(path.join(storageRoot(MOUNT), folder, MARKER), 'utf8'));
}

async function pollDisk(rel: string, want: (names: string[]) => boolean, what: string): Promise<string[]> {
  let names: string[] = [];
  await expect
    .poll(
      () => {
        names = disk(rel);
        return want(names);
      },
      { message: `${what} — on disk: ${JSON.stringify(names)}`, timeout: 20_000 },
    )
    .toBe(true);
  return names;
}

/** With E2E_SHOTS=<dir>, a picture of the screen at this point (for a person
 *  to look at; nothing asserts on it). */
async function shot(page: Page, name: string) {
  const dir = process.env.E2E_SHOTS;
  if (!dir) return;
  fs.mkdirSync(dir, { recursive: true });
  await page.waitForTimeout(300);
  await page.screenshot({ path: path.join(dir, name) });
}

async function openStorage(page: Page) {
  await page.addInitScript(() => localStorage.setItem('filex.tourDone', '1'));
  // A decrypted download goes through the File System Access save dialog
  // where the browser has one (Chromium; docs/E2E-ENCRYPTION.md → "Where a
  // decrypted download goes"), and a headless run cannot answer that dialog.
  // This spec is about names and contents, not the save sink — spec 173
  // measures both sinks — so every engine takes the in-memory path here,
  // which Playwright sees as a download.
  await page.addInitScript(() => {
    Object.defineProperty(window, 'showSaveFilePicker', { value: undefined, configurable: true, writable: true });
  });
  await loginAs(page);
  await setAccountViewMode(page.request, 'list');
  await page.goto(`/drive/explore?storage=${encodeURIComponent(STORE)}`);
  await expect(page.getByTestId('sidenav-new').first()).toBeVisible({ timeout: 30_000 });
}

/** A row of the main listing by the name a person reads. */
function row(page: Page, name: string) {
  return page
    .locator('.fe-list__row[data-fe-path]')
    .filter({ has: page.locator('.fe-list__name', { hasText: new RegExp(`^${escapeRe(name)}$`) }) })
    .first();
}

function escapeRe(s: string): string {
  return s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
}

async function openFolder(page: Page, name: string) {
  const target = await settled(row(page, name));
  await target.dblclick();
  await target.dispose();
  // FilePane ignores a second open within 500 ms (lesson #502).
  await page.waitForTimeout(700);
}

async function newMenu(page: Page, item: RegExp) {
  await page.getByTestId('sidenav-new').first().click();
  // By the label alone: the item also carries its shortcut ("Shift+N").
  await page
    .locator('.fe-ctx__item', { has: page.locator('.fe-ctx__label', { hasText: item }) })
    .first()
    .click();
}

async function createEncryptedFolder(page: Page, name: string, level: 'content' | 'names'): Promise<string> {
  await newMenu(page, /^New folder$/);
  await page.getByText('Create encrypted folder…', { exact: true }).click();
  const dialog = page.locator('.fe-modal__card').filter({ has: page.getByTestId('e2e-create-names') });
  await expect(dialog).toBeVisible();
  await dialog.locator('input[type="text"]').first().fill(name);
  const pws = dialog.locator('input[type="password"]');
  await pws.nth(0).fill(PW);
  await pws.nth(1).fill(PW);
  // Level 1 (contents only) is the default; the vault is not offered.
  await expect(dialog.getByTestId('e2e-level-content'), 'level 1 is the default').toBeChecked();
  await expect(dialog.getByTestId('e2e-level-names')).not.toBeChecked();
  await expect(dialog.locator('input[type="radio"]'), 'only the levels that work').toHaveCount(2);
  if (level === 'names') await dialog.getByTestId('e2e-level-names').check();
  await dialog.getByTestId('e2e-create-ack').check();
  await dialog.getByRole('button', { name: 'Create encrypted folder', exact: true }).click();
  const keyEl = page.locator('.fe-e2e-rk__key');
  await expect(keyEl).toBeVisible({ timeout: 20_000 });
  const key = (await keyEl.innerText()).trim();
  await page.locator('.fe-e2e-rk .fe-e2e-ack input[type="checkbox"]').check();
  await page.getByRole('button', { name: 'Done', exact: true }).click();
  await expect(keyEl).toBeHidden();
  return key;
}

/**
 * Upload one file and wait until the explorer has taken it in: the upload's
 * answer, then the row in the listing the explorer reloads after it.
 *
 * ⚠ Not just the file on the disk (pollDisk). The disk has the file before the
 * upload answers, and the explorer reloads the folder it was in only after the
 * answer: a breadcrumb click in that gap was undone by the late reload of the
 * folder being left (two listings in flight, the last to answer wins), and the
 * next row was looked for in the wrong folder (seen in the 0.50 integration
 * run under load: "alt" not found, the view still in Eski/alt).
 */
async function uploadFile(page: Page, name: string, body: string) {
  const answered = page.waitForResponse(
    (r) => r.request().method() === 'POST' && new URL(r.url()).searchParams.get('action') === 'upload',
  );
  await page.locator('input[type="file"]').first().setInputFiles({
    name,
    mimeType: 'text/plain',
    buffer: Buffer.from(body, 'utf8'),
  });
  await answered;
  await expect(row(page, name)).toBeVisible({ timeout: 15_000 });
}

async function newFolderHere(page: Page, name: string) {
  await newMenu(page, /^New folder$/);
  const dialog = page.getByRole('dialog', { name: 'New folder' });
  await expect(dialog).toBeVisible();
  await dialog.getByRole('textbox').fill(name);
  await dialog.getByRole('button', { name: 'Create', exact: true }).click();
  await expect(dialog).toBeHidden();
}

async function rowVerb(page: Page, name: string, verb: RegExp) {
  const target = await settled(row(page, name));
  await target.click({ button: 'right' });
  await target.dispose();
  const menu = page.getByRole('menu').first();
  await expect(menu).toBeVisible();
  await menu.getByRole('menuitem', { name: verb }).click();
}

async function unlock(page: Page, password = PW) {
  const lock = page.locator('.fe-e2e-lock');
  await expect(lock).toBeVisible({ timeout: 20_000 });
  await lock.locator('input[type="password"]').fill(password);
  await lock.getByRole('button', { name: 'Unlock', exact: true }).click();
  await expect(page.locator('.fe-e2e-strip')).toBeVisible({ timeout: 20_000 });
}

test.describe.serial('E2E encrypted names — what the server keeps', () => {
  let api: APIRequestContext;

  test.beforeAll(async ({ request, playwright, baseURL }) => {
    await dropStorageByName(request, STORE);
    await seedLocalStorage(request, STORE, MOUNT);
    api = await newAuthedRequest(playwright, baseURL ?? '');
    const got = await api.get(PREFS);
    const doc = got.ok() ? (await got.json()).prefs : {};
    prefsBefore = doc && typeof doc === 'object' && !Array.isArray(doc) ? doc : {};
    const put = await api.put(PREFS, { data: { prefs: { ...prefsBefore, locale: 'en' } } });
    expect(put.ok()).toBe(true);
  });

  test.afterAll(async ({ request }) => {
    await api?.put(PREFS, { data: { prefs: prefsBefore } }).catch(() => undefined);
    await api?.dispose();
    await dropStorageByName(request, STORE);
  });

  test('a new folder: every name on the server is ciphertext, every name on screen is not', async ({ page }) => {
    test.setTimeout(240_000);
    await openStorage(page);
    kasaRecoveryKey = await createEncryptedFolder(page, 'Kasa', 'names');

    // The marker: v3, names required, nothing pending.
    const m = markerOnDisk('Kasa');
    expect(m.v).toBe(3);
    expect(m.req).toEqual(['names']);
    expect((m.names as { alg: string }).alg).toBe('AES-SIV-512');
    expect((m.names as { pending?: boolean }).pending).toBeUndefined();

    await openFolder(page, 'Kasa');
    await expect(page.getByTestId('e2e-names-status')).toHaveText('Contents and names');
    await shot(page, 'level2-strip.png');

    // ── upload: ciphertext name AND ciphertext content on disk ──────────
    await uploadFile(page, SECRET_NAME, SECRET_BODY);
    const [stored] = await pollDisk('Kasa', (n) => n.length === 1, 'the upload reached the disk');
    expect(stored, 'the stored name is ciphertext').toMatch(STORED);
    expect(stored).not.toContain('Bütçe');
    expect(fs.readFileSync(path.join(storageRoot(MOUNT), 'Kasa', stored)).subarray(0, 8).toString()).toBe('filexe2e');
    await expect(row(page, SECRET_NAME), 'the person sees the plaintext').toBeVisible();

    // …and the API answers with what the server has: ciphertext only.
    const listed = await api.get(`/api/files/manager?action=index&path=${encodeURIComponent(`${STORE}://Kasa`)}`);
    const names = ((await listed.json()).files as Array<{ basename: string }>).map((f) => f.basename);
    expect(names).toEqual([stored]);

    // ── new folder ─────────────────────────────────────────────────────
    await newFolderHere(page, 'Sözleşmeler');
    const two = await pollDisk('Kasa', (n) => n.length === 2, 'the new folder reached the disk');
    let folderStored = two.find((n) => n !== stored)!;
    expect(folderStored, 'a folder: its name, a dot, its folder id').toMatch(STORED_DIR);
    expect(fs.statSync(path.join(storageRoot(MOUNT), 'Kasa', folderStored)).isDirectory()).toBe(true);
    await expect(row(page, 'Sözleşmeler')).toBeVisible();

    // ── rename ─────────────────────────────────────────────────────────
    await rowVerb(page, SECRET_NAME, /^Rename$/);
    const renameDialog = page.getByRole('dialog', { name: /^Rename$/ });
    await expect(renameDialog).toBeVisible();
    await expect(renameDialog.getByRole('textbox'), 'the dialog offers the plaintext').toHaveValue(SECRET_NAME);
    await renameDialog.getByRole('textbox').fill('Rapor.txt');
    await renameDialog.getByRole('button', { name: /^Save$/ }).click();
    await expect(row(page, 'Rapor.txt')).toBeVisible();
    const afterRename = await pollDisk('Kasa', (n) => !n.includes(stored) && n.length === 2, 'the rename reached the disk');
    const renamed = afterRename.find((n) => n !== folderStored)!;
    expect(renamed).toMatch(STORED);

    // ── move, through the destination picker (plaintext rows) ──────────
    await rowVerb(page, 'Rapor.txt', /^Move to…$/);
    const picker = page.getByTestId('destpicker');
    await expect(picker).toBeVisible();
    await picker.getByTestId('destpicker-row-Sözleşmeler').click();
    await page.getByTestId('destpicker-confirm').click();
    // A name is sealed for its folder: the move re-seals it, in one step.
    const [inSub] = await pollDisk(`Kasa/${folderStored}`, (n) => n.length === 1, 'the move reached the disk');
    expect(inSub).toMatch(STORED);
    expect(inSub, 'the same name, sealed for another folder, is another stored name').not.toBe(renamed);
    expect(disk('Kasa')).toEqual([folderStored]);

    // ── rename the folder: its id stays, nothing inside it is touched ──
    const folderId = STORED_DIR.exec(folderStored)![1];
    await rowVerb(page, 'Sözleşmeler', /^Rename$/);
    const folderRename = page.getByRole('dialog', { name: /^Rename$/ });
    await folderRename.getByRole('textbox').fill('Anlaşmalar');
    await folderRename.getByRole('button', { name: /^Save$/ }).click();
    const [renamedFolder] = await pollDisk('Kasa', (n) => n.length === 1 && n[0] !== folderStored, 'the folder rename');
    expect(STORED_DIR.exec(renamedFolder)?.[1], 'the folder keeps its id').toBe(folderId);
    expect(disk(`Kasa/${renamedFolder}`), 'its contents are not renamed').toEqual([inSub]);
    await rowVerb(page, 'Anlaşmalar', /^Rename$/);
    await page.getByRole('dialog', { name: /^Rename$/ }).getByRole('textbox').fill('Sözleşmeler');
    await page.getByRole('dialog', { name: /^Rename$/ }).getByRole('button', { name: /^Save$/ }).click();
    [folderStored] = await pollDisk('Kasa', (n) => n.length === 1 && n[0] !== renamedFolder, 'renamed back');
    await expect(row(page, 'Sözleşmeler')).toBeVisible();

    // ── the same name in another folder is another stored name ────────
    await uploadFile(page, 'Rapor.txt', 'kökteki rapor\n');
    const withTwin = await pollDisk('Kasa', (n) => n.length === 2, 'the second Rapor.txt');
    const twin = withTwin.find((n) => n !== folderStored)!;
    expect(twin, 'Kasa/Rapor.txt and Kasa/Sözleşmeler/Rapor.txt are two stored names').not.toBe(inSub);
    expect(twin).toMatch(STORED);

    // ── the breadcrumb and the tab speak plaintext ─────────────────────
    await openFolder(page, 'Sözleşmeler');
    await expect(row(page, 'Rapor.txt')).toBeVisible();
    const crumbs = await page.locator('.fe-breadcrumb').first().innerText();
    expect(crumbs).toContain('Sözleşmeler');
    expect(crumbs).not.toContain(folderStored);

    // ── download: plaintext bytes, plaintext name ──────────────────────
    const [dl] = await Promise.all([page.waitForEvent('download'), rowVerb(page, 'Rapor.txt', /^Download$/)]);
    expect(dl.suggestedFilename()).toBe('Rapor.txt');
    const saved = await dl.path();
    expect(fs.readFileSync(saved!, 'utf8')).toBe(SECRET_BODY);

    // Open it once, so Recent has it.
    await openFolder(page, 'Rapor.txt');
    await page.keyboard.press('Escape');

    // ── search: the browser finds it, the server cannot ────────────────
    await page.locator('.fe-breadcrumb__crumb', { hasText: 'Kasa' }).first().click();
    await expect(row(page, 'Sözleşmeler')).toBeVisible();
    const field = page.locator('[data-testid="drive-search"] input').first();
    await field.fill('rapor');
    await field.press('Enter');
    await expect(row(page, 'Rapor.txt'), 'the search inside the folder runs in the browser').toBeVisible();
    await field.fill('');
    await field.press('Enter');
    const serverSearch = await api.get(
      `/api/files/manager?action=search&path=${encodeURIComponent(`${STORE}://`)}&filter=Rapor`,
    );
    expect(((await serverSearch.json()).files as unknown[]).length, 'the server cannot find a name it never saw').toBe(0);

    // ── a new session: locked everywhere, then plaintext again ─────────
    // Sign out through the explorer's own account menu (the shared logout
    // helper waits for /admin/login; the explorer's sign-out lands on its own
    // sign-in page), and wait for the sign-in form.
    await page.getByTestId('explore-account').click();
    await page.getByTestId('explore-signout').click();
    await expect(page.getByRole('button', { name: 'Sign in', exact: true })).toBeVisible({ timeout: 15_000 });
    await loginAs(page);
    await page.goto(`/drive/explore?storage=${encodeURIComponent(STORE)}`);
    await page.getByTestId('sidenav-view-recent').click();
    await expect(
      page.locator('.fe-list__name', { hasText: '🔒 Encrypted item' }).first(),
      'Recent names a locked item honestly',
    ).toBeVisible({ timeout: 20_000 });
    await expect(page.locator('.fe-list__name', { hasText: 'Rapor.txt' })).toHaveCount(0);

    await page.goto(`/drive/explore?storage=${encodeURIComponent(STORE)}`);
    await openFolder(page, 'Kasa');
    await unlock(page);
    await expect(row(page, 'Sözleşmeler')).toBeVisible();
    await page.getByTestId('sidenav-view-recent').click();
    await expect(page.locator('.fe-list__name', { hasText: /^Rapor\.txt$/ }).first(), 'and by name once unlocked').toBeVisible();

    // Nothing the explorer wrote left a plaintext name anywhere on disk.
    const all: string[] = [];
    const walk = (dir: string) => {
      for (const e of fs.readdirSync(dir, { withFileTypes: true })) {
        all.push(e.name);
        if (e.isDirectory()) walk(path.join(dir, e.name));
      }
    };
    walk(path.join(storageRoot(MOUNT), 'Kasa'));
    for (const n of all) {
      // The key file, and filex's own bookkeeping (`.keepdir` in an empty
      // folder on some drivers) — never a name a person gave.
      if (n === MARKER || n === '.keepdir' || n.startsWith('.filex-')) continue;
      expect(n, `stored name ${n}`).toMatch(STORED);
    }
  });

  test('the split pane inside a locked folder shows its lock screen, and one unlock opens both panes', async ({ page }) => {
    test.setTimeout(120_000);
    await openStorage(page);
    await openFolder(page, 'Kasa');
    const main = page.getByTestId('pane-main');
    await expect(main.getByTestId('e2e-lock')).toBeVisible({ timeout: 20_000 });

    await page.getByTestId('tabs-split').click();
    const split = page.getByTestId('pane-split');
    await expect(split).toBeVisible();
    // The same lock screen, not rows of "🔒 Encrypted item".
    await expect(split.getByTestId('e2e-lock')).toBeVisible({ timeout: 20_000 });
    await expect(split.locator('.fe-list__row[data-fe-path]')).toHaveCount(0);
    await shot(page, 'split-pane-lock.png');

    const form = split.getByTestId('e2e-lock');
    await form.locator('input[type="password"]').fill('not the password');
    await form.getByRole('button', { name: 'Unlock', exact: true }).click();
    await expect(form.locator('.fe-form__error')).toBeVisible();
    await form.locator('input[type="password"]').fill(PW);
    await form.getByRole('button', { name: 'Unlock', exact: true }).click();

    // One key ring per tab: both panes open.
    await expect(split.getByTestId('e2e-lock')).toHaveCount(0, { timeout: 20_000 });
    await expect(main.getByTestId('e2e-lock')).toHaveCount(0, { timeout: 20_000 });
    await expect(split.locator('.fe-list__name', { hasText: /^Sözleşmeler$/ }).first()).toBeVisible();
    await expect(main.locator('.fe-list__name', { hasText: /^Sözleşmeler$/ }).first()).toBeVisible();
    await page.getByTestId('tabs-split').click();
  });

  test('a folder moves from level 1 to level 2 in its settings, and an interrupted change resumes', async ({ page }) => {
    test.setTimeout(240_000);
    await openStorage(page);
    await createEncryptedFolder(page, 'Eski', 'content');
    expect(markerOnDisk('Eski').v, 'level 1 keeps the v2 marker').toBe(2);

    await openFolder(page, 'Eski');
    await expect(page.getByTestId('e2e-names-status')).toHaveText('Contents only');
    await uploadFile(page, 'bir.txt', 'bir\n');
    await pollDisk('Eski', (n) => n.includes('bir.txt'), 'first upload');
    await uploadFile(page, 'iki.txt', 'iki\n');
    await pollDisk('Eski', (n) => n.includes('iki.txt'), 'second upload');
    await newFolderHere(page, 'alt');
    await pollDisk('Eski', (n) => n.includes('alt'), 'the folder');
    await openFolder(page, 'alt');
    await uploadFile(page, 'üç.txt', 'üç\n');
    await pollDisk('Eski/alt', (n) => n.includes('üç.txt'), 'an upload inside it');
    await page.locator('.fe-breadcrumb__crumb', { hasText: 'Eski' }).first().click();
    await expect(row(page, 'alt')).toBeVisible();

    // Lock and unlock: nothing is offered — the level is the folder's.
    await page.locator('.fe-e2e-strip').getByRole('button', { name: 'Lock', exact: true }).click();
    await unlock(page);
    await page.waitForTimeout(500);
    await expect(page.getByTestId('e2e-settings'), 'no offer pops up after an unlock').toHaveCount(0);
    await expect(page.getByTestId('e2e-names-progress-strip')).toHaveCount(0);

    // The settings: the level, and what raising it costs.
    await page.getByTestId('e2e-settings-open').click();
    await expect(page.getByTestId('e2e-settings-level')).toHaveText('Contents only');
    await page.getByTestId('e2e-settings-raise').click();
    const confirm = page.getByTestId('e2e-settings-raise-confirm');
    await expect(confirm).toContainText('filex 0.47 and older will refuse to open this folder');
    await expect(page.getByTestId('e2e-settings-raise-go'), 'not without the acknowledgement').toBeDisabled();
    await page.getByTestId('e2e-settings-raise-ack').check();
    await shot(page, 'settings-raise-level.png');

    // Interrupt it: the second rename the pass sends fails.
    let renames = 0;
    await page.route(
      (u) => u.pathname === '/api/files/manager' && u.searchParams.get('action') === 'rename',
      async (route) => {
        renames++;
        if (renames === 2) await route.abort('failed');
        else await route.continue();
      },
    );
    await page.getByTestId('e2e-settings-raise-go').click();
    const strip = page.getByTestId('e2e-names-progress-strip');
    await expect(strip.locator('.fe-form__error')).toContainText('could not be renamed', { timeout: 30_000 });
    await shot(page, 'level-change-interrupted.png');
    await page.unrouteAll({ behavior: 'ignoreErrors' });
    const half = markerOnDisk('Eski');
    expect(half.v).toBe(3);
    expect((half.names as { pending?: boolean }).pending, 'the marker says the switch is not finished').toBe(true);
    const mixed = disk('Eski');
    expect(mixed.filter((n) => !STORED.test(n)).length, 'one name is still readable').toBe(1);

    // Continue: the same pass, to the end.
    await expect(page.getByTestId('e2e-names-resume')).toHaveText('Continue');
    await page.getByTestId('e2e-names-resume').click();
    await expect(strip).toBeHidden({ timeout: 30_000 });
    const done = markerOnDisk('Eski');
    expect((done.names as { pending?: boolean }).pending).toBeUndefined();
    const top = disk('Eski');
    for (const n of top) expect(n).toMatch(STORED);
    const altStored = top.find((n) => STORED_DIR.test(n))!;
    expect(altStored, 'the folder carries its id').toBeTruthy();
    for (const n of disk(`Eski/${altStored}`)) expect(n).toMatch(STORED);
    await expect(page.getByTestId('e2e-names-status')).toHaveText('Contents and names');
    for (const n of ['bir.txt', 'iki.txt', 'alt']) await expect(row(page, n)).toBeVisible();
    await openFolder(page, 'alt');
    await expect(row(page, 'üç.txt'), 'what is inside a renamed folder still reads').toBeVisible();
  });

  test('the password changes; a recovery-key unlock makes the person set a new one', async ({ page }) => {
    test.setTimeout(240_000);
    await openStorage(page);
    await openFolder(page, 'Kasa');
    await unlock(page);
    const before = markerOnDisk('Kasa');
    const bytesBefore = snapshot('Kasa');

    // ── change it with the current password (from the folder's settings) ─
    await page.getByTestId('e2e-settings-open').click();
    await page.getByTestId('e2e-password-open').click();
    const form = page.getByTestId('e2e-password-form');
    await expect(form).toBeVisible();
    await expect(page.getByTestId('e2e-password-cost'), 'a wrapped folder: only the key file changes').toContainText(
      'Only the folder’s key file changes',
    );
    await page.getByTestId('e2e-password-current').fill('not the password at all');
    await page.getByTestId('e2e-password-new').fill(PW2);
    await page.getByTestId('e2e-password-new2').fill(PW2);
    await page.getByTestId('e2e-password-submit').click();
    await expect(page.getByTestId('e2e-password-error')).toHaveText('Wrong password.');
    expect(markerOnDisk('Kasa').salt, 'a wrong current password changes nothing').toBe(before.salt);

    await page.getByTestId('e2e-password-current').fill(PW);
    await page.getByTestId('e2e-password-submit').click();
    await expect(form).toBeHidden({ timeout: 30_000 });
    const after = markerOnDisk('Kasa');
    expect(after.salt).not.toBe(before.salt);
    expect(after.fmk_pw).not.toBe(before.fmk_pw);
    expect(after.rk, 'the recovery slot is untouched').toEqual(before.rk);
    expect(after.names, 'the name key is untouched').toEqual(before.names);
    expect(snapshot('Kasa'), 'no file was rewritten, no entry renamed').toEqual(bytesBefore);

    // The old password opens nothing; the new one opens everything.
    await page.locator('.fe-e2e-strip').getByRole('button', { name: 'Lock', exact: true }).click();
    const lock = page.locator('.fe-e2e-lock');
    await lock.locator('input[type="password"]').fill(PW);
    await lock.getByRole('button', { name: 'Unlock', exact: true }).click();
    await expect(lock.locator('.fe-form__error')).toHaveText('Wrong password.');
    await unlock(page, PW2);
    await expect(row(page, 'Sözleşmeler')).toBeVisible();

    // ── the recovery key: in, and straight to a new password ───────────
    await page.locator('.fe-e2e-strip').getByRole('button', { name: 'Lock', exact: true }).click();
    await expect(lock).toBeVisible();
    await page.locator('.fe-e2e-optlink').click();
    const recover = page.locator('.fe-modal__card').filter({ has: page.locator('.fe-e2e-recover__key') });
    await recover.locator('.fe-e2e-recover__key').fill(kasaRecoveryKey);
    await recover.getByRole('button', { name: 'Unlock', exact: true }).click();
    await expect(form, 'a used recovery key means a new password, now').toBeVisible({ timeout: 20_000 });
    await expect(page.getByTestId('e2e-password-current'), 'the key in hand is the proof').toHaveCount(0);
    await page.getByTestId('e2e-password-new').fill(PW3);
    await page.getByTestId('e2e-password-new2').fill(PW3);
    await page.getByTestId('e2e-password-submit').click();
    await expect(form).toBeHidden({ timeout: 30_000 });
    await expect(row(page, 'Sözleşmeler')).toBeVisible();

    // The owner (the admin who made the folder) was told, twice — once a warning.
    await expect
      .poll(async () => {
        const r = await api.get('/api/notifications?limit=50');
        const items = ((await r.json()).items ?? []) as Array<{ event: string; severity?: string; meta?: { via?: string; storage?: string } }>;
        // This run's storage only: the engines share one server and one
        // administrator, and spec 173's .fxe password changes notify them too.
        return items
          .filter((n) => n.event === 'e2e.password_changed' && n.meta?.storage === STORE)
          .map((n) => n.meta?.via ?? n.severity)
          .sort();
      }, { timeout: 20_000 })
      .toEqual(['password', 'recovery_key']);

    // The reset is not optional: closing it locks the folder again.
    await page.locator('.fe-e2e-strip').getByRole('button', { name: 'Lock', exact: true }).click();
    await page.locator('.fe-e2e-optlink').click();
    await recover.locator('.fe-e2e-recover__key').fill(kasaRecoveryKey);
    await recover.getByRole('button', { name: 'Unlock', exact: true }).click();
    await expect(form).toBeVisible({ timeout: 20_000 });
    await page.getByRole('button', { name: 'Lock the folder instead' }).click();
    await expect(page.locator('.fe-e2e-lock')).toBeVisible();
    await unlock(page, PW3);
  });

  test('a folder from before v0.31 re-wraps its file keys to change its password, resumably', async ({ page }) => {
    test.setTimeout(240_000);
    // Made by the v0.30.1 module itself, written through filex.
    const legacy = await import('../../web/tests/fixtures/e2ecrypto-legacy-v0.30.1');
    const current = await import('../../packages/core/src/lib/e2ecrypto');
    const made = await legacy.createMarker(PW);
    const files: Record<string, string> = { 'bir.txt': 'birinci dosya\n', 'iki.txt': 'ikinci dosya\n' };
    const mk = await api.post('/api/files/manager?action=newfolder', { data: { path: `${STORE}://`, name: 'Eski1' } });
    expect(mk.ok()).toBe(true);
    const up = await api.post('/api/files/manager?action=upload', {
      multipart: {
        path: `${STORE}://Eski1`,
        'file[]': { name: MARKER, mimeType: 'application/json', buffer: Buffer.from(JSON.stringify(made.marker)) },
      },
    });
    expect(up.ok()).toBe(true);
    for (const [name, body] of Object.entries(files)) {
      const ct = await legacy.encryptFile(made.kek, new TextEncoder().encode(body).buffer as ArrayBuffer);
      const r = await api.post('/api/files/manager?action=upload', {
        multipart: { path: `${STORE}://Eski1`, 'file[]': { name, mimeType: 'application/octet-stream', buffer: Buffer.from(ct) } },
      });
      expect(r.ok()).toBe(true);
    }
    const contentBefore = Object.fromEntries(
      Object.keys(files).map((n) => [n, fs.readFileSync(path.join(storageRoot(MOUNT), 'Eski1', n)).subarray(97)]),
    );

    await openStorage(page);
    await openFolder(page, 'Eski1');
    await unlock(page);
    await page.getByTestId('e2e-settings-open').click();
    await page.getByTestId('e2e-password-open').click();
    await expect(page.getByTestId('e2e-password-cost')).toContainText('its key comes from its password');
    await page.getByTestId('e2e-password-current').fill(PW);
    await page.getByTestId('e2e-password-new').fill(PW2);
    await page.getByTestId('e2e-password-new2').fill(PW2);
    await page.getByTestId('e2e-password-submit').click();
    await expect(page.getByTestId('e2e-password-error'), 'a re-key has to be acknowledged').toBeVisible();
    await page.getByTestId('e2e-password-ack').check();

    // Interrupt it: the first FILE write fails (key-file writes go through).
    let failed = false;
    await page.route(
      (u) => u.pathname === '/api/files/manager' && u.searchParams.get('action') === 'upload',
      async (route) => {
        const body = route.request().postData() ?? '';
        if (!failed && !body.includes(`filename="${MARKER}"`)) {
          failed = true;
          await route.abort('failed');
          return;
        }
        await route.continue();
      },
    );
    await page.getByTestId('e2e-password-submit').click();

    // The new recovery key, shown once — the old folder had none at all.
    const keyEl = page.locator('.fe-e2e-rk__key');
    await expect(keyEl).toBeVisible({ timeout: 30_000 });
    await page.locator('.fe-e2e-rk .fe-e2e-ack input[type="checkbox"]').check();
    await page.getByRole('button', { name: 'Done', exact: true }).click();
    const resume = page.getByTestId('e2e-rekey-offer');
    await expect(resume.locator('.fe-form__error')).toContainText('could not be re-wrapped', { timeout: 30_000 });
    await page.unrouteAll({ behavior: 'ignoreErrors' });

    const half = markerOnDisk('Eski1');
    expect(half.v).toBe(3);
    expect(half.req).toEqual(['rekey']);
    // Both files still open mid-way (one under each key).
    for (const n of Object.keys(files)) await expect(row(page, n)).toBeVisible();

    await page.getByTestId('e2e-rekey-resume').click();
    await expect(resume).toBeHidden({ timeout: 30_000 });
    const done = markerOnDisk('Eski1') as unknown as import('../../packages/core/src/lib/e2ecrypto').E2eMarker;
    expect(done.v, 'finished: a plain v2 folder with a key of its own').toBe(2);
    expect(done.fmk).toBe('wrapped');
    expect(done.rekey).toBeUndefined();

    // Measured on the bytes the server holds: the content is exactly what it
    // was, the new password opens it, the old one opens nothing.
    const fmk = await current.unlockWithPassword(done, PW2);
    expect(fmk).not.toBeNull();
    expect(await current.unlockWithPassword(done, PW)).toBeNull();
    for (const [n, body] of Object.entries(files)) {
      const bytes = fs.readFileSync(path.join(storageRoot(MOUNT), 'Eski1', n));
      expect(bytes.subarray(97).equals(contentBefore[n]), `${n}: content ciphertext untouched`).toBe(true);
      const plain = await current.decryptFile(fmk!, bytes.buffer.slice(bytes.byteOffset, bytes.byteOffset + bytes.byteLength) as ArrayBuffer);
      expect(new TextDecoder().decode(plain)).toBe(body);
      await expect(
        current.decryptFile(made.kek, bytes.buffer.slice(bytes.byteOffset, bytes.byteOffset + bytes.byteLength) as ArrayBuffer),
        `${n}: the old key opens nothing`,
      ).rejects.toThrow();
    }
  });

  test('filex decrypt opens the zip of what the browser wrote — with the current password only', async () => {
    const bin = process.env.E2E_FILEX_BIN ?? '';
    test.skip(!bin || !fs.existsSync(bin), 'no binary to run (E2E_FILEX_BIN)');
    const help = spawnSync(bin, ['decrypt', '--help'], { encoding: 'utf8' });
    test.skip(help.status !== 0, 'this binary has no `filex decrypt`');

    // The zip a person gets by downloading the encrypted folder from its parent.
    const mint = await api.post('/api/files/archive/download', { data: { paths: [`${STORE}://Kasa`] } });
    expect(mint.ok(), `archive mint: ${mint.status()}`).toBe(true);
    const ticket = (await mint.json()) as { url: string };
    const zip = await api.get(ticket.url);
    expect(zip.ok()).toBe(true);
    const work = fs.mkdtempSync(path.join(os.tmpdir(), 'filex-e2e-decrypt-'));
    const zipPath = path.join(work, 'Kasa.zip');
    fs.writeFileSync(zipPath, await zip.body());

    // Every password the folder ever had but its current one: exit 5, and
    // nothing written.
    for (const old of [PW, PW2, 'not the password']) {
      const wrongOut = path.join(work, 'wrong');
      const wrong = spawnSync(bin, ['decrypt', zipPath, '-o', wrongOut, '--password-stdin'], {
        input: `${old}\n`,
        encoding: 'utf8',
      });
      expect(wrong.status, `${old}: ${wrong.stderr}`).toBe(5);
      expect(fs.existsSync(wrongOut), 'no partial output').toBe(false);
    }

    const out = path.join(work, 'plain');
    const ok = spawnSync(bin, ['decrypt', zipPath, '-o', out, '--password-stdin'], {
      input: `${PW3}\n`,
      encoding: 'utf8',
    });
    expect(ok.status, `${ok.stdout}\n${ok.stderr}`).toBe(0);
    const file = path.join(out, 'Sözleşmeler', 'Rapor.txt');
    expect(fs.existsSync(file), `decrypted tree: ${JSON.stringify(fs.readdirSync(out))}`).toBe(true);
    expect(fs.readFileSync(file, 'utf8')).toBe(SECRET_BODY);
    fs.rmSync(work, { recursive: true, force: true });
  });
});

