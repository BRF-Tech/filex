/**
 * 230-store-storage-plugins - storage plugins from an app store (#215,
 * docs/PLUGINS.md → Installing from a store; filex 0.55), in every engine
 * `E2E_BROWSERS` names, against a real server and a fake store:
 *
 *   1. the store screen's catalog carries the store's storage plugins with
 *      the server's answer for each - whether the store has a build for THIS
 *      server, what the store's plugin validator proved, the row's state - and
 *      the sentence the Storage tab says first;
 *   2. the screen: the Storage plugins tab lists them apart from the apps,
 *      shows that sentence, says of a plugin with no build for this server
 *      that it has none and offers no request for it;
 *   3. a person asks for a storage plugin: the row is "asked for", in the
 *      server's state;
 *   4. an install link for a plugin the store has no build for here: the
 *      page says the server's sentence (which builds there are) and opens no
 *      review;
 *   5. an install link whose release feed cannot be read: the server's
 *      sentence about the FEED - not the app wording for an unreachable store;
 *   6. a license asked of a storage plugin that is not installed: a code and
 *      a sentence.
 *
 * What it does not do: install a build. The review reads the release's feed
 * over https and the install starts a native program; both are proved by the
 * Go tests with a real plugin build (handlers/app_store_storage_test.go).
 *
 * The store is a small HTTP server here, signing exactly as a store must
 * (ed25519 over the lower-hex sha256 of the canonical JSON, or of the index's
 * bytes as served).
 */
import { test, expect, type APIRequestContext, type Page } from '@playwright/test';
import { createServer, type Server } from 'node:http';
import { createHash, generateKeyPairSync, sign, type KeyObject } from 'node:crypto';
import type { AddressInfo } from 'node:net';
import { loginAs } from '../helpers/auth';
import { newAuthedRequest } from '../helpers/seed';

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

function signHex(k: StoreKey, sha: string): string {
  return sign(null, Buffer.from(sha, 'utf8'), k.priv).toString('hex');
}

function envelope(k: StoreKey, payload: Record<string, unknown>) {
  return { payload, key_id: k.id, signature: signHex(k, sha256hex(Buffer.from(canonical(payload), 'utf8'))) };
}

const iso = (d: Date) => d.toISOString().replace(/\.\d+Z$/, 'Z');

/** The platforms a store may pin a storage plugin's build for. */
const PLATFORMS = ['linux/amd64', 'linux/arm64', 'windows/amd64', 'windows/arm64', 'darwin/amd64', 'darwin/arm64'];
/** A platform no filex runs on: the plugin the store has no build of for here. */
const ELSEWHERE = 'plan9/amd64';

interface CatalogRowAnswer {
  name: string;
  kind: string;
  state: string;
  permissions: string[];
  storage?: { platform: string; for_here: boolean; summary: string; capabilities: Array<{ id: string; label: string }> };
}

interface CatalogAnswer {
  storage_note?: string;
  apps: CatalogRowAnswer[];
}

/* ── the spec ───────────────────────────────────────────────────────────── */

test.describe.serial('Storage plugins from an app store', () => {
  let api: APIRequestContext;
  let store: Server;
  let origin = '';
  let filexOrigin = '';
  let here = '';
  let far = '';
  let app = '';
  /** The store screen's settings before this spec, put back after. */
  let viewBefore: Record<string, unknown> | null = null;
  const idx = newKey('idx-230', 'index');
  const lic = newKey('lic-230', 'license');
  const intents = new Map<string, Record<string, unknown>>();
  const feedSha = sha256hex('{"name":"feed"}');

  const conformance = {
    platform: 'linux/amd64', filex: '0.55.0', verified: true, passed: 67, failed: 0, skipped: 12,
    driver: 'herefs', capabilities: ['write', 'delete', 'mkdir'],
  };

  function buildsFor(name: string, platforms: string[]) {
    return Object.fromEntries(
      platforms.map((p) => [
        p,
        {
          url: `https://github.com/acme/${name}/releases/download/v1.0.0/${name}-${p.replace('/', '-')}`,
          sha256: sha256hex(`${name}@${p}`),
          size: 1024,
          sig: '',
        },
      ]),
    );
  }

  function indexBody(): Buffer {
    const now = new Date();
    const entry = (name: string, platforms: string[]) => ({
      name, kind: 'storage', publisher: 'acme', repo: `acme/${name}`, categories: [],
      label: { en: `Storage ${name}` }, summary: { en: 'A storage plugin' }, latest: '1.0.0',
      versions: [
        {
          version: '1.0.0', filex: '>=0.55.0', published_at: iso(now), manifest: { sha256: feedSha }, permissions: [],
          binaries: Object.fromEntries(platforms.map((p) => [p, { sha256: sha256hex(`${name}@${p}`) }])),
          conformance,
        },
      ],
    });
    const doc = {
      schema: 1, serial: 1, generated_at: iso(now), expires_at: iso(new Date(now.getTime() + 24 * 3600_000)),
      keys: [], publishers: [{ id: 'acme', name: 'Acme', github: 'acme', verified: true, official: false }],
      apps: [
        entry(here, PLATFORMS),
        entry(far, [ELSEWHERE]),
        {
          name: app, kind: 'app', publisher: 'acme', repo: `acme/${app}`, categories: [],
          label: { en: `App ${app}` }, summary: { en: 'An app' }, latest: '1.0.0',
          versions: [
            {
              version: '1.0.0', filex: '>=0.52.0', published_at: iso(now), manifest: { sha256: sha256hex(app) },
              ui: { sha256: sha256hex(`${app}-ui`) }, permissions: ['files:read'],
            },
          ],
        },
      ],
    };
    return Buffer.from(JSON.stringify(doc), 'utf8');
  }

  /** A signed install link for a storage plugin; its builds and feed as given. */
  function storageLink(token: string, name: string, builds: Record<string, unknown>, feedURL: string): string {
    intents.set(token, {
      store: origin, token_id: `tid-${token}`, app: name, kind: 'storage', version: '1.0.0',
      repo: `acme/${name}`, ref: 'v1.0.0', commit: createHash('sha1').update(`${name}@1.0.0`).digest('hex'),
      filex_origin: filexOrigin, manifest_sha256: feedSha, feed_url: feedURL, binaries: builds, conformance,
      filex_range: '>=0.55.0', paid: false, expires_at: iso(new Date(Date.now() + 30 * 60_000)),
    });
    return `/admin/store-install#store=${encodeURIComponent(origin)}&intent=${token}`;
  }

  async function catalog(page: Page): Promise<CatalogAnswer> {
    const res = await page.request.get(`/api/app-store/catalog?store=${encodeURIComponent(origin)}`);
    expect(res.status(), await res.text()).toBe(200);
    return (await res.json()) as CatalogAnswer;
  }

  test.beforeAll(async ({ playwright, baseURL }, info) => {
    filexOrigin = new URL(baseURL ?? 'http://127.0.0.1').origin;
    const tag = info.project.name.replace(/[^a-z]/g, '').slice(0, 8) || 'x';
    here = `herefs-${tag}`;
    far = `farfs-${tag}`;
    app = `shelfapp-${tag}`;
    store = createServer((req, res) => {
      const url = req.url ?? '';
      const send = (code: number, body: unknown) => {
        res.writeHead(code, { 'Content-Type': 'application/json' }).end(JSON.stringify(body));
      };
      req.resume();
      req.on('end', () => {
        if (req.method === 'GET' && url === '/v1/keys.json') {
          send(200, { keys: [idx, lic].map((k) => ({ id: k.id, use: k.use, ed25519: k.pub.toString('hex'), status: 'active' })) });
          return;
        }
        if (req.method === 'GET' && url === '/v1/index.json') {
          res.writeHead(200, { 'Content-Type': 'application/json' }).end(indexBody());
          return;
        }
        if (req.method === 'GET' && url === '/v1/index.json.sig') {
          res.writeHead(200, { 'Content-Type': 'text/plain' }).end(signHex(idx, sha256hex(indexBody())));
          return;
        }
        const done = url.match(/^\/v1\/install\/([^/]+)\/complete$/);
        if (req.method === 'POST' && done) {
          res.writeHead(204).end();
          return;
        }
        const read = url.match(/^\/v1\/install\/([^/]+)$/);
        if (req.method === 'GET' && read) {
          const payload = intents.get(read[1]);
          if (!payload) return send(404, { error: 'not_found' });
          send(200, envelope(idx, payload));
          return;
        }
        send(404, { error: 'not_found' });
      });
    });
    await new Promise<void>((ok) => store.listen(0, '127.0.0.1', ok));
    origin = `http://127.0.0.1:${(store.address() as AddressInfo).port}`;
    api = await newAuthedRequest(playwright, baseURL ?? '');

    // Trusted with the keys it publishes, and on the store screen for everyone.
    // ⚠ As the server spells them (appstore.Fingerprints): `use:id:sha256`,
    // the keys the administrator was shown - a bare sha256 is refused with
    // store_key_changed.
    const fingerprints = [idx, lic].map((k) => `${k.use}:${k.id}:${sha256hex(k.pub)}`).sort();
    const trusted = await api.post('/api/admin/app-plugins/stores', { data: { store: origin, fingerprints } });
    expect(trusted.status(), await trusted.text()).toBe(200);
    const view = await api.get('/api/admin/app-plugins/store-view');
    expect(view.status(), await view.text()).toBe(200);
    viewBefore = ((await view.json()) as { settings: Record<string, unknown> }).settings;
    const put = await api.put('/api/admin/app-plugins/store-view', {
      data: { settings: { enabled: true, stores: [origin], audience: 'everyone', roles: [], groups: [] } },
    });
    expect(put.status(), await put.text()).toBe(200);
  });

  test.afterAll(async () => {
    const list = await api.get('/api/admin/plugin-requests');
    if (list.ok()) {
      const body = (await list.json()) as { requests?: Array<{ id: number; name: string; status: string }> };
      for (const r of body.requests ?? []) {
        if (r.name === here && r.status === 'pending') {
          await api.post(`/api/admin/plugin-requests/${r.id}/reject`, { data: { reason: 'e2e 230 cleanup' } });
        }
      }
    }
    if (viewBefore) await api.put('/api/admin/app-plugins/store-view', { data: { settings: viewBefore } });
    await api.delete(`/api/admin/app-plugins/stores?store=${encodeURIComponent(origin)}`);
    await api.dispose();
    await new Promise<void>((ok) => store.close(() => ok()));
  });

  test('the catalog says, for each storage plugin, whether there is a build for this server and what the store proved', async ({ page }) => {
    await loginAs(page);
    const c = await catalog(page);
    expect(c.storage_note ?? '', 'the Storage tab says first what a storage plugin is').not.toBe('');
    const byName = new Map(c.apps.map((a) => [a.name, a]));
    const h = byName.get(here);
    const f = byName.get(far);
    expect(h?.kind).toBe('storage');
    expect(h?.state).toBe('none');
    expect(h?.permissions).toEqual([]);
    expect(h?.storage?.for_here, 'the store pinned a build for every platform filex runs on').toBe(true);
    expect(h?.storage?.platform).toMatch(/^[a-z0-9]+\/[a-z0-9]+$/);
    expect(h?.storage?.summary).toContain('67');
    expect(h?.storage?.summary).toContain('linux/amd64');
    expect(h?.storage?.capabilities.map((x) => x.id)).toEqual(['write', 'delete', 'mkdir']);
    expect(f?.storage?.for_here).toBe(false);
    expect(f?.storage?.summary).toContain(h?.storage?.platform ?? '?');
    expect(byName.get(app)?.kind).toBe('app');
    expect(byName.get(app)?.storage, 'an app has no storage part').toBeUndefined();
  });

  // The store screen is a route of the panel (web router `app-store`, under
  // /admin/); `/app-store` is no page of filex's and answers 404.
  test('the screen lists storage plugins on their own tab, and offers no request for one with no build here', async ({ page }) => {
    await page.addInitScript(() => localStorage.setItem('filex.tourDone', '1'));
    await loginAs(page);
    await page.goto('/admin/app-store');
    await expect(page.getByTestId('store-screen')).toBeVisible();
    await expect(page.getByTestId(`store-app-${app}`)).toBeVisible();
    await expect(page.getByTestId(`store-app-${here}`), 'the Apps tab lists no storage plugin').toHaveCount(0);
    await expect(page.getByTestId('store-screen-storage-note')).toHaveCount(0);

    await page.getByTestId('store-screen-kind-storage').click();
    await expect(page.getByTestId('store-screen-storage-note')).toBeVisible();
    await expect(page.getByTestId(`store-app-${here}`)).toBeVisible();
    await expect(page.getByTestId(`store-app-${far}`)).toBeVisible();
    await expect(page.getByTestId(`store-app-${app}`), 'the Storage tab lists no app').toHaveCount(0);
    await expect(page.getByTestId(`store-app-checks-${here}`)).toContainText('67');
    await expect(page.getByTestId(`store-app-checks-${far}`)).toHaveClass(/text-rose-600/);

    await page.getByTestId(`store-app-actions-${far}`).click();
    await expect(page.getByTestId(`store-app-actions-${far}-details`)).toBeVisible();
    await expect(page.getByTestId(`store-app-actions-${far}-request`), 'no build here: nothing to ask for').toHaveCount(0);
    await page.keyboard.press('Escape');
    await expect(page.getByTestId(`store-app-actions-${far}-details`)).toHaveCount(0);

    await page.getByTestId(`store-app-actions-${here}`).click();
    await expect(page.getByTestId(`store-app-actions-${here}-request`)).toBeVisible();
  });

  test('a person asks for a storage plugin; the row is asked for, in the server’s state', async ({ page }) => {
    await page.addInitScript(() => localStorage.setItem('filex.tourDone', '1'));
    await loginAs(page);
    await page.goto('/admin/app-store');
    await page.getByTestId('store-screen-kind-storage').click();
    await page.getByTestId(`store-app-actions-${here}`).click();
    await page.getByTestId(`store-app-actions-${here}-request`).click();
    await expect(page.getByTestId('store-app-detail')).toBeVisible();
    await expect(page.getByTestId('store-app-checks')).toContainText('67');
    await page.locator('textarea[name="store-request-reason"]').fill('We keep our archive on this backend.');
    await page.getByTestId('store-request-send').click();
    await expect(page.getByTestId('store-app-detail')).toHaveCount(0);

    await expect.poll(async () => (await catalog(page)).apps.find((a) => a.name === here)?.state).toBe('pending');
    const mine = await page.request.get('/api/app-store/requests');
    expect(mine.status()).toBe(200);
    const asked = ((await mine.json()) as { requests: Array<{ name: string; kind: string; status: string }> }).requests.find((r) => r.name === here);
    expect(asked?.kind).toBe('storage');
    expect(asked?.status).toBe('pending');
  });

  test('a link for a plugin the store has no build of for this server: the server says which builds there are, no review', async ({ page }) => {
    await loginAs(page);
    await page.goto(storageLink('tok230-nobuild-0001', far, buildsFor(far, [ELSEWHERE]), `https://github.com/acme/${far}/releases/download/v1.0.0/filex-storage.json`));
    const error = page.getByTestId('store-install-error');
    await expect(error).toBeVisible();
    await expect(error).toContainText(far);
    await expect(error).toContainText(ELSEWHERE);
    await expect(page.getByTestId('storage-store-review')).toHaveCount(0);
  });

  test('a link whose release feed cannot be read: the server speaks of the feed, not of an unreachable store', async ({ page }) => {
    await loginAs(page);
    await page.goto(storageLink('tok230-nofeed-0001', here, buildsFor(here, PLATFORMS), `https://127.0.0.1:9/acme/${here}/filex-storage.json`));
    const error = page.getByTestId('store-install-error');
    await expect(error).toBeVisible({ timeout: 30_000 });
    await expect(error).toContainText(/feed|besleme/i);
    await expect(error).toContainText('filex-storage.json');
    await expect(page.getByTestId('storage-store-review')).toHaveCount(0);
  });

  test('the license of a storage plugin that is not installed: a code and the server’s sentence', async () => {
    const res = await api.get(`/api/admin/app-plugins/storage/${here}/license`);
    expect(res.status()).toBe(404);
    const body = (await res.json()) as { error: string; message?: string };
    expect(body.error).toBe('not_found');
    expect(body.message ?? '').toContain(here);
  });
});
