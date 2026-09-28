// The window's own title bar works while ANYTHING the explorer draws is open.
//
// 2026-09-27, owner: "masaüstünde başlık çubuğu HER ZAMAN çalışsın" — with a
// dialog open, minimise, maximise and close answer the FIRST click, and the
// window still drags from its title bar. The window is frameless: the bar at
// the top (#titlebar, #winctl) is page content, so an overlay the explorer
// draws can land on it. Measured before the fix (this suite, red):
//
//   • the user settings dialog was a native <dialog> opened with showModal():
//     the browser's top layer, above every z-index, the rest of the page
//     inert — the point under "minimise" was the <dialog>, and the click
//     closed the dialog instead of minimising;
//   • the share dialog's scrim (z-index 10000, above the bar's 220): the same;
//   • a dialog built on core's Modal (rename) was already fine (z-index 70).
//   The OS hit test said "caption" on the bar in every case: dragging was
//   never what broke, the clicks were.
//
// Core now keeps every window-sized layer below `--fe-overlay-top` (the host's
// reserved strip — the desktop sets it to its bar's height), and the settings
// dialog is core's Modal like every other dialog. This suite opens each kind of
// layer in turn and, for each:
//
//   1. the bar is not covered: the point under each window button is that
//      button, and the point on the brand is the bar;
//   2. Windows: the OS asks the window what is under a point (WM_NCHITTEST) —
//      HTCAPTION on the bar is what lets a drag move the window. Sent to THIS
//      window's handle; no cursor moves;
//   3. ONE click (Playwright's page mouse, CDP — never the operator's) on
//      minimise minimises, on maximise maximises (and one more restores), on
//      close closes — the window's close() is counted in the main process, not
//      performed — and the dialog is STILL open afterwards: the click was the
//      bar's. The layer is re-opened before each click if it is not open, so
//      a click that closed it cannot leave the next one measuring nothing.
//
// Run: node scripts/titlebar-e2e.mjs
// Env: FILEX_SERVER, FILEX_EMAIL, FILEX_PASSWORD, FILEX_STORAGE

import { execFileSync } from 'node:child_process';
import fs from 'node:fs';
import path from 'node:path';
import { SHOTS, STORAGE, api, check, finish, launchApp, signIn, skipTour, sleep, tickRow } from './lib/harness.mjs';

fs.mkdirSync(SHOTS, { recursive: true });
const DIR = `titlebar-e2e-${Date.now()}`;
const FILE = 'başlık.txt';
const HTCLIENT = 1;
const HTCAPTION = 2;

const { app } = await launchApp({ lang: process.env.TITLEBAR_LANG || 'en-US' });
let token = null;
try {
  // Nothing leaves this instance: no browser, no toast on the operator's screen.
  await app.evaluate(({ shell, Notification }) => {
    shell.openExternal = async () => {};
    Notification.prototype.show = function show() {};
  });
  const { win, adminToken } = await signIn(app, { label: 'filex desktop — title bar e2e' });
  token = adminToken;
  if (process.platform === 'darwin') {
    // macOS keeps the native traffic lights; there is no bar of ours to cover.
    check('macOS draws the system title bar — nothing of ours to measure', true);
    throw Object.assign(new Error('darwin'), { skip: true });
  }

  // A folder with one file: the rename and share dialogs need a selection.
  const mk = await api('/api/files/manager?action=newfolder', {
    method: 'POST', headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ path: `${STORAGE}://`, name: DIR }),
  }, token);
  if (!mk.ok) throw new Error(`scratch folder: ${mk.status}`);
  const up = await api('/api/files/save-text', {
    method: 'POST', headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ path: `${STORAGE}://${DIR}/${FILE}`, content: 'title bar\n' }),
  }, token);
  if (!up.ok) throw new Error(`seed file: ${up.status}`);

  await win.waitForTimeout(3500);
  await skipTour(win);
  await win.evaluate((d) => {
    const row = [...document.querySelectorAll('.fe-list__row')].find((r) => r.textContent?.includes(d));
    row?.dispatchEvent(new MouseEvent('dblclick', { bubbles: true }));
  }, DIR);
  await win.waitForTimeout(2500);
  await skipTour(win);

  // THIS window's close() is counted, not performed.
  await app.evaluate(({ BrowserWindow }) => {
    globalThis.__closes = 0;
    const w = BrowserWindow.getAllWindows().find((x) => x.webContents.getURL().startsWith('app://filex'));
    if (w) w.close = () => { globalThis.__closes++; };
  });
  const winState = () => app.evaluate(({ BrowserWindow }) => {
    const w = BrowserWindow.getAllWindows().find((x) => x.webContents.getURL().startsWith('app://filex'));
    return { min: w.isMinimized(), max: w.isMaximized(), closes: globalThis.__closes };
  });
  const settle = () => app.evaluate(({ BrowserWindow }) => {
    const w = BrowserWindow.getAllWindows().find((x) => x.webContents.getURL().startsWith('app://filex'));
    if (w.isMinimized()) w.restore();
    if (w.isMaximized()) w.unmaximize();
    w.show();
  });

  /** Where the bar's parts are, and what the page has at those points. */
  const barPoints = () => win.evaluate(() => {
    const at = (el) => {
      const r = el.getBoundingClientRect();
      return { x: Math.round(r.left + r.width / 2), y: Math.round(r.top + r.height / 2) };
    };
    const btn = (k) => document.querySelector(`#winctl button[data-lk="${k}"]`);
    const brand = document.getElementById('titlebar__brand');
    const pts = {
      min: btn('win.minimize') && at(btn('win.minimize')),
      max: btn('win.maximize') && at(btn('win.maximize')),
      close: btn('win.close') && at(btn('win.close')),
      brand: brand && at(brand),
    };
    const want = {
      min: btn('win.minimize'),
      max: btn('win.maximize'),
      close: btn('win.close'),
      brand: document.getElementById('titlebar'),
    };
    const hit = {};
    for (const [k, p] of Object.entries(pts)) {
      const el = p && document.elementFromPoint(p.x, p.y);
      hit[k] = !!el && !!want[k] && want[k].contains(el);
      if (!hit[k]) hit[`${k}Got`] = el ? `${el.tagName.toLowerCase()}${el.id ? '#' + el.id : ''}.${String(el.className).split(' ')[0]}` : 'nothing';
    }
    return { pts, hit };
  });

  /** WM_NCHITTEST at page points, answered by the window itself. */
  async function osHitTest(points) {
    if (process.platform !== 'win32') return null;
    const where = await app.evaluate(({ BrowserWindow, screen }, pts) => {
      const w = BrowserWindow.getAllWindows().find((x) => x.webContents.getURL().startsWith('app://filex'));
      const b = w.getContentBounds();
      return {
        hwnd: w.getNativeWindowHandle().readBigUInt64LE(0).toString(),
        pts: pts.map((p) => {
          const s = screen.dipToScreenPoint({ x: b.x + p.x, y: b.y + p.y });
          return { x: Math.round(s.x), y: Math.round(s.y) };
        }),
      };
    }, points);
    const lparams = where.pts.map((p) => ((p.y & 0xffff) * 0x10000 + (p.x & 0xffff)).toString());
    const ps = [
      "Add-Type -Namespace FxE2e -Name U -MemberDefinition '[DllImport(\"user32.dll\")] public static extern System.IntPtr SendMessageW(System.IntPtr h, uint m, System.IntPtr w, System.IntPtr l);'",
      `$h = [IntPtr][Int64]${where.hwnd}`,
      `foreach ($l in @(${lparams.join(',')})) { [FxE2e.U]::SendMessageW($h, 0x84, [IntPtr]0, [IntPtr][Int64]$l).ToInt64() }`,
    ].join('; ');
    const out = execFileSync('powershell.exe', ['-NoProfile', '-NonInteractive', '-Command', ps], { encoding: 'utf8' });
    return out.trim().split(/\s+/).map(Number);
  }

  /** Every check, with `layer` open (null: nothing open, the baseline). */
  async function measure(label, layer) {
    const ensure = async () => {
      if (!layer) return true;
      if (!(await layer.isOpen())) await layer.open();
      return layer.isOpen();
    };
    const stillOpen = async () => (layer ? layer.isOpen() : true);
    const tail = layer ? ', and what was open stays open' : '';

    await settle();
    await sleep(400);
    await ensure();
    const bar = await barPoints();
    check(`${label}: the title bar is not covered — each window button and the bar itself are what is under the pointer`,
      bar.hit.min && bar.hit.max && bar.hit.close && bar.hit.brand, JSON.stringify(bar.hit));

    // Drag: the window says "caption" on its bar, "client" on its buttons.
    const os = await osHitTest([bar.pts.brand, bar.pts.min]);
    if (os) {
      check(`${label}: the OS may drag the window from its title bar (WM_NCHITTEST = HTCAPTION)`, os[0] === HTCAPTION, `brand=${os[0]}`);
      check(`${label}: …and the window buttons stay buttons (HTCLIENT)`, os[1] === HTCLIENT, `minimise=${os[1]}`);
    }

    // Minimise — one click.
    const openMin = await ensure();
    const b0 = await barPoints();
    await win.mouse.click(b0.pts.min.x, b0.pts.min.y);
    await sleep(700);
    let s = await winState();
    await settle();
    await sleep(700);
    const afterMin = await stillOpen();
    check(`${label}: ONE click on minimise minimises the window${tail}`,
      openMin && s.min === true && afterMin, `${JSON.stringify(s)} open before=${openMin} after=${afterMin}`);

    // Maximise — one click, and one more to come back.
    const openMax = await ensure();
    const b1 = await barPoints();
    await win.mouse.click(b1.pts.max.x, b1.pts.max.y);
    await sleep(900);
    s = await winState();
    const afterMax = await stillOpen();
    check(`${label}: ONE click on maximise maximises the window${tail}`,
      openMax && s.max === true && afterMax, `${JSON.stringify(s)} open before=${openMax} after=${afterMax}`);
    await ensure();
    const b2 = await barPoints();
    await win.mouse.click(b2.pts.max.x, b2.pts.max.y);
    await sleep(900);
    s = await winState();
    check(`${label}: …and one more restores it`, s.max === false, JSON.stringify(s));
    await settle();
    await sleep(500);

    // Close — one click reaches the window's close (counted, not performed).
    const openClose = await ensure();
    const before = (await winState()).closes;
    const b3 = await barPoints();
    await win.mouse.click(b3.pts.close.x, b3.pts.close.y);
    await sleep(600);
    s = await winState();
    const afterClose = await stillOpen();
    check(`${label}: ONE click on close closes the window${tail}`,
      openClose && s.closes === before + 1 && afterClose,
      `close() calls ${s.closes - before}, open before=${openClose} after=${afterClose}`);
  }

  // ── 0. nothing open: the measurement itself works ─────────────────────
  await measure('nothing open', null);

  // ── 1. User settings — the dialog that was a native top-layer <dialog> ──
  const settingsDlg = win.locator('[data-testid="user-settings-dialog"]');
  const settings = {
    isOpen: () => settingsDlg.isVisible().catch(() => false),
    open: async () => {
      await win.locator('[data-testid="explore-account"]').click();
      await win.locator('[data-testid="explore-user-settings"]').click();
      await settingsDlg.waitFor({ state: 'visible', timeout: 10_000 });
      await sleep(500);
    },
  };
  await settings.open();
  await win.screenshot({ path: path.join(SHOTS, 'titlebar-01-settings.png') }).catch(() => {});
  await measure('User settings open', settings);

  // The dialog is still a dialog: the keyboard stays in it, Escape closes it,
  // and the focus goes back to the avatar that opened it.
  if (!(await settings.isOpen())) await settings.open();
  await sleep(300);
  let wandered = 0;
  for (let i = 0; i < 30; i++) {
    await win.keyboard.press('Tab');
    if (!(await win.evaluate(() => !!document.activeElement?.closest('[data-testid="user-settings-dialog"]')))) wandered++;
  }
  check('with the settings dialog open, Tab stays inside it', wandered === 0, `${wandered}/30 presses left the dialog`);
  await win.keyboard.press('Escape');
  const escClosed = await settingsDlg.waitFor({ state: 'hidden', timeout: 5000 }).then(() => true, () => false);
  await sleep(300);
  const focusNow = await win.evaluate(() => {
    const a = document.activeElement;
    return a?.closest?.('[data-testid="explore-account"]') ? 'avatar' : a === document.body ? 'body' : `${a?.tagName?.toLowerCase()}.${String(a?.className).split(' ')[0]}`;
  });
  check('Escape closes it, and the focus goes back to the avatar that opened it', escClosed && focusNow === 'avatar',
    `closed=${escClosed} focus=${focusNow}`);

  // ── 2. a dialog built on core's Modal — rename ────────────────────────
  const selBar = win.locator('[data-testid="selection-bar"]');
  const select = async () => {
    if (!(await selBar.isVisible().catch(() => false))) await tickRow(win, FILE);
  };
  const renameBox = win.locator('[role="dialog"] input.fe-input').first();
  const rename = {
    isOpen: () => renameBox.isVisible().catch(() => false),
    open: async () => {
      await select();
      const icon = win.locator('[data-testid="selection-bar"] [data-testid="selbar-rename"]');
      if (await icon.isVisible().catch(() => false)) await icon.click();
      else {
        await win.locator('[data-testid="selection-bar"] [data-testid="selbar-more"]').click();
        await win.getByRole('menuitem', { name: /Rename|Yeniden adlandır/i }).first().click();
      }
      await renameBox.waitFor({ state: 'visible', timeout: 10_000 });
    },
  };
  await rename.open();
  await win.screenshot({ path: path.join(SHOTS, 'titlebar-02-rename.png') }).catch(() => {});
  await measure('Rename open', rename);
  await win.keyboard.press('Escape');
  await renameBox.waitFor({ state: 'hidden', timeout: 5000 }).catch(() => {});

  // ── 3. the share dialog — its own scrim, z-index 10000 ────────────────
  const scrim = win.locator('.fe-share__scrim').first();
  const share = {
    isOpen: () => scrim.isVisible().catch(() => false),
    open: async () => {
      await select();
      const icon = win.locator('[data-testid="selection-bar"] [data-testid="selbar-access"]');
      if (await icon.isVisible().catch(() => false)) await icon.click();
      else {
        await win.locator('[data-testid="selection-bar"] [data-testid="selbar-more"]').click();
        await win.getByRole('menuitem', { name: /^(Share|Paylaş)$/i }).first().click();
      }
      await scrim.waitFor({ state: 'visible', timeout: 10_000 });
      await sleep(400);
    },
  };
  await share.open();
  await win.screenshot({ path: path.join(SHOTS, 'titlebar-03-share.png') }).catch(() => {});
  await measure('Share open', share);
  await win.keyboard.press('Escape');
} catch (e) {
  if (!e?.skip) check('flow completed', false, String(e && e.message).split('\n')[0]);
} finally {
  if (token) {
    await api('/api/files/manager?action=delete', {
      method: 'POST', headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ items: [{ path: `${STORAGE}://${DIR}`, type: 'dir' }] }),
    }, token).catch(() => {});
  }
  await app.close().catch(() => {});
}

finish();
