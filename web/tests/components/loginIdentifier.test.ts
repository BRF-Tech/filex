// The sign-in form does not advertise an account.
//
// ⚠ QA, 2026-09-21: the identifier box carried `placeholder="admin@local"` —
// the address filex gives the administrator it creates on first run. On every
// install it told a visitor which account to guess a password for, and on an
// install where that account was renamed it was simply wrong. The label above
// the box ("E-mail or username") already says what goes in it.
import { describe, it, expect, beforeEach, vi } from 'vitest';
import { mount } from '@vue/test-utils';
import { createPinia, setActivePinia } from 'pinia';
import { createI18n } from 'vue-i18n';

import Login from '@/views/Login.vue';
import en from '@/locales/en.json';

vi.mock('@/api/branding', () => ({
  BrandingApi: { get: vi.fn().mockResolvedValue({}) },
}));
vi.mock('vue-router', () => ({
  useRoute: () => ({ query: {} }),
  useRouter: () => ({ push: vi.fn() }),
}));

describe('sign-in identifier', () => {
  beforeEach(() => setActivePinia(createPinia()));

  it('names no account in its placeholder', () => {
    const i18n = createI18n({ legacy: false, locale: 'en', messages: { en } });
    const w = mount(Login, {
      global: {
        plugins: [i18n],
        stubs: { LogoMark: true, LocaleSwitcher: true, DarkModeToggle: true, Input: true, Checkbox: true },
      },
    });
    const box = w.find('input#email');
    expect(box.exists(), 'the password form is drawn by default').toBe(true);
    expect(box.attributes('placeholder') ?? '').not.toMatch(/@/);
    expect(w.text()).toContain(en.login.identifier);
  });
});
