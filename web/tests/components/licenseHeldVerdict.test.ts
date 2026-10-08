// Whether a paid app runs is the SERVER's verdict (0.54 audit, B5).
//
// The server judges a license once (appstore judge → View.Held) and holds the
// app by that verdict; the License section and the install review read a
// second copy of the rule here (`licenseRuns(status)`: free | valid | grace).
// The two agreed only while nobody changed either. The panel now reads
// `held` and nothing else: a status the copy would have called running, with
// the server holding the app, shows as held - and the other way round.
import { readFileSync } from 'node:fs';
import path from 'node:path';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { flushPromises, mount } from '@vue/test-utils';
import { createPinia, setActivePinia } from 'pinia';
import { createI18n } from 'vue-i18n';

import en from '@/locales/en.json';

let licenseAnswer: Record<string, unknown> = {};

vi.mock('@/api/client', () => ({
  extractError: (_e: unknown, f: string) => f,
  api: {
    get: vi.fn(async (url: string) => {
      if (url.endsWith('/license')) return { data: licenseAnswer };
      throw new Error('unexpected GET ' + url);
    }),
    post: vi.fn(),
    put: vi.fn(),
    delete: vi.fn(),
  },
}));

import AppPluginLicense from '@/components/plugins/AppPluginLicense.vue';

const SRC = path.resolve(__dirname, '../../src');
const global = () => ({
  plugins: [createI18n({ legacy: false, locale: 'en', fallbackLocale: 'en', messages: { en } })],
  stubs: { RouterLink: { template: '<a><slot /></a>' } },
});

describe('a paid app runs when the server says it does', () => {
  beforeEach(() => setActivePinia(createPinia()));

  it('held by the server: shown as held, whatever the status word', async () => {
    licenseAnswer = { app: 'sign', required: true, status: 'valid', held: true, store_trusted: true };
    const w = mount(AppPluginLicense, { props: { pluginId: 4 }, global: global() });
    await flushPromises();
    expect(w.find('[data-testid="app-plugin-license-held"]').exists()).toBe(true);
  });

  it('not held by the server: not shown as held, whatever the status word', async () => {
    licenseAnswer = { app: 'sign', required: true, status: 'revoked', held: false, store_trusted: true };
    const w = mount(AppPluginLicense, { props: { pluginId: 4 }, global: global() });
    await flushPromises();
    expect(w.find('[data-testid="app-plugin-license-held"]').exists()).toBe(false);
  });

  it('the panel keeps no copy of the rule', () => {
    for (const f of ['api/appStore.ts', 'components/plugins/AppPluginLicense.vue', 'components/plugins/AppPluginInstallWizard.vue']) {
      expect(readFileSync(path.join(SRC, f), 'utf8'), f).not.toMatch(/\blicenseRuns\b/);
    }
  });
});
