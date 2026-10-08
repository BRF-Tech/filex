// Ops → Updates: the policy badge says what this install DOES by itself.
//
// #72 (2026-09-25): a Homebrew install saved with policy "patch" read
// "Policy: install patches" while the server turned every patch into
// instructions — the badge promised something the install cannot do. The
// server works out the effective behaviour (update.EffectiveOf).
//
// 0.54 (audit A5): the server also SAYS it - `policy_name`, `policy_badge`
// and `policy_note`, in the screen's language (handlers/update.go sayPolicy,
// held by update_policy_words_test.go). The page kept its own copy of the
// policy words (updates.policyName.* / policyLimit.* / behavior.* / policyIs)
// beside the server's `server.update.policy.*`; it now prints the server's
// sentences as they are and has no words of its own for a policy.
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
    current: 'v0.44.2',
    enabled: true,
    policy: 'patch',
    mode: 'binary',
    can_self_apply: true,
    restart_required: false,
    action: 'none',
    step: 'none',
    behavior: 'patch',
    policy_name: 'yamaları kur',
    policy_badge: 'Politika: yamaları kur',
    ...over,
  };
}

async function mountIn(locale: 'en' | 'tr') {
  const i18n = createI18n({ legacy: false, locale, fallbackLocale: 'en', messages: { en, tr } });
  const w = mount(Updates, { global: { plugins: [i18n] } });
  await flushPromises();
  return w;
}

describe("Updates: the policy is said by the server, and the page prints it", () => {
  beforeEach(() => setActivePinia(createPinia()));

  it('Homebrew with policy "patch": the server badge, its note, and the manager command', async () => {
    state.status = status({
      mode: 'package',
      can_self_apply: false,
      package_manager: 'homebrew',
      package_manager_name: 'Homebrew',
      upgrade_command: 'brew upgrade --cask filex',
      behavior: 'announce',
      policy_limit: 'package',
      policy_badge: 'Yalnızca duyurur',
      policy_note: 'Kayıtlı politika (“yamaları kur”) bu kurulumda etkisiz: ikili dosyanın sahibi Homebrew.',
    });
    const w = await mountIn('tr');

    expect(w.get('[data-testid="updates-policy"]').text()).toBe('Yalnızca duyurur');
    const note = w.get('[data-testid="updates-policy-note"]');
    expect(note.get('[data-testid="updates-policy-note-text"]').text()).toBe(
      'Kayıtlı politika (“yamaları kur”) bu kurulumda etkisiz: ikili dosyanın sahibi Homebrew.',
    );
    expect(note.get('[data-testid="updates-policy-command"]').text()).toBe('brew upgrade --cask filex');
  });

  it('a policy in force: the server badge, and no note', async () => {
    state.status = status({});
    const w = await mountIn('tr');
    expect(w.get('[data-testid="updates-policy"]').text()).toBe('Politika: yamaları kur');
    expect(w.find('[data-testid="updates-policy-note"]').exists()).toBe(false);
  });

  it('the page words nothing itself: whatever the server says is what is printed', async () => {
    // A badge no catalogue of the page has - had the page still worded the
    // policy from `behavior` + `policy_limit`, this would read otherwise.
    state.status = status({
      behavior: 'announce',
      policy_limit: 'container',
      policy_badge: 'SERVER BADGE',
      policy_note: 'SERVER NOTE',
    });
    const w = await mountIn('en');
    expect(w.get('[data-testid="updates-policy"]').text()).toBe('SERVER BADGE');
    expect(w.get('[data-testid="updates-policy-note-text"]').text()).toBe('SERVER NOTE');
  });

  it('the panel catalogue keeps no policy words of its own', () => {
    for (const cat of [en, tr] as Array<Record<string, Record<string, unknown>>>) {
      for (const gone of ['policyName', 'policyLimit', 'behavior', 'policyIs']) {
        expect(cat.updates[gone], `updates.${gone}`).toBeUndefined();
      }
    }
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
