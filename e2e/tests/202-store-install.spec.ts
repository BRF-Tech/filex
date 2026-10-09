/**
 * 202-store-install - installing an app from a store's install link, and a
 * paid app's license (docs/APP-PLUGINS.md → Installing from a store; filex
 * 0.52.0), in every engine `E2E_BROWSERS` names, against a real server and a
 * fake store:
 *
 *   1. the link's token is taken off the address bar at once; an untrusted
 *      store shows its key fingerprints and nothing is read from its link;
 *   2. trusting it (box ticked, Trust pressed) opens the install review marked
 *      "From store", with the license key by its prefix; Install installs,
 *      the store is told `installed`, the license is checked;
 *   3. the app's own interface reads its license over the bridge
 *      (`license.get`, fx.license.get()) - never the key;
 *   4. the store revokes the license: "Verify now" holds the app (state
 *      Unlicensed, a band on the admin pages), nothing is removed;
 *   5. the same link again is refused;
 *   6. a signed-out administrator signs in and comes back to the link, whose
 *      token was in no URL on the way; closing the review tells the store
 *      `cancelled`;
 *   7. a repository that serves other bytes than the store approved opens no
 *      review;
 *   8. (store fe review, filex 0.52.0) the review cannot be closed while
 *      Install or Upgrade is on its way - no ×, Escape and the backdrop do
 *      nothing - the store hears one ending and the page says installed;
 *   9. a second link opened in a tab already on the store page (a
 *      same-document fragment navigation, no page load) leaves the address
 *      bar and is read; with the session gone it goes into no sign-in
 *      address and the page comes back to it after the sign-in;
 *  10. a sign-in that ends on the panel's front door (as a single sign-on
 *      does: the server takes no return address) comes back to the link;
 *  11. the path in another letter case is the same page: the token leaves
 *      the address bar there too;
 *  12. a link the store made for another filex (`filex_origin`) is refused,
 *      and the page says for which filex it was made.
 *
 * The store is a small HTTP server here, signing exactly as a store must
 * (ed25519 over the lower-hex sha256 of the canonical JSON, Node's crypto);
 * the app's repository is the fake GitHub e2e/run.mjs starts
 * (FILEX_APP_GITHUB_RAW_BASE, E2E_FAKE_GITHUB_DIR).
 */
import { test, expect, type APIRequestContext, type Page } from '@playwright/test';
import { createServer, type Server } from 'node:http';
import { createHash, generateKeyPairSync, sign, type KeyObject } from 'node:crypto';
import { mkdirSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import type { AddressInfo } from 'node:net';
import { ADMIN_EMAIL, ADMIN_PASSWORD, apiLogin, dismissInstallBanner, loginAs } from '../helpers/auth';
import { dropStorageByName, newAuthedRequest, seedLocalStorage, storageRoot } from '../helpers/seed';
import { zip } from '../helpers/zip';

const GH_DIR = process.env.E2E_FAKE_GITHUB_DIR ?? '';

/* ── signing as a store must ─────────────────────────────────────────────── */

function canonical(v: unknown): string {
  if (v === null || typeof v !== 'object') return JSON.stringify(v);
  if (Array.isArray(v)) return '[' + v.map(canonical).join(',') + ']';
  const o = v as Record<string, unknown>;
  return '{' + Object.keys(o).sort().map((k) => JSON.stringify(k) + ':' + canonical(o[k])).join(',') + '}';
}

const sha256hex = (b: Buffer | string) => createHash('sha256').update(b).digest('hex');

interface StoreKey {
  id: string;
  use: 'index' | 'license';
  priv: KeyObject;
  pub: Buffer;
}

function newKey(id: string, use: StoreKey['use']): StoreKey {
  const { privateKey, publicKey } = generateKeyPairSync('ed25519');
  const pub = publicKey.export({ format: 'der', type: 'spki' }).subarray(-32);
  return { id, use, priv: privateKey, pub };
}

function envelope(k: StoreKey, payload: Record<string, unknown>) {
  const sig = sign(null, Buffer.from(sha256hex(Buffer.from(canonical(payload), 'utf8')), 'utf8'), k.priv).toString('hex');
  return { payload, key_id: k.id, signature: sig };
}

/* ── the paid app: an interface that reads its license over the bridge ──── */

const APP_JS = `
(async () => {
  const port = await new Promise((resolve, reject) => {
    addEventListener('message', (ev) => {
      if (ev.source === parent && ev.data && ev.data.type === 'filex:port' && ev.ports[0]) resolve(ev.ports[0]);
    });
    parent.postMessage({ type: 'filex:hello', v: 1 }, '*');
    setTimeout(() => reject(new Error('filex did not answer')), 10000);
  });
  let seq = 0;
  const pending = new Map();
  port.onmessage = (ev) => {
    const m = ev.data;
    if (!m || !pending.has(m.id)) return;
    const p = pending.get(m.id);
    pending.delete(m.id);
    if (m.error) p.reject(m.error); else p.resolve(m.result);
  };
  const call = (method, params) => new Promise((resolve, reject) => {
    const id = ++seq;
    pending.set(id, { resolve, reject });
    port.postMessage({ id, method, params });
  });
  const lic = await call('license.get');
  document.body.dataset.license = JSON.stringify(lic);
  document.body.dataset.ready = '1';
})().catch((e) => { document.body.dataset.error = String((e && (e.message || e.code)) || e); });
`;

const UI = zip({
  'index.html': '<!doctype html><html><head><meta charset="utf-8"><script src="app.js" defer></script></head><body><h1>licensed</h1></body></html>',
  'app.js': APP_JS,
});

const LICENSE_KEY = 'FXL-E2E1-KEY0-0202';

/* ── the spec ───────────────────────────────────────────────────────────── */

test.describe.serial('Installing from a store, and a paid app’s license', () => {
  test.skip(!GH_DIR, 'needs the fake GitHub e2e/run.mjs starts (E2E_FAKE_GITHUB_DIR)');

  let api: APIRequestContext;
  let store: Server;
  let origin = '';
  let tag = '';
  let app = '';
  let ext = '';
  let storage = '';
  let manifestBytes = Buffer.alloc(0);
  const idx = newKey('idx-e2e', 'index');
  const lic = newKey('lic-e2e', 'license');
  const intents = new Map<string, { payload: Record<string, unknown>; status?: number }>();
  const completions: Array<{ token: string; result: string; instance_id: string }> = [];
  const checks: Array<{ key: string; app: string; instance_id: string }> = [];
  let licenseResult = 'valid';
  /** How long the store takes to answer a license check (an install on its way). */
  let verifyDelayMs = 0;
  let intentHits = 0;
  let keysHits = 0;
  let filexOrigin = '';

  function publishRepo(version: string, extra: Record<string, unknown> = {}, name = app) {
    const m = {
      manifest_version: 1,
      name,
      version,
      label: { en: 'Licensed viewer', tr: 'Lisanslı görüntüleyici' },
      permissions: ['files:read'],
      ui: { bundle: { url: 'ui.zip', sha256: sha256hex(UI) } },
      views: [{ id: 'view', placement: 'viewer', ui: 'index.html', label: { en: 'Licensed viewer' }, applies: { ext: [ext] } }],
      ...extra,
    };
    const bytes = Buffer.from(JSON.stringify(m));
    // The release, under its tag and under its commit: a link names both,
    // and the manifest is the same bytes at either.
    for (const ref of [`v${version}`, commitOf(version, name)]) {
      const dir = join(GH_DIR, 'Owner', name, ref);
      mkdirSync(dir, { recursive: true });
      writeFileSync(join(dir, 'filex-app.json'), bytes);
      writeFileSync(join(dir, 'ui.zip'), UI);
    }
    return bytes;
  }

  /** The commit a release of this app sits on (one per version, 40 hex). */
  function commitOf(version: string, name = app): string {
    return createHash('sha1').update(`${name}@${version}`).digest('hex');
  }

  function link(token: string, version: string, pinned: Buffer, paid = true, name = app) {
    const payload: Record<string, unknown> = {
      store: origin, token_id: `tid-${token}`, app: name, kind: 'app', version,
      repo: `Owner/${name}`, ref: `v${version}`, commit: commitOf(version, name), filex_origin: filexOrigin,
      manifest_sha256: sha256hex(pinned), ui_sha256: sha256hex(UI), permissions: ['files:read'],
      filex_range: '>=0.52.0', paid, expires_at: new Date(Date.now() + 30 * 60_000).toISOString().replace(/\.\d+Z$/, 'Z'),
    };
    if (paid) payload.license_key = LICENSE_KEY;
    intents.set(token, { payload });
    return `/admin/store-install#store=${encodeURIComponent(origin)}&intent=${token}`;
  }

  test.beforeAll(async ({ playwright, baseURL }, info) => {
    // The filex a link is for: the store signs it, filex refuses another's.
    filexOrigin = new URL(baseURL ?? 'http://127.0.0.1').origin;
    tag = info.project.name.replace(/[^a-z]/g, '').slice(0, 8) || 'x';
    app = `paid-${tag}`;
    ext = `lic${tag.slice(0, 3)}`;
    storage = `e2e-store-${tag}-${Date.now()}`;
    store = createServer((req, res) => {
      const url = req.url ?? '';
      const send = (code: number, body: unknown) => {
        res.writeHead(code, { 'Content-Type': 'application/json' }).end(JSON.stringify(body));
      };
      let raw = '';
      req.on('data', (c) => (raw += c));
      req.on('end', () => {
        if (req.method === 'GET' && url.startsWith('/frame?')) {
          // A page on ANOTHER origin that frames a store link on filex.
          const target = decodeURIComponent(url.slice('/frame?'.length));
          res.writeHead(200, { 'Content-Type': 'text/html' }).end(`<!doctype html><iframe id="f" src="${target}" width="800" height="600"></iframe>`);
          return;
        }
        if (req.method === 'GET' && url === '/v1/keys.json') {
          keysHits++;
          send(200, { keys: [idx, lic].map((k) => ({ id: k.id, use: k.use, ed25519: k.pub.toString('hex'), status: 'active' })) });
          return;
        }
        const done = url.match(/^\/v1\/install\/([^/]+)\/complete$/);
        if (req.method === 'POST' && done) {
          const b = JSON.parse(raw || '{}');
          completions.push({ token: done[1], result: b.result, instance_id: b.instance_id });
          res.writeHead(204).end();
          return;
        }
        const read = url.match(/^\/v1\/install\/([^/]+)$/);
        if (req.method === 'GET' && read) {
          intentHits++;
          const e = intents.get(read[1]);
          if (!e) return send(404, { error: 'not_found' });
          send(200, envelope(idx, e.payload));
          return;
        }
        if (req.method === 'POST' && url === '/v1/licenses/verify') {
          const b = JSON.parse(raw || '{}');
          checks.push({ key: b.key, app: b.app, instance_id: b.instance_id });
          const now = new Date();
          const iso = (d: Date) => d.toISOString();
          const answer = envelope(lic, {
            result: licenseResult, app: b.app, licensee: 'Acme Ltd.', seats: 5, seats_used: 1,
            valid_until: iso(new Date(now.getTime() + 365 * 86400_000)), instance_id: b.instance_id,
            checked_at: iso(now), next_check_by: iso(new Date(now.getTime() + 86400_000)),
            grace_until: iso(new Date(now.getTime() + 7 * 86400_000)),
          });
          setTimeout(() => send(200, answer), verifyDelayMs);
          return;
        }
        send(404, { error: 'not_found' });
      });
    });
    await new Promise<void>((ok) => store.listen(0, '127.0.0.1', ok));
    origin = `http://127.0.0.1:${(store.address() as AddressInfo).port}`;
    api = await newAuthedRequest(playwright, baseURL ?? '');
    manifestBytes = publishRepo('1.0.0');
    const mount = `/tmp/filex-${storage}`;
    const root = storageRoot(mount);
    mkdirSync(root, { recursive: true });
    writeFileSync(join(root, `doc.${ext}`), 'a licensed document');
    await seedLocalStorage(api, storage, mount);
  });

  test.afterAll(async () => {
    const list = await api.get('/api/admin/app-plugins');
    if (list.ok()) {
      for (const p of ((await list.json()) as { plugins?: Array<{ id: number; name: string }> }).plugins ?? []) {
        if (p.name === app || p.name === `${app}-n`) await api.delete(`/api/admin/app-plugins/${p.id}`);
      }
    }
    await dropStorageByName(api, storage);
    await api.dispose();
    await new Promise<void>((ok) => store.close(() => ok()));
  });

  const fingerprint = (k: StoreKey) => (sha256hex(k.pub).slice(0, 32).match(/.{4}/g) ?? []).join(' ');

  async function appState(): Promise<string> {
    const list = (await (await api.get('/api/admin/app-plugins')).json()) as { plugins: Array<{ name: string; state: string }> };
    return list.plugins.find((p) => p.name === app)?.state ?? 'absent';
  }

  async function appVersion(name = app): Promise<string> {
    const list = (await (await api.get('/api/admin/app-plugins')).json()) as { plugins: Array<{ name: string; version: string }> };
    return list.plugins.find((p) => p.name === name)?.version ?? 'absent';
  }

  /**
   * Presses Install on the open review with the store's license answer held
   * back, and tries the three ways a dialog closes while the server works:
   * the × (there is none), Escape twice (Chromium closes a dialog on a second
   * Escape whatever its page says, unless the page opens it again), and the
   * backdrop. The review stays, and says what the server answered.
   */
  async function closeWhileInstalling(page: Page) {
    const wizard = page.getByTestId('app-plugin-wizard');
    await expect(wizard).toBeVisible();
    await page.locator('input[name="app-plugin-understand"]').check();
    verifyDelayMs = 6_000;
    const before = checks.length;
    try {
      await page.getByTestId('app-plugin-install').click();
      await expect.poll(() => checks.length, { timeout: 60_000 }).toBeGreaterThan(before);
      await expect(page.getByRole('dialog').getByRole('button', { name: 'Close', exact: true }), 'no × while Install is on its way').toHaveCount(0);
      await page.keyboard.press('Escape');
      await page.keyboard.press('Escape');
      await page.mouse.click(4, 4);
      await expect(wizard, 'the review stays open').toBeVisible();
      await expect(page.getByTestId('store-install-cancelled')).toHaveCount(0);
      await expect(page.getByTestId('app-plugin-done')).toBeVisible({ timeout: 60_000 });
    } finally {
      verifyDelayMs = 0;
    }
    await page.getByTestId('app-plugin-done').getByRole('button').click();
  }

  test('an untrusted store: the token leaves the address bar, the fingerprints are shown, nothing is read', async ({ page }) => {
    await loginAs(page);
    await page.goto(link('tok-first-0001', '1.0.0', manifestBytes));
    await expect(page.getByTestId('store-install-trust')).toBeVisible();
    expect(page.url()).not.toContain('tok-first-0001');
    expect(page.url()).not.toContain('#');
    await expect(page.getByTestId('store-trust-origin')).toHaveText(origin);
    const fps = page.getByTestId('store-trust-fingerprint');
    await expect(fps).toHaveCount(2);
    await expect(fps.nth(0)).toHaveText(fingerprint(idx));
    await expect(fps.nth(1)).toHaveText(fingerprint(lic));
    expect(intentHits, 'nothing was read from the link of an untrusted store').toBe(0);
    await expect(page.getByTestId('store-trust-approve')).toBeDisabled();
  });

  test('trusting it opens the review from the store; Install installs, the store is told, the license holds', async ({ page }) => {
    await loginAs(page);
    await page.goto(link('tok-install-0001', '1.0.0', manifestBytes));
    await page.locator('input[name="store-trust-compared"]').check();
    await page.getByTestId('store-trust-approve').click();
    const wizard = page.getByTestId('app-plugin-wizard');
    await expect(wizard).toBeVisible();
    await expect(page.getByTestId('app-plugin-from-store')).toContainText(origin);
    await expect(page.getByTestId('app-plugin-store-license-prefix')).toContainText('FXL-E2…');
    await expect(wizard).not.toContainText(LICENSE_KEY);
    const install = page.getByTestId('app-plugin-install');
    await expect(install).toBeDisabled();
    await page.locator('input[name="app-plugin-understand"]').check();
    await install.click();
    await expect(page.getByTestId('app-plugin-done')).toBeVisible({ timeout: 60_000 });
    await expect(page.getByTestId('app-plugin-store-held')).toHaveCount(0);
    await page.getByTestId('app-plugin-done').getByRole('button').click();
    await expect(page.getByTestId('store-install-done')).toBeVisible();

    await expect.poll(() => completions.find((c) => c.token === 'tok-install-0001')?.result).toBe('installed');
    expect(completions.find((c) => c.token === 'tok-install-0001')?.instance_id).toMatch(/^fx-[0-9a-f]{32}$/);
    expect(checks.at(-1)).toMatchObject({ key: LICENSE_KEY, app });
    expect(await appState()).toBe('running');
  });

  test('the app reads its own license over the bridge, never the key', async ({ page }) => {
    // The first-use tour opens over the explorer about a second after it
    // mounts and takes the pointer (measured on a slower machine: the double
    // click below hit the tour's backdrop for 10 s). Every other spec that
    // opens the explorer keeps it away the same way.
    await page.addInitScript(() => localStorage.setItem('filex.tourDone', '1'));
    await loginAs(page);
    await page.goto(`/admin/explore?storage=${encodeURIComponent(storage)}`);
    const row = page.locator(`[data-fe-path="${storage}://doc.${ext}"]`);
    await row.first().waitFor();
    await row.getByText(`doc.${ext}`, { exact: true }).dblclick();
    const el = page.locator('iframe[data-testid="app-frame"]');
    await expect(page.locator('.fe-appframe[data-connected="true"]')).toBeVisible({ timeout: 20_000 });
    const frame = (await (await el.elementHandle())!.contentFrame())!;
    await frame.waitForFunction(() => document.body.dataset.ready === '1' || !!document.body.dataset.error);
    expect(await frame.evaluate(() => document.body.dataset.error ?? null)).toBeNull();
    const got = JSON.parse((await frame.evaluate(() => document.body.dataset.license)) ?? '{}');
    expect(got).toMatchObject({ status: 'valid' });
    // The bridge hands the app its status and dates only: not the key, not
    // the licensee (the administrator reads that on the License section).
    expect(Object.keys(got).sort()).toEqual(['status', 'valid_until']);
    expect(JSON.stringify(got)).not.toContain(LICENSE_KEY);
    expect(JSON.stringify(got)).not.toContain('Acme');
  });

  test('a revoked license holds the app, says so on every admin page, and removes nothing', async ({ page }) => {
    licenseResult = 'revoked';
    await loginAs(page);
    await page.goto(`/admin/plugins/apps/${app}`);
    const section = page.getByTestId('app-plugin-license');
    await expect(section).toBeVisible();
    await expect(page.getByTestId('app-plugin-license-prefix')).toHaveText('FXL-E2…');
    await page.getByTestId('app-plugin-license-verify').click();
    await expect(page.getByTestId('app-plugin-license-status')).toContainText('revoked');
    await expect(page.getByTestId('app-plugin-license-held')).toBeVisible();
    expect(await appState()).toBe('unlicensed');
    await page.goto('/admin/dashboard');
    await expect(page.getByTestId('app-license-held-band')).toContainText(app);
    expect(await page.content()).not.toContain(LICENSE_KEY);
    licenseResult = 'valid';
    await page.goto(`/admin/plugins/apps/${app}`);
    await page.getByTestId('app-plugin-license-verify').click();
    await expect(page.getByTestId('app-plugin-license-status')).toContainText('valid');
    expect(await appState()).toBe('running');
  });

  test('someone who is not an administrator: nothing is asked of the store, the token is in no address', async ({ page }) => {
    const email = `store-user-${tag}@test.local`;
    const password = 'StoreUser!2026';
    await api.post('/api/admin/users', { data: { email, password, role: 'user' } });
    // Signed in as that person (loginAs waits for the admin home they never see).
    await page.goto('/admin/login');
    await page.getByLabel(/e-?mail|kullanıcı adı/i).fill(email);
    await page.getByLabel(/password|parola/i).fill(password);
    await page.getByRole('button', { name: 'Sign in', exact: true }).or(page.getByRole('button', { name: 'Oturum aç', exact: true })).first().click();
    await page.waitForURL((u) => !u.pathname.endsWith('/login'));
    const before = { keys: keysHits, intents: intentHits };
    const seen: string[] = [];
    page.on('request', (r) => seen.push(r.url()));
    await page.goto(link('tok-notadmin-0001', '1.0.0', manifestBytes));
    await page.waitForLoadState('networkidle');
    expect(page.url()).not.toContain('tok-notadmin-0001');
    expect(page.url()).not.toContain('/admin/store-install');
    await expect(page.getByTestId('store-install-trust')).toHaveCount(0);
    await expect(page.getByTestId('app-plugin-wizard')).toHaveCount(0);
    expect({ keys: keysHits, intents: intentHits }, 'the store was asked nothing').toEqual(before);
    expect(seen.filter((u) => u.includes('/store-intent')), 'no store request reached filex').toEqual([]);
    // store fe review #3 (P3): nor is the token kept in the tab, where an
    // administrator signing in later would bring it back to life.
    expect(await page.evaluate(() => sessionStorage.getItem('filex.storeLink')), 'nothing kept in the tab').toBeNull();
    // And the app's license route answers status and dates, not who holds it
    // (store review #11).
    const lic = await page.request.get(`/api/files/plugins/license/${app}`);
    expect(lic.status()).toBe(200);
    expect(await lic.text()).not.toContain('licensee');
  });

  test('a store link inside a frame: refused by the header across origins, and by the page itself', async ({ page }) => {
    // Two frames waited out on purpose, in WebKit too.
    test.setTimeout(90_000);
    const res = await api.get('/admin/store-install');
    expect(res.headers()['content-security-policy'] ?? '', 'the page says who may frame it').toContain("frame-ancestors 'self'");
    await loginAs(page);
    const before = { keys: keysHits, intents: intentHits };
    // Another origin frames the link: the browser refuses to draw it.
    const target = `${filexOrigin}${link('tok-framed-0001', '1.0.0', manifestBytes)}`;
    // ⚠ Not the `load` event: a frame the browser refuses keeps WebKit's page
    // from ever firing it.
    await page.goto(`${origin}/frame?${encodeURIComponent(target)}`, { waitUntil: 'domcontentloaded' });
    await page.waitForTimeout(2_000);
    for (const f of page.frames().filter((x) => x !== page.mainFrame())) {
      const drawn = await Promise.race([
        f.locator('[data-testid="store-install-trust"], [data-testid="app-plugin-wizard"]').count(),
        new Promise<number>((ok) => setTimeout(() => ok(0), 3_000)),
      ]);
      expect(drawn, `frame ${f.url()}`).toBe(0);
    }
    // A page of filex itself frames it: filex's pages frame only their apps
    // and editors (`frame-src`), so nothing is drawn either. (The page's own
    // refusal of any frame is held in web/tests/components/storeInstall.test.ts.)
    await page.goto('/admin/dashboard');
    await page.evaluate((src) => {
      const f = document.createElement('iframe');
      f.id = 'store-frame';
      f.src = src;
      document.body.appendChild(f);
    }, link('tok-framed-0002', '1.0.0', manifestBytes));
    await page.waitForTimeout(3_000);
    // ⚠ Not frameLocator: WebKit never finishes entering a frame its CSP
    // refused, and the expect waits out the whole test.
    for (const f of page.frames().filter((x) => x !== page.mainFrame())) {
      const drawn = await Promise.race([
        f.locator('[data-testid="store-install-trust"], [data-testid="app-plugin-wizard"]').count(),
        new Promise<number>((ok) => setTimeout(() => ok(0), 3_000)),
      ]);
      expect(drawn, `frame ${f.url()}`).toBe(0);
    }
    expect({ keys: keysHits, intents: intentHits }, 'the store was asked nothing').toEqual(before);
  });

  test('the same link again is refused', async ({ page }) => {
    await loginAs(page);
    await page.goto(`/admin/store-install#store=${encodeURIComponent(origin)}&intent=tok-install-0001`);
    await expect(page.getByTestId('store-install-error')).toContainText(/already used/i);
  });

  test('signed out: sign in, come back to the link (in no URL on the way); closing the review cancels it', async ({ page }) => {
    const v = '1.1.0';
    const bytes = publishRepo(v);
    const target = link('tok-signin-0001', v, bytes, false);
    // Every address the page went to or asked for: past the link itself,
    // none may carry the token (the sign-in redirect's `?redirect=` least).
    const seen: string[] = [];
    page.on('framenavigated', (f) => {
      if (f === page.mainFrame()) seen.push(f.url());
    });
    page.on('request', (r) => seen.push(r.url()));
    await page.addInitScript(() => {
      try {
        localStorage.setItem('filex.installPrompt.dismissed', '1');
      } catch {
        /* storage blocked */
      }
    });
    await page.goto(target);
    await page.getByLabel(/e-?mail|kullanıcı adı/i).fill(ADMIN_EMAIL);
    await page.getByLabel(/password|parola/i).fill(ADMIN_PASSWORD);
    await page.getByRole('button', { name: 'Sign in', exact: true }).or(page.getByRole('button', { name: 'Oturum aç', exact: true })).first().click();
    await expect(page.getByTestId('app-plugin-wizard')).toBeVisible({ timeout: 30_000 });
    await expect(page.getByTestId('app-plugin-upgrade-diff')).toBeVisible();
    const leaks = seen.filter((u) => u.includes('tok-signin-0001') && !u.endsWith(target));
    expect(leaks, 'the token was in no address after the link').toEqual([]);
    await page.getByRole('dialog').getByRole('button', { name: 'Close', exact: true }).first().click();
    await expect(page.getByTestId('store-install-cancelled')).toBeVisible();
    await expect.poll(() => completions.find((c) => c.token === 'tok-signin-0001')?.result).toBe('cancelled');
  });

  test('a repository serving other bytes than the store approved opens no review', async ({ page }) => {
    const v = '1.2.0';
    publishRepo(v);
    const target = link('tok-pins-0001', v, Buffer.from('{"other":"bytes"}'));
    await loginAs(page);
    await page.goto(target);
    await expect(page.getByTestId('store-install-error')).toContainText('manifest_sha256');
    await expect(page.getByTestId('app-plugin-wizard')).toBeHidden();
  });

  // store fe review #1 (P1): closing the review while Install was on its way
  // told the store `cancelled` and said "Nothing was installed"; the app
  // landed anyway, and an upgrade went through.
  test('a new install: the review cannot be closed while Install is on its way; the store hears one ending, the page says installed', async ({ page }) => {
    const other = `${app}-n`;
    const bytes = publishRepo('1.0.0', {}, other);
    await loginAs(page);
    await page.goto(link('tok-close-new-0001', '1.0.0', bytes, true, other));
    await closeWhileInstalling(page);
    await expect(page.getByTestId('store-install-done')).toContainText(`${other} is installed.`);
    await expect(page.getByTestId('store-install-cancelled')).toHaveCount(0);
    await expect.poll(() => completions.filter((c) => c.token === 'tok-close-new-0001').map((c) => c.result)).toEqual(['installed']);
    await page.waitForTimeout(1_000);
    expect(completions.filter((c) => c.token === 'tok-close-new-0001').map((c) => c.result), 'one ending for one link').toEqual(['installed']);
    expect(await appVersion(other)).toBe('1.0.0');
  });

  test('an upgrade: the review cannot be closed while it is on its way; the page says the version it went to', async ({ page }) => {
    const v0 = await appVersion();
    const bytes = publishRepo('1.3.0');
    await loginAs(page);
    await page.goto(link('tok-close-up-0001', '1.3.0', bytes));
    await expect(page.getByTestId('app-plugin-upgrade-diff')).toBeVisible();
    await closeWhileInstalling(page);
    await expect(page.getByTestId('store-install-done')).toContainText(`${app} is upgraded to 1.3.0.`);
    await expect.poll(() => completions.filter((c) => c.token === 'tok-close-up-0001').map((c) => c.result)).toEqual(['installed']);
    await page.waitForTimeout(1_000);
    expect(completions.filter((c) => c.token === 'tok-close-up-0001').map((c) => c.result), 'one ending for one link').toEqual(['installed']);
    expect(v0).not.toBe('1.3.0');
    expect(await appVersion()).toBe('1.3.0');
  });

  // store fe review #2 (P2): the token stayed in the address bar and in a
  // new history entry, and the page said there was no link.
  test('a second link in a tab already on the store page: off the address bar and read', async ({ page }) => {
    await loginAs(page);
    await page.goto('/admin/store-install');
    await expect(page.getByTestId('store-install-none')).toBeVisible();
    const asked: string[] = [];
    page.on('request', (r) => {
      if (r.url().includes('/store-intent')) asked.push(r.url());
    });
    const target = link('tok-samedoc-0001', '1.4.0', publishRepo('1.4.0'), false);
    await page.evaluate((u) => {
      window.location.href = u;
    }, target);
    await expect(page.getByTestId('app-plugin-wizard')).toBeVisible({ timeout: 30_000 });
    expect(page.url()).not.toContain('tok-samedoc-0001');
    expect(page.url()).not.toContain('#');
    expect(asked.length, 'the link was read').toBeGreaterThan(0);
    // store fe review #8: the upgrade says where the installed app came from.
    await expect(page.getByTestId('app-plugin-store-upgrade-from')).toHaveText(
      `Installed 1.3.0, from the store ${origin} (Owner/${app}) → this link 1.4.0, from the store ${origin} (Owner/${app})`,
    );
    await page.getByRole('dialog').getByRole('button', { name: 'Close', exact: true }).first().click();
    await expect(page.getByTestId('store-install-cancelled')).toBeVisible();
    await expect.poll(() => completions.find((c) => c.token === 'tok-samedoc-0001')?.result).toBe('cancelled');
  });

  test('the session gone, a second link in that tab goes into no sign-in address; the page comes back to it', async ({ page }) => {
    await page.addInitScript(() => {
      try {
        localStorage.setItem('filex.installPrompt.dismissed', '1');
      } catch {
        /* storage blocked */
      }
    });
    await loginAs(page);
    await page.goto('/admin/store-install');
    await expect(page.getByTestId('store-install-none')).toBeVisible();
    // The session ends behind the page's back: no cookie, no bearer.
    await page.context().clearCookies();
    await page.evaluate(() => sessionStorage.removeItem('filex.bearer'));
    const target = link('tok-dropped-0001', '1.5.0', publishRepo('1.5.0'), false);
    const seen: string[] = [];
    page.on('framenavigated', (f) => {
      if (f === page.mainFrame()) seen.push(f.url());
    });
    page.on('request', (r) => seen.push(r.url()));
    await page.evaluate((u) => {
      window.location.href = u;
    }, target);
    await page.waitForURL(/\/admin\/login/, { timeout: 30_000 });
    const back = new URL(page.url()).searchParams.get('redirect') ?? '';
    expect(back, 'the sign-in comes back to the page, not to the link').toBe('/store-install');
    await page.getByLabel(/e-?mail|kullanıcı adı/i).fill(ADMIN_EMAIL);
    await page.getByLabel(/password|parola/i).fill(ADMIN_PASSWORD);
    await page.getByRole('button', { name: 'Sign in', exact: true }).or(page.getByRole('button', { name: 'Oturum aç', exact: true })).first().click();
    await expect(page.getByTestId('app-plugin-wizard')).toBeVisible({ timeout: 30_000 });
    const leaks = seen.filter((u) => u.includes('tok-dropped-0001') && !u.endsWith(target));
    expect(leaks, 'the token was in no address after the link').toEqual([]);
    await page.getByRole('dialog').getByRole('button', { name: 'Close', exact: true }).first().click();
    await expect(page.getByTestId('store-install-cancelled')).toBeVisible();
    await expect.poll(() => completions.find((c) => c.token === 'tok-dropped-0001')?.result).toBe('cancelled');
  });

  // store fe review #5: an SSO sign-in lands on the panel's front door (the
  // server takes no return address), so the link was never come back to.
  test('a sign-in that ends on the front door, as a single sign-on does, comes back to the link', async ({ page }) => {
    await dismissInstallBanner(page);
    const target = link('tok-sso-0001', '1.6.0', publishRepo('1.6.0'), false);
    await page.goto(target);
    await page.waitForURL(/\/admin\/login/);
    expect(page.url()).not.toContain('tok-sso-0001');
    // Signed in outside the form, then the front door - where an SSO
    // round trip ends.
    await apiLogin(page.request);
    await page.goto('/admin/');
    await expect(page.getByTestId('app-plugin-wizard')).toBeVisible({ timeout: 30_000 });
    expect(new URL(page.url()).pathname).toBe('/admin/store-install');
    await page.getByRole('dialog').getByRole('button', { name: 'Close', exact: true }).first().click();
    await expect(page.getByTestId('store-install-cancelled')).toBeVisible();
    await expect.poll(() => completions.find((c) => c.token === 'tok-sso-0001')?.result).toBe('cancelled');
  });

  // store fe review #6 (P4): the router matched /admin/Store-Install, the
  // capture did not, and the token stayed in the address bar.
  test('the path in another letter case: the token leaves the address bar and the link is read', async ({ page }) => {
    await loginAs(page);
    const target = link('tok-case-0001', '1.7.0', publishRepo('1.7.0'), false).replace('/admin/store-install', '/admin/Store-Install');
    await page.goto(target);
    await expect(page.getByTestId('app-plugin-wizard')).toBeVisible({ timeout: 30_000 });
    expect(page.url()).not.toContain('tok-case-0001');
    expect(page.url()).not.toContain('#');
    await page.getByRole('dialog').getByRole('button', { name: 'Close', exact: true }).first().click();
    await expect(page.getByTestId('store-install-cancelled')).toBeVisible();
    await expect.poll(() => completions.find((c) => c.token === 'tok-case-0001')?.result).toBe('cancelled');
  });

  // store fe review #9b: a link signed for another filex (a leaked link, or
  // one made for a colleague's server) opens nowhere else - store review #8
  // on the server, a sentence naming both addresses on the page.
  test('a link the store made for another filex is refused, and the page says for which', async ({ page }) => {
    await loginAs(page);
    const target = link('tok-elsewhere-0001', '1.8.0', publishRepo('1.8.0'), false);
    intents.get('tok-elsewhere-0001')!.payload.filex_origin = 'https://another-filex.example';
    const before = completions.length;
    await page.goto(target);
    await expect(page.getByTestId('store-install-error')).toContainText(
      `This install link was made for another filex (https://another-filex.example); this one is ${filexOrigin}.`,
    );
    await expect(page.getByTestId('app-plugin-wizard')).toBeHidden();
    expect(page.url()).not.toContain('tok-elsewhere-0001');
    expect(completions.length, 'nothing was installed or told').toBe(before);
    expect(await appVersion()).not.toBe('1.8.0');
  });
});
