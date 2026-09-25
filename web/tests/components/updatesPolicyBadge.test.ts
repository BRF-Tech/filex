// Ops → Updates: the policy badge says what this install DOES by itself.
//
// #72 (2026-09-25): a Homebrew install saved with policy "patch" read
// "Policy: install patches" while the server turned every patch into
// instructions — the badge promised something the install cannot do. The
// server now works out the effective behaviour (update.EffectiveOf) and sends
// `behavior` + `policy_limit`; the page words them and derives nothing from
// `mode` and `policy` itself. The saved policy is still named, as having no
// effect on this install.
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

type Cat = typeof en;

function status(over: Partial<UpdateStatus>): UpdateStatus {
  return {
    current: 'v0.44.2',
    enabled: true,
    policy: 'patch',
    mode: 'binary',
    can_self_apply: true,
    restart_required: false,
    action: 'none',
    step: 'none',
    behavior: 'patch',
    ...over,
  };
}

async function mountIn(locale: 'en' | 'tr') {
  const i18n = createI18n({ legacy: false, locale, fallbackLocale: 'en', messages: { en, tr } });
  const w = mount(Updates, { global: { plugins: [i18n] } });
  await flushPromises();
  return w;
}

const fill = (s: string, vars: Record<string, string>) =>
  Object.entries(vars).reduce((out, [k, v]) => out.replaceAll(`{${k}}`, v), s);

describe('Updates: the policy badge follows the server', () => {
  beforeEach(() => setActivePinia(createPinia()));

  for (const [locale, cat] of [['en', en], ['tr', tr]] as [string, Cat][]) {
    const lang = locale as 'en' | 'tr';
    const patchName = cat.updates.policyName.patch;

    it(`${locale}: Homebrew with policy "patch" announces only, names the manager and its command`, async () => {
      state.status = status({
        mode: 'package',
        can_self_apply: false,
        package_manager: 'homebrew',
        package_manager_name: 'Homebrew',
        upgrade_command: 'brew upgrade --cask filex',
        behavior: 'announce',
        policy_limit: 'package',
      });
      const w = await mountIn(lang);

      const badge = w.get('[data-testid="updates-policy"]').text();
      expect(badge).toBe(cat.updates.behavior.announce);
      expect(badge).not.toContain(patchName);

      const note = w.get('[data-testid="updates-policy-note"]');
      expect(note.get('[data-testid="updates-policy-note-text"]').text()).toBe(
        fill(cat.updates.policyLimit.package, { policy: patchName, manager: 'Homebrew' }),
      );
      expect(note.text()).not.toContain('{');
      expect(note.get('[data-testid="updates-policy-command"]').text()).toBe('brew upgrade --cask filex');
    });

    it(`${locale}: a package whose manager is unknown says "your package manager", no command`, async () => {
      state.status = status({ mode: 'package', can_self_apply: false, behavior: 'announce', policy_limit: 'package' });
      const w = await mountIn(lang);
      expect(w.get('[data-testid="updates-policy"]').text()).toBe(cat.updates.behavior.announce);
      expect(w.get('[data-testid="updates-policy-note-text"]').text()).toBe(
        fill(cat.updates.policyLimit.packageUnknown, { policy: patchName }),
      );
      expect(w.find('[data-testid="updates-policy-command"]').exists()).toBe(false);
    });

    it(`${locale}: a container with policy "minor" announces only`, async () => {
      state.status = status({
        mode: 'docker',
        can_self_apply: false,
        policy: 'minor',
        behavior: 'announce',
        policy_limit: 'container',
      });
      const w = await mountIn(lang);
      expect(w.get('[data-testid="updates-policy"]').text()).toBe(cat.updates.behavior.announce);
      expect(w.get('[data-testid="updates-policy-note-text"]').text()).toBe(
        fill(cat.updates.policyLimit.container, { policy: cat.updates.policyName.minor }),
      );
      expect(w.find('[data-testid="updates-policy-command"]').exists()).toBe(false);
    });

    it(`${locale}: checking switched off overrides the policy`, async () => {
      state.status = status({ enabled: false, behavior: 'off', policy_limit: 'disabled' });
      const w = await mountIn(lang);
      expect(w.get('[data-testid="updates-policy"]').text()).toBe(cat.updates.behavior.off);
      expect(w.get('[data-testid="updates-policy-note-text"]').text()).toBe(
        fill(cat.updates.policyLimit.disabled, { policy: patchName }),
      );
    });

    it(`${locale}: policy "minor" on 0.x installs patches only`, async () => {
      state.status = status({ policy: 'minor', behavior: 'patch', policy_limit: 'zero_major' });
      const w = await mountIn(lang);
      expect(w.get('[data-testid="updates-policy"]').text()).toBe(cat.updates.behavior.patch);
      expect(w.get('[data-testid="updates-policy-note-text"]').text()).toBe(
        fill(cat.updates.policyLimit.zero_major, { policy: cat.updates.policyName.minor }),
      );
    });

    it(`${locale}: a policy in force is named as the policy, with no note`, async () => {
      state.status = status({});
      const w = await mountIn(lang);
      expect(w.get('[data-testid="updates-policy"]').text()).toBe(
        fill(cat.updates.policyIs, { policy: patchName }),
      );
      expect(w.find('[data-testid="updates-policy-note"]').exists()).toBe(false);
    });
  }

  it('the page does not work the behaviour out from the mode: it renders what the server says', async () => {
    // A package install the server reports as in force (it is the server's
    // call — e.g. a policy of "manual") keeps the policy badge; the page must
    // not decide on its own that a package install "announces only".
    state.status = status({
      mode: 'package',
      can_self_apply: false,
      package_manager_name: 'Homebrew',
      upgrade_command: 'brew upgrade --cask filex',
      policy: 'manual',
      behavior: 'announce',
    });
    const w = await mountIn('tr');
    expect(w.get('[data-testid="updates-policy"]').text()).toBe(
      fill(tr.updates.policyIs, { policy: tr.updates.policyName.manual }),
    );
    expect(w.find('[data-testid="updates-policy-note"]').exists()).toBe(false);
  });

  it('the release badges carry their colour (a security fix is not grey)', async () => {
    state.status = status({
      action: 'confirm',
      step: 'major',
      latest: { version: 'v1.0.0', security: true, migrations: true },
    });
    const w = await mountIn('en');
    const badge = (text: string) => w.findAll('span').find((s) => s.text() === text);
    expect(badge(en.updates.security)?.classes().join(' ')).toContain('bg-rose-50');
    expect(badge(en.updates.migrations)?.classes().join(' ')).toContain('bg-amber-50');
    expect(badge(en.updates.step.major)?.classes().join(' ')).toContain('bg-amber-50');
  });
});
