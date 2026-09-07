// The demo landing's "Open the demo" CTA must submit the credentials the
// SERVER configured, not a constant baked into the page.
//
// Red proof for the defect this replaced: the button called
// auth.login({ password: 'demo' }) unconditionally. FILEX_DEMO_PASS was
// parsed by the config loader and documented in docs/CONFIGURATION.md, so an
// operator who changed it believed they had secured the demo — and instead
// broke the only button on the page, plus the credentials hint printed under
// it, both of which still said "demo".
import { describe, it, expect, beforeEach, vi } from 'vitest';
import { mount } from '@vue/test-utils';
import { createPinia, setActivePinia } from 'pinia';
import { createI18n } from 'vue-i18n';

import Login from '@/views/Login.vue';
import { useAuthStore } from '@/stores/auth';
import { useCapabilitiesStore } from '@/stores/capabilities';
import en from '@/locales/en.json';

vi.mock('@/api/branding', () => ({
  BrandingApi: { get: vi.fn().mockResolvedValue({}) },
}));

const push = vi.fn();

function mountLogin() {
  const i18n = createI18n({ legacy: false, locale: 'en', messages: { en } });
  return mount(Login, {
    global: {
      plugins: [i18n],
      mocks: {},
      stubs: {
        LogoMark: true,
        LocaleSwitcher: true,
        DarkModeToggle: true,
        Input: true,
        Checkbox: true,
      },
      provide: {},
      config: {
        globalProperties: {},
      },
      // vue-router is not installed in the test app; stub the two composables
      // the view uses via component-level mocks below.
    },
  });
}

vi.mock('vue-router', () => ({
  useRoute: () => ({ query: {} }),
  useRouter: () => ({ push }),
}));

describe('demo landing CTA', () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    push.mockClear();
  });

  it('submits the password the server published, not a hardcoded one', async () => {
    const caps = useCapabilitiesStore();
    caps.data = {
      ...caps.data,
      demo_mode: true,
      demo_user: 'demo@demo.com',
      demo_pass: 's3cret',
    };
    const auth = useAuthStore();
    const login = vi.spyOn(auth, 'login').mockResolvedValue(true);

    const wrapper = mountLogin();
    // The CTA is the only primary button on the demo landing.
    const cta = wrapper.findAll('button').find((b) => b.text().includes(en.demo.openCta));
    expect(cta, 'demo CTA rendered').toBeTruthy();
    await cta!.trigger('click');

    expect(login).toHaveBeenCalledWith(
      expect.objectContaining({ email: 'demo@demo.com', password: 's3cret' }),
    );
    // …and the hint under the button quotes the same password.
    expect(wrapper.text()).toContain('s3cret');
  });

  it('falls back to "demo" when the server does not publish one', async () => {
    const caps = useCapabilitiesStore();
    caps.data = { ...caps.data, demo_mode: true, demo_user: 'demo@demo.com', demo_pass: '' };
    const auth = useAuthStore();
    const login = vi.spyOn(auth, 'login').mockResolvedValue(true);

    const wrapper = mountLogin();
    const cta = wrapper.findAll('button').find((b) => b.text().includes(en.demo.openCta));
    await cta!.trigger('click');
    expect(login).toHaveBeenCalledWith(expect.objectContaining({ password: 'demo' }));
  });
});
