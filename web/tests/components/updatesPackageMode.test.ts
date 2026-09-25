// Ops → Updates on an install a package manager owns (Homebrew, winget, Snap).
//
// filex does not replace a binary a package manager owns (docs/UPDATES.md →
// "Package-manager installs"), so the card must not offer "Upgrade now" and
// must say, in the reader's language, why the command it shows is the
// manager's and not filex's own. The command itself comes from the server.
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { flushPromises, mount } from '@vue/test-utils';
import { createPinia, setActivePinia } from 'pinia';
import { createI18n } from 'vue-i18n';

import en from '@/locales/en.json';
import tr from '@/locales/tr.json';
import type { UpdateStatus } from '@/api/updates';

const state = vi.hoisted(() => ({ status: null as unknown as UpdateStatus }));

vi.mock('@/api/updates', () => ({
  UpdatesApi: {
    status: vi.fn(async () => state.status),
    check: vi.fn(async () => state.status),
    apply: vi.fn(),
  },
}));

import Updates from '@/views/Updates.vue';

function status(over: Partial<UpdateStatus>): UpdateStatus {
  return {
    current: 'v0.43.2',
    enabled: true,
    policy: 'patch',
    mode: 'binary',
    can_self_apply: true,
    restart_required: false,
    action: 'confirm',
    step: 'patch',
    reason: 'server sentence',
    latest: { version: 'v0.43.3' },
    ...over,
  };
}

const brew = (): UpdateStatus =>
  status({
    mode: 'package',
    can_self_apply: false,
    package_manager: 'homebrew',
    package_manager_name: 'Homebrew',
    upgrade_command: 'brew upgrade --cask filex',
    action: 'instruct',
    instructions: ['brew upgrade --cask filex', '# then restart filex, so the new version is the one running'],
  });

async function mountIn(locale: 'en' | 'tr') {
  const i18n = createI18n({ legacy: false, locale, fallbackLocale: 'en', messages: { en, tr } });
  const w = mount(Updates, { global: { plugins: [i18n] } });
  await flushPromises();
  return w;
}

const fill = (s: string, manager: string) => s.replaceAll('{manager}', manager);

describe('Updates: a package-manager install', () => {
  beforeEach(() => setActivePinia(createPinia()));

  for (const [locale, cat] of [['en', en], ['tr', tr]] as const) {
    it(`${locale}: names the manager, says why, shows its command — and offers no "Upgrade now"`, async () => {
      state.status = brew();
      const w = await mountIn(locale);

      expect(w.get('[data-testid="updates-mode"]').text()).toBe(fill(cat.updates.mode.package, 'Homebrew'));
      const why = w.get('[data-testid="updates-package-howto"]').text();
      expect(why).toBe(fill(cat.updates.packageHowTo, 'Homebrew'));
      // Rendered, not the raw template: a placeholder that did not fill would
      // still be "in" the catalogue text a naive assertion compares with.
      expect(why).not.toContain('{');

      const howto = w.get('[data-testid="updates-howto"]');
      expect(howto.get('pre').text()).toContain('brew upgrade --cask filex');
      expect(howto.text()).not.toContain('self-update');

      const labels = w.findAll('button').map((b) => b.text());
      expect(labels).not.toContain(cat.updates.applyNow);
    });
  }

  it('a package install whose manager is unknown says only that', async () => {
    state.status = status({
      mode: 'package',
      can_self_apply: false,
      action: 'instruct',
      instructions: ['# upgrade filex with the package manager that installed it'],
    });
    const w = await mountIn('tr');
    expect(w.get('[data-testid="updates-mode"]').text()).toBe(tr.updates.mode.packageUnknown);
    expect(w.get('[data-testid="updates-package-howto"]').text()).toBe(tr.updates.packageHowToUnknown);
  });

  it('a plain binary keeps its button and gets no package sentence', async () => {
    state.status = status({});
    const w = await mountIn('en');
    expect(w.get('[data-testid="updates-mode"]').text()).toBe(en.updates.mode.binary);
    expect(w.find('[data-testid="updates-package-howto"]').exists()).toBe(false);
    expect(w.findAll('button').map((b) => b.text())).toContain(en.updates.applyNow);
  });

  it('a container keeps its own label and no package sentence', async () => {
    state.status = status({
      mode: 'docker',
      can_self_apply: false,
      action: 'instruct',
      instructions: ['docker compose pull filex', 'docker compose up -d'],
    });
    const w = await mountIn('en');
    expect(w.get('[data-testid="updates-mode"]').text()).toBe(en.updates.mode.docker);
    expect(w.find('[data-testid="updates-package-howto"]').exists()).toBe(false);
  });
});
