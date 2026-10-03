// External services → ONLYOFFICE → Test now (issue #80).
//
// What Test now tests (the form's values, unsaved) and what it says about the
// document server's route back to filex, in the
// panel's language. A document server that takes a request with no token has
// JWT off: it refuses filex's editor token and its save callbacks arrive
// unsigned, which filex refuses. The server measures it and names it by code
// (`jwt_not_enforced`); the card says it in en and tr, never in the server's
// English fallback.
//
// These go through the REAL view, store and api module with only the HTTP
// layer (and the browser probe, which loads a script) faked.
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { flushPromises, mount } from '@vue/test-utils';
import { createPinia, setActivePinia } from 'pinia';
import { createI18n } from 'vue-i18n';
import { createMemoryHistory, createRouter } from 'vue-router';

import External from '@/views/External.vue';
import { useToastStore } from '@/stores/toast';
import en from '@/locales/en.json';
import tr from '@/locales/tr.json';

type Call = { url: string; body?: unknown };
const calls = { posts: [] as Call[], patches: [] as Call[], probes: [] as Array<{ id: string; url: string }> };
let testAnswer: Record<string, unknown> = {};

const ROW = {
  Name: 'onlyoffice',
  Enabled: true,
  URL: 'https://office.example.com',
  SecretEnc: '***',
  OptionsJSON: '{}',
  LastCheck: null,
  LastState: 'ok',
  env_managed: false,
  advisories: [],
  callback_url: '',
};

vi.mock('@/api/client', () => ({
  extractError: (_e: unknown, f?: string) => f ?? 'error',
  api: {
    get: vi.fn(async (url: string) => {
      if (url === '/admin/external') {
        return { data: { entries: [ROW], public_url: 'https://files.example.com' } };
      }
      throw new Error(`unexpected GET ${url}`);
    }),
    post: vi.fn(async (url: string, body?: unknown) => {
      calls.posts.push({ url, body });
      return { data: testAnswer };
    }),
    patch: vi.fn(async (url: string, body?: unknown) => {
      calls.patches.push({ url, body });
      return { data: { ok: true } };
    }),
  },
}));

vi.mock('@brftech/filex-core', async (importOriginal) => {
  const real = await importOriginal<Record<string, unknown>>();
  return {
    ...real,
    probeExternalFromBrowser: vi.fn(async (id: string, url: string) => {
      calls.probes.push({ id, url });
      return { state: 'ok', url };
    }),
  };
});

const JWT_OFF_ANSWER = {
  ok: true,
  name: 'onlyoffice',
  reachable: true,
  server_reachable: true,
  state: 'ok',
  url: 'https://office.example.com',
  public_url: 'https://files.example.com',
  service_to_filex: { checked: true, ok: true, url: 'https://files.example.com/api/files/onlyoffice/fetch?n=0', jwt_enforced: false },
  advisories: [
    {
      code: 'jwt_not_enforced',
      field: 'secret',
      severity: 'warning',
      detail: 'https://office.example.com',
      message: 'the server English fallback',
    },
  ],
};

async function mountExternal(locale: 'en' | 'tr') {
  const pinia = createPinia();
  setActivePinia(pinia);
  const router = createRouter({
    history: createMemoryHistory('/admin/'),
    routes: [
      { path: '/', component: { template: '<div />' } },
      { path: '/external', name: 'external', component: { template: '<div />' } },
    ],
  });
  await router.push('/');
  const i18n = createI18n({ legacy: false, locale, fallbackLocale: 'en', messages: { en, tr } });
  const w = mount(External, { global: { plugins: [pinia, router, i18n] } });
  await flushPromises();
  return w;
}

function testNowButton(w: Awaited<ReturnType<typeof mountExternal>>, label: string) {
  const btn = w.findAll('button').find((b) => b.text().includes(label));
  if (!btn) throw new Error(`no "${label}" button`);
  return btn;
}

beforeEach(() => {
  calls.posts.length = 0;
  calls.patches.length = 0;
  calls.probes.length = 0;
  testAnswer = JWT_OFF_ANSWER;
});

// "Test now" tested the SAVED row while the box held another address: an
// operator typed a new URL, pressed Test, and was told about the old one. It
// now tests what is in the form, and saves nothing (saving switches every open
// editor over; an unchecked address must not reach them through a Test).
describe('Test now tests what is in the form, without saving it', () => {
  it('sends the typed address, probes it from this browser too, and saves nothing', async () => {
    const w = await mountExternal('en');
    await w.get('[data-testid="external-url-onlyoffice"] input').setValue('https://new-office.example.com');
    calls.probes.length = 0;
    await testNowButton(w, en.common.testNow).trigger('click');
    await flushPromises();

    const sent = calls.posts.find((c) => c.url === '/admin/external/onlyoffice/test');
    expect(sent, 'the Test was run').toBeDefined();
    expect((sent?.body as Record<string, unknown> | undefined)?.url).toBe('https://new-office.example.com');
    expect(calls.patches, 'a Test saves nothing').toEqual([]);
    expect(calls.probes.map((p) => p.url)).toContain('https://new-office.example.com');
    expect(calls.probes.map((p) => p.url)).not.toContain('https://office.example.com');
  });

  it('says the result is about values that are not saved', async () => {
    testAnswer = { ...JWT_OFF_ANSWER, advisories: [], unsaved: true };
    const w = await mountExternal('en');
    await w.get('[data-testid="external-url-onlyoffice"] input').setValue('https://new-office.example.com');
    await testNowButton(w, en.common.testNow).trigger('click');
    await flushPromises();
    expect(w.get('[data-testid="tested-unsaved-onlyoffice"]').text()).toBe(en.external.testedUnsaved);
  });

  it('does not test an address that is not one', async () => {
    const w = await mountExternal('en');
    await w.get('[data-testid="external-url-onlyoffice"] input').setValue('bu-bir-adres-degil');
    await testNowButton(w, en.common.testNow).trigger('click');
    await flushPromises();
    expect(calls.posts.filter((c) => c.url === '/admin/external/onlyoffice/test')).toEqual([]);
  });
});

describe('Test now: a document server that does not enforce JWT', () => {
  it('is named on the card in English', async () => {
    const w = await mountExternal('en');
    await testNowButton(w, en.common.testNow).trigger('click');
    await flushPromises();
    const adv = w.get('[data-testid="advisory-onlyoffice-jwt_not_enforced"]');
    expect(adv.text()).toBe(en.external.advisories.jwt_not_enforced);
    expect(adv.text()).not.toContain('the server English fallback');
  });

  // Measured against ONLYOFFICE Docs 9.4 with JWT off (e2e/realenv, issue
  // #80 S2): the card said reachable and the toast said "Health check
  // failed". The health check passed; the toast points at the card.
  it('the toast does not say the health check failed when it passed', async () => {
    const w = await mountExternal('en');
    const toasts = useToastStore();
    toasts.clear();
    await testNowButton(w, en.common.testNow).trigger('click');
    await flushPromises();
    const said = toasts.toasts.map((x) => x.message);
    expect(said).toContain(en.external.testWarn);
    expect(said).not.toContain(en.external.testFail);
  });

  it('is named on the card in Turkish', async () => {
    const w = await mountExternal('tr');
    await testNowButton(w, tr.common.testNow).trigger('click');
    await flushPromises();
    const adv = w.get('[data-testid="advisory-onlyoffice-jwt_not_enforced"]');
    expect(adv.text()).toBe(tr.external.advisories.jwt_not_enforced);
    expect(adv.text()).toContain('JWT');
  });
});

// 0.50: an ONLYOFFICE pinned by the environment and switched off here stays
// off only until filex restarts (the boot writes the environment back). The
// card says so, names the variable, and says how to switch it off for good.
describe('a service the environment pins', () => {
  it('names the variable and says a switch-off lasts until the restart, in English', async () => {
    ROW.env_managed = true;
    try {
      const w = await mountExternal('en');
      const hint = w.get('[data-testid="env-managed-hint-onlyoffice"]').text();
      expect(hint).toContain('Set by FILEX_ONLYOFFICE_URL.');
      expect(hint).toContain('switching it off included');
      expect(hint).toContain('until filex restarts');
      expect(hint).toContain('remove FILEX_ONLYOFFICE_URL');
    } finally {
      ROW.env_managed = false;
    }
  });

  it('says the same in Turkish', async () => {
    ROW.env_managed = true;
    try {
      const w = await mountExternal('tr');
      const hint = w.get('[data-testid="env-managed-hint-onlyoffice"]').text();
      expect(hint).toContain('FILEX_ONLYOFFICE_URL ile ayarlanmış.');
      expect(hint).toContain('kapatmak da dahil');
      expect(hint).toContain('yeniden başlayana kadar');
    } finally {
      ROW.env_managed = false;
    }
  });

  it('has no such note on a service the environment does not pin', async () => {
    const w = await mountExternal('en');
    expect(w.find('[data-testid="env-managed-hint-onlyoffice"]').exists()).toBe(false);
  });
});
