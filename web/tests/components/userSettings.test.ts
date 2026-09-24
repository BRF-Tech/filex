// The user-settings modal — the seam between "there is an endpoint" and "a
// person can reach it".
//
// Two things are worth a test here and they are not the pretty ones:
//
//  1. THE GAP IT CLOSES. `GET/PATCH /api/notifications/settings` is a per-user
//     endpoint open to every account, and its only screen used to be
//     /admin/notifications — inside the AdminLayout block, which the router
//     guards with `requiresAdmin`. The people who receive notifications were
//     the only people who could not turn them off. So: a non-admin gets the
//     whole modal, and the switch reaches the endpoint.
//  2. THE PATCH IS A WHOLE-OBJECT WRITE. The backend replaces the row from the
//     body (`handlers/notifications.go` → in_app_enabled + muted_events), so
//     sending one half silently wipes the other. Flipping the bell must carry
//     the mute list along, and muting an event must carry the bell flag.
//
// And one negative: a control whose value nothing reads back must NOT appear
// here. A test that only checks what IS drawn cannot catch a field that
// shouldn't be.
//
// ⚠ The time zone used to be the headline example of that — `users.timezone`
// was written by PATCH /api/auth/profile and read by nothing, so every date in
// the product came out in the browser's zone. zaman:z1 built the reader, so
// the assertion flipped: the field is drawn now, and what is tested is that
// picking a zone actually moves the module both date formatters read
// (`the time zone is offered because something reads it`). If that reader is
// ever removed, the control has to go with it.
import { describe, expect, it, afterEach, beforeEach, vi } from 'vitest';
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils';
import { createPinia, setActivePinia } from 'pinia';
import { createI18n } from 'vue-i18n';

import { activeTimeZone as coreActiveTimeZone, registerLocale, resetLocales } from '@brftech/filex-core';
import { formatDate } from '@/lib/format';
import { startRouteName } from '@/lib/startPage';
import UserSettingsModal from '@/components/UserSettingsModal.vue';
import { useAuthStore } from '@/stores/auth';
import { useNotificationsStore } from '@/stores/notifications';
import { useCapabilitiesStore } from '@/stores/capabilities';
import { useToastStore } from '@/stores/toast';
import en from '@/locales/en.json';
import type { NotificationSettings, User } from '@/api/types';

const updateSettings = vi.fn<[{ in_app_enabled: boolean; muted_events: string[] }], Promise<NotificationSettings>>();
const getSettings = vi.fn<[], Promise<NotificationSettings>>();

vi.mock('@/api/notifications', () => ({
  NotificationsApi: {
    getSettings: (...a: unknown[]) => getSettings(...(a as [])),
    updateSettings: (...a: unknown[]) =>
      updateSettings(...(a as [{ in_app_enabled: boolean; muted_events: string[] }])),
    list: vi.fn().mockResolvedValue({ items: [], total: 0 }),
    unreadCount: vi.fn().mockResolvedValue(0),
    adminList: vi.fn().mockResolvedValue({ items: [], total: 0 }),
    getWebhookConfig: vi.fn().mockResolvedValue({ url: '', token_set: false }),
  },
}));

vi.mock('@/api/quota', () => ({
  quotaApi: {
    me: vi.fn().mockResolvedValue({
      used_bytes: 1234,
      quota_bytes: 0,
      percent_used: 0,
      unlimited: true,
    }),
  },
}));

const updateProfile = vi.fn();
vi.mock('@/api/auth', () => ({
  AuthApi: {
    updateProfile: (...a: unknown[]) => updateProfile(...a),
    changePassword: vi.fn().mockResolvedValue(undefined),
    enrollTotp: vi.fn(),
    verifyTotp: vi.fn(),
    disableTotp: vi.fn(),
  },
}));

// The stylesheet import is a side effect Vite handles; vitest must not choke
// on the package's built CSS path.
vi.mock('@brftech/filex-core/style.css', () => ({}));

const NON_ADMIN: User = {
  id: 42,
  email: 'kaya@example.com',
  username: 'kaya',
  display_name: 'Kaya Demir',
  role: 'user',
  created_at: '2026-09-01T00:00:00Z',
  updated_at: '2026-09-01T00:00:00Z',
};

/**
 * Every modal this file mounts, so `afterEach` can take it down again.
 *
 * ⚠⚠ A mounted UserSettingsModal is NOT inert: it starts
 * `setInterval(refreshTzNow, 1000)` and only clears it in `onBeforeUnmount`.
 * Left mounted, sixteen of them go on ticking after the file's environment is
 * torn down, and the first tick to land afterwards re-renders a component
 * whose `window` is gone — "ReferenceError: window is not defined", reported
 * against whichever file was unlucky, with every test still green. Vitest
 * counts that unhandled rejection and exits non-zero, so the gate is red and
 * the list of failures is empty (seen on the v0.43.0 release branch, only in
 * the full parallel run — this file alone always passed).
 */
const live: VueWrapper[] = [];

function mountModal(): VueWrapper {
  const i18n = createI18n({ legacy: false, locale: 'en', fallbackLocale: 'en', messages: { en } });
  const w = mount(UserSettingsModal, {
    props: { modelValue: true },
    global: { plugins: [i18n] },
    attachTo: document.body,
  });
  live.push(w);
  return w;
}

describe('UserSettingsModal', () => {
  afterEach(() => {
    while (live.length) live.pop()!.unmount();
  });

  beforeEach(() => {
    setActivePinia(createPinia());
    vi.clearAllMocks();
    getSettings.mockResolvedValue({
      user_id: 42,
      in_app_enabled: true,
      muted_events: ['file.moved'],
    });
    updateSettings.mockImplementation(async (p) => ({
      user_id: 42,
      in_app_enabled: p.in_app_enabled,
      muted_events: p.muted_events,
    }));
  });

  it('gives a NON-admin the whole modal — every section, no role gate', async () => {
    const auth = useAuthStore();
    auth.user = NON_ADMIN;
    expect(auth.isAdmin).toBe(false);

    const w = mountModal();
    await w.vm.$nextTick();

    for (const key of ['profile', 'preferences', 'notifications', 'security', 'ai']) {
      expect(
        w.find(`[data-testid="user-settings-tab-${key}"]`).exists(),
        `rail entry ${key} missing for a non-admin`,
      ).toBe(true);
    }
    const rail = w.findAll('[data-testid^="user-settings-tab-"]').map((b) => b.text());
    expect(rail.length).toBe(5);
    expect(rail.slice(0, 4)).toEqual(['Profile', 'Preferences', 'Notifications', 'Security']);
  });

  // ⚠ The assistant row exists so the rail matches the reference shell's five,
  // and the owner's condition for that was that it promise NOTHING: there are
  // no per-user assistant settings on this server (the AI settings that exist
  // are instance-wide and admin-only). A future hand filling that pane with a
  // switch has to delete this test to do it, which is the point — the rail
  // entry saying "coming soon" while the pane offered a control would be the
  // one shape worse than not having the entry at all.
  it('the assistant section says it is not here yet, and offers nothing to press', async () => {
    const auth = useAuthStore();
    auth.user = NON_ADMIN;
    const w = mountModal();
    await w.vm.$nextTick();

    const tab = w.find('[data-testid="user-settings-tab-ai"]');
    expect(tab.text()).toContain('Coming soon');

    await tab.trigger('click');
    await w.vm.$nextTick();

    const pane = w.find('[data-testid="user-settings-ai"]');
    expect(pane.exists()).toBe(true);
    expect(pane.text()).toContain('Coming soon');
    expect(pane.findAll('input, select, textarea').length).toBe(0);
    expect(pane.findAll('button').length).toBe(0);
  });

  it('carries the mute list along when the bell flag is flipped', async () => {
    const auth = useAuthStore();
    auth.user = NON_ADMIN;
    const notif = useNotificationsStore();
    const w = mountModal();
    await notif.fetchSettings();
    await w.vm.$nextTick();

    await w.find('[data-testid="user-settings-tab-notifications"]').trigger('click');
    const bell = w.find('[data-testid="user-settings-inapp"]');
    expect(bell.attributes('aria-checked')).toBe('true');

    await bell.trigger('click');
    // ⚠ Not `{ in_app_enabled: false }` on its own: the handler replaces the
    // whole row, so the mute the user already had must travel with it.
    expect(updateSettings).toHaveBeenCalledWith({
      in_app_enabled: false,
      muted_events: ['file.moved'],
    });
  });

  // ⚠ Measured in the release-candidate sweep (2026-09-21): the dialog offered
  // "A virus is found in a file" with scanning off and "…opened with the
  // escrow key" with no escrow key — switches that can never fire.
  it('offers only the events that can happen on this instance', async () => {
    const auth = useAuthStore();
    auth.user = NON_ADMIN;
    const caps = useCapabilitiesStore();
    const notif = useNotificationsStore();
    caps.data = { ...caps.data, antivirus: false, e2e_escrow: { enabled: false }, app_plugins: { enabled: false } };
    const w = mountModal();
    await notif.fetchSettings();
    await w.vm.$nextTick();
    await w.find('[data-testid="user-settings-tab-notifications"]').trigger('click');
    for (const ev of ['file.infected', 'e2e.escrow_used', 'plugin.notice']) {
      expect(w.find(`[data-testid="user-settings-event-${ev}"]`).exists(), `${ev} offered with its service off`).toBe(false);
    }
    expect(w.find('[data-testid="user-settings-event-share.created"]').exists()).toBe(true);

    caps.data = { ...caps.data, antivirus: true, e2e_escrow: { enabled: true }, app_plugins: { enabled: true } };
    await w.vm.$nextTick();
    for (const ev of ['file.infected', 'e2e.escrow_used', 'plugin.notice']) {
      expect(w.find(`[data-testid="user-settings-event-${ev}"]`).exists(), `${ev} missing with its service on`).toBe(true);
    }
  });

  // QA #39, the admin half of the rule every "needs a service" entry follows
  // (core lib/serviceGate): an administrator, who can switch the service on,
  // sees the switch GREYED with the reason — not hidden, not live.
  it('shows an administrator the impossible events greyed, with the reason', async () => {
    const auth = useAuthStore();
    auth.user = NON_ADMIN;
    const caps = useCapabilitiesStore();
    const notif = useNotificationsStore();
    caps.data = {
      ...caps.data,
      caller_admin: true,
      antivirus: false,
      e2e_escrow: { enabled: true },
      app_plugins: { enabled: true },
    };
    const w = mountModal();
    await notif.fetchSettings();
    await w.vm.$nextTick();
    await w.find('[data-testid="user-settings-tab-notifications"]').trigger('click');
    const sw = w.find('[data-testid="user-settings-event-file.infected"]');
    expect(sw.exists(), 'an administrator is shown the event').toBe(true);
    expect(sw.attributes('disabled'), 'but it cannot be switched').toBeDefined();
    expect(sw.attributes('aria-checked')).toBe('false');
    expect(w.find('[data-testid="user-settings-event-off-file.infected"]').text()).toContain('Virus scanning is off');
    const live = w.find('[data-testid="user-settings-event-share.created"]');
    expect(live.attributes('disabled')).toBeUndefined();
    expect(w.find('[data-testid="user-settings-event-off-e2e.escrow_used"]').exists()).toBe(false);
  });

  it('mutes one event without disturbing the bell flag or the other mutes', async () => {
    const auth = useAuthStore();
    auth.user = NON_ADMIN;
    const notif = useNotificationsStore();
    const w = mountModal();
    await notif.fetchSettings();
    await w.vm.$nextTick();

    await w.find('[data-testid="user-settings-tab-notifications"]').trigger('click');

    // An event that is NOT muted shows as on; clicking it mutes it.
    const shared = w.find('[data-testid="user-settings-event-share.created"]');
    expect(shared.attributes('aria-checked')).toBe('true');
    await shared.trigger('click');
    expect(updateSettings).toHaveBeenCalledWith({
      in_app_enabled: true,
      muted_events: ['file.moved', 'share.created'],
    });

    // A muted one shows as off, and clicking it un-mutes only itself.
    const moved = w.find('[data-testid="user-settings-event-file.moved"]');
    expect(moved.attributes('aria-checked')).toBe('false');
  });

  it('draws no field the product cannot read back', async () => {
    const auth = useAuthStore();
    auth.user = NON_ADMIN;
    const w = mountModal();
    await w.vm.$nextTick();

    // Walk every pane — the phantom fields would live on Preferences and
    // Security, and a test that only looked at the pane that happened to be
    // open would miss them.
    const seen: string[] = [];
    for (const key of ['profile', 'preferences', 'notifications', 'security']) {
      await w.find(`[data-testid="user-settings-tab-${key}"]`).trigger('click');
      seen.push(w.find(`[data-testid="user-settings-${key}"]`).text());
    }
    const all = seen.join('\n').toLowerCase();

    // ⚠ `timezone` USED to be asserted absent here, and rightly: it was
    // write-only, and no date in the product was formatted with it. zaman:z1
    // gave it a reader (core's lib/timezone → useLocale.formatDate and
    // lib/format), so the field is drawn now — and the rule it was protecting
    // is enforced by `the time zone is offered because something reads it`
    // below instead, which fails if that reader is ever taken away again.
    // No session list endpoint exists.
    expect(all).not.toContain('session');
    // No upload-preference columns exist.
    expect(all).not.toContain('upload folder');
    expect(all).not.toContain('conflict');
  });

  it('the time zone is offered because something reads it', async () => {
    const auth = useAuthStore();
    auth.user = NON_ADMIN;
    const w = mountModal();
    await w.vm.$nextTick();
    await w.find('[data-testid="user-settings-tab-preferences"]').trigger('click');

    // The control exists…
    const sel = w.find('[data-testid="user-settings-timezone"]');
    expect(sel.exists()).toBe(true);

    // …and it is a combobox now, not a list box: type to filter, pick a row.
    // ⚠ What is asserted BELOW this line did not change and must not — the
    // stored value, the key it is stored under and the reader that formats
    // dates against it are the point of this test; the widget is not.
    const pick = async (zone: string, query: string) => {
      const box = w.find('[data-testid="user-settings-tz-filter"]');
      await box.trigger('focus');
      await box.setValue(query);
      await w.vm.$nextTick();
      const opt = w.find(`[data-testid="user-settings-tz-opt-${zone || 'device'}"]`);
      expect(opt.exists(), `no row for ${zone || '(this device)'} after typing "${query}"`).toBe(
        true,
      );
      await opt.trigger('mousedown');
      await w.vm.$nextTick();
    };

    // …and the module the dates are formatted against actually moves. This is
    // the half that matters: the previous version of this modal deliberately
    // left the field out because `users.timezone` was stored and read by
    // nothing, and a picker that only wrote a row would be that same bug with
    // a nicer surface.
    await pick('UTC', 'utc');
    expect(localStorage.getItem('filex.timezone')).toBe('UTC');
    expect(coreActiveTimeZone()).toBe('UTC');
    // 00:30Z read on the UTC clock. (en-US is a 12-hour locale, hence AM.)
    expect(formatDate('2026-09-12T00:30:00Z', 'en')).toContain('12:30 AM');

    // Same instant, other clock — the owner's scenario, at unit scale.
    // Typed the way a person in Istanbul would type it — three letters, not
    // an IANA id — which is the whole reason this control was rebuilt.
    await pick('Europe/Istanbul', 'ist');
    expect(coreActiveTimeZone()).toBe('Europe/Istanbul');
    // The SAME instant, +03:00 — the offset, not a different moment.
    expect(formatDate('2026-09-12T00:30:00Z', 'en')).toContain('3:30 AM');

    // And it reaches the account, so the next browser agrees.
    expect(updateProfile).toHaveBeenCalledWith({ timezone: 'Europe/Istanbul' });

    // "Use this device's zone" clears the preference rather than freezing
    // today's answer into it.
    await pick('', 'device');
    expect(localStorage.getItem('filex.timezone')).toBe(null);
    expect(coreActiveTimeZone()).toBe(undefined);
  });

  it('offers the admin start page only to an admin, and the router obeys it', async () => {
    const auth = useAuthStore();
    auth.user = NON_ADMIN;
    const w = mountModal();
    await w.vm.$nextTick();
    await w.find('[data-testid="user-settings-tab-preferences"]').trigger('click');

    expect(w.find('[data-testid="user-settings-start-home"]').exists()).toBe(true);
    expect(w.find('[data-testid="user-settings-start-files"]').exists()).toBe(true);
    // ⚠ Never offered — a door a non-admin cannot walk through.
    expect(w.find('[data-testid="user-settings-start-admin"]').exists()).toBe(false);

    await w.find('[data-testid="user-settings-start-files"]').trigger('click');
    expect(localStorage.getItem('filex.startpage')).toBe('files');
    // The reader: router/index.ts asks exactly this.
    expect(startRouteName({ isAdmin: false, userBase: true })).toBe('explore');

    // A choice that is no longer available degrades to Home instead of
    // bouncing the person off the admin guard on every single launch.
    localStorage.setItem('filex.startpage', 'admin');
    expect(startRouteName({ isAdmin: false, userBase: false })).toBe('home');
    expect(startRouteName({ isAdmin: true, userBase: false })).toBe('dashboard');

    // ⚠ No choice at all = Home, for an admin as well (owner's decision,
    // 2026-09-12). The dashboard is a destination, not a landing page — which
    // is exactly why the control above has to keep offering it to admins.
    localStorage.removeItem('filex.startpage');
    expect(startRouteName({ isAdmin: true, userBase: false })).toBe('home');
    expect(startRouteName({ isAdmin: false, userBase: true })).toBe('home');
  });

  it('the palette and the light/dark mode are two axes, stored apart', async () => {
    const auth = useAuthStore();
    auth.user = NON_ADMIN;
    const w = mountModal();
    await w.vm.$nextTick();
    await w.find('[data-testid="user-settings-tab-preferences"]').trigger('click');

    await w.find('[data-testid="user-settings-theme-dark"]').trigger('click');
    await w.find('[data-theme-id="night"]').trigger('click');

    // ⚠ The regression this guards: both of these lived under `filex.theme`
    // until zaman:z1 — core's palette and the app's mode — so picking a
    // palette silently reset the mode to auto and vice versa. Two keys.
    expect(localStorage.getItem('filex.theme')).toBe('dark');
    expect(localStorage.getItem('filex.palette')).toBe('night');
  });

  it('saves the profile with exactly the fields PATCH /api/auth/profile accepts', async () => {
    const auth = useAuthStore();
    auth.user = NON_ADMIN;
    updateProfile.mockResolvedValue({ ...NON_ADMIN, display_name: 'Kaya D.' });

    const w = mountModal();
    await w.vm.$nextTick();

    await w.find('[data-testid="user-settings-save-profile"]').trigger('click');
    expect(updateProfile).toHaveBeenCalledWith({
      email: 'kaya@example.com',
      username: 'kaya',
      display_name: 'Kaya Demir',
      avatar_url: '',
    });
  });

  // Release-candidate sweep, 2026-09-21: "bu-bir-eposta-degil" was saved as
  // the address with "Profil kaydedildi"; a username with "ş" came back after
  // Save as the server's raw English in a toast. Both are now said under
  // their box while typed, and Save waits.
  it('says what is wrong with the address under its box, and does not save it', async () => {
    const auth = useAuthStore();
    auth.user = NON_ADMIN;
    const w = mountModal();
    await w.vm.$nextTick();

    expect(w.find('[data-testid="profile-email-error"]').exists()).toBe(false);
    await w.find('[data-testid="profile-email"]').setValue('bu-bir-eposta-degil');
    const err = w.find('[data-testid="profile-email-error"]');
    expect(err.exists()).toBe(true);
    expect(err.text()).toBe('This is not an email address. Write it as name@example.com.');
    expect(w.find('[data-testid="profile-email"]').attributes('aria-invalid')).toBe('true');

    const save = w.find('[data-testid="user-settings-save-profile"]');
    expect(save.attributes('disabled')).toBeDefined();
    await save.trigger('click');
    expect(updateProfile).not.toHaveBeenCalled();

    // A dotless domain is an address: the first administrator's is admin@local.
    await w.find('[data-testid="profile-email"]').setValue('kaya@local');
    expect(w.find('[data-testid="profile-email-error"]').exists()).toBe(false);
    expect(w.find('[data-testid="user-settings-save-profile"]').attributes('disabled')).toBeUndefined();
  });

  it('says what is wrong with the username in words, not the server log line', async () => {
    const auth = useAuthStore();
    auth.user = NON_ADMIN;
    const w = mountModal();
    await w.vm.$nextTick();

    await w.find('[data-testid="profile-username"]').setValue('ayşe');
    const err = w.find('[data-testid="profile-username-error"]');
    expect(err.text()).toBe('“ş” cannot be used in a username. Use a–z, 0–9, dot, dash or underscore.');
    expect(err.text()).not.toContain('invalid username');
    await w.find('[data-testid="user-settings-save-profile"]').trigger('click');
    expect(updateProfile).not.toHaveBeenCalled();
  });

  it('puts a refusal the server still makes under the box it is about, not in a toast', async () => {
    const auth = useAuthStore();
    auth.user = NON_ADMIN;
    const toasts = useToastStore();
    updateProfile.mockRejectedValue({
      response: {
        status: 409,
        data: { error: 'email_taken', field: 'email', message: 'That address belongs to another account.' },
      },
    });
    const w = mountModal();
    await w.vm.$nextTick();

    await w.find('[data-testid="profile-email"]').setValue('taken@example.com');
    await w.find('[data-testid="user-settings-save-profile"]').trigger('click');
    await flushPromises();

    expect(w.find('[data-testid="profile-email-error"]').text()).toBe('That address belongs to another account.');
    // ⚠ Not "Profile saved": that is what the duplicate address used to get.
    expect(toasts.toasts.map((x) => x.message)).toEqual([]);

    // Typing again is a new question; the old answer goes.
    await w.find('[data-testid="profile-email"]').setValue('free@example.com');
    expect(w.find('[data-testid="profile-email-error"]').exists()).toBe(false);
  });

  it('persists the theme and the density where their readers look', async () => {
    const auth = useAuthStore();
    auth.user = NON_ADMIN;
    const w = mountModal();
    await w.vm.$nextTick();

    await w.find('[data-testid="user-settings-tab-preferences"]').trigger('click');
    await w.find('[data-testid="user-settings-theme-dark"]').trigger('click');
    // lib/theme reads this on every boot, and toggles <html class="dark">.
    expect(localStorage.getItem('filex.theme')).toBe('dark');
    expect(document.documentElement.classList.contains('dark')).toBe(true);

    await w.find('[data-testid="user-settings-density"]').trigger('click');
    // packages/core's Toolbar reads this at setup — see tests/lib/density.
    expect(localStorage.getItem('filex.density')).toBe('compact');
  });

  // ⚠⚠ The language field offered `[en, tr]`, written here, and its
  // `currentLocale` turned anything that was not `tr` into `en` — so a
  // language pack's language could be installed and never chosen, and once
  // chosen elsewhere this dialog said the person was reading English.
  it("offers every language the instance has, a language pack's included, and marks the active one", async () => {
    const auth = useAuthStore();
    auth.user = NON_ADMIN;
    registerLocale({ code: 'es', source: 'plugin', plugin: 'lang-es' });
    try {
      const i18n = createI18n({ legacy: false, locale: 'es', fallbackLocale: 'en', messages: { en } });
      const w = mount(UserSettingsModal, { props: { modelValue: true }, global: { plugins: [i18n] }, attachTo: document.body });
      live.push(w);
      await w.vm.$nextTick();
      await w.find('[data-testid="user-settings-tab-preferences"]').trigger('click');
      const es = w.find('[data-testid="user-settings-locale-es"]');
      expect(es.exists()).toBe(true);
      expect(es.text()).toBe('Español');
      expect(es.attributes('aria-pressed')).toBe('true');
      expect(w.find('[data-testid="user-settings-locale-en"]').attributes('aria-pressed')).toBe('false');
    } finally {
      resetLocales();
    }
  });
});
