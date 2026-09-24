// Issue #36 end to end: a sign-in that fails must leave the person on the
// waiting screen of their attempt — the address to copy, the code box — never
// on the server-address form with the attempt stranded behind it.
//
// The REAL desktop app (a hermetic profile, FILEX_NO_BROWSER=1: nothing is
// registered with the OS and no browser opens) against a live filex server.
// The browser's half is played over HTTP, exactly as ui-login-e2e.mjs does, and
// the failing links are fed through the app's own deep-link entry point.
//
// Run:  FILEX_SERVER=http://127.0.0.1:5641 FILEX_EMAIL=admin@local \
//       FILEX_PASSWORD=admin node scripts/signin-retry-e2e.mjs
//
// On v0.42.2 the first check already fails: the window comes back on #server.

import { SERVER, EMAIL, PASSWORD, check, finish, launchApp, sleep } from './lib/harness.mjs';

/** Plays the browser's half: sign in, then complete the desktop hand-off. */
async function browserHalf(authUrl) {
  const u = new URL(authUrl);
  const state = u.searchParams.get('desktop_state');
  const challenge = u.searchParams.get('desktop_challenge');
  if (!state || !challenge) throw new Error('auth URL carried no desktop parameters');
  const login = await fetch(`${SERVER}/api/auth/login`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ email: EMAIL, password: PASSWORD, remember: true }),
  });
  if (!login.ok) throw new Error(`browser login failed (${login.status})`);
  const cookie = (login.headers.getSetCookie?.() ?? []).map((c) => c.split(';')[0]).join('; ');
  const done = await fetch(`${SERVER}/api/auth/desktop/complete`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', Cookie: cookie },
    body: JSON.stringify({ state, challenge, label: 'filex desktop — signin-retry e2e' }),
  });
  if (!done.ok) throw new Error(`complete failed (${done.status}): ${(await done.text()).slice(0, 160)}`);
  const { code } = await done.json();
  return { state, code };
}

/** Feeds a sign-in link in the way the OS would. The window it concerns may
 *  reload while the call is in flight, which ends the evaluate — expected. */
async function deepLink(win, url) {
  await win.evaluate((u) => window.filexShell.__testDeepLink(u), url).catch(() => {});
  await sleep(600);
  await win.waitForLoadState('domcontentloaded');
}

/** What the sign-in window is showing right now. */
async function screen(win) {
  await win.waitForSelector('#server, #authurl', { timeout: 15_000 });
  return {
    serverForm: await win.locator('#server').isVisible().catch(() => false),
    codeBox: await win.locator('#code').isVisible().catch(() => false),
    authUrl: (await win.locator('#authurl').inputValue().catch(() => '')) || '',
    err: ((await win.locator('#err').textContent().catch(() => '')) || '').trim(),
    retry: await win.locator('#retry').isVisible().catch(() => false),
  };
}

const { app } = await launchApp();
try {
  const win = await app.firstWindow();
  await win.waitForLoadState('domcontentloaded');
  await win.locator('#server').fill(SERVER);
  await win.locator('#go').click();
  await win.locator('#authurl').waitFor({ timeout: 15_000 });
  const url1 = await win.locator('#authurl').inputValue();

  // ── 1. a link from an EARLIER attempt (stale state) ─────────────────────
  await deepLink(win, 'filex://auth?state=an-earlier-attempt&code=123456');
  let s = await screen(win);
  check('a stale sign-in link keeps the waiting screen (not the server form)', s.codeBox && !s.serverForm,
    JSON.stringify(s));
  check('… of the SAME attempt', s.authUrl === url1);
  check('… and says the link was from an earlier attempt', /earlier attempt/i.test(s.err), s.err);

  // ── 2. a reload — what the tray icon, the Dock or a second launch do ────
  await win.reload();
  s = await screen(win);
  check('a reload of the window keeps the waiting screen', s.codeBox && !s.serverForm && s.authUrl === url1,
    JSON.stringify(s));

  // ── 3. a code the server REFUSES (right attempt, wrong code) ────────────
  const first = await browserHalf(url1);
  await deepLink(win, `filex://auth?state=${encodeURIComponent(first.state)}&code=not-the-code`);
  s = await screen(win);
  check('a refused code keeps the waiting screen', s.codeBox && !s.serverForm, JSON.stringify(s));
  check('… says the code cannot be used again', /cannot be used again/i.test(s.err), s.err);
  check('… and offers to start again in the browser', s.retry);

  // ── 4. start again: a new attempt, same server, no retyping ─────────────
  await win.locator('#retry').click();
  await win.waitForFunction((old) => document.querySelector('#authurl')?.value !== old, url1, { timeout: 15_000 });
  s = await screen(win);
  const url2 = s.authUrl;
  check('"Start again in the browser" begins a NEW attempt on the same server',
    url2 !== url1 && url2.startsWith(SERVER), url2.slice(0, 80));
  check('… and clears the old failure', s.err === '', s.err);

  // ── 5. Cancel is the one way back to the server form, and it sticks ─────
  await win.locator('#cancel').click();
  await win.locator('#server').waitFor({ timeout: 10_000 });
  await win.reload();
  s = await screen(win);
  check('Cancel returns to the server form, and a reload keeps it there', s.serverForm && !s.codeBox,
    JSON.stringify(s));

  // ── 6. and the person can still finish ─────────────────────────────────
  await win.locator('#server').fill(SERVER);
  await win.locator('#go').click();
  await win.locator('#authurl').waitFor({ timeout: 15_000 });
  const url3 = await win.locator('#authurl').inputValue();
  const last = await browserHalf(url3);
  await win.locator('#code').fill(last.code);
  await win.locator('#usecode').click().catch(() => {});
  const appWindow = await app.waitForEvent('window', { timeout: 60_000 });
  await appWindow.waitForURL(/^app:\/\/filex/, { timeout: 30_000 }).catch(() => {});
  await appWindow.waitForLoadState('domcontentloaded');
  const st = await appWindow.evaluate(() => window.filexApp.getState());
  check('signed in after the failures', st.accounts.length === 1,
    st.accounts[0] ? `${st.accounts[0].email} @ ${st.accounts[0].serverUrl}` : 'none');
} catch (e) {
  check('flow completed', false, String(e && e.message).split('\n')[0]);
} finally {
  await app.close().catch(() => {});
}

finish();
