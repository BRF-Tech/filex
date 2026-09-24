// Notifications, measured in a real browser.
//
//   node e2e/shots/notifications.mjs
//
// What this proves, and why each part needs a browser rather than a unit test:
//
//   1. THE BELL MOVES. A real write into a real storage produces a real row,
//      and the unread count the page reads goes up. A unit test can only prove
//      the store would have counted it.
//   2. A BROWSER NOTIFICATION IS CONSTRUCTED. `window.Notification` is stubbed
//      before the app boots and every construction is recorded — a real OS
//      toast cannot be read back from a page, so the constructor call IS the
//      measurement.
//   3. THE CLICK LANDS. The recorded notification's own onclick is fired, and
//      the resulting URL, the folder the explorer opened and the row that ends
//      up `aria-selected="true"` are all asserted. This is the part the whole
//      feature is for and the part no unit test can reach: it depends on the
//      explorer's hash contract and on its row markup.
//
// It also checks the same click coming from the BELL ROW, and that the browser
// notification stays silent inside the desktop shell (where a native OS one is
// shown instead).
//
// ⚠ Runs against its OWN instance on its own port with its own data dir. The
// shared dev instance on :5212 is somebody else's, and a measurement that
// mutates it is a measurement that breaks their run.
//
// ⚠ Not the Playwright MCP browser: sibling agents share that profile and it
// locks. This drives the @playwright/test chromium under e2e/node_modules,
// the way e2e/shots/driveshell.mjs does.

import { spawn } from 'node:child_process';
import { existsSync, mkdirSync, rmSync, statSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { basename, dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { chromium } from '@playwright/test';

const HERE = dirname(fileURLToPath(import.meta.url));
const REPO = resolve(HERE, '../..');

const PORT = Number(process.env.NOTIFY_PORT ?? 5399);
const URL = `http://127.0.0.1:${PORT}`;
const DATA = join(tmpdir(), 'filex-notify-measure-data');
const FILES = join(tmpdir(), 'filex-notify-measure-files');
const ADMIN = { email: 'admin@local', password: 'admin' };

const log = (...a) => console.log('•', ...a);
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

const results = [];
function check(name, ok, detail) {
  results.push({ name, ok, detail });
  console.log(`${ok ? '  PASS' : '  FAIL'}  ${name}${detail ? ` — ${detail}` : ''}`);
}

function binary() {
  // ⚠ NOTIFY_BIN first. What this script measures — the URL the click produces,
  // the folder the explorer opens on, the row that ends up aria-selected — is
  // the EXPLORER'S markup, so running it against whatever `filex-notifyprobe`
  // happens to be lying in bin/ measures whichever build that was. On
  // 2026-09-13 that was the day-old binary, i.e. the UI before the shell was
  // rebuilt, while the shots beside it came from the new one.
  const override = process.env.NOTIFY_BIN;
  if (override) {
    if (!existsSync(override)) throw new Error(`NOTIFY_BIN does not exist: ${override}`);
    return override;
  }
  for (const p of [join(REPO, 'bin/filex-notifyprobe.exe'), join(REPO, 'bin/filex-notifyprobe')]) {
    if (!existsSync(p)) continue;
    // ⚠ Say so when the probe binary is older than the main one. A stale probe
    // does not fail — it PASSES, against the UI of whenever it was built, and
    // a green run is exactly what stops anybody looking. (2026-09-13: this
    // reported 16/16 on a day-old binary while the shell had been rebuilt;
    // against the new one it was 15/16 and the miss was real.)
    for (const main of [join(REPO, 'bin/filex.exe'), join(REPO, 'bin/filex')]) {
      if (!existsSync(main)) continue;
      if (statSync(main).mtimeMs > statSync(p).mtimeMs + 60_000) {
        log(`⚠ ${basename(p)} is OLDER than ${basename(main)} — this measures the`);
        log('  UI of the older build. Rebuild it, or pass NOTIFY_BIN=<path>.');
      }
      break;
    }
    return p;
  }
  throw new Error(
    'build it first:\n' +
      "  wsl -e bash -lc 'cd /mnt/g/filex/backend && CGO_ENABLED=0 GOOS=windows go build -o /mnt/g/filex/bin/filex-notifyprobe.exe ./cmd/filex'",
  );
}

async function waitForHealth() {
  for (let i = 0; i < 120; i++) {
    try {
      const r = await fetch(`${URL}/healthz`);
      if (r.ok) return;
    } catch {
      /* not up yet */
    }
    await sleep(250);
  }
  throw new Error(`no /healthz on ${URL}`);
}

async function api(token, path, init = {}) {
  return fetch(`${URL}${path}`, {
    ...init,
    headers: {
      'Content-Type': 'application/json',
      ...(token ? { Authorization: `Bearer ${token}` } : {}),
      ...(init.headers ?? {}),
    },
  });
}

async function main() {
  rmSync(DATA, { recursive: true, force: true });
  rmSync(FILES, { recursive: true, force: true });
  mkdirSync(join(FILES, 'Documents'), { recursive: true });
  writeFileSync(join(FILES, 'Documents', 'already-here.txt'), 'x');
  writeFileSync(join(FILES, 'readme.txt'), 'x');
  mkdirSync(DATA, { recursive: true });

  const bin = binary();
  log(`booting ${bin} on :${PORT}`);
  const proc = spawn(bin, ['serve'], {
    env: {
      ...process.env,
      FILEX_LISTEN: `127.0.0.1:${PORT}`,
      FILEX_DATA_DIR: DATA,
      FILEX_ADMIN_EMAIL: ADMIN.email,
      FILEX_ADMIN_PASSWORD: ADMIN.password,
      FILEX_DEFAULT_LOCALE: 'en',
      FILEX_SECRET_KEY: 'notify-measure-key-not-a-real-secret',
    },
    stdio: ['ignore', 'pipe', 'pipe'],
  });
  proc.stdout.on('data', (d) => process.env.VERBOSE && process.stdout.write(d));
  proc.stderr.on('data', (d) => process.env.VERBOSE && process.stderr.write(d));

  let browser;
  try {
    await waitForHealth();

    const token = await api(null, '/api/auth/login', {
      method: 'POST',
      body: JSON.stringify({ email: ADMIN.email, password: ADMIN.password }),
    })
      .then((r) => r.json())
      .then((b) => b.token);

    const storageRes = await api(token, '/api/admin/storages', {
      method: 'POST',
      body: JSON.stringify({
        name: 'qldemo',
        driver: 'local',
        mount_path: FILES,
        config: { path: FILES },
        sync_mode: 'ondemand',
        sync_interval_s: 0,
        enabled: true,
      }),
    });
    if (!storageRes.ok) throw new Error(`storage: ${storageRes.status} ${await storageRes.text()}`);
    // Index it so the folder is listable before anything is written into it.
    await api(token, `/api/files/manager?action=index&path=${encodeURIComponent('qldemo://')}`);
    await api(token, `/api/files/manager?action=index&path=${encodeURIComponent('qldemo://Documents')}`);
    log('storage qldemo ready');

    browser = await chromium.launch();

    // ── the Notification stub ──────────────────────────────────────────
    // Installed before ANY app script runs. Records every construction and
    // hands the instances back, which is the only way to read a notification
    // back from a page — the real thing is drawn by the OS.
    const stub = `
      window.__notifs = [];
      class StubNotification {
        constructor(title, options) {
          this.title = title; this.options = options || {};
          this.onclick = null; this.closed = false;
          window.__notifs.push(this);
        }
        close() { this.closed = true; }
      }
      StubNotification.permission = 'granted';
      StubNotification.requestPermission = async () => 'granted';
      window.Notification = StubNotification;
      try {
        localStorage.setItem('filex.tourDone', '1');
        localStorage.setItem('filex.locale', 'en');
      } catch {}
    `;

    const ctx = await browser.newContext();
    await ctx.addInitScript(stub);
    const page = await ctx.newPage();
    page.on('pageerror', (e) => console.log('  [pageerror]', String(e).slice(0, 200)));

    await page.goto(`${URL}/admin/login`);
    await page.fill('#email', ADMIN.email);
    await page.fill('#password', ADMIN.password);
    await page.click('button[type="submit"]');
    // ⚠ Not /admin/dashboard: signing in lands on /admin/home for every role
    // since 2026-09-12 (web/src/router/index.ts). This waited 25s on a page that
    // had already arrived.
    await page.waitForURL(/\/admin\/(home|dashboard|explore)/, { timeout: 25_000 });
    log('signed in');

    const unread = () =>
      page.evaluate(() =>
        fetch('/api/notifications/unread-count', { credentials: 'include' })
          .then((r) => r.json())
          .then((b) => b.count),
      );

    // ⚠ By data-testid since 2026-09-14. The bell used to be a lucide icon
    // with a rose DOT, and this looked for `svg.lucide-bell` + `bg-rose-500`;
    // the bell that now sits in every header (web/src/components/
    // NotificationBell.vue) draws its icon from core's actionIcons and a
    // COUNT badge, so the old probe answered "(no bell button)" on a page
    // with a bell on it. Read straight out of the DOM so a miss reports what
    // IS there rather than an empty locator.
    //
    // ⚠ And by the testid of the ONE badge since v0.43.0: the count is
    // `UnreadBadge.vue` (`unread-badge`), drawn on every surface that shows an
    // unread count, and the bell's own `notification-bell-count` span is gone.
    // The probe that still asked for it failed this check on a bell that was
    // plainly showing "1".
    const dotState = () =>
      page.evaluate(() => {
        const btn = document.querySelector('[data-testid="notification-bell"]');
        if (!btn) return { found: false, html: '(no bell button)' };
        const badge = btn.querySelector('[data-testid="unread-badge"]');
        return {
          found: !!badge && /\d/.test(badge.textContent || ''),
          html: btn.outerHTML.replace(/\s+/g, ' ').slice(0, 200),
        };
      });

    const before = await unread();
    check('1a. baseline unread count read from the page', typeof before === 'number', `count=${before}`);

    // ── the event ──────────────────────────────────────────────────────
    // A real write through a real surface (the editor save path), which is
    // what raises file.uploaded with a file target.
    const NAME = 'measure-me.txt';
    const REL = `Documents/${NAME}`;
    const w = await api(token, '/api/files/save-text', {
      method: 'POST',
      body: JSON.stringify({ path: `qldemo://${REL}`, content: 'measured' }),
    });
    check('1b. a real write went through', w.ok, `HTTP ${w.status}`);

    // The row carries the typed target the click routes on.
    const row = await api(token, '/api/notifications?limit=1').then((r) => r.json());
    const target = row.items?.[0]?.target;
    check(
      '1c. the notification row carries a typed target',
      target?.kind === 'file' && target?.storage === 'qldemo' && target?.path === REL,
      JSON.stringify(target),
    );

    // ── 1. the bell count moves ────────────────────────────────────────
    let after = before;
    for (let i = 0; i < 60 && after <= before; i++) {
      await sleep(1000);
      after = await unread();
    }
    check('1. the bell count moved', after > before, `${before} → ${after}`);
    // ⚠ Polled, not read once. The dot is drawn from the STORE, which the
    // 15 s watcher fills — the raw count above moves the moment the row is
    // written, the badge moves on the next tick. Reading it immediately
    // measures the poll interval, not the badge.
    let dot = await dotState();
    for (let i = 0; i < 25 && !dot.found; i++) {
      await sleep(1000);
      dot = await dotState();
    }
    check('1d. the bell shows its unread dot', dot.found, dot.html);

    // ── 2. a browser Notification was constructed ──────────────────────
    let notifs = [];
    for (let i = 0; i < 40 && !notifs.length; i++) {
      notifs = await page.evaluate(() =>
        window.__notifs.map((n) => ({ title: n.title, body: n.options.body, tag: n.options.tag })),
      );
      if (!notifs.length) await sleep(1000);
    }
    check(
      '2. a browser Notification was constructed',
      notifs.length > 0 && String(notifs[0].body ?? '').includes(NAME),
      JSON.stringify(notifs[0] ?? null),
    );
    check(
      '2b. …tagged per notification id, so one event cannot become two toasts',
      /^filex-notification-\d+$/.test(notifs[0]?.tag ?? ''),
      notifs[0]?.tag,
    );

    // ── 3. the click lands on the right folder, with the right row ─────
    await page.evaluate(() => {
      const n = window.__notifs[window.__notifs.length - 1];
      n.onclick?.();
    });
    await page.waitForURL(/\/admin\/explore/, { timeout: 20_000 });
    const url = new global.URL(page.url());
    check(
      '3a. the click routed to the explorer, at the right folder',
      url.pathname === '/admin/explore' && decodeURIComponent(url.hash) === '#qldemo/Documents',
      `${url.pathname}${url.search}${url.hash}`,
    );
    check(
      '3b. …carrying the row to select',
      url.searchParams.get('select') === `qldemo://${REL}`,
      url.searchParams.get('select') ?? '(none)',
    );

    // Polled: the reveal retries until the listing has settled, so the answer
    // that matters is the one it comes to rest on.
    let selected = null;
    for (let i = 0; i < 20 && selected !== 'true'; i++) {
      selected = await page
        .locator(`[data-fe-path="qldemo://${REL}"]`)
        .first()
        .getAttribute('aria-selected')
        .catch(() => null);
      if (selected !== 'true') await sleep(700);
    }
    check('3c. …and that row is the selected one', selected === 'true', `aria-selected=${selected}`);

    const otherSelected = await page.evaluate(
      (want) =>
        Array.from(document.querySelectorAll('[data-fe-path]'))
          .filter((el) => el.getAttribute('aria-selected') === 'true')
          .map((el) => el.getAttribute('data-fe-path'))
          .filter((p) => p !== want),
      `qldemo://${REL}`,
    );
    check('3d. …and nothing else is selected', otherSelected.length === 0, JSON.stringify(otherSelected));

    // ── the same click, from the bell row ──────────────────────────────
    const NAME2 = 'second-one.txt';
    await api(token, '/api/files/save-text', {
      method: 'POST',
      body: JSON.stringify({ path: `qldemo://${NAME2}`, content: 'second' }),
    });
    await page.goto(`${URL}/admin/dashboard`);
    await page.waitForTimeout(1500);
    // ⚠ Not `button[aria-label="Notifications"]`: with anything unread the
    // label is "Notifications — N unread", so the exact match found nothing
    // and this sat in a 30s timeout.
    await page.click('[data-testid="notification-bell"]');
    await page.waitForTimeout(500);
    await page.locator('[data-testid="notification-row"]', { hasText: NAME2 }).first().click();
    await page.waitForURL(/\/admin\/explore/, { timeout: 20_000 });
    const url2 = new global.URL(page.url());
    // Polled the same way the reveal itself retries: the listing settles a
    // moment after its rows appear.
    let sel2 = null;
    for (let i = 0; i < 20 && sel2 !== 'true'; i++) {
      sel2 = await page
        .locator(`[data-fe-path="qldemo://${NAME2}"]`)
        .first()
        .getAttribute('aria-selected')
        .catch(() => null);
      if (sel2 !== 'true') await sleep(700);
    }
    check(
      '4. the bell row lands in the same place as the toast',
      decodeURIComponent(url2.hash) === '#qldemo' &&
        url2.searchParams.get('select') === `qldemo://${NAME2}` &&
        sel2 === 'true',
      `${url2.search}${url2.hash} aria-selected=${sel2}`,
    );

    // ── 6. the settings screen: permission is asked from a CLICK ───────
    //
    // A separate context whose stub reports permission as "default", so the
    // button is the thing that changes it. The measurement is that
    // requestPermission is NOT called while the page loads and IS called when
    // the button is pressed — asking on load is the pattern browsers punish.
    const sctx = await browser.newContext();
    await sctx.addInitScript(`
      window.__askedAt = [];
      window.__notifs = [];
      class StubNotification {
        constructor(title, options) { this.title = title; this.options = options || {}; this.onclick = null; window.__notifs.push(this); }
        close() {}
      }
      StubNotification.permission = 'default';
      StubNotification.requestPermission = async () => {
        window.__askedAt.push(Date.now());
        StubNotification.permission = 'granted';
        return 'granted';
      };
      window.Notification = StubNotification;
      try { localStorage.setItem('filex.tourDone', '1'); localStorage.setItem('filex.locale', 'en'); } catch {}
    `);
    const spage = await sctx.newPage();
    await spage.goto(`${URL}/admin/login`);
    await spage.fill('#email', ADMIN.email);
    await spage.fill('#password', ADMIN.password);
    await spage.click('button[type="submit"]');
    await spage.waitForURL(/\/admin\/(home|dashboard|explore)/, { timeout: 25_000 });
    // ⚠ A person's own preferences live in ONE place — the user settings
    // dialog. The admin Notifications page points at it
    // (`notif-open-own-prefs`, which opens the dialog already on its
    // notifications section) and no longer carries the button itself. This
    // script waited for the page's old `notif-browser-ask` until v0.43.0 and
    // died here, taking every scene after it down with it;
    // `web/tests/deploy/shotsFixtures.test.ts` now fails when a test id a shot
    // script waits for is emitted nowhere in the interface.
    await spage.goto(`${URL}/admin/notifications`);
    await spage.waitForSelector('[data-testid="notif-open-own-prefs"]', { timeout: 20_000 });
    const askedOnLoad = await spage.evaluate(() => window.__askedAt.length);
    check('6a. permission was NOT asked on page load', askedOnLoad === 0, `calls=${askedOnLoad}`);
    await spage.click('[data-testid="notif-open-own-prefs"]');
    await spage.waitForSelector('[data-testid="user-settings-browser-ask"]', { timeout: 20_000 });
    const askedBefore = await spage.evaluate(() => window.__askedAt.length);
    check('6a2. …nor by opening the dialog that offers it', askedBefore === 0, `calls=${askedBefore}`);
    await spage.click('[data-testid="user-settings-browser-ask"]');
    await spage.waitForTimeout(600);
    const askedAfter = await spage.evaluate(() => window.__askedAt.length);
    check('6b. …and IS asked from the button', askedAfter === 1, `calls=${askedAfter}`);
    const gone = await spage.locator('[data-testid="user-settings-browser-ask"]').count();
    check('6c. …after which the screen says it is allowed', gone === 0, `button still present: ${gone}`);
    await sctx.close();

    // ── the desktop guard ──────────────────────────────────────────────
    const dctx = await browser.newContext();
    await dctx.addInitScript(stub);
    await dctx.addInitScript(`window.filexApp = { isDesktop: true };`);
    const dpage = await dctx.newPage();
    await dpage.goto(`${URL}/admin/login`);
    await dpage.fill('#email', ADMIN.email);
    await dpage.fill('#password', ADMIN.password);
    await dpage.click('button[type="submit"]');
    await dpage.waitForURL(/\/admin\/(home|dashboard|explore)/, { timeout: 25_000 });
    await api(token, '/api/files/save-text', {
      method: 'POST',
      body: JSON.stringify({ path: 'qldemo://third-one.txt', content: 'third' }),
    });
    await sleep(22_000);
    const inShell = await dpage.evaluate(() => window.__notifs.length);
    check(
      '5. no browser notification inside the desktop shell (it shows a native one)',
      inShell === 0,
      `constructed=${inShell}`,
    );
    await dctx.close();

    await ctx.close();
  } finally {
    if (browser) await browser.close().catch(() => {});
    proc.kill();
  }

  const failed = results.filter((r) => !r.ok);
  console.log(`\n${results.length - failed.length}/${results.length} checks passed`);
  if (failed.length) process.exitCode = 1;
}

await main();
