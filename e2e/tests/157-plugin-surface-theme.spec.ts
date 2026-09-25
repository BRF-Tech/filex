/**
 * 157 — an app's screen wears the theme (#57).
 *
 * Owner, 2026-09-24: "Pluginlerin ekranları wasm olduğu için çok güzel
 * temalaşmıyorlar." An app describes its screen and filex draws it, so a
 * theme should reach it by itself. Measured on 2026-09-25 with an operator
 * theme, dark mode, compact rows and Arabic, beside filex's own screens, what
 * did NOT reach it was small and exact:
 *
 *   1. the wizard's current step: a white number on the theme's pale primary
 *      (2.3:1), where the share dialog's button uses `--fe-text-on-primary`;
 *   2. the people picker's avatar: the same white;
 *   3. a box card's number in the signing wizard: `--fe-text-on-primary`
 *      (dark in dark mode) on a signer's colour, which is the same blue in
 *      every theme (3.5:1);
 *   4. light/dark while the screen is OPEN: the explorer turned with the
 *      window, an app's page stayed in the mode it was opened in, and so did
 *      the public page (a share, a signing link).
 *
 * Every assertion reads the browser's own computed values against the tokens
 * as the browser resolves them — a picture cannot tell a token from the same
 * hex, and a stock palette hides all of it (white on the stock light primary
 * is fine). Hence an operator theme, and dark.
 *
 * The signature pad is NOT here on purpose: it is paper (white, ink #111 in
 * every theme, docs/APP-PLUGINS-API.md → "Theme tokens, node by node").
 */
import { test, expect, type APIRequestContext, type Page } from '@playwright/test';
import { apiLogin, loginAs } from '../helpers/auth';
import { dropStorageByName, newAuthedRequest, seedLocalStorage } from '../helpers/seed';
import { guardFixture, installThroughWizard, minimalPDF, resolveApp } from '../helpers/appPlugin';
import { removeApp } from '../helpers/surface';
import { walk } from '../helpers/wizard';

const APP = resolveApp('sign');

const STORAGE = `e2e-theme157-${Date.now()}`;
const MOUNT = `/tmp/filex-${STORAGE}`;
const DOC = 'agreement.pdf';
const QUALIFIED = `${STORAGE}://${DOC}`;
const PEER = { email: 'theme157@example.test', password: 'theme157-peer-pw', display_name: 'Tema Kişi' };

const THEME_KEY = 'e2e-plum157';
/** A pale primary in dark mode: the case where white ink fails and the token's does not. */
const DARK_PRIMARY = '#f08ac8';
const LIGHT_BG = '#fffaf7';
const DARK_BG = '#1b1216';
const THEME = {
  filex_theme: 1,
  key: THEME_KEY,
  name: 'Plum 157',
  light: {
    '--fe-bg': LIGHT_BG,
    '--fe-bg-elev': '#fbf1ec',
    '--fe-text': '#2a1a22',
    '--fe-text-muted': '#6e5663',
    '--fe-border': '#ecd9e1',
    '--fe-primary': '#7a1f5c',
    '--fe-primary-hover': '#63184a',
    '--fe-primary-soft': '#f3e1ec',
    '--fe-primary-ink': '#63184a',
    '--fe-danger': '#b42318',
    '--fe-warning': '#b54708',
    '--fe-ok': '#067647',
    '--fe-text-on-primary': '#ffffff',
  },
  dark: {
    '--fe-bg': DARK_BG,
    '--fe-bg-elev': '#24181e',
    '--fe-text': '#f3e6ec',
    '--fe-text-muted': '#b39aa8',
    '--fe-border': '#3f2c35',
    '--fe-primary': DARK_PRIMARY,
    '--fe-primary-hover': '#f5a8d6',
    '--fe-primary-soft': '#3a1f2e',
    '--fe-primary-ink': '#f5a8d6',
    '--fe-danger': '#ff7a70',
    '--fe-warning': '#fbbf24',
    '--fe-ok': '#4ade80',
    '--fe-text-on-primary': '#1b1216',
  },
};

const rgb = (hex: string) => {
  const n = parseInt(hex.slice(1), 16);
  return `rgb(${(n >> 16) & 255}, ${(n >> 8) & 255}, ${n & 255})`;
};

/** A token as the browser resolves it INSIDE `scope`, as an rgb() string. */
async function tokenColor(page: Page, scope: string, token: string): Promise<string> {
  return page.evaluate(
    ([sel, tok]) => {
      const host = document.querySelector(sel);
      if (!host) return `no ${sel}`;
      const probe = document.createElement('i');
      probe.style.color = `var(${tok})`;
      host.appendChild(probe);
      const c = getComputedStyle(probe).color;
      probe.remove();
      return c;
    },
    [scope, token] as const,
  );
}

async function styleOf(page: Page, sel: string, prop: 'color' | 'backgroundColor'): Promise<string> {
  return page.locator(sel).first().evaluate((el, p) => getComputedStyle(el)[p as 'color'], prop);
}

let admin: APIRequestContext;
let savedPrefs: unknown = {};

test.describe('App screens wear the theme (#57)', () => {
  test.describe.configure({ mode: 'serial' });
  guardFixture(APP, test.skip);

  test.beforeAll(async ({ playwright, baseURL, request }) => {
    admin = await newAuthedRequest(playwright, baseURL ?? '');
    await dropStorageByName(request, STORAGE);
    await seedLocalStorage(request, STORAGE, MOUNT);
    await apiLogin(request);
    const up = await request.post('/api/files/manager?action=upload', {
      multipart: { path: `${STORAGE}://`, 'file[]': { name: DOC, mimeType: 'application/pdf', buffer: minimalPDF('theme 157') } },
    });
    expect(up.ok(), `upload: ${up.status()}`).toBe(true);
    await request.post('/api/admin/users', { data: { ...PEER, role: 'user' } });
    await removeApp(request, 'sign');

    const put = await admin.put(`/api/admin/themes/${THEME_KEY}`, { data: THEME });
    expect(put.ok(), `theme: ${put.status()} ${await put.text()}`).toBe(true);
    const def = await admin.patch('/api/admin/settings', { data: { 'ui.default_theme': `custom:${THEME_KEY}` } });
    expect(def.ok(), `default theme: ${def.status()}`).toBe(true);
    // ⚠ Light/dark lives on the ACCOUNT (helpers/prefs.ts): "auto" there, so
    // the operating system — emulated below — decides. Read-modify-write, and
    // put back afterwards: the document carries other specs' preferences.
    const cur = await admin.get('/api/me/prefs?surface=web');
    savedPrefs = cur.ok() ? ((await cur.json()) as { prefs?: unknown }).prefs ?? {} : {};
    const mine = { ...(savedPrefs as Record<string, unknown>), theme: 'auto', palette: `custom:${THEME_KEY}` };
    const pr = await admin.put('/api/me/prefs?surface=web', { data: { prefs: mine } });
    expect(pr.ok(), `prefs: ${pr.status()}`).toBe(true);
  });

  test.afterAll(async ({ request }) => {
    await admin.put('/api/me/prefs?surface=web', { data: { prefs: savedPrefs } });
    await admin.patch('/api/admin/settings', { data: { 'ui.default_theme': 'default' } });
    await admin.delete(`/api/admin/themes/${THEME_KEY}`);
    await admin.dispose();
    await apiLogin(request);
    await removeApp(request, 'sign');
    await dropStorageByName(request, STORAGE);
  });

  test('install', async ({ page }) => {
    await loginAs(page);
    await installThroughWizard(page, APP);
  });

  test('the current step, the people chip and a box card take their ink from the theme — or from their own colour', async ({ page }) => {
    await page.emulateMedia({ colorScheme: 'dark' });
    await page.setViewportSize({ width: 1440, height: 900 });
    await loginAs(page);
    await page.goto(`/admin/apps/sign/request?path=${encodeURIComponent(QUALIFIED)}`);
    const root = '[data-testid="plugin-page"]';
    await expect(page.locator(root)).toHaveClass(/fe--theme-dark/);
    await expect
      .poll(() => tokenColor(page, root, '--fe-primary'), { message: 'the operator theme reaches the app page' })
      .toBe(rgb(DARK_PRIMARY));

    const onPrimary = await tokenColor(page, root, '--fe-text-on-primary');
    const primary = await tokenColor(page, root, '--fe-primary');
    expect(onPrimary, 'the theme’s own ink for its primary').toBe(rgb(THEME.dark['--fe-text-on-primary']));

    // 1. the current step's disc: the primary, and the ink the palette names for it
    const marker = '.fe-steps__item[aria-current="step"] .fe-steps__marker';
    await expect(page.locator(marker)).toBeVisible();
    expect(await styleOf(page, marker, 'backgroundColor')).toBe(primary);
    expect(await styleOf(page, marker, 'color'), 'the current step’s number: --fe-text-on-primary, not white').toBe(onPrimary);

    // 2. the people picker's avatar
    await page.getByTestId('surface-people-input').fill('theme157');
    await page.locator('[data-testid="surface-people-suggest"] li').first().click();
    const av = '[data-testid="surface-people"] .fe-speople__av';
    await expect(page.locator(av).first()).toBeVisible();
    expect(await styleOf(page, av, 'backgroundColor')).toBe(primary);
    expect(await styleOf(page, av, 'color'), 'the avatar’s letter: --fe-text-on-primary, not white').toBe(onPrimary);

    // 3. a box card's number sits on the SIGNER's colour (the same in every
    //    theme), so its ink is that colour's — white on the first signer's blue.
    await walk(page, () => page.getByTestId('surface-pdf-add-signature').isVisible().catch(() => false), 3);
    await page.getByTestId('surface-pdf-add-signature').click();
    const no = '[data-testid^="surface-pdf-card-"] .fe-spdf__cardno';
    await expect(page.locator(no).first()).toBeVisible();
    const noBg = await styleOf(page, no, 'backgroundColor');
    const noInk = await styleOf(page, no, 'color');
    expect(noBg, 'a signer colour, not the theme').not.toBe(primary);
    expect(noInk, 'the number’s ink comes from its own ground, not from --fe-text-on-primary').toBe('rgb(255, 255, 255)');
    expect(noInk).not.toBe(onPrimary);
  });

  test('an OPEN app page turns with the window, both ways', async ({ page }) => {
    await page.emulateMedia({ colorScheme: 'dark' });
    await loginAs(page);
    await page.goto(`/admin/apps/sign/request?path=${encodeURIComponent(QUALIFIED)}`);
    const root = page.getByTestId('plugin-page');
    await expect(root).toHaveClass(/fe--theme-dark/);
    await expect.poll(() => tokenColor(page, '[data-testid="plugin-page"]', '--fe-bg')).toBe(rgb(DARK_BG));

    await page.emulateMedia({ colorScheme: 'light' });
    await expect(root, 'the window turned light; the page open in it must too').toHaveClass(/fe--theme-light/);
    await expect.poll(() => tokenColor(page, '[data-testid="plugin-page"]', '--fe-bg')).toBe(rgb(LIGHT_BG));

    await page.emulateMedia({ colorScheme: 'dark' });
    await expect(root).toHaveClass(/fe--theme-dark/);
    await expect.poll(() => tokenColor(page, '[data-testid="plugin-page"]', '--fe-bg')).toBe(rgb(DARK_BG));
  });

  test('an OPEN public page turns with the window, both ways', async ({ browser, baseURL }) => {
    const share = await admin.post('/api/files/share', { data: { path: QUALIFIED, kind: 'file' } });
    expect(share.ok(), `share: ${share.status()}`).toBe(true);
    const body = (await share.json()) as { token?: string; share?: { token?: string } };
    const token = body.share?.token ?? body.token;
    expect(token, 'a share token').toBeTruthy();

    // A stranger: no session, so the operating system decides.
    const ctx = await browser.newContext({ baseURL, colorScheme: 'dark', serviceWorkers: 'block' });
    const page = await ctx.newPage();
    try {
      await page.goto(`/s/${token}`);
      const root = page.getByTestId('public-page');
      await expect(root).toHaveClass(/fe--theme-dark/);
      await page.emulateMedia({ colorScheme: 'light' });
      await expect(root, 'opened dark, the OS turned light: the page must follow').toHaveClass(/fe--theme-light/);
      await page.emulateMedia({ colorScheme: 'dark' });
      await expect(root).toHaveClass(/fe--theme-dark/);
    } finally {
      await ctx.close();
    }
  });
});
