// The Sign-in security page: the numbers, the exempt addresses, the trusted
// proxies, the locks and the trail — read from the endpoints' real shapes
// (backend/internal/api/handlers/login_security.go), in both languages.
//
// ⚠ jsdom has no layout, so what is pinned here is structure and wording; the
// fit at 958 and 1440 px is measured in a browser (e2e/shots/loginsecurity.mjs).
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils';
import { createPinia, setActivePinia } from 'pinia';
import { createI18n } from 'vue-i18n';

import LoginSecurity from '@/views/LoginSecurity.vue';
import en from '@/locales/en.json';
import tr from '@/locales/tr.json';
import { __resetViewPrefs } from '@brftech/filex-core/src/lib/viewPrefs';
import { closeRowMenus, openRowMenu, pickMenuItem } from '../helpers/rowMenu';
import { listedOptions, pickOption } from '../helpers/choiceSelect';

type Body = Record<string, unknown>;

const calls: { method: string; url: string; body?: unknown; params?: unknown }[] = [];
let state: Body;
let locks: Body[];
let attempts: Body[];
let refuse: { status: number; data: Body } | null;
let failNextPatch: Body | null;

function makeState(over: Body = {}): Body {
  return {
    settings: {
      enabled: true,
      account_max_fails: 5,
      ip_max_fails: 10,
      window_seconds: 600,
      lock_base_seconds: 60,
      lock_max_seconds: 900,
      ip_allowlist: ['10.0.0.0/8'],
      trusted_proxies: [],
    },
    limits: {
      account_max_fails: { min: 1, max: 1000, default: 5 },
      ip_max_fails: { min: 1, max: 10000, default: 10 },
      window_seconds: { min: 10, max: 86400, default: 600 },
      lock_base_seconds: { min: 1, max: 86400, default: 60 },
      lock_max_seconds: { min: 1, max: 86400, default: 900 },
    },
    trusted_proxies_effective: ['loopback'],
    trusted_proxies_source: 'auto',
    trusted_defaults: { auto: true, loopback: false, private: false, link_local: false },
    trusted_addresses: [],
    trusted_proxies_auto: {
      in_use: true, environment: 'plain', runtime: '', networks: [], excluded_gateways: [], excluded_self: [],
      interfaces: [], warning: '', resolved_at: '2026-10-01T09:00:00Z',
    },
    untrusted_forwarders: [],
    untrusted_forwarders_total: 0,
    your_ip: '203.0.113.9',
    your_ip_allowlisted: false,
    ...over,
  };
}

function refusal(status: number, data: Body) {
  return Object.assign(new Error('refused'), { isAxiosError: true, response: { status, data } });
}

vi.mock('@/api/client', () => {
  const answer = (method: string) =>
    vi.fn(async (url: string, a?: unknown) => {
      const params = method === 'get' ? (a as { params?: unknown } | undefined)?.params : undefined;
      calls.push({ method, url, body: method === 'get' ? undefined : a, params });
      if (refuse) throw refusal(refuse.status, refuse.data);
      if (method === 'patch' && failNextPatch) {
        const data = failNextPatch;
        failNextPatch = null;
        throw refusal(400, data);
      }
      if (url === '/admin/login-security' && method === 'get') return { data: state };
      if (url === '/admin/login-security' && method === 'patch') {
        const patch = a as Body;
        state = { ...state, settings: { ...(state.settings as Body), ...patch } };
        if (Array.isArray(patch.ip_allowlist)) {
          state.your_ip_allowlisted = (patch.ip_allowlist as string[]).includes(String(state.your_ip));
        }
        if (Array.isArray(patch.trusted_proxies)) {
          // What the server answers (login_security.go view): the words are
          // the switches, the rest the addresses; [] = back to the default
          // (auto). A forwarder the list now names leaves the list.
          const list = patch.trusted_proxies as string[];
          const words = new Set(list);
          const def = list.length === 0;
          state.trusted_proxies_source = def ? 'auto' : 'setting';
          state.trusted_defaults = {
            auto: def || words.has('auto'),
            loopback: words.has('loopback'),
            private: words.has('private'),
            link_local: words.has('link-local'),
          };
          state.trusted_addresses = list.filter((e) => !['auto', 'loopback', 'private', 'link-local', 'none'].includes(e));
          state.trusted_proxies_auto = { ...(state.trusted_proxies_auto as Body), in_use: def || words.has('auto') };
          state.untrusted_forwarders = ((state.untrusted_forwarders as Body[]) ?? []).filter(
            (f) => !list.includes(String(f.address)),
          );
        }
        return { data: state };
      }
      if (url === '/admin/login-security/locks') return { data: { items: locks, now: '2026-09-30T10:00:00Z' } };
      if (url === '/admin/login-security/attempts') return { data: { items: attempts, total: attempts.length } };
      if (url === '/admin/login-security/unlock') return { data: { ok: true, unlocked: 1 } };
      throw new Error(`unexpected ${method} ${url}`);
    });
  return {
    extractError: (e: unknown, f: string) =>
      (e as { response?: { data?: { message?: string } } }).response?.data?.message ?? f,
    api: { get: answer('get'), post: answer('post'), patch: answer('patch') },
  };
});
vi.mock('axios', async (orig) => {
  const real = await orig<typeof import('axios')>();
  const isAxiosError = (e: unknown) => !!(e as { isAxiosError?: boolean })?.isAxiosError;
  return { ...real, isAxiosError, default: { ...real.default, isAxiosError } };
});

const mounted: VueWrapper[] = [];

async function mountPage(locale: 'en' | 'tr' = 'en') {
  const pinia = createPinia();
  setActivePinia(pinia);
  const i18n = createI18n({ legacy: false, locale, fallbackLocale: 'en', messages: { en, tr } });
  const w = mount(LoginSecurity, { global: { plugins: [pinia, i18n] } });
  mounted.push(w);
  await flushPromises();
  return w;
}

beforeEach(() => {
  __resetViewPrefs();
  closeRowMenus();
  calls.length = 0;
  state = makeState();
  refuse = null;
  failNextPatch = null;
  locks = [
    {
      id: 'acct-1', scope: 'account', subject: 'ada@example.com', fails: 5, limit: 5, lock_level: 2, locked: true,
      locked_until: '2026-09-30T10:04:00Z', retry_after: 240, window_start: '2026-09-30T09:55:00Z',
      last_fail_at: '2026-09-30T10:00:00Z', last_ip: '198.51.100.4', last_protocol: 'web',
    },
    {
      id: 'ip-1', scope: 'ip', subject: '198.51.100.4', fails: 3, limit: 10, lock_level: 0, locked: false,
      retry_after: 0, window_start: '2026-09-30T09:58:00Z', last_fail_at: '2026-09-30T10:00:00Z',
      last_ip: '198.51.100.4', last_protocol: 'sftp',
    },
  ];
  attempts = [
    { id: 9, action: 'login.locked', identifier: 'ada@example.com', ip: '198.51.100.4', protocol: 'web', reason: 'invalid_credentials', at: '2026-09-30T10:00:00Z' },
    { id: 8, action: 'login.failed', identifier: 'ada@example.com', ip: '198.51.100.4', protocol: 'weird-door', reason: 'made_up_reason', at: '2026-09-30T09:59:00Z' },
  ];
});

afterEach(() => {
  while (mounted.length) mounted.pop()!.unmount();
});

const patches = () => calls.filter((c) => c.method === 'patch');

describe('Sign-in security — settings', () => {
  it('fills the form from the server and states the bounds it enforces', async () => {
    const w = await mountPage();
    const box = w.get('input[name="login-limit-account_max_fails"]');
    expect((box.element as HTMLInputElement).value).toBe('5');
    expect(box.attributes('min')).toBe('1');
    expect(box.attributes('max')).toBe('1000');
    expect(w.text()).toContain('Between 1 and 1000. Default 5.');
    expect(w.text()).toContain('In seconds (10 min)');
  });

  it('refuses a number outside the server bounds and sends nothing', async () => {
    const w = await mountPage();
    await w.get('input[name="login-limit-account_max_fails"]').setValue('5000');
    await w.get('[data-testid="login-limit-form"]').trigger('submit');
    await flushPromises();
    expect(w.text()).toContain('Enter a whole number between 1 and 1000.');
    expect(patches()).toHaveLength(0);
  });

  it('refuses a longest lock shorter than the first lock', async () => {
    const w = await mountPage();
    await w.get('input[name="login-limit-lock_max_seconds"]').setValue('30');
    await w.get('[data-testid="login-limit-form"]').trigger('submit');
    await flushPromises();
    expect(w.text()).toContain(en.loginSecurity.limit.errOrder);
    expect(patches()).toHaveLength(0);
  });

  it('saves the switch and the five numbers in one patch', async () => {
    const w = await mountPage();
    await w.get('input[name="login-limit-ip_max_fails"]').setValue('20');
    await w.get('[data-testid="login-limit-form"]').trigger('submit');
    await flushPromises();
    expect(patches()[0]?.body).toEqual({
      enabled: true, account_max_fails: 5, ip_max_fails: 20, window_seconds: 600,
      lock_base_seconds: 60, lock_max_seconds: 900,
    });
  });

  it('puts a server refusal under the field it names', async () => {
    const w = await mountPage();
    failNextPatch = { error: 'invalid_setting', field: 'window_seconds', message: 'too short' };
    await w.get('[data-testid="login-limit-form"]').trigger('submit');
    await flushPromises();
    expect(w.text()).toContain('too short');
  });
});

describe('Sign-in security — allowed addresses', () => {
  it('shows whether the address of this request is on the list', async () => {
    const w = await mountPage();
    expect(w.get('[data-testid="login-your-ip"]').text()).toContain('203.0.113.9');
    expect(w.get('[data-testid="login-your-ip-state"]').text()).toBe(en.loginSecurity.allowlist.mineNotListed);
  });

  it('adds and saves this address with one click', async () => {
    const w = await mountPage();
    await w.get('[data-testid="login-add-my-ip"]').trigger('click');
    await flushPromises();
    expect(patches()[0]?.body).toEqual({ ip_allowlist: ['10.0.0.0/8', '203.0.113.9'] });
    expect(w.get('[data-testid="login-your-ip-state"]').text()).toBe(en.loginSecurity.allowlist.mineListed);
    expect(w.find('[data-testid="login-add-my-ip"]').exists()).toBe(false);
  });

  it('takes several typed entries and removes one, saving only on Save', async () => {
    const w = await mountPage();
    await w.get('[data-testid="login-allowlist"] input').setValue('192.0.2.1, 192.0.2.0/24');
    await w.get('[data-testid="login-allowlist"] [data-testid="address-add"]').trigger('click');
    const chips = () => w.findAll('[data-testid="login-allowlist"] [data-testid="address-chips"] li').map((l) => l.text());
    expect(chips()).toEqual(['10.0.0.0/8', '192.0.2.1', '192.0.2.0/24']);
    await w.get('[aria-label="Remove 10.0.0.0/8"]').trigger('click');
    expect(chips()).toEqual(['192.0.2.1', '192.0.2.0/24']);
    expect(patches()).toHaveLength(0);
    await w.get('[data-testid="login-allowlist-form"]').trigger('submit');
    await flushPromises();
    expect(patches()[0]?.body).toEqual({ ip_allowlist: ['192.0.2.1', '192.0.2.0/24'] });
  });
});

describe('Sign-in security — trusted proxies', () => {
  const sw = (w: VueWrapper, k: string) => w.get(`#login-proxies-class-${k}`);
  const on = (w: VueWrapper, k: string) => sw(w, k).attributes('aria-checked') === 'true';
  const saveProxies = async (w: VueWrapper) => {
    await w.get('[data-testid="login-proxies-form"]').trigger('submit');
    await flushPromises();
  };

  it('says where the list in force comes from, and that a saved list wins', async () => {
    const w = await mountPage();
    expect(w.get('[data-testid="login-proxies-source"]').text()).toBe(en.loginSecurity.proxies.source.auto);
    expect(w.get('[data-testid="login-proxies-override"]').text()).toContain('FILEX_TRUSTED_PROXIES');
    expect(w.find('[data-testid="login-proxies-reset"]').exists(), 'nothing saved here, nothing to remove').toBe(false);
  });

  it('the default is automatic: its switch on, the three classes as three switches, off', async () => {
    const w = await mountPage();
    expect(on(w, 'auto')).toBe(true);
    expect(w.text()).toContain(en.loginSecurity.proxies.class.auto);
    const classes = w.get('[data-testid="login-proxies-classes"]');
    expect(classes.findAll('button[role="switch"]')).toHaveLength(3);
    for (const k of ['loopback', 'private', 'link_local']) expect(on(w, k)).toBe(false);
    const said = classes.text();
    expect(said).toContain(en.loginSecurity.proxies.class.loopback);
    expect(said).toContain(en.loginSecurity.proxies.class.private);
    expect(said).toContain(en.loginSecurity.proxies.class.linkLocal);
    expect(said, 'the classes are described, no range is spelled out').not.toMatch(/\b(10|172|192)\.\d/);
    expect(w.get('[data-testid="login-proxies"]').text()).toContain(en.loginSecurity.proxies.noAddresses);
  });

  it('starts from the list in force, whatever its source: its classes and its addresses apart', async () => {
    state = makeState({
      trusted_proxies_source: 'env',
      trusted_proxies_effective: ['loopback', '192.0.2.0/24'],
      trusted_defaults: { loopback: true, private: false, link_local: false },
      trusted_addresses: ['192.0.2.0/24'],
    });
    const w = await mountPage();
    expect([on(w, 'auto'), on(w, 'loopback'), on(w, 'private'), on(w, 'link_local')]).toEqual([false, true, false, false]);
    expect(w.find('[data-testid="login-proxies-auto"]').exists(), 'automatic is off: nothing of it is shown').toBe(false);
    const chips = w.findAll('[data-testid="login-proxies"] [data-testid="address-chips"] li').map((l) => l.text());
    expect(chips).toEqual(['192.0.2.0/24']);
    const save = w.get('[data-testid="login-proxies-form"] button[type="submit"]');
    expect(save.attributes('disabled')).toBeDefined();
  });

  it('switches a class on next to automatic and saves the list as words and addresses', async () => {
    const w = await mountPage();
    await sw(w, 'private').trigger('click');
    expect(on(w, 'private')).toBe(true);
    await saveProxies(w);
    expect(patches()[0]?.body).toEqual({ trusted_proxies: ['auto', 'private'] });
    expect(w.get('[data-testid="login-proxies-source"]').text()).toBe(en.loginSecurity.proxies.source.setting);
    expect(on(w, 'private'), 'the answer is adopted').toBe(true);
    expect(on(w, 'auto')).toBe(true);
  });

  it('switches automatic off and saves the classes alone', async () => {
    const w = await mountPage();
    await sw(w, 'auto').trigger('click');
    await sw(w, 'loopback').trigger('click');
    await saveProxies(w);
    expect(patches()[0]?.body).toEqual({ trusted_proxies: ['loopback'] });
  });

  it('with every switch off and nothing listed it trusts nobody, says so, and saves none', async () => {
    const w = await mountPage();
    expect(w.find('[data-testid="login-proxies-nobody"]').exists()).toBe(false);
    await sw(w, 'auto').trigger('click');
    expect(w.get('[data-testid="login-proxies-nobody"]').text()).toBe(en.loginSecurity.proxies.trustsNobody);
    await saveProxies(w);
    expect(patches()[0]?.body, 'an empty list would mean "not set"').toEqual({ trusted_proxies: ['none'] });
  });

  it('removes a saved list after asking, and the default is in force again', async () => {
    state = makeState({
      trusted_proxies_source: 'setting',
      trusted_defaults: { auto: false, loopback: true, private: false, link_local: false },
      trusted_addresses: [],
    });
    const confirm = vi.spyOn(window, 'confirm').mockReturnValue(true);
    const w = await mountPage();
    await w.get('[data-testid="login-proxies-reset"]').trigger('click');
    await flushPromises();
    expect(confirm).toHaveBeenCalled();
    expect(patches()[0]?.body).toEqual({ trusted_proxies: [] });
    expect(w.get('[data-testid="login-proxies-source"]').text()).toBe(en.loginSecurity.proxies.source.auto);
    expect([on(w, 'auto'), on(w, 'loopback'), on(w, 'private'), on(w, 'link_local')]).toEqual([true, false, false, false]);
    expect(w.find('[data-testid="login-proxies-reset"]').exists()).toBe(false);
    confirm.mockRestore();
  });

  it.each([
    ['setting', en.loginSecurity.proxies.source.setting],
    ['env', en.loginSecurity.proxies.source.env],
  ])('names the %s source in words, never the wire value', async (source, words) => {
    state = makeState({ trusted_proxies_source: source });
    const w = await mountPage();
    const said = w.get('[data-testid="login-proxies-source"]').text();
    expect(said).toBe(words);
    expect(said).not.toBe(source);
  });

  it('saves the typed list as the trusted_proxies setting, after the switches', async () => {
    const w = await mountPage();
    await w.get('[data-testid="login-proxies"] input').setValue('172.16.0.0/12');
    await w.get('[data-testid="login-proxies"] [data-testid="address-add"]').trigger('click');
    await saveProxies(w);
    expect(patches()[0]?.body).toEqual({ trusted_proxies: ['auto', '172.16.0.0/12'] });
  });
});

describe('Sign-in security - what automatic resolved to', () => {
  const containerAuto = {
    in_use: true, environment: 'container', runtime: 'docker',
    networks: ['172.18.0.0/16'], excluded_gateways: ['172.18.0.1'], excluded_self: ['172.18.0.5'],
    interfaces: [
      { name: 'eth0', kind: 'veth', addresses: ['172.18.0.5/16'], trusted: true, reason: 'container-network' },
      { name: 'eth1', kind: 'macvlan', addresses: ['192.168.77.2/24'], trusted: false, reason: 'lan' },
    ],
    warning: '', resolved_at: '2026-10-01T09:00:00Z',
  };

  it('shows what it trusts, what it never trusts, and why, in words', async () => {
    state = makeState({ trusted_proxies_effective: ['loopback', '172.18.0.0/16'], trusted_proxies_auto: containerAuto });
    const w = await mountPage();
    const card = w.get('[data-testid="login-proxies-auto"]');
    const said = card.text();
    expect(card.get('[data-testid="login-proxies-auto-why"]').text()).toBe(en.loginSecurity.proxies.auto.env.container);
    expect(card.get('[data-testid="login-proxies-auto-runtime"]').text()).toBe('Runtime: Docker');
    expect(card.get('[data-testid="login-proxies-auto-believes"]').text()).toContain(en.loginSecurity.proxies.auto.thisMachine);
    expect(card.get('[data-testid="login-proxies-auto-believes"]').text()).toContain('172.18.0.0/16');
    const excluded = card.get('[data-testid="login-proxies-auto-excluded"]').text();
    expect(excluded).toContain('172.18.0.1');
    expect(excluded).toContain('172.18.0.5');
    expect(excluded).toContain(en.loginSecurity.proxies.auto.excludedHint);
    const ifaces = card.get('[data-testid="login-proxies-auto-interfaces"]').text();
    expect(ifaces).toContain('eth0');
    expect(ifaces).toContain(en.loginSecurity.proxies.auto.reason['container-network']);
    expect(ifaces).toContain(en.loginSecurity.proxies.auto.reason.lan);
    expect(said, 'a wire code is never printed').not.toContain('container-network');
    expect(card.find('[data-testid="login-proxies-auto-unused"]').exists()).toBe(false);
    expect(card.find('[data-testid="login-proxies-auto-warning"]').exists()).toBe(false);
  });

  it.each([
    ['plain'], ['host-network'], ['kubernetes'], ['podman-rootless'], ['unknown'],
  ] as const)('says the %s environment in its own sentence', async (env) => {
    state = makeState({ trusted_proxies_auto: { ...containerAuto, environment: env, runtime: env === 'plain' ? '' : 'podman' } });
    const w = await mountPage();
    expect(w.get('[data-testid="login-proxies-auto-why"]').text()).toBe(en.loginSecurity.proxies.auto.env[env]);
    expect(w.find('[data-testid="login-proxies-auto-runtime"]').exists()).toBe(env !== 'plain');
  });

  it('reads a value it does not know as words, never the raw code', async () => {
    state = makeState({
      trusted_proxies_auto: {
        ...containerAuto, environment: 'martian', runtime: 'lxd-ish',
        interfaces: [{ name: 'x0', kind: 'weird', addresses: ['10.9.0.2/24'], trusted: false, reason: 'made_up' }],
      },
    });
    const w = await mountPage();
    const said = w.get('[data-testid="login-proxies-auto"]').text();
    expect(said).toContain(en.loginSecurity.proxies.auto.env.unknown);
    expect(said).toContain(en.loginSecurity.proxies.auto.reason.other);
    expect(said).toContain('Runtime: container');
    expect(said).not.toContain('martian');
    expect(said).not.toContain('made_up');
    expect(said).not.toContain('lxd-ish');
  });

  it('warns, with the detail, when automatic could not tell and fell back to this machine', async () => {
    state = makeState({
      trusted_proxies_auto: { ...containerAuto, environment: 'unknown', networks: [], excluded_gateways: [], excluded_self: [], warning: 'unreadable', warning_detail: 'netlink: operation not permitted' },
    });
    const w = await mountPage();
    const warn = w.get('[data-testid="login-proxies-auto-warning"]').text();
    expect(warn).toContain(en.loginSecurity.proxies.auto.warning.unreadable);
    expect(warn).toContain('netlink: operation not permitted');
  });

  it('switched on over a list that does not use it, says it is not in force yet', async () => {
    state = makeState({
      trusted_proxies_source: 'setting',
      trusted_defaults: { auto: false, loopback: true, private: false, link_local: false },
      trusted_proxies_auto: { ...containerAuto, in_use: false },
    });
    const w = await mountPage();
    expect(w.find('[data-testid="login-proxies-auto"]').exists()).toBe(false);
    await w.get('#login-proxies-class-auto').trigger('click');
    expect(w.get('[data-testid="login-proxies-auto-unused"]').text()).toBe(en.loginSecurity.proxies.auto.notInUse);
    await w.get('[data-testid="login-proxies-form"]').trigger('submit');
    await flushPromises();
    expect(patches()[0]?.body).toEqual({ trusted_proxies: ['auto', 'loopback'] });
  });
});

describe('Sign-in security - a peer that sends forwarded addresses and is not trusted', () => {
  const fwd = (address: string, over: Body = {}): Body => ({
    address, first_seen: '2026-10-01T08:00:00Z', last_seen: '2026-10-01T09:00:00Z', count: 12, public: false, relay: false, ...over,
  });

  it('names it and trusts it in one click: the default auto is kept, the address added', async () => {
    state = makeState({ untrusted_forwarders: [fwd('172.18.0.1', { relay: true })], untrusted_forwarders_total: 1 });
    const confirm = vi.spyOn(window, 'confirm').mockReturnValue(true);
    const w = await mountPage();
    const panel = w.get('[data-testid="login-forwarders"]');
    expect(panel.text()).toContain(en.loginSecurity.proxies.forwarders.title);
    expect(panel.text()).toContain('172.18.0.1');
    expect(panel.text()).toContain('12 requests');
    expect(panel.text()).toContain(en.loginSecurity.proxies.forwarders.relay);
    await w.get('[data-testid="login-forwarder-trust-172.18.0.1"]').trigger('click');
    await flushPromises();
    const asked = String(confirm.mock.calls[0]?.[0]);
    expect(asked).toContain('Trust 172.18.0.1 as a proxy?');
    expect(asked, 'it says what trusting a stranger costs').toContain('chooses the address they are counted by');
    expect(asked, 'and that a gateway carries what the host relays').toContain('a container network\'s gateway');
    expect(asked, 'not public').not.toContain('is a public address');
    expect(patches()[0]?.body).toEqual({ trusted_proxies: ['auto', '172.18.0.1'] });
    expect(w.find('[data-testid="login-forwarders"]').exists(), 'trusted now: the warning goes').toBe(false);
    expect(w.get('[data-testid="login-proxies-source"]').text()).toBe(en.loginSecurity.proxies.source.setting);
    confirm.mockRestore();
  });

  it('adds to the list in force when it is a saved one, and warns about a public address', async () => {
    state = makeState({
      trusted_proxies_source: 'setting',
      settings: { ...(makeState().settings as Body), trusted_proxies: ['loopback', '192.0.2.0/24'] },
      trusted_defaults: { auto: false, loopback: true, private: false, link_local: false },
      trusted_addresses: ['192.0.2.0/24'],
      untrusted_forwarders: [fwd('203.0.113.9', { public: true, count: 1 })],
      untrusted_forwarders_total: 1,
    });
    const confirm = vi.spyOn(window, 'confirm').mockReturnValue(true);
    const w = await mountPage();
    expect(w.get('[data-testid="login-forwarders"]').text()).toContain('1 request,');
    await w.get('[data-testid="login-forwarder-trust-203.0.113.9"]').trigger('click');
    await flushPromises();
    expect(String(confirm.mock.calls[0]?.[0])).toContain('203.0.113.9 is a public address');
    expect(patches()[0]?.body).toEqual({ trusted_proxies: ['loopback', '192.0.2.0/24', '203.0.113.9'] });
    confirm.mockRestore();
  });

  it('changes nothing when the person says no', async () => {
    state = makeState({ untrusted_forwarders: [fwd('172.18.0.1')], untrusted_forwarders_total: 1 });
    const confirm = vi.spyOn(window, 'confirm').mockReturnValue(false);
    const w = await mountPage();
    await w.get('[data-testid="login-forwarder-trust-172.18.0.1"]').trigger('click');
    await flushPromises();
    expect(patches()).toHaveLength(0);
    expect(w.find('[data-testid="login-forwarders"]').exists()).toBe(true);
    confirm.mockRestore();
  });

  it('shows the busiest three, the rest on request, and how many the server did not send', async () => {
    state = makeState({
      untrusted_forwarders: ['198.51.100.1', '198.51.100.2', '198.51.100.3', '198.51.100.4', '2001:db8::5'].map((a) => fwd(a)),
      untrusted_forwarders_total: 7,
    });
    const w = await mountPage();
    const rows = () => w.findAll('[data-testid^="login-forwarder-trust-"]');
    expect(rows()).toHaveLength(3);
    expect(w.get('[data-testid="login-forwarders-more"]').text()).toBe('2 more not listed here.');
    await w.get('[data-testid="login-forwarders-toggle"]').trigger('click');
    expect(rows()).toHaveLength(5);
    expect(w.find('[data-testid="login-forwarder-trust-2001:db8::5"]').exists(), 'an IPv6 address can be trusted too').toBe(true);
  });

  it('on a public demo the address is masked, and there is nothing to trust', async () => {
    state = makeState({ untrusted_forwarders: [fwd('hidden on the demo', { public: true })], untrusted_forwarders_total: 1 });
    const w = await mountPage();
    expect(w.get('[data-testid="login-forwarders"]').text()).toContain('hidden on the demo');
    expect(w.findAll('[data-testid^="login-forwarder-trust-"]')).toHaveLength(0);
  });

  it('is not there when no peer sends one', async () => {
    const w = await mountPage();
    expect(w.find('[data-testid="login-forwarders"]').exists()).toBe(false);
  });
});

describe('Sign-in security — locks', () => {
  it('draws one row per counter with its status in words', async () => {
    const w = await mountPage();
    const rows = w.findAll('[data-testid="login-locks"] .fe-list__row');
    expect(rows).toHaveLength(2);
    expect(rows[0].text()).toContain('ada@example.com');
    expect(rows[0].text()).toContain('5 of 5');
    expect(rows[0].text()).toContain('Locked for 4 min');
    expect(rows[1].text()).toContain('Counting');
    expect(rows[1].text()).toContain('SFTP');
  });

  it('lifts one lock by its row action, after asking', async () => {
    const confirm = vi.spyOn(window, 'confirm').mockReturnValue(true);
    const w = await mountPage();
    await openRowMenu(w, 'login-lock-actions-acct-1');
    await pickMenuItem('login-lock-actions-acct-1-unlock');
    await flushPromises();
    expect(calls.find((c) => c.url.endsWith('/unlock'))?.body).toEqual({ scope: 'account', subject: 'ada@example.com' });
    expect(confirm).toHaveBeenCalled();
    confirm.mockRestore();
  });

  it('lifts every lock at once, and does nothing when the person says no', async () => {
    const confirm = vi.spyOn(window, 'confirm').mockReturnValue(false);
    const w = await mountPage();
    await w.get('[data-testid="login-unlock-all"]').trigger('click');
    expect(calls.some((c) => c.url.endsWith('/unlock'))).toBe(false);
    confirm.mockReturnValue(true);
    await w.get('[data-testid="login-unlock-all"]').trigger('click');
    await flushPromises();
    expect(calls.find((c) => c.url.endsWith('/unlock'))?.body).toEqual({ all: true });
    confirm.mockRestore();
  });

  it('asks the server for only the locks in force when the box is ticked', async () => {
    const w = await mountPage();
    await w.get('[data-testid="login-locks"] input[type="checkbox"]').setValue(true);
    await flushPromises();
    const last = [...calls].reverse().find((c) => c.url.endsWith('/locks'));
    expect(last?.params).toMatchObject({ locked: 1 });
  });
});

describe('Sign-in security — the trail', () => {
  it('says each event, door and reason in words — an unknown wire value says Other', async () => {
    const w = await mountPage();
    const rows = w.findAll('[data-testid="login-attempts"] .fe-list__row');
    expect(rows).toHaveLength(2);
    expect(rows[0].text()).toContain('Locked');
    expect(rows[0].text()).toContain('Web form');
    expect(rows[0].text()).toContain('Wrong password');
    const said = rows[1].text();
    expect(said).toContain('Wrong attempt');
    expect(said).not.toContain('weird-door');
    expect(said).not.toContain('made_up_reason');
    expect(said).not.toContain('login.failed');
  });

  it('narrows to one event kind through the server', async () => {
    const w = await mountPage();
    await pickOption(w.get('[data-testid="login-attempt-filter"]'), 'login.failed');
    await flushPromises();
    const last = [...calls].reverse().find((c) => c.url.endsWith('/attempts'));
    expect(last?.params).toMatchObject({ action: 'login.failed', limit: 50 });
  });

  // An administrator's unlock and settings change are in the trail whichever
  // door they used - the panel, an API key, an MCP tool - and the Door column
  // says which (backend: login_security.go loginAttemptOf, `via`).
  it("names an administrator's door, the fields a change touched, and an unlock of everything", async () => {
    attempts = [
      { id: 14, action: 'login_security.update', via: 'mcp', changed_fields: ['ip_max_fails', 'window_seconds'], at: '2026-09-30T10:04:00Z' },
      { id: 13, action: 'login.unlocked', reason: 'admin', via: 'api', identifier: 'b@example.com', scope: 'account', at: '2026-09-30T10:03:00Z' },
      { id: 12, action: 'login.unlocked', reason: 'admin', via: 'panel', ip: '198.51.100.4', scope: 'ip', at: '2026-09-30T10:02:00Z' },
      { id: 11, action: 'login.unlocked', reason: 'admin', via: 'mcp', metadata: { all: true, unlocked: 3 }, at: '2026-09-30T10:01:00Z' },
      { id: 10, action: 'login_security.update', via: 'carrier-pigeon', changed_fields: ['made_up'], at: '2026-09-30T10:00:00Z' },
    ];
    const w = await mountPage();
    const rows = w.findAll('[data-testid="login-attempts"] .fe-list__row').map((r) => r.text());
    expect(rows).toHaveLength(5);
    expect(rows[0]).toContain(en.loginSecurity.attempts.action.settings_changed);
    expect(rows[0]).toContain('MCP');
    expect(rows[0]).toContain('Changed: Wrong attempts per address, Counting window');
    expect(rows[1]).toContain(en.loginSecurity.attempts.action.unlocked);
    expect(rows[1]).toContain('b@example.com');
    expect(rows[1]).toContain('API key');
    expect(rows[1]).toContain('Lifted by an administrator');
    expect(rows[2]).toContain('198.51.100.4');
    expect(rows[2]).toContain('Admin panel');
    expect(rows[3]).toContain('All locks (3)');
    expect(rows[4]).toContain('Other');
    for (const r of rows) {
      expect(r).not.toContain('login_security.update');
      expect(r).not.toContain('carrier-pigeon');
      expect(r).not.toContain('made_up');
    }
  });

  it('offers the settings changes in the event filter', async () => {
    const w = await mountPage();
    const options = (await listedOptions(w.get('[data-testid="login-attempt-filter"]'))).map((o) => [o.value, o.label]);
    expect(options).toContainEqual(['login_security.update', en.loginSecurity.attempts.action.settings_changed]);
    await pickOption(w.get('[data-testid="login-attempt-filter"]'), 'login_security.update');
    await flushPromises();
    const last = [...calls].reverse().find((c) => c.url.endsWith('/attempts'));
    expect(last?.params).toMatchObject({ action: 'login_security.update' });
  });
});

// On a public demo the server writes "hidden on the demo" in place of every
// address (handlers/demo_redact.go). The page says it in the reader's
// language, keeps two masked rows two rows, and offers no action on one.
describe('Sign-in security - a public demo', () => {
  const MASK = 'hidden on the demo';
  beforeEach(() => {
    locks = [
      { id: 'm1', scope: 'ip', subject: MASK, fails: 3, limit: 10, lock_level: 1, locked: true, locked_until: '2026-09-30T10:04:00Z', retry_after: 240, window_start: '2026-09-30T09:58:00Z', last_fail_at: '2026-09-30T10:00:00Z', last_ip: MASK, last_protocol: 'web' },
      { id: 'm2', scope: 'ip', subject: MASK, fails: 3, limit: 10, lock_level: 1, locked: true, locked_until: '2026-09-30T10:04:00Z', retry_after: 240, window_start: '2026-09-30T09:58:00Z', last_fail_at: '2026-09-30T10:00:00Z', last_ip: MASK, last_protocol: 'dav' },
      { id: 'a1', scope: 'account', subject: 'ada@example.com', fails: 2, limit: 5, lock_level: 0, locked: false, retry_after: 0, window_start: '2026-09-30T09:58:00Z', last_fail_at: '2026-09-30T10:00:00Z', last_ip: MASK, last_protocol: 'web' },
      // A name typed that is no account of the demo is a visitor's, masked too.
      { id: 'a2', scope: 'account', subject: MASK, fails: 1, limit: 5, lock_level: 0, locked: false, retry_after: 0, window_start: '2026-09-30T09:58:00Z', last_fail_at: '2026-09-30T10:00:00Z', last_ip: MASK, last_protocol: 'web' },
    ];
    attempts = [
      { id: 3, action: 'login.locked', ip: MASK, scope: 'ip', protocol: 'web', at: '2026-09-30T10:00:00Z' },
      { id: 2, action: 'login.unlocked', reason: 'admin', via: 'panel', ip: MASK, scope: 'ip', at: '2026-09-30T09:00:00Z' },
    ];
    state = makeState({
      settings: { ...(makeState().settings as Body), ip_allowlist: [MASK, MASK] },
      trusted_proxies_effective: ['loopback', MASK],
      trusted_defaults: { loopback: true, private: false, link_local: false },
      trusted_addresses: [MASK],
    });
  });

  it('keeps two masked locks two rows, says the mask in words, and offers no action on them', async () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {});
    const w = await mountPage();
    const rows = w.findAll('[data-testid="login-locks"] .fe-list__row');
    expect(rows).toHaveLength(4);
    expect(rows[0].text()).toContain(en.demo.hiddenAddress);
    expect(rows[1].text()).toContain(en.demo.hiddenAddress);
    expect(w.find('[data-testid="login-lock-actions-m1"]').exists(), 'a masked address names nothing to unlock').toBe(false);
    expect(w.find('[data-testid="login-lock-actions-m2"]').exists()).toBe(false);
    expect(w.find('[data-testid="login-lock-actions-a2"]').exists(), 'nor does a masked name').toBe(false);
    expect(w.find('[data-testid="login-lock-actions-a1"]').exists(), 'an account row keeps its action').toBe(true);
    expect(warn.mock.calls.flat().join(' ')).not.toMatch(/[Dd]uplicate keys/);
    warn.mockRestore();
  });

  it('shows masked allow-list and proxy entries as chips that cannot be removed', async () => {
    const w = await mountPage();
    const chips = w.findAll('[data-testid="login-allowlist"] [data-testid="address-chips"] li');
    expect(chips.map((c) => c.text())).toEqual([en.demo.hiddenAddress, en.demo.hiddenAddress]);
    expect(chips.every((c) => !c.find('button').exists())).toBe(true);
    const proxies = w.findAll('[data-testid="login-proxies"] [data-testid="address-chips"] li').map((c) => c.text());
    expect(proxies).toEqual([en.demo.hiddenAddress]);
  });

  it('says the mask in the trail, in Turkish too', async () => {
    let w = await mountPage();
    let rows = w.findAll('[data-testid="login-attempts"] .fe-list__row').map((r) => r.text());
    expect(rows[0]).toContain(en.demo.hiddenAddress);
    expect(rows[1]).toContain(en.demo.hiddenAddress);
    w.unmount();
    mounted.pop();
    w = await mountPage('tr');
    rows = w.findAll('[data-testid="login-attempts"] .fe-list__row').map((r) => r.text());
    expect(rows[0]).toContain('demoda gizli');
    expect(rows[0]).not.toContain(MASK);
    expect(w.findAll('[data-testid="login-locks"] .fe-list__row')[0].text()).toContain('demoda gizli');
  });

  // The trusted-proxy detail (the untrusted forwarders, what `auto` found) is
  // masked by the server too; the page says the mask in words, keeps two
  // masked entries two entries, and offers no Trust on one.
  it('says the mask in the untrusted forwarders and the automatic card, in Turkish too', async () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {});
    state = {
      ...state,
      untrusted_forwarders: [
        { address: MASK, first_seen: '2026-10-01T08:00:00Z', last_seen: '2026-10-01T09:00:00Z', count: 3, public: true, relay: false },
        { address: MASK, first_seen: '2026-10-01T08:00:00Z', last_seen: '2026-10-01T09:05:00Z', count: 1, public: false, relay: true },
      ],
      untrusted_forwarders_total: 2,
      trusted_defaults: { loopback: true, private: false, link_local: false, auto: true },
      trusted_proxies_auto: {
        in_use: true, environment: 'container', runtime: 'docker',
        networks: [MASK, MASK], excluded_gateways: [MASK], excluded_self: [MASK],
        interfaces: [{ name: 'eth0', kind: 'veth', addresses: [MASK], trusted: true, reason: 'container-network' }],
        warning: '', resolved_at: '2026-10-01T09:00:00Z',
      },
    };
    for (const locale of ['en', 'tr'] as const) {
      const w = await mountPage(locale);
      const hidden = locale === 'en' ? en.demo.hiddenAddress : tr.demo.hiddenAddress;
      const fwd = w.get('[data-testid="login-forwarders"]');
      expect(fwd.findAll('li')).toHaveLength(2);
      expect(fwd.text()).toContain(hidden);
      expect(w.findAll('[data-testid^="login-forwarder-trust-"]'), 'a masked peer names nothing to trust').toHaveLength(0);
      const card = w.get('[data-testid="login-proxies-auto"]');
      expect(card.findAll('[data-testid="login-proxies-auto-believes"] span').map((s) => s.text()).filter((x) => x === hidden)).toHaveLength(2);
      expect(card.text()).toContain(hidden);
      if (locale === 'tr') {
        expect(fwd.text()).not.toContain(MASK);
        expect(card.text()).not.toContain(MASK);
      }
      w.unmount();
      mounted.pop();
    }
    expect(warn.mock.calls.flat().join(' ')).not.toMatch(/[Dd]uplicate keys/);
    warn.mockRestore();
  });
});

describe('Sign-in security — a tenant administrator', () => {
  it('gets the server sentence and no form', async () => {
    refuse = { status: 403, data: { error: 'supertenant_only', message: 'managed by the platform operator' } };
    const w = await mountPage();
    expect(w.get('[data-testid="login-security-error"]').text()).toContain('managed by the platform operator');
    expect(w.find('form').exists()).toBe(false);
  });
});

describe('Sign-in security — Turkish', () => {
  it('speaks Turkish, with the right characters', async () => {
    const w = await mountPage('tr');
    const text = w.text();
    expect(text).toContain('Giriş güvenliği');
    expect(text).toContain('İzinli adresler');
    expect(text).toContain('Güvenilir vekil sunucular');
    expect(text).toContain('Adresimi ekle (203.0.113.9)');
    expect(text).toContain('Özel ağlar');
    expect(text).toContain('Bağlantı-yerel adresler');
    expect(text).toContain('Otomatik (auto)');
    expect(text).toContain('Otomatik: yerleşik varsayılan');
    expect(text).toContain('filex konteynerde değil, doğrudan bu makinede çalışıyor');
    expect(text).not.toContain('Giris guvenligi');
  });

  it('names an untrusted peer in Turkish, with the right characters', async () => {
    state = makeState({
      untrusted_forwarders: [{ address: '172.18.0.1', first_seen: '2026-10-01T08:00:00Z', last_seen: '2026-10-01T09:00:00Z', count: 3, public: false, relay: true }],
      untrusted_forwarders_total: 1,
    });
    const w = await mountPage('tr');
    const said = w.get('[data-testid="login-forwarders"]').text();
    expect(said).toContain('Güvenilmeyen bir eş, iletilen adres gönderiyor');
    expect(said).toContain('3 istek, son:');
    expect(said).toContain('ağ geçidi ya da filex\'in kendi adresi');
    expect(said).toContain('172.18.0.1 adresine güven');
  });

  it("names an administrator's door and a settings change in Turkish", async () => {
    attempts = [
      { id: 2, action: 'login_security.update', via: 'api', changed_fields: ['account_max_fails'], at: '2026-09-30T10:01:00Z' },
      { id: 1, action: 'login.unlocked', reason: 'admin', via: 'panel', identifier: 'ada@example.com', at: '2026-09-30T10:00:00Z' },
    ];
    const w = await mountPage('tr');
    const rows = w.findAll('[data-testid="login-attempts"] .fe-list__row').map((r) => r.text());
    expect(rows[0]).toContain('Ayarlar değiştirildi');
    expect(rows[0]).toContain('API anahtarı');
    expect(rows[0]).toContain('Değişen: ' + tr.loginSecurity.limit.accountMaxFails);
    expect(rows[1]).toContain('Yönetim paneli');
  });
});
