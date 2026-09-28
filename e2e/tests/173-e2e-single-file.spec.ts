/**
 * 173 — single encrypted files (`.fxe`) and large encrypted-folder files,
 * measured on what the SERVER stores and on what the person gets back.
 *
 * docs/E2E-ENCRYPTION.md → "Single encrypted files", "Streaming content",
 * "Where a decrypted download goes". The unit suites pin the formats to an
 * independent implementation (web/tests/lib/e2estream.test.ts,
 * e2efile.test.ts); this file proves what only a browser against a real
 * binary can:
 *
 *   1. a plain file is encrypted from its context menu: the storage directory
 *      then holds `filexfxe` bytes and not the original, and the recovery key
 *      is shown once;
 *   2. it opens (the preview decrypts), downloads decrypted under its ORIGINAL
 *      name — through whichever save sink this engine has — and downloads
 *      encrypted byte for byte;
 *   3. its password changes (the old one fails, the new one works, the body is
 *      untouched), `filex decrypt` reads it, and "Remove encryption" puts the
 *      plaintext back;
 *   4. a hidden-name `.fxe` of a file above the chunk size (staged, streamed);
 *   5. a file over 200 MB in an encrypted folder is a STREAM (0x02) file, and
 *      comes back whole — alone, and in the decrypted zip of the folder;
 *   6. on an installation with key escrow (a second instance this file boots),
 *      the operator opens a `.fxe` with the escrow key and its OWNER is told;
 *   7. a password change is announced (the owner's bell, the audit log) and
 *      seen by the server itself (e2e.fxe_header_rewritten), and no version
 *      keeps the old header;
 *   8. an administrator can delete the original for good while encrypting it
 *      — its versions and its trash entry — with a second confirmation when
 *      the file is someone else's;
 *   9. over 1 GB, Firefox and WebKit say so before asking for a password and
 *      hand over the encrypted file and the `filex decrypt` command; Chromium
 *      streams it;
 *  10. a `.fxe` uploaded into a level-2 folder is one of its files: a name
 *      sealed for the folder (re-sealed on rename and move), the folder key
 *      over its bytes, and back byte for byte — downloaded, zipped, and out of
 *      `filex decrypt`.
 *
 * Engines: Chromium by default; `E2E_BROWSERS=chromium,firefox,webkit` runs it
 * in all three (e2e/playwright.config.ts). Chromium saves through the File
 * System Access API — a stub over the origin-private file system stands in for
 * the native save dialog, which a headless run cannot answer; Firefox and
 * WebKit have no such API and save through the in-memory Blob path, which
 * Playwright sees as a download. Each test asserts which path it took.
 *
 * ⚠ Language pinned to English on the ACCOUNT and restored (lesson #616).
 */
import { test, expect, type APIRequestContext, type Page, type Download } from '@playwright/test';
import { spawn, spawnSync } from 'node:child_process';
import { createHash, randomBytes } from 'node:crypto';
import fs from 'node:fs';
import { createServer } from 'node:net';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { loginAs } from '../helpers/auth';
import { dropStorageByName, newAuthedRequest, seedLocalStorage, storageRoot } from '../helpers/seed';
import { setAccountViewMode } from '../helpers/prefs';
import { settled } from '../helpers/stable';

const STORE = `e2e-fxe-${Date.now()}`;
const MOUNT = `/tmp/filex-${STORE}`;
const PW = 'a file password, long enough';
const PW2 = 'the second file password';
const PREFS = '/api/me/prefs?surface=web';
const SHOTS = process.env.E2E_FXE_SHOTS || '';

const TEXT_NAME = 'Rapor 2027 — gizli.txt';
const TEXT_BODY = 'içerik: çok gizli rapor, 2027\n'.repeat(40);
/** Above the explorer's chunk size (8 MiB): the staged, streamed path. */
const MID_NAME = 'Sunum kaydı.bin';
const MID_SIZE = 20 * 1024 * 1024 + 12_345;
/** Over the one-shot limit: a STREAM (0x02) file in an encrypted folder. */
const BIG_NAME = 'Büyük arşiv.bin';
const BIG_SIZE = 200 * 1024 * 1024 + 7 * 1024 * 1024 + 321;

let prefsBefore: Record<string, unknown> = {};
let recoveryKey = '';
const midBytes = randomBytes(MID_SIZE);
let bigPath = '';
let bigSha = '';

const sha = (b: Uint8Array | Buffer) => createHash('sha256').update(b).digest('hex');

function disk(rel = ''): string[] {
  return fs
    .readdirSync(path.join(storageRoot(MOUNT), rel))
    .filter((n) => n !== '.keepdir' && !n.startsWith('.filex-'))
    .sort();
}

function onDisk(rel: string): Buffer {
  return fs.readFileSync(path.join(storageRoot(MOUNT), rel));
}

async function pollDisk(rel: string, want: (names: string[]) => boolean, what: string, timeout = 60_000) {
  let names: string[] = [];
  await expect
    .poll(
      () => {
        names = disk(rel);
        return want(names);
      },
      { message: `${what} — on disk: ${JSON.stringify(names)}`, timeout },
    )
    .toBe(true);
  return names;
}

async function shot(page: Page, name: string) {
  if (!SHOTS) return;
  fs.mkdirSync(SHOTS, { recursive: true });
  // The context menu fades out and the dialog fades in: wait for both, or the
  // picture shows a half-transparent mix of the two (lesson #584).
  await expect(page.getByRole('menu')).toHaveCount(0, { timeout: 5_000 }).catch(() => undefined);
  await page.waitForTimeout(700);
  await page.screenshot({ path: path.join(SHOTS, `${test.info().project.name}-${name}.png`) });
}

/**
 * Chromium: a stand-in for the native save dialog, over the origin-private
 * file system — the REAL FileSystemWritableFileStream underneath, so the
 * write path is the product's; the stub only answers the dialog. Records
 * every save (name, how many writes, bytes).
 */
async function installSaveStub(page: Page) {
  await page.addInitScript(() => {
    const w = window as unknown as Record<string, unknown>;
    w.__nativeSaveDialog = typeof (window as unknown as { showSaveFilePicker?: unknown }).showSaveFilePicker;
    const saves: Array<{ name: string; writes: number; bytes: number; closed: boolean; aborted: boolean }> = [];
    w.__fxeSaves = saves;
    w.showSaveFilePicker = async (opts?: { suggestedName?: string }) => {
      const name = opts?.suggestedName ?? 'download';
      const root = await navigator.storage.getDirectory();
      const handle = await root.getFileHandle(name, { create: true });
      const rec = { name, writes: 0, bytes: 0, closed: false, aborted: false };
      saves.push(rec);
      return {
        async createWritable() {
          const inner = await (handle as unknown as { createWritable(): Promise<WritableStreamDefaultWriter & { write(d: unknown): Promise<void>; close(): Promise<void>; abort(r?: unknown): Promise<void> }> }).createWritable();
          return {
            write: (d: Uint8Array) => {
              rec.writes++;
              rec.bytes += d.byteLength;
              return inner.write(d);
            },
            close: async () => {
              await inner.close();
              rec.closed = true;
            },
            abort: (r?: unknown) => {
              rec.aborted = true;
              return inner.abort(r);
            },
          };
        },
        remove: () => root.removeEntry(name),
      };
    };
  });
}

/** SHA-256 of a file the stub saved, read back from the private file system. */
async function savedSha(page: Page, name: string): Promise<{ sha: string; size: number; writes: number }> {
  return page.evaluate(async (n) => {
    const root = await navigator.storage.getDirectory();
    const file = await (await root.getFileHandle(n)).getFile();
    const digest = new Uint8Array(await crypto.subtle.digest('SHA-256', await file.arrayBuffer()));
    const saves = (window as unknown as { __fxeSaves: Array<{ name: string; writes: number }> }).__fxeSaves;
    const rec = saves.filter((s) => s.name === n).pop();
    return {
      sha: Array.from(digest, (x) => x.toString(16).padStart(2, '0')).join(''),
      size: file.size,
      writes: rec?.writes ?? 0,
    };
  }, name);
}

const isChromium = () => test.info().project.name === 'chromium';

/**
 * Run `act`, and get back what was saved: through the stub on Chromium, as a
 * Playwright download elsewhere. Asserts the engine took the path it has.
 */
async function saveOf(
  page: Page,
  name: string,
  act: () => Promise<void>,
): Promise<{ sha: string; size: number; sink: 'fsa' | 'blob'; writes?: number; file?: string }> {
  if (isChromium()) {
    await act();
    await expect
      .poll(() => page.evaluate((n) => ((window as unknown as { __fxeSaves: Array<{ name: string; closed: boolean }> }).__fxeSaves ?? []).some((s) => s.name === n && s.closed), name), {
        timeout: 180_000,
        message: `${name} saved through the File System Access path`,
      })
      .toBe(true);
    const got = await savedSha(page, name);
    return { ...got, sink: 'fsa' };
  }
  expect(await page.evaluate(() => typeof (window as unknown as { showSaveFilePicker?: unknown }).showSaveFilePicker)).toBe('undefined');
  const [dl] = await Promise.all([page.waitForEvent('download', { timeout: 180_000 }), act()]);
  expect(dl.suggestedFilename()).toBe(name);
  const file = (await dl.path())!;
  const b = fs.readFileSync(file);
  return { sha: sha(b), size: b.length, sink: 'blob', file };
}

/** A zip's entry names and one entry's SHA-256, from its central directory. */
function zipInside(file: string, entry: string): { names: string[]; entrySha: string } {
  const u8 = fs.readFileSync(file);
  const eocd = u8.length - 22;
  const count = u8.readUInt16LE(eocd + 10);
  let o = u8.readUInt32LE(eocd + 16);
  const names: string[] = [];
  let entrySha = '';
  for (let i = 0; i < count; i++) {
    const size = u8.readUInt32LE(o + 24);
    const nameLen = u8.readUInt16LE(o + 28);
    const extraLen = u8.readUInt16LE(o + 30);
    const commentLen = u8.readUInt16LE(o + 32);
    const local = u8.readUInt32LE(o + 42);
    const name = u8.subarray(o + 46, o + 46 + nameLen).toString('utf8');
    names.push(name);
    if (name === entry) {
      const start = local + 30 + u8.readUInt16LE(local + 26) + u8.readUInt16LE(local + 28);
      entrySha = sha(u8.subarray(start, start + size));
    }
    o += 46 + nameLen + extraLen + commentLen;
  }
  return { names, entrySha };
}

/** A download the explorer starts with `window.open`: some engines attribute
 *  it to the page, others to the popup it opened — take whichever comes. */
async function rawDownload(page: Page, act: () => Promise<void>): Promise<Download> {
  const got = new Promise<Download>((resolve) => {
    page.once('download', resolve);
    page.context().once('page', (popup) => popup.once('download', resolve));
  });
  await act();
  return Promise.race([
    got,
    new Promise<Download>((_, reject) => setTimeout(() => reject(new Error('no download within 60 s')), 60_000)),
  ]);
}

async function openStorage(page: Page, store = STORE, who?: { email: string; password: string }) {
  if (isChromium()) await installSaveStub(page);
  await page.addInitScript(() => localStorage.setItem('filex.tourDone', '1'));
  await loginAs(page, who?.email, who?.password);
  await setAccountViewMode(page.request, 'list');
  await page.goto(`/drive/explore?storage=${encodeURIComponent(store)}`);
  await expect(page.getByTestId('sidenav-new').first()).toBeVisible({ timeout: 30_000 });
}

function row(page: Page, name: string) {
  return page
    .locator('.fe-list__row[data-fe-path]')
    .filter({ has: page.locator('.fe-list__name', { hasText: new RegExp(`^${escapeRe(name)}$`) }) })
    .first();
}

function escapeRe(s: string): string {
  return s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
}

async function rowVerb(page: Page, name: string, verb: RegExp) {
  const target = await settled(row(page, name));
  await target.click({ button: 'right' });
  await target.dispose();
  const menu = page.getByRole('menu').first();
  await expect(menu).toBeVisible();
  await menu.getByRole('menuitem', { name: verb }).click();
}

async function openFolder(page: Page, name: string) {
  const target = await settled(row(page, name));
  await target.dblclick();
  await target.dispose();
  await page.waitForTimeout(700);
}

async function putFile(api: APIRequestContext, dir: string, name: string, body: Buffer) {
  const r = await api.post('/api/files/manager?action=upload', {
    multipart: { path: `${STORE}://${dir}`, 'file[]': { name, mimeType: 'application/octet-stream', buffer: body } },
  });
  expect(r.ok(), `upload ${name}: ${r.status()}`).toBe(true);
}

/** The decrypted text, wherever the viewer draws it (a text file opens in the
 *  code viewer, read-only). */
const previewText = (page: Page) => page.getByText('içerik: çok gizli rapor, 2027').first();

const unlockDialog = (page: Page) => page.locator('.fe-modal__card').filter({ has: page.getByTestId('fxe-unlock-form') });

// ── the four follow-ups (escrow, announced password changes, an operator's
//    "delete for good", the in-memory limit): helpers ─────────────────────

const HERE = path.dirname(fileURLToPath(import.meta.url));
const ESCROW_KEY = JSON.parse(fs.readFileSync(path.join(HERE, '../../web/tests/fixtures/escrow-testkey.json'), 'utf8')) as {
  public_spki_b64: string;
  private_pkcs8_b64: string;
};

/** The core library, built (packages/core/dist): a real `.fxe` without a browser. */
type CoreFxe = {
  createFxe(
    name: string,
    size: number,
    src: ReadableStream<Uint8Array>,
    password: string,
    opts?: { escrowPublicKey?: string | null },
  ): Promise<{ stream: ReadableStream<Uint8Array>; size: number }>;
};
async function coreFxe(): Promise<CoreFxe> {
  return (await import(pathToFileURL(path.join(HERE, '../../packages/core/dist/filex-core.js')).href)) as CoreFxe;
}

/** A `.fxe` of `plain`, made by the product's own library. */
async function fxeOf(name: string, plain: Buffer, password: string, escrow = false): Promise<Buffer> {
  const core = await coreFxe();
  const created = await core.createFxe(
    name,
    plain.length,
    new ReadableStream<Uint8Array>({
      start(c) {
        c.enqueue(new Uint8Array(plain));
        c.close();
      },
    }),
    password,
    { escrowPublicKey: escrow ? ESCROW_KEY.public_spki_b64 : null },
  );
  const parts: Buffer[] = [];
  for await (const piece of created.stream as unknown as AsyncIterable<Uint8Array>) parts.push(Buffer.from(piece));
  return Buffer.concat(parts);
}

async function putFileTo(api: APIRequestContext, store: string, dir: string, name: string, body: Buffer) {
  const r = await api.post('/api/files/manager?action=upload', {
    multipart: { path: `${store}://${dir}`, 'file[]': { name, mimeType: 'application/octet-stream', buffer: body } },
  });
  expect(r.ok(), `upload ${name}: ${r.status()} ${await r.text()}`).toBe(true);
}

type Row = Record<string, unknown>;

/** The bell's rows for the account behind `api`. */
async function notifications(api: APIRequestContext): Promise<Row[]> {
  const r = await api.get('/api/notifications?limit=100');
  expect(r.ok(), `notifications: ${r.status()}`).toBe(true);
  const body = (await r.json()) as { items?: Row[] };
  return body.items ?? [];
}

/** Audit rows of one action, newest first. */
async function auditRows(api: APIRequestContext, action: string): Promise<Row[]> {
  const r = await api.get(`/api/admin/audit?action=${encodeURIComponent(action)}&limit=100`);
  expect(r.ok(), `audit: ${r.status()}`).toBe(true);
  const body = (await r.json()) as Row[] | { items?: Row[]; entries?: Row[] };
  return Array.isArray(body) ? body : (body.items ?? body.entries ?? []);
}

/** The listing row the server has for `name` in `dir` (its node id among it). */
async function rowOf(api: APIRequestContext, store: string, dir: string, name: string): Promise<Row | undefined> {
  const r = await api.get(`/api/files/manager?action=index&path=${encodeURIComponent(`${store}://${dir}`)}`);
  expect(r.ok(), `index: ${r.status()}`).toBe(true);
  const files = ((await r.json()) as { files?: Row[] }).files ?? [];
  return files.find((f) => f.basename === name);
}

async function versionsOfNode(api: APIRequestContext, nodeId: number): Promise<Row[]> {
  const r = await api.get(`/api/files/versions?node_id=${nodeId}`);
  if (r.status() === 404) return [];
  expect(r.ok(), `versions: ${r.status()}`).toBe(true);
  return ((await r.json()) as { versions?: Row[] | null }).versions ?? [];
}

/** Every file under a storage's directory, its system folders included
 *  (`.versions/`, `.filex-trash/`): where a plaintext could be left. */
function everyFileUnder(root: string): string[] {
  const out: string[] = [];
  const walk = (d: string) => {
    for (const e of fs.readdirSync(d, { withFileTypes: true })) {
      const p = path.join(d, e.name);
      if (e.isDirectory()) walk(p);
      else if (e.isFile()) out.push(p);
    }
  };
  if (fs.existsSync(root)) walk(root);
  return out;
}

function holding(root: string, needle: string): string[] {
  const n = Buffer.from(needle, 'utf8');
  return everyFileUnder(root).filter((f) => fs.readFileSync(f).includes(n));
}

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

/**
 * A second filex, with key escrow ON (FILEX_INSTALLATION_E2E_ESCROW_KEY is an
 * install-time setting, so the hermetic instance cannot have it), keyed with
 * the committed TEST key pair (web/tests/fixtures/escrow-testkey.json).
 * Same admin as the hermetic one, its own data directory.
 */
async function bootEscrowInstance(bin: string): Promise<{ base: string; stop: () => Promise<void> }> {
  const scratch = fs.mkdtempSync(path.join(os.tmpdir(), 'filex-fxe-escrow-'));
  const dataDir = path.join(scratch, 'data');
  fs.mkdirSync(dataDir);
  const port = await freePort();
  const base = `http://127.0.0.1:${port}`;
  const logFile = path.join(scratch, 'server.log');
  const logFd = fs.openSync(logFile, 'a');
  // ⚠ Output to a file, not a pipe nobody drains (e2e/run.mjs).
  const child = spawn(bin, ['serve'], {
    env: {
      ...process.env,
      FILEX_LISTEN: `127.0.0.1:${port}`,
      FILEX_DATA_DIR: dataDir,
      FILEX_PUBLIC_URL: base,
      FILEX_ADMIN_EMAIL: process.env.E2E_ADMIN_EMAIL ?? 'admin@local',
      FILEX_ADMIN_PASSWORD: process.env.E2E_ADMIN_PASSWORD ?? 'admin',
      FILEX_DEFAULT_LOCALE: 'en',
      FILEX_SECRET_KEY: 'e2e-fxe-escrow-key-not-a-real-secret',
      FILEX_INSTALLATION_E2E_ESCROW_KEY: ESCROW_KEY.public_spki_b64,
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
      throw new Error(`the escrow instance exited during start-up (${exited})\n${log}`);
    }
    try {
      if ((await fetch(`${base}/healthz`)).ok) break;
    } catch {
      /* not up yet */
    }
    if (Date.now() > until) {
      await stop();
      throw new Error(`the escrow instance never answered /healthz on ${base}`);
    }
    await new Promise((r) => setTimeout(r, 300));
  }
  return { base, stop };
}

test.describe.serial('E2E single encrypted files — what the server keeps', () => {
  let api: APIRequestContext;

  test.beforeAll(async ({ request, playwright, baseURL }) => {
    await dropStorageByName(request, STORE);
    await seedLocalStorage(request, STORE, MOUNT);
    api = await newAuthedRequest(playwright, baseURL ?? '');
    const got = await api.get(PREFS);
    const doc = got.ok() ? (await got.json()).prefs : {};
    prefsBefore = doc && typeof doc === 'object' && !Array.isArray(doc) ? doc : {};
    expect((await api.put(PREFS, { data: { prefs: { ...prefsBefore, locale: 'en' } } })).ok()).toBe(true);
    await putFile(api, '', TEXT_NAME, Buffer.from(TEXT_BODY, 'utf8'));
    await putFile(api, '', MID_NAME, midBytes);
  });

  test.afterAll(async ({ request }) => {
    await api?.put(PREFS, { data: { prefs: prefsBefore } }).catch(() => undefined);
    await api?.dispose();
    await dropStorageByName(request, STORE);
    if (bigPath) fs.rmSync(bigPath, { force: true });
  });

  test('a file is encrypted from its context menu; the server keeps only filexfxe bytes', async ({ page }) => {
    test.setTimeout(180_000);
    await openStorage(page);
    await expect(row(page, TEXT_NAME)).toBeVisible({ timeout: 20_000 });
    await rowVerb(page, TEXT_NAME, /^Encrypt with E2EE…$/);
    const form = page.getByTestId('fxe-encrypt-form');
    await expect(form).toBeVisible();
    await expect(page.getByTestId('fxe-encrypt-seen'), 'the dialog says what the server already saw').toContainText('trash');
    await expect(page.getByTestId('fxe-encrypt-hide-hint')).toContainText(`${TEXT_NAME}.fxe`);
    await shot(page, 'encrypt-dialog');
    await page.getByTestId('fxe-encrypt-pw').fill(PW);
    await page.getByTestId('fxe-encrypt-pw2').fill(PW);
    await page.getByTestId('fxe-encrypt-submit').click();
    await expect(page.getByTestId('fxe-encrypt-error'), 'the acknowledgement is required').toBeVisible();
    await page.getByTestId('fxe-encrypt-ack').check();
    await page.getByTestId('fxe-encrypt-submit').click();

    const keyEl = page.locator('.fe-e2e-rk__key');
    await expect(keyEl).toBeVisible({ timeout: 60_000 });
    await expect(page.locator('.fe-e2e-rk .fe-e2e-rk__lead')).toContainText('opens the file without its password');
    await shot(page, 'recovery-key');
    recoveryKey = (await keyEl.innerText()).trim();
    expect(recoveryKey).toMatch(/^([0-9A-Z]{4}-){7}[0-9A-Z]{4}$/);
    await page.locator('.fe-e2e-rk .fe-e2e-ack input[type="checkbox"]').check();
    await page.getByRole('button', { name: 'Done', exact: true }).click();

    // The server: the .fxe, not the original.
    await pollDisk('', (n) => n.includes(`${TEXT_NAME}.fxe`) && !n.includes(TEXT_NAME), 'the original left the folder');
    const stored = onDisk(`${TEXT_NAME}.fxe`);
    expect(stored.subarray(0, 8).toString()).toBe('filexfxe');
    expect(stored[8]).toBe(1);
    expect(stored.includes(Buffer.from('çok gizli', 'utf8')), 'no plaintext on disk').toBe(false);
    expect(stored.includes(Buffer.from(TEXT_NAME, 'utf8')), 'the name is sealed, not written').toBe(false);
    // This tab made it, so the row already reads its real name — with the
    // padlock tile, which is how it differs from the plain file it replaced.
    await expect(row(page, TEXT_NAME).locator('.fe-ftile--file-locked')).toBeVisible({ timeout: 20_000 });
  });

  test('it opens (the preview decrypts) and downloads — decrypted under its name, and encrypted as it is', async ({ page }) => {
    test.setTimeout(180_000);
    await openStorage(page);
    await openFolder(page, `${TEXT_NAME}.fxe`);
    const dialog = unlockDialog(page);
    await expect(dialog).toBeVisible();
    await page.getByTestId('fxe-unlock-pw').fill('not the password');
    await page.getByTestId('fxe-unlock-submit').click();
    await expect(page.getByTestId('fxe-unlock-error')).toHaveText('Wrong password.', { timeout: 30_000 });
    await shot(page, 'unlock-wrong-password');
    await page.getByTestId('fxe-unlock-pw').fill(PW);
    await page.getByTestId('fxe-unlock-submit').click();
    await expect(previewText(page), 'the preview shows the decrypted text').toBeVisible({ timeout: 30_000 });
    // …and describes the plaintext: its real name and its own size, not the .fxe's.
    await expect(page.getByText(/^1\.28 KB/).first(), 'the plaintext size in the viewer').toBeVisible();
    await shot(page, 'preview-decrypted');
    await page.keyboard.press('Escape');
    // Opened in this tab: the row is named for what it is.
    await expect(row(page, TEXT_NAME), 'the real name, once opened here').toBeVisible();

    const saved = await saveOf(page, TEXT_NAME, () => rowVerb(page, TEXT_NAME, /^Download$/));
    expect(saved.sha).toBe(sha(Buffer.from(TEXT_BODY, 'utf8')));
    test.info().annotations.push({ type: 'save-sink', description: `${test.info().project.name}: ${saved.sink}` });

    const raw = await rawDownload(page, () => rowVerb(page, TEXT_NAME, /^Download encrypted file$/));
    // The server names this download (Content-Disposition). WebKit on Windows
    // takes the header's ASCII fallback, where the em dash is "_" — measured
    // 2026-09-27; the decrypted save above, named by the page, keeps it.
    const rawName = `${TEXT_NAME}.fxe`;
    expect(raw.suggestedFilename()).toMatch(new RegExp(`^${escapeRe(rawName).replace('—', '[—_]')}$`));
    expect(sha(fs.readFileSync((await raw.path())!))).toBe(sha(onDisk(`${TEXT_NAME}.fxe`)));
  });

  test('its password changes: only the header, the old one opens nothing', async ({ page }) => {
    test.setTimeout(180_000);
    await openStorage(page);
    const before = onDisk(`${TEXT_NAME}.fxe`);
    const bodyOf = (b: Buffer) => b.subarray(13 + b.readUInt32BE(9));
    await rowVerb(page, `${TEXT_NAME}.fxe`, /^Change password…$/);
    const form = page.getByTestId('e2e-password-form');
    await expect(form).toBeVisible();
    await expect(page.getByTestId('e2e-password-cost')).toContainText('Only the file’s header changes');
    await expect(page.getByTestId('e2e-password-rotate'), 'a file has no folder key to replace').toHaveCount(0);
    await shot(page, 'change-password');
    await page.getByTestId('e2e-password-current').fill('wrong current password');
    await page.getByTestId('e2e-password-new').fill(PW2);
    await page.getByTestId('e2e-password-new2').fill(PW2);
    await page.getByTestId('e2e-password-submit').click();
    await expect(page.getByTestId('e2e-password-error')).toHaveText('Wrong password.', { timeout: 30_000 });
    await page.getByTestId('e2e-password-current').fill(PW);
    await page.getByTestId('e2e-password-submit').click();
    await expect(form).toBeHidden({ timeout: 60_000 });
    let after = before;
    await expect.poll(() => (after = onDisk(`${TEXT_NAME}.fxe`)).equals(before), { timeout: 30_000 }).toBe(false);
    expect(bodyOf(after).equals(bodyOf(before)), 'the body is byte for byte what it was').toBe(true);

    // A new tab knows nothing: the old password fails, the new one opens it.
    await page.reload();
    await expect(row(page, `${TEXT_NAME}.fxe`)).toBeVisible({ timeout: 20_000 });
    await openFolder(page, `${TEXT_NAME}.fxe`);
    await page.getByTestId('fxe-unlock-pw').fill(PW);
    await page.getByTestId('fxe-unlock-submit').click();
    await expect(page.getByTestId('fxe-unlock-error')).toHaveText('Wrong password.', { timeout: 30_000 });
    // …and the recovery key shown at encryption still opens it.
    await page.getByTestId('fxe-unlock-toggle').click();
    await page.getByTestId('fxe-unlock-recovery').fill(recoveryKey);
    await page.getByTestId('fxe-unlock-submit').click();
    await expect(previewText(page), 'the preview shows the decrypted text').toBeVisible({ timeout: 30_000 });
    await page.keyboard.press('Escape');
  });

  test('filex decrypt reads the .fxe the browser wrote — its name, its bytes, nothing on a wrong password', async () => {
    const bin = process.env.E2E_FILEX_BIN ?? '';
    test.skip(!bin || !fs.existsSync(bin), 'no binary to run (E2E_FILEX_BIN)');
    test.skip(!isChromium(), 'the binary is the same whichever browser wrote the file; measured once');
    const work = fs.mkdtempSync(path.join(os.tmpdir(), 'filex-fxe-decrypt-'));
    const input = path.join(work, `${TEXT_NAME}.fxe`);
    fs.copyFileSync(path.join(storageRoot(MOUNT), `${TEXT_NAME}.fxe`), input);
    for (const wrong of [PW, 'not it']) {
      const r = spawnSync(bin, ['decrypt', input, '--password-stdin'], { input: `${wrong}\n`, encoding: 'utf8' });
      expect(r.status, r.stderr).toBe(5);
      expect(fs.readdirSync(work)).toEqual([`${TEXT_NAME}.fxe`]);
    }
    const ok = spawnSync(bin, ['decrypt', input, '--password-stdin'], { input: `${PW2}\n`, encoding: 'utf8' });
    expect(ok.status, `${ok.stdout}\n${ok.stderr}`).toBe(0);
    expect(fs.readFileSync(path.join(work, TEXT_NAME), 'utf8')).toBe(TEXT_BODY);
    // Damaged: exit 6, still nothing written.
    const bad = fs.readFileSync(input);
    bad[bad.length - 5] ^= 1;
    const damaged = path.join(work, 'damaged.fxe');
    fs.writeFileSync(damaged, bad);
    const out = path.join(work, 'x.txt');
    const r6 = spawnSync(bin, ['decrypt', damaged, '-o', out, '--password-stdin'], { input: `${PW2}\n`, encoding: 'utf8' });
    expect(r6.status, r6.stderr).toBe(6);
    expect(fs.existsSync(out)).toBe(false);
    fs.rmSync(work, { recursive: true, force: true });
  });

  test('"Remove encryption" puts the plaintext back under its name and trashes the .fxe', async ({ page }) => {
    test.setTimeout(180_000);
    await openStorage(page);
    await rowVerb(page, `${TEXT_NAME}.fxe`, /^Remove encryption…$/);
    const dialog = unlockDialog(page);
    await expect(dialog).toBeVisible();
    await expect(page.getByTestId('fxe-remove-warn')).toContainText('The plaintext goes back to the server');
    await shot(page, 'remove-encryption');
    await page.getByTestId('fxe-unlock-pw').fill(PW2);
    await page.getByTestId('fxe-unlock-submit').click();
    await pollDisk('', (n) => n.includes(TEXT_NAME) && !n.includes(`${TEXT_NAME}.fxe`), 'the plaintext is back');
    expect(onDisk(TEXT_NAME).toString('utf8')).toBe(TEXT_BODY);
    await expect(row(page, TEXT_NAME)).toBeVisible({ timeout: 20_000 });
  });

  test('Turkish: the menu verb and the dialog speak correct Turkish', async ({ page }) => {
    test.skip(!isChromium(), 'the words are the same in every engine; looked at once');
    // The account's language, set and put back (lesson #616).
    expect((await api.put(PREFS, { data: { prefs: { ...prefsBefore, locale: 'tr' } } })).ok()).toBe(true);
    try {
      await openStorage(page);
      await expect(row(page, TEXT_NAME)).toBeVisible({ timeout: 20_000 });
      const target = await settled(row(page, TEXT_NAME));
      await target.click({ button: 'right' });
      await target.dispose();
      const menu = page.getByRole('menu').first();
      await expect(menu.getByRole('menuitem', { name: /^E2EE ile şifrele…$/ })).toBeVisible();
      await menu.getByRole('menuitem', { name: /^E2EE ile şifrele…$/ }).click();
      await expect(page.getByRole('dialog', { name: 'Bu dosyayı şifrele' })).toBeVisible();
      await expect(page.getByTestId('fxe-encrypt-seen')).toContainText('Sunucunun zaten gördükleri');
      await expect(page.getByTestId('fxe-encrypt-seen')).toContainText('çöp kutusuna');
      await shot(page, 'encrypt-dialog-tr');
      await page.keyboard.press('Escape');
    } finally {
      await api.put(PREFS, { data: { prefs: { ...prefsBefore, locale: 'en' } } });
    }
  });

  test('a hidden name, a file above the chunk size: staged and streamed both ways', async ({ page }) => {
    test.setTimeout(300_000);
    await openStorage(page);
    await rowVerb(page, MID_NAME, /^Encrypt with E2EE…$/);
    await page.getByTestId('fxe-encrypt-pw').fill(PW);
    await page.getByTestId('fxe-encrypt-pw2').fill(PW);
    await page.getByTestId('fxe-encrypt-hide').check();
    await expect(page.getByTestId('fxe-encrypt-hide-hint')).toContainText('encrypted-xxxxxxxx.fxe');
    await page.getByTestId('fxe-encrypt-ack').check();
    await page.getByTestId('fxe-encrypt-submit').click();
    await expect(page.locator('.fe-e2e-rk__key')).toBeVisible({ timeout: 180_000 });
    await page.locator('.fe-e2e-rk .fe-e2e-ack input[type="checkbox"]').check();
    await page.getByRole('button', { name: 'Done', exact: true }).click();

    const names = await pollDisk('', (n) => n.some((x) => /^encrypted-[0-9a-f]{8}\.fxe$/.test(x)) && !n.includes(MID_NAME), 'hidden-name .fxe');
    const hidden = names.find((x) => /^encrypted-[0-9a-f]{8}\.fxe$/.test(x))!;
    const stored = onDisk(hidden);
    expect(stored.subarray(0, 8).toString()).toBe('filexfxe');
    const header = JSON.parse(stored.subarray(13, 13 + stored.readUInt32BE(9)).toString('utf8'));
    expect(header.chunk).toBe(20);
    expect(header.size).toBe(MID_SIZE);
    // 21 chunks of 1 MiB (+ tags) after the header: many chunks, one file.
    expect(stored.length).toBe(13 + stored.readUInt32BE(9) + MID_SIZE + 16 * Math.ceil(MID_SIZE / 2 ** 20));
    // This tab made it, so it knows the real name already — and the plain
    // original is gone from the listing, so the one row by that name is it.
    await expect(row(page, MID_NAME).locator('.fe-ftile--file-locked')).toBeVisible({ timeout: 30_000 });
    await expect(
      page.locator('.fe-list__row[data-fe-path]').filter({ has: page.locator('.fe-list__name', { hasText: new RegExp(`^${escapeRe(MID_NAME)}$`) }) }),
    ).toHaveCount(1, { timeout: 30_000 });
    const saved = await saveOf(page, MID_NAME, () => rowVerb(page, MID_NAME, /^Download$/));
    expect(saved.size).toBe(MID_SIZE);
    expect(saved.sha).toBe(sha(midBytes));
    if (saved.sink === 'fsa') expect(saved.writes, 'written as it was decrypted, chunk by chunk').toBeGreaterThan(10);
  });

  test('a file over 200 MB in an encrypted folder is a STREAM file and comes back whole', async ({ page }) => {
    test.setTimeout(900_000);
    // A file on disk: Playwright hands a path to the page as a stream.
    bigPath = path.join(os.tmpdir(), `filex-fxe-big-${Date.now()}.bin`);
    const h = createHash('sha256');
    const fd = fs.openSync(bigPath, 'w');
    for (let left = BIG_SIZE; left > 0; ) {
      const piece = randomBytes(Math.min(left, 8 * 1024 * 1024));
      fs.writeSync(fd, piece);
      h.update(piece);
      left -= piece.length;
    }
    fs.closeSync(fd);
    bigSha = h.digest('hex');

    await openStorage(page);
    await page.getByTestId('sidenav-new').first().click();
    await page
      .locator('.fe-ctx__item', { has: page.locator('.fe-ctx__label', { hasText: /^New folder$/ }) })
      .first()
      .click();
    await page.getByText('Create encrypted folder…', { exact: true }).click();
    const create = page.locator('.fe-modal__card').filter({ has: page.getByTestId('e2e-create-names') });
    await create.locator('input[type="text"]').first().fill('Arşiv');
    const pws = create.locator('input[type="password"]');
    await pws.nth(0).fill(PW);
    await pws.nth(1).fill(PW);
    await create.getByTestId('e2e-create-ack').check();
    await create.getByRole('button', { name: 'Create encrypted folder', exact: true }).click();
    await expect(page.locator('.fe-e2e-rk__key')).toBeVisible({ timeout: 30_000 });
    await page.locator('.fe-e2e-rk .fe-e2e-ack input[type="checkbox"]').check();
    await page.getByRole('button', { name: 'Done', exact: true }).click();
    await openFolder(page, 'Arşiv');
    await expect(page.locator('.fe-e2e-strip')).toBeVisible({ timeout: 20_000 });

    // The picked file keeps the name on disk; give it the name the test reads.
    const named = path.join(path.dirname(bigPath), BIG_NAME);
    fs.renameSync(bigPath, named);
    bigPath = named;
    await page.locator('input[type="file"]').first().setInputFiles(bigPath);
    const [storedBig] = await pollDisk(
      'Arşiv',
      (n) => n.some((x) => fs.statSync(path.join(storageRoot(MOUNT), 'Arşiv', x)).size > 200 * 1024 * 1024),
      'the large upload reached the disk',
      600_000,
    ).then((n) => n.filter((x) => fs.statSync(path.join(storageRoot(MOUNT), 'Arşiv', x)).size > 200 * 1024 * 1024));
    const bigOnDisk = path.join(storageRoot(MOUNT), 'Arşiv', storedBig);
    const head = Buffer.alloc(97);
    const rfd = fs.openSync(bigOnDisk, 'r');
    fs.readSync(rfd, head, 0, 97, 0);
    fs.closeSync(rfd);
    expect(head.subarray(0, 8).toString()).toBe('filexe2e');
    expect(head[8], 'a STREAM file, header version 0x02').toBe(2);
    expect(head[76], 'chunk size 2^20').toBe(20);
    expect(fs.statSync(bigOnDisk).size).toBe(97 + BIG_SIZE + 16 * Math.ceil(BIG_SIZE / 2 ** 20));

    await expect(row(page, BIG_NAME)).toBeVisible({ timeout: 60_000 });
    const one = await saveOf(page, BIG_NAME, () => rowVerb(page, BIG_NAME, /^Download$/));
    expect(one.size).toBe(BIG_SIZE);
    expect(one.sha).toBe(bigSha);

    // The folder as a decrypted zip, from the folder that holds it — in-app,
    // so the key this tab holds stays in memory.
    const storeCrumb = page.locator('.fe-breadcrumb__crumb', { hasText: STORE }).first();
    if (await storeCrumb.count()) await storeCrumb.click();
    else await page.getByTestId('breadcrumb-home').click();
    await expect(row(page, 'Arşiv')).toBeVisible({ timeout: 20_000 });
    const zipped = await saveOf(page, 'Arşiv.zip', () => rowVerb(page, 'Arşiv', /^Download$/));
    test.info().annotations.push({ type: 'zip-sink', description: `${test.info().project.name}: ${zipped.sink}` });
    // What is inside, read without holding the zip twice: the entry names in
    // plaintext, no key file, and the large entry's bytes exactly.
    const inside =
      zipped.sink === 'fsa'
        ? await page.evaluate(
            async ({ zipName, entry }) => {
              const root = await navigator.storage.getDirectory();
              const file = await (await root.getFileHandle(zipName)).getFile();
              const u8 = new Uint8Array(await file.arrayBuffer());
              const dv = new DataView(u8.buffer);
              const eocd = u8.length - 22;
              const count = dv.getUint16(eocd + 10, true);
              let o = dv.getUint32(eocd + 16, true);
              const names: string[] = [];
              let entrySha = '';
              for (let i = 0; i < count; i++) {
                const size = dv.getUint32(o + 24, true);
                const nameLen = dv.getUint16(o + 28, true);
                const extraLen = dv.getUint16(o + 30, true);
                const commentLen = dv.getUint16(o + 32, true);
                const local = dv.getUint32(o + 42, true);
                const name = new TextDecoder().decode(u8.subarray(o + 46, o + 46 + nameLen));
                names.push(name);
                if (name === entry) {
                  const start = local + 30 + dv.getUint16(local + 26, true) + dv.getUint16(local + 28, true);
                  const d = new Uint8Array(await crypto.subtle.digest('SHA-256', u8.subarray(start, start + size)));
                  entrySha = Array.from(d, (x) => x.toString(16).padStart(2, '0')).join('');
                }
                o += 46 + nameLen + extraLen + commentLen;
              }
              return { names, entrySha };
            },
            { zipName: 'Arşiv.zip', entry: `Arşiv/${BIG_NAME}` },
          )
        : null;
    const got = inside ?? zipInside(zipped.file!, `Arşiv/${BIG_NAME}`);
    expect(got.names, 'plaintext names, no key file').toEqual(['Arşiv/', `Arşiv/${BIG_NAME}`]);
    expect(got.entrySha).toBe(bigSha);
    expect(zipped.size).toBeGreaterThan(BIG_SIZE);
  });
});

// ── 1. the escrow key opens a single file, and its owner is told ────────────

test.describe.serial('E2E single encrypted files — the escrow key', () => {
  const ESC_STORE = `e2e-fxe-esc-${Date.now()}`;
  const ESC_MOUNT = `/tmp/filex-${ESC_STORE}`;
  const ESC_NAME = 'Denetim notu 2027.txt';
  const ESC_BODY = 'içerik: emanetle açılan dosya, 2027\n'.repeat(20);
  const OPERATOR = { email: 'operator@local', password: 'an operator password, long' };
  let esc: { base: string; stop: () => Promise<void> } | null = null;
  let escApi: APIRequestContext;
  let operatorApi: APIRequestContext;

  test.beforeAll(async ({ playwright }) => {
    test.setTimeout(120_000);
    const bin = process.env.E2E_FILEX_BIN ?? '';
    test.skip(!bin || !fs.existsSync(bin), 'no binary to boot an escrow installation with (E2E_FILEX_BIN)');
    esc = await bootEscrowInstance(bin);
    escApi = await newAuthedRequest(playwright, esc.base);
    await seedLocalStorage(escApi, ESC_STORE, ESC_MOUNT);
    const caps = await (await escApi.get('/api/capabilities')).json();
    expect(caps?.e2e_escrow?.enabled, 'the second instance has key escrow on').toBe(true);
    expect((await escApi.put(PREFS, { data: { prefs: { locale: 'en' } } })).ok()).toBe(true);
    // The operator: a second administrator. The file belongs to the first.
    const made = await escApi.post('/api/admin/users', { data: { email: OPERATOR.email, password: OPERATOR.password, role: 'admin' } });
    expect(made.ok(), `operator: ${made.status()} ${await made.text()}`).toBe(true);
    operatorApi = await newAuthedRequest(playwright, esc.base, OPERATOR.email, OPERATOR.password);
    expect((await operatorApi.put(PREFS, { data: { prefs: { locale: 'en' } } })).ok()).toBe(true);
    await putFileTo(escApi, ESC_STORE, '', ESC_NAME, Buffer.from(ESC_BODY, 'utf8'));
  });

  test.afterAll(async () => {
    await operatorApi?.dispose();
    await escApi?.dispose();
    await esc?.stop();
  });

  test('the owner encrypts it; the operator opens it with the escrow key — and the owner is told first', async ({ browser }) => {
    test.setTimeout(240_000);
    const ctxOpts = { baseURL: esc!.base, viewport: { width: 1280, height: 860 } };

    // The owner encrypts it in the browser: on an installation with escrow the
    // dialog says so, and the file gets the installation's escrow slot.
    const ownerCtx = await browser.newContext(ctxOpts);
    const owner = await ownerCtx.newPage();
    await openStorage(owner, ESC_STORE);
    await rowVerb(owner, ESC_NAME, /^Encrypt with E2EE…$/);
    await expect(owner.getByTestId('fxe-encrypt-escrow'), 'the dialog says the operator holds a key').toBeVisible();
    await owner.getByTestId('fxe-encrypt-pw').fill(PW);
    await owner.getByTestId('fxe-encrypt-pw2').fill(PW);
    await owner.getByTestId('fxe-encrypt-ack').check();
    await owner.getByTestId('fxe-encrypt-submit').click();
    await expect(owner.locator('.fe-e2e-rk__key')).toBeVisible({ timeout: 60_000 });
    await owner.locator('.fe-e2e-rk .fe-e2e-ack input[type="checkbox"]').check();
    await owner.getByRole('button', { name: 'Done', exact: true }).click();
    const stored = path.join(storageRoot(ESC_MOUNT), `${ESC_NAME}.fxe`);
    await expect.poll(() => fs.existsSync(stored), { timeout: 30_000 }).toBe(true);
    const header = JSON.parse(fs.readFileSync(stored).subarray(13, 13 + fs.readFileSync(stored).readUInt32BE(9)).toString('utf8'));
    expect(header.esc?.kid, 'sealed to this installation’s escrow key too').toMatch(/^[0-9a-f]{16}$/);
    await ownerCtx.close();
    const bellBefore = (await notifications(escApi)).filter((n) => n.event === 'e2e.escrow_used').length;

    // The operator, who has no password: the escrow door, with its warning.
    const opCtx = await browser.newContext(ctxOpts);
    const op = await opCtx.newPage();
    await openStorage(op, ESC_STORE, OPERATOR);
    await openFolder(op, `${ESC_NAME}.fxe`);
    await expect(unlockDialog(op)).toBeVisible();
    const door = op.getByTestId('fxe-unlock-escrow-toggle');
    await expect(door, 'the escrow door is offered for a file sealed to this installation').toBeVisible({ timeout: 20_000 });
    await door.click();
    await expect(op.getByTestId('fxe-unlock-escrow-warn')).toContainText('The file’s owner will be told');
    await expect(op.getByTestId('fxe-unlock-escrow-warn')).toContainText(header.esc.kid);
    await shot(op, 'escrow-unlock');
    await op.getByTestId('fxe-unlock-escrow').fill('not a private key');
    await op.getByTestId('fxe-unlock-submit').click();
    await expect(op.getByTestId('fxe-unlock-error')).toContainText('not a readable private key');
    expect((await notifications(escApi)).filter((n) => n.event === 'e2e.escrow_used').length, 'a refused key tells nobody').toBe(bellBefore);
    await op.getByTestId('fxe-unlock-escrow').fill(ESCROW_KEY.private_pkcs8_b64);
    await op.getByTestId('fxe-unlock-submit').click();
    await expect(op.getByText('içerik: emanetle açılan dosya, 2027').first(), 'the file opens').toBeVisible({ timeout: 30_000 });
    await op.keyboard.press('Escape');

    // The owner — not the operator — has the notification, naming the file.
    await expect
      .poll(async () => (await notifications(escApi)).filter((n) => n.event === 'e2e.escrow_used').length, { timeout: 15_000 })
      .toBe(bellBefore + 1);
    const rows = (await notifications(escApi)).filter((n) => n.event === 'e2e.escrow_used');
    expect(String(rows[0].title)).toBe('Encrypted file opened with the escrow key');
    expect(JSON.stringify(rows[0])).toContain(`${ESC_NAME}.fxe`);
    expect(
      (await notifications(operatorApi)).filter((n) => n.event === 'e2e.escrow_used'),
      'the operator is not told about themselves',
    ).toHaveLength(0);
    await opCtx.close();
  });
});

// ── 2. a password change is announced, and the server keeps its own record ──

test.describe.serial('E2E single encrypted files — a password change is announced', () => {
  const PWC_STORE = `e2e-fxe-pwc-${Date.now()}`;
  const PWC_MOUNT = `/tmp/filex-${PWC_STORE}`;
  const PWC_NAME = 'Sözleşme taslağı.txt';
  const PWC_BODY = 'içerik: parolası değişen sözleşme\n'.repeat(30);
  let api: APIRequestContext;

  test.beforeAll(async ({ playwright, baseURL }) => {
    test.setTimeout(120_000);
    api = await newAuthedRequest(playwright, baseURL ?? '');
    await dropStorageByName(api, PWC_STORE);
    await seedLocalStorage(api, PWC_STORE, PWC_MOUNT);
    await putFileTo(api, PWC_STORE, '', `${PWC_NAME}.fxe`, await fxeOf(PWC_NAME, Buffer.from(PWC_BODY, 'utf8'), PW));
  });

  test.afterAll(async () => {
    await dropStorageByName(api, PWC_STORE).catch(() => undefined);
    await api?.dispose();
  });

  test('the owner is told, the audit log has it twice — announced and seen — and no old header survives', async ({ page }) => {
    test.setTimeout(180_000);
    const stored = `${PWC_NAME}.fxe`;
    const auditBefore = (await auditRows(api, 'e2e.password_change')).length;
    const seenBefore = (await auditRows(api, 'e2e.fxe_header_rewritten')).length;
    await openStorage(page, PWC_STORE);
    await rowVerb(page, stored, /^Change password…$/);
    await expect(page.getByTestId('e2e-password-form')).toBeVisible();
    await page.getByTestId('e2e-password-current').fill(PW);
    await page.getByTestId('e2e-password-new').fill(PW2);
    await page.getByTestId('e2e-password-new2').fill(PW2);
    await page.getByTestId('e2e-password-submit').click();
    await expect(page.getByTestId('e2e-password-form')).toBeHidden({ timeout: 60_000 });

    // Announced by the browser: the owner's bell, and the audit row. Matched
    // on THIS run's storage too: the engines share one server, and the file
    // name is the same in each.
    const ours = (r: Row) => JSON.stringify(r).includes(stored) && JSON.stringify(r).includes(PWC_STORE);
    await expect
      .poll(async () => (await notifications(api)).some((n) => n.event === 'e2e.password_changed' && ours(n)), {
        timeout: 15_000,
        message: 'the owner is told',
      })
      .toBe(true);
    const told = (await notifications(api)).find((n) => n.event === 'e2e.password_changed' && ours(n))!;
    expect(String(told.title)).toBe('Encrypted file password changed');
    const announced = (await auditRows(api, 'e2e.password_change')).filter(ours);
    expect(announced.length, 'the announcement is in the audit log').toBe(1);
    expect((await auditRows(api, 'e2e.password_change')).length).toBe(auditBefore + 1);

    // Seen by the server itself, whatever the client says: the header was
    // rewritten, the password slot changed — and the version the overwrite
    // kept (the old header, which the old password still opens) is gone.
    await expect
      .poll(async () => (await auditRows(api, 'e2e.fxe_header_rewritten')).length, { timeout: 15_000, message: 'the server audits the rewrite' })
      .toBe(seenBefore + 1);
    const seen = (await auditRows(api, 'e2e.fxe_header_rewritten')).find((r) => JSON.stringify(r).includes(stored));
    expect(seen, 'the rewrite names the file').toBeTruthy();
    expect(JSON.stringify(seen)).toContain('"password"');
    const node = await rowOf(api, PWC_STORE, '', stored);
    expect(node?.id, 'the .fxe is catalogued').toBeTruthy();
    expect(await versionsOfNode(api, Number(node!.id)), 'no version keeps the old header').toHaveLength(0);
  });
});

// ── 3. an administrator deletes the original for good ──────────────────────

test.describe.serial('E2E single encrypted files — an administrator deletes the original for good', () => {
  const PRG_STORE = `e2e-fxe-prg-${Date.now()}`;
  const PRG_MOUNT = `/tmp/filex-${PRG_STORE}`;
  const MINE = 'Maaş listesi 2027.csv';
  const MINE_V1 = 'ad;maaş\nAyşe;1 ilk-sürüm-satırı\n';
  const MINE_V2 = 'ad;maaş\nAyşe;2 ikinci-sürüm-satırı\n';
  const THEIRS = 'İzin çizelgesi.csv';
  const THEIRS_BODY = 'ad;izin\nÇağla;başkasının-dosyası-satırı\n';
  const COLLEAGUE = { email: 'colleague@local', password: 'a colleague password, long' };
  let api: APIRequestContext;
  let colleagueApi: APIRequestContext;
  let colleagueId = 0;

  test.beforeAll(async ({ playwright, baseURL }) => {
    test.setTimeout(120_000);
    api = await newAuthedRequest(playwright, baseURL ?? '');
    await dropStorageByName(api, PRG_STORE);
    await seedLocalStorage(api, PRG_STORE, PRG_MOUNT);
    expect((await api.put(PREFS, { data: { prefs: { ...prefsBefore, locale: 'en' } } })).ok()).toBe(true);
    // Mine, overwritten once: the first content is now a version.
    await putFileTo(api, PRG_STORE, '', MINE, Buffer.from(MINE_V1, 'utf8'));
    await putFileTo(api, PRG_STORE, '', MINE, Buffer.from(MINE_V2, 'utf8'));
    // Someone else's: uploaded by a second administrator.
    const existing = await api.get('/api/admin/users');
    const listed = ((await existing.json()) as { users?: Row[] } | Row[]) as unknown;
    const users = Array.isArray(listed) ? (listed as Row[]) : (((listed as { users?: Row[] }).users ?? []) as Row[]);
    const old = users.find((u) => u.email === COLLEAGUE.email);
    if (old) await api.delete(`/api/admin/users/${old.id}`);
    const made = await api.post('/api/admin/users', { data: { email: COLLEAGUE.email, password: COLLEAGUE.password, role: 'admin' } });
    expect(made.ok(), `colleague: ${made.status()} ${await made.text()}`).toBe(true);
    const madeBody = (await made.json()) as Row & { user?: Row };
    colleagueId = Number(madeBody.id ?? madeBody.user?.id ?? 0);
    colleagueApi = await newAuthedRequest(playwright, baseURL ?? '', COLLEAGUE.email, COLLEAGUE.password);
    await putFileTo(colleagueApi, PRG_STORE, '', THEIRS, Buffer.from(THEIRS_BODY, 'utf8'));
  });

  test.afterAll(async () => {
    await colleagueApi?.dispose();
    if (colleagueId) await api?.delete(`/api/admin/users/${colleagueId}`).catch(() => undefined);
    await dropStorageByName(api, PRG_STORE).catch(() => undefined);
    await api?.dispose();
  });

  async function encryptWithPurge(page: Page, name: string, opts: { others: boolean }) {
    await rowVerb(page, name, /^Encrypt with E2EE…$/);
    const purge = page.getByTestId('fxe-encrypt-purge');
    await expect(purge, 'an administrator is offered "delete for good"').toBeVisible();
    await expect(page.getByTestId('fxe-encrypt-purge-others')).toHaveCount(0);
    await purge.check();
    const others = page.getByTestId('fxe-encrypt-purge-others');
    if (opts.others) {
      await expect(others, 'someone else’s file: said, and confirmed separately').toBeVisible();
      await expect(others, 'the owner is named').toContainText('colleague');
    } else {
      await expect(others, 'my own file needs no second confirmation').toHaveCount(0);
    }
    await page.getByTestId('fxe-encrypt-pw').fill(PW);
    await page.getByTestId('fxe-encrypt-pw2').fill(PW);
    await page.getByTestId('fxe-encrypt-ack').check();
    if (opts.others) {
      await page.getByTestId('fxe-encrypt-submit').click();
      await expect(page.getByTestId('fxe-encrypt-error'), 'not without the second confirmation').toBeVisible();
      expect(fs.existsSync(path.join(storageRoot(PRG_MOUNT), name)), 'nothing happened yet').toBe(true);
      await shot(page, 'encrypt-purge-others');
      await page.getByTestId('fxe-encrypt-purge-others-ack').check();
    } else {
      await shot(page, 'encrypt-purge');
    }
    await page.getByTestId('fxe-encrypt-submit').click();
    await expect(page.locator('.fe-e2e-rk__key')).toBeVisible({ timeout: 60_000 });
    await page.locator('.fe-e2e-rk .fe-e2e-ack input[type="checkbox"]').check();
    await page.getByRole('button', { name: 'Done', exact: true }).click();
  }

  async function trashNames(): Promise<string> {
    const r = await api.get('/api/files/manager/trash');
    expect(r.ok(), `trash: ${r.status()}`).toBe(true);
    return JSON.stringify(await r.json());
  }

  test('my file: its old versions and its trash entry are gone, and no plaintext is left on the disk', async ({ page }) => {
    test.setTimeout(180_000);
    const before = await rowOf(api, PRG_STORE, '', MINE);
    expect(before?.id).toBeTruthy();
    const versions = await versionsOfNode(api, Number(before!.id));
    expect(versions.length, 'the overwrite kept the first content as a version').toBeGreaterThan(0);
    expect(holding(storageRoot(PRG_MOUNT), 'ilk-sürüm-satırı').length, 'the version is on the disk').toBeGreaterThan(0);

    await openStorage(page, PRG_STORE);
    await encryptWithPurge(page, MINE, { others: false });
    const root = storageRoot(PRG_MOUNT);
    await expect.poll(() => fs.existsSync(path.join(root, `${MINE}.fxe`)), { timeout: 30_000 }).toBe(true);
    await expect
      .poll(() => [...holding(root, 'ilk-sürüm-satırı'), ...holding(root, 'ikinci-sürüm-satırı')], {
        timeout: 30_000,
        message: 'no plaintext anywhere under the storage (versions, trash)',
      })
      .toEqual([]);
    expect(await trashNames(), 'no trash entry').not.toContain(MINE);
    expect(await versionsOfNode(api, Number(before!.id))).toHaveLength(0);
  });

  test('someone else’s file: the dialog names its owner and wants a second confirmation', async ({ page }) => {
    test.setTimeout(180_000);
    await openStorage(page, PRG_STORE);
    await encryptWithPurge(page, THEIRS, { others: true });
    const root = storageRoot(PRG_MOUNT);
    await expect.poll(() => fs.existsSync(path.join(root, `${THEIRS}.fxe`)), { timeout: 30_000 }).toBe(true);
    await expect.poll(() => holding(root, 'başkasının-dosyası-satırı'), { timeout: 30_000 }).toEqual([]);
    expect(await trashNames(), 'no trash entry').not.toContain(THEIRS);
  });

  test('a user who is not an administrator is not offered it', async ({ page }) => {
    test.skip(!isChromium(), 'the same dialog in every engine; looked at once');
    // The capability, not the engine, decides: an account without
    // administration never sees the box (the endpoints would refuse it anyway).
    await page.addInitScript(() => localStorage.setItem('filex.tourDone', '1'));
    await page.route('**/api/files/capabilities', async (route) => {
      const res = await route.fetch();
      const body = await res.json();
      await route.fulfill({ response: res, json: { ...body, caller_admin: false } });
    });
    await loginAs(page);
    await setAccountViewMode(page.request, 'list');
    await putFileTo(api, PRG_STORE, '', 'plain.txt', Buffer.from('just a file', 'utf8'));
    await page.goto(`/drive/explore?storage=${encodeURIComponent(PRG_STORE)}`);
    await rowVerb(page, 'plain.txt', /^Encrypt with E2EE…$/);
    await expect(page.getByTestId('fxe-encrypt-form')).toBeVisible();
    await expect(page.getByTestId('fxe-encrypt-purge')).toHaveCount(0);
    await page.keyboard.press('Escape');
  });
});

// ── 4. over the in-memory limit: said, with the way out ────────────────────

test.describe.serial('E2E single encrypted files — over the in-memory limit', () => {
  const BIG_STORE = `e2e-fxe-1g-${Date.now()}`;
  const BIG_MOUNT = `/tmp/filex-${BIG_STORE}`;
  const HUGE_NAME = 'Yedek arşivi 2027.bin';
  /** Over E2E_BLOB_SAVE_LIMIT (1 GiB): the Firefox/Safari in-memory save refuses it. */
  const HUGE_SIZE = 1024 * 1024 * 1024 + 60 * 1024 * 1024 + 4321;
  const MiB = 1 << 20;
  let api: APIRequestContext;
  let firstSha = '';
  let lastSha = '';
  let hugeFile = '';

  test.beforeAll(async ({ playwright, baseURL }) => {
    test.setTimeout(600_000);
    api = await newAuthedRequest(playwright, baseURL ?? '');
    await dropStorageByName(api, BIG_STORE);
    // Written straight into the storage's directory BEFORE the storage is
    // registered: its first scan catalogues it, and 1.1 GB never crosses HTTP.
    const root = storageRoot(BIG_MOUNT);
    fs.mkdirSync(root, { recursive: true });
    hugeFile = path.join(root, `${HUGE_NAME}.fxe`);
    const block = randomBytes(MiB);
    const pieceAt = (i: number) => {
      const n = Math.min(MiB, HUGE_SIZE - i * MiB);
      const piece = Buffer.from(block.subarray(0, n));
      piece.writeUInt32BE(i, 0);
      return piece;
    };
    const chunks = Math.ceil(HUGE_SIZE / MiB);
    firstSha = sha(pieceAt(0));
    lastSha = sha(pieceAt(chunks - 1));
    let i = 0;
    const src = new ReadableStream<Uint8Array>({
      pull(c) {
        if (i >= chunks) {
          c.close();
          return;
        }
        c.enqueue(new Uint8Array(pieceAt(i++)));
      },
    });
    const core = await coreFxe();
    const created = await core.createFxe(HUGE_NAME, HUGE_SIZE, src, PW);
    const fd = fs.openSync(hugeFile, 'w');
    for await (const piece of created.stream as unknown as AsyncIterable<Uint8Array>) fs.writeSync(fd, piece);
    fs.closeSync(fd);
    expect(fs.statSync(hugeFile).size).toBe(created.size);
    await seedLocalStorage(api, BIG_STORE, BIG_MOUNT);
  });

  test.afterAll(async () => {
    await dropStorageByName(api, BIG_STORE).catch(() => undefined);
    await api?.dispose();
    if (hugeFile) fs.rmSync(hugeFile, { force: true });
  });

  test('Firefox and Safari say so before asking for the password, and hand over the encrypted file and the command; Chromium streams it', async ({ page }) => {
    test.setTimeout(900_000);
    await openStorage(page, BIG_STORE);
    await expect(row(page, `${HUGE_NAME}.fxe`)).toBeVisible({ timeout: 60_000 });

    if (isChromium()) {
      // The streaming path, unchanged: the password, then the save dialog,
      // written as it is decrypted — no limit, no warning.
      await rowVerb(page, `${HUGE_NAME}.fxe`, /^Download$/);
      await page.getByTestId('fxe-unlock-pw').fill(PW);
      await page.getByTestId('fxe-unlock-submit').click();
      await expect
        .poll(
          () =>
            page.evaluate(
              (n) => ((window as unknown as { __fxeSaves: Array<{ name: string; closed: boolean }> }).__fxeSaves ?? []).some((s) => s.name === n && s.closed),
              HUGE_NAME,
            ),
          { timeout: 600_000, message: 'streamed to disk through the File System Access path' },
        )
        .toBe(true);
      await expect(page.getByTestId('e2e-too-big')).toHaveCount(0);
      const got = await page.evaluate(
        async ({ n, mib }) => {
          const root = await navigator.storage.getDirectory();
          const file = await (await root.getFileHandle(n)).getFile();
          const hex = async (b: Blob) =>
            Array.from(new Uint8Array(await crypto.subtle.digest('SHA-256', await b.arrayBuffer())), (x) => x.toString(16).padStart(2, '0')).join('');
          const lastStart = Math.floor((file.size - 1) / mib) * mib;
          const saves = (window as unknown as { __fxeSaves: Array<{ name: string; writes: number }> }).__fxeSaves;
          return {
            size: file.size,
            first: await hex(file.slice(0, mib)),
            last: await hex(file.slice(lastStart)),
            writes: saves.filter((s) => s.name === n).pop()?.writes ?? 0,
          };
        },
        { n: HUGE_NAME, mib: MiB },
      );
      expect(got.size).toBe(HUGE_SIZE);
      expect(got.first).toBe(firstSha);
      expect(got.last).toBe(lastSha);
      expect(got.writes, 'written chunk by chunk, never held whole').toBeGreaterThan(1000);
      await page.evaluate(async (n) => (await navigator.storage.getDirectory()).removeEntry(n), HUGE_NAME);
      return;
    }

    // In-memory save: refused up front, in words, with the way out — and no
    // password is asked for a download that cannot happen here.
    let downloads = 0;
    page.on('download', () => downloads++);
    await rowVerb(page, `${HUGE_NAME}.fxe`, /^Download$/);
    const dialog = page.getByTestId('e2e-too-big');
    await expect(dialog).toBeVisible({ timeout: 30_000 });
    await expect(page.getByTestId('fxe-unlock-form')).toHaveCount(0);
    // Decimal units, as every size in filex (useLocale formatByteSize): the
    // limit is 1 GiB = 1.07 GB, the file 1.14 GB.
    await expect(dialog).toContainText('1.07 GB');
    await expect(dialog).toContainText('1.14 GB');
    await expect(page.getByTestId('e2e-too-big-command')).toHaveText(`filex decrypt "${HUGE_NAME}.fxe"`);
    await shot(page, 'too-big');
    expect(downloads, 'nothing was saved').toBe(0);
    // "Download encrypted file" hands over the bytes the server has.
    const dl = await rawDownload(page, () => page.getByTestId('e2e-too-big-download').click());
    expect(dl.suggestedFilename()).toMatch(/\.fxe$/);
    await dl.cancel();
    await expect(dialog).toBeHidden();
  });
});

// ── 5. a .fxe inside a level-2 folder (contents and names) ──────────────────
//
// Two names, two layers, and the decision between them (docs/E2E-ENCRYPTION.md
// → "A .fxe inside an encrypted folder"): the name SEALED IN THE .fxe HEADER
// is the file's own, under its own key and bound to no folder, so the file
// stays portable; the folder's entry for it is an ordinary folder file — its
// bytes wrapped by the folder key, its stored name sealed for the folder it is
// in (its folder id), re-sealed on a rename and on a move like any other.

test.describe.serial('E2E single encrypted files — a .fxe inside a level-2 folder', () => {
  const L2_STORE = `e2e-fxe-l2-${Date.now()}`;
  const L2_MOUNT = `/tmp/filex-${L2_STORE}`;
  const FXE_NAME = 'Rapor 2027.pdf.fxe';
  const RENAMED = 'Rapor 2027 son.pdf.fxe';
  const FOLDER_PW = 'the level-2 folder password';
  const SEALED = /^[A-Za-z0-9_-]{23,}(\.[A-Za-z0-9_-]{22})?$/;
  const SEALED_DIR = /^[A-Za-z0-9_-]{23,}\.([A-Za-z0-9_-]{22})$/;
  let api: APIRequestContext;
  let fxeBytes: Buffer;

  const kasa = (rel = '') => path.join(storageRoot(L2_MOUNT), 'Kasa', rel);
  const entries = (rel = '') =>
    fs
      .readdirSync(kasa(rel))
      .filter((n) => n !== '.filex-e2e.json' && n !== '.keepdir' && !n.startsWith('.filex-'))
      .sort();

  test.beforeAll(async ({ playwright, baseURL }) => {
    test.setTimeout(120_000);
    api = await newAuthedRequest(playwright, baseURL ?? '');
    await dropStorageByName(api, L2_STORE);
    await seedLocalStorage(api, L2_STORE, L2_MOUNT);
    expect((await api.put(PREFS, { data: { prefs: { ...prefsBefore, locale: 'en' } } })).ok()).toBe(true);
    fxeBytes = await fxeOf('Rapor 2027.pdf', Buffer.from('içerik: kasadaki rapor, 2027\n'.repeat(60), 'utf8'), PW);
  });

  test.afterAll(async () => {
    await dropStorageByName(api, L2_STORE).catch(() => undefined);
    await api?.dispose();
  });

  async function newMenu(page: Page, item: RegExp) {
    await page.getByTestId('sidenav-new').first().click();
    await page
      .locator('.fe-ctx__item', { has: page.locator('.fe-ctx__label', { hasText: item }) })
      .first()
      .click();
  }

  async function savedBytes(page: Page, name: string): Promise<Buffer> {
    const b64 = await page.evaluate(async (n) => {
      const root = await navigator.storage.getDirectory();
      const u8 = new Uint8Array(await (await (await root.getFileHandle(n)).getFile()).arrayBuffer());
      let s = '';
      for (let i = 0; i < u8.length; i++) s += String.fromCharCode(u8[i]);
      return btoa(s);
    }, name);
    return Buffer.from(b64, 'base64');
  }

  test('it is a file of the folder: a sealed name, the folder key over it, and it comes back byte for byte', async ({ page }) => {
    test.setTimeout(300_000);
    await openStorage(page, L2_STORE);

    // A level-2 folder: contents and names.
    await newMenu(page, /^New folder$/);
    await page.getByText('Create encrypted folder…', { exact: true }).click();
    const create = page.locator('.fe-modal__card').filter({ has: page.getByTestId('e2e-create-names') });
    await create.locator('input[type="text"]').first().fill('Kasa');
    const pws = create.locator('input[type="password"]');
    await pws.nth(0).fill(FOLDER_PW);
    await pws.nth(1).fill(FOLDER_PW);
    await create.getByTestId('e2e-level-names').check();
    await create.getByTestId('e2e-create-ack').check();
    await create.getByRole('button', { name: 'Create encrypted folder', exact: true }).click();
    await expect(page.locator('.fe-e2e-rk__key')).toBeVisible({ timeout: 30_000 });
    await page.locator('.fe-e2e-rk .fe-e2e-ack input[type="checkbox"]').check();
    await page.getByRole('button', { name: 'Done', exact: true }).click();
    await openFolder(page, 'Kasa');
    await expect(page.getByTestId('e2e-names-status')).toHaveText('Contents and names', { timeout: 20_000 });

    // Upload a .fxe into it: the folder treats it as the file it is.
    await page.locator('input[type="file"]').first().setInputFiles({ name: FXE_NAME, mimeType: 'application/octet-stream', buffer: fxeBytes });
    await expect.poll(() => entries().length, { timeout: 30_000, message: 'the upload reached the disk' }).toBe(1);
    const [stored] = entries();
    expect(stored, 'its name in the folder is sealed for the folder').toMatch(SEALED);
    expect(stored).not.toContain('fxe');
    const wrapped = fs.readFileSync(kasa(stored));
    expect(wrapped.subarray(0, 8).toString(), 'the folder key over it').toBe('filexe2e');
    expect(wrapped.includes(Buffer.from('filexfxe')), 'not even its own header shows').toBe(false);
    await expect(row(page, FXE_NAME), 'the plaintext name on screen').toBeVisible({ timeout: 20_000 });
    // Its single-file verbs are not offered here: the folder already encrypts it.
    const target = await settled(row(page, FXE_NAME));
    await target.click({ button: 'right' });
    await target.dispose();
    const menu = page.getByRole('menu').first();
    await expect(menu).toBeVisible();
    await expect(menu.getByRole('menuitem', { name: /^Change password…$/ })).toHaveCount(0);
    await expect(menu.getByRole('menuitem', { name: /^Remove encryption…$/ })).toHaveCount(0);
    await page.keyboard.press('Escape');

    // Renamed: re-sealed under a new stored name, the bytes untouched.
    await rowVerb(page, FXE_NAME, /^Rename$/);
    const renameDialog = page.getByRole('dialog', { name: /^Rename$/ });
    await expect(renameDialog.getByRole('textbox')).toHaveValue(FXE_NAME);
    await renameDialog.getByRole('textbox').fill(RENAMED);
    await renameDialog.getByRole('button', { name: /^Save$/ }).click();
    await expect.poll(() => entries().join(), { timeout: 20_000, message: 'the rename reached the disk' }).not.toBe(stored);
    const [renamed] = entries();
    expect(renamed).toMatch(SEALED);
    expect(fs.readFileSync(kasa(renamed)).equals(wrapped), 'a rename touches the name only').toBe(true);
    await expect(row(page, RENAMED)).toBeVisible({ timeout: 20_000 });

    // Moved into a subfolder: re-sealed for that folder's id.
    await newMenu(page, /^New folder$/);
    const nf = page.getByRole('dialog', { name: 'New folder' });
    await nf.getByRole('textbox').fill('Alt');
    await nf.getByRole('button', { name: 'Create', exact: true }).click();
    await expect.poll(() => entries().length, { timeout: 20_000 }).toBe(2);
    const altStored = entries().find((n) => SEALED_DIR.test(n))!;
    expect(altStored, 'a folder: its sealed name and its folder id').toMatch(SEALED_DIR);
    await rowVerb(page, RENAMED, /^Move to…$/);
    const picker = page.getByTestId('destpicker');
    await expect(picker).toBeVisible();
    await picker.getByTestId('destpicker-row-Alt').click();
    await page.getByTestId('destpicker-confirm').click();
    await expect.poll(() => (fs.existsSync(kasa(altStored)) ? entries(altStored).length : 0), { timeout: 20_000 }).toBe(1);
    const [inAlt] = entries(altStored);
    expect(inAlt).toMatch(SEALED);
    expect(inAlt, 'the same name sealed for another folder is another stored name').not.toBe(renamed);
    expect(entries()).toEqual([altStored]);

    // Downloaded from there: the .fxe exactly as it was uploaded.
    await openFolder(page, 'Alt');
    await expect(row(page, RENAMED)).toBeVisible({ timeout: 20_000 });
    const one = await saveOf(page, RENAMED, () => rowVerb(page, RENAMED, /^Download$/));
    expect(one.sha, 'byte for byte the uploaded .fxe').toBe(sha(fxeBytes));

    // …and in the decrypted zip of the subfolder, under its plaintext name.
    await page.locator('.fe-breadcrumb__crumb', { hasText: 'Kasa' }).first().click();
    await expect(row(page, 'Alt')).toBeVisible({ timeout: 20_000 });
    const zipped = await saveOf(page, 'Alt.zip', () => rowVerb(page, 'Alt', /^Download$/));
    let zipFile = zipped.file;
    if (zipped.sink === 'fsa') {
      zipFile = path.join(fs.mkdtempSync(path.join(os.tmpdir(), 'filex-fxe-l2-')), 'Alt.zip');
      fs.writeFileSync(zipFile, await savedBytes(page, 'Alt.zip'));
    }
    const inside = zipInside(zipFile!, `Alt/${RENAMED}`);
    expect(inside.names).toEqual(['Alt/', `Alt/${RENAMED}`]);
    expect(inside.entrySha).toBe(sha(fxeBytes));
  });

  test('filex decrypt takes the level-2 folder apart and hands the .fxe back as it is', async () => {
    const bin = process.env.E2E_FILEX_BIN ?? '';
    test.skip(!bin || !fs.existsSync(bin), 'no binary to run (E2E_FILEX_BIN)');
    test.skip(!isChromium(), 'the binary is the same whichever browser wrote the folder; measured once');
    const work = fs.mkdtempSync(path.join(os.tmpdir(), 'filex-fxe-l2-cli-'));
    const copy = path.join(work, 'Kasa');
    fs.cpSync(kasa(), copy, { recursive: true });
    const out = path.join(work, 'plain');
    const r = spawnSync(bin, ['decrypt', copy, '-o', out, '--password-stdin'], { input: `${FOLDER_PW}\n`, encoding: 'utf8' });
    expect(r.status, `${r.stdout}\n${r.stderr}`).toBe(0);
    expect(fs.readdirSync(out)).toEqual(['Alt']);
    expect(fs.readdirSync(path.join(out, 'Alt'))).toEqual([RENAMED]);
    expect(sha(fs.readFileSync(path.join(out, 'Alt', RENAMED))), 'the .fxe, with its own password still on it').toBe(sha(fxeBytes));
    // …which filex decrypt opens with ITS password, and its own sealed name.
    const inner = spawnSync(bin, ['decrypt', path.join(out, 'Alt', RENAMED), '--password-stdin'], { input: `${PW}\n`, encoding: 'utf8' });
    expect(inner.status, `${inner.stdout}\n${inner.stderr}`).toBe(0);
    expect(fs.readFileSync(path.join(out, 'Alt', 'Rapor 2027.pdf'), 'utf8')).toBe('içerik: kasadaki rapor, 2027\n'.repeat(60));
    fs.rmSync(work, { recursive: true, force: true });
  });
});
