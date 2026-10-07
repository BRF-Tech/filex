// Tenant realms — the sign-in form of a multi-tenant install (0.50.0, #128).
//
//   node e2e/shots/realm.mjs       (from the repo root; `pnpm shots` runs it)
//
// Writes e2e/.artifacts/shots/capture/realm/ (or SHOTS_OUT):
//
//   login-realm-1440.png         the platform's sign-in page: the Realm field,
//                                empty and free (empty = the platform's own
//                                accounts), filled in with a tenant's realm
//   login-realm-locked-1440.png  a tenant's own address: the field arrives
//                                with that tenant's realm, read-only, and
//                                says why
//   login-realm-958.png          the platform's page at the narrowest desktop
//                                width
//
// It also MEASURES, in a real browser and in both languages (jsdom has no
// layout): at 958 and 1440 px, on both pages, the sign-in card does not scroll
// sideways, nothing in it sticks out of the window, and no two of its controls
// overlap. And it signs in for real:
//
//   · on the platform's page with realm `beta` (a tenant with no address of its
//     own): the session is opened right there;
//   · the tenant's-address half of a HANDOFF: a sign-in with realm `acme` made
//     on the platform's address answers with a one-use ticket, and the tenant's
//     own sign-in page, opened with it in the fragment, takes it out of the
//     address bar and signs the person in.
//
// ⚠ The tenant's own address is `files.acme.test`, sent to the instance on
// loopback by the browser's host resolver: filex resolves a tenant by the Host
// the browser asks for, and nothing else here needs DNS.
//
// Environment: FILEX_BIN, SHOTS_OUT, SHOTS_KEEP (see apps.mjs).

import { chromium } from '@playwright/test';
import { bootInstance, client, log, mustSay, newContext, shot, sleep } from './scene.mjs';

const SET = 'realm';
const ADMIN = { email: 'demo@demo.com', password: 'demo-shots' };
const ACME_HOST = 'files.acme.test';
const PERSON = { email: 'deniz@beta.example', password: 'deniz-shots-2026', display_name: 'Deniz Kaya' };
const ACME_PERSON = { email: 'ece@acme.example', password: 'ece-shots-2026', display_name: 'Ece Aydın' };

/** What does not fit on the sign-in card, in a real browser. */
async function cardProblems(page) {
  return page.evaluate(() => {
    const problems = [];
    const root = document.documentElement;
    if (root.scrollWidth > root.clientWidth + 1) problems.push(`the page scrolls sideways (${root.scrollWidth} > ${root.clientWidth})`);
    const view = window.innerWidth;
    const card = document.querySelector('.lg-card');
    if (!card) return ['no sign-in card'];
    for (const el of card.querySelectorAll('h1, p, label, button, input, a')) {
      for (const r of [...el.getClientRects()].filter((x) => x.width > 0 && x.height > 0)) {
        if (r.left < -0.5 || r.right > view + 0.5) {
          problems.push(`${el.tagName.toLowerCase()} "${(el.textContent || el.getAttribute('name') || '').trim().slice(0, 40)}" sticks out: ${Math.round(r.left)}..${Math.round(r.right)} of ${view}`);
        }
      }
    }
    const inCard = (el) => {
      const c = card.getBoundingClientRect();
      const r = el.getBoundingClientRect();
      return r.left >= c.left - 0.5 && r.right <= c.right + 0.5;
    };
    for (const el of card.querySelectorAll('input, button, .lg-field')) {
      if (el.getClientRects().length && !inCard(el)) problems.push(`"${el.getAttribute('name') || el.textContent?.trim().slice(0, 24)}" leaves the card`);
    }
    const boxes = [...card.querySelectorAll('.lg-field, button, .lg-hint, .lg-label')]
      .map((e) => ({ e, r: e.getBoundingClientRect() }))
      .filter(({ r }) => r.width > 0 && r.height > 0);
    for (let i = 0; i < boxes.length; i++) {
      for (let j = i + 1; j < boxes.length; j++) {
        const a = boxes[i].r;
        const b = boxes[j].r;
        const w = Math.min(a.right, b.right) - Math.max(a.left, b.left);
        const h = Math.min(a.bottom, b.bottom) - Math.max(a.top, b.top);
        if (w > 2 && h > 2 && !boxes[i].e.contains(boxes[j].e) && !boxes[j].e.contains(boxes[i].e)) {
          problems.push(`two parts overlap: "${(boxes[i].e.textContent || '').trim().slice(0, 24)}" and "${(boxes[j].e.textContent || '').trim().slice(0, 24)}"`);
        }
      }
    }
    return problems;
  });
}

async function openLogin(page, base, width) {
  await page.setViewportSize({ width, height: 900 });
  await page.goto(`${base}/admin/login?local=1`);
  await page.locator('#realm').waitFor({ timeout: 20_000 });
  await page.mouse.move(4, 4);
  await sleep(300);
}

async function main() {
  const inst = await bootInstance({ name: SET, admin: ADMIN, env: { FILEX_UPDATE_CHECK: '0', FILEX_MULTI_TENANT: '1' } });
  const port = new URL(inst.url).port;
  const acmeBase = `http://${ACME_HOST}:${port}`;
  const browser = await chromium.launch({ args: [`--host-resolver-rules=MAP ${ACME_HOST} 127.0.0.1`] });
  try {
    const admin = client(inst.url);
    await admin.login(ADMIN.email, ADMIN.password);
    await admin.patch('/api/auth/profile', { locale: 'en', display_name: 'Dana Reyes' });

    // acme has an address of its own; beta has none. Their realms are their
    // slugs (the default), given once and never changed.
    const acme = await admin.post('/api/admin/providers', { slug: 'acme', name: 'Acme Studio', host: ACME_HOST, auth_type: 'local' });
    const beta = await admin.post('/api/admin/providers', { slug: 'beta', name: 'Beta Works', auth_type: 'local' });
    if (acme.realm !== 'acme' || beta.realm !== 'beta') throw new Error(`unexpected realms: ${acme.realm}, ${beta.realm}`);
    const renamed = await admin.call(`/api/admin/providers/${acme.id}`, { method: 'PATCH', body: JSON.stringify({ realm: 'acme2' }) });
    if (renamed.status !== 400) throw new Error(`a realm change was not refused: ${renamed.status}`);
    for (const [p, tenant] of [
      [PERSON, beta],
      [ACME_PERSON, acme],
    ]) {
      await admin.post('/api/admin/users', { ...p, role: 'user', locale: 'en', provider_id: tenant.id });
    }

    // The server tells the form what to show.
    const capsPlatform = await (await fetch(`${inst.url}/api/capabilities`)).json();
    if (!capsPlatform.realm?.enabled || capsPlatform.realm.locked_realm !== null) {
      throw new Error(`platform capabilities: ${JSON.stringify(capsPlatform.realm)}`);
    }

    // ── measure: both pages, both widths, both languages ────────────────────
    const problems = [];
    for (const locale of ['en', 'tr']) {
      const ctx = await newContext(browser, { height: 900 });
      await ctx.addInitScript((l) => localStorage.setItem('filex.locale', l), locale);
      const page = await ctx.newPage();
      for (const [where, base] of [
        ['platform', inst.url],
        ['acme', acmeBase],
      ]) {
        for (const width of [958, 1440]) {
          await openLogin(page, base, width);
          const field = page.locator('#realm');
          const locked = (await field.getAttribute('readonly')) !== null;
          if (where === 'acme' && (!locked || (await field.inputValue()) !== 'acme')) throw new Error(`${locale} ${where}: the realm field is not acme's, read-only`);
          if (where === 'platform' && (locked || (await field.inputValue()) !== '')) throw new Error(`${locale} ${where}: the realm field is not empty and free`);
          if (where === 'platform') await field.fill('beta');
          for (const p of await cardProblems(page)) problems.push(`${locale} ${where} ${width}px: ${p}`);
          if (locale === 'tr') {
            await mustSay(page.locator('.lg-card'), `the Turkish ${where} page`, where === 'acme' ? ['Realm', 'Bu adres acme realm'] : ['Realm', 'Kurumunuzun oturum açma adı']);
          }
        }
      }
      await ctx.close();
    }
    if (problems.length) throw new Error(`layout problems:\n  ${problems.join('\n  ')}`);
    log('the sign-in card fits at 958 and 1440 px, on both pages, in English and Turkish');

    // ── a real sign-in with a realm, on the platform's page ─────────────────
    {
      const ctx = await newContext(browser, { height: 900 });
      const page = await ctx.newPage();
      await openLogin(page, inst.url, 1440);
      await page.fill('#realm', 'beta');
      await page.fill('#email', PERSON.email);
      await page.fill('#password', PERSON.password);
      await page.click('button[type="submit"]');
      await page.waitForURL(/\/drive\//, { timeout: 20_000 });
      log('realm beta on the platform page: signed in there (beta has no address of its own)');
      await ctx.close();
    }

    // ── the tenant's-address half of a handoff ──────────────────────────────
    {
      const res = await fetch(`${inst.url}/api/auth/login`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ email: ACME_PERSON.email, password: ACME_PERSON.password, realm: 'acme' }),
      });
      const body = await res.json();
      if (!body.handoff?.code || body.token) throw new Error(`no handoff: ${JSON.stringify(body)}`);
      if (res.headers.get('set-cookie')) throw new Error('a session cookie was set on the platform address');
      const ctx = await newContext(browser, { height: 900 });
      const page = await ctx.newPage();
      await page.goto(`${acmeBase}/admin/login#handoff=${encodeURIComponent(body.handoff.code)}`);
      await page.waitForURL(/\/drive\//, { timeout: 20_000 });
      if (page.url().includes('handoff')) throw new Error(`the ticket is still in the address bar: ${page.url()}`);
      const cookies = await ctx.cookies(acmeBase);
      if (!cookies.some((c) => c.name === 'filex_session')) throw new Error('no session cookie on the tenant address');
      // Spent: the same ticket again is refused.
      const again = await page.evaluate(async (code) => (await fetch('/api/auth/handoff', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ code }) })).status, body.handoff.code);
      if (again !== 401) throw new Error(`a spent ticket answered ${again}`);
      log('handoff: the tenant page took the ticket out of the address bar, signed in, and the ticket was spent');
      await ctx.close();
    }

    // ── pictures, English ───────────────────────────────────────────────────
    const ctx = await newContext(browser, { height: 900 });
    const page = await ctx.newPage();
    for (const width of [1440, 958]) {
      await openLogin(page, inst.url, width);
      await page.fill('#realm', 'acme');
      await page.fill('#email', 'ece@acme.example');
      await mustSay(page.locator('.lg-card'), 'the platform sign-in page', ['Realm', 'Leave it empty to sign in with this server', 'Email or username', 'Password']);
      // The typed field keeps the keyboard's focus ring otherwise.
      await page.evaluate(() => document.activeElement?.blur());
      await page.mouse.move(4, 4);
      await shot(page, SET, `login-realm-${width}.png`);
    }
    await openLogin(page, acmeBase, 1440);
    await mustSay(page.locator('.lg-card'), 'the tenant sign-in page', ['Realm', 'This address belongs to the acme realm.']);
    await shot(page, SET, 'login-realm-locked-1440.png');
    await ctx.close();
    log('the platform page (1440 and 958) and a tenant page (1440)');
  } finally {
    await browser.close();
    await inst.stop();
  }
}

main().catch((err) => {
  console.error('✗', err.stack ?? err.message);
  process.exit(1);
});
