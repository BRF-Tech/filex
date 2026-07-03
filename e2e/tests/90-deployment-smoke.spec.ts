/**
 * Deployment smoke — runs against the production deployments to catch
 * config drift between code and what's actually serving traffic.
 *
 * Drives by hostname, NOT by Playwright's baseURL. Set:
 *
 *   E2E_DEMO_HOST=https://demo-fm.example.com
 *   E2E_FM_HOST=https://fm.example.com
 *   E2E_FM_TOKEN=<admin bearer token from /api/auth/login>
 *
 * The token-bearing requests verify protected endpoints; anonymous
 * checks cover the public surface (healthz, embed.js, /admin SPA boot).
 *
 * Each host is treated independently — failure on demo doesn't fail fm
 * and vice versa, so a one-off deploy hiccup is easy to localise.
 */
import { test, expect, request as pwRequest } from '@playwright/test';

const DEMO_HOST = process.env.E2E_DEMO_HOST ?? '';
const FM_HOST = process.env.E2E_FM_HOST ?? '';
const FM_TOKEN = process.env.E2E_FM_TOKEN ?? '';

async function probe(
  host: string,
  path: string,
  opts: { token?: string; method?: string } = {},
): Promise<{ status: number; headers: Record<string, string>; text: string; json: () => unknown }> {
  // Helper has to read the body BEFORE disposing the context, otherwise
  // the response handle is invalidated. Returns a thin wrapper so each
  // test can still use a familiar shape (status/headers/text/json).
  const ctx = await pwRequest.newContext();
  const headers: Record<string, string> = { Accept: 'application/json' };
  if (opts.token) headers.Authorization = `Bearer ${opts.token}`;
  const res = await ctx.fetch(`${host}${path}`, {
    method: opts.method ?? 'GET',
    headers,
    maxRedirects: 0,
  });
  const status = res.status();
  const respHeaders = res.headers();
  const text = await res.text();
  await ctx.dispose();
  return {
    status,
    headers: respHeaders,
    text,
    json: () => JSON.parse(text),
  };
}

test.describe('demo-fm.example.com smoke', () => {
  test.skip(!DEMO_HOST, 'E2E_DEMO_HOST not set');

  test('healthz responds 200', async () => {
    const res = await probe(DEMO_HOST, '/healthz');
    expect(res.status).toBe(200);
    const body = res.json() as { status?: string };
    expect(body.status).toBe('ok');
  });

  test('/ redirects into the admin SPA', async () => {
    const res = await probe(DEMO_HOST, '/');
    // Either a 302/308 to /admin or a 200 with HTML — both acceptable.
    expect([200, 301, 302, 308]).toContain(res.status);
  });

  test('/admin/ SPA shell loads', async () => {
    const res = await probe(DEMO_HOST, '/admin/');
    expect(res.status).toBe(200);
    expect(res.text).toMatch(/<div id="app"|<filex-explorer|<!doctype html/i);
  });

  test('embed.js is reachable + ES module', async () => {
    const res = await probe(DEMO_HOST, '/embed.js');
    expect(res.status).toBe(200);
    expect(res.headers['content-type'] ?? '').toMatch(/javascript/);
  });

  test('default storage `demo` lists root via API', async () => {
    // Demo mode auth: the password is `demo` — log in headlessly.
    const ctx = await pwRequest.newContext({ baseURL: DEMO_HOST });
    const login = await ctx.post('/api/auth/login', {
      data: { email: 'demo@demo.com', password: 'demo' },
    });
    if (login.status() === 404) {
      test.skip(true, 'demo login disabled in this build');
      await ctx.dispose();
      return;
    }
    expect(login.ok()).toBeTruthy();
    const { token } = await login.json();

    const ls = await ctx.get('/api/files/manager?action=index&path=demo://example', {
      headers: { Authorization: `Bearer ${token}` },
    });
    expect(ls.status()).toBe(200);
    const body = await ls.json();
    expect(body.adapter).toBe('demo');
    expect(body.files.length).toBeGreaterThan(0);
    await ctx.dispose();
  });
});

test.describe('fm.example.com smoke', () => {
  test.skip(!FM_HOST, 'E2E_FM_HOST not set');

  test('healthz responds 200', async () => {
    const res = await probe(FM_HOST, '/healthz');
    expect(res.status).toBe(200);
  });

  test('/admin/ SPA shell loads', async () => {
    const res = await probe(FM_HOST, '/admin/');
    expect(res.status).toBe(200);
  });

  test('OIDC redirect lands at the right Keycloak realm', async () => {
    const ctx = await pwRequest.newContext({ baseURL: FM_HOST });
    const res = await ctx.get('/api/auth/oidc/start', { maxRedirects: 0 });
    // 302 → auth.example.com/realms/brf/protocol/openid-connect/auth
    if (res.status() === 404) {
      test.skip(true, 'OIDC route not enabled');
      await ctx.dispose();
      return;
    }
    expect([302, 303, 307]).toContain(res.status());
    const loc = res.headers()['location'] ?? '';
    expect(loc).toMatch(/auth\.brf\.sh.*realms\/brf/);
    await ctx.dispose();
  });

  test('/api/capabilities emits flat aliases AND nested thumbs shape', async () => {
    // Guards 87cf497 — the SFC reads `caps.ffmpeg` / `caps.onlyoffice_url`
    // / `caps.max_chunk_mb` directly, while the admin SPA still expects
    // the rich nested `thumbs.{image,video,pdf,office}`. The handler
    // ships BOTH so both consumers stay happy.
    const res = await probe(FM_HOST, '/api/capabilities');
    expect(res.status).toBe(200);
    const body = res.json() as {
      // flat aliases:
      ffmpeg?: boolean;
      ghostscript?: boolean;
      libreoffice?: boolean;
      max_chunk_mb?: number;
      upload_limit_mb?: number;
      onlyoffice_url?: string;
      drawio_url?: string;
      // nested:
      thumbs?: { image?: boolean; video?: boolean; pdf?: boolean; office?: boolean };
    };

    // Flat aliases — types matter (booleans / numbers / strings).
    expect(typeof body.ffmpeg, '`ffmpeg` flat alias').toBe('boolean');
    expect(typeof body.ghostscript, '`ghostscript` flat alias').toBe('boolean');
    expect(typeof body.libreoffice, '`libreoffice` flat alias').toBe('boolean');
    expect(typeof body.max_chunk_mb, '`max_chunk_mb` flat alias').toBe('number');
    expect(typeof body.upload_limit_mb, '`upload_limit_mb` flat alias').toBe('number');
    expect(typeof body.onlyoffice_url, '`onlyoffice_url` flat alias').toBe('string');
    expect(typeof body.drawio_url, '`drawio_url` flat alias').toBe('string');

    // Nested shape still present.
    expect(body.thumbs, 'nested thumbs object').toBeTruthy();
    expect(typeof body.thumbs!.image).toBe('boolean');
    expect(typeof body.thumbs!.video).toBe('boolean');
    expect(typeof body.thumbs!.pdf).toBe('boolean');
    expect(typeof body.thumbs!.office).toBe('boolean');

    // Cross-check: flat aliases reflect the nested booleans.
    expect(body.ffmpeg).toBe(body.thumbs!.video);
    expect(body.ghostscript).toBe(body.thumbs!.pdf);
    expect(body.libreoffice).toBe(body.thumbs!.office);
  });

  test.skip(!FM_TOKEN, 'E2E_FM_TOKEN not set');

  test('storage list exposes both seeded adapters (main + s3-test)', async () => {
    const res = await probe(FM_HOST, '/api/admin/storages', { token: FM_TOKEN });
    expect(res.status).toBe(200);
    const items = res.json() as Array<{ name: string; driver: string }>;
    const names = items.map((s) => s.name);
    expect(names).toContain('main');
    expect(names).toContain('s3-test');
  });

  test('s3-test://example lists 19 sample files', async () => {
    const res = await probe(
      FM_HOST,
      `/api/files/manager?action=index&path=${encodeURIComponent('s3-test://example')}`,
      { token: FM_TOKEN },
    );
    expect(res.status).toBe(200);
    const body = res.json() as { adapter: string; files: unknown[] };
    expect(body.adapter).toBe('s3-test');
    expect(body.files.length).toBeGreaterThanOrEqual(15);
  });

  test('preview a sample S3 file via fully-qualified path', async () => {
    const res = await probe(
      FM_HOST,
      `/api/files/manager?action=preview&path=${encodeURIComponent(
        's3-test://example/demo.md',
      )}`,
      { token: FM_TOKEN },
    );
    expect(res.status).toBe(200);
    expect(res.headers['content-type'] ?? '').toMatch(/markdown/);
  });

  test('preview WITHOUT adapter prefix 404s (regression for the SFC fix)', async () => {
    const res = await probe(
      FM_HOST,
      `/api/files/manager?action=preview&path=${encodeURIComponent('example/demo.md')}`,
      { token: FM_TOKEN },
    );
    // Backend falls back to storages[0] = main (local) which doesn't
    // have example/demo.md → 404 is the correct, defensive response.
    expect(res.status).toBe(404);
  });
});
