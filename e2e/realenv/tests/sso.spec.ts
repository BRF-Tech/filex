/**
 * realenv sso - sign-in through a REAL Keycloak and a REAL OpenLDAP
 * (docs/TENANT-ADMIN.md, "Sign-in providers bound to tenants" and "The
 * sign-in page per realm"). e2e/realenv/run.sh sso starts:
 *
 *   Keycloak (dev mode) at http://idp.example.test:8080, realms imported from
 *   stack/keycloak: `corp` (the operator's SSO; alice in engineering, bob in
 *   filex-admins, carol in nothing) and `beta` (tenant beta's own; frank in
 *   beta-staff, grace in nothing).
 *   OpenLDAP at ldaps://ldap.example.test:636 with a certificate from the
 *   run's own CA (dave in beta-staff, erin in nothing), also reachable as
 *   ldap-private.test on the private network.
 *
 * Keycloak and the directory sit on a network whose addresses the guarded
 * client counts as public (TEST-NET-3), so a tenant's own providers are
 * dialled THROUGH the guard, as on a real install; the private-network name of
 * the same directory is what the guard must refuse.
 *
 * filex: multi-tenant, the platform at http://files.sso.test:5212, tenants
 * acme and beta with no address of their own (they sign in with their realm).
 */
import { test, expect, type APIRequestContext, type Browser, type Page } from '@playwright/test';
import fs from 'node:fs';
import { adminApi, env, logLines, missing, okJSON, until } from '../lib/realenv';

const why = missing('REALENV_SSO', 'sso', 'Keycloak and OpenLDAP');
const BASE = process.env.REALENV_SSO_URL ?? '';
const IDP = process.env.REALENV_IDP ?? '';
const LOG = `/work/logs/${process.env.REALENV_PREFIX ?? 'fxre'}-sso.log`;

interface Me {
  user: { email: string; role: string; provider_id?: number };
}

/** A fresh browser (no Keycloak session left over from another person). */
async function fresh(browser: Browser): Promise<Page> {
  const ctx = await browser.newContext({ baseURL: BASE });
  await ctx.addInitScript(() => {
    try {
      localStorage.setItem('filex.installPrompt.dismissed', '1');
      localStorage.setItem('filex.locale', 'en');
    } catch {
      /* storage blocked */
    }
  });
  return ctx.newPage();
}

/** Keycloak's own sign-in form, as a person fills it. */
async function keycloak(page: Page, user: string, password: string) {
  await page.waitForURL((u) => u.href.startsWith(IDP), { timeout: 30_000 });
  await page.locator('#username').fill(user);
  await page.locator('#password').fill(password);
  await page.locator('#kc-login').click();
}

/** The sign-in page's sentence for an SSO failure that names no reason (login.errOidc). */
const GENERIC = 'SSO sign-in failed. Try again, or sign in with password.';

async function me(page: Page): Promise<Me | undefined> {
  const r = await page.request.get('/api/auth/me');
  return r.ok() ? ((await r.json()) as Me) : undefined;
}

async function signedIn(page: Page): Promise<Me> {
  // An administrator lands in /admin/, anyone else in the drive (/drive/).
  await page.waitForURL(/\/(admin|drive)\/(home|dashboard|files)?([?#/]|$)/, { timeout: 30_000 });
  return until('a session', () => me(page), 15_000, 500);
}

test.describe('realenv: SSO and LDAP sign-in against real servers', () => {
  test.skip(!!why, why);
  test.describe.configure({ mode: 'default' });
  test.use({ baseURL: BASE });

  let api: APIRequestContext;
  let platform = 0;
  let acme = 0;
  let beta = 0;
  let engineers = 0;
  // beta's own OIDC, by the slug it is made with.
  const betaOIDC = 'beta-sso';

  // Idempotent: a failed test restarts the worker, and this runs again
  // against the same server.
  test.beforeAll(async ({ playwright }) => {
    api = await adminApi(playwright.request, BASE);
    const tenants = async () =>
      (await okJSON<{ providers: Array<{ id: number; slug: string; is_supertenant?: boolean }> }>(await api.get('/api/admin/providers'), 'tenants')).providers;
    for (const slug of ['acme', 'beta']) {
      if (!(await tenants()).some((p) => p.slug === slug)) {
        await okJSON(await api.post('/api/admin/providers', { data: { name: slug[0]!.toUpperCase() + slug.slice(1), slug, realm: slug, enabled: true } }), `tenant ${slug}`);
      }
    }
    const list = await tenants();
    platform = list.find((p) => p.is_supertenant)!.id;
    acme = list.find((p) => p.slug === 'acme')!.id;
    beta = list.find((p) => p.slug === 'beta')!.id;

    // The operator's SSO, for the platform's own tenant and for acme. Saving
    // it runs the real test against Keycloak first.
    const providers = await api.get('/api/admin/auth-providers');
    if (!(await providers.text()).includes('"corp"')) {
      await okJSON(
        await api.post('/api/admin/auth-providers', {
          data: {
            driver: 'oidc', slug: 'corp', label: 'Corp SSO', enabled: true, tenants: [platform, acme],
            config: {
              issuer: `${IDP}/realms/corp`, client_id: 'filex', client_secret: 'corp-client-secret',
              role_claim: 'groups', admin_group: 'filex-admins', auto_create: true,
            },
          },
        }),
        'the operator SSO (corp)',
      );
    }
    // A filex group whose members are the directory's engineering group.
    const groups = await okJSON<Array<{ id: number; name: string }> | { groups: Array<{ id: number; name: string }> }>(await api.get('/api/admin/groups'), 'groups');
    const found = (Array.isArray(groups) ? groups : groups.groups ?? []).find((g) => g.name === 'Engineers');
    if (found) {
      engineers = found.id;
    } else {
      const g = await okJSON<{ id?: number; group?: { id: number } }>(
        await api.post('/api/admin/groups', { data: { name: 'Engineers', links: [{ kind: 'sso', value: 'engineering' }] } }),
        'group Engineers',
      );
      engineers = g.group?.id ?? g.id ?? 0;
    }
  });
  test.afterAll(async () => {
    await api?.dispose();
  });

  test('the platform sign-in page draws the SSO button; alice signs in to the platform tenant and joins the linked group', async ({ browser }) => {
    const page = await fresh(browser);
    await page.goto('/admin/login');
    const button = page.getByTestId('login-sso-corp');
    await expect(button).toBeVisible();
    await expect(button).toHaveText(/Corp SSO/);
    await button.click();
    await keycloak(page, 'alice', 'alice-pass');
    const who = await signedIn(page);
    expect(who.user.email).toBe('alice@corp.test');
    expect(who.user.provider_id).toBe(platform);
    expect(who.user.role).not.toBe('admin');

    const group = await okJSON<{ members?: Array<{ email: string; source: string }> }>(await api.get(`/api/admin/groups/${engineers}`), 'group');
    const member = (group.members ?? []).find((m) => m.email === 'alice@corp.test');
    expect(member, JSON.stringify(group)).toBeTruthy();
    expect(member!.source).toBe('sso');
    await page.context().close();
  });

  test("bob's group is the admin group: he signs in as an administrator", async ({ browser }) => {
    const page = await fresh(browser);
    await page.goto('/admin/login');
    await page.getByTestId('login-sso-corp').click();
    await keycloak(page, 'bob', 'bob-pass');
    const who = await signedIn(page);
    expect(who.user.email).toBe('bob@corp.test');
    expect(who.user.role).toBe('admin');
    await page.context().close();
  });

  test("typing a realm brings that tenant's buttons; carol signs in to acme through the shared SSO", async ({ browser }) => {
    const page = await fresh(browser);
    await page.goto('/admin/login');
    await page.getByTestId('login-realm').fill('acme');
    await expect(page.getByTestId('login-sso-corp')).toBeVisible();
    await page.getByTestId('login-sso-corp').click();
    await keycloak(page, 'carol', 'carol-pass');
    const who = await signedIn(page);
    expect(who.user.email).toBe('carol@corp.test');
    expect(who.user.provider_id, JSON.stringify(who)).toBe(acme);
    await page.context().close();
  });

  test('the same person in a second tenant is refused, as documented (one e-mail address, one tenant): the page says only "SSO failed", the log says why', async ({ browser }) => {
    // docs/MULTI-TENANCY.md: users.email is globally unique, so alice, who
    // has her account in the platform's tenant, cannot get one in acme too.
    // ⚠⚠ The ONE refusal that names no reason (docs/SSO.md, "When an SSO
    // sign-in is refused"): saying so would tell her that another tenant
    // exists and that her address belongs to it. Same address, same sentence
    // as any failure with no reason.
    const page = await fresh(browser);
    await page.goto('/admin/login');
    await page.getByTestId('login-realm').fill('acme');
    await page.getByTestId('login-sso-corp').click();
    await keycloak(page, 'alice', 'alice-pass');
    await page.waitForURL(/\/admin\/login\?error=oidc/);
    expect(new URL(page.url()).searchParams.get('reason'), page.url()).toBeNull();
    await expect(page.getByTestId('login-sso-error')).toHaveText(GENERIC);
    await until('the reason in the log', async () => logLines(LOG, 'oidc callback failed', 'registered to another tenant').length > 0, 10_000);
    await page.context().close();
  });

  test("a realm with no SSO shows none (and a realm nobody has looks the same)", async ({ browser }) => {
    const page = await fresh(browser);
    await page.goto('/admin/login');
    await page.getByTestId('login-realm').fill('beta');
    await expect(page.getByTestId('login-sso-corp')).toHaveCount(0);
    await page.getByTestId('login-realm').fill('nobody-has-this');
    await expect(page.getByTestId('login-sso-corp')).toHaveCount(0);
    await page.context().close();
  });

  test("a tenant's own OIDC behind the guard must not be a private address", async () => {
    const res = await api.post(`/api/admin/tenant/auth-providers?tenant=${beta}`, {
      data: {
        driver: 'oidc', label: 'Private', slug: 'beta-private', enabled: true,
        config: { issuer: `${BASE}/realms/none`, client_id: 'x', client_secret: 'y' },
      },
    });
    expect(res.ok(), 'an issuer on the private network is refused for a tenant').toBe(false);
    expect(await res.text()).toMatch(/private|local|internal/i);
  });

  test("tenant beta's own OIDC: with account creation off, a new person is refused and the page says no account is opened at a first sign-in", async ({ browser }) => {
    const made = await api.post(`/api/admin/tenant/auth-providers?tenant=${beta}`, {
      data: {
        driver: 'oidc', label: 'Beta SSO', slug: betaOIDC, enabled: true,
        config: {
          issuer: `${IDP}/realms/beta`, client_id: 'filex-beta', client_secret: 'beta-client-secret',
          role_claim: 'groups', auto_create: false,
        },
      },
    });
    if (made.status() === 409) {
      // A run that restarted after a failure: it is there, put the rule back.
      await okJSON(await api.patch(`/api/admin/tenant/auth-providers/${betaOIDC}?tenant=${beta}`, { data: { config: { auto_create: false } } }), 'account creation off');
    } else {
      await okJSON(made, "beta's own OIDC");
    }
    const page = await fresh(browser);
    await page.goto('/admin/login');
    await page.getByTestId('login-realm').fill('beta');
    const button = page.getByTestId(`login-sso-${betaOIDC}`);
    await expect(button).toBeVisible();
    await expect(button).toHaveText(/Beta SSO/);
    await button.click();
    await keycloak(page, 'frank', 'frank-pass');
    await page.waitForURL(/\/admin\/login\?error=oidc&reason=auto_create_off/);
    await expect(page.getByTestId('login-sso-error')).toHaveText(
      'This server does not open an account at a first SSO sign-in. Ask your administrator to open your account.',
    );
    expect(await me(page)).toBeUndefined();
    await until('the reason in the log', async () => logLines(LOG, 'oidc callback failed', 'auto_create_off').length > 0, 10_000);
    await page.context().close();
  });

  test("tenant beta's own OIDC, no address of its own: /api/auth/oidc/start?instance=&realm= opens the session in beta; only beta-staff may get an account", async ({ browser }) => {
    await okJSON(
      await api.patch(`/api/admin/tenant/auth-providers/${betaOIDC}?tenant=${beta}`, {
        data: { config: { auto_create: true, allowed_groups: 'beta-staff' } },
      }),
      'account creation on, for beta-staff',
    );
    // grace is in no group: refused, and the page says so.
    let page = await fresh(browser);
    await page.goto(`/api/auth/oidc/start?instance=${betaOIDC}&realm=beta`);
    await keycloak(page, 'grace', 'grace-pass');
    await page.waitForURL(/\/admin\/login\?error=oidc&reason=group_not_allowed/);
    await expect(page.getByTestId('login-sso-error')).toHaveText(
      'Your account is not in a group that may sign in here. Ask your administrator for access.',
    );
    expect(await me(page)).toBeUndefined();
    await page.context().close();

    // frank is in beta-staff: an account in beta, signed in there.
    page = await fresh(browser);
    await page.goto(`/api/auth/oidc/start?instance=${betaOIDC}&realm=beta`);
    await keycloak(page, 'frank', 'frank-pass');
    const who = await signedIn(page);
    expect(who.user.email).toBe('frank@beta.test');
    expect(who.user.provider_id).toBe(beta);
    await page.context().close();

    // beta's SSO started for another realm is refused like an SSO nobody has:
    // no reason (it would say which realms have which SSO).
    page = await fresh(browser);
    await page.goto(`/api/auth/oidc/start?instance=${betaOIDC}&realm=acme`);
    await page.waitForURL(/\/admin\/login\?error=oidc/);
    expect(new URL(page.url()).searchParams.get('reason'), page.url()).toBeNull();
    await expect(page.getByTestId('login-sso-error')).toHaveText(GENERIC);
    await page.context().close();
  });

  test("tenant beta's own LDAP through the guard: ldaps with its pasted CA passes, the guard's refusals say why", async () => {
    const ca = fs.readFileSync(env('REALENV_LDAP_CA'), 'utf8');
    const base = {
      base_dn: 'dc=example,dc=test', bind_dn: 'cn=admin,dc=example,dc=test', bind_password: 'admin-pass',
      user_filter: '(uid=%s)', email_attr: 'mail', group_attr: 'memberOf', auto_create: true,
    };
    const tryOne = (slug: string, config: Record<string, unknown>) =>
      api.post(`/api/admin/tenant/auth-providers?tenant=${beta}`, {
        data: { driver: 'ldap', label: 'Beta directory', slug, enabled: true, config: { ...base, ...config } },
      });

    // Not encrypted: refused before any connection.
    const plain = await tryOne('beta-ldap-plain', { url: env('REALENV_LDAP_URL').replace('ldaps://', 'ldap://').replace(':636', ':389') });
    expect(plain.ok()).toBe(false);
    expect(await plain.text()).toMatch(/ldaps|StartTLS/i);
    // The same directory by its private-network name: the guard refuses it.
    const priv = await tryOne('beta-ldap-private', { url: env('REALENV_LDAP_PRIVATE_URL'), ca_pem: ca });
    expect(priv.ok()).toBe(false);
    expect(await priv.text()).toMatch(/private|local|internal/i);
    // Its own CA not given: the certificate is not trusted.
    const noCA = await tryOne('beta-ldap-noca', { url: env('REALENV_LDAP_URL') });
    expect(noCA.ok()).toBe(false);
    expect(await noCA.text()).toMatch(/certificate|x509|authority/i);
    // ldaps with the pasted CA: the test passes and it is switched on.
    const good = await tryOne('beta-ldap', { url: env('REALENV_LDAP_URL'), ca_pem: ca, allowed_groups: 'beta-staff' });
    if (good.status() !== 409) await okJSON(good, "beta's own LDAP");
  });

  test('dave signs in to beta through its LDAP with the realm; erin, in no allowed group, gets no account (told why only with show_refusal_reason)', async ({ browser, playwright }) => {
    const anon = await playwright.request.newContext({ baseURL: BASE });
    try {
      const ok = await anon.post('/api/auth/login', { data: { email: 'dave', password: 'dave-pass', realm: 'beta' } });
      expect(ok.ok(), await ok.text()).toBe(true);
      const { token } = (await ok.json()) as { token: string };
      const who = (await (await anon.get('/api/auth/me', { headers: { Authorization: `Bearer ${token}` } })).json()) as Me;
      expect(who.user.provider_id).toBe(beta);
      expect(who.user.email).toBe('dave@beta.test');

      // erin's directory password is right, but she is in no allowed group.
      // By default that is the wrong-password answer (no reason: the form must
      // not confirm a directory password); with the provider's
      // show_refusal_reason on, the form is told why (docs/LDAP.md).
      const refused = await anon.post('/api/auth/login', { data: { email: 'erin', password: 'erin-pass', realm: 'beta' } });
      expect(refused.ok()).toBe(false);
      expect(refused.status()).toBe(401);
      expect(((await refused.json()) as { reason?: string }).reason).toBeUndefined();

      await okJSON(
        await api.patch(`/api/admin/tenant/auth-providers/beta-ldap?tenant=${beta}`, { data: { config: { show_refusal_reason: true } } }),
        'show_refusal_reason on',
      );
      try {
        const told = await anon.post('/api/auth/login', { data: { email: 'erin', password: 'erin-pass', realm: 'beta' } });
        expect(told.status(), await told.text()).toBe(403);
        expect(((await told.json()) as { reason?: string }).reason).toBe('group_not_allowed');
        const wrong = await anon.post('/api/auth/login', { data: { email: 'erin', password: 'not-hers', realm: 'beta' } });
        expect(wrong.status()).toBe(401);
        expect(((await wrong.json()) as { reason?: string }).reason).toBeUndefined();
      } finally {
        await okJSON(
          await api.patch(`/api/admin/tenant/auth-providers/beta-ldap?tenant=${beta}`, { data: { config: { show_refusal_reason: false } } }),
          'show_refusal_reason off again',
        );
      }
    } finally {
      await anon.dispose();
    }

    // The same sign-in on the page, realm typed in its field.
    const page = await fresh(browser);
    await page.goto('/admin/login?local=1');
    await page.getByTestId('login-realm').fill('beta');
    await page.getByLabel(/e-?mail|user/i).first().fill('dave');
    await page.getByLabel(/password/i).first().fill('dave-pass');
    await page.getByRole('button', { name: 'Sign in', exact: true }).click();
    const who = await signedIn(page);
    expect(who.user.provider_id).toBe(beta);
    await page.context().close();
  });
});
