// "Test now" on an identity provider.
//
// Release-candidate sweep, 2026-09-21: an LDAP entry with no address at all
// answered "Sağlayıcı tamam" — the handler returned `{ok:true}` for anything.
// The server now really connects (backend/internal/auth/drivers/*/probe.go)
// and answers step by step. What the page has to keep true:
//
//   · the button is drawn only where there is something to reach — local
//     accounts and API tokens have no server, and a button that can only say
//     "OK" is worse than none;
//   · what is tested is the form AS IT STANDS (unsaved edits included), the
//     same map Save would write;
//   · each step is said in the reader's language - by the SERVER since 0.54
//     (`text`, backend auth/probe_say.go; the page prints it as it is), with
//     the server's own error only as a "technical detail";
//   · every step and reason the Go probes can answer has a sentence in both
//     languages of the SERVER catalogue (backend/internal/srvtext/locales) -
//     read out of the Go source, so a new check without words fails here
//     instead of showing "connect: fail" to an operator.
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils';
import { createPinia, setActivePinia } from 'pinia';
import { createI18n } from 'vue-i18n';
import { createMemoryHistory, createRouter, type Router } from 'vue-router';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

import en from '@/locales/en.json';
import tr from '@/locales/tr.json';
import type { AuthProvider, AuthProviderField, AuthProviderTestAccount, AuthProviderTestResult } from '@/api/types';
import { useToastStore } from '@/stores/toast';
import { formatInterval } from '@/lib/format';

const testCall = vi.fn<[string, Record<string, unknown>, AuthProviderTestAccount?], Promise<AuthProviderTestResult>>();
const updateCall = vi.fn();
const removeCall = vi.fn(async () => undefined);
const syncStatusCall = vi.fn(async (): Promise<unknown> => ({ name: 'ldap', available: true, running: false, interval_seconds: 0, last: null }));
const syncStartCall = vi.fn(async () => undefined);

const LDAP_FIELDS: AuthProviderField[] = [
  { key: 'url', kind: 'text', required: true },
  { key: 'base_dn', kind: 'text', required: true },
  { key: 'bind_password', kind: 'secret' },
  { key: 'start_tls', kind: 'bool' },
];

function provider(id: string, extra: Partial<AuthProvider> = {}): AuthProvider {
  return {
    id: id as AuthProvider['id'],
    enabled: true,
    config: {},
    config_redacted: {},
    status: 'ok',
    last_error: null,
    testable: false,
    origin: 'page',
    state: 'running',
    secrets_set: {},
    fields: [],
    ...extra,
  } as AuthProvider;
}

const fx = vi.hoisted(() => ({ providers: [] as unknown[], secretKey: true }));

function defaultProviders(): AuthProvider[] {
  return [
    provider('local', { origin: 'environment', from: 'FILEX_AUTH_DRIVERS' }),
    provider('ldap', {
      testable: true,
      managed: true,
      config_redacted: { url: 'ldap://dc.example.com:389', base_dn: 'dc=example,dc=com', start_tls: false },
      secrets_set: { bind_password: true },
      fields: LDAP_FIELDS,
    }),
    provider('oidc', {
      testable: true,
      origin: 'environment',
      from: 'the config file /etc/filex/filex.yaml (auth.drivers)',
      config_redacted: { issuer: 'https://idp.example/realms/main', client_id: 'filex' },
      secrets_set: { client_secret: true },
      shadowed: true,
    }),
    provider('proxy-header', { managed: true, enabled: false, state: 'off', status: 'disabled', legacy: true, fields: [] }),
    provider('api-token', { origin: 'builtin' }),
  ];
}

vi.mock('@/api/auth-providers', () => ({
  AuthProvidersApi: {
    overview: vi.fn(async () => ({
      providers: fx.providers,
      passwordSignIn: true,
      recoveryLogin: false,
      secretKey: fx.secretKey,
    })),
    update: (...a: unknown[]) => updateCall(...a),
    test: (id: string, draft: Record<string, unknown>) => testCall(id, draft),
    syncStatus: (...a: unknown[]) => syncStatusCall(...(a as [])),
    syncStart: (...a: unknown[]) => syncStartCall(...(a as [])),
    remove: (...a: unknown[]) => removeCall(...(a as [])),
    create: vi.fn(async () => null),
    setTenants: vi.fn(async () => null),
    dismissReview: vi.fn(async () => undefined),
  },
}));

if (typeof HTMLDialogElement !== 'undefined' && !HTMLDialogElement.prototype.showModal) {
  HTMLDialogElement.prototype.showModal = function () {
    this.setAttribute('open', '');
  };
  HTMLDialogElement.prototype.close = function () {
    this.removeAttribute('open');
  };
}

import AuthProviders from '@/views/AuthProviders.vue';
import AuthProviderEdit from '@/views/AuthProviderEdit.vue';

// The overview (cards under tabs) and a provider's own page, behind a real
// router: a card opens /auth-providers/:name, and the page goes back to its tab.
let router: Router;
async function mountAt(locale: 'en' | 'tr', path: string): Promise<VueWrapper> {
  const i18n = createI18n({ legacy: false, locale, fallbackLocale: 'en', messages: { en, tr } });
  router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/auth-providers', name: 'auth-providers', component: AuthProviders },
      { path: '/auth-providers/:name', name: 'auth-providers.edit', component: AuthProviderEdit },
    ],
  });
  await router.push(path);
  await router.isReady();
  const view = path === '/auth-providers' ? AuthProviders : AuthProviderEdit;
  const w = mount(view, { global: { plugins: [i18n, router] }, attachTo: document.body });
  await flushPromises();
  return w;
}
const mountPage = (locale: 'en' | 'tr') => mountAt(locale, '/auth-providers');
const mountEdit = (id: string, locale: 'en' | 'tr' = 'en') => mountAt(locale, `/auth-providers/${id}`);

beforeEach(() => {
  setActivePinia(createPinia());
  vi.clearAllMocks();
  fx.providers = defaultProviders();
  fx.secretKey = true;
});

describe('Test now', () => {
  it('is offered only where there is a server to reach', async () => {
    expect((await mountEdit('ldap')).find('[data-testid="auth-provider-test-button-ldap"]').exists()).toBe(true);
    expect((await mountEdit('oidc')).find('[data-testid="auth-provider-test-button-oidc"]').exists()).toBe(true);
    expect((await mountEdit('local')).find('[data-testid="auth-provider-test-button-local"]').exists()).toBe(false);
  });

  it('tests the form as it stands, not the saved row', async () => {
    testCall.mockResolvedValue({ testable: true, ok: true, checks: [] });
    const w = await mountEdit('ldap');
    await w.find('input[name="auth-field-ldap-url"]').setValue('ldap://other.example.com:389');
    await w.find('[data-testid="auth-provider-test-button-ldap"]').trigger('click');
    await flushPromises();

    expect(testCall).toHaveBeenCalledTimes(1);
    const [id, draft] = testCall.mock.calls[0];
    expect(id).toBe('ldap');
    expect(draft.url).toBe('ldap://other.example.com:389');
    // ⚠ A stored secret is never echoed back as its placeholder: blank means
    // "the one you have".
    expect(draft).not.toHaveProperty('bind_password');
  });

  it('prints every step as the server said it, the failed one with its technical detail', async () => {
    testCall.mockResolvedValue({
      testable: true,
      ok: false,
      checks: [
        { id: 'required', status: 'ok', text: 'Zorunlu alanlar dolu.' },
        {
          id: 'connect',
          status: 'fail',
          params: { host: 'dc.example.com:389', reason: 'refused', detail: 'dial tcp: connection refused' },
          text: 'dc.example.com:389 adresine bağlanılamadı: bağlantı reddedildi.',
        },
      ],
    });
    const w = await mountEdit('ldap', 'tr');
    await w.find('[data-testid="auth-provider-test-button-ldap"]').trigger('click');
    await flushPromises();

    const box = w.find('[data-testid="auth-provider-test-ldap"]');
    expect(box.text()).toContain(tr.authProviders.testFail);
    expect(box.text()).not.toContain(tr.authProviders.testOk);

    const connect = w.find('[data-testid="auth-provider-check-connect"]');
    expect(connect.attributes('data-status')).toBe('fail');
    expect(connect.text()).toContain('dc.example.com:389 adresine bağlanılamadı: bağlantı reddedildi.');
    // The error the server met only as the technical detail, never as the sentence.
    expect(connect.text()).toContain(tr.authProviders.technicalDetailIs.replace('{detail}', 'dial tcp: connection refused'));
    expect(connect.text()).not.toMatch(/^connect: fail/);
    expect(w.find('[data-testid="auth-provider-check-required"]').text()).toContain('Zorunlu alanlar dolu.');
  });
});

describe('a step the server worded itself', () => {
  it('shows the server’s hint as the sentence, not the step’s name', async () => {
    testCall.mockResolvedValue({
      testable: true,
      ok: false,
      checks: [
        { id: 'sudo', status: 'ok', text: 'sudo lets filex run that one command without a password.' },
        {
          id: 'pamtester',
          status: 'fail',
          params: { reason: 'missing', hint: 'pamtester is not installed at /usr/bin/pamtester. Install it.' },
          text: 'pamtester is not installed at /usr/bin/pamtester. Install it.',
        },
      ],
    });
    const w = await mountEdit('ldap');
    await w.find('[data-testid="auth-provider-test-button-ldap"]').trigger('click');
    await flushPromises();
    const bad = w.find('[data-testid="auth-provider-check-pamtester"]');
    expect(bad.attributes('data-status')).toBe('fail');
    expect(bad.text()).toContain('pamtester is not installed at /usr/bin/pamtester. Install it.');
    expect(bad.text()).not.toContain('pamtester: fail');
    expect(w.find('[data-testid="auth-provider-check-sudo"]').text()).toContain('sudo lets filex run that one command without a password.');
  });
});

describe('what the page may change, and what it says it may not', () => {
  it('an environment provider is read-only and says where it comes from', async () => {
    const w = await mountEdit('oidc');
    const card = w.find('[data-testid="auth-provider-oidc"]');
    expect(card.find('input').exists()).toBe(false);
    expect(card.find('[data-testid="auth-provider-save-oidc"]').exists()).toBe(false);
    expect(card.find('[data-testid="auth-provider-origin-oidc"]').text()).toContain(en.authProviders.origin.environment);
    expect(card.find('[data-testid="auth-provider-env-oidc"]').text()).toContain('the config file /etc/filex/filex.yaml (auth.drivers)');
    expect(card.text()).toContain('https://idp.example/realms/main');
    expect(card.find('[data-testid="auth-provider-shadowed-oidc"]').text()).toBe(en.authProviders.shadowed);
    // The environment's secret is said to be set, never shown.
    expect(card.text()).toContain(en.authProviders.secretSetShort);
  });

  it('password sign-in has no switch here, and the overview says why nothing here can lock you out', async () => {
    const edit = await mountEdit('local', 'tr');
    const local = edit.find('[data-testid="auth-provider-local"]');
    expect(local.find('input').exists()).toBe(false);
    expect(local.find('button').exists()).toBe(false);
    const w = await mountPage('tr');
    expect(w.find('[data-testid="auth-providers-lockout-note"]').text()).toContain(tr.authProviders.lockoutNote);
  });

  it('a stored secret is never put in its box; the box says it is set', async () => {
    const w = await mountEdit('ldap');
    const box = w.find('input[name="auth-field-ldap-bind_password"]');
    expect((box.element as HTMLInputElement).value).toBe('');
    expect(w.find('[data-testid="auth-provider-ldap"]').text()).toContain(en.authProviders.secretSet);
  });

  it('marks a provider saved before v0.43.0 as never applied, and imported off', async () => {
    const w = await mountEdit('proxy-header', 'tr');
    expect(w.find('[data-testid="auth-provider-legacy-proxy-header"]').text()).toBe(tr.authProviders.legacy);
    expect(w.find('[data-testid="auth-provider-state-proxy-header"]').text()).toBe(tr.authProviders.status.disabled);
  });

  it('says a provider that is on but could not start, with the reason — on its card and its page', async () => {
    fx.providers = defaultProviders().map((p) =>
      p.id === 'ldap' ? { ...p, state: 'failed', status: 'misconfigured', last_error: 'ldap: url and base_dn required' } : p,
    );
    const w = await mountEdit('ldap');
    expect(w.find('[data-testid="auth-provider-state-ldap"]').text()).toBe(en.authProviders.status.misconfigured);
    expect(w.find('[data-testid="auth-provider-error-ldap"]').text()).toContain('ldap: url and base_dn required');
    const o = await mountPage('en');
    expect(o.get('[data-testid="auth-card-state-ldap"]').text()).toBe(en.authProviders.status.misconfigured);
    expect(o.get('[data-testid="auth-card-ldap"]').text()).toContain('ldap: url and base_dn required');
  });

  it('warns that secrets cannot be stored without FILEX_SECRET_KEY', async () => {
    fx.secretKey = false;
    const w = await mountPage('en');
    const said = w.find('[data-testid="auth-providers-no-secret-key"]');
    // ⚠ The variable's NAME is not in the catalogue: the sentence carries an
    // `{env}` slot and the page draws the name as <code>, so a translator
    // cannot mistype it (v0.43.0 translation sweep).
    expect(en.authProviders.noSecretKey).toContain('{env}');
    expect(en.authProviders.noSecretKey).not.toContain('FILEX_SECRET_KEY');
    expect(said.find('code').text()).toBe('FILEX_SECRET_KEY');
    expect(said.text()).toBe(en.authProviders.noSecretKey.replace('{env}', 'FILEX_SECRET_KEY'));
    // …and on a page provider's own page, where the secret is typed.
    expect((await mountEdit('ldap')).find('[data-testid="auth-providers-no-secret-key"]').exists()).toBe(true);
  });
});

describe('Save and apply', () => {
  it('sends the form, never a secret that was left blank', async () => {
    updateCall.mockResolvedValue({ status: 'saved', provider: null, checks: [], testOk: true });
    const w = await mountEdit('ldap');
    await w.find('[data-testid="auth-provider-save-ldap"]').trigger('click');
    await flushPromises();
    expect(updateCall).toHaveBeenCalledTimes(1);
    const [id, body] = updateCall.mock.calls[0];
    expect(id).toBe('ldap');
    expect(body.enabled).toBe(true);
    expect(body.config.url).toBe('ldap://dc.example.com:389');
    expect(body.config).not.toHaveProperty('bind_password');
    expect(body.confirm_failed_test).toBeUndefined();
  });

  it('asks before switching on a provider whose test failed, naming the steps, and only then confirms', async () => {
    updateCall.mockResolvedValueOnce({
      status: 'test_failed',
      message: 'The test failed',
      failed: ['connect'],
      checks: [
        { id: 'required', status: 'ok', text: 'Zorunlu alanlar dolu.' },
        { id: 'connect', status: 'fail', params: { host: 'dc.example.com:389', reason: 'refused' }, text: 'dc.example.com:389 adresine bağlanılamadı: bağlantı reddedildi.' },
      ],
    });
    updateCall.mockResolvedValueOnce({ status: 'saved', provider: null, checks: [], testOk: false });
    const w = await mountEdit('ldap', 'tr');
    await w.find('[data-testid="auth-provider-save-ldap"]').trigger('click');
    await flushPromises();

    const ask = w.find('[data-testid="auth-provider-confirm"]');
    expect(ask.exists()).toBe(true);
    const step = w.find('[data-testid="auth-provider-confirm-connect"]');
    expect(step.text()).toContain('dc.example.com:389 adresine bağlanılamadı: bağlantı reddedildi.');
    expect(w.find('[data-testid="auth-provider-confirm-required"]').exists()).toBe(false);
    expect(updateCall).toHaveBeenCalledTimes(1);

    await w.find('[data-testid="auth-provider-confirm-enable"]').trigger('click');
    await flushPromises();
    expect(updateCall).toHaveBeenCalledTimes(2);
    expect(updateCall.mock.calls[1][1].confirm_failed_test).toBe(true);
  });

  it('says a refusal on the page it is about, not in a toast', async () => {
    updateCall.mockRejectedValue(
      Object.assign(new Error('Request failed with status code 409'), {
        isAxiosError: true,
        response: { status: 409, data: { error: 'last_sign_in_method', message: 'the last way an administrator can sign in' } },
      }),
    );
    const w = await mountEdit('ldap');
    await w.find('[data-testid="auth-provider-save-ldap"]').trigger('click');
    await flushPromises();
    expect(w.find('[data-testid="auth-provider-refusal-ldap"]').text()).toContain('the last way an administrator can sign in');
  });
});

describe('the words for every step the server can answer', () => {
  const here = path.dirname(fileURLToPath(import.meta.url));
  const AUTH = path.resolve(here, '../../../backend/internal/auth');
  // The save door adds one check of its own (a stored secret that cannot
  // be opened); it is said on the page like every other.
  const HANDLER = path.resolve(here, '../../../backend/internal/api/handlers/auth_providers.go');

  function goSources(dir: string): string {
    let out = '';
    for (const e of fs.readdirSync(dir, { withFileTypes: true })) {
      const p = path.join(dir, e.name);
      if (e.isDirectory()) out += goSources(p);
      else if (e.name.endsWith('.go') && !e.name.endsWith('_test.go')) out += fs.readFileSync(p, 'utf8');
    }
    return out;
  }
  const src = goSources(AUTH) + fs.readFileSync(HANDLER, 'utf8');
  const STATUS: Record<string, string> = { ProbeOK: 'ok', ProbeFail: 'fail', ProbeUnchecked: 'unchecked' };
  const checks = [...new Set([...src.matchAll(/auth\.Check\("([a-z_]+)", auth\.(Probe\w+)/g)].map((m) => `${m[1]}.${STATUS[m[2]]}`))];
  const reasons = [
    ...new Set([
      ...[...src.matchAll(/"reason", "([a-z_]+)"/g)].map((m) => m[1]),
      ...[...src.matchAll(/reason :?= "([a-z_]+)"/g)].map((m) => m[1]),
      ...[...(/func NetReason[\s\S]*?\n\}/.exec(src)?.[0] ?? '').matchAll(/return "([a-z_]+)"/g)].map((m) => m[1]),
    ]),
  ];

  it('finds the checks at all', () => {
    expect(checks.length, `no auth.Check(...) calls parsed out of ${AUTH}`).toBeGreaterThan(15);
    expect(reasons).toContain('refused');
  });

  it.each([
    ['en', en],
    ['tr', tr],
  ])('%s has a sentence for each, in the SERVER catalogue', (_lang, bundle) => {
    const table = JSON.parse(
      fs.readFileSync(path.resolve(here, `../../../backend/internal/srvtext/locales/${_lang}.json`), 'utf8'),
    ) as Record<string, string>;
    const missing = checks.filter((c) => !table[`server.auth_provider.check.${c}`]);
    expect(missing).toEqual([]);
    expect(reasons.filter((r) => !table[`server.auth_provider.reason.${r}`])).toEqual([]);
    // …and the panel keeps no copy of them (0.54: lib/providerChecks is gone).
    const panel = bundle.authProviders as Record<string, unknown>;
    expect(panel.checks).toBeUndefined();
    expect(panel.reasons).toBeUndefined();
  });
});

describe('an LDAP directory’s page: sections, hints, defaults and the directory steps', () => {
  const FULL: AuthProviderField[] = [
    { key: 'url', kind: 'text', required: true },
    { key: 'base_dn', kind: 'text', required: true },
    { key: 'bind_dn', kind: 'text' },
    { key: 'bind_password', kind: 'secret' },
    { key: 'user_filter', kind: 'text', default: '(mail=%s)' },
    { key: 'email_attr', kind: 'text', default: 'mail' },
    { key: 'group_attr', kind: 'text', default: 'memberOf' },
    { key: 'group_filter', kind: 'text' },
    { key: 'start_tls', kind: 'bool' },
    { key: 'sync_interval', kind: 'text' },
    { key: 'sync_groups', kind: 'bool', default: 'true' },
  ];
  it('groups the form into sections, in order, with hints on the new settings', async () => {
    fx.providers = [provider('ldap', { testable: true, managed: true, fields: FULL })];
    const w = await mountEdit('ldap');
    const heads = w.findAll('[data-testid^="auth-section-ldap-"]').map((h) => h.attributes('data-testid'));
    expect(heads).toEqual(['auth-section-ldap-connection', 'auth-section-ldap-people', 'auth-section-ldap-groups', 'auth-section-ldap-sync']);
    const names = w.findAll('input[name^="auth-field-ldap-"]').map((i) => i.attributes('name'));
    expect(names.indexOf('auth-field-ldap-start_tls')).toBeLessThan(names.indexOf('auth-field-ldap-user_filter'));
    expect(w.text()).toContain(en.authProviders.fieldHints.group_filter);
    expect(w.text()).toContain(en.authProviders.fieldHints.sync_interval);
  });

  it('an environment LDAP says each setting in the same order, and what an unset one means', async () => {
    fx.providers = [provider('ldap', {
      testable: true, origin: 'environment', from: 'FILEX_AUTH_DRIVERS', fields: FULL,
      config_redacted: { url: 'ldap://127.0.0.1:3890', base_dn: 'dc=example,dc=com', sync_interval: '6h', multi_tenant: false },
      secrets_set: { bind_password: true },
    })];
    const w = await mountEdit('ldap');
    const text = w.get('[data-testid="auth-provider-envfields-ldap"]').text();
    expect(text).toContain(en.authProviders.defaultIs.replace('{value}', 'memberOf'));
    expect(text).toContain(en.authProviders.defaultIs.replace('{value}', 'Yes'));
    expect(text.indexOf(en.authProviders.sections.people)).toBeLessThan(text.indexOf(en.authProviders.sections.sync));
    expect(text.indexOf('6h')).toBeGreaterThan(text.indexOf(en.authProviders.sections.sync));
    expect(text).toContain(en.authProviders.secretSetShort);
  });

  // The one/many wording is the server's (auth/probe_say_test.go); the page
  // prints what it was given.
  it('says what the directory holds, as the server worded it', async () => {
    testCall.mockResolvedValue({
      testable: true,
      ok: true,
      checks: [
        {
          id: 'people',
          status: 'ok',
          params: { n: '1000+', mail: '998', filter: '(mail=*)', attr: 'mail' },
          text: '1000+ people found with (mail=*); 998 with an email (mail).',
        },
        {
          id: 'groups',
          status: 'unchecked',
          params: { attr: 'memberOf' },
          text: 'Nobody lists groups in memberOf. If your directory has groups, set a group search filter.',
        },
        { id: 'sync_groups', status: 'ok', params: { n: '1' }, text: 'Directory sync would bring in 1 group.' },
      ],
    });
    const w = await mountEdit('ldap');
    await w.find('[data-testid="auth-provider-test-button-ldap"]').trigger('click');
    await flushPromises();
    expect(w.get('[data-testid="auth-provider-check-people"]').text()).toContain('1000+ people found with (mail=*); 998 with an email (mail).');
    expect(w.get('[data-testid="auth-provider-check-groups"]').text()).toContain('Nobody lists groups in memberOf');
    expect(w.get('[data-testid="auth-provider-check-sync_groups"]').text()).toContain('bring in 1 group.');
  });

  it('has a Settings and a Sync section; Sync holds the directory’s sync', async () => {
    const w = await mountEdit('ldap');
    expect(w.findAll('[data-testid^="auth-section-tab-"]').map((b) => b.text())).toEqual([en.authProviders.editSections.settings, en.authProviders.editSections.sync]);
    await w.get('[data-testid="auth-section-tab-sync"]').trigger('click');
    await flushPromises();
    expect(w.find('[data-testid="auth-provider-sync"]').exists()).toBe(true);
    expect((w.get('[data-testid="auth-provider-ldap"]').element as HTMLElement).style.display).toBe('none');
    expect(router.currentRoute.value.query.section).toBe('sync');
  });
});

describe('several LDAP directories', () => {
  // Another LDAP provider is an instance of the driver ("Add a provider"):
  // its slug, its own name, and the driver it runs.
  function twoDirectories() {
    fx.providers = [
      provider('ldap', { driver: 'ldap', testable: true, managed: true, fields: LDAP_FIELDS, config_redacted: { url: 'ldap://dc.example.com:389', base_dn: 'dc=example,dc=com', email_domains: 'example.com' } }),
      provider('partner', {
        driver: 'ldap', instance_id: 2, label: 'Partner AD', testable: true, managed: true, enabled: false, state: 'off', status: 'disabled',
        config_redacted: { url: 'ldaps://partner.example', base_dn: 'dc=partner' }, fields: LDAP_FIELDS,
      }),
    ];
  }

  it('shows each as a card, by its own name, that opens its page', async () => {
    twoDirectories();
    const w = await mountPage('en');
    const partner = w.get('[data-testid="auth-card-partner"]');
    expect(partner.text()).toContain('Partner AD');
    expect(partner.text()).toContain('ldaps://partner.example · dc=partner');
    expect(partner.text()).toContain(en.authProviders.card.anyDomain);
    expect(w.get('[data-testid="auth-card-ldap"]').text()).toContain(en.authProviders.card.domains.replace('{list}', 'example.com'));
    expect(partner.attributes('href')).toBe('/auth-providers/partner');
  });

  it('deletes one made with Add a provider from its page, and goes back to the LDAP tab - never the first', async () => {
    twoDirectories();
    const first = await mountEdit('ldap');
    expect(first.find('[data-testid="auth-provider-delete-ldap"]').exists()).toBe(false);
    first.unmount();
    const w = await mountEdit('partner');
    expect(w.get('[data-testid="auth-provider-title"]').text()).toContain('Partner AD');
    await w.get('[data-testid="auth-provider-delete-partner"]').trigger('click');
    await flushPromises();
    expect(document.body.querySelector('[data-testid="auth-provider-delete-body"]')?.textContent).toContain('Partner AD');
    (document.body.querySelector('[data-testid="auth-provider-delete-confirm"]') as HTMLButtonElement).click();
    await flushPromises();
    expect(removeCall).toHaveBeenCalledWith('partner', false);
    expect(router.currentRoute.value.fullPath).toBe('/auth-providers?tab=ldap');
  });

  it('a provider that is not there says so', async () => {
    const w = await mountEdit('ldap-gone');
    expect(w.get('[data-testid="auth-provider-missing"]').text()).toBe(en.authProviders.notFound);
  });
});

describe('the overview: tabs of cards', () => {
  it('one tab per kind of sign-in, LDAP first; API keys are not on this page', async () => {
    const w = await mountPage('en');
    const tabs = w.findAll('[data-testid^="auth-tab-"]').map((b) => b.text());
    expect(tabs).toEqual([
      en.authProviders.tabs.ldap, en.authProviders.tabs.local, en.authProviders.tabs.oidc,
      en.authProviders.tabs['proxy-header'], en.authProviders.tabs.windows, en.authProviders.tabs.pam,
    ]);
    expect(w.find('[data-testid="auth-card-api-token"]').exists()).toBe(false);
    expect(w.find('[data-testid="auth-card-ldap"]').exists()).toBe(true);
    expect(w.find('[data-testid="auth-card-oidc"]').exists()).toBe(false);

    await w.get('[data-testid="auth-tab-oidc"]').trigger('click');
    await flushPromises();
    expect(w.find('[data-testid="auth-card-oidc"]').exists()).toBe(true);
    expect(w.find('[data-testid="auth-card-ldap"]').exists()).toBe(false);
    expect(w.get('[data-testid="auth-card-oidc"]').text()).toContain('https://idp.example/realms/main');
    expect(w.get('[data-testid="auth-tab-oidc"]').attributes('aria-selected')).toBe('true');
    expect(router.currentRoute.value.query.tab).toBe('oidc');
  });

  it('a directory’s card says its sync, and runs it without opening the directory', async () => {
    syncStatusCall.mockResolvedValue({
      name: 'ldap', available: true, running: false, interval_seconds: 21600,
      last: { provider: 'ldap', trigger: 'manual', started_at: new Date().toISOString(), finished_at: new Date().toISOString(), found: 1005, created: 0, updated: 0, skipped: 1, missing: 3, disabled: 0, groups_found: 406, groups_created: 0, groups_renamed: 0, groups_restored: 0, groups_removed: 0, groups_linked_by_hand: 11 },
    });
    const w = await mountPage('en');
    const sync = w.get('[data-testid="auth-card-sync-ldap"]').text();
    // The interval in the viewer's words ("6 hr"), never the config's "6h".
    expect(sync).toContain(en.authProviders.card.every.replace('{every}', formatInterval(21600, 'en')));
    expect(sync).not.toContain('6h');
    expect(sync).toContain('1005');
    expect(sync).toContain('406');
    // Sync now is beside the link, not inside it: a button in an <a> is
    // invalid HTML and reads as part of the link.
    expect(w.get('[data-testid="auth-card-ldap"]').find('button').exists()).toBe(false);
    await w.get('[data-testid="auth-card-syncnow-ldap"]').trigger('click');
    await flushPromises();
    expect(syncStartCall).toHaveBeenCalledWith('ldap');
    expect(router.currentRoute.value.path).toBe('/auth-providers');
  });
});
