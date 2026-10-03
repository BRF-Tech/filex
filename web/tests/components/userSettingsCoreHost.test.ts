// The user settings dialog as a CORE component — what the desktop app opens.
//
// 2026-09-27, owner: *"Kullanıcı ayarları uygulamanın İÇİNDE açılsın."* The
// dialog lived in web/src bound to the admin app's pinia stores, axios client
// and vue-i18n; it moved into packages/core and takes what it cannot know from
// a `host` (lib/userSettingsHost). What is pinned here:
//
//   1. It runs with NO pinia, NO router, NO vue-i18n — a plain host object,
//      which is all the explorer can hand it.
//   2. A row only some surfaces have is drawn only when the host gives it:
//      the desktop app keeps its language and its click setting in its own
//      Settings, has no start page, no download of itself and no browser
//      notification API — none of those may appear there.
//   3. The writes go through the host's endpoints and the host's own stores
//      (profile → setUser, appearance → host.mode, bell switches → the
//      notification settings endpoint, whole document).
//   4. It speaks the explorer catalogue: Turkish with Turkish characters.
//
// (The admin app's binding keeps its own, older suite: userSettings.test.ts.)
import { describe, expect, it, vi } from 'vitest';
import { readFileSync } from 'node:fs';
import path from 'node:path';
import { flushPromises, mount } from '@vue/test-utils';

import UserSettingsDialog from '@brftech/filex-core/src/components/UserSettingsDialog.vue';
import type { UserSettingsHost, SettingsUser } from '@brftech/filex-core/src/lib/userSettingsHost';
import { tr } from '@brftech/filex-core/src/locales/tr';
import { answerAccountPrefs } from '../helpers/accountPrefs';

// Picking a theme writes it to the account 400 ms later.
answerAccountPrefs();

function plainHost(extra: Partial<UserSettingsHost> = {}, locale = 'en') {
  let user: SettingsUser = { id: 7, email: 'ayse@example.com', username: 'ayse', display_name: 'Ayşe', role: 'user' };
  const calls = {
    updateProfile: vi.fn(async (patch: Partial<SettingsUser>) => ({ ...user, ...patch })),
    updateNotificationSettings: vi.fn(async (p: { in_app_enabled: boolean; muted_events: string[] }) => p),
    setUser: vi.fn((u: SettingsUser) => {
      user = u;
    }),
    toast: vi.fn(),
    modeSet: vi.fn(),
  };
  const host: UserSettingsHost = {
    locale,
    get user() {
      return user;
    },
    isAdmin: false,
    demoReadOnly: false,
    capabilities: { version: '0.47.0', caller_admin: false },
    api: {
      updateProfile: calls.updateProfile,
      changePassword: vi.fn(async () => {}),
      enrollTotp: vi.fn(async () => ({ secret: 'S', qr_svg: '<svg></svg>' })),
      verifyTotp: vi.fn(async () => {}),
      disableTotp: vi.fn(async () => {}),
      quota: vi.fn(async () => ({ used_bytes: 1024, quota_bytes: 0, percent_used: 0, unlimited: true })),
      notificationSettings: vi.fn(async () => ({ in_app_enabled: true, muted_events: ['file.moved'] })),
      updateNotificationSettings: calls.updateNotificationSettings,
    },
    setUser: calls.setUser,
    toast: calls.toast,
    errorText: (e, fb) => (e as Error)?.message || fb,
    zone: { get: () => '', set: vi.fn() },
    mode: { get: () => 'auto', set: calls.modeSet },
    ...extra,
  };
  return { host, calls };
}

function open(host: UserSettingsHost) {
  return mount(UserSettingsDialog, { props: { modelValue: true, host }, attachTo: document.body });
}

describe('the core settings dialog, with only what an explorer can give it', () => {
  it('draws none of the rows only some surfaces have', async () => {
    const { host } = plainHost();
    const w = open(host);
    await flushPromises();
    await w.find('[data-testid="user-settings-tab-preferences"]').trigger('click');
    for (const sel of [
      '[data-testid="user-settings-locale"]',
      '[data-testid^="user-settings-start-"]',
      '[data-testid^="user-settings-opentrigger-"]',
      '[data-testid="user-settings-desktop-app"]',
    ]) {
      expect(w.find(sel).exists(), sel).toBe(false);
    }
    // …and what every surface has is there.
    expect(w.find('[data-testid="user-settings-timezone"]').exists()).toBe(true);
    expect(w.find('[data-testid="user-settings-appearance"]').exists()).toBe(true);
    expect(w.find('[data-testid="user-settings-density"]').exists()).toBe(true);
    expect(w.find('[data-testid="user-settings-folder-view"]').exists()).toBe(true);
    await w.find('[data-testid="user-settings-tab-notifications"]').trigger('click');
    expect(w.find('[data-testid="user-settings-inapp"]').exists()).toBe(true);
    expect(w.find('[data-testid="user-settings-browser"]').exists(), 'the browser switch').toBe(false);
  });

  it('draws them when the host has them', async () => {
    const { host } = plainHost({
      language: { pick: vi.fn() },
      startPage: { options: ['home', 'files'], get: () => 'home', set: vi.fn() },
      openTrigger: { get: () => 'double', set: vi.fn() },
      browserNotifications: {
        desktopShell: false,
        permission: () => 'default',
        enabled: () => false,
        setEnabled: vi.fn(),
        ask: vi.fn(async () => 'granted' as const),
      },
    });
    const w = open(host);
    await flushPromises();
    await w.find('[data-testid="user-settings-tab-preferences"]').trigger('click');
    expect(w.find('[data-testid="user-settings-locale"]').exists()).toBe(true);
    expect(w.findAll('[data-testid^="user-settings-start-"]').length).toBe(2);
    expect(w.findAll('[data-testid^="user-settings-opentrigger-"]').length).toBe(2);
    await w.find('[data-testid="user-settings-tab-notifications"]').trigger('click');
    expect(w.find('[data-testid="user-settings-browser"]').exists()).toBe(true);
  });

  it('a profile save goes through the host and hands the answer back', async () => {
    const { host, calls } = plainHost();
    const w = open(host);
    await flushPromises();
    await w.find('[data-testid="profile-username"]').setValue('ayse.k');
    await w.find('[data-testid="user-settings-save-profile"]').trigger('click');
    await flushPromises();
    expect(calls.updateProfile).toHaveBeenCalledWith(
      expect.objectContaining({ username: 'ayse.k', email: 'ayse@example.com', display_name: 'Ayşe' }),
    );
    expect(calls.setUser).toHaveBeenCalledWith(expect.objectContaining({ username: 'ayse.k' }));
    expect(calls.toast).toHaveBeenCalledWith('success', expect.any(String));
  });

  it('a problem under the box stops the save — in the explorer catalogue’s words', async () => {
    const { host, calls } = plainHost();
    const w = open(host);
    await flushPromises();
    await w.find('[data-testid="profile-email"]').setValue('bu-bir-eposta-degil');
    const err = w.find('[data-testid="profile-email-error"]');
    expect(err.text()).toBe('This is not an email address. Write it as name@example.com.');
    expect(w.find('[data-testid="user-settings-save-profile"]').attributes('disabled')).toBeDefined();
    expect(calls.updateProfile).not.toHaveBeenCalled();
  });

  it('the mode and the bell switches go where the host keeps them', async () => {
    const { host, calls } = plainHost();
    const w = open(host);
    await flushPromises();
    await w.find('[data-testid="user-settings-tab-preferences"]').trigger('click');
    await w.find('[data-testid="user-settings-theme-dark"]').trigger('click');
    expect(calls.modeSet).toHaveBeenCalledWith('dark');

    await w.find('[data-testid="user-settings-tab-notifications"]').trigger('click');
    await w.find('[data-testid="user-settings-inapp"]').trigger('click');
    await flushPromises();
    // The WHOLE document: the mute the person already had travels with it.
    expect(calls.updateNotificationSettings).toHaveBeenCalledWith({ in_app_enabled: false, muted_events: ['file.moved'] });
  });

  it('speaks Turkish with Turkish characters', async () => {
    const { host } = plainHost({}, 'tr');
    const w = open(host);
    await flushPromises();
    const rail = w.findAll('[data-testid^="user-settings-tab-"]').map((b) => b.text());
    expect(rail.slice(0, 4)).toEqual([
      tr['userSettings.tabs.profile'],
      tr['userSettings.tabs.preferences'],
      tr['userSettings.tabs.notifications'],
      tr['userSettings.tabs.security'],
    ]);
    expect(w.text()).toContain('Kullanıcı ayarları');
    expect(w.text()).not.toMatch(/Kullanici|Guvenlik|Bildirimler olmadan/);
  });
});

/* ⚠ Measured in the desktop window AND on the web page the day the dialog
   moved: its root also carries `.fe` (for the palette), and base.css's `.fe`
   (display:flex, height:100%, a border) is written AFTER the dialog's rules in
   the package's one stylesheet. At equal weight it won — the rail stretched
   across the top with the pane under it. While the dialog lived in web/src,
   its stylesheet loaded after core's and nobody saw the tie. happy-dom does
   not lay out, so the rules themselves are held here. */
describe('the dialog’s own layout outweighs the explorer’s .fe', () => {
  const root = path.resolve(__dirname, '../../../packages/core/src');
  const css = readFileSync(path.join(root, 'components/UserSettingsDialog.vue'), 'utf8').split('<style>')[1] ?? '';

  it('base.css still makes .fe a flex column — the rule the dialog has to beat', () => {
    const base = readFileSync(path.join(root, 'styles/base.css'), 'utf8');
    expect(base).toMatch(/\n\.fe \{[^}]*display: flex;/);
  });

  it('the grid is declared on .fe.fx-us, never on a bare .fx-us', () => {
    expect(css).toMatch(/\n\.fe\.fx-us \{[^}]*display: grid;/);
    expect(css, 'a bare .fx-us rule ties with .fe and loses').not.toMatch(/(^|\n)\s*\.fx-us \{/);
  });

  it('the phone layout overrides the same selector', () => {
    const phone = css.split('@media (max-width: 640px)')[1] ?? '';
    expect(phone).toMatch(/\.fe\.fx-us \{[^}]*grid-template-columns/);
  });

  /* The desktop window styles every <button> (content centred) and every <h2>
     (an upper-case caption) on its own settings screen; the dialog lives in
     the same document. */
  it('a host’s button and heading rules do not reshape the rail or the title', () => {
    expect(css).toMatch(/\n\.fx-us__rail-item \{[^}]*justify-content: flex-start;/);
    expect(css).toMatch(/\n\.fx-us__title \{[^}]*text-transform: none;/);
  });
});

/* Owner, 2026-09-27: the desktop app's title bar works ALWAYS — with this
   dialog open too. It was a native <dialog> opened with showModal(): the
   browser's top layer, above every z-index, and the rest of the page inert,
   so the frameless window's minimise / maximise / close (page content) lay
   under its backdrop and their first click closed the dialog. It is core's
   Modal now (bare); what the native element gave for free is held here. */
describe('the settings dialog is a dialog in the page, not in the top layer', () => {
  it('draws no native <dialog>; the card is role=dialog, aria-modal, named by its own heading', async () => {
    const { host } = plainHost();
    open(host);
    await flushPromises();
    expect(document.querySelector('dialog'), 'a native <dialog> goes to the top layer').toBeNull();
    const card = document.querySelector('[role="dialog"]')!;
    expect(card.getAttribute('aria-modal')).toBe('true');
    const heading = document.getElementById(card.getAttribute('aria-labelledby') ?? '');
    expect(heading).toBe(document.querySelector('.fx-us__title'));
    expect(card.querySelector('[data-testid="user-settings-dialog"]')).not.toBeNull();
  });

  it('Escape closes it, and so does a click that starts and ends outside it', async () => {
    const { host } = plainHost();
    const w = open(host);
    await flushPromises();
    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }));
    expect(w.emitted('update:modelValue')?.[0]).toEqual([false]);
    const backdrop = document.querySelector('.fe-modal__backdrop')!;
    backdrop.dispatchEvent(new MouseEvent('pointerdown', { bubbles: true }));
    backdrop.dispatchEvent(new MouseEvent('click', { bubbles: true }));
    expect(w.emitted('update:modelValue')?.[1]).toEqual([false]);
    // …and a click inside is not one of those.
    const inside = document.querySelector('.fx-us__head')!;
    inside.dispatchEvent(new MouseEvent('pointerdown', { bubbles: true }));
    inside.dispatchEvent(new MouseEvent('click', { bubbles: true }));
    expect(w.emitted('update:modelValue')).toHaveLength(2);
  });

  it('takes the focus, and gives it back to whoever opened it', async () => {
    const opener = document.createElement('button');
    document.body.appendChild(opener);
    opener.focus();
    const { host } = plainHost();
    const w = open(host);
    await flushPromises();
    await new Promise((r) => setTimeout(r, 60));
    expect(document.querySelector('[role="dialog"]')!.contains(document.activeElement)).toBe(true);
    await w.setProps({ modelValue: false });
    await flushPromises();
    expect(document.activeElement).toBe(opener);
  });
});
