// A refused sign-in says how many tries are left, or how long a lock still
// holds — read from the server's 401 / 429 bodies
// (backend/internal/api/handlers/auth.go, loginFailed / writeLocked).
//
// ⚠ The countdown is the client's clock, so it is driven here with fake
// timers; it ends by itself and gives the button back without a request.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils';
import { createPinia, setActivePinia } from 'pinia';
import { createI18n } from 'vue-i18n';

import Login from '@/views/Login.vue';
import { useAuthStore } from '@/stores/auth';
import { clock, readLoginRefusal } from '@/lib/loginRefusal';
import en from '@/locales/en.json';
import tr from '@/locales/tr.json';

let answer: { status: number; data: Record<string, unknown>; headers?: Record<string, string> } | null = null;

vi.mock('@/api/branding', () => ({ BrandingApi: { get: vi.fn().mockResolvedValue({}) } }));
vi.mock('@/api/capabilities', () => ({ CapabilitiesApi: { fetch: vi.fn(async () => ({})) } }));
vi.mock('vue-router', () => ({
  useRoute: () => ({ query: {} }),
  useRouter: () => ({ push: vi.fn() }),
}));
vi.mock('@/api/auth', () => ({
  AuthApi: {
    login: vi.fn(async () => {
      throw Object.assign(new Error('refused'), {
        isAxiosError: true,
        response: { status: answer?.status, data: answer?.data, headers: answer?.headers ?? {} },
      });
    }),
    me: vi.fn(),
    oidcStartUrl: vi.fn(() => '/oidc'),
  },
}));
vi.mock('@/api/client', () => ({
  extractError: (e: unknown, f: string) =>
    (e as { response?: { data?: { message?: string } } }).response?.data?.message ?? f,
  api: { get: vi.fn(), post: vi.fn() },
}));
vi.mock('axios', async (orig) => {
  const real = await orig<typeof import('axios')>();
  const isAxiosError = (e: unknown) => !!(e as { isAxiosError?: boolean })?.isAxiosError;
  return { ...real, isAxiosError, default: { ...real.default, isAxiosError } };
});

const err = (status: number, data: Record<string, unknown>, headers: Record<string, string> = {}) =>
  Object.assign(new Error('x'), { isAxiosError: true, response: { status, data, headers } });

describe('readLoginRefusal', () => {
  it('reads the tries left from a 401', () => {
    expect(readLoginRefusal(err(401, { error: 'x', message: 'm', remaining: 3, limit: 5, scope: 'account' }))).toEqual({
      status: 401, locked: false, scope: 'account', remaining: 3, limit: 5, totp: false,
    });
  });

  it('reads the wait from a 429, from the body first and the header second', () => {
    expect(readLoginRefusal(err(429, { locked: true, scope: 'ip', retry_after: 90 }))).toMatchObject({
      locked: true, scope: 'ip', retryAfter: 90,
    });
    expect(readLoginRefusal(err(429, { locked: true }, { 'retry-after': '45' }))).toMatchObject({ retryAfter: 45 });
  });

  it('says nothing for any other answer or a missing one', () => {
    expect(readLoginRefusal(err(500, {}))).toBeNull();
    expect(readLoginRefusal(new Error('no response'))).toBeNull();
  });

  it('writes a clock', () => {
    expect(clock(245)).toBe('4:05');
    expect(clock(9)).toBe('0:09');
    expect(clock(-3)).toBe('0:00');
  });
});

describe('the sign-in page after a refusal', () => {
  const mounted: VueWrapper[] = [];

  async function mountLogin(locale: 'en' | 'tr' = 'en') {
    setActivePinia(createPinia());
    const i18n = createI18n({ legacy: false, locale, fallbackLocale: 'en', messages: { en, tr } });
    const w = mount(Login, {
      global: {
        plugins: [i18n],
        stubs: { LogoMark: true, LocaleSwitcher: true, DarkModeToggle: true, RouterLink: true },
      },
    });
    mounted.push(w);
    await flushPromises();
    return w;
  }

  async function attempt(w: VueWrapper) {
    await w.get('input#email').setValue('ada@example.com');
    await w.get('input#password').setValue('nope');
    await w.get('form').trigger('submit');
    await flushPromises();
  }

  beforeEach(() => vi.useFakeTimers({ shouldAdvanceTime: false, toFake: ['setInterval', 'clearInterval', 'Date'] }));
  afterEach(() => {
    while (mounted.length) mounted.pop()!.unmount();
    vi.useRealTimers();
    answer = null;
  });

  it('says how many tries are left before a lock', async () => {
    answer = { status: 401, data: { error: 'invalid credentials', message: 'server words', remaining: 3, limit: 5, scope: 'account' } };
    const w = await mountLogin();
    await attempt(w);
    expect(w.get('[role="alert"]').text()).toBe('Email, username or password is wrong. Attempts left before a lock: 3.');
  });

  it('counts a lock down, holds the button, and gives it back at zero', async () => {
    answer = { status: 429, data: { error: 'too many attempts', message: 'server words', locked: true, scope: 'account', retry_after: 5 } };
    const w = await mountLogin();
    await attempt(w);
    expect(w.get('[role="alert"]').text()).toBe('Too many wrong attempts for this account. Try again in 0:05.');
    const button = () => w.get('button[type="submit"]');
    expect(button().attributes('disabled')).toBeDefined();
    expect(button().text()).toContain('Try again in 0:05');

    vi.advanceTimersByTime(2000);
    await flushPromises();
    expect(w.get('[role="alert"]').text()).toContain('0:03');

    vi.advanceTimersByTime(4000);
    await flushPromises();
    expect(w.find('[role="alert"]').exists()).toBe(false);
    expect(button().attributes('disabled')).toBeUndefined();
    expect(button().text()).toContain(en.login.submit);
  });

  it('names the address, not the account, when the address is locked', async () => {
    answer = { status: 429, data: { locked: true, scope: 'ip', retry_after: 65 } };
    const w = await mountLogin();
    await attempt(w);
    expect(w.get('[role="alert"]').text()).toBe('Too many wrong attempts from this address. Try again in 1:05.');
  });

  it('speaks Turkish', async () => {
    answer = { status: 429, data: { locked: true, scope: 'account', retry_after: 30 } };
    const w = await mountLogin('tr');
    await attempt(w);
    expect(w.get('[role="alert"]').text()).toBe('Bu hesap için çok fazla hatalı deneme yapıldı. 0:30 sonra tekrar deneyin.');
  });

  it('falls back to the server sentence when it gave no count', async () => {
    answer = { status: 401, data: { error: 'invalid credentials', message: 'Wrong email or password.' } };
    const w = await mountLogin();
    await attempt(w);
    expect(w.get('[role="alert"]').text()).toBe('Wrong email or password.');
  });
});
