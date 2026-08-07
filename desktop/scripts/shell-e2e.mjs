// Drives the REAL app window and checks it is a FILE MANAGER, not a console.
//
// This suite exists because the first build shipped the admin SPA inside the
// window: signing in landed the user on a dashboard with users, storages and
// server settings — a server console wearing a desktop app's icon. The checks
// below are the specific things that were wrong, so they cannot come back.
//
// ⚠ No OS-level input. Playwright talks to the renderer.
//
// Run: node scripts/shell-e2e.mjs
// Env: FILEX_SERVER, FILEX_EMAIL, FILEX_PASSWORD

import fs, { readdirSync } from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const DESKTOP = path.resolve(__dirname, '..');
const REPO = path.resolve(DESKTOP, '..');

const PNPM = path.join(REPO, 'node_modules/.pnpm');
const pwDir = readdirSync(PNPM).find((d) => d.startsWith('playwright-core@'));
const { _electron } = await import(
  pathToFileURL(path.join(PNPM, pwDir, 'node_modules/playwright-core/index.mjs')).href
);

const SERVER = process.env.FILEX_SERVER ?? 'https://fm.brf.sh';
const EMAIL = process.env.FILEX_EMAIL ?? '';
const PASSWORD = process.env.FILEX_PASSWORD ?? '';

let failures = 0;
const check = (name, ok, detail = '') => {
  if (!ok) failures++;
  console.log(`${ok ? 'PASS' : 'FAIL'}  ${name}${detail ? `  — ${detail}` : ''}`);
};

async function browserHalf(authUrl) {
  const u = new URL(authUrl);
  const state = u.searchParams.get('desktop_state');
  const challenge = u.searchParams.get('desktop_challenge');
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
    body: JSON.stringify({ state, challenge, label: 'filex desktop — shell e2e' }),
  });
  if (!done.ok) throw new Error(`complete failed (${done.status})`);
  return (await done.json()).code;
}

const profile = fs.mkdtempSync(path.join(os.tmpdir(), 'filex-shell-e2e-'));
const fakeHome = fs.mkdtempSync(path.join(os.tmpdir(), 'filex-home-'));
const cli = path.join(DESKTOP, 'build', 'bin', process.platform === 'win32' ? 'filex.exe' : 'filex');

// FILEX_APP_BINARY points at a PACKAGED build (release2/win-unpacked/filex.exe,
// or the AppImage's unpacked binary). Worth running: `electron .` loads files
// straight off disk, so it cannot catch anything that goes wrong at packaging
// time — a file missing from the asar, a resource path that only resolves in
// the source tree. The installed app is what the user actually runs.
//
// ⚠ The hermetic --user-data-dir also keeps the single-instance lock separate,
// so this never hijacks or kills an app the operator has open.
const packaged = process.env.FILEX_APP_BINARY;
const app = await _electron.launch({
  ...(packaged
    ? { executablePath: packaged, args: [`--user-data-dir=${profile}`] }
    : { args: [DESKTOP, `--user-data-dir=${profile}`], cwd: DESKTOP }),
  env: {
    ...process.env,
    FILEX_NO_BROWSER: '1',
    HOME: fakeHome,
    USERPROFILE: fakeHome,
    ...(fs.existsSync(cli) ? { FILEX_CLI: cli } : {}),
  },
});
if (packaged) console.log(`(driving the PACKAGED app: ${packaged})`);

try {
  // ── sign in ───────────────────────────────────────────────────────
  const connect = await app.firstWindow();
  await connect.waitForLoadState('domcontentloaded');
  check('starts on the connect screen', await connect.locator('#server').isVisible());
  check('the connect screen offers nothing but connecting',
    (await connect.locator('nav').count()) === 0,
    'accounts + sync + settings belong in the app window');

  await connect.locator('#server').fill(SERVER);
  await connect.locator('#go').click();
  await connect.locator('#authurl').waitFor({ timeout: 15_000 });
  const code = await browserHalf(await connect.locator('#authurl').inputValue());
  await connect.locator('#code').fill(code);
  await connect.locator('#usecode').click().catch(() => {});

  // ── the app window ────────────────────────────────────────────────
  const win = await app.waitForEvent('window', { timeout: 60_000 });
  await win.waitForURL(/^app:\/\/filex/, { timeout: 30_000 }).catch(() => {});
  await win.waitForLoadState('domcontentloaded');
  const origin = await win.evaluate(() => location.origin);
  check('the app window loaded', origin === 'app://filex', `${origin} url=${win.url()}`);
  if (origin !== 'app://filex') {
    throw new Error(`the app never loaded (${win.url()})`);
  }

  await win.waitForTimeout(4000);

  // ⭐ The point of this whole rewrite: what is on screen when you sign in.
  const explorerCount = await win.locator('filex-explorer').count();
  check('the window shows the FILE EXPLORER', explorerCount === 1, `${explorerCount} found`);

  const bodyText = await win.evaluate(() => document.body.innerText);
  const consoleWords = ['Dashboard', 'Storages', 'Users', 'Audit log', 'Webhooks', 'Kontrol'];
  const found = consoleWords.filter((w) => bodyText.includes(w));
  check('no admin console in the window', found.length === 0, found.join(', ') || 'clean');

  // ⚠ "the component rendered something" is too weak a claim — an empty folder
  // and a dead component look the same from the outside. The server is seeded
  // with a file, so the test asserts THAT FILE IS ON SCREEN: the token function,
  // the cross-origin request and the listing all have to work for it to appear.
  const seeded = process.env.FILEX_SEEDED_FILE ?? 'rapor-2026.txt';
  let shows = false;
  for (let i = 0; i < 20 && !shows; i++) {
    shows = (await win.evaluate(() => document.body.innerText)).includes(seeded);
    if (!shows) await win.waitForTimeout(500);
  }
  check(`the explorer lists a real file from the server (${seeded})`, shows);

  // ── the account rail ──────────────────────────────────────────────
  const rail = await win.evaluate(() => {
    const r = document.querySelector('#rail');
    return {
      exists: !!r,
      avatars: r ? r.querySelectorAll('.avatar').length : 0,
      hasAdd: !!r?.querySelector('.rail-btn'),
    };
  });
  check('there is an account rail down the left', rail.exists && rail.avatars === 1,
    `${rail.avatars} account(s)`);
  check('the rail offers adding another account', rail.hasAdd);

  // ── settings: the APP's, not the server's ─────────────────────────
  await win.evaluate(() => document.querySelectorAll('#rail .rail-btn')[1].click());
  await win.waitForTimeout(500);
  const settings = await win.evaluate(() => {
    const s = document.querySelector('#settings');
    return { open: s?.classList.contains('open'), text: s?.innerText ?? '' };
  });
  check('settings opens inside the app', settings.open === true);
  check('settings manages ACCOUNTS', /Sign out/.test(settings.text));
  check('settings manages SYNCED FOLDERS', /Synced folders/i.test(settings.text));
  check('settings offers background running', /background/i.test(settings.text));
  check('settings offers start-at-login', /Start when I sign in/i.test(settings.text));
  check('the admin panel is a LINK OUT, not a screen in here',
    /Admin panel/.test(settings.text));
  // ⚠ Ask what is ACTUALLY on top, not whether the element I chose to hide is
  // hidden. The earlier version of this check asserted `#explorer-host` was
  // display:none — it was, and it passed, while the explorer's onboarding tour
  // (a child of <body>, not of the host) carried on sitting in the middle of
  // Settings. The screenshot showed it; the test did not. elementFromPoint is
  // the browser's own answer to "what would the user click here".
  const onTop = await win.evaluate(() => {
    const el = document.elementFromPoint(innerWidth / 2, innerHeight / 2);
    return { inSettings: !!el?.closest('#settings'), got: el?.className?.toString?.().slice(0, 40) ?? el?.tagName };
  });
  check('nothing overlays the settings panel', onTop.inSettings, `topmost = ${onTop.got}`);
  const tourVisible = await win.evaluate(() => {
    const t = document.querySelector('.fe-tour');
    return !!t && getComputedStyle(t).display !== 'none';
  });
  check("the explorer's onboarding tour is not on top of settings", !tourVisible);

  await win.screenshot({ path: path.join(REPO, 'desktop-shell-settings.png') });

  // ── the remote folder picker ──────────────────────────────────────
  await win.evaluate(() => document.querySelector('#add-sync')?.click());
  await win.waitForTimeout(2500);
  const picker = await win.evaluate(() => {
    const p = document.querySelector('#picker');
    return {
      open: p?.classList.contains('open'),
      items: [...p.querySelectorAll('#pk-list li')].map((li) => li.textContent),
    };
  });
  check('choosing a folder browses the REAL server', picker.open === true && picker.items.length > 0,
    picker.items.join(' | ') || 'nothing listed');
  check('the storage is listed by name', picker.items.some((t) => /docs/.test(t)),
    picker.items.join(' | '));

  await win.screenshot({ path: path.join(REPO, 'desktop-shell-picker.png') });

  // Close it again and go back to the explorer.
  await win.evaluate(() => document.querySelector('#pk-close').click());
  await win.evaluate(() => document.querySelector('#close-settings')?.click());
  await win.waitForTimeout(400);
  const backToFiles = await win.evaluate(() =>
    !document.querySelector('#settings').classList.contains('open'));
  check('closing settings returns to the files', backToFiles);

  // ⚠ The dialog a user actually opens, and whether it LOOKS like a dialog.
  // It rendered as raw unstyled HTML in every embedded surface — no box, no
  // backdrop, browser-default inputs flowing down the page — because Vue's
  // scoped-style hash does not survive the web-component build. Nothing threw;
  // the functional suites were all green while it looked broken.
  await win.evaluate((name) => {
    const row = [...document.querySelectorAll('*')].find((e) => e.textContent?.trim() === name);
    row?.dispatchEvent(new MouseEvent('click', { bubbles: true }));
  }, seeded);
  await win.waitForTimeout(500);
  await win.evaluate(() => {
    const b = [...document.querySelectorAll('button')].find((x) =>
      /Payla[sş] \/ [İI]zinler|Share \/ Permissions/i.test(x.textContent ?? ''));
    b?.click();
  });
  await win.waitForTimeout(1500);

  const dialog = await win.evaluate(() => {
    const m = document.querySelector('.fx-perm-modal');
    if (!m) return { present: false };
    const cs = getComputedStyle(m);
    return {
      present: true,
      background: cs.backgroundColor,
      radius: cs.borderRadius,
      // A transparent background with no radius is the unstyled signature.
      styled: cs.backgroundColor !== 'rgba(0, 0, 0, 0)' && cs.borderRadius !== '0px',
    };
  });
  check('the share dialog opens', dialog.present === true);
  check('the share dialog is actually STYLED, not raw HTML', dialog.styled === true,
    `background=${dialog.background} radius=${dialog.radius}`);

  await win.screenshot({ path: path.join(REPO, 'desktop-shell-share.png') });
  await win.evaluate(() => {
    const x = [...document.querySelectorAll('button')].find((b) => /^[✕×]$/.test(b.textContent?.trim() ?? ''));
    x?.click();
  });
  await win.waitForTimeout(400);

  await win.screenshot({ path: path.join(REPO, 'desktop-shell.png') });
} catch (e) {
  check('flow completed', false, String(e && e.message).split('\n')[0]);
} finally {
  await app.close().catch(() => {});
}

console.log(`\n==== ${failures === 0 ? 'ALL PASSED' : failures + ' FAILED'} ====`);
process.exit(failures === 0 ? 0 : 1);
