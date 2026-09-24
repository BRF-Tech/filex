/**
 * Measure the folder-recovery flow in a real browser, and capture the
 * screenshots docs/screenshots/<release>/e2e-recovery/ shows (the release is
 * named once, in ./release.mjs; --out overrides it).
 *
 * This is the only place the feature is measured end to end. The unit suite
 * proves the crypto; this proves a person can actually get their folder back:
 * create it, see the key once, lose the password, get in with the key.
 *
 * It is a script rather than a spec because it needs an installation booted
 * with escrow ON (FILEX_INSTALLATION_E2E_ESCROW_KEY), which is an install-time
 * decision and therefore not something a spec inside a shared run can arrange.
 *
 * Usage:
 *   node e2e/shots/e2e-recovery.mjs                 # boots its own escrow instance
 *
 * With no --url it does the whole setup itself, every run: generates a
 * throwaway RSA-OAEP key pair into a fresh temp directory, boots FILEX_BIN
 * (default bin/filex[.exe]) with the public half pinned as the installation's
 * escrow key, uses the private half for the escrow leg, and tears the instance
 * and the keys down again. ⚠ That used to be a hand step — boot an instance in
 * another shell, generate a key pair, paste the private half on the command
 * line — and so it was the one script `pnpm shots` could not run unattended.
 *
 * Against an instance that is already running (escrow must be on):
 *   node e2e/shots/e2e-recovery.mjs --url http://127.0.0.1:8123 \
 *        --escrow-private <pkcs8-b64> --root <dir the storage points at>
 */
import { chromium } from '@playwright/test';
// The SHIPPED bundle, not a re-implementation: the v1 folder this script
// seeds has to be the same v1 folder the product makes, or the upgrade leg
// measures a fixture instead of the feature.
import * as e2ecrypto from '../../packages/core/dist/filex-core.js';
import { shotsDir } from './release.mjs';
import { spawn } from 'node:child_process';
import { createHash, generateKeyPairSync } from 'node:crypto';
import fs from 'node:fs';
import { createServer } from 'node:net';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const REPO = path.resolve(__dirname, '../..');
const SHOTS = arg('out', shotsDir('e2e-recovery'));

function arg(name, fallback) {
  const i = process.argv.indexOf(`--${name}`);
  return i > -1 ? process.argv[i + 1] : fallback;
}

const EMAIL = arg('email', 'admin@example.com');
const PASSWORD = arg('password', 'Passw0rd!e2e');
// Set from the flags when --url names a running instance, or by
// bootEscrowInstance() when this script starts its own.
let BASE = arg('url', '');
let ESCROW_PRIVATE = arg('escrow-private', process.env.FILEX_E2E_ESCROW_PRIVATE || '');
// ⚠ Scratch space outside the repo. These used to default to `_livedata/` in
// the working tree, which is neither tracked nor ignored — every run left an
// untracked directory beside the source for the next `git add -A` to sweep up.
let STORAGE_ROOT = arg('root', path.join(os.tmpdir(), 'filex-e2e-recovery-store'));
const STORAGE = 'enc';
const FOLDER_PW = 'hunter2-hunter2';

const results = [];
function check(name, ok, detail = '') {
  results.push({ name, ok, detail });
  console.log(`${ok ? 'PASS' : 'FAIL'}  ${name}${detail ? ` — ${detail}` : ''}`);
}

async function api(token, method, url, body, isForm = false) {
  const headers = { Authorization: `Bearer ${token}` };
  let payload = body;
  if (body && !isForm) {
    headers['Content-Type'] = 'application/json';
    payload = JSON.stringify(body);
  }
  const res = await fetch(BASE + url, { method, headers, body: payload });
  const text = await res.text();
  let json = null;
  try {
    json = JSON.parse(text);
  } catch {
    /* not json */
  }
  return { status: res.status, json, text };
}

/**
 * Takes one of this script's screenshots once the page has stopped moving.
 *
 * ⚠ `waitFor({ state: 'visible' })` is not "finished drawing". A dialog counts
 * as visible from the first frame of its fade-in, and on 2026-09-13
 * `legacy-folder-recovery-key.png` came out with the dialog at partial
 * opacity — the listing's "Type / Owner / Plain text" showing THROUGH the
 * recovery key. So this waits until no CSS animation or transition on the page
 * is still running. A fixed sleep would be the same bet, just slower.
 *
 * ⚠ Bounded: an element that animates forever (a spinner) would otherwise
 * hold the run hostage, so after 5s it shoots anyway and says so.
 */
async function shot(page, name) {
  try {
    await page.waitForFunction(
      () => document.getAnimations().every((a) => a.playState !== 'running'),
      null,
      { timeout: 5000 },
    );
  } catch {
    console.log(`    [shot] ${name}: an animation was still running after 5s — taken anyway`);
  }
  await page.screenshot({ path: path.join(SHOTS, name) });
}

/**
 * Opens the "New Folder" modal.
 *
 * ⚠ There is no bare "New folder" button on the toolbar any more. Since the
 * 2026-09-12 shell the panel's "+ New" (data-testid="sidenav-new") opens a
 * menu — Upload files · New folder · New document · Request files — and the
 * modal is behind its second row. This script used to click a button by its
 * accessible name and spent 30s not finding one.
 *
 * The old path is kept as a fallback so a surface with no navigation panel
 * (an embed with `sidenav` off) still reaches the same modal.
 */
async function openNewFolder(page) {
  const plus = page.locator('[data-testid="sidenav-new"]');
  if (await plus.count()) {
    await plus.first().click();
    await page.waitForTimeout(400);
    await page.locator('.fe-ctx__item', { hasText: /new folder|yeni klasör/i }).first().click();
    return;
  }
  await page.getByRole('button', { name: /new folder|yeni klasör/i }).first().click();
}

function freePort() {
  return new Promise((resolve, reject) => {
    const srv = createServer();
    srv.on('error', reject);
    srv.listen(0, '127.0.0.1', () => {
      const { port } = srv.address();
      srv.close(() => resolve(port));
    });
  });
}

/**
 * Boots an installation with escrow ON, keyed to a pair made for this run only.
 *
 * ⚠ The escrow key is pinned at an installation's first boot and refused if it
 * ever changes (backend/internal/e2e/installation.go), so reusing a data dir
 * across runs with a fresh key would stop the server. Everything — keys, data
 * dir, storage root, log — lives in one mkdtemp and goes with it.
 *
 * Returns the instance's escrow key id as this script computes it: the
 * capabilities check in main() compares it with what the server publishes, and
 * a mismatch means the port answered from somebody else's filex.
 */
async function bootEscrowInstance() {
  const bin =
    process.env.FILEX_BIN ??
    [path.join(REPO, 'bin/filex.exe'), path.join(REPO, 'bin/filex')].find((p) => fs.existsSync(p));
  if (!bin || !fs.existsSync(bin)) {
    throw new Error('no filex binary — run `pnpm shots`, `pnpm run build:all`, or set FILEX_BIN');
  }
  const scratch = fs.mkdtempSync(path.join(os.tmpdir(), 'filex-e2e-recovery-'));
  // RSA-OAEP-256 is the only escrow algorithm (packages/core/src/lib/e2ecrypto.ts):
  // SPKI for the server, PKCS#8 for the person holding the escrow key, both
  // base64 DER. 3072 bits is what `filex e2e-escrow keygen` makes.
  const { publicKey, privateKey } = generateKeyPairSync('rsa', {
    modulusLength: 3072,
    publicKeyEncoding: { type: 'spki', format: 'der' },
    privateKeyEncoding: { type: 'pkcs8', format: 'der' },
  });
  const spki = publicKey.toString('base64');
  const keyDir = path.join(scratch, 'escrow-key');
  fs.mkdirSync(keyDir);
  fs.writeFileSync(path.join(keyDir, 'public.spki.b64'), `${spki}\n`, { mode: 0o600 });
  fs.writeFileSync(path.join(keyDir, 'private.pkcs8.b64'), `${privateKey.toString('base64')}\n`, { mode: 0o600 });

  const dataDir = path.join(scratch, 'data');
  const storeDir = path.join(scratch, 'store');
  fs.mkdirSync(dataDir);
  fs.mkdirSync(storeDir);
  const port = Number(process.env.SHOTS_PORT) || (await freePort());
  const logFile = path.join(scratch, 'server.log');
  const logFd = fs.openSync(logFile, 'a');
  const base = `http://127.0.0.1:${port}`;
  console.log(`booting ${bin} on :${port} with a throwaway escrow key (${keyDir})`);
  // ⚠ Output to a file descriptor, not a pipe: nothing in this script drains
  // one, and a full pipe wedges filex mid-request (e2e/run.mjs).
  const child = spawn(bin, ['serve'], {
    env: {
      ...process.env,
      FILEX_LISTEN: `127.0.0.1:${port}`,
      FILEX_DATA_DIR: dataDir,
      FILEX_ADMIN_EMAIL: EMAIL,
      FILEX_ADMIN_PASSWORD: PASSWORD,
      FILEX_DEFAULT_LOCALE: 'en',
      FILEX_SECRET_KEY: 'e2e-recovery-shots-key-not-a-real-secret',
      FILEX_INSTALLATION_E2E_ESCROW_KEY: spki,
    },
    stdio: ['ignore', logFd, logFd],
    windowsHide: true,
  });
  let exited = null;
  child.on('exit', (code, signal) => {
    exited = signal ? `signal ${signal}` : `code ${code}`;
  });
  const stop = async () => {
    if (exited === null) child.kill();
    for (let i = 0; i < 50 && exited === null; i++) await new Promise((r) => setTimeout(r, 100));
    fs.closeSync(logFd);
    if (process.env.SHOTS_KEEP) {
      console.log(`kept ${scratch}`);
      return;
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
  const kid = createHash('sha256').update(publicKey).digest().subarray(0, 8).toString('hex');
  return { base, escrowPrivate: privateKey.toString('base64'), storageRoot: storeDir, kid, stop };
}

async function main(expectedKid) {
  fs.mkdirSync(SHOTS, { recursive: true });
  fs.mkdirSync(STORAGE_ROOT, { recursive: true });

  // ── setup over the API ────────────────────────────────────────────
  const login = await api(null, 'POST', '/api/auth/login', {
    email: EMAIL,
    password: PASSWORD,
  });
  if (login.status !== 200) throw new Error(`login failed: ${login.status} ${login.text}`);
  const token = login.json.token;

  const caps = await api(token, 'GET', '/api/capabilities');
  if (expectedKid && caps.json?.e2e_escrow?.kid !== expectedKid) {
    throw new Error(
      `${BASE} publishes escrow key ${caps.json?.e2e_escrow?.kid || '(none)'}, not the ${expectedKid} this ` +
        'script just generated — the port is answering from another filex',
    );
  }
  check(
    'capabilities publish the escrow key so the browser can wrap to it',
    caps.json?.e2e_escrow?.enabled === true && !!caps.json?.e2e_escrow?.public_key,
    `kid=${caps.json?.e2e_escrow?.kid}`,
  );

  await api(token, 'PUT', '/api/auth/profile', { locale: 'en' });

  const existing = await api(token, 'GET', '/api/admin/storages');
  const already = (existing.json?.storages || existing.json || []).find?.(
    (s) => s.name === STORAGE,
  );
  if (!already) {
    const made = await api(token, 'POST', '/api/admin/storages', {
      name: STORAGE,
      driver: 'local',
      mount_path: STORAGE_ROOT,
      config: { path: STORAGE_ROOT },
      enabled: true,
    });
    if (made.status >= 400) throw new Error(`storage create failed: ${made.text}`);
  }

  // A plaintext file that lives OUTSIDE the encrypted folder — the move
  // guard needs something to refuse.
  fs.writeFileSync(path.join(STORAGE_ROOT, 'plain.txt'), 'not a secret\n');

  browser = await chromium.launch();
  const ctx = await browser.newContext({
    viewport: { width: 1280, height: 860 },
    deviceScaleFactor: 2,
    locale: 'en-US',
  });
  // The repository's screenshots are English — see docs/CONTRIBUTING.md,
  // "Release process" step 2. Pin the stored preference before any app code
  // runs; browser locale alone loses to it.
  //
  // ⚠ `filex.tourDone` is not cosmetic here: the onboarding tour's backdrop
  // is `aria-modal` and swallows every click this script needs to make. The
  // first run of this script spent 30s failing to press "New folder" because
  // of it.
  await ctx.addInitScript(() => {
    try {
      localStorage.setItem('filex.locale', 'en');
      localStorage.setItem('filex.tourDone', '1');
      localStorage.setItem('filex.installPrompt.dismissed', '1');
    } catch {
      /* storage blocked */
    }
  });
  const page = await ctx.newPage();
  page.on('console', (m) => {
    if (m.type() === 'error') console.log('    [browser error]', m.text());
  });

  // ── sign in ───────────────────────────────────────────────────────
  await page.goto(`${BASE}/admin/login`, { waitUntil: 'networkidle' });
  await page.locator('input[type="email"], input[name="email"]').first().fill(EMAIL);
  await page.locator('input[type="password"]').first().fill(PASSWORD);
  await page.locator('form button[type="submit"]').first().click();
  // ⚠ `home` since 2026-09-12: signing in lands on /admin/home for every role
  // (web/src/router/index.ts). Without it this waited 20s on a page that had
  // already arrived, and the run died before the first screenshot.
  await page.waitForURL(/\/admin\/(home|dashboard|explore)/, { timeout: 20000 });

  await page.goto(`${BASE}/admin/explore`, { waitUntil: 'networkidle' });
  await page.waitForTimeout(1500);

  // ── 1. create an encrypted folder ─────────────────────────────────
  const folderName = 'Vault';
  await openNewFolder(page);
  await page.waitForTimeout(400);
  await page.getByText(/create encrypted folder/i).first().click();
  await page.waitForTimeout(400);

  const createDialog = page.locator('.fe-modal__card');
  await createDialog.locator('input[type="text"]').first().fill(folderName);
  const pws = createDialog.locator('input[type="password"]');
  await pws.nth(0).fill(FOLDER_PW);
  await pws.nth(1).fill(FOLDER_PW);

  const escrowNoticeVisible = await createDialog
    .locator('.fe-e2e-form .fe-e2e-rk__escrow')
    .first()
    .isVisible()
    .catch(() => false);
  check(
    'the create dialog discloses escrow BEFORE the folder exists',
    escrowNoticeVisible,
  );
  await shot(page, 'create-encrypted-folder.png');

  await createDialog.locator('input[type="checkbox"]').first().check();
  await createDialog.getByRole('button', { name: /create encrypted folder/i }).click();

  // ── 2. the recovery key, shown once ───────────────────────────────
  const keyEl = page.locator('.fe-e2e-rk__key');
  await keyEl.waitFor({ state: 'visible', timeout: 20000 });
  const recoveryKey = (await keyEl.innerText()).trim();
  check(
    'a recovery key is shown when the folder is created',
    /^[0-9A-HJKMNP-TV-Z]{4}(-[0-9A-HJKMNP-TV-Z]{4}){7}$/.test(recoveryKey),
    recoveryKey.slice(0, 9) + '…',
  );
  const escrowInKeyDialog = await page.locator('.fe-e2e-rk .fe-e2e-rk__escrow').isVisible();
  check('the key dialog repeats that the operator holds a key too', escrowInKeyDialog);
  await shot(page, 'recovery-key-shown-once.png');

  // The dialog must not be dismissable until acknowledged — the key cannot
  // be shown again, so an accidental ESC is data loss.
  await page.keyboard.press('Escape');
  await page.waitForTimeout(300);
  check(
    'ESC does not discard the key before it is acknowledged',
    await keyEl.isVisible(),
  );

  await page.locator('.fe-e2e-ack input[type="checkbox"]').check();
  await page.getByRole('button', { name: /^done$/i }).click();
  await keyEl.waitFor({ state: 'hidden', timeout: 10000 });

  // ── 3. put a file in it, then lose the password ───────────────────
  await page.waitForTimeout(1200);
  const marker = path.join(STORAGE_ROOT, folderName, '.filex-e2e.json');
  const markerJson = JSON.parse(fs.readFileSync(marker, 'utf8'));
  check(
    'the marker on disk is v2 with a recovery slot and an escrow slot',
    markerJson.v === 2 && !!markerJson.rk && !!markerJson.esc,
    `v=${markerJson.v} fmk=${markerJson.fmk} esc.kid=${markerJson.esc?.kid}`,
  );
  check(
    'the marker contains neither the password nor the recovery key',
    !JSON.stringify(markerJson).includes(FOLDER_PW) &&
      !JSON.stringify(markerJson).includes(recoveryKey.replace(/-/g, '')),
  );

  await page.getByText(folderName, { exact: true }).first().dblclick();
  await page.waitForTimeout(1200);

  const upload = page.locator('input[type="file"]').first();
  const secretPath = path.join(os.tmpdir(), 'filex-e2e-recovery-secret', 'secret.txt');
  fs.mkdirSync(path.dirname(secretPath), { recursive: true });
  fs.writeFileSync(secretPath, 'the treasure is buried under the third oak\n');
  await upload.setInputFiles(secretPath);
  await page.waitForTimeout(2500);

  const onDisk = path.join(STORAGE_ROOT, folderName, 'secret.txt');
  /* ⚠ POLL, do not sleep and hope. The upload is a round trip: on a loaded
     machine the fixed wait above expired before the bytes reached the disk,
     this read returned `magic=` (no file at all), and the run stopped here
     — at a scene that is not about timing, in the middle of a release
     (v0.43.0). Waiting for the eight bytes to BE there is the same check,
     without the race; a file that never appears still fails, 15s later. */
  let head = '';
  for (let i = 0; i < 60; i++) {
    head = fs.existsSync(onDisk) ? fs.readFileSync(onDisk).subarray(0, 8).toString() : '';
    if (head.length === 8) break;
    await page.waitForTimeout(250);
  }
  check('the uploaded file is ciphertext on disk', head === 'filexe2e', `magic=${head}`);

  // ── 4. the move guard ─────────────────────────────────────────────
  const guard = await api(token, 'POST', '/api/files/copy', {
    source: [`${STORAGE}://plain.txt`],
    target: `${STORAGE}://${folderName}`,
  });
  check(
    'copying a plaintext file INTO the encrypted folder is refused',
    guard.status === 409 && /unencrypted/i.test(guard.text),
    `${guard.status} ${guard.json?.error?.slice(0, 60) || ''}`,
  );
  const guardOut = await api(token, 'POST', '/api/files/move', {
    source: [`${STORAGE}://${folderName}/secret.txt`],
    target: `${STORAGE}://`,
  });
  check(
    'moving an encrypted file OUT of the folder is refused',
    guardOut.status === 409,
    `${guardOut.status} ${guardOut.json?.error?.slice(0, 60) || ''}`,
  );

  // ── 5. lose the password: reload drops the key from memory ────────
  await page.reload({ waitUntil: 'networkidle' });
  await page.waitForTimeout(2000);
  const lock = page.locator('.fe-e2e-lock');
  await lock.waitFor({ state: 'visible', timeout: 20000 });

  await lock.locator('input[type="password"]').fill('definitely-not-the-password');
  await lock.getByRole('button', { name: /^unlock$/i }).click();
  await page.waitForTimeout(1500);
  check(
    'a wrong password is refused at the lock screen',
    await page.locator('.fe-e2e-lock .fe-form__error').isVisible(),
  );
  await shot(page, 'locked-folder.png');

  // ── 6. get back in with the recovery key ──────────────────────────
  await page.locator('.fe-e2e-optlink').click();
  await page.waitForTimeout(600);
  await shot(page, 'unlock-with-recovery-key.png');

  const recoverDialog = page.locator('.fe-modal__card');
  // A wrong key first: "it failed cleanly" is the claim being measured.
  await recoverDialog
    .locator('.fe-e2e-recover__key')
    .fill('AAAA-BBBB-CCCC-DDDD-EEEE-FFFF-GGGG-HHHH');
  await recoverDialog.getByRole('button', { name: /^unlock$/i }).click();
  await page.waitForTimeout(1500);
  check(
    'a wrong recovery key is refused, without unlocking anything',
    await recoverDialog.locator('.fe-form__error').isVisible(),
  );

  await recoverDialog.locator('.fe-e2e-recover__key').fill(recoveryKey);
  await recoverDialog.getByRole('button', { name: /^unlock$/i }).click();
  await page.waitForTimeout(2500);
  const stripVisible = await page.locator('.fe-e2e-strip').isVisible();
  check('the right recovery key opens the folder', stripVisible);

  // And the contents really decrypt — an unlocked shell proves nothing.
  const listed = await page.locator('.fe-list, .fe-grid').first().innerText();
  check('the folder lists its files once unlocked', /secret\.txt/.test(listed));
  await shot(page, 'unlocked-with-recovery-key.png');

  // ── 7. the escrow key, and the notification it produces ───────────
  if (ESCROW_PRIVATE) {
    const before = await api(token, 'GET', '/api/notifications?limit=50');
    const beforeCount = (before.json?.items || []).filter(
      (n) => n.event === 'e2e.escrow_used',
    ).length;

    await page.reload({ waitUntil: 'networkidle' });
    await page.waitForTimeout(2000);
    await page.locator('.fe-e2e-lock').waitFor({ state: 'visible', timeout: 20000 });
    await page.locator('.fe-e2e-optlink').click();
    await page.waitForTimeout(600);

    const d = page.locator('.fe-modal__card');
    await d.getByRole('tab', { name: /escrow key/i }).click();
    await page.waitForTimeout(300);
    await shot(page, 'unlock-with-escrow-key.png');
    await d.locator('.fe-e2e-recover__escrow').fill(ESCROW_PRIVATE);
    await d.getByRole('button', { name: /^unlock$/i }).click();
    await page.waitForTimeout(3500);

    check('the escrow key opens the folder', await page.locator('.fe-e2e-strip').isVisible());

    const after = await api(token, 'GET', '/api/notifications?limit=50');
    const rows = (after.json?.items || []).filter((n) => n.event === 'e2e.escrow_used');
    check(
      'using the escrow key notifies — the event arrives',
      rows.length === beforeCount + 1,
      rows[0] ? `${rows[0].title} · ${rows[0].body}` : 'no row',
    );

    // A report with a wrong nonce must NOT produce a notification: the
    // proof-of-possession is what makes the notification mean something.
    const ch = await api(token, 'POST', '/api/files/e2e/escrow/challenge', {
      path: `${STORAGE}://${folderName}`,
    });
    const forged = await api(token, 'POST', '/api/files/e2e/escrow/used', {
      path: `${STORAGE}://${folderName}`,
      id: ch.json?.id,
      nonce: Buffer.alloc(32).toString('base64'),
    });
    const afterForge = await api(token, 'GET', '/api/notifications?limit=50');
    const forgedRows = (afterForge.json?.items || []).filter(
      (n) => n.event === 'e2e.escrow_used',
    ).length;
    check(
      'a forged escrow report is rejected and notifies nobody',
      forged.status === 403 && forgedRows === rows.length,
      `${forged.status}`,
    );

    // ⚠ Open the bell before the shutter. This picture is named for the
    // notification, and until 2026-09-14 it was taken of the unlocked listing
    // with nothing open — byte-identical to unlocked-with-recovery-key.png, a
    // caption the pixels did not back. The header has had a bell since the
    // 2026-09-14 shell (web/src/components/NotificationBell.vue), so the row
    // the event produced is one click away; the panel fetches on open.
    //
    // ⚠ Matched on "Encrypted folder opened", not on /escrow/: the bell renders
    // the event through web/src/lib/notificationText.ts, whose wording is its
    // own, so the server's title is not what is on screen.
    //
    // ⚠ And the page is RELOADED first, which is why the folder is locked
    // behind the panel. Measured 2026-09-14 on this build: the bell's list is
    // fetched on the panel's first open in a page's lifetime only — a
    // notification that arrives afterwards moves the badge but never reaches
    // the list, however many times the panel is closed and reopened (reported
    // as a product defect, not worked around in the product). Reopening in the
    // same page photographed two "New file" rows under a badge of 3.
    const bell = page.locator('[data-testid="notification-bell"]');
    const escrowRow = page.locator('[data-testid="notification-row"]', {
      hasText: /encrypted folder opened/i,
    });
    let rowShown = false;
    for (let attempt = 0; attempt < 3 && !rowShown; attempt++) {
      await page.reload({ waitUntil: 'networkidle' });
      await bell.first().waitFor({ state: 'visible', timeout: 20000 }).catch(() => {});
      await page.waitForTimeout(1500);
      if (!(await bell.count())) break;
      await bell.first().click();
      rowShown = await escrowRow
        .first()
        .waitFor({ state: 'visible', timeout: 8000 })
        .then(() => true)
        .catch(() => false);
    }
    const bellRows = (await page.locator('[data-testid="notification-row"]').allInnerTexts()).map(
      (t) => t.replace(/\s+/g, ' ').trim(),
    );
    check(
      'the escrow notification is on screen in the bell before it is photographed',
      rowShown,
      (await bell.count())
        ? `rows: [${bellRows.join(' | ')}]`
        : 'no [data-testid="notification-bell"] in the header',
    );
    await page.mouse.move(4, 4);
    await shot(page, 'escrow-used-notification.png');
    await page.keyboard.press('Escape');
  } else {
    console.log('SKIP  escrow flow — pass --escrow-private <pkcs8-b64>');
  }

  // ── 8. a folder from BEFORE recovery existed ──────────────────────
  //
  // The hardest case in the whole change: a v1 folder must keep opening, and
  // the offer to give it a recovery key must be visible rather than silent.
  const legacyName = 'Legacy';
  const legacyPw = 'old-folder-password';
  const legacyText = 'written under v1, long before recovery keys\n';
  {
    const made = await e2ecrypto.createMarker(legacyPw);
    if (made.marker.v !== 1) throw new Error('the seeded folder is not v1');
    const cipher = await e2ecrypto.encryptFile(
      made.kek,
      new TextEncoder().encode(legacyText).buffer,
    );
    // Written THROUGH filex, not behind its back: the marker has to reach the
    // node cache, because that cache is what tells the backend a folder is
    // encrypted (internal/e2e FindRoot). Files dropped straight onto disk are
    // invisible until a sync run, and this is not the place to test sync.
    await api(token, 'POST', '/api/files/manager?action=newfolder', {
      path: `${STORAGE}://`,
      name: legacyName,
    });
    const form = new FormData();
    form.append('path', `${STORAGE}://${legacyName}`);
    form.append(
      'file[]',
      new Blob([JSON.stringify(made.marker)], { type: 'application/json' }),
      '.filex-e2e.json',
    );
    form.append(
      'file[]',
      new Blob([new Uint8Array(cipher)], { type: 'application/octet-stream' }),
      'old-notes.txt',
    );
    const up = await fetch(`${BASE}/api/files/manager?action=upload`, {
      method: 'POST',
      headers: { Authorization: `Bearer ${token}` },
      body: form,
    });
    if (!up.ok) throw new Error(`legacy seed upload failed: ${up.status} ${await up.text()}`);
    await new Promise((r) => setTimeout(r, 1500));
  }

  await page.goto(`${BASE}/admin/explore?storage=${STORAGE}`, { waitUntil: 'networkidle' });
  await page.waitForTimeout(2500);
  await page.getByText(legacyName, { exact: true }).first().dblclick();
  await page.waitForTimeout(1500);
  // ⚠ The double-click's first click SELECTS the row, and the selection
  // survives opening the folder: the lock screen below came out under a
  // "1 selected" bar with nothing listed (2026-09-14, reported as a product
  // quirk). Cleared here so the pictures show the lock screen, not the bar.
  const clearSel = page.locator('[data-testid="selection-clear"]');
  if (await clearSel.count()) {
    await clearSel.first().click().catch(() => {});
    await page.waitForTimeout(400);
  }

  const legacyLock = page.locator('.fe-e2e-lock');
  await legacyLock.waitFor({ state: 'visible', timeout: 20000 });

  // The recovery dialog must SAY there is no recovery here, rather than
  // offering a door that does not exist.
  await page.locator('.fe-e2e-optlink').click();
  await page.waitForTimeout(600);
  const noneMsg = await page.locator('.fe-e2e-recover__none').isVisible();
  check('a pre-0.31 folder says plainly that it has no recovery key', noneMsg);
  await shot(page, 'legacy-folder-no-recovery.png');
  await page.getByRole('button', { name: /^cancel$/i }).click();
  await page.waitForTimeout(400);

  await legacyLock.locator('input[type="password"]').fill(legacyPw);
  await legacyLock.getByRole('button', { name: /^unlock$/i }).click();
  await page.waitForTimeout(2500);

  check(
    'a folder created by the previous release still opens with its password',
    await page.locator('.fe-e2e-strip').isVisible(),
  );
  const offer = page.locator('.fe-e2e-upgrade');
  check('and filex OFFERS it a recovery key rather than acting silently', await offer.isVisible());
  const offerText = await offer.innerText();
  check(
    'the offer discloses that accepting also gives the operator a key',
    /escrow/i.test(offerText),
  );
  await shot(page, 'legacy-folder-upgrade-offer.png');

  await offer.getByRole('button', { name: /create a recovery key/i }).click();
  const legacyKeyEl = page.locator('.fe-e2e-rk__key');
  await legacyKeyEl.waitFor({ state: 'visible', timeout: 20000 });
  const legacyRecoveryKey = (await legacyKeyEl.innerText()).trim();
  await shot(page, 'legacy-folder-recovery-key.png');
  await page.locator('.fe-e2e-ack input[type="checkbox"]').check();
  await page.getByRole('button', { name: /^done$/i }).click();
  await page.waitForTimeout(1500);

  // The measurement that matters: the marker the UI just wrote, plus the file
  // that was on disk BEFORE the upgrade, opened by the key the UI just showed.
  {
    const m = JSON.parse(
      fs.readFileSync(path.join(STORAGE_ROOT, legacyName, '.filex-e2e.json'), 'utf8'),
    );
    check(
      'the upgraded marker keeps the v1 key derivation (fmk: kek)',
      m.v === 2 && m.fmk === 'kek' && !!m.rk && !!m.esc,
      `v=${m.v} fmk=${m.fmk}`,
    );
    const parsed = e2ecrypto.parseMarker(JSON.stringify(m));
    const viaKey = await e2ecrypto.unlockWithRecoveryKey(parsed, legacyRecoveryKey);
    const bytes = fs.readFileSync(path.join(STORAGE_ROOT, legacyName, 'old-notes.txt'));
    const plain = viaKey
      ? new TextDecoder().decode(
          await e2ecrypto.decryptFile(
            viaKey,
            bytes.buffer.slice(bytes.byteOffset, bytes.byteOffset + bytes.byteLength),
          ),
        )
      : '';
    check(
      'the new recovery key opens a file written before the upgrade',
      plain === legacyText,
      plain ? 'byte-identical' : 'FAILED TO DECRYPT',
    );
    const viaPw = await e2ecrypto.unlockWithPassword(parsed, legacyPw);
    const plain2 = viaPw
      ? new TextDecoder().decode(
          await e2ecrypto.decryptFile(
            viaPw,
            bytes.buffer.slice(bytes.byteOffset, bytes.byteOffset + bytes.byteLength),
          ),
        )
      : '';
    check('and the original password still opens it too', plain2 === legacyText);
  }

  await browser.close();

  console.log('\n─── summary ───');
  const failed = results.filter((r) => !r.ok);
  console.log(`${results.length - failed.length}/${results.length} checks passed`);
  if (failed.length) {
    for (const f of failed) console.log(`  FAILED: ${f.name} ${f.detail}`);
    process.exitCode = 1;
  }
  console.log(`screenshots → ${SHOTS}`);
}

// The browser lives here rather than inside main() so that a run which throws
// half-way still closes it: on Windows an orphaned Chromium outlives the node
// process that launched it.
let browser = null;
let instance = null;
try {
  if (!BASE) {
    instance = await bootEscrowInstance();
    BASE = instance.base;
    ESCROW_PRIVATE = instance.escrowPrivate;
    STORAGE_ROOT = instance.storageRoot;
  }
  await main(instance?.kid);
} catch (e) {
  console.error(e);
  process.exitCode = 1;
} finally {
  await browser?.close().catch(() => {});
  await instance?.stop();
}
