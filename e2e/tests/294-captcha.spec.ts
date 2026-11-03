/**
 * 294 - the bot check (captcha), in a real browser against a real binary and
 * Cloudflare's own Turnstile test keys (plan §7's gate):
 *
 *   1. the widget is drawn and its token signs the person in;
 *   2. a sign-in without a token is refused (422);
 *   3. a token the provider refuses is refused, the page says the server's
 *      sentence, and the local fallback is NOT offered;
 *   4. the server cannot reach siteverify: 409 with a challenge, the browser
 *      solves it in a worker and sends the sign-in again by itself - and the
 *      time it took is under 3 s;
 *   5. the provider `none`: no widget, the sign-in goes through;
 *
 * and the forms past the sign-in: a link's PIN, a file request's upload (its
 * pass in X-Captcha-Pass), a demo's one-click sign-in, a widget whose script
 * is blocked (the person is told, the check is not skipped), a server set up
 * without a secret (the fallback answers, no widget), and the Bot protection
 * page.
 *
 * ⚠ One filex per scenario (helpers/extraInstance): a test site key hands
 * out the SAME dummy token every time, and a server accepts a token once -
 * the second use is `replayed`. A shared instance would turn scenario order
 * into the result.
 *
 * ⚠ Needs E2E_FILEX_BIN and a route to challenges.cloudflare.com (the widget
 * is Cloudflare's own); a machine without one skips, it does not fail.
 */
import { test, expect, type APIRequestContext, type Page, type Request } from '@playwright/test';
import fs from 'node:fs';
import { ADMIN_EMAIL, ADMIN_PASSWORD, dismissInstallBanner } from '../helpers/auth';
import { bootExtraInstance, type ExtraInstance } from '../helpers/extraInstance';
import { seedLocalStorage } from '../helpers/seed';

/** Cloudflare's public test keys: pass / fail. Not secrets. */
const SITE_PASS = '1x00000000000000000000AA';
const SECRET_PASS = '1x0000000000000000000000000000000AA';
const SECRET_FAIL = '2x0000000000000000000000000000000AA';

const BIN = process.env.E2E_FILEX_BIN ?? '';

let cloudflare: boolean | null = null;
async function cloudflareReachable(): Promise<boolean> {
  if (cloudflare === null) {
    try {
      cloudflare = (await fetch('https://challenges.cloudflare.com/turnstile/v0/api.js', { signal: AbortSignal.timeout(8000) })).ok;
    } catch {
      cloudflare = false;
    }
  }
  return cloudflare;
}

/** A filex with the bot check set by the environment. */
async function boot(label: string, env: Record<string, string>): Promise<ExtraInstance> {
  return bootExtraInstance(BIN, {
    label: `captcha-${label}`,
    env: {
      FILEX_SECRET_KEY: 'e2e-captcha-key-not-a-real-secret',
      CAPTCHA_PROVIDER: 'turnstile',
      CAPTCHA_SITE_KEY: SITE_PASS,
      CAPTCHA_SECRET: SECRET_PASS,
      ...env,
    },
  });
}

/** Skips the whole describe on a machine that cannot run these. */
async function needs(): Promise<void> {
  test.skip(!BIN || !fs.existsSync(BIN), 'no binary to boot a captcha instance with (E2E_FILEX_BIN)');
  test.skip(!(await cloudflareReachable()), 'no route to challenges.cloudflare.com (the widget is Cloudflare’s)');
}

/**
 * Remembers the widget's `data-captcha-solve-ms` the moment it is written:
 * the sign-in that follows navigates away from the form that carried it.
 */
async function recordSolveTime(page: Page): Promise<void> {
  await page.addInitScript(() => {
    new MutationObserver(() => {
      const el = document.querySelector('[data-captcha-solve-ms]');
      if (el) (window as unknown as { __solveMs?: string }).__solveMs = el.getAttribute('data-captcha-solve-ms') ?? '';
    }).observe(document, { subtree: true, attributes: true, childList: true, attributeFilter: ['data-captcha-solve-ms'] });
  });
}

/** The sign-in form on `base`, filled; the caller presses the button. */
async function openLogin(page: Page, base: string): Promise<void> {
  await dismissInstallBanner(page);
  await page.goto(`${base}/admin/login`);
  await page.getByLabel(/e-?mail|kullanıcı adı/i).fill(ADMIN_EMAIL);
  await page.getByLabel(/password|parola/i).fill(ADMIN_PASSWORD);
}

function signInButton(page: Page) {
  return page.getByRole('button', { name: 'Sign in', exact: true });
}

/** Every POST to a path, with its JSON body. */
function posts(page: Page, path: string): Array<{ body: Record<string, unknown>; req: Request }> {
  const seen: Array<{ body: Record<string, unknown>; req: Request }> = [];
  page.on('request', (r) => {
    if (r.method() === 'POST' && new URL(r.url()).pathname === path) {
      let body: Record<string, unknown> = {};
      try {
        body = (r.postDataJSON() ?? {}) as Record<string, unknown>;
      } catch {
        body = {};
      }
      seen.push({ body, req: r });
    }
  });
  return seen;
}

/** A signed-in API context on an instance whose sign-in is NOT protected. */
async function adminApi(playwright: { request: { newContext(o: object): Promise<APIRequestContext> } }, base: string) {
  const ctx = await playwright.request.newContext({ baseURL: base });
  const res = await ctx.post('/api/auth/login', { data: { email: ADMIN_EMAIL, password: ADMIN_PASSWORD } });
  if (!res.ok()) throw new Error(`admin sign-in on ${base}: ${res.status()} ${await res.text()}`);
  return ctx;
}

test.describe('294 sign-in: the widget, and its token signs in', () => {
  let inst: ExtraInstance;
  test.beforeAll(async () => {
    await needs();
    inst = await boot('login', { CAPTCHA_FORMS: 'login' });
  });
  test.afterAll(async () => inst?.stop());

  test('the widget is drawn, solved, and the sign-in carries its token', async ({ page }) => {
    const logins = posts(page, '/api/auth/login');
    await openLogin(page, inst.base);
    const widget = page.getByTestId('captcha');
    await expect(widget).toHaveAttribute('data-captcha-form', 'login');
    await expect(widget).toHaveAttribute('data-captcha-provider', 'turnstile');
    await expect(widget).toHaveAttribute('data-captcha-state', 'solved', { timeout: 30_000 });
    expect(page.frames().some((f) => f.url().includes('challenges.cloudflare.com'))).toBe(true);
    await signInButton(page).click();
    await page.waitForURL(/\/admin\/(home|dashboard)([?#]|$)/);
    expect(logins).toHaveLength(1);
    expect(String(logins[0].body.captcha_token ?? '')).not.toBe('');
  });

  test('a sign-in without a token is refused before any password is looked at (422)', async ({ request }) => {
    const res = await request.post(`${inst.base}/api/auth/login`, { data: { email: ADMIN_EMAIL, password: 'wrong-on-purpose' } });
    expect(res.status()).toBe(422);
    const body = await res.json();
    expect(body.error).toBe('captcha_required');
    expect(typeof body.message).toBe('string');
  });
});

test.describe('294 sign-in: a token the provider refuses', () => {
  let inst: ExtraInstance;
  test.beforeAll(async () => {
    await needs();
    inst = await boot('refused', { CAPTCHA_FORMS: 'login', CAPTCHA_SECRET: SECRET_FAIL });
  });
  test.afterAll(async () => inst?.stop());

  test('is a 422 with the server’s sentence under the widget, and no fallback', async ({ page }) => {
    const challenges = posts(page, '/api/captcha/local/challenge');
    const logins = posts(page, '/api/auth/login');
    await openLogin(page, inst.base);
    await expect(page.getByTestId('captcha')).toHaveAttribute('data-captcha-state', 'solved', { timeout: 30_000 });
    const answered = page.waitForResponse((r) => r.url().endsWith('/api/auth/login') && r.request().method() === 'POST');
    await signInButton(page).click();
    const res = await answered;
    expect(res.status()).toBe(422);
    const said = (await res.json()).message as string;
    await expect(page.getByTestId('captcha-message')).toHaveText(said);
    // still on the form, one attempt, nothing solved locally
    await expect(page).toHaveURL(/\/admin\/login/);
    expect(logins).toHaveLength(1);
    expect(challenges).toHaveLength(0);
    // the form's own alert does not say it a second time
    await expect(page.locator('form#login-form [role="alert"]').filter({ hasText: said })).toHaveCount(0);
  });
});

test.describe('294 sign-in: the server cannot reach siteverify', () => {
  let inst: ExtraInstance;
  test.beforeAll(async () => {
    await needs();
    inst = await boot('outage', { CAPTCHA_FORMS: 'login', CAPTCHA_TURNSTILE_SITEVERIFY: 'http://127.0.0.1:1/' });
  });
  test.afterAll(async () => inst?.stop());

  test('409: the browser solves the challenge and signs in by itself, in under 3 s of work', async ({ page }, info) => {
    await recordSolveTime(page);
    const logins = posts(page, '/api/auth/login');
    await openLogin(page, inst.base);
    await expect(page.getByTestId('captcha')).toHaveAttribute('data-captcha-state', 'solved', { timeout: 30_000 });
    await signInButton(page).click();
    await page.waitForURL(/\/admin\/(home|dashboard)([?#]|$)/, { timeout: 30_000 });
    expect(logins.map((l) => Object.keys(l.body).filter((k) => k.startsWith('captcha_')))).toEqual([['captcha_token'], ['captcha_local']]);
    const ms = Number(await page.evaluate(() => (window as unknown as { __solveMs?: string }).__solveMs ?? 'NaN'));
    info.annotations.push({ type: 'captcha-solve-ms', description: String(ms) });
    console.log(`294: the local fallback took ${ms} ms (${info.project.name})`);
    expect(ms).toBeGreaterThanOrEqual(0);
    expect(ms).toBeLessThan(3000);
  });
});

test.describe('294 sign-in: the provider is none', () => {
  let inst: ExtraInstance;
  test.beforeAll(async () => {
    test.skip(!BIN || !fs.existsSync(BIN), 'no binary to boot a captcha instance with (E2E_FILEX_BIN)');
    // Nothing set: the default, which is off (keys alone switch nothing on).
    inst = await boot('none', { CAPTCHA_PROVIDER: '' });
  });
  test.afterAll(async () => inst?.stop());

  test('no widget, and the sign-in goes through', async ({ page }) => {
    const config = page.waitForResponse((r) => r.url().endsWith('/api/captcha/config'));
    await openLogin(page, inst.base);
    const c = await (await config).json();
    expect(c.provider).toBe('none');
    expect(c.forms).toEqual([]);
    await expect(page.getByTestId('captcha')).toHaveCount(0);
    await signInButton(page).click();
    await page.waitForURL(/\/admin\/(home|dashboard)([?#]|$)/);
  });
});

test.describe('294 sign-in: the widget’s script is blocked here', () => {
  let inst: ExtraInstance;
  test.beforeAll(async () => {
    await needs();
    inst = await boot('blocked', { CAPTCHA_FORMS: 'login' });
  });
  test.afterAll(async () => inst?.stop());

  test('the server can reach the provider, so there is no way around: the person is told', async ({ page }) => {
    await page.route('https://challenges.cloudflare.com/**', (r) => r.abort());
    const answered = page.waitForResponse((r) => r.url().endsWith('/api/captcha/local/challenge'));
    await openLogin(page, inst.base);
    expect((await answered).status()).toBe(409);
    await expect(page.getByTestId('captcha')).toHaveAttribute('data-captcha-state', 'error');
    await expect(page.getByTestId('captcha-message')).toContainText(/could not load|yüklenemedi/);
    const login = page.waitForResponse((r) => r.url().endsWith('/api/auth/login'));
    await signInButton(page).click();
    expect((await login).status()).toBe(422);
    await expect(page).toHaveURL(/\/admin\/login/);
    await expect(page.getByTestId('captcha-message')).toContainText(/could not load|yüklenemedi/);
  });
});

test.describe('294 sign-in: a server set up without a secret', () => {
  let inst: ExtraInstance;
  test.beforeAll(async () => {
    await needs();
    inst = await boot('misconfigured', { CAPTCHA_FORMS: 'login', CAPTCHA_SECRET: '' });
  });
  test.afterAll(async () => inst?.stop());

  test('draws no widget, solves the server’s challenge, and signs in - the check does not switch off', async ({ page }) => {
    const config = page.waitForResponse((r) => r.url().endsWith('/api/captcha/config'));
    const logins = posts(page, '/api/auth/login');
    await openLogin(page, inst.base);
    expect((await (await config).json()).misconfigured).toBe(true);
    await expect(page.getByTestId('captcha')).toHaveAttribute('data-captcha-state', 'solved', { timeout: 30_000 });
    expect(page.frames().some((f) => f.url().includes('challenges.cloudflare.com'))).toBe(false);
    await signInButton(page).click();
    await page.waitForURL(/\/admin\/(home|dashboard)([?#]|$)/, { timeout: 30_000 });
    expect(Object.keys(logins[0].body)).toContain('captcha_local');
  });
});

test.describe('294 a demo’s one-click sign-in', () => {
  let inst: ExtraInstance;
  test.beforeAll(async () => {
    await needs();
    // The demo account is the admin here: a demo's account is the operator's
    // to create (FILEX_DEMO_USER / FILEX_DEMO_PASS name it), not the binary's.
    inst = await boot('demo', {
      CAPTCHA_FORMS: 'demo_login',
      FILEX_DEMO_MODE: '1',
      FILEX_DEMO_USER: ADMIN_EMAIL,
      FILEX_DEMO_PASS: ADMIN_PASSWORD,
    });
  });
  test.afterAll(async () => inst?.stop());

  test('is checked under its button, and opens the explorer', async ({ page }) => {
    const logins = posts(page, '/api/auth/login');
    await dismissInstallBanner(page);
    await page.goto(`${inst.base}/admin/login`);
    const widget = page.getByTestId('captcha');
    await expect(widget).toHaveAttribute('data-captcha-form', 'demo_login');
    await expect(widget).toHaveAttribute('data-captcha-state', 'solved', { timeout: 30_000 });
    await page.getByTestId('demo-open').click();
    await page.waitForURL(/\/explore/, { timeout: 30_000 });
    expect(String(logins[0].body.captcha_token ?? '')).not.toBe('');
  });
});

test.describe('294 a link’s PIN', () => {
  let inst: ExtraInstance;
  let api: APIRequestContext;
  let token = '';
  const STORE = `e2e-captcha-pin-${Date.now()}`;

  test.beforeAll(async ({ playwright }) => {
    await needs();
    inst = await boot('pin', { CAPTCHA_FORMS: 'share_password,file_request' });
    api = await adminApi(playwright, inst.base);
    await seedLocalStorage(api, STORE, `/tmp/filex-${STORE}`);
    const up = await api.post('/api/files/manager?action=upload', {
      multipart: { path: `${STORE}://`, 'file[]': { name: 'teklif.pdf', mimeType: 'application/pdf', buffer: Buffer.from('%PDF-1.4 captcha\n') } },
    });
    expect(up.ok(), `upload ${up.status()} ${await up.text()}`).toBeTruthy();
    const made = await api.post('/api/files/share', { data: { path: `${STORE}://teklif.pdf`, kind: 'file', pin: '4821' } });
    expect(made.ok(), `share ${made.status()} ${await made.text()}`).toBeTruthy();
    token = (await made.json()).share.token;
  });
  test.afterAll(async () => {
    await api?.dispose();
    await inst?.stop();
  });

  test('the check sits between the box and the button, and the PIN carries its token', async ({ page }) => {
    const pins = posts(page, `/api/public/s/${token}/pin`);
    await page.goto(`${inst.base}/s/${token}`);
    const gate = page.getByTestId('public-page-pin');
    await expect(gate).toBeVisible();
    const widget = gate.getByTestId('captcha');
    await expect(widget).toHaveAttribute('data-captcha-form', 'share_password');
    await expect(widget).toHaveAttribute('data-captcha-state', 'solved', { timeout: 30_000 });
    const box = await page.getByTestId('public-page-pin-input').boundingBox();
    const check = await widget.boundingBox();
    const button = await page.getByTestId('public-page-pin-submit').boundingBox();
    expect(check!.y).toBeGreaterThan(box!.y);
    expect(button!.y).toBeGreaterThan(check!.y);
    await page.getByTestId('public-page-pin-input').fill('4821');
    await page.getByTestId('public-page-pin-submit').click();
    await expect(page.getByTestId('public-page')).toHaveAttribute('data-state', 'ready');
    expect(pins).toHaveLength(1);
    expect(pins[0].body.pin).toBe('4821');
    expect(String(pins[0].body.captcha_token ?? '')).not.toBe('');
  });
});

test.describe('294 a file request’s upload, and the Bot protection page', () => {
  let inst: ExtraInstance;
  let api: APIRequestContext;
  let token = '';
  const STORE = `e2e-captcha-drop-${Date.now()}`;

  test.beforeAll(async ({ playwright }) => {
    await needs();
    inst = await boot('drop', { CAPTCHA_FORMS: 'share_password,file_request,demo_login' });
    api = await adminApi(playwright, inst.base);
    await seedLocalStorage(api, STORE, `/tmp/filex-${STORE}`);
    const mk = await api.post('/api/files/manager?action=newfolder', { data: { path: `${STORE}://`, name: 'Gelen' } });
    expect(mk.ok(), `newfolder ${mk.status()}`).toBeTruthy();
    const made = await api.post('/api/files/share', { data: { path: `${STORE}://Gelen`, kind: 'drop', drop_settings: { ask_name: false } } });
    expect(made.ok(), `drop ${made.status()} ${await made.text()}`).toBeTruthy();
    token = (await made.json()).share.token;
  });
  test.afterAll(async () => {
    await api?.dispose();
    await inst?.stop();
  });

  test.describe.configure({ mode: 'serial' });

  test('asks for the pass right before the upload, and the upload carries it', async ({ page }) => {
    const passes = posts(page, `/api/public/d/${token}/captcha`);
    const uploads = posts(page, `/api/public/d/${token}/upload`);
    await page.goto(`${inst.base}/d/${token}`);
    await expect(page.getByTestId('public-request-drop')).toBeVisible();
    const widget = page.getByTestId('public-request').getByTestId('captcha');
    await expect(widget).toHaveAttribute('data-captcha-form', 'file_request');
    await expect(widget).toHaveAttribute('data-captcha-state', 'solved', { timeout: 30_000 });
    await page.getByTestId('public-request-input').setInputFiles([
      { name: 'belge.pdf', mimeType: 'application/pdf', buffer: Buffer.from('%PDF-1.4\n%%EOF\n') },
    ]);
    await expect(page.getByTestId('public-request-uploads').locator('[data-state="done"]')).toHaveCount(1);
    expect(passes).toHaveLength(1);
    expect(uploads).toHaveLength(1);
    expect(String(passes[0].body.captcha_token ?? '')).not.toBe('');
    expect((await uploads[0].req.allHeaders())['x-captcha-pass']).toBeTruthy();
  });

  test('an upload without a pass is refused (422 missing_pass)', async ({ request }) => {
    const res = await request.post(`${inst.base}/api/public/d/${token}/upload`, {
      multipart: { file: { name: 'b.pdf', mimeType: 'application/pdf', buffer: Buffer.from('%PDF-1.4\n') } },
    });
    expect(res.status()).toBe(422);
    expect((await res.json()).captcha).toBe('missing_pass');
  });

  test('the Bot protection page says what is in force and where it came from, and saves only a change', async ({ page }) => {
    const res = await page.request.post(`${inst.base}/api/auth/login`, { data: { email: ADMIN_EMAIL, password: ADMIN_PASSWORD } });
    expect(res.ok()).toBeTruthy();
    await dismissInstallBanner(page);
    await page.goto(`${inst.base}/admin/bot-protection`);
    const root = page.getByTestId('bot-protection');
    await expect(root.getByTestId('bot-protection-state')).toHaveAttribute('data-state', 'on');
    await expect(root.getByTestId('bot-protection-provider-turnstile')).toHaveAttribute('aria-checked', 'true');
    await expect(root).toContainText('From the environment (CAPTCHA_PROVIDER).');
    await expect(root.locator('select')).toHaveCount(0);
    await expect(page.locator('input[name="bot-protection-secret"]')).toHaveValue('');
    const patched = page.waitForRequest((r) => r.method() === 'PATCH' && r.url().endsWith('/api/admin/captcha'));
    await page.locator('#bot-protection-form-demo_login').click();
    await root.getByTestId('bot-protection-save').click();
    expect((await patched).postDataJSON()).toEqual({ forms: ['share_password', 'file_request'] });
    await expect(root).toContainText('Set on this page.');
    const config = await (await page.request.get(`${inst.base}/api/captcha/config`)).json();
    expect(config.forms).toEqual(['share_password', 'file_request']);
  });
});
