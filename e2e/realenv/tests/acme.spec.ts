/**
 * realenv acme - filex issuing its OWN certificates (FILEX_TLS_MODE=acme,
 * docs/TENANT-ADMIN.md "TLS") from a real ACME authority: Pebble, Let's
 * Encrypt's test server, with pebble-challtestsrv as the DNS it asks.
 * e2e/realenv/run.sh tls starts it all:
 *
 *   acme     TLS on :5001 and NO HTTP listener: a certificate can only have
 *            been proved by TLS-ALPN-01
 *   h1       TLS on :8443 (Pebble never dials it), HTTP on :5002: only
 *            HTTP-01 can prove it
 *   renew    a Pebble whose certificates live three minutes: autocert renews
 *            at a third of the lifetime, while the test watches
 *   notrust  no trust in the authority's certificate
 *
 * filex trusts Pebble's directory the way any Go program trusts a private CA:
 * SSL_CERT_FILE names Pebble's root (there is no filex setting for it), and
 * FILEX_TLS_ACME_DIRECTORY names the directory.
 */
import { test, expect, type APIRequestContext } from '@playwright/test';
import https from 'node:https';
import type { X509Certificate } from 'node:crypto';
import { loginAs } from '../../helpers/auth';
import { adminApi, dns, env, handshake, logLines, missing, okJSON, pebbleRoot, readLog, until, verifyChain, type Handshake } from '../lib/realenv';

const why = missing('REALENV_TLS', 'tls', 'Pebble and pebble-challtestsrv');

/** Handshakes until the server presents a certificate (the first one makes
 *  filex order it, so it may take the authority's round trips). */
async function certified(ip: string, port: number, name: string, timeoutMs = 120_000): Promise<Handshake> {
  let last: Handshake | undefined;
  try {
    return await until(
      `a certificate for ${name}`,
      async () => {
        last = await handshake(ip, port, name);
        return last.ok ? last : undefined;
      },
      timeoutMs,
      2000,
    );
  } catch (err) {
    throw new Error(`${String(err)}; last handshake: ${last?.error}`);
  }
}

function filexLog(name: string): string {
  return `/work/logs/${process.env.REALENV_PREFIX ?? 'fxre'}-${name}.log`;
}

/** An HTTPS request to filex's own TLS listener, trusting only `root`. */
function getVia(ip: string, port: number, host: string, path: string, root: X509Certificate): Promise<number> {
  return new Promise((resolve, reject) => {
    https
      .get({ host: ip, port, path, servername: host, headers: { Host: `${host}:${port}` }, ca: root.toString() }, (res) => {
        res.resume();
        resolve(res.statusCode ?? 0);
      })
      .on('error', reject);
  });
}

test.describe('realenv: filex issues its own certificates from a real ACME authority', () => {
  test.skip(!!why, why);
  test.describe.configure({ mode: 'default' });
  test.use({ baseURL: process.env.REALENV_ACME_API });

  let api: APIRequestContext;
  let root: X509Certificate;
  const ip = process.env.REALENV_ACME_IP ?? '';

  test.beforeAll(async ({ playwright }) => {
    api = await adminApi(playwright.request, env('REALENV_ACME_API'));
    root = await pebbleRoot(env('REALENV_PEBBLE_MGMT'));
  });
  test.afterAll(async () => {
    await api?.dispose();
  });

  test('TLS-ALPN-01: the platform address gets a certificate with no HTTP listener at all', async () => {
    await dns.addA('files.filex.test', ip);
    const h = await certified(ip, 5001, 'files.filex.test');
    expect(verifyChain(h, root, 'files.filex.test')).toBe('');
    expect(h.leaf!.issuer).toMatch(/Pebble/);
    // The certificate is the one the page is served with.
    expect(await getVia(ip, 5001, 'files.filex.test', '/healthz', root)).toBe(200);
    // Pebble validated it (it does not log the challenge type of a
    // TLS-ALPN-01 validation) and never tried HTTP-01: this filex has no
    // HTTP listener, so TLS-ALPN-01 is the only challenge that can have
    // proved it.
    const peb = env('REALENV_PEBBLE_LOG');
    await until('Pebble validated it', async () => logLines(peb, 'Pulled a task', '"files.filex.test"').length > 0, 10_000);
    expect(logLines(peb, 'validate w/ HTTP', 'files.filex.test'), readLog(peb).slice(-2000)).toEqual([]);
  });

  test('a name the platform does not serve gets no certificate, and the log says so', async () => {
    await dns.addA('stranger.filex.test', ip);
    const h = await handshake(ip, 5001, 'stranger.filex.test');
    expect(h.ok, 'a stranger name pointed at the platform must not be certified').toBe(false);
    await until('the refusal in filex log', async () =>
      logLines(filexLog('acme'), 'stranger.filex.test', 'not an address this platform serves').length > 0, 10_000);
    // Nothing was ordered for it: the authority never heard the name.
    expect(logLines(env('REALENV_PEBBLE_LOG'), 'stranger.filex.test')).toEqual([]);
  });

  test("a tenant's own domain: waiting while it has no CNAME, on the screen too; certified once it points at the tenant", async ({ page }) => {
    const tenant = await okJSON<{ id: number }>(
      await api.post('/api/admin/providers', { data: { name: 'Acme Corp', slug: 'acme', realm: 'acme', enabled: true } }),
      'create tenant acme',
    );
    const q = `?tenant=${tenant.id}`;
    const added = await okJSON<{ id: number; status: string }>(
      await api.post(`/api/admin/tenant/domains${q}`, { data: { domain: 'files.acme-corp.test' } }),
      'add own domain',
    );
    expect(added.status).toBe('pending');

    // No record at all. pebble-challtestsrv answers a name it does not know
    // with an empty answer and no NXDOMAIN, which Go's resolver calls a "lame
    // referral": not a definite answer, so filex says the DNS could not be
    // asked and changes nothing (a real DNS server says NXDOMAIN: no_record).
    type Checked = { domain: { status: string; last_error_code?: string; last_error?: string } };
    let row = (await okJSON<Checked>(await api.post(`/api/admin/tenant/domains/${added.id}/check${q}`), 'check with no record')).domain;
    expect(row.status).toBe('pending');
    expect(row.last_error_code, JSON.stringify(row)).toBe('dns_failed');
    await loginAs(page);
    await page.goto(`/admin/tenants/${tenant.id}`);
    const detail = page.getByTestId(`tenant-domain-detail-${added.id}`);
    await expect(detail).toContainText('The DNS could not be asked (');
    await expect(detail).toContainText('nothing was changed');

    // The commonest mistake: an address record where the CNAME should be.
    // The check says so, in words and as a code.
    await dns.addA('files.acme-corp.test', ip);
    row = (await okJSON<Checked>(await api.post(`/api/admin/tenant/domains/${added.id}/check${q}`), 'check with an address record')).domain;
    expect(row.status).toBe('pending');
    expect(row.last_error_code, JSON.stringify(row)).toBe('no_cname');
    // A pending domain is not the platform's: no certificate is ordered.
    const early = await handshake(ip, 5001, 'files.acme-corp.test');
    expect(early.ok).toBe(false);
    await page.reload();
    await expect(detail).toHaveText('files.acme-corp.test has address records of its own, not a CNAME to acme.tenants.filex.test');

    // The tenant's CNAME, to its platform subdomain (address records, as
    // the documentation asks for the platform's wildcard).
    await dns.clearA('files.acme-corp.test');
    await dns.addA('acme.tenants.filex.test', ip);
    await dns.setCNAME('files.acme-corp.test', 'acme.tenants.filex.test.');
    row = (await okJSON<Checked>(await api.post(`/api/admin/tenant/domains/${added.id}/check${q}`), 'check with the CNAME')).domain;
    expect(row.status, JSON.stringify(row)).toBe('active');
    await page.reload();
    await expect(detail).toContainText(/Working since/);
    // Nothing has asked for its certificate yet, and the screen says so.
    const cert = page.getByTestId(`tenant-domain-acme-${added.id}`);
    await expect(cert).toHaveText('By filex (ACME): not obtained yet');

    const own = await certified(ip, 5001, 'files.acme-corp.test');
    expect(verifyChain(own, root, 'files.acme-corp.test')).toBe('');
    const sub = await certified(ip, 5001, 'acme.tenants.filex.test');
    expect(verifyChain(sub, root, 'acme.tenants.filex.test')).toBe('');
    expect(await getVia(ip, 5001, 'files.acme-corp.test', '/healthz', root)).toBe(200);
    // Obtained, and until when.
    await page.reload();
    await expect(cert).toHaveText(/^By filex \(ACME\), until /);
    await page.screenshot({ path: `${process.env.REALENV_RESULTS ?? '/work/results/tls'}/acme-obtained.png`, fullPage: true });
  });

  test("an own domain the authority cannot reach: no certificate; the log and the screen say the authority's reason", async ({ page }) => {
    // beta's own domain points at beta's subdomain (so it is active and
    // served), but the subdomain's address is one where nothing listens.
    const tenant = await okJSON<{ id: number }>(
      await api.post('/api/admin/providers', { data: { name: 'Beta', slug: 'beta', realm: 'beta', enabled: true } }),
      'create tenant beta',
    );
    const q = `?tenant=${tenant.id}`;
    await dns.addA('beta.tenants.filex.test', env('REALENV_DEAD_IP'));
    await dns.setCNAME('files.beta-corp.test', 'beta.tenants.filex.test.');
    const added = await okJSON<{ id: number }>(await api.post(`/api/admin/tenant/domains${q}`, { data: { domain: 'files.beta-corp.test' } }), 'add own domain');
    const row = (await okJSON<{ domain: { status: string } }>(await api.post(`/api/admin/tenant/domains/${added.id}/check${q}`), 'check')).domain;
    expect(row.status, JSON.stringify(row)).toBe('active');

    const h = await handshake(ip, 5001, 'files.beta-corp.test');
    expect(h.ok).toBe(false);
    // The log: the authority's reason, not only autocert's "no viable
    // challenge type found".
    const lines = await until(
      "the authority's reason in filex log",
      async () => {
        const l = logLines(filexLog('acme'), 'could not validate an address', 'files.beta-corp.test');
        return l.length ? l : undefined;
      },
      60_000,
    );
    expect(lines.join('\n')).toMatch(/refused|connect/i);

    // The screen: not obtained, with the authority's words quoted.
    await loginAs(page);
    await page.goto(`/admin/tenants/${tenant.id}`);
    await expect(page.getByTestId(`tenant-domain-acme-${added.id}`)).toContainText('By filex (ACME): could not be obtained');
    const reason = page.getByTestId(`tenant-domain-acme-reason-${added.id}`);
    await expect(reason).toHaveText(/^The ACME authority said: ".*(refused|connect).*"$/i);
    await page.screenshot({ path: `${process.env.REALENV_RESULTS ?? '/work/results/tls'}/acme-failed.png`, fullPage: true });
  });

  test("a served address with no DNS record: the log says the authority found no address for it", async () => {
    await okJSON(await api.post('/api/admin/providers', { data: { name: 'Gamma', slug: 'gamma', realm: 'gamma', enabled: true } }), 'create tenant gamma');
    const h = await handshake(ip, 5001, 'gamma.tenants.filex.test');
    expect(h.ok).toBe(false);
    const lines = await until(
      'the failure in filex log',
      async () => {
        const l = logLines(filexLog('acme'), 'could not validate an address', 'gamma.tenants.filex.test');
        return l.length ? l : undefined;
      },
      60_000,
    );
    // Pebble: "Could not resolve URL ..."; Let's Encrypt: "DNS problem:
    // NXDOMAIN looking up A for ...".
    expect(lines.join('\n')).toMatch(/could not resolve|NXDOMAIN|no valid A records|DNS problem/i);
  });

  test('HTTP-01: a filex whose TLS port the authority never dials still gets its certificate', async () => {
    const h1 = env('REALENV_H1_IP');
    await dns.addA('h1.filex.test', h1);
    const h = await certified(h1, 8443, 'h1.filex.test');
    expect(verifyChain(h, root, 'h1.filex.test')).toBe('');
    const peb = env('REALENV_PEBBLE_LOG');
    await until('Pebble validated it over HTTP', async () => logLines(peb, 'validate w/ HTTP', 'h1.filex.test').length > 0, 10_000);
  });

  test('renewal: a certificate is replaced before it runs out', async () => {
    test.setTimeout(420_000);
    const rip = env('REALENV_RENEW_IP');
    const shortRoot = await pebbleRoot(env('REALENV_PEBBLE_SHORT_MGMT'));
    await dns.addA('renew.filex.test', rip);
    const first = await certified(rip, 5001, 'renew.filex.test');
    expect(verifyChain(first, shortRoot, 'renew.filex.test')).toBe('');
    const life = Date.parse(first.leaf!.validTo) - Date.parse(first.leaf!.validFrom);
    expect(life, 'the short-lived authority issued a short certificate').toBeLessThan(10 * 60_000);
    // autocert renews when a third of the lifetime is left (x/crypto/acme/autocert,
    // renewal.go): about two minutes into a three-minute certificate.
    const second = await until(
      'a renewed certificate',
      async () => {
        const h = await handshake(rip, 5001, 'renew.filex.test');
        return h.ok && h.leaf!.serialNumber !== first.leaf!.serialNumber ? h : undefined;
      },
      Math.max(life, 60_000) + 60_000,
      5000,
    );
    expect(verifyChain(second, shortRoot, 'renew.filex.test')).toBe('');
    expect(Date.parse(second.leaf!.validTo)).toBeGreaterThan(Date.parse(first.leaf!.validTo));
    // Renewed BEFORE the old one ran out.
    expect(Date.now()).toBeLessThan(Date.parse(first.leaf!.validTo));
  });

  test("an authority whose certificate filex does not trust: no certificate, and the log says it is not trusted", async () => {
    const nip = env('REALENV_NOTRUST_IP');
    await dns.addA('notrust.filex.test', nip);
    const h = await handshake(nip, 5001, 'notrust.filex.test');
    expect(h.ok).toBe(false);
    // The handshake's error is the directory request's: it names the
    // authority's address and the reason, not the host being certified.
    const lines = await until(
      'the failure in filex log',
      async () => {
        const l = logLines(filexLog('notrust'), 'TLS handshake error', 'certificate signed by unknown authority');
        return l.length ? l : undefined;
      },
      30_000,
    );
    expect(lines.join('\n')).toContain('pebble:14000');
  });
});
