// End-to-end checks for the app's CHROME — the parts of the window that are
// not the file list: the server's brand mark on the rail, the tab strip, the
// scrollbars, and the day/night/automatic switch.
//
// Every one of these was reported as "it looks wrong", and every one of them is
// invisible to a unit test: they are CSS, a computed style, or a strip that is
// rendered conditionally. So they are measured in the running app.
//
// Usage:
//   FILEX_EMAIL=… FILEX_PASSWORD=… node scripts/chrome-e2e.mjs
//   FILEX_APP_BINARY=…\filex.exe node scripts/chrome-e2e.mjs   (drive the install)

import { execFileSync } from 'node:child_process';
import fs from 'node:fs';
import path from 'node:path';
import { REPO, check, finish, launchApp, signIn, skipTour, sleep } from './lib/harness.mjs';

const { app } = await launchApp();
const { win } = await signIn(app, { label: 'filex desktop — chrome-e2e' });

// ⚠ The tour is a <body>-level dialog that swallows clicks, and it appears
// asynchronously — one dismissal at sign-in races it and it is back by the time
// the toolbar is clicked. Dismiss until it is actually gone.
async function ensureNoTour() {
  for (let i = 0; i < 12; i++) {
    if ((await win.locator('.fe-tour').count()) === 0) return;
    await skipTour(win);
    await sleep(250);
  }
}
await ensureNoTour();

// ── the server's brand mark ──────────────────────────────────────────
// GET /api/branding is public and fm.brf.sh serves a data: URI. The plate is
// painted only once that request lands, so give it a moment.
await win.waitForSelector('#rail .slot.active .avatar--brand img', { timeout: 15_000 }).catch(() => {});
const brand = await win.evaluate(() => {
  const img = document.querySelector('#rail .slot.active .avatar--brand img');
  return {
    // The app's own mark is FIXED and leads the rail; it must not be the thing
    // that changes with the selected account.
    appMarkLeads: document.querySelector('#rail')?.firstElementChild?.classList.contains('appmark'),
    src: img ? (img.getAttribute('src') || '').slice(0, 24) : null,
    // naturalWidth is the honest question: a broken URL still leaves an <img>
    // in the DOM, and the row would be an empty white plate.
    decoded: !!img && img.naturalWidth > 0 && img.naturalHeight > 0,
    // Rows that have no branding must keep their initials rather than going blank.
    fallbacks: [...document.querySelectorAll('#rail .avatar:not(.avatar--brand)')]
      .map((el) => el.textContent.trim()),
  };
});
check("the app's own mark leads the rail, fixed", brand.appMarkLeads === true);
check("the active server's row carries its own logo", brand.src !== null,
  `.avatar--brand img: ${brand.src === null ? 'MISSING' : 'present'}`);
check('the logo actually decoded', brand.decoded, `src=${brand.src ?? '—'}…`);
check('rows without branding keep their initials', brand.fallbacks.every((t) => t.length > 0),
  JSON.stringify(brand.fallbacks));

// ── tab strip ────────────────────────────────────────────────────────
const strip = () => win.locator('.fe-tabs');
const tabs = () => win.locator('.fe-tabs__tab');

await win.waitForSelector('.fe-tabs', { timeout: 15_000 }).catch(() => {});
// The regression this locks: the strip used to render only once a SECOND tab
// existed, so a fresh window had no strip and no + button.
check('tab strip is on screen with a single tab', await strip().isVisible().catch(() => false));
check('…and it holds exactly one tab', (await tabs().count()) === 1);

// The + lives in the strip, so a hidden strip means no way to open a tab.
await win.locator('.fe-tabs__new').click();
await sleep(400);
check('the + in the strip opens a second tab', (await tabs().count()) === 2);

// Closing back down to one must NOT take the strip away again.
await win.locator('.fe-tabs__tab').nth(1).locator('.fe-tabs__close').click();
await sleep(400);
check('closing back to one tab leaves the strip in place',
  (await tabs().count()) === 1 && (await strip().isVisible()));

// ── the strip's own overflow ─────────────────────────────────────────
// ⚠ Three separate complaints from one CSS mistake: a bar appeared beside the
// tabs, a VERTICAL bar appeared with nothing to scroll, and enough tabs ran off
// the edge with no way to scroll to them.
// ⚠ The tour comes back on its own; without this the loop below spends 30s
// being intercepted by it and reports a layout problem that is not there.
await ensureNoTour();
// Open tabs until the strip ACTUALLY overflows rather than a fixed count:
// tabs shrink to a 72px floor first, so how many it takes depends on the
// window — 25 was not enough at 1944px and would have been plenty at 1280.
// Burak's report was "ekranı taştı, akıyor gidiyor"; this reaches that state
// on whatever screen the suite happens to run on.
let overflowed = false;
for (let i = 0; i < 60 && !overflowed; i++) {
  await win.locator('.fe-tabs__new').click();
  await sleep(80);
  overflowed = await win.evaluate(() => {
    const el = document.querySelector('.fe-tabs__scroll');
    return !!el && el.scrollWidth > el.clientWidth + 1;
  });
}
await sleep(500);
const stripBox = await win.evaluate(() => {
  const el = document.querySelector('.fe-tabs__scroll');
  if (!el) return null;
  const cs = getComputedStyle(el);
  const tabs = [...el.querySelectorAll('.fe-tabs__tab')];
  return {
    tabs: tabs.length,
    tabWidth: tabs.length ? Math.round(tabs[0].getBoundingClientRect().width) : 0,
    overflowX: cs.overflowX,
    overflowY: cs.overflowY,
    scrollbarWidth: cs.scrollbarWidth,
    overflowPx: el.scrollWidth - el.clientWidth,
    // Vertical: must be nothing. A bar down the side of a row of tabs is the
    // bug, and `overflow-x: auto` alone computes the other axis to `auto` too.
    scrollableY: el.scrollHeight > el.clientHeight + 1,
    // The + must stay reachable once the tabs run off — it lives OUTSIDE the
    // scroller for exactly that reason.
    plusOutsideScroller: !document.querySelector('.fe-tabs__scroll .fe-tabs__new')
      && !!document.querySelector('.fe-tabs > .fe-tabs__new'),
  };
});
check('enough tabs overflow the strip', overflowed && stripBox?.overflowPx > 0,
  `${stripBox?.tabs ?? 0} sekme · sekme genişliği ${stripBox?.tabWidth}px · taşma ${stripBox?.overflowPx}px`);
check('…and the strip scrolls sideways with a thin bar',
  stripBox?.overflowX === 'auto' && stripBox?.scrollbarWidth === 'thin',
  `overflow-x=${stripBox?.overflowX} scrollbar-width=${stripBox?.scrollbarWidth}`);
check('…and the + stays put instead of scrolling away with them',
  stripBox?.plusOutsideScroller === true);
check('…with no vertical axis at all', stripBox?.overflowY === 'hidden' && stripBox?.scrollableY === false,
  `overflow-y=${stripBox?.overflowY} scrollableY=${stripBox?.scrollableY}`);

// Back to one tab so the rest of the suite starts clean.
for (let i = 0; i < 30; i++) {
  const n = await tabs().count();
  if (n <= 1) break;
  await win.locator('.fe-tabs__tab').nth(n - 1).locator('.fe-tabs__close').click();
  await sleep(120);
}

// ── scrollbars ───────────────────────────────────────────────────────
const bars = await win.evaluate(() => {
  const root = document.querySelector('.fe');
  const shell = document.querySelector('#settings');
  const cs = (el) => (el ? getComputedStyle(el) : null);
  return {
    explorer: cs(root)?.scrollbarColor ?? '',
    shell: cs(shell)?.scrollbarColor ?? '',
    width: cs(root)?.scrollbarWidth ?? '',
  };
});
check('the explorer paints themed scrollbars', bars.explorer !== '' && bars.explorer !== 'auto',
  `scrollbar-color=${bars.explorer || '—'}`);
check('the app chrome paints themed scrollbars', bars.shell !== '' && bars.shell !== 'auto',
  `scrollbar-color=${bars.shell || '—'}`);
check('…and they are the thin variant', bars.width === 'thin', bars.width);

// ⚠ The shipped stylesheet is loaded by EMBEDDERS (work, fishapp, the admin
// SPA). A bare `*` scrollbar rule there would repaint the host page from
// inside an embedded file browser, which no embedder asked for. Cheap to
// assert, impossible to notice by looking at this window.
const css = fs.readFileSync(path.join(REPO, 'packages/webcomponent/dist/style.css'), 'utf8');
// ⚠ Order matters as much as scoping: the generic block and `.fe-tabs__scroll`
// have the SAME specificity, so whichever comes last wins. With the generic
// block at the bottom it overrode the strip's own rules and put a scrollbar
// under the tabs.
const genericAt = css.indexOf('.fe ::-webkit-scrollbar');
const stripAt = css.indexOf('.fe-tabs__scroll');
check('the generic scrollbar block comes BEFORE the component rules',
  genericAt >= 0 && stripAt > genericAt, `generic@${genericAt} strip@${stripAt}`);
check('the shipped CSS scopes its scrollbar rules to .fe',
  css.includes('.fe ::-webkit-scrollbar') && !/(^|[^-\w.])\*::-webkit-scrollbar/.test(css),
  'a bare `*` rule would hijack the embedding page');

// ── day / night / automatic ──────────────────────────────────────────
await ensureNoTour();
await win.locator('.fe-toolbar__theme:not(.fe-toolbar__measure .fe-toolbar__theme)').first().click();
await win.waitForSelector('.fe-thememode', { timeout: 10_000 }).catch(() => {});
const opts = win.locator('.fe-thememode__opt');
check('the theme gallery offers a day/night/automatic switch', (await opts.count()) === 3);
check('exactly one of the three reads as active',
  (await win.locator('.fe-thememode__opt.is-active').count()) === 1);

/** Reads what the explorer actually resolved to, not what we asked for. */
const mode = () =>
  win.evaluate(() => {
    const root = document.querySelector('.fe');
    return {
      light: !!root?.classList.contains('fe--theme-light'),
      dark: !!root?.classList.contains('fe--theme-dark'),
      stored: localStorage.getItem('filex.thememode'),
      // The token that actually paints the surface — the classes are only the
      // mechanism, this is the result.
      bg: getComputedStyle(root).getPropertyValue('--fe-bg').trim(),
    };
  });

await opts.nth(1).click(); // Night
await sleep(300);
let m = await mode();
check('Night paints the dark variant', m.dark && !m.light, JSON.stringify(m));
check('…and the choice is remembered', m.stored === 'dark', String(m.stored));

await opts.nth(0).click(); // Day
await sleep(300);
m = await mode();
check('Day paints the light variant', m.light && !m.dark, JSON.stringify(m));
check('…and the choice is remembered', m.stored === 'light', String(m.stored));

await opts.nth(2).click(); // Automatic
await sleep(300);
m = await mode();
check('Automatic pins neither variant and defers to the system',
  !m.light && !m.dark, JSON.stringify(m));
check('…and is stored as an explicit choice, not as "unset"', m.stored === 'auto', String(m.stored));

// ── start at login ──────────────────────────────────────────────────
// ⚠⚠ The bug that started this round: a DEV run registered `process.execPath`
// — node_modules/.../electron.exe, with no project path — in the user's real
// HKCU\…\Run key. Every sign-in for months afterwards opened Electron's own
// welcome window, and the entry outlived the checkout it pointed at.
//
// This runs UNPACKAGED (that is what the suite drives), so the correct outcome
// is: the app refuses, says why, and touches nothing.
if (process.platform === 'win32') {
  const runKey = () =>
    execFileSync('reg', ['query', 'HKCU\\SOFTWARE\\Microsoft\\Windows\\CurrentVersion\\Run'],
      { encoding: 'utf8' });

  const before = await win.evaluate(() => window.filexApp.getState());
  // The suite drives the source tree by default and the installed package when
  // FILEX_APP_BINARY is set. Both cases matter and they expect OPPOSITE things,
  // so the assertion follows what the app reports rather than what we assume.
  const packaged = before.launchAtLoginSupported === true;
  check(`the run under test is ${packaged ? 'packaged' : 'unpackaged'}`, true,
    packaged ? 'driving an installed app' : 'driving the source tree');

  // Ask for it — through the same IPC the settings switch uses.
  const after = await win.evaluate(() => window.filexApp.setSettings({ launchAtLogin: true }));

  if (packaged) {
    check('the installed app can actually register a login item', after.launchAtLoginEffective === true);
    // The command Windows keeps is the whole point: an installed executable
    // with --hidden, so the launch nobody asked for stays in the tray.
    const line = runKey().split(/\r?\n/).find((l) => /electron\.app\.filex/i.test(l)) ?? '';
    check('…pointing at the installed executable', /Programs[\\/]filex[\\/]filex\.exe/i.test(line), line.trim());
    check('…and carrying --hidden', /--hidden/.test(line), line.trim());
    check('…and never at a bare electron.exe', !/node_modules/i.test(line), line.trim());

    const off = await win.evaluate(() => window.filexApp.setSettings({ launchAtLogin: false }));
    check('turning it off removes the entry',
      off.launchAtLoginEffective === false && !/electron\.app\.filex/i.test(runKey()));
  } else {
    check('an unpackaged run reports the setting as unavailable', before.launchAtLoginEffective === false);
    check('…and asking for it still leaves the OS untouched', after.launchAtLoginEffective === false);
    // ⚠⚠ The bug that started this round: a dev run registered
    // node_modules/.../electron.exe with no project path, so every sign-in for
    // months afterwards opened Electron's own welcome window.
    const run = runKey();
    const stray = /electron\.app\.Electron/i.test(run) || /node_modules[\\/][\s\S]*?electron\.exe/i.test(run);
    check("no bare electron.exe was written to the user's Run key", !stray,
      stray ? 'a dev run must never register a login item' : 'clean');
    await win.evaluate(() => window.filexApp.setSettings({ launchAtLogin: false }));
  }
}

// Persistence across a reload is the whole point of storing it.
await win.reload();
await win.waitForLoadState('domcontentloaded');
await win.waitForSelector('.fe', { timeout: 20_000 }).catch(() => {});
await sleep(600);
check('the mode survives a reload', (await mode()).stored === 'auto');

await app.close();
finish();
