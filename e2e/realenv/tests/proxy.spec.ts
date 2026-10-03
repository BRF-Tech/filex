/**
 * realenv proxy - filex behind a REAL Caddy (FILEX_TLS_MODE=proxy, the
 * default; docs/TENANT-ADMIN.md "TLS"). Caddy issues certificates on demand
 * from the realenv Pebble and asks filex two questions:
 *
 *   on_demand_tls ask      GET /api/tls/ask?domain=<name>: certify it?
 *   get_certificate http   GET /api/tls/certificate?server_name=<name>: the
 *                          tenant's own certificate, or 204
 *
 * filex trusts only Caddy as its proxy here (FILEX_TRUSTED_PROXIES = Caddy's
 * address), and the hooks answer the proxy ITSELF only
 * (clientip.ProxyItself): a stranger's request that Caddy forwards, or one
 * that reaches filex directly, learns nothing. stack/Caddyfile does NOT
 * block /api/tls/* on purpose, so it is filex's own guard being measured.
 */
import { test, expect, type APIRequestContext } from '@playwright/test';
import fs from 'node:fs';
import https from 'node:https';
import { X509Certificate } from 'node:crypto';
import { adminApi, dns, env, handshake, logLines, missing, okJSON, pebbleRoot, readLog, until, verifyChain, type Handshake } from '../lib/realenv';

const why = missing('REALENV_TLS', 'tls', 'Pebble, pebble-challtestsrv and Caddy');

async function certified(ip: string, name: string): Promise<Handshake> {
  let last: Handshake | undefined;
  try {
    return await until(
      `a certificate for ${name}`,
      async () => {
        last = await handshake(ip, 5001, name);
        return last.ok ? last : undefined;
      },
      120_000,
      2000,
    );
  } catch (err) {
    throw new Error(`${String(err)}; last handshake: ${last?.error}`);
  }
}

/** A request through Caddy, as anybody on the internet makes it. */
function viaCaddy(name: string, path: string, root: X509Certificate): Promise<{ status: number; body: string }> {
  return new Promise((resolve, reject) => {
    https
      .get(
        { host: env('REALENV_CADDY_IP'), port: 5001, path, servername: name, headers: { Host: `${name}:5001` }, ca: root.toString() },
        (res) => {
          let body = '';
          res.on('data', (d) => (body += d));
          res.on('end', () => resolve({ status: res.statusCode ?? 0, body }));
        },
      )
      .on('error', reject);
  });
}

test.describe('realenv: filex behind a real Caddy that asks it about certificates', () => {
  test.skip(!!why, why);
  test.describe.configure({ mode: 'default' });

  let api: APIRequestContext;
  let root: X509Certificate;
  let caddy = '';

  test.beforeAll(async ({ playwright }) => {
    api = await adminApi(playwright.request, env('REALENV_PROXY_API'));
    root = await pebbleRoot(env('REALENV_PEBBLE_MGMT'));
    caddy = env('REALENV_CADDY_IP');
  });
  test.afterAll(async () => {
    await api?.dispose();
  });

  test('Caddy asks filex first: the platform address is certified, a stranger name is not', async () => {
    await dns.addA('files.proxy.test', caddy);
    await dns.addA('stranger.proxy.test', caddy);
    const h = await certified(caddy, 'files.proxy.test');
    expect(verifyChain(h, root, 'files.proxy.test')).toBe('');
    expect((await viaCaddy('files.proxy.test', '/healthz', root)).status).toBe(200);

    // Caddy asks filex (get_certificate first, then ask) and gets no for the
    // stranger: the 404 in filex's own log, from Caddy's address.
    const flog = `/work/logs/${process.env.REALENV_PREFIX ?? 'fxre'}-fxproxy.log`;
    const refusals = () => logLines(flog, 'path=/api/tls/ask', 'status=404', `ip=${caddy}`).length;
    const before = refusals();
    const s = await handshake(caddy, 5001, 'stranger.proxy.test');
    expect(s.ok, 'Caddy must not certify a name filex does not serve').toBe(false);
    await until('filex refused Caddy', async () => refusals() > before, 10_000);
    // ...and Caddy never asked the authority for it.
    expect(logLines(env('REALENV_PEBBLE_LOG'), 'stranger.proxy.test')).toEqual([]);
    expect(logLines(env('REALENV_CADDY_LOG'), 'stranger.proxy.test', 'obtain'), readLog(env('REALENV_CADDY_LOG')).slice(-2000)).toEqual([]);
  });

  test("a tenant's own certificate is the one Caddy serves (get_certificate http); its other domain is certified by the authority", async () => {
    const tenant = await okJSON<{ id: number }>(
      await api.post('/api/admin/providers', { data: { name: 'Acme Proxy', slug: 'acme', realm: 'acme', enabled: true } }),
      'create tenant acme',
    );
    const q = `?tenant=${tenant.id}`;
    await dns.addA('acme.tenants.proxy.test', caddy);
    const ids: Record<string, number> = {};
    for (const d of ['files.acme-proxy.test', 'docs.acme-proxy.test']) {
      await dns.setCNAME(d, 'acme.tenants.proxy.test.');
      const row = await okJSON<{ id: number }>(await api.post(`/api/admin/tenant/domains${q}`, { data: { domain: d } }), `add ${d}`);
      const checked = (await okJSON<{ domain: { status: string } }>(await api.post(`/api/admin/tenant/domains/${row.id}/check${q}`), `check ${d}`)).domain;
      expect(checked.status, JSON.stringify(checked)).toBe('active');
      ids[d] = row.id;
    }
    const certPem = fs.readFileSync('/work/certs/own-proxy.crt', 'utf8');
    const keyPem = fs.readFileSync('/work/certs/own-proxy.key', 'utf8');
    await okJSON(
      await api.put(`/api/admin/tenant/domains/${ids['files.acme-proxy.test']}/certificate${q}`, { data: { cert_pem: certPem, key_pem: keyPem } }),
      'bring a certificate',
    );

    const own = new X509Certificate(certPem);
    const ca = new X509Certificate(fs.readFileSync('/work/certs/ca.pem'));
    const h = await certified(caddy, 'files.acme-proxy.test');
    expect(h.leaf!.fingerprint256, `Caddy served ${h.leaf!.issuer}`).toBe(own.fingerprint256);
    expect(verifyChain(h, ca, 'files.acme-proxy.test')).toBe('');

    const other = await certified(caddy, 'docs.acme-proxy.test');
    expect(verifyChain(other, root, 'docs.acme-proxy.test')).toBe('');
  });

  test('the hooks answer the proxy only: through Caddy or straight to filex, a stranger learns nothing', async ({ playwright }) => {
    // Through the same proxy that asks: Caddy adds X-Forwarded-For.
    const ask = await viaCaddy('files.proxy.test', '/api/tls/ask?domain=files.proxy.test', root);
    expect(ask.status).toBe(404);
    const key = await viaCaddy('files.proxy.test', '/api/tls/certificate?server_name=files.acme-proxy.test', root);
    expect(key.status).toBe(404);
    expect(key.body).not.toContain('PRIVATE KEY');

    // Straight to filex from an address that is not the proxy.
    const direct = await playwright.request.newContext({ baseURL: env('REALENV_PROXY_API') });
    try {
      expect((await direct.get('/api/tls/ask?domain=files.proxy.test')).status()).toBe(404);
      const d = await direct.get('/api/tls/certificate?server_name=files.acme-proxy.test');
      expect(d.status()).toBe(404);
      expect(await d.text()).not.toContain('PRIVATE KEY');
    } finally {
      await direct.dispose();
    }
  });

  test('with the default trusted list (auto), any container on filex own network counts as the proxy, as the documentation warns', async ({ playwright }) => {
    // docs/TENANT-ADMIN.md, "TLS": keep filex's port off every network but
    // the proxy's, or name the proxy in FILEX_TRUSTED_PROXIES. This pins
    // that warning to what `auto` does on a real container network.
    await okJSON(await api.patch('/api/admin/login-security', { data: { trusted_proxies: ['auto'] } }), 'trusted proxies: auto');
    const direct = await playwright.request.newContext({ baseURL: env('REALENV_PROXY_API') });
    try {
      expect((await direct.get('/api/tls/ask?domain=files.proxy.test')).status(), 'a sibling container is "the proxy" under auto').toBe(200);
    } finally {
      await direct.dispose();
      await okJSON(await api.patch('/api/admin/login-security', { data: { trusted_proxies: [] } }), 'trusted proxies back to FILEX_TRUSTED_PROXIES');
    }
    const after = await playwright.request.newContext({ baseURL: env('REALENV_PROXY_API') });
    try {
      expect((await after.get('/api/tls/ask?domain=files.proxy.test')).status(), 'Caddy alone again').toBe(404);
    } finally {
      await after.dispose();
    }
  });
});
