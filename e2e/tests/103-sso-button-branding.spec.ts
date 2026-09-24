/**
 * 103-sso-button-branding — issues #28 and #29, measured on the real sign-in
 * page in both themes.
 *
 *   #28 "Add option to set custom name for OIDC login button"
 *   #29 "If I select OIDC login button black color almost disappearing in
 *        background of login frame … Also applies to white color for button it
 *        completely invisible" — the reporter preferred "different design for
 *        dark theme and light theme".
 *
 * The capabilities answer is stubbed to name `oidc`, so the SSO button is drawn;
 * it is never pressed. What is measured is what a person sees: the label on the
 * fill must be readable, and the button must stand out from the card of the
 * theme it is shown in (either the fill itself does, or its edge does).
 */
import { test, expect, type APIRequestContext, type Page } from '@playwright/test';
import { apiLogin, dismissInstallBanner } from '../helpers/auth';

const LABEL = 'Şirket hesabıyla giriş';

type RGB = [number, number, number];

function parseColor(css: string): RGB {
  const m = css.match(/rgba?\(([^)]+)\)/);
  if (!m) throw new Error(`unparsed colour ${css}`);
  const [r, g, b] = m[1]!.split(',').map((v) => parseFloat(v));
  return [r!, g!, b!];
}

function alphaOf(css: string): number {
  const m = css.match(/rgba\([^,]+,[^,]+,[^,]+,\s*([\d.]+)\)/);
  return m ? parseFloat(m[1]!) : 1;
}

/** Blend a possibly translucent colour over an opaque backdrop. */
function over(css: string, backdrop: RGB): RGB {
  const a = alphaOf(css);
  const c = parseColor(css);
  return [0, 1, 2].map((i) => c[i]! * a + backdrop[i]! * (1 - a)) as RGB;
}

function luminance([r, g, b]: RGB): number {
  const lin = (v: number) => {
    const c = v / 255;
    return c <= 0.03928 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4;
  };
  return 0.2126 * lin(r) + 0.7152 * lin(g) + 0.0722 * lin(b);
}

function contrast(a: RGB, b: RGB): number {
  const la = luminance(a);
  const lb = luminance(b);
  return (Math.max(la, lb) + 0.05) / (Math.min(la, lb) + 0.05);
}

async function setBranding(request: APIRequestContext, body: Record<string, string>) {
  await apiLogin(request);
  const res = await request.patch('/api/admin/settings', { data: body });
  if (!res.ok()) throw new Error(`settings patch failed: ${res.status()} ${await res.text()}`);
}

async function openLogin(page: Page, theme: 'light' | 'dark') {
  await dismissInstallBanner(page);
  await page.addInitScript((mode) => localStorage.setItem('filex.theme', mode), theme);
  await page.route('**/api/**capabilities*', async (route) => {
    const res = await route.fetch();
    const body = await res.json();
    const drivers: string[] = body.auth_drivers ?? [];
    if (!drivers.includes('oidc')) body.auth_drivers = [...drivers, 'oidc'];
    await route.fulfill({ response: res, json: body });
  });
  await page.goto('/admin/login?local=1');
}

async function measure(page: Page) {
  const btn = page.locator('.lg-btn--sso');
  await expect(btn).toBeVisible({ timeout: 15_000 });
  return btn.evaluate((el) => {
    const s = getComputedStyle(el);
    const card = el.closest('.lg-card') as HTMLElement;
    return {
      text: (el.textContent ?? '').trim(),
      fill: s.backgroundColor,
      label: s.color,
      edge: s.borderTopColor,
      card: getComputedStyle(card).backgroundColor,
    };
  });
}

test.describe('SSO button — custom label and a readable accent in both themes (issues #28, #29)', () => {
  test.use({ viewport: { width: 1280, height: 900 } });

  test.afterAll(async ({ request }) => {
    await setBranding(request, { 'branding.accent': '', 'branding.sso_label': '' });
  });

  for (const [theme, accent] of [
    ['dark', '#000000'],
    ['light', '#ffffff'],
    ['dark', '#ffffff'],
    ['light', '#000000'],
  ] as const) {
    test(`${accent} accent on the ${theme} card`, async ({ page, request }) => {
      await setBranding(request, { 'branding.accent': accent, 'branding.sso_label': LABEL });
      await openLogin(page, theme);
      const m = await measure(page);

      expect(m.text, '#28: the operator label is on the button').toBe(LABEL);

      const card = parseColor(m.card);
      const fill = parseColor(m.fill);
      const label = over(m.label, fill);
      expect(contrast(label, fill), `label ${m.label} on fill ${m.fill}`).toBeGreaterThanOrEqual(3);

      const edge = over(m.edge, card);
      const standsOut = Math.max(contrast(fill, card), contrast(edge, card));
      expect(standsOut, `fill ${m.fill} / edge ${m.edge} on card ${m.card}`).toBeGreaterThanOrEqual(1.6);

      await page.locator('.lg-card').screenshot({ path: `test-results/103-sso-${theme}-${accent.slice(1)}.png` });
    });
  }

  test('an empty label falls back to the translated default', async ({ page, request }) => {
    await setBranding(request, { 'branding.accent': '', 'branding.sso_label': '' });
    await openLogin(page, 'light');
    const m = await measure(page);
    expect(m.text).toMatch(/^(Sign in with SSO|SSO ile oturum aç)$/);
  });
});
