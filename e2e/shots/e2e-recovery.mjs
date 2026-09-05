/**
 * Measure the folder-recovery flow in a real browser, and capture the
 * screenshots docs/screenshots/e2e-recovery/ shows.
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
 *   FILEX_INSTALLATION_E2E_ESCROW_KEY=<spki> filex serve      # in another shell
 *   node e2e/shots/e2e-recovery.mjs --url http://127.0.0.1:8123 \
 *        --escrow-private <pkcs8-b64> --root <dir the storage points at>
 */
import { chromium } from '@playwright/test';
// The SHIPPED bundle, not a re-implementation: the v1 folder this script
// seeds has to be the same v1 folder the product makes, or the upgrade leg
// measures a fixture instead of the feature.
import * as e2ecrypto from '../../packages/core/dist/filex-core.js';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const REPO = path.resolve(__dirname, '../..');
const SHOTS = path.join(REPO, 'docs/screenshots/e2e-recovery');

function arg(name, fallback) {
  const i = process.argv.indexOf(`--${name}`);
  return i > -1 ? process.argv[i + 1] : fallback;
}

const BASE = arg('url', 'http://127.0.0.1:8123');
const EMAIL = arg('email', 'admin@example.com');
const PASSWORD = arg('password', 'Passw0rd!e2e');
const ESCROW_PRIVATE = arg('escrow-private', process.env.FILEX_E2E_ESCROW_PRIVATE || '');
const STORAGE_ROOT = arg('root', path.join(REPO, '_livedata/store'));
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

async function main() {
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

  const browser = await chromium.launch();
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
  await page.waitForURL(/\/admin\/(dashboard|explore)/, { timeout: 20000 });

  await page.goto(`${BASE}/admin/explore`, { waitUntil: 'networkidle' });
  await page.waitForTimeout(1500);

  // ── 1. create an encrypted folder ─────────────────────────────────
  const folderName = 'Vault';
  await page.getByRole('button', { name: /new folder|yeni klasör/i }).first().click();
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
  await page.screenshot({ path: path.join(SHOTS, 'create-encrypted-folder.png') });

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
  await page.screenshot({ path: path.join(SHOTS, 'recovery-key-shown-once.png') });

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
  const secretPath = path.join(REPO, '_livedata', 'secret.txt');
  fs.writeFileSync(secretPath, 'the treasure is buried under the third oak\n');
  await upload.setInputFiles(secretPath);
  await page.waitForTimeout(2500);

  const onDisk = path.join(STORAGE_ROOT, folderName, 'secret.txt');
  const head = fs.existsSync(onDisk) ? fs.readFileSync(onDisk).subarray(0, 8).toString() : '';
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
  await page.screenshot({ path: path.join(SHOTS, 'locked-folder.png') });

  // ── 6. get back in with the recovery key ──────────────────────────
  await page.locator('.fe-e2e-optlink').click();
  await page.waitForTimeout(600);
  await page.screenshot({ path: path.join(SHOTS, 'unlock-with-recovery-key.png') });

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
  await page.screenshot({ path: path.join(SHOTS, 'unlocked-with-recovery-key.png') });

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
    await page.screenshot({ path: path.join(SHOTS, 'unlock-with-escrow-key.png') });
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
    await page.screenshot({ path: path.join(SHOTS, 'escrow-used-notification.png') });
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

  const legacyLock = page.locator('.fe-e2e-lock');
  await legacyLock.waitFor({ state: 'visible', timeout: 20000 });

  // The recovery dialog must SAY there is no recovery here, rather than
  // offering a door that does not exist.
  await page.locator('.fe-e2e-optlink').click();
  await page.waitForTimeout(600);
  const noneMsg = await page.locator('.fe-e2e-recover__none').isVisible();
  check('a pre-0.31 folder says plainly that it has no recovery key', noneMsg);
  await page.screenshot({ path: path.join(SHOTS, 'legacy-folder-no-recovery.png') });
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
  await page.screenshot({ path: path.join(SHOTS, 'legacy-folder-upgrade-offer.png') });

  await offer.getByRole('button', { name: /create a recovery key/i }).click();
  const legacyKeyEl = page.locator('.fe-e2e-rk__key');
  await legacyKeyEl.waitFor({ state: 'visible', timeout: 20000 });
  const legacyRecoveryKey = (await legacyKeyEl.innerText()).trim();
  await page.screenshot({ path: path.join(SHOTS, 'legacy-folder-recovery-key.png') });
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

main().catch((e) => {
  console.error(e);
  process.exit(1);
});
