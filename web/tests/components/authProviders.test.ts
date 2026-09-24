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
//   · each step is said in the reader's language, with the reason in words
//     and the server's own text only as a "technical detail";
//   · every step and reason the Go probes can answer has a sentence in both
//     languages — read out of the Go source, so a new check without words
//     fails here instead of showing "connect: fail" to an operator.
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils';
import { createPinia, setActivePinia } from 'pinia';
import { createI18n } from 'vue-i18n';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

import en from '@/locales/en.json';
import tr from '@/locales/tr.json';
import type { AuthProvider, AuthProviderField, AuthProviderTestResult } from '@/api/types';

const testCall = vi.fn<[string, Record<string, unknown>], Promise<AuthProviderTestResult>>();
const updateCall = vi.fn();

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

function mountPage(locale: 'en' | 'tr'): VueWrapper {
  const i18n = createI18n({ legacy: false, locale, fallbackLocale: 'en', messages: { en, tr } });
  return mount(AuthProviders, { global: { plugins: [i18n] }, attachTo: document.body });
}

beforeEach(() => {
  setActivePinia(createPinia());
  vi.clearAllMocks();
  document.body.innerHTML = '';
  fx.providers = defaultProviders();
  fx.secretKey = true;
});

describe('Test now', () => {
  it('is offered only where there is a server to reach', async () => {
    const w = mountPage('en');
    await flushPromises();
    expect(w.find('[data-testid="auth-provider-test-button-ldap"]').exists()).toBe(true);
    expect(w.find('[data-testid="auth-provider-test-button-oidc"]').exists()).toBe(true);
    expect(w.find('[data-testid="auth-provider-test-button-local"]').exists()).toBe(false);
    expect(w.find('[data-testid="auth-provider-test-button-api-token"]').exists()).toBe(false);
  });

  it('tests the form as it stands, not the saved row', async () => {
    testCall.mockResolvedValue({ testable: true, ok: true, checks: [] });
    const w = mountPage('en');
    await flushPromises();

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

  it('says every step in the reader’s language, the failed one with its reason', async () => {
    testCall.mockResolvedValue({
      testable: true,
      ok: false,
      checks: [
        { id: 'required', status: 'ok' },
        { id: 'connect', status: 'fail', params: { host: 'dc.example.com:389', reason: 'refused', detail: 'dial tcp: connection refused' } },
      ],
    });
    const w = mountPage('tr');
    await flushPromises();
    await w.find('[data-testid="auth-provider-test-button-ldap"]').trigger('click');
    await flushPromises();

    const box = w.find('[data-testid="auth-provider-test-ldap"]');
    expect(box.text()).toContain(tr.authProviders.testFail);
    expect(box.text()).not.toContain(tr.authProviders.testOk);

    const connect = w.find('[data-testid="auth-provider-check-connect"]');
    expect(connect.attributes('data-status')).toBe('fail');
    expect(connect.text()).toContain('dc.example.com:389');
    expect(connect.text()).toContain(tr.authProviders.reasons.refused);
    // The server's own words only as the technical detail, never as the sentence.
    // The colon is in the message (a French space goes before it).
    expect(connect.text()).toContain(tr.authProviders.technicalDetailIs.replace('{detail}', 'dial tcp: connection refused'));
    expect(connect.text()).not.toMatch(/^connect: fail/);
    expect(w.find('[data-testid="auth-provider-check-required"]').text()).toContain(tr.authProviders.checks.required.ok);
  });
});

describe('what the page may change, and what it says it may not', () => {
  it('an environment provider is read-only and says where it comes from', async () => {
    const w = mountPage('en');
    await flushPromises();
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

  it('password sign-in has no switch here, and the page says why nothing here can lock you out', async () => {
    const w = mountPage('tr');
    await flushPromises();
    const local = w.find('[data-testid="auth-provider-local"]');
    expect(local.find('input').exists()).toBe(false);
    expect(local.find('button').exists()).toBe(false);
    expect(w.find('[data-testid="auth-providers-lockout-note"]').text()).toContain(tr.authProviders.lockoutNote);
  });

  it('a stored secret is never put in its box; the box says it is set', async () => {
    const w = mountPage('en');
    await flushPromises();
    const box = w.find('input[name="auth-field-ldap-bind_password"]');
    expect((box.element as HTMLInputElement).value).toBe('');
    expect(w.find('[data-testid="auth-provider-ldap"]').text()).toContain(en.authProviders.secretSet);
  });

  it('marks a provider saved before v0.43.0 as never applied, and imported off', async () => {
    const w = mountPage('tr');
    await flushPromises();
    expect(w.find('[data-testid="auth-provider-legacy-proxy-header"]').text()).toBe(tr.authProviders.legacy);
    expect(w.find('[data-testid="auth-provider-state-proxy-header"]').text()).toBe(tr.authProviders.status.disabled);
  });

  it('says a provider that is on but could not start, with the reason', async () => {
    fx.providers = defaultProviders().map((p) =>
      p.id === 'ldap' ? { ...p, state: 'failed', status: 'misconfigured', last_error: 'ldap: url and base_dn required' } : p,
    );
    const w = mountPage('en');
    await flushPromises();
    expect(w.find('[data-testid="auth-provider-state-ldap"]').text()).toBe(en.authProviders.status.misconfigured);
    expect(w.find('[data-testid="auth-provider-error-ldap"]').text()).toContain('ldap: url and base_dn required');
  });

  it('warns that secrets cannot be stored without FILEX_SECRET_KEY', async () => {
    fx.secretKey = false;
    const w = mountPage('en');
    await flushPromises();
    const said = w.find('[data-testid="auth-providers-no-secret-key"]');
    // ⚠ The variable's NAME is not in the catalogue: the sentence carries an
    // `{env}` slot and the page draws the name as <code>, so a translator
    // cannot mistype it (v0.43.0 translation sweep).
    expect(en.authProviders.noSecretKey).toContain('{env}');
    expect(en.authProviders.noSecretKey).not.toContain('FILEX_SECRET_KEY');
    expect(said.find('code').text()).toBe('FILEX_SECRET_KEY');
    expect(said.text()).toBe(en.authProviders.noSecretKey.replace('{env}', 'FILEX_SECRET_KEY'));
  });
});

describe('Save and apply', () => {
  it('sends the form, never a secret that was left blank', async () => {
    updateCall.mockResolvedValue({ status: 'saved', provider: null, checks: [], testOk: true });
    const w = mountPage('en');
    await flushPromises();
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
        { id: 'required', status: 'ok' },
        { id: 'connect', status: 'fail', params: { host: 'dc.example.com:389', reason: 'refused' } },
      ],
    });
    updateCall.mockResolvedValueOnce({ status: 'saved', provider: null, checks: [], testOk: false });
    const w = mountPage('tr');
    await flushPromises();
    await w.find('[data-testid="auth-provider-save-ldap"]').trigger('click');
    await flushPromises();

    const ask = w.find('[data-testid="auth-provider-confirm"]');
    expect(ask.exists()).toBe(true);
    const step = w.find('[data-testid="auth-provider-confirm-connect"]');
    expect(step.text()).toContain('dc.example.com:389');
    expect(step.text()).toContain(tr.authProviders.reasons.refused);
    expect(w.find('[data-testid="auth-provider-confirm-required"]').exists()).toBe(false);
    expect(updateCall).toHaveBeenCalledTimes(1);

    await w.find('[data-testid="auth-provider-confirm-enable"]').trigger('click');
    await flushPromises();
    expect(updateCall).toHaveBeenCalledTimes(2);
    expect(updateCall.mock.calls[1][1].confirm_failed_test).toBe(true);
  });

  it('says a refusal on the card it is about, not in a toast', async () => {
    updateCall.mockRejectedValue(
      Object.assign(new Error('Request failed with status code 409'), {
        isAxiosError: true,
        response: { status: 409, data: { error: 'last_sign_in_method', message: 'the last way an administrator can sign in' } },
      }),
    );
    const w = mountPage('en');
    await flushPromises();
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
  ])('%s has a sentence for each', (_lang, bundle) => {
    const said = bundle.authProviders.checks as Record<string, Record<string, string>>;
    const missing = checks.filter((c) => {
      const [id, st] = c.split('.');
      return !said[id]?.[st];
    });
    expect(missing).toEqual([]);
    const words = bundle.authProviders.reasons as Record<string, string>;
    expect(reasons.filter((r) => !words[r])).toEqual([]);
  });
});
