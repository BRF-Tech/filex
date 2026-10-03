// Sign-in security — the administrator's page for the sign-in attempt limit,
// and what the sign-in form says once the limit bites (0.50.0, #76 step 1).
//
//   node e2e/shots/loginsecurity.mjs       (from the repo root; `pnpm shots` runs it)
//
// Writes docs/screenshots/<release>/loginsecurity/ (or SHOTS_OUT):
//
//   login-security-1440.png   Admin → Sign-in security, whole page: the limit
//                             numbers, the allowed addresses with "add my
//                             address", the trusted proxies (the automatic
//                             switch and what it resolved to and why, the
//                             loopback, private and link-local switches, the
//                             addresses, where the list in force came from),
//                             the locks and the trail
//   login-security-958.png    the same page at the narrowest desktop width
//   login-locked-1440.png     the sign-in form after the account was locked:
//                             the sentence and the button, counting down
//   login-remaining-1440.png  the sign-in form after a wrong password with
//                             tries left before a lock
//
// It also MEASURES, in a real browser and in both languages (jsdom has no
// layout): at 958 and 1440 px the page has no horizontal scroll, no element of
// the page's own forms sticks out of the window, and the locks and trail
// tables keep their columns inside their own scroll area rather than shedding
// them. A failed measurement throws; the pictures are only written by a run
// that passed it.
//
// ⚠ The wrong attempts are made through the API with X-Forwarded-For: the
// scene's own address is loopback, which the default (`auto`) trusts on every
// install, so the header is believed and each "person" is a different address. The sign-in pictures
// are taken from loopback itself, i.e. as the address the instance sees.
//
// Environment: FILEX_BIN, SHOTS_OUT, SHOTS_KEEP (see apps.mjs).

import { chromium } from '@playwright/test';
import { bootInstance, client, layoutProblems, log, mustSay, newContext, setLanguage, shot, signIn, sleep } from './scene.mjs';

const SET = 'loginsecurity';
const ADMIN = { email: 'demo@demo.com', password: 'demo-shots' };
const PEOPLE = [
  { email: 'deniz@example.com', display_name: 'Deniz Kaya' },
  { email: 'ece@example.com', display_name: 'Ece Aydın' },
  { email: 'sam@example.com', display_name: 'Sam Carter' },
];

/** A wrong password from `ip`, through the trusted proxy header. */
async function wrong(url, email, ip) {
  const res = await fetch(`${url}/api/auth/login`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', 'X-Forwarded-For': ip },
    body: JSON.stringify({ email, password: 'not-the-password' }),
  });
  return res.status;
}

/**
 * An address list's Add button stands level with its box: the same top edge
 * and the same vertical centre (±1 px), whatever line — hint or error — is
 * written under the box. Returns the problems and the numbers measured.
 */
async function addButtonLevel(page, testId) {
  return page.evaluate((id) => {
    const root = document.querySelector(`[data-testid="${id}"]`);
    const input = root?.querySelector('input');
    const add = root?.querySelector('[data-testid="address-add"]');
    if (!input || !add) return { problems: [`${id}: no box or no Add button`], said: '' };
    const a = input.getBoundingClientRect();
    const b = add.getBoundingClientRect();
    const problems = [];
    if (Math.abs(a.top - b.top) > 1) problems.push(`${id}: box top ${a.top.toFixed(1)} vs Add top ${b.top.toFixed(1)}`);
    const ca = a.top + a.height / 2;
    const cb = b.top + b.height / 2;
    if (Math.abs(ca - cb) > 1) problems.push(`${id}: box centre ${ca.toFixed(1)} vs Add centre ${cb.toFixed(1)}`);
    const line = root.querySelector('[data-testid="field-error"], .help-text');
    if (!line) problems.push(`${id}: no hint or error line under the box — nothing was measured with one`);
    const kind = line?.matches('[data-testid="field-error"]') ? 'error' : 'hint';
    const said = `${id} (${kind} shown): box ${a.top.toFixed(1)}+${a.height.toFixed(1)}, Add ${b.top.toFixed(1)}+${b.height.toFixed(1)}`;
    return { problems, said };
  }, testId);
}

async function openPage(page, url, width) {
  await page.setViewportSize({ width, height: 900 });
  await page.goto(`${url}/admin/login-security`);
  await page.getByTestId('login-locks').waitFor({ timeout: 20_000 });
  await page.getByTestId('login-attempts').locator('.fe-list__row').first().waitFor({ timeout: 20_000 });
  // The whole page in one picture: as tall as it is.
  const height = await page.evaluate(() => document.querySelector('main')?.scrollHeight ?? 900);
  await page.setViewportSize({ width, height: Math.min(4000, Math.max(900, height + 160)) });
  await page.mouse.move(4, 4);
  await sleep(500);
}

async function main() {
  const inst = await bootInstance({ name: SET, admin: ADMIN, env: { FILEX_UPDATE_CHECK: '0' } });
  const browser = await chromium.launch();
  try {
    const admin = client(inst.url);
    await admin.login(ADMIN.email, ADMIN.password);
    await admin.patch('/api/auth/profile', { locale: 'en', display_name: 'Dana Reyes' });
    for (const p of PEOPLE) {
      await admin.post('/api/admin/users', {
        email: p.email,
        password: `${p.email.split('@')[0]}-shots-2026`,
        display_name: p.display_name,
        role: 'user',
        locale: 'en',
      });
    }

    // Deniz: five wrong attempts from one address — the account is locked.
    for (let i = 0; i < 5; i++) await wrong(inst.url, 'deniz@example.com', '198.51.100.23');
    // Ece: two wrong attempts from another — counted, not locked.
    for (let i = 0; i < 2; i++) await wrong(inst.url, 'ece@example.com', '198.51.100.77');
    // An address that is being counted on its own (many accounts, one address).
    for (const who of ['sam@example.com', 'nobody@example.com', 'admin@example.com']) await wrong(inst.url, who, '203.0.113.50');
    // The office is exempt; the page shows the list as an administrator left it.
    await admin.patch('/api/admin/login-security', { ip_allowlist: ['192.0.2.0/24'] });

    const state = await admin.json('/api/admin/login-security');
    if (state.settings.account_max_fails !== 5) throw new Error(`unexpected defaults: ${JSON.stringify(state.settings)}`);
    if (typeof state.your_ip !== 'string' || !state.your_ip) throw new Error('the server did not say which address this request comes from');
    const locks = await admin.json('/api/admin/login-security/locks?locked=1');
    if (!locks.items.some((l) => l.scope === 'account' && l.subject === 'deniz@example.com' && l.locked)) {
      throw new Error(`Deniz is not locked: ${JSON.stringify(locks.items)}`);
    }

    const ctx = await newContext(browser, { height: 900 });
    const page = await ctx.newPage();
    await signIn(page, inst.url, ADMIN);

    // ── measure: both widths, both languages ────────────────────────────────
    const problems = [];
    for (const locale of ['en', 'tr']) {
      await setLanguage(admin, page, locale);
      for (const width of [958, 1440]) {
        await openPage(page, inst.url, width);
        for (const p of await layoutProblems(page, { frames: ['login-locks', 'login-attempts'] })) problems.push(`${locale} ${width}px: ${p}`);
        // The Add buttons: level with their box while a hint is shown (the
        // trusted proxies always have one) and while an error is (an entry the
        // server refuses on the allowed addresses; nothing is saved).
        // ⚠ By the forms' own test ids: the editors' ids are a component prop
        // (`test-id`), which web/tests/deploy/shotsFixtures.test.ts cannot see.
        const hinted = await addButtonLevel(page, 'login-proxies-form');
        const allow = page.getByTestId('login-allowlist-form');
        await allow.locator('input').fill('not-an-address');
        await allow.getByTestId('address-add').click();
        await allow.locator('button[type="submit"]').click();
        await allow.getByTestId('field-error').waitFor({ timeout: 15_000 });
        const errored = await addButtonLevel(page, 'login-allowlist-form');
        for (const m of [hinted, errored]) {
          for (const p of m.problems) problems.push(`${locale} ${width}px: ${p}`);
          log(`${locale} ${width}px: ${m.said}`);
        }
        if (locale === 'tr') {
          await mustSay(page.locator('main'), 'the Turkish page', [
            'Giriş güvenliği', 'İzinli adresler', 'Güvenilir vekil sunucular', 'Kilitler', 'Son oturum açma olayları',
            'Bu makine (loopback)', 'Özel ağlar', 'Bağlantı-yerel adresler', 'Otomatik (auto)', 'Otomatik neye çözüldü',
          ]);
        }
      }
    }
    if (problems.length) throw new Error(`layout problems:\n  ${problems.join('\n  ')}`);
    log('no overflow or overlap at 958 and 1440 px, in English and Turkish');

    // ── pictures, English ───────────────────────────────────────────────────
    await setLanguage(admin, page, 'en');
    for (const width of [1440, 958]) {
      await openPage(page, inst.url, width);
      await mustSay(page.locator('main'), 'the Sign-in security page', [
        'Sign-in security', 'Sign-in attempt limit', 'Allowed addresses', 'Trusted proxies', 'Locks',
        'Recent sign-in events', 'deniz@example.com', 'Automatic: the built-in default', 'Add my address',
        'Automatic (auto)', 'What automatic resolves to',
      ]);
      await mustSay(page.getByTestId('login-proxies-classes'), 'the trusted proxy classes', [
        'This machine (loopback)', 'Private networks', 'Link-local addresses',
      ]);
      // While nothing is set the list in force is `auto` (clientip
      // DefaultClasses, since the automatic trusted proxies): its switch is on
      // and the three classes are off - auto resolves them itself. ⚠ This
      // scene still asserted "all three classes on" after that change, and
      // the 0.50 release run stopped here on a page that was right.
      if ((await page.locator('#login-proxies-class-auto').getAttribute('aria-checked')) !== 'true') {
        throw new Error('the automatic switch is not on by default');
      }
      const switches = page.getByTestId('login-proxies-classes').getByRole('switch');
      if ((await switches.count()) !== 3) throw new Error(`expected three class switches, found ${await switches.count()}`);
      for (let i = 0; i < 3; i++) {
        if ((await switches.nth(i).getAttribute('aria-checked')) !== 'false') {
          throw new Error(`class switch ${i} is on by default, beside the automatic list`);
        }
      }
      await mustSay(page.getByTestId('login-locks'), 'the locks table', ['Locked for', 'Counting', 'Account', 'Address']);
      await mustSay(page.getByTestId('login-attempts'), 'the trail', ['Wrong attempt', 'Locked', 'Web form', 'Wrong password']);
      await shot(page, SET, `login-security-${width}.png`);
    }

    // ── the sign-in form, signed out ────────────────────────────────────────
    const out = await newContext(browser, { height: 900 });
    const form = await out.newPage();
    await form.goto(`${inst.url}/admin/login?local=1`);
    await form.fill('#email', 'ece@example.com');
    await form.fill('#password', 'still-wrong');
    await form.click('button[type="submit"]');
    await form.getByRole('alert').waitFor({ timeout: 15_000 });
    await mustSay(form.getByRole('alert'), 'the sign-in form after a wrong password', ['Attempts left before a lock: 2']);
    await sleep(400);
    await shot(form, SET, 'login-remaining-1440.png');

    await form.fill('#email', 'deniz@example.com');
    await form.fill('#password', 'not-the-password');
    await form.click('button[type="submit"]');
    await form.getByRole('alert').waitFor({ timeout: 15_000 });
    await mustSay(form.getByRole('alert'), 'the sign-in form on a locked account', ['Too many wrong attempts for this account', 'Try again in']);
    const button = form.locator('button[type="submit"]');
    if (await button.isEnabled()) throw new Error('the sign-in button is still enabled while the lock holds');
    await mustSay(button, 'the locked sign-in button', ['Try again in']);
    await sleep(400);
    await shot(form, SET, 'login-locked-1440.png');
    await out.close();

    // A lock the administrator lifts lets the person in again.
    const unlocked = await admin.post('/api/admin/login-security/unlock', { scope: 'account', subject: 'deniz@example.com' });
    if (unlocked.unlocked !== 1) throw new Error(`unlock said ${JSON.stringify(unlocked)}`);
    log('login-security page (1440 and 958), the form with tries left, the form locked');
    await ctx.close();
  } finally {
    await browser.close();
    await inst.stop();
  }
}

main().catch((err) => {
  console.error('✗', err.stack ?? err.message);
  process.exit(1);
});
