// 0.50 - Settings → Preferences → Default apps (core UserSettingsDialog,
// lib/openWith): the kinds this person chose an app for, judged against what
// the administrator left on. A part of Preferences, not a rail entry of its
// own (the maintainer, 2026-10-01: the rail stays at five). A choice switched off since is kept, NOT used, and the row says so
// and what the kind opens with instead (docs/APP-PLUGINS.md → Default apps).
import { describe, expect, it, vi } from 'vitest';
import { flushPromises, mount } from '@vue/test-utils';

import UserSettingsDialog from '@brftech/filex-core/src/components/UserSettingsDialog.vue';
import type { UserSettingsHost } from '@brftech/filex-core/src/lib/userSettingsHost';
import type { PluginActionsResponse } from '@brftech/filex-core/src/types/Plugins';
import { clearOpenWithChoices, openWithChoices, setOpenWithChoice } from '@brftech/filex-core/src/lib/openWith';
import { answerAccountPrefs } from '../helpers/accountPrefs';

answerAccountPrefs();

const viewer = (plugin: string, ext: string[], label: string) => ({
  plugin,
  id: 'editor',
  placement: 'viewer',
  label: { en: label, tr: label },
  applies: { kind: 'file', ext },
  ui: { url: `/_appui/${plugin}/0123456789abcdef/index.html`, grants: [], engine: false, version: '1' },
});

function host(actions: PluginActionsResponse | null, locale = 'en'): UserSettingsHost {
  return {
    locale,
    user: { id: 7, email: 'ayse@example.com', username: 'ayse', display_name: 'Ayşe', role: 'user' },
    isAdmin: false,
    demoReadOnly: false,
    capabilities: { version: '0.50.0', caller_admin: false },
    api: {
      updateProfile: vi.fn(),
      changePassword: vi.fn(),
      enrollTotp: vi.fn(),
      verifyTotp: vi.fn(),
      disableTotp: vi.fn(),
      quota: vi.fn(async () => ({ used_bytes: 0, quota_bytes: 0, percent_used: 0, unlimited: true })),
      notificationSettings: vi.fn(async () => null),
      updateNotificationSettings: vi.fn(async () => null),
      pluginActions: actions ? vi.fn(async () => actions) : undefined,
    },
    setUser: vi.fn(),
    toast: vi.fn(),
    errorText: (_e, fb) => fb,
    zone: { get: () => '', set: vi.fn() },
    mode: { get: () => 'auto', set: vi.fn() },
  } as unknown as UserSettingsHost;
}

const ACTIONS = {
  actions: [],
  views: [viewer('sketch', ['sketch'], 'Sketch'), viewer('board', ['sketch', 'drawio'], 'Board')],
  open_rules: { drawio: { order: [], off: ['app:board/editor'] } },
} as unknown as PluginActionsResponse;

async function openApps(h: UserSettingsHost) {
  const w = mount(UserSettingsDialog, { props: { modelValue: true, host: h, initialSection: 'preferences' }, attachTo: document.body });
  await flushPromises();
  return w;
}

describe('Settings → Preferences → Default apps', () => {
  it('is a part of Preferences: the rail keeps its five entries', async () => {
    clearOpenWithChoices();
    setOpenWithChoice('sketch', 'app:board/editor');
    const w = await openApps(host(ACTIONS));
    expect(w.find('[data-testid="user-settings-preferences"] [data-testid="user-settings-apps"]').exists()).toBe(true);
    expect(w.find('[data-testid="user-settings-preferences"] [data-testid="user-settings-app-sketch"]').exists()).toBe(true);
    expect(w.find('[data-testid="user-settings-tab-apps"]').exists(), 'no rail entry of its own').toBe(false);
    expect(w.findAll('[data-testid^="user-settings-tab-"]')).toHaveLength(5);
    w.unmount();
  });

  it('says there is nothing chosen yet', async () => {
    clearOpenWithChoices();
    const w = await openApps(host(ACTIONS));
    expect(w.find('[data-testid="user-settings-apps-empty"]').exists()).toBe(true);
    w.unmount();
  });

  it('lists each choice, and one the administrator switched off as no longer available', async () => {
    clearOpenWithChoices();
    setOpenWithChoice('sketch', 'app:board/editor');
    setOpenWithChoice('drawio', 'app:board/editor');
    const w = await openApps(host(ACTIONS));
    const sketch = w.get('[data-testid="user-settings-app-sketch"]');
    expect(sketch.text()).toContain('.sketch');
    expect(sketch.text()).toContain('Board');
    expect(w.find('[data-testid="user-settings-app-gone-sketch"]').exists(), 'still on: available').toBe(false);
    const gone = w.get('[data-testid="user-settings-app-gone-drawio"]');
    expect(gone.text()).toContain('No longer available');
    expect(gone.text(), 'what it opens with instead: filex’s own viewer').toContain('filex viewer (built-in)');
    w.unmount();
  });

  it('changes and resets a choice on the account', async () => {
    clearOpenWithChoices();
    setOpenWithChoice('sketch', 'app:board/editor');
    const w = await openApps(host(ACTIONS));
    await w.get('[data-testid="user-settings-app-choice-sketch-app:sketch/editor"]').trigger('click');
    expect(openWithChoices().sketch).toBe('app:sketch/editor');
    await w.get('[data-testid="user-settings-app-reset-sketch"]').trigger('click');
    expect(openWithChoices().sketch).toBeUndefined();
    w.unmount();
  });

  it('lists the choices without judging them where the apps cannot be read', async () => {
    clearOpenWithChoices();
    setOpenWithChoice('drawio', 'app:board/editor');
    const w = await openApps(host(null));
    expect(w.find('[data-testid="user-settings-app-drawio"]').exists()).toBe(true);
    expect(w.find('[data-testid="user-settings-app-gone-drawio"]').exists()).toBe(false);
    w.unmount();
  });

  it('speaks Turkish', async () => {
    clearOpenWithChoices();
    setOpenWithChoice('drawio', 'app:board/editor');
    const w = await openApps(host(ACTIONS, 'tr'));
    expect(document.body.textContent).toContain('Varsayılan uygulamalar');
    expect(w.get('[data-testid="user-settings-app-gone-drawio"]').text()).toContain('Artık kullanılamıyor');
    w.unmount();
  });
});
