// The admin panel's mega menu (0.51, GitHub #82, docs/ADMIN-PANEL.md):
//
//   node e2e/shots/megamenu.mjs     (from the repo root; `pnpm shots` runs it)
//
// Writes e2e/.artifacts/shots/capture/megamenu/ (or SHOTS_OUT):
//
//   people-panel-1440.png     the top bar with People & security open over
//                             Admin → Users: two sections, a short line under
//                             every page, the page you are on marked
//   system-dark-tr-1440.png   the System panel in Turkish, in the dark theme -
//                             the same menu, translated, three sections
//   drawer-390.png            a phone: the Menu button's drawer, every page as
//                             a list under its panel and section headings
//   search-ldap-1440.png      the panel's search (task #168) for "ldap": the
//                             Identity providers page and its LDAP tab, found
//                             by a synonym
//   search-tr-390.png         a phone, in Turkish: the search over the window
//                             for "kullanici" (Kullanıcılar, folded)
//
// It also MEASURES, in a real browser (jsdom has no layout): each panel opens
// inside the window at 1024 and 1440 px in English and Turkish, nothing on the
// page scrolls sideways, the drawer fits a 390 px phone, the search's panel
// opens inside the window at 1024 and 1440 px in both languages, and the
// phone's search covers the window. A failed
// measurement throws; the pictures are only written by a run that passed it.
//
// ⚠ The panels open on a CLICK, never on hover (docs/ADMIN-PANEL.md), and
// App.vue cross-fades two AdminLayouts for 120 ms after every navigation: the
// scene waits for one mega menu before it touches the bar (lesson #994).
//
// Environment: FILEX_BIN, SHOTS_OUT, SHOTS_KEEP (see apps.mjs).

import { chromium } from '@playwright/test';
import { bootInstance, client, layoutProblems, log, mustSay, newContext, setLanguage, shot, signIn, sleep } from './scene.mjs';

const SET = 'megamenu';
const ADMIN = { email: 'demo@demo.com', password: 'demo-shots' };
const PEOPLE = [
  { email: 'deniz@example.com', display_name: 'Deniz Kaya' },
  { email: 'ece@example.com', display_name: 'Ece Aydın' },
  { email: 'sam@example.com', display_name: 'Sam Carter' },
  { email: 'lena@example.com', display_name: 'Lena Fischer' },
  { email: 'omar@example.com', display_name: 'Omar Haddad' },
];

/** One mega menu on the page (the cross-fade is over), and its panels closed. */
async function settled(page) {
  const menu = page.getByTestId('mega-menu');
  for (let i = 0; i < 50 && (await menu.count()) !== 1; i++) await sleep(100);
  if ((await menu.count()) !== 1) throw new Error(`expected one mega menu, found ${await menu.count()}`);
  await sleep(300);
}

/** Opens the panel `id` (files | people | system) and returns it, measured. */
async function openPanel(page, id, what) {
  await settled(page);
  await page.getByTestId(`nav-top-${id}`).click();
  const panel = page.getByTestId(`nav-panel-${id}`);
  await panel.waitFor({ state: 'visible', timeout: 10_000 });
  await sleep(350);
  const box = await panel.boundingBox();
  const view = page.viewportSize();
  if (!box || !view) throw new Error(`${what}: the panel has no box`);
  if (box.x < 0 || box.x + box.width > view.width + 0.5 || box.y + box.height > view.height + 0.5) {
    throw new Error(`${what}: the panel does not fit the window (${Math.round(box.x)}..${Math.round(box.x + box.width)} x ${Math.round(box.y + box.height)} in ${view.width}x${view.height})`);
  }
  return panel;
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
    await admin.post('/api/notifications/read-all', {});

    // ── measure: every panel, two widths, both languages ─────────────────
    const ctx = await newContext(browser, { width: 1440, height: 900 });
    const page = await ctx.newPage();
    await signIn(page, inst.url, ADMIN);
    const problems = [];
    for (const locale of ['en', 'tr']) {
      await setLanguage(admin, page, locale);
      for (const width of [1024, 1440]) {
        await page.setViewportSize({ width, height: 900 });
        await page.goto(`${inst.url}/admin/users`);
        await page.getByTestId('mega-menu').first().waitFor({ timeout: 20_000 });
        for (const id of ['files', 'people', 'system']) {
          try {
            await openPanel(page, id, `${locale} ${width}px ${id}`);
          } catch (err) {
            problems.push(err.message);
          }
          for (const x of await layoutProblems(page)) problems.push(`${locale} ${width}px, ${id} open: ${x}`);
          await page.keyboard.press('Escape');
          await sleep(200);
        }
      }
    }
    if (problems.length) throw new Error(`layout problems:\n  ${problems.join('\n  ')}`);
    log('every panel fits at 1024 and 1440 px, in English and Turkish');

    // ── 1. People & security over Admin → Users, English, light ──────────
    await setLanguage(admin, page, 'en');
    await page.setViewportSize({ width: 1440, height: 760 });
    await page.goto(`${inst.url}/admin/users`);
    await page.getByTestId('mega-menu').first().waitFor({ timeout: 20_000 });
    await page.getByText('Deniz Kaya').first().waitFor({ timeout: 20_000 });
    const people = await openPanel(page, 'people', 'People & security');
    await mustSay(people, 'the People & security panel', [
      'People & access', 'Security', 'Users', 'Groups', 'Roles', 'Folder access',
      'Identity providers', 'Sign-in security', 'Encryption', 'Protection', 'API / MCP',
    ]);
    await page.mouse.move(1300, 700);
    await sleep(300);
    await shot(page, SET, 'people-panel-1440.png');
    await page.keyboard.press('Escape');
    await ctx.close();

    // ── 2. System in Turkish, dark ────────────────────────────────────────
    const dark = await newContext(browser, { scheme: 'dark', width: 1440, height: 900 });
    const dpage = await dark.newPage();
    await signIn(dpage, inst.url, ADMIN);
    await setLanguage(admin, dpage, 'tr');
    await dpage.goto(`${inst.url}/admin/dashboard`);
    await dpage.getByTestId('mega-menu').first().waitFor({ timeout: 20_000 });
    const system = await openPanel(dpage, 'system', 'Sistem');
    await mustSay(system, 'the Turkish System panel', ['Eklentiler ve entegrasyonlar', 'Özelleştirme', 'Bakım ve kayıtlar', 'Eklentiler', 'Ayarlar', 'Denetim kaydı']);
    await dpage.mouse.move(1300, 700);
    await sleep(300);
    await shot(dpage, SET, 'system-dark-tr-1440.png');
    await setLanguage(admin, dpage, 'en');
    await dark.close();

    // ── 3. A phone: the drawer ────────────────────────────────────────────
    const phone = await newContext(browser, { width: 390, height: 844 });
    const ppage = await phone.newPage();
    await signIn(ppage, inst.url, ADMIN);
    await ppage.goto(`${inst.url}/admin/users`);
    const toggle = ppage.getByTestId('nav-drawer-toggle');
    await toggle.first().waitFor({ timeout: 20_000 });
    for (let i = 0; i < 50 && (await toggle.count()) !== 1; i++) await sleep(100);
    if ((await toggle.count()) !== 1) throw new Error(`expected one Menu button, found ${await toggle.count()}`);
    await sleep(400);
    await toggle.click();
    const drawer = ppage.getByTestId('nav-drawer');
    await drawer.waitFor({ state: 'visible', timeout: 10_000 });
    await sleep(500);
    await mustSay(drawer, 'the drawer', ['Files & storage', 'People & security', 'Users', 'Groups']);
    const box = await drawer.boundingBox();
    if (!box || box.x < -0.5 || box.x + box.width > 390.5) throw new Error(`the drawer does not fit a 390 px phone: ${JSON.stringify(box)}`);
    const sideways = await layoutProblems(ppage);
    if (sideways.some((p) => p.startsWith('the page scrolls sideways'))) throw new Error(`the phone page scrolls sideways: ${sideways.join('; ')}`);
    await shot(ppage, SET, 'drawer-390.png');
    await phone.close();

    // ── 4. The panel's search (task #168): it fits, then "ldap" at 1440 ──
    const sctx = await newContext(browser, { width: 1440, height: 900 });
    const spage = await sctx.newPage();
    await signIn(spage, inst.url, ADMIN);
    const searchProblems = [];
    for (const locale of ['en', 'tr']) {
      await setLanguage(admin, spage, locale);
      for (const width of [1024, 1440]) {
        await spage.setViewportSize({ width, height: 900 });
        await spage.goto(`${inst.url}/admin/dashboard`);
        await spage.getByTestId('mega-menu').first().waitFor({ timeout: 20_000 });
        await settled(spage);
        await spage.getByTestId('panel-search-open').click();
        const layer = spage.getByTestId('panel-search');
        await layer.waitFor({ state: 'visible', timeout: 10_000 });
        await spage.getByTestId('panel-search-input').fill('se');
        await sleep(700);
        const sbox = await layer.boundingBox();
        if (!sbox || sbox.x < 0 || sbox.x + sbox.width > width + 0.5 || sbox.y + sbox.height > 900.5) {
          searchProblems.push(`${locale} ${width}px: the search panel does not fit the window (${JSON.stringify(sbox)})`);
        }
        for (const x of await layoutProblems(spage)) searchProblems.push(`${locale} ${width}px, search open: ${x}`);
        await spage.keyboard.press('Escape');
        await sleep(200);
      }
    }
    if (searchProblems.length) throw new Error(`search layout problems:\n  ${searchProblems.join('\n  ')}`);
    log('the search panel fits at 1024 and 1440 px, in English and Turkish');

    await setLanguage(admin, spage, 'en');
    await spage.setViewportSize({ width: 1440, height: 760 });
    await spage.goto(`${inst.url}/admin/users`);
    await spage.getByTestId('mega-menu').first().waitFor({ timeout: 20_000 });
    await settled(spage);
    await spage.getByTestId('panel-search-open').click();
    await spage.getByTestId('panel-search-input').fill('ldap');
    const found = spage.getByTestId('panel-search');
    await found.waitFor({ state: 'visible', timeout: 10_000 });
    await sleep(700);
    await mustSay(found, 'the search for "ldap"', ['Pages', 'Identity providers', 'LDAP / Active Directory']);
    await shot(spage, SET, 'search-ldap-1440.png');
    await sctx.close();

    // ── 5. A phone, in Turkish: the search over the whole window ─────────
    const sphone = await newContext(browser, { width: 390, height: 844 });
    const sppage = await sphone.newPage();
    await signIn(sppage, inst.url, ADMIN);
    await setLanguage(admin, sppage, 'tr');
    await sppage.goto(`${inst.url}/admin/dashboard`);
    const searchButton = sppage.getByTestId('panel-search-open');
    await searchButton.first().waitFor({ timeout: 20_000 });
    for (let i = 0; i < 50 && (await searchButton.count()) !== 1; i++) await sleep(100);
    if ((await searchButton.count()) !== 1) throw new Error(`expected one search button, found ${await searchButton.count()}`);
    await sleep(400);
    await searchButton.click();
    await sppage.getByTestId('panel-search-input').fill('kullanici');
    const over = sppage.getByTestId('panel-search');
    await over.waitFor({ state: 'visible', timeout: 10_000 });
    await sleep(700);
    await mustSay(over, 'the Turkish phone search', ['Sayfalar', 'Kullanıcılar']);
    const obox = await over.boundingBox();
    if (!obox || obox.x < -0.5 || obox.width < 389) throw new Error(`the phone's search does not cover the window: ${JSON.stringify(obox)}`);
    const phoneSideways = await layoutProblems(sppage);
    if (phoneSideways.some((p) => p.startsWith('the page scrolls sideways'))) throw new Error(`the phone page scrolls sideways: ${phoneSideways.join('; ')}`);
    await shot(sppage, SET, 'search-tr-390.png');
    await setLanguage(admin, sppage, 'en');
    await sphone.close();
    log('People & security over Users, System in Turkish dark, the phone drawer, the search at 1440 and on a phone');
  } finally {
    await browser.close();
    await inst.stop();
  }
}

main().catch((err) => {
  console.error('✗', err.stack ?? err.message);
  process.exit(1);
});
