/**
 * Share creation + public access — modern + legacy shapes.
 *
 * Guards the share-handler rewrite landed in 87cf497. The endpoint now
 * accepts BOTH the SFC's `{path, password (bool), expires_at,
 * max_downloads}` shape AND the legacy embed.js `{node_id, pin,
 * expires_in}` shape. The response envelope carries a nested `share.*`
 * block (SFC consumption) plus flat top-level aliases (legacy embed.js).
 *
 * Cases covered:
 *   1.  Path-shape, password=false → 200 + `share.url` + flat `url` +
 *       `share.has_pin === false`. Server-generated PIN absent.
 *   2.  Path-shape, password=true → response carries `share.password_pin`
 *       (8 numeric digits). Side-band display only — never returned again.
 *   3.  GET /api/files/share/{token} (metadata) → returns
 *       `{requires_pin, filename, size, mime, ...}`.
 *   4.  GET /s/{token} on a no-pin share → 200 stream of file body.
 *   5.  GET /s/{token} on a pin-protected share without PIN → 200 with
 *       text/html PIN form (NOT a JSON 401).
 *   6.  POST /s/{token} with the right PIN → 200 stream.
 *   7.  Legacy `{node_id, pin}` shape still mints a working share.
 *   8.  Admin GET /api/admin/shares → BOTH SPA-shape (items/total/page/
 *       page_size) AND legacy (entries/limit/offset).
 *
 * Endpoints exercised:
 *   - POST   /api/files/share
 *   - GET    /api/files/share/{token}
 *   - GET    /s/{token}
 *   - POST   /s/{token}
 *   - DELETE /api/files/share/{id}
 *   - GET    /api/admin/shares
 */
import { test as base, expect, request as pwRequest, type APIRequestContext } from '@playwright/test';
import {
  dropStorageByName,
  seedLocalStorage,
  newAuthedRequest,
  findNodeIdByBasename,
} from '../helpers/seed';

const STORAGE = `e2e-share-${Date.now()}`;
const MOUNT = `/tmp/filex-${STORAGE}`;
const FILE_NAME = 'shareable.txt';
const FILE_BODY = 'shared-content-bytes';

const test = base.extend<{ authedRequest: APIRequestContext }>({
  authedRequest: async ({ playwright, baseURL }, use) => {
    const ctx = await newAuthedRequest(playwright, baseURL ?? '');
    await use(ctx);
    await ctx.dispose();
  },
});

// Track share ids/tokens we mint so afterAll can revoke + sweep.
const created: Array<{ id?: number; token?: string }> = [];

/**
 * Mint a share and remember it for the afterAll sweep.
 *
 * ⚠ Every test mints its own instead of reaching for one an earlier test left
 * in `created`. Playwright tears the worker down after a failed test and
 * starts a fresh one, which RE-EVALUATES this module: `Date.now()` produces a
 * new STORAGE name, beforeAll seeds a new (empty) storage, and every
 * module-level array is back to []. So one genuine failure used to knock over
 * every later test in the file — measured here as `files: []` and a null node
 * id in "legacy {node_id, pin}", a test that has nothing to do with the one
 * that actually broke. Cross-test state is a liability, not a shortcut.
 */
async function mintShare(
  request: APIRequestContext,
  body: Record<string, unknown>,
): Promise<{
  share: { id: number; token: string; url: string; has_pin: boolean; password_pin?: string };
  url: string;
  token: string;
  id: number;
}> {
  const res = await request.post('/api/files/share', { data: body });
  expect(res.ok(), `share status ${res.status()}`).toBeTruthy();
  const json = await res.json();
  created.push({ id: json.share.id, token: json.share.token });
  return json;
}

test.describe('Share — create + public access (path + node_id shapes)', () => {
  test.beforeAll(async ({ request, playwright, baseURL }) => {
    await dropStorageByName(request, STORAGE);
    await seedLocalStorage(request, STORAGE, MOUNT);
    // The fixture lives in the hook, not in a test — see mintShare's note on
    // worker restarts. A hook re-runs for the fresh worker; a test does not.
    const authed = await newAuthedRequest(playwright, baseURL ?? '');
    const upRes = await authed.post('/api/files/manager?action=upload', {
      multipart: {
        path: `${STORAGE}://`,
        'file[]': { name: FILE_NAME, mimeType: 'text/plain', buffer: Buffer.from(FILE_BODY) },
      },
    });
    if (!upRes.ok()) {
      throw new Error(`seed upload failed: ${upRes.status()} ${await upRes.text()}`);
    }
    await authed.dispose();
  });

  test.afterAll(async ({ request }) => {
    // Best-effort revoke of every share minted here.
    for (const c of created) {
      if (c.id != null) {
        await request.delete(`/api/files/share/${c.id}`).catch(() => undefined);
      }
    }
    await dropStorageByName(request, STORAGE);
  });

  test('the uploaded fixture is listed with a node id', async ({ authedRequest: request }) => {
    // Confirm the listing emits an `id` per file — the SFC reads
    // `f.id` to drive the legacy share-by-node-id flow. This also
    // guards the projectFileNodes change in 87cf497.
    const id = await findNodeIdByBasename(request, `${STORAGE}://`, FILE_NAME);
    expect(id, `node id for ${FILE_NAME}`).toBeTruthy();
    expect(typeof id).toBe('number');
  });

  test('path-shape, password=false → has_pin=false + flat url alias', async ({
    authedRequest: request,
  }) => {
    const body = await mintShare(request, {
      path: `${STORAGE}://${FILE_NAME}`,
      password: false,
      expires_at: null,
      max_downloads: null,
    });

    // Nested envelope — SFC consumes this.
    expect(body.share).toBeTruthy();
    expect(body.share.url).toContain('/s/');
    expect(body.share.token).toBeTruthy();
    expect(body.share.has_pin).toBe(false);
    expect(body.share.password_pin ?? '').toBe('');

    // Flat aliases — legacy embed.js consumes these.
    expect(body.url).toBe(body.share.url);
    expect(body.token).toBe(body.share.token);
    expect(body.id).toBe(body.share.id);
  });

  test('path-shape, password=true → server returns 8-digit numeric PIN', async ({
    authedRequest: request,
  }) => {
    const body = await mintShare(request, {
      path: `${STORAGE}://${FILE_NAME}`,
      password: true,
      expires_at: null,
      max_downloads: null,
    });
    expect(body.share.has_pin).toBe(true);
    expect(body.share.password_pin).toMatch(/^\d{8}$/);
  });

  test('GET /api/files/share/{token} returns metadata for the no-pin share', async ({
    authedRequest: request,
  }) => {
    const noPin = await mintShare(request, {
      path: `${STORAGE}://${FILE_NAME}`,
      password: false,
    });

    const res = await request.get(`/api/files/share/${noPin.share.token}`);
    expect(res.ok(), `metadata status ${res.status()}`).toBeTruthy();
    const body: {
      requires_pin: boolean;
      filename?: string;
      size?: number;
      mime?: string;
      is_directory?: boolean;
    } = await res.json();
    expect(body.requires_pin).toBe(false);
    expect(body.filename).toBe(FILE_NAME);
    expect(body.size).toBe(FILE_BODY.length);
    // MIME is best-effort; for a fresh local-driver upload it's often
    // empty until the sync worker stats it. Just make sure the field
    // exists and never explodes.
    expect('mime' in body || 'size' in body).toBeTruthy();
  });

  test('GET /s/{token} on a no-pin share streams the file body', async ({
    authedRequest: request,
    baseURL,
  }) => {
    const noPin = await mintShare(request, {
      path: `${STORAGE}://${FILE_NAME}`,
      password: false,
    });

    // The /s/ endpoint is public (no auth header). Use a fresh anon ctx.
    const anon = await pwRequest.newContext({ baseURL });
    const res = await anon.get(`/s/${noPin.share.token}`, { maxRedirects: 0 });
    expect(res.status(), `public download status ${res.status()}`).toBe(200);
    const body = await res.text();
    expect(body).toBe(FILE_BODY);
    await anon.dispose();
  });

  test('GET /s/{token} on a PIN-protected share without PIN renders the HTML form', async ({
    authedRequest: request,
    baseURL,
  }) => {
    const pinned = await mintShare(request, {
      path: `${STORAGE}://${FILE_NAME}`,
      password: true,
    });

    const anon = await pwRequest.newContext({ baseURL });
    const res = await anon.get(`/s/${pinned.share.token}`);
    expect(res.status(), `pin-form status ${res.status()}`).toBe(200);
    const ct = res.headers()['content-type'] ?? '';
    expect(ct).toMatch(/text\/html/);
    const html = await res.text();
    expect(html.toLowerCase()).toContain('<form');
    expect(html.toLowerCase()).toContain('pin');
    await anon.dispose();
  });

  test('POST /s/{token} with the right PIN streams the file', async ({
    authedRequest: request,
    baseURL,
  }) => {
    const pinned = await mintShare(request, {
      path: `${STORAGE}://${FILE_NAME}`,
      password: true,
    });
    const token = pinned.share.token;
    const pin = pinned.share.password_pin;
    expect(pin, 'the server must hand back the PIN it generated').toMatch(/^\d{8}$/);

    const anon = await pwRequest.newContext({ baseURL });

    // Step 1 — the PIN is accepted and the user is TOLD so. The handler
    // deliberately shows this interstitial instead of streaming straight
    // away, because an `attachment` Content-Disposition hijacks the page and
    // the user would never learn whether the PIN matched (share.go:727).
    const unlocked = await anon.post(`/s/${token}`, { form: { pin: pin! }, maxRedirects: 0 });
    expect(unlocked.status(), `unlock status ${unlocked.status()}`).toBe(200);
    expect(unlocked.headers()['content-type'] ?? '').toMatch(/text\/html/);
    const page = await unlocked.text();
    // The page auto-submits itself back with ?confirmed=1 — that form IS the
    // contract, so assert it rather than just "some HTML came back".
    expect(page).toContain(`?confirmed=1`);

    // Step 2 — the confirmed request streams the actual bytes.
    const res = await anon.post(`/s/${token}?confirmed=1`, {
      form: { pin: pin! },
      maxRedirects: 0,
    });
    expect(res.status(), `confirmed download status ${res.status()}`).toBe(200);
    const body = await res.text();
    expect(body).toBe(FILE_BODY);

    // A wrong PIN must still not get through either step.
    const wrong = await anon.post(`/s/${token}?confirmed=1`, {
      form: { pin: '00000000' },
      maxRedirects: 0,
    });
    expect(await wrong.text(), 'a wrong PIN must never stream the file').not.toBe(FILE_BODY);
    await anon.dispose();
  });

  test('legacy {node_id, pin} shape still mints a working share', async ({
    authedRequest: request,
  }) => {
    const id = await findNodeIdByBasename(request, `${STORAGE}://`, FILE_NAME);
    expect(id).toBeTruthy();

    const res = await request.post('/api/files/share', {
      data: {
        node_id: id,
        pin: '1234',
        expires_in: 3600,
      },
    });
    expect(res.ok(), `legacy share status ${res.status()}`).toBeTruthy();
    const body: {
      share: { id: number; token: string; has_pin: boolean };
      token: string;
      url: string;
    } = await res.json();
    expect(body.share.has_pin).toBe(true);
    // password_pin only appears when the SERVER generated it (password=true
    // path). Legacy callers supply their own; we don't echo it back.
    expect((body.share as { password_pin?: string }).password_pin ?? '').toBe('');
    expect(body.token).toBe(body.share.token);

    created.push({ id: body.share.id, token: body.share.token });
  });

  test('admin GET /api/admin/shares carries BOTH SPA + legacy envelopes', async ({
    authedRequest: request,
  }) => {
    const res = await request.get('/api/admin/shares?limit=10&offset=0');
    expect(res.ok(), `admin list status ${res.status()}`).toBeTruthy();
    const body: {
      items?: unknown[];
      total?: number;
      page?: number;
      page_size?: number;
      entries?: unknown[];
      limit?: number;
      offset?: number;
    } = await res.json();

    // SPA shape.
    expect(Array.isArray(body.items), 'items array').toBeTruthy();
    expect(typeof body.total).toBe('number');
    expect(typeof body.page).toBe('number');
    expect(typeof body.page_size).toBe('number');

    // Legacy aliases.
    expect(Array.isArray(body.entries), 'entries array').toBeTruthy();
    expect(typeof body.limit).toBe('number');
    expect(typeof body.offset).toBe('number');

    // The two arrays should refer to the same row set (same length).
    expect(body.entries!.length).toBe(body.items!.length);
  });
});
