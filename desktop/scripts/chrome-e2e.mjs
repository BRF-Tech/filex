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

// ⚠ ensureNoTour() cannot win a race it cannot see: the tour ENTERS a moment
// after an explorer mounts (a new tab is a new explorer), so a dismissal that
// finds nothing and returns is followed by a click the tour then swallows —
// measured 2026-09-21, one run in two waited out Playwright's 30 s at the tab
// close below. Clicks that can meet it retry past it instead.
async function clickPastTour(locator) {
  for (let i = 0; i < 6; i++) {
    try {
      await locator.click({ timeout: 2500 });
      return;
    } catch (e) {
      if (!/fe-tour|intercepts pointer events/.test(String(e))) throw e;
      await ensureNoTour();
    }
  }
  await locator.click();
}

// ── the server's brand mark ──────────────────────────────────────────
// GET /api/branding is public and fm.example.com serves a data: URI. The plate is
// painted only once that request lands, so give it a moment.
await win.waitForSelector('#rail .slot.active .avatar--brand img', { timeout: 15_000 }).catch(() => {});
const brand = await win.evaluate(() => {
  const img = document.querySelector('#rail .slot.active .avatar--brand img');
  return {
    // ⚠ The app's own mark is in the TOP BAR now, not on the rail — the rail
    // drew a second copy of it about 60px from this one (gorunum:v3-shell gave
    // the explorer a brand corner and this window fills it via `config.brand`).
    // The RULE is unchanged and is what this still measures: something on
    // screen always names the app, and it is never the selected account's
    // badge. It just moved, and gained the wordmark on the way.
    //
    // ⚠ naturalWidth, not "the element exists": the mark is a data: URI, and a
    // URI that fails to decode leaves a perfectly good <img> with a src on a
    // corner that is blank.
    brandImgDecoded: (() => {
      const img = document.querySelector('.fe-toolbar__mark .fe-toolbar__markimg');
      return !!img && img.naturalWidth > 0 && img.naturalHeight > 0;
    })(),
    brandWordmark: [...(document.querySelectorAll('.fe-toolbar__mark span') ?? [])]
      .map((e) => e.textContent.trim()).filter(Boolean).join(' '),
    // …and the rail starts with a SERVER, with no second mark above it.
    railStartsWithAccount: document.querySelector('#rail')?.firstElementChild?.classList.contains('slot'),
    railHasAppMark: !!document.querySelector('#rail .appmark'),
    src: img ? (img.getAttribute('src') || '').slice(0, 24) : null,
    // naturalWidth is the honest question: a broken URL still leaves an <img>
    // in the DOM, and the row would be an empty white plate.
    decoded: !!img && img.naturalWidth > 0 && img.naturalHeight > 0,
    // Rows that have no branding must keep their initials rather than going blank.
    fallbacks: [...document.querySelectorAll('#rail .avatar:not(.avatar--brand)')]
      .map((el) => el.textContent.trim()),
  };
});
check('the top bar names the app — the mark actually decoded', brand.brandImgDecoded === true);
check('…with the wordmark beside it', brand.brandWordmark === 'filex', brand.brandWordmark || '(none)');
check('…and the rail does not draw a SECOND copy of the same mark',
  brand.railHasAppMark === false && brand.railStartsWithAccount === true,
  `appmark=${brand.railHasAppMark} firstChild=${brand.railStartsWithAccount ? 'account' : 'something else'}`);
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

// ⚠ Again, here. `ensureNoTour()` above runs seconds before this point, and
// between the two sits a 15s wait for the branding plate — a wait that runs to
// its full timeout on any server with no logo set. The tour appears during it,
// and the click below then fails with "…fe-tour intercepts pointer events",
// which reads as "the + is broken". Measured 2026-09-12 against a local
// instance: the branding wait timed out and every later click was swallowed.
await ensureNoTour();

// The + lives in the strip, so a hidden strip means no way to open a tab.
await clickPastTour(win.locator('.fe-tabs__new'));
await sleep(400);
check('the + in the strip opens a second tab', (await tabs().count()) === 2);

// Closing back down to one must NOT take the strip away again.
// ⚠ The new tab is a fresh explorer, and a fresh explorer can open the tour
// again — see clickPastTour.
await clickPastTour(win.locator('.fe-tabs__tab').nth(1).locator('.fe-tabs__close'));
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
// The report was "it overflowed the screen and keeps sliding"; this reaches that state
// on whatever screen the suite happens to run on.
let overflowed = false;
for (let i = 0; i < 60 && !overflowed; i++) {
  await clickPastTour(win.locator('.fe-tabs__new'));
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
  await clickPastTour(win.locator('.fe-tabs__tab').nth(n - 1).locator('.fe-tabs__close'));
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
//
// ⚠ Opened from the "⋯" menu, not from a toolbar button. gorunum:v2 took the
// theme control off the header ("density, theme → user settings, and the '⋯'
// menu below", Toolbar.vue) and `.fe-toolbar__theme` stopped existing — this
// suite then spent 30s waiting for it and died with a locator timeout, which
// reads as "the theme gallery is broken" rather than "the door moved".
await ensureNoTour();
await win.locator('[data-testid="drive-more"]').first().click();
await sleep(500);
await win.evaluate(() => {
  const visible = (e) => e.getClientRects().length > 0 && !e.closest('[aria-hidden="true"]');
  [...document.querySelectorAll('.fe-ctx__item, [role="menuitem"], button, li')]
    .filter(visible)
    .find((x) => /^(Tema|Theme)$/i.test((x.textContent ?? '').trim()))
    ?.click();
});
await win.waitForSelector('.fe-thememode', { timeout: 10_000 }).catch(() => {});
const opts = win.locator('.fe-thememode__opt');
check('the theme gallery offers a day/night/automatic switch', (await opts.count()) === 3);
check('exactly one of the three reads as active',
  (await win.locator('.fe-thememode__opt.is-active').count()) === 1);

/** Reads what the explorer actually resolved to, not what we asked for — and
 *  what the SHELL around it did about it.
 *
 *  ⚠⚠ `shellDark` is the half this suite used to have no opinion about, and it
 *  was wrong for as long as nobody looked. Measured 2026-09-12: with the
 *  preference on Dark the file list went dark and the app's own settings
 *  surface stayed white — a full-window white page inside a dark application.
 *  The shell now copies the explorer's answer (ui/app.html →
 *  adoptExplorerTheme), so the two can be asserted against each other. */
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
      // …and the same question asked of the chrome that wraps it.
      shellDark: document.documentElement.classList.contains('dark'),
      shellBg: getComputedStyle(document.documentElement).getPropertyValue('--fe-bg').trim(),
      shellScheme: getComputedStyle(document.documentElement).colorScheme,
    };
  });

// The window's close button turns the THEME's danger red on hover, with the
// theme's on-colour glyph — the pair the product's own danger button uses. It
// was a literal #e53935 on #fff that no theme could reach (0.42.0–0.42.2).
// Hovered through Playwright (CDP), not the operator's mouse.
const closeHover = async () => {
  await win.hover('#winctl button.winctl--close');
  await sleep(300); // the .12s background transition
  const r = await win.evaluate(() => {
    const b = document.querySelector('#winctl button.winctl--close');
    const probe = document.createElement('div');
    probe.style.background = 'var(--fe-danger)';
    probe.style.color = 'var(--fe-text-on-primary)';
    document.body.appendChild(probe);
    const want = { bg: getComputedStyle(probe).backgroundColor, fg: getComputedStyle(probe).color };
    probe.remove();
    return { got: { bg: getComputedStyle(b).backgroundColor, fg: getComputedStyle(b).color }, want };
  });
  await win.mouse.move(400, 400);
  return r;
};

await opts.nth(1).click(); // Night
await sleep(400);
if (process.platform !== 'darwin') {
  const c = await closeHover();
  check('Night: the close button hovers in the theme’s danger red, not a literal',
    c.got.bg === c.want.bg && c.got.fg === c.want.fg && c.got.bg !== 'rgb(229, 57, 53)', JSON.stringify(c));
}
let m = await mode();
check('Night paints the dark variant', m.dark && !m.light, JSON.stringify(m));
check('…and the choice is remembered', m.stored === 'dark', String(m.stored));
check('…and the APP CHROME goes dark with it, not just the file list',
  m.shellDark === true && m.shellBg === m.bg,
  `shellDark=${m.shellDark} shellBg=${m.shellBg} explorerBg=${m.bg}`);
check('…including the colour-scheme the window\u2019s own form controls follow',
  m.shellScheme === 'dark', m.shellScheme);

await opts.nth(0).click(); // Day
await sleep(400);
if (process.platform !== 'darwin') {
  const c = await closeHover();
  check('Day: the close button hovers in the theme’s danger red, not a literal',
    c.got.bg === c.want.bg && c.got.fg === c.want.fg && c.got.bg !== 'rgb(229, 57, 53)', JSON.stringify(c));
}
m = await mode();
check('Day paints the light variant', m.light && !m.dark, JSON.stringify(m));
check('…and the choice is remembered', m.stored === 'light', String(m.stored));
check('…and the chrome comes back with it', m.shellDark === false && m.shellBg === m.bg,
  `shellDark=${m.shellDark} shellBg=${m.shellBg} explorerBg=${m.bg}`);

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
    // The command must name the app UNDER TEST — an installed app or a build
    // output, whichever is being driven. Pinned to the install path this failed
    // for a packaged build sitting in release/, which is a genuine packaged run
    // and exactly what a pre-release check drives.
    const driven = (process.env.FILEX_APP_BINARY ?? '').replace(/\//g, '\\');
    check('…pointing at the executable under test',
      driven ? line.toLowerCase().includes(driven.toLowerCase()) : /filex\.exe/i.test(line),
      line.trim());
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
