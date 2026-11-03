// Admin → Bot protection (#294): the provider, its keys, the forms, the
// outage fallback - read from the endpoints' shapes (GET/PATCH
// /api/admin/captcha, POST /api/admin/captcha/test), in both languages.
//
// ⚠ What is pinned: the page says where each value in force came from (this
// panel, the environment, the default), sends ONLY what changed (a save must
// not copy the environment's other values into the panel), never shows the
// secret, and hands a field back to the environment with an empty value.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils';
import { createPinia, setActivePinia } from 'pinia';
import { createI18n } from 'vue-i18n';

import BotProtection from '@/views/BotProtection.vue';
import en from '@/locales/en.json';
import tr from '@/locales/tr.json';

type Body = Record<string, unknown>;

const calls: { method: string; url: string; body?: unknown }[] = [];
let state: Body;
let refuse: { status: number; data: Body } | null;

const ALL = ['login', 'demo_login', 'share_password', 'file_request'];

function view(over: Body = {}): Body {
  return {
    provider: 'turnstile',
    site_key: '1x00000000000000000000AA',
    has_secret: true,
    forms: ALL,
    fallback: 'local',
    verify_timeout_ms: 4000,
    ...over,
  };
}

function makeState(over: Body = {}): Body {
  return {
    stored: view({ provider: '', site_key: '', has_secret: false, forms: null, fallback: '' }),
    effective: view(),
    sources: { provider: 'env', site_key: 'env', secret: 'env', forms: 'default', fallback: 'default', verify_timeout_ms: 'default' },
    misconfigured: '',
    secret_key_configured: true,
    fallback_active: false,
    fallback_total: 0,
    all_forms: ALL,
    providers: ['none', 'turnstile', 'recaptcha'],
    bounds: { verify_timeout_ms: [1000, 15000] },
    ...over,
  };
}

function refusal(status: number, data: Body) {
  return Object.assign(new Error('refused'), { isAxiosError: true, response: { status, data } });
}

vi.mock('@/api/client', () => {
  const answer = (method: string) =>
    vi.fn(async (url: string, a?: unknown) => {
      calls.push({ method, url, body: method === 'get' ? undefined : a });
      if (refuse) throw refusal(refuse.status, refuse.data);
      if (url === '/admin/captcha' && method === 'get') return { data: state };
      if (url === '/admin/captcha' && method === 'patch') {
        const p = a as Body;
        const eff = { ...(state.effective as Body) };
        const sources = { ...(state.sources as Body) };
        for (const k of ['provider', 'site_key', 'fallback', 'verify_timeout_ms']) {
          if (k in p) {
            if (p[k] === '') sources[k] = 'env';
            else {
              eff[k] = p[k];
              sources[k] = 'db';
            }
          }
        }
        if (p.forms) {
          eff.forms = p.forms;
          sources.forms = 'db';
        }
        if (p.clear_forms) sources.forms = 'default';
        if (p.secret) {
          eff.has_secret = true;
          sources.secret = 'db';
        }
        state = { ...state, effective: eff, sources };
        return { data: state };
      }
      if (url === '/admin/captcha/test') {
        return { data: { reachable: true, secret_ok: true, codes: [], misconfigured: '', cause: '', message: 'The provider answered.' } };
      }
      throw new Error(`unexpected ${method} ${url}`);
    });
  return {
    extractError: (e: unknown, f: string) => (e as { response?: { data?: { message?: string } } }).response?.data?.message ?? f,
    api: { get: answer('get'), post: answer('post'), patch: answer('patch') },
  };
});

const mounted: VueWrapper[] = [];
async function mountPage(locale: 'en' | 'tr' = 'en') {
  const pinia = createPinia();
  setActivePinia(pinia);
  const i18n = createI18n({ legacy: false, locale, fallbackLocale: 'en', messages: { en, tr } });
  const w = mount(BotProtection, { global: { plugins: [pinia, i18n], stubs: { RouterLink: true } } });
  mounted.push(w);
  await flushPromises();
  return w;
}

beforeEach(() => {
  calls.length = 0;
  state = makeState();
  refuse = null;
});
afterEach(() => {
  while (mounted.length) mounted.pop()!.unmount();
});

const patches = () => calls.filter((c) => c.method === 'patch');

describe('Bot protection', () => {
  it('opens on what is in force, and says the environment set it', async () => {
    const w = await mountPage();
    expect(w.get('[data-testid="bot-protection-state"]').attributes('data-state')).toBe('on');
    expect(w.get('[data-testid="bot-protection-provider-turnstile"]').attributes('aria-checked')).toBe('true');
    expect((w.get('input[name="bot-protection-site-key"]').element as HTMLInputElement).value).toBe('1x00000000000000000000AA');
    expect(w.text()).toContain('From the environment (CAPTCHA_PROVIDER).');
    expect(w.text()).toContain('The default.');
  });

  it('never shows the secret: the box is empty and only says one is stored', async () => {
    const w = await mountPage();
    const box = w.get('input[name="bot-protection-secret"]');
    expect((box.element as HTMLInputElement).value).toBe('');
    expect(box.attributes('type')).toBe('password');
    expect(box.attributes('placeholder')).toBe(en.botProtection.secretStored);
  });

  it('sends only what changed - one form switched off is the whole patch', async () => {
    const w = await mountPage();
    expect(w.get('[data-testid="bot-protection-save"]').attributes('disabled')).toBeDefined();
    await w.get('button#bot-protection-form-demo_login').trigger('click');
    await w.get('[data-testid="bot-protection-save"]').trigger('click');
    await flushPromises();
    expect(patches()).toHaveLength(1);
    expect(patches()[0].body).toEqual({ forms: ['login', 'share_password', 'file_request'] });
    expect(w.text()).toContain('Set on this page.');
  });

  it('a typed secret goes once and the box empties again', async () => {
    const w = await mountPage();
    await w.get('input[name="bot-protection-secret"]').setValue('1x0000000000000000000000000000000AA');
    await w.get('[data-testid="bot-protection-save"]').trigger('click');
    await flushPromises();
    expect(patches()[0].body).toEqual({ secret: '1x0000000000000000000000000000000AA' });
    expect((w.get('input[name="bot-protection-secret"]').element as HTMLInputElement).value).toBe('');
  });

  it('a value set here goes back to the environment with an empty one', async () => {
    state = makeState({ sources: { provider: 'db', site_key: 'env', secret: 'env', forms: 'default', fallback: 'default' } });
    const w = await mountPage();
    await w.get('[data-testid="bot-protection-handback-provider"]').trigger('click');
    await flushPromises();
    expect(patches()[0].body).toEqual({ provider: '' });
  });

  it('switched off, the page asks for nothing else', async () => {
    const w = await mountPage();
    await w.get('[data-testid="bot-protection-provider-none"]').trigger('click');
    expect(w.find('input[name="bot-protection-site-key"]').exists()).toBe(false);
    await w.get('[data-testid="bot-protection-save"]').trigger('click');
    await flushPromises();
    expect(patches()[0].body).toEqual({ provider: 'none' });
  });

  it('the fallback is a strip, not a dropdown, and says what refusing means', async () => {
    const w = await mountPage();
    expect(w.find('select').exists()).toBe(false);
    await w.get('[data-testid="bot-protection-fallback-off"]').trigger('click');
    await w.get('[data-testid="bot-protection-save"]').trigger('click');
    await flushPromises();
    expect(patches()[0].body).toEqual({ fallback: 'off' });
  });

  it('a setup that cannot verify says why, and that the fallback answers meanwhile', async () => {
    state = makeState({ misconfigured: 'secret_missing' });
    const w = await mountPage('tr');
    expect(w.get('[data-testid="bot-protection-state"]').text()).toBe('Çalışmıyor');
    expect(w.get('[data-testid="bot-protection-misconfigured"]').text()).toBe(tr.botProtection.mis.secret_missing);
  });

  it('no encryption key: the secret cannot be stored here, and the page says where to put it', async () => {
    state = makeState({ secret_key_configured: false });
    const w = await mountPage();
    expect(w.get('input[name="bot-protection-secret"]').attributes('disabled')).toBeDefined();
    expect(w.get('[data-testid="bot-protection-no-secret-key"]').text()).toContain('CAPTCHA_SECRET');
  });

  it('the test sends the keys on the page and shows the server’s sentence', async () => {
    const w = await mountPage();
    await w.get('input[name="bot-protection-secret"]').setValue('2x0000000000000000000000000000000AA');
    await w.get('[data-testid="bot-protection-test"]').trigger('click');
    await flushPromises();
    const sent = calls.find((c) => c.url === '/admin/captcha/test');
    expect(sent?.body).toEqual({ provider: 'turnstile', site_key: '1x00000000000000000000AA', secret: '2x0000000000000000000000000000000AA' });
    expect(w.get('[data-testid="bot-protection-test-result"]').text()).toContain('The provider answered.');
  });

  it('a tenant’s administrator gets the server’s refusal, not a form', async () => {
    refuse = { status: 403, data: { error: 'supertenant_only', message: 'Managed by the platform operator.' } };
    const w = await mountPage();
    expect(w.text()).toContain('Managed by the platform operator.');
    expect(w.find('[data-testid="bot-protection-save"]').exists()).toBe(false);
  });
});
