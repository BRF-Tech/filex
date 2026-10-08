// "Push notifications on this device" in the user settings dialog (#191) - the
// row core draws for a host that runs filex's own service worker
// (lib/userSettingsHost `webPush`, lib/webPush).
//
// What is pinned:
//   1. No row where the host gives none (the desktop app, an embedded explorer).
//   2. The switch says this device's state, the person's devices are listed
//      (THIS one marked), one can be removed, and a test can be sent.
//   3. Where push cannot work the switch does not move, and says why - the
//      server's reason only to an administrator, who can act on it; an
//      iPhone's Safari tab is told where push does work.
//   4. ⚠ The tap calls enable() at once, nothing awaited before it: Safari
//      asks for the permission only from the tap itself.
//   5. Turkish with Turkish characters.
//
// ⚠ Red on the code before it: the host has no `webPush` row and the dialog
// draws none.
import { describe, expect, it, vi } from 'vitest';
import { flushPromises, mount } from '@vue/test-utils';

import UserSettingsDialog from '@brftech/filex-core/src/components/UserSettingsDialog.vue';
import type { UserSettingsHost } from '@brftech/filex-core/src/lib/userSettingsHost';
import type { WebPushController, WebPushState } from '@brftech/filex-core/src/lib/webPush';
import { en } from '@brftech/filex-core/src/locales/en';
import { tr } from '@brftech/filex-core/src/locales/tr';
import { answerAccountPrefs } from '../helpers/accountPrefs';

answerAccountPrefs();

const DEVICES = [
  { id: 3, label: 'Firefox · Windows', endpoint_hash: 'a', created_at: '2026-10-01T09:00:00Z' },
  { id: 4, label: 'Safari · iPhone', endpoint_hash: 'b', created_at: '2026-10-07T09:00:00Z' },
];

function pushState(over: Partial<WebPushState> = {}): WebPushState {
  return {
    supported: true,
    permission: 'granted',
    status: { available: true, public_key: 'BKEY', devices: DEVICES },
    on: true,
    thisDevice: 4,
    ...over,
  };
}

function fakePush(start: WebPushState) {
  let current = start;
  const ctl = {
    state: vi.fn(async () => current),
    enable: vi.fn(async () => (current = { ...current, on: true, thisDevice: 4 })),
    disable: vi.fn(async () => (current = { ...current, on: false, thisDevice: null })),
    remove: vi.fn(async (id: number) => {
      const devices = (current.status?.devices ?? []).filter((d) => d.id !== id);
      current = { ...current, status: { ...current.status!, devices } };
      return current;
    }),
    test: vi.fn(async () => ({ sent: 2, failed: 0 })),
    reconcile: vi.fn(async () => undefined),
    forgetDevice: vi.fn(async () => undefined),
  } satisfies WebPushController;
  return ctl;
}

function host(extra: Partial<UserSettingsHost> = {}, locale = 'en'): { host: UserSettingsHost; toast: ReturnType<typeof vi.fn> } {
  const toast = vi.fn();
  return {
    toast,
    host: {
      locale,
      user: { id: 7, email: 'ayse@example.com', username: 'ayse', display_name: 'Ayşe', role: 'user' },
      isAdmin: false,
      demoReadOnly: false,
      capabilities: { version: '0.54.0', caller_admin: false },
      api: {
        updateProfile: vi.fn(async (p) => p),
        changePassword: vi.fn(async () => {}),
        enrollTotp: vi.fn(async () => ({ secret: 'S', qr_svg: '<svg></svg>' })),
        verifyTotp: vi.fn(async () => {}),
        disableTotp: vi.fn(async () => {}),
        quota: vi.fn(async () => ({ used_bytes: 0, quota_bytes: 0, percent_used: 0, unlimited: true })),
        notificationSettings: vi.fn(async () => ({ in_app_enabled: true, muted_events: [] })),
        updateNotificationSettings: vi.fn(async (p) => p),
      },
      setUser: vi.fn(),
      toast,
      errorText: (e, fb) => (e as Error)?.message || fb,
      zone: { get: () => '', set: vi.fn() },
      mode: { get: () => 'auto', set: vi.fn() },
      ...extra,
    },
  };
}

async function notificationsPane(h: UserSettingsHost) {
  const w = mount(UserSettingsDialog, { props: { modelValue: true, host: h }, attachTo: document.body });
  await flushPromises();
  await w.find('[data-testid="user-settings-tab-notifications"]').trigger('click');
  await flushPromises();
  return w;
}

describe('push notifications on this device', () => {
  it('draws no row for a host that has no push of its own', async () => {
    const w = await notificationsPane(host().host);
    expect(w.find('[data-testid="user-settings-push-row"]').exists()).toBe(false);
    w.unmount();
  });

  it('says this device is on, lists the person’s devices and marks this one', async () => {
    const push = fakePush(pushState());
    const w = await notificationsPane(host({ webPush: push }).host);
    const sw = w.find('[data-testid="user-settings-push"]');
    expect(sw.attributes('aria-checked')).toBe('true');
    expect(sw.attributes('disabled')).toBeUndefined();
    expect(w.find('[data-testid="user-settings-push-state"]').text()).toBe(en['notifications.prefs.pushOn']);
    const rows = w.findAll('[data-testid^="user-settings-push-device-"]');
    expect(rows).toHaveLength(2);
    expect(w.find('[data-testid="user-settings-push-device-4"]').text()).toContain(en['notifications.prefs.pushThisDevice']);
    expect(w.find('[data-testid="user-settings-push-device-3"]').text()).not.toContain(en['notifications.prefs.pushThisDevice']);
    // ⚠ A list, not a table: one row per device, never a <table>.
    expect(w.find('[data-testid="user-settings-push-devices"] table').exists()).toBe(false);
    w.unmount();
  });

  it('a device is removed, a test is sent, and the switch turns this device off', async () => {
    const push = fakePush(pushState());
    const { host: h, toast } = host({ webPush: push });
    const w = await notificationsPane(h);
    await w.find('[data-testid="user-settings-push-remove-3"]').trigger('click');
    await flushPromises();
    expect(push.remove).toHaveBeenCalledWith(3);
    expect(w.find('[data-testid="user-settings-push-device-3"]').exists()).toBe(false);

    await w.find('[data-testid="user-settings-push-test"]').trigger('click');
    await flushPromises();
    expect(push.test).toHaveBeenCalled();
    expect(toast).toHaveBeenCalledWith('success', 'Test sent to 2 devices');

    await w.find('[data-testid="user-settings-push"]').trigger('click');
    await flushPromises();
    expect(push.disable).toHaveBeenCalled();
    expect(w.find('[data-testid="user-settings-push"]').attributes('aria-checked')).toBe('false');
    w.unmount();
  });

  it('⚠ the tap turns it on at once - nothing awaited before it (Safari asks only from the tap)', async () => {
    const push = fakePush(pushState({ on: false, thisDevice: null, permission: 'default' }));
    const w = await notificationsPane(host({ webPush: push }).host);
    const sw = w.find('[data-testid="user-settings-push"]');
    expect(sw.attributes('aria-checked')).toBe('false');
    (sw.element as HTMLButtonElement).click();
    expect(push.enable).toHaveBeenCalledTimes(1);
    await flushPromises();
    expect(w.find('[data-testid="user-settings-push"]').attributes('aria-checked')).toBe('true');
    w.unmount();
  });

  it('where push cannot work, the switch does not move and says why', async () => {
    const off = pushState({ on: false, thisDevice: null, status: { available: false, reason: 'no_secret_key', devices: [] } });
    // An administrator is told what to do about it…
    let w = await notificationsPane(host({ webPush: fakePush(off), isAdmin: true }).host);
    expect(w.find('[data-testid="user-settings-push"]').attributes('disabled')).toBeDefined();
    expect(w.find('[data-testid="user-settings-push-state"]').text()).toBe(en['notifications.prefs.pushUnavailable']);
    expect(w.find('[data-testid="user-settings-push-why"]').text()).toBe(en['notifications.prefs.pushNoSecretKey']);
    w.unmount();
    // …everybody else only that it is not available.
    w = await notificationsPane(host({ webPush: fakePush(off) }).host);
    expect(w.find('[data-testid="user-settings-push-why"]').exists()).toBe(false);
    w.unmount();

    // An iPhone's Safari tab: no push at all, and where it does work.
    const ios = pushState({ supported: false, permission: 'unsupported', on: false, thisDevice: null });
    w = await notificationsPane(
      host({ webPush: fakePush(ios), installApp: { state: 'ios', install: vi.fn(async () => 'unavailable' as const) } }).host,
    );
    expect(w.find('[data-testid="user-settings-push"]').attributes('disabled')).toBeDefined();
    expect(w.find('[data-testid="user-settings-push-state"]').text()).toBe(en['notifications.prefs.pushUnsupported']);
    expect(w.find('[data-testid="user-settings-push-why"]').text()).toBe(en['notifications.prefs.iosHomeScreen']);
    w.unmount();

    // Blocked by the browser: the switch cannot turn it on, and says where to fix it.
    const blocked = pushState({ permission: 'denied', on: false, thisDevice: null });
    w = await notificationsPane(host({ webPush: fakePush(blocked) }).host);
    expect(w.find('[data-testid="user-settings-push"]').attributes('disabled')).toBeDefined();
    expect(w.find('[data-testid="user-settings-push-why"]').text()).toBe(en['notifications.prefs.permDeniedHint']);
    w.unmount();
  });

  it('speaks Turkish with Turkish characters', async () => {
    const w = await notificationsPane(host({ webPush: fakePush(pushState()) }, 'tr').host);
    const row = w.find('[data-testid="user-settings-push-row"]').text();
    expect(row).toContain(tr['notifications.prefs.push']);
    expect(row).toContain('Bu cihazda anlık bildirim');
    expect(row).toContain(tr['notifications.prefs.pushDevices']);
    expect(row).not.toMatch(/anlik|Anlik/);
    w.unmount();
  });
});
