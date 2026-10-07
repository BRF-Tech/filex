// Admin → Multi-tenant mode (task #167, docs/MULTI-TENANCY.md → Mode gating):
//
//   node e2e/shots/tenancy.mjs     (from the repo root; `pnpm shots` runs it)
//
// Writes e2e/.artifacts/shots/capture/tenancy/ (or SHOTS_OUT):
//
//   restart-1440.png         the switch turned on, saved for the next start:
//                            "restart filex to apply the change"
//   confirm-1440.png         turning it off on an install with two tenants:
//                            how many, what happens to them, nothing deleted,
//                            and the box for their number
//   confirm-dark-tr-1440.png the same warning in Turkish, in the dark theme
//   locked-1440.png          FILEX_MULTI_TENANT=1 pins the mode: the switch
//                            locked, naming the variable
//   page-390.png             a phone: the page fits, nothing scrolls sideways
//
// It also MEASURES, in a real browser: the page has no layout problem at
// 1440 and 390 px in English and Turkish, the warning names the number of
// tenants, and its "Turn off" stays disabled until that number is typed. A
// failed measurement throws; the pictures are only written by a run that
// passed it.
//
// ⚠ The tenants are made through the API on a single-tenant instance (the
// Tenants API answers there too) and the switch is saved ON for the next
// start: that is the only state in which turning it off asks for a number
// without restarting the instance between pictures.
//
// Environment: FILEX_BIN, SHOTS_OUT, SHOTS_KEEP (see apps.mjs).

import { chromium } from '@playwright/test';
import { bootInstance, client, layoutProblems, log, mustSay, newContext, setLanguage, shot, shootWhole, signIn, sleep } from './scene.mjs';

const SET = 'tenancy';
const ADMIN = { email: 'demo@demo.com', password: 'demo-shots' };
const TENANTS = [
  { slug: 'acme', name: 'Acme Corp' },
  { slug: 'northwind', name: 'Northwind Traders' },
];

async function openPage(page, url) {
  await page.goto(`${url}/admin/tenancy`);
  await page.getByTestId('tenancy-page').waitFor({ timeout: 20_000 });
  await page.getByTestId('tenancy-switch').waitFor({ timeout: 20_000 });
  await sleep(400);
}

async function main() {
  const inst = await bootInstance({ name: SET, admin: ADMIN, env: { FILEX_UPDATE_CHECK: '0' } });
  const locked = await bootInstance({ name: `${SET}-locked`, admin: ADMIN, env: { FILEX_UPDATE_CHECK: '0', FILEX_MULTI_TENANT: '1' } });
  const browser = await chromium.launch();
  try {
    const admin = client(inst.url);
    await admin.login(ADMIN.email, ADMIN.password);
    await admin.patch('/api/auth/profile', { locale: 'en', display_name: 'Dana Reyes' });
    for (const t of TENANTS) await admin.post('/api/admin/providers/', { slug: t.slug, name: t.name, realm: t.slug });
    // On for the next start: the page then says "restart", and turning it off asks.
    const saved = await admin.json('/api/admin/tenancy', { method: 'PUT', body: JSON.stringify({ enabled: true }) });
    if (!saved.restart_required || saved.tenants !== TENANTS.length) {
      throw new Error(`the switch did not save for the next start: ${JSON.stringify(saved)}`);
    }

    // ── measure: both languages, both widths ──────────────────────────────
    const ctx = await newContext(browser, { width: 1440, height: 900 });
    const page = await ctx.newPage();
    await signIn(page, inst.url, ADMIN);
    const problems = [];
    for (const locale of ['en', 'tr']) {
      await setLanguage(admin, page, locale);
      for (const width of [1440, 390]) {
        await page.setViewportSize({ width, height: 900 });
        await openPage(page, inst.url);
        for (const x of await layoutProblems(page)) problems.push(`${locale} ${width}px: ${x}`);
      }
    }
    if (problems.length) throw new Error(`layout problems:\n  ${problems.join('\n  ')}`);
    log('the page fits at 1440 and 390 px, in English and Turkish');

    // ── 1. turned on, waiting for a restart ───────────────────────────────
    await setLanguage(admin, page, 'en');
    await page.setViewportSize({ width: 1440, height: 760 });
    await openPage(page, inst.url);
    await mustSay(page.getByTestId('tenancy-restart'), 'the restart notice', ['Restart filex to apply the change']);
    await shot(page, SET, 'restart-1440.png');

    // ── 2. turning it off: the warning, the number ────────────────────────
    await page.getByTestId('tenancy-switch').getByRole('switch').click();
    const dialog = page.getByTestId('tenancy-confirm');
    await dialog.waitFor({ state: 'visible', timeout: 10_000 });
    await mustSay(dialog, 'the warning', ['2 tenants besides the platform', 'maintenance mode', 'Nothing is deleted']);
    const off = page.getByTestId('tenancy-confirm-off');
    if (!(await off.isDisabled())) throw new Error('"Turn off" is enabled before the number is typed');
    await page.getByTestId('tenancy-confirm-input').locator('input').fill('2');
    if (await off.isDisabled()) throw new Error('"Turn off" stays disabled with the right number typed');
    await sleep(300);
    await shootWhole(page, page.locator('dialog[open] [role="dialog"]'), SET, 'confirm-1440.png', { restore: { width: 1440, height: 760 } });
    await page.keyboard.press('Escape');
    await ctx.close();

    // ── 3. the same warning in Turkish, dark ──────────────────────────────
    const dark = await newContext(browser, { scheme: 'dark', width: 1440, height: 900 });
    const dpage = await dark.newPage();
    await signIn(dpage, inst.url, ADMIN);
    await setLanguage(admin, dpage, 'tr');
    await openPage(dpage, inst.url);
    await dpage.getByTestId('tenancy-switch').getByRole('switch').click();
    const ddialog = dpage.getByTestId('tenancy-confirm');
    await ddialog.waitFor({ state: 'visible', timeout: 10_000 });
    await mustSay(ddialog, 'the Turkish warning', ['platformun kendi kiracısı dışında 2 kiracı var', 'Hiçbir şey silinmez']);
    await sleep(300);
    await shootWhole(dpage, dpage.locator('dialog[open] [role="dialog"]'), SET, 'confirm-dark-tr-1440.png');
    await setLanguage(admin, dpage, 'en');
    await dark.close();

    // ── 4. pinned by FILEX_MULTI_TENANT ───────────────────────────────────
    const lctx = await newContext(browser, { width: 1440, height: 760 });
    const lpage = await lctx.newPage();
    await signIn(lpage, locked.url, ADMIN);
    await openPage(lpage, locked.url);
    await mustSay(lpage.getByTestId('tenancy-locked'), 'the lock', ['FILEX_MULTI_TENANT']);
    if (!(await lpage.getByTestId('tenancy-switch').getByRole('switch').isDisabled())) throw new Error('a pinned switch can be clicked');
    await shot(lpage, SET, 'locked-1440.png');
    await lctx.close();

    // ── 5. a phone ────────────────────────────────────────────────────────
    const phone = await newContext(browser, { width: 390, height: 844 });
    const ppage = await phone.newPage();
    await signIn(ppage, inst.url, ADMIN);
    await openPage(ppage, inst.url);
    const sideways = await layoutProblems(ppage);
    if (sideways.length) throw new Error(`the phone page: ${sideways.join('; ')}`);
    await shot(ppage, SET, 'page-390.png');
    await phone.close();
    log('restart notice, the warning in English and Turkish dark, the lock, the phone');
  } finally {
    await browser.close();
    await inst.stop();
    await locked.stop();
  }
}

main().catch((err) => {
  console.error('✗', err.stack ?? err.message);
  process.exit(1);
});
