/**
 * 212 - the vault (encryption level 3), in a real browser against a real
 * binary (docs/E2E-VAULT-FORMAT.md; task #94).
 *
 * The unit suites prove the format byte for byte (web/tests/lib/e2evault*),
 * the lock's clocks (e2evaultLock.test.ts) and the explorer's vault mode
 * against a server in memory (composables/useE2eVault.test.ts). This file
 * proves what only the browser, the server and the disk together can:
 *
 *   1. a vault is made from the New folder dialog (level 3, 4 MiB packs) and
 *      the server's disk holds a key file, index files and packs of exactly
 *      4 MiB - never a name, a folder or a size the person sees;
 *   2. two tabs of the same person are two sessions: while one writes the
 *      other reads, says who writes, and cannot write;
 *   3. the writer that does nothing for its idle time (1 minute here, the
 *      person's own setting) goes back to read-only and says so; the other
 *      tab can then write;
 *   4. a vault opened and not used for 15 minutes locks itself: the keys
 *      leave memory, the lock screen asks for the password again (the page's
 *      clock is moved forward: Playwright `page.clock`).
 *
 * ⚠ Runs on a SECOND filex with the vault on (`FILEX_E2E_VAULT=1`): the vault
 * is off by default until it ships, and the shared hermetic instance keeps
 * the default (spec 172 counts two levels there). Needs E2E_FILEX_BIN, like
 * spec 173's escrow instance.
 */
import { test, expect, type APIRequestContext, type Browser, type Page } from '@playwright/test';
import { spawn } from 'node:child_process';
import fs from 'node:fs';
import { createServer } from 'node:net';
import os from 'node:os';
import path from 'node:path';
import { loginAs } from '../helpers/auth';
import { newAuthedRequest, seedLocalStorage, storageRoot } from '../helpers/seed';
import { setAccountViewMode } from '../helpers/prefs';
import { settled } from '../helpers/stable';

const STORE = `e2e-vault-${Date.now()}`;
const MOUNT = `/tmp/filex-${STORE}`;
const PW = 'correct horse battery staple';
const PREFS = '/api/me/prefs?surface=web';
const VAULT = 'Kasa';
const FOLDER = 'Gizli belgeler 2027';
const FILE_NAME = 'Maaş bordrosu - Ekim.txt';
const FILE_BODY = 'içerik: yalnız kasada, 2027\n'.repeat(40);

function freePort(): Promise<number> {
  return new Promise((resolve, reject) => {
    const srv = createServer();
    srv.once('error', reject);
    srv.listen(0, '127.0.0.1', () => {
      const addr = srv.address();
      const port = typeof addr === 'object' && addr ? addr.port : 0;
      srv.close(() => resolve(port));
    });
  });
}

/** A filex with the vault on, its own data directory, the same admin. */
async function bootVaultInstance(bin: string): Promise<{ base: string; stop: () => Promise<void> }> {
  const scratch = fs.mkdtempSync(path.join(os.tmpdir(), 'filex-vault-'));
  const dataDir = path.join(scratch, 'data');
  fs.mkdirSync(dataDir);
  const port = await freePort();
  const base = `http://127.0.0.1:${port}`;
  const logFile = path.join(scratch, 'server.log');
  const logFd = fs.openSync(logFile, 'a');
  const child = spawn(bin, ['serve'], {
    env: {
      ...process.env,
      FILEX_LISTEN: `127.0.0.1:${port}`,
      FILEX_DATA_DIR: dataDir,
      FILEX_PUBLIC_URL: base,
      FILEX_ADMIN_EMAIL: process.env.E2E_ADMIN_EMAIL ?? 'admin@local',
      FILEX_ADMIN_PASSWORD: process.env.E2E_ADMIN_PASSWORD ?? 'admin',
      FILEX_DEFAULT_LOCALE: 'en',
      FILEX_SECRET_KEY: 'e2e-vault-key-not-a-real-secret',
      FILEX_E2E_VAULT: '1',
    },
    stdio: ['ignore', logFd, logFd],
    windowsHide: true,
  });
  let exited: string | null = null;
  child.on('exit', (code, signal) => {
    exited = signal ? `signal ${signal}` : `code ${code}`;
  });
  const stop = async () => {
    if (exited === null) child.kill();
    for (let i = 0; i < 50 && exited === null; i++) await new Promise((r) => setTimeout(r, 100));
    try {
      fs.closeSync(logFd);
    } catch {
      /* closed already */
    }
    fs.rmSync(scratch, { recursive: true, force: true });
  };
  const until = Date.now() + 60_000;
  for (;;) {
    if (exited) {
      const log = fs.readFileSync(logFile, 'utf8').slice(-1500);
      await stop();
      throw new Error(`the vault instance exited during start-up (${exited})\n${log}`);
    }
    try {
      if ((await fetch(`${base}/healthz`)).ok) break;
    } catch {
      /* not up yet */
    }
    if (Date.now() > until) {
      await stop();
      throw new Error(`the vault instance never answered /healthz on ${base}`);
    }
    await new Promise((r) => setTimeout(r, 300));
  }
  return { base, stop };
}

/** Every file under the vault folder on the server's disk: relative path → size. */
function onDisk(): Record<string, number> {
  const root = path.join(storageRoot(MOUNT), VAULT);
  const out: Record<string, number> = {};
  const walk = (dir: string, rel: string) => {
    for (const e of fs.readdirSync(dir, { withFileTypes: true })) {
      const r = rel ? `${rel}/${e.name}` : e.name;
      if (e.isDirectory()) walk(path.join(dir, e.name), r);
      else out[r] = fs.statSync(path.join(dir, e.name)).size;
    }
  };
  if (fs.existsSync(root)) walk(root, '');
  return out;
}

/** Every byte the server holds for the vault, as one buffer (for a search). */
function allBytes(): Buffer {
  const root = path.join(storageRoot(MOUNT), VAULT);
  return Buffer.concat(Object.keys(onDisk()).map((r) => fs.readFileSync(path.join(root, r))));
}

function row(page: Page, name: string) {
  const re = new RegExp(`^${name.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')}$`);
  return page.locator('.fe-list__row[data-fe-path]').filter({ has: page.locator('.fe-list__name', { hasText: re }) }).first();
}

async function openStorage(page: Page) {
  await page.addInitScript(() => localStorage.setItem('filex.tourDone', '1'));
  await loginAs(page);
  await setAccountViewMode(page.request, 'list');
  await page.goto(`/drive/explore?storage=${encodeURIComponent(STORE)}`);
  await expect(page.getByTestId('sidenav-new').first()).toBeVisible({ timeout: 30_000 });
}

async function newMenu(page: Page, item: RegExp) {
  await page.getByTestId('sidenav-new').first().click();
  await page.locator('.fe-ctx__item', { has: page.locator('.fe-ctx__label', { hasText: item }) }).first().click();
}

async function openFolder(page: Page, name: string) {
  const target = await settled(row(page, name));
  await target.dblclick();
  await target.dispose();
  await page.waitForTimeout(700);
}

async function newFolderHere(page: Page, name: string) {
  await newMenu(page, /^New folder$/);
  const dialog = page.getByRole('dialog', { name: 'New folder' });
  await expect(dialog).toBeVisible();
  await dialog.getByRole('textbox').fill(name);
  await dialog.getByRole('button', { name: 'Create', exact: true }).click();
}

async function unlock(page: Page) {
  const lock = page.locator('.fe-e2e-lock');
  await expect(lock).toBeVisible({ timeout: 20_000 });
  await lock.locator('input[type="password"]').fill(PW);
  await lock.getByRole('button', { name: 'Unlock', exact: true }).click();
  await expect(page.getByTestId('e2e-vault-strip')).toBeVisible({ timeout: 30_000 });
}

async function shot(page: Page, name: string) {
  const dir = process.env.E2E_SHOTS;
  if (!dir) return;
  fs.mkdirSync(dir, { recursive: true });
  await page.waitForTimeout(300);
  await page.screenshot({ path: path.join(dir, name) });
}

async function newTab(browser: Browser, base: string): Promise<Page> {
  const ctx = await browser.newContext({ baseURL: base, viewport: { width: 1280, height: 860 } });
  return ctx.newPage();
}

test.describe.serial('E2E vault (level 3) - what the server keeps, one writer at a time, the two clocks', () => {
  let inst: { base: string; stop: () => Promise<void> } | null = null;
  let api: APIRequestContext;
  let writer: Page;
  let reader: Page;

  test.beforeAll(async ({ playwright }) => {
    test.setTimeout(120_000);
    const bin = process.env.E2E_FILEX_BIN ?? '';
    test.skip(!bin || !fs.existsSync(bin), 'no binary to boot a vault installation with (E2E_FILEX_BIN)');
    inst = await bootVaultInstance(bin);
    api = await newAuthedRequest(playwright, inst.base);
    await seedLocalStorage(api, STORE, MOUNT);
    const caps = await (await api.get('/api/files/capabilities')).json();
    expect(caps?.e2e_vault, 'the instance has the vault on').toBe(true);
    expect((await api.put(PREFS, { data: { prefs: { locale: 'en' } } })).ok()).toBe(true);
    // The person's idle time for a vault: 1 minute (1-10; 3 by default).
    const idle = await api.put('/api/files/e2e/vault/prefs', { data: { idle_minutes: 1 } });
    expect(idle.ok(), `prefs: ${idle.status()} ${await idle.text()}`).toBe(true);
    expect((await idle.json()).idle_minutes).toBe(1);
  });

  test.afterAll(async () => {
    await writer?.context().close().catch(() => undefined);
    await reader?.context().close().catch(() => undefined);
    await api?.dispose();
    await inst?.stop();
  });

  test('a vault is made at level 3; the server keeps a key file, an index and nothing it can read', async ({ browser }) => {
    test.setTimeout(180_000);
    writer = await newTab(browser, inst!.base);
    await openStorage(writer);
    await newMenu(writer, /^New folder$/);
    await writer.getByText('Create encrypted folder…', { exact: true }).click();
    const dialog = writer.locator('.fe-modal__card').filter({ has: writer.getByTestId('e2e-create-names') });
    await expect(dialog).toBeVisible();
    await expect(dialog.locator('input[type="radio"]'), 'three levels where the server has vaults').toHaveCount(3);
    await dialog.locator('input[type="text"]').first().fill(VAULT);
    const pws = dialog.locator('input[type="password"]');
    await pws.nth(0).fill(PW);
    await pws.nth(1).fill(PW);
    await dialog.getByTestId('e2e-level-vault').check();
    await expect(dialog.getByTestId('e2e-level-vault-cost')).toBeVisible();
    await expect(dialog.getByTestId('e2e-vault-pack')).toBeVisible();
    await shot(writer, 'vault-level-picker.png');
    await dialog.getByTestId('e2e-create-ack').check();
    await dialog.getByRole('button', { name: 'Create encrypted folder', exact: true }).click();
    const keyEl = writer.locator('.fe-e2e-rk__key');
    await expect(keyEl).toBeVisible({ timeout: 30_000 });
    await writer.locator('.fe-e2e-rk .fe-e2e-ack input[type="checkbox"]').check();
    await writer.getByRole('button', { name: 'Done', exact: true }).click();

    // The key file and generation 1, nothing else.
    await expect.poll(() => Object.keys(onDisk()).sort(), { timeout: 20_000 }).toEqual(['.filex-e2e.json', 'v/idx/0000000000000001.fxi']);
    const marker = JSON.parse(fs.readFileSync(path.join(storageRoot(MOUNT), VAULT, '.filex-e2e.json'), 'utf8'));
    expect(marker.v).toBe(3);
    expect(marker.req).toEqual(['vault']);
    expect(marker.vault).toMatchObject({ v: 1, pack: 22 });
    expect(onDisk()['v/idx/0000000000000001.fxi']).toBe(65536);
  });

  test('the first write takes the lock; folders and names never reach the disk', async () => {
    test.setTimeout(180_000);
    await openFolder(writer, VAULT);
    const strip = writer.getByTestId('e2e-vault-strip');
    await expect(strip).toBeVisible({ timeout: 20_000 });
    await expect(strip).toHaveAttribute('data-mode', 'read');
    await shot(writer, 'vault-strip-read.png');

    await newFolderHere(writer, FOLDER);
    await expect(row(writer, FOLDER)).toBeVisible({ timeout: 30_000 });
    await expect(strip).toHaveAttribute('data-mode', 'write');
    await expect(writer.getByTestId('e2e-vault-sentence')).toContainText('You are writing to this vault');
    await shot(writer, 'vault-strip-write.png');

    await openFolder(writer, FOLDER);
    await writer.locator('input[type="file"]').first().setInputFiles({
      name: FILE_NAME,
      mimeType: 'text/plain',
      buffer: Buffer.from(FILE_BODY, 'utf8'),
    });
    await expect(row(writer, FILE_NAME)).toBeVisible({ timeout: 60_000 });

    // On disk: the key file, index files and packs of exactly 4 MiB.
    const files = onDisk();
    for (const [rel, size] of Object.entries(files)) {
      if (rel === '.filex-e2e.json') continue;
      if (rel.startsWith('v/idx/')) expect(rel).toMatch(/^v\/idx\/[0-9a-f]{16}\.fxi$/);
      else {
        expect(rel).toMatch(/^v\/p\/([0-9a-f]{2})\/\1[0-9a-f]{30}\.fxp$/);
        expect(size, rel).toBe(4 * 1024 * 1024);
      }
    }
    // Not a name, not a byte of the contents, anywhere.
    const bytes = allBytes();
    for (const needle of [FOLDER, FILE_NAME, 'Maaş', 'yalnız kasada']) {
      expect(bytes.includes(Buffer.from(needle, 'utf8')), needle).toBe(false);
    }
    expect(Object.keys(files).some((r) => r.includes('Gizli') || r.includes('Maa'))).toBe(false);
  });

  test('a second tab of the same person reads while the first writes, and cannot write', async ({ browser }) => {
    test.setTimeout(180_000);
    reader = await newTab(browser, inst!.base);
    await openStorage(reader);
    await openFolder(reader, VAULT);
    await unlock(reader);
    const strip = reader.getByTestId('e2e-vault-strip');
    await expect(strip).toHaveAttribute('data-mode', 'read');
    // It says who writes (after its first poll of the state, at most 30 s).
    await expect(reader.getByTestId('e2e-vault-take-over')).toBeVisible({ timeout: 45_000 });
    await shot(reader, 'vault-strip-held.png');
    // It reads: the file opens, decrypted, while the other tab holds the lock.
    await openFolder(reader, FOLDER);
    await row(reader, FILE_NAME).dblclick();
    await expect(reader.getByText('yalnız kasada').first()).toBeVisible({ timeout: 30_000 });
    await reader.keyboard.press('Escape');
    // It cannot write: the change is refused and nothing is committed.
    const before = Object.keys(onDisk()).filter((r) => r.startsWith('v/idx/')).length;
    await newFolderHere(reader, 'from the reader');
    // The server's sentence (server.e2e.vault.locked, 0.54 #209), not a copy.
    await expect(reader.locator('.fe-toast', { hasText: /is writing (to )?this vault/ })).toBeVisible({ timeout: 20_000 });
    expect(Object.keys(onDisk()).filter((r) => r.startsWith('v/idx/')).length).toBe(before);
  });

  test('the writer that does nothing for its idle time goes back to read-only; the reader can then write', async () => {
    test.setTimeout(240_000);
    const strip = writer.getByTestId('e2e-vault-strip');
    // Idle time 1 minute, then the next heartbeat (15 s) hears it.
    await expect(strip).toHaveAttribute('data-mode', 'read', { timeout: 120_000 });
    await expect(writer.getByTestId('e2e-vault-sentence')).toHaveText(
      'You did nothing for 1 minute, so this vault went back to read-only. Nothing was lost.',
    );
    await shot(writer, 'vault-strip-idle.png');
    // The lock is free: the reader's next change takes it.
    await reader.getByRole('dialog', { name: 'New folder' }).getByRole('button', { name: 'Cancel' }).click().catch(() => undefined);
    await newFolderHere(reader, 'Sonra');
    await expect(row(reader, 'Sonra')).toBeVisible({ timeout: 30_000 });
    await expect(reader.getByTestId('e2e-vault-strip')).toHaveAttribute('data-mode', 'write');
  });

  test('a vault opened and not used for 15 minutes locks itself and asks for the password again', async ({ browser }) => {
    test.setTimeout(180_000);
    const page = await newTab(browser, inst!.base);
    await page.clock.install();
    await openStorage(page);
    await openFolder(page, VAULT);
    await unlock(page);
    await expect(page.getByTestId('e2e-vault-lock-countdown')).toBeVisible();
    await page.clock.fastForward('14:00');
    await expect(page.getByTestId('e2e-vault-strip')).toBeVisible();
    await page.clock.fastForward('01:30');
    await expect(page.locator('.fe-e2e-lock')).toBeVisible({ timeout: 20_000 });
    await expect(page.getByTestId('e2e-vault-strip')).toBeHidden();
    await shot(page, 'vault-locked-idle.png');
    // The password opens it again.
    await unlock(page);
    await page.context().close();
  });
});
