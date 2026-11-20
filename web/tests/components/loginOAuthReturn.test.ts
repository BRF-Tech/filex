import { beforeEach, afterEach, describe, expect, it, vi } from 'vitest';
import { flushPromises, mount } from '@vue/test-utils';
import { createPinia, setActivePinia } from 'pinia';
import { createI18n } from 'vue-i18n';
import Login from '@/views/Login.vue';
import { useAuthStore } from '@/stores/auth';
import { useCapabilitiesStore } from '@/stores/capabilities';
import en from '@/locales/en.json';

vi.mock('@/api/captcha', () => ({ CaptchaPublicApi: { config: async () => null } }));
vi.mock('@/api/branding', () => ({ BrandingApi: { get: async () => ({}) } }));
const state = vi.hoisted(() => ({ query: {} as Record<string, unknown>, push: vi.fn(), navigate: vi.fn() }));
vi.mock('vue-router', () => ({ useRoute: () => ({ query: state.query }), useRouter: () => ({ push: state.push }) }));
vi.mock('@/lib/realmHandoff', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/lib/realmHandoff')>()),
  navigateTo: (target: string) => state.navigate(target),
}));

function mountLogin() {
  return mount(Login, { global: {
    plugins: [createI18n({ legacy: false, locale: 'en', messages: { en } })],
    stubs: { LogoMark: true, Input: true, Checkbox: true, RouterLink: true },
  } });
}

const target = '/api/oauth/authorize?request=0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef';

describe('OAuth login continuation', () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    const caps = useCapabilitiesStore();
    caps.loaded = true;
    caps.data = { ...caps.data, auth_drivers: ['local'] };
    state.query = { redirect: target };
    state.push.mockClear();
    state.navigate.mockClear();
    window.history.replaceState({}, '', '/admin/login');
  });
  afterEach(() => window.history.replaceState({}, '', '/'));

  it('leaves the SPA for backend authorization after a password login', async () => {
    vi.spyOn(useAuthStore(), 'login').mockResolvedValue(true);
    const wrapper = mountLogin();
    await flushPromises();
    await wrapper.get('#email').setValue('alex');
    await wrapper.get('#password').setValue('pw');
    await wrapper.get('form').trigger('submit');
    await flushPromises();
    expect(state.navigate).toHaveBeenCalledWith(target);
    expect(state.push).not.toHaveBeenCalled();
    wrapper.unmount();
  });

  it('leaves the SPA for backend authorization after redeeming a handoff', async () => {
    window.location.hash = '#handoff=ticket';
    vi.spyOn(useAuthStore(), 'redeemHandoff').mockResolvedValue(true);
    const wrapper = mountLogin();
    await flushPromises();
    expect(state.navigate).toHaveBeenCalledWith(target);
    expect(state.push).not.toHaveBeenCalled();
    wrapper.unmount();
  });
});
