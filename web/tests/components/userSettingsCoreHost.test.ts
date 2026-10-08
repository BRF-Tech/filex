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
import { WEBHOOK_EVENTS, userEventKey } from '@brftech/filex-core/src/lib/webhookEvents';
import { en as coreEn } from '@brftech/filex-core/src/locales/en';
import { tr } from '@brftech/filex-core/src/locales/tr';
import { answerAccountPrefs } from '../helpers/accountPrefs';
import { ACCOUNT_CHECK_DELAY_MS } from '@brftech/filex-core/src/lib/accountRules';

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
      // The server's account check (POST /api/auth/account/check): its code
      // and its sentence for what it would refuse.
      checkAccount: vi.fn(async (q: { email?: string }) =>
        q.email !== undefined && q.email !== user.email && !q.email.includes('@')
          ? { email: { error: 'email_invalid', message: `“${q.email}” is not an email address. Write it as name@example.com.` } }
          : {},
      ),
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
      '[data-testid="user-settings-install-app"]',
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

  // ⚠ 0.54 (#209, B15/A12): the words are the SERVER's, asked while typing;
  // the explorer catalogue keeps no copy of them.
  it('a problem under the box stops the save — in the server’s words', async () => {
    const { host, calls } = plainHost();
    const w = open(host);
    await flushPromises();
    await w.find('[data-testid="profile-email"]').setValue('bu-bir-eposta-degil');
    await new Promise((r) => setTimeout(r, ACCOUNT_CHECK_DELAY_MS + 50));
    await flushPromises();
    const err = w.find('[data-testid="profile-email-error"]');
    expect(err.text()).toBe('“bu-bir-eposta-degil” is not an email address. Write it as name@example.com.');
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

// The two encryption request switches, as the explorer's own host draws them.
// Whether they can happen, and whether THIS caller could change that, is the
// server's answer (`capabilities.event_off`, #211 audit B16; the rule - the
// approval policy, a new request only for administrator accounts, the role and
// not `caller_admin` - is backend capabilities_rules.go eventsOff, tested in
// Go). The host's `isAdmin` decides nothing here any more.
describe('the encryption request switches, in the explorer’s host', () => {
  const VERSION = '0.49.0';
  const CREATED = 'e2e.request_created';
  const DECIDED = 'e2e.request_decided';
  const SAID_TR = 'Yalnız şifreleme politikası onay istediğinde.';

  async function notifications(extra: Partial<UserSettingsHost>, locale = 'en') {
    const { host } = plainHost(extra, locale);
    const w = open(host);
    await flushPromises();
    await w.find('[data-testid="user-settings-tab-notifications"]').trigger('click');
    return w;
  }
  const sw = (w: VueWrapper, ev: string) => w.find(`[data-testid="user-settings-event-${ev}"]`);

  it('offers both, switchable, when the server lists neither', async () => {
    const w = await notifications({ isAdmin: false, capabilities: { version: VERSION, event_off: {} } });
    for (const ev of [CREATED, DECIDED]) {
      expect(sw(w, ev).exists(), `${ev} offered`).toBe(true);
      expect(sw(w, ev).attributes('disabled'), `${ev} switchable`).toBeUndefined();
    }
  });

  it('greys what the caller could switch on, with the server’s sentence in the reader’s language', async () => {
    const said = { reason: 'e2e_approval', fixable: true, text: SAID_TR };
    const w = await notifications(
      { isAdmin: false, capabilities: { version: VERSION, event_off: { [CREATED]: said, [DECIDED]: said } } },
      'tr',
    );
    for (const ev of [CREATED, DECIDED]) {
      expect(sw(w, ev).attributes('disabled'), `${ev} cannot be switched`).toBeDefined();
      expect(w.find(`[data-testid="user-settings-event-off-${ev}"]`).text()).toBe(SAID_TR);
    }
  });

  it('does not offer what the caller cannot change, whatever the host says of the account', async () => {
    const w = await notifications({
      isAdmin: true,
      capabilities: {
        version: VERSION,
        event_off: { [CREATED]: { reason: 'e2e_approval', fixable: false, text: SAID_TR } },
      },
    });
    expect(sw(w, CREATED).exists()).toBe(false);
    expect(sw(w, DECIDED).exists()).toBe(true);
  });
});

// Every switch the dialog offers has a NAME — a sentence, never the key it is
// looked up by. ⚠ `e2e.password_changed` drew
// `userSettings.notifications.events.e2e_password_changed` in the explorer and
// the desktop app from v0.48.0 on: its label lived in the admin app's JSON
// only. This reads what a person sees; webhooks/eventCatalog names which table
// lacks a label.
describe('every switch the dialog offers has a name', () => {
  it.each(['en', 'tr'] as const)('in %s: no event is drawn as its raw key', async (locale) => {
    // A host that makes every event possible, so none is left out of the
    // list: the supertenant's administrator, every service on, and a tenant
    // whose policy asks for approval.
    const { host } = plainHost(
      {
        isAdmin: true,
        capabilities: {
          version: '0.49.0',
          caller_admin: true,
          antivirus: true,
          e2e_escrow: { enabled: true },
          app_plugins: { enabled: true },
          e2e_policy: { available: true, policy: 'approval' },
        },
      },
      locale,
    );
    const w = open(host);
    await flushPromises();
    await w.find('[data-testid="user-settings-tab-notifications"]').trigger('click');
    const table = locale === 'en' ? coreEn : tr;

    const drawn: Record<string, string> = {};
    for (const ev of WEBHOOK_EVENTS) {
      const sw = w.find(`[data-testid="user-settings-event-${ev}"]`);
      expect(sw.exists(), `${ev} is offered`).toBe(true);
      drawn[ev] = sw.element.closest('.fx-us__switch-row')?.querySelector('.fx-us__event-label')?.textContent?.trim() ?? '';
    }
    const raw = WEBHOOK_EVENTS.filter((ev) => !drawn[ev] || drawn[ev].includes('userSettings.') || drawn[ev] === userEventKey(ev));
    expect(raw, `drawn as a raw key or not at all: ${raw.join(', ')}`).toEqual([]);
    // …and each says what the explorer's catalogue says.
    for (const ev of WEBHOOK_EVENTS) expect(drawn[ev], ev).toBe(table[userEventKey(ev)]);
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

/* ── installing the web app on a phone (#190) ─────────────────────────────
 *
 * ⚠⚠ A phone had no install row: the downloads group is the desktop app's,
 * drawn only for a PC (`desktopApp.platform` is null on a phone), and the
 * reminder that does offer the web app was closed for good or never drawn.
 * The host's `installApp` row is where the offer stays. */
describe('the web app’s install row, when the host has one', () => {
  async function preferences(installApp: UserSettingsHost['installApp'], extra: Partial<UserSettingsHost> = {}, locale = 'en') {
    const { host } = plainHost({ installApp, ...extra }, locale);
    const w = open(host);
    await flushPromises();
    await w.find('[data-testid="user-settings-tab-preferences"]').trigger('click');
    return w;
  }

  it('a browser that offered an install: one button opens its dialog', async () => {
    const install = vi.fn(async () => 'accepted' as const);
    const w = await preferences({ state: 'prompt', install });
    const row = w.find('[data-testid="user-settings-install-app"]');
    expect(row.exists()).toBe(true);
    expect(row.attributes('data-state')).toBe('prompt');
    await w.find('[data-testid="user-settings-install-app-button"]').trigger('click');
    await flushPromises();
    expect(install).toHaveBeenCalledTimes(1);
  });

  it('an iPhone: the Share sheet, in words, and no button', async () => {
    const w = await preferences({ state: 'ios', install: vi.fn() });
    expect(w.find('[data-testid="user-settings-install-app-ios"]').text()).toBe(coreEn['install.iosInstructions']);
    expect(w.find('[data-testid="user-settings-install-app-button"]').exists()).toBe(false);
  });

  it('a phone whose browser has not offered one: its own menu', async () => {
    const w = await preferences({ state: 'menu', install: vi.fn() });
    expect(w.find('[data-testid="user-settings-install-app-menu"]').text()).toBe(coreEn['install.menuInstructions']);
  });

  it('nothing to offer (a PC, the installed app): no row', async () => {
    const w = await preferences({ state: null, install: vi.fn() });
    expect(w.find('[data-testid="user-settings-install-app"]').exists()).toBe(false);
  });

  it('speaks Turkish with Turkish characters', async () => {
    const w = await preferences({ state: 'ios', install: vi.fn() }, {}, 'tr');
    const row = w.find('[data-testid="user-settings-install-app"]');
    expect(row.text()).toContain(tr['userSettings.prefs.installApp']);
    expect(row.text()).toContain('Ana Ekrana Ekle');
  });

  it('an iPhone’s browser tab is told where its notifications are: the Home Screen app', async () => {
    const w = await preferences(
      { state: 'ios', install: vi.fn() },
      {
        browserNotifications: {
          desktopShell: false,
          permission: () => 'unsupported',
          enabled: () => false,
          setEnabled: vi.fn(),
          ask: vi.fn(async () => 'unsupported' as const),
        },
      },
    );
    await w.find('[data-testid="user-settings-tab-notifications"]').trigger('click');
    expect(w.find('[data-testid="user-settings-browser-ios"]').text()).toBe(coreEn['notifications.prefs.iosHomeScreen']);
  });
});
