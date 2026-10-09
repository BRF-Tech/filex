// The vault, encryption level 3 (docs/E2E-VAULT-FORMAT.md; task #94): the
// level in the picker, and the strip that says who writes and when the vault
// locks itself.
//
//   node e2e/shots/vault.mjs     (from the repo root; `pnpm shots` runs it)
//
// Writes e2e/.artifacts/shots/capture/vault/ (or SHOTS_OUT):
//
//   level-picker-vault.png        New folder → Create encrypted folder…, level 3
//                                 chosen: what it costs, and the pack size
//   vault-strip-read-1280.png     an open vault nobody writes to: read-only, and
//                                 the 15-minute countdown to the vault locking
//   vault-strip-write-1280.png    after a change: this tab writes, and how long
//                                 until it goes back to read-only
//   vault-strip-held-1280.png     a second tab of the same person: somebody else
//                                 writes, since when, and "Take over"
//   vault-strip-held-390-dark.png the same at 390 px, dark
//
// It also MEASURES the explorer in a vault at 390 and 1280 px, light and
// dark, in English and Turkish (no sideways scroll, no control on another).
//
// ⚠ The instance is booted with FILEX_E2E_VAULT=1: the vault is off by
// default until it ships, and nothing offers it on a server without it.
//
// Environment: FILEX_BIN, SHOTS_OUT, SHOTS_KEEP (see apps.mjs).

import { mkdtempSync, rmSync, utimesSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { chromium } from '@playwright/test';
import { fixtureTime } from './clock.mjs';
import { addLocalStorage, bootInstance, client, dismissToasts, layoutProblems, log, mustSay, newContext, setLanguage, shot, shootWhole, signIn, sleep } from './scene.mjs';

const SET = 'vault';
const ADMIN = { email: 'demo@demo.com', password: 'demo-shots' };
const VAULT_PW = 'correct horse battery staple';

/**
 * The files the scene uploads into the vault, written to `dir` with fixed
 * times; their paths, for setInputFiles.
 *
 * ⚠ Inside a vault a file's Modified time is the one its File says
 * (`lastModified`; docs/E2E-VAULT-FORMAT.md, an entry's `mtime`: "what the
 * uploader said"), kept in the encrypted index. No answer of the server
 * carries it, so the scene clock's route (clock.mjs) cannot move it. A buffer
 * handed to setInputFiles became a File stamped with the REAL now - the
 * browser's File constructor does not read the page's clock - and the 0.54
 * pictures showed the day the scene ran beside the scene's September 15. A
 * path carries its file's mtime (Playwright reads it), pinned here to the
 * fixtures' fixed times (clock.mjs fixtureTime), the same in every run.
 */
function vaultUploads(dir) {
  const files = [
    ['Board minutes 2026-09.md', '# Board minutes\n\nOnly in the vault.\n'],
    ['Salaries 2026.csv', 'name;salary\nDeniz Kaya;4200\n'],
  ];
  return files.map(([name, body]) => {
    const full = join(dir, name);
    writeFileSync(full, body);
    const at = new Date(fixtureTime(`vault/${name}`));
    utimesSync(full, at, at);
    return full;
  });
}

async function openTeam(page, url) {
  await page.goto(`${url}/drive/explore#${encodeURIComponent('Team')}`);
  // ⚠ The listing, not the side panel's New button: at 390 px the panel is a
  // drawer, closed until asked for, so its button is not on screen and the
  // 390-px held-strip step waited 20 s for it (0.54 nightly 001b652e, after
  // four pictures). The listing is there at every width.
  await page.locator('.fe__body').first().waitFor({ state: 'visible', timeout: 20_000 });
  await page.locator('.fe__body [aria-busy="true"]').first().waitFor({ state: 'detached', timeout: 20_000 });
  await sleep(1200);
}

function row(page, name) {
  return page.locator('.fe-list__row[data-fe-path]').filter({ has: page.locator('.fe-list__name', { hasText: name }) }).first();
}

async function newMenu(page, item) {
  await page.getByTestId('sidenav-new').first().click();
  await page.locator('.fe-ctx__item', { has: page.locator('.fe-ctx__label', { hasText: item }) }).first().click();
}

async function openVault(page) {
  await row(page, 'Vault').dblclick();
  await sleep(800);
}

async function unlock(page) {
  const lock = page.locator('.fe-e2e-lock');
  const strip = page.getByTestId('e2e-vault-strip');
  // ⚠ A tab that unlocked the vault once keeps it open across a reload: the
  // measuring loop opens it again in the same tab for its second language,
  // and there is no lock screen to type into then (0.54, PC run of this
  // scene: 20 s waiting for `.fe-e2e-lock`). Either is a vault on screen.
  await lock.or(strip).first().waitFor({ state: 'visible', timeout: 20_000 });
  if (await lock.isVisible()) {
    await lock.locator('input[type="password"]').fill(VAULT_PW);
    await lock.getByRole('button', { name: /^(unlock|kilidi aç)$/i }).click();
  }
  await strip.waitFor({ state: 'visible', timeout: 30_000 });
  await sleep(600);
}

async function main() {
  const inst = await bootInstance({ name: SET, admin: ADMIN, env: { FILEX_UPDATE_CHECK: '0', FILEX_E2E_VAULT: '1' } });
  const browser = await chromium.launch();
  const uploads = mkdtempSync(join(tmpdir(), 'filex-shots-vault-'));
  try {
    const admin = client(inst.url);
    await admin.login(ADMIN.email, ADMIN.password);
    await admin.patch('/api/auth/profile', { locale: 'en', display_name: 'Dana Reyes' });
    await addLocalStorage(admin, 'Team', inst.storageRoot('team'), { onHost: !inst.container });
    const caps = await admin.json('/api/files/capabilities');
    if (caps?.e2e_vault !== true) throw new Error('the instance does not offer the vault (FILEX_E2E_VAULT)');

    // ── 1. the level picker: level 3, its cost, the pack size ─────────────
    const ctx = await newContext(browser, { width: 1280, height: 900 });
    const page = await ctx.newPage();
    await signIn(page, inst.url, ADMIN);
    await openTeam(page, inst.url);
    await newMenu(page, /^New folder$/);
    await page.getByText('Create encrypted folder…', { exact: true }).click();
    const dialog = page.locator('.fe-modal__card').filter({ has: page.getByTestId('e2e-create-names') });
    await dialog.waitFor({ state: 'visible', timeout: 10_000 });
    await dialog.locator('input[type="text"]').first().fill('Vault');
    const pws = dialog.locator('input[type="password"]');
    await pws.nth(0).fill(VAULT_PW);
    await pws.nth(1).fill(VAULT_PW);
    await dialog.getByTestId('e2e-level-vault').check();
    await dialog.getByTestId('e2e-vault-pack').waitFor({ state: 'visible', timeout: 5_000 });
    await mustSay(dialog, 'the level picker', ['3 · Vault', 'What it costs', 'Pack size', '4 MiB', '16 MiB']);
    await page.mouse.move(4, 4);
    await sleep(300);
    await shootWhole(page, dialog, SET, 'level-picker-vault.png', { restore: { width: 1280, height: 900 } });
    await dialog.getByTestId('e2e-create-ack').check();
    await dialog.getByRole('button', { name: 'Create encrypted folder', exact: true }).click();
    await page.locator('.fe-e2e-rk__key').waitFor({ state: 'visible', timeout: 30_000 });
    await page.locator('.fe-e2e-rk .fe-e2e-ack input[type="checkbox"]').check();
    await page.getByRole('button', { name: 'Done', exact: true }).click();
    await sleep(600);

    // ── 2. an open vault, read-only ──────────────────────────────────────
    await openVault(page);
    await page.getByTestId('e2e-vault-strip').waitFor({ state: 'visible', timeout: 20_000 });
    await page.getByTestId('e2e-vault-lock-countdown').waitFor({ state: 'visible', timeout: 10_000 });
    await dismissToasts(page);
    await page.mouse.move(4, 4);
    await shot(page, SET, 'vault-strip-read-1280.png');

    // ── 3. after a change: this tab writes ───────────────────────────────
    await newMenu(page, /^New folder$/);
    const nf = page.getByRole('dialog', { name: 'New folder' });
    await nf.waitFor({ state: 'visible', timeout: 10_000 });
    await nf.getByRole('textbox').fill('Contracts');
    await nf.getByRole('button', { name: 'Create', exact: true }).click();
    await row(page, 'Contracts').waitFor({ state: 'visible', timeout: 30_000 });
    await page.locator('input[type="file"]').first().setInputFiles(vaultUploads(uploads));
    await row(page, 'Salaries 2026.csv').waitFor({ state: 'visible', timeout: 60_000 });
    await mustSay(page.getByTestId('e2e-vault-strip'), 'the writing strip', ['You are writing to this vault']);
    await dismissToasts(page);
    await page.mouse.move(4, 4);
    await shot(page, SET, 'vault-strip-write-1280.png');

    // ── 4. a second tab of the same person: somebody else writes ─────────
    const ctx2 = await newContext(browser, { width: 1280, height: 900 });
    const other = await ctx2.newPage();
    await signIn(other, inst.url, ADMIN);
    await openTeam(other, inst.url);
    await openVault(other);
    await unlock(other);
    // The state is asked every 30 seconds; the strip names the writer then.
    await other.getByTestId('e2e-vault-take-over').waitFor({ state: 'visible', timeout: 45_000 });
    await mustSay(other.getByTestId('e2e-vault-strip'), 'the held strip', ['read-only for you', 'Take over']);
    await dismissToasts(other);
    await other.mouse.move(4, 4);
    await shot(other, SET, 'vault-strip-held-1280.png');

    // The same at 390 px, dark - while the first tab still writes (its idle
    // time is 3 minutes, the default).
    const ctx3 = await newContext(browser, { scheme: 'dark', width: 390, height: 844 });
    const phone = await ctx3.newPage();
    await signIn(phone, inst.url, ADMIN);
    await openTeam(phone, inst.url);
    await openVault(phone);
    await unlock(phone);
    await phone.getByTestId('e2e-vault-take-over').waitFor({ state: 'visible', timeout: 45_000 });
    await dismissToasts(phone);
    await phone.mouse.move(2, 2);
    await shot(phone, SET, 'vault-strip-held-390-dark.png');
    await ctx3.close();

    // ── 5. measure: 390 and 1280 px, light and dark, English and Turkish ─
    const problems = [];
    for (const scheme of ['light', 'dark']) {
      for (const width of [390, 1280]) {
        const mctx = await newContext(browser, { scheme, width, height: 844 });
        const m = await mctx.newPage();
        await signIn(m, inst.url, ADMIN);
        for (const locale of ['en', 'tr']) {
          await setLanguage(admin, m, locale);
          // ⚠ A new document, or nothing changes language: the tab already
          // shows /drive/explore#Team/Vault, and a `goto` that differs only
          // after the `#` is a same-document navigation - the app is not
          // loaded again and the Turkish round measured the English screen
          // (a PC run of this scene, 0.54).
          await m.goto('about:blank');
          await openTeam(m, inst.url);
          await m.waitForFunction((l) => document.documentElement.lang === l, locale, { timeout: 20_000 });
          await openVault(m);
          await unlock(m);
          // The filter row scrolls on itself at 390 px, its "Listing actions"
          // pinned over the chips that slide under it (base.css
          // .fe-filterbar, .fe-filterbar__actions): the row must fit, not
          // what it scrolls.
          for (const x of await layoutProblems(m, { scrollers: ['.fe-filterbar'] })) problems.push(`${scheme} ${width}px ${locale}: ${x}`);
        }
        await setLanguage(admin, m, 'en');
        await mctx.close();
      }
    }
    if (problems.length) throw new Error(`layout problems:\n  ${problems.join('\n  ')}`);
    log('the vault strip fits at 390 and 1280 px, light and dark, in English and Turkish');

    await ctx2.close();
    await ctx.close();
  } finally {
    await browser.close();
    await inst.stop();
    rmSync(uploads, { recursive: true, force: true });
  }
}

main().catch((err) => {
  console.error('✗', err.stack ?? err.message);
  process.exit(1);
});
