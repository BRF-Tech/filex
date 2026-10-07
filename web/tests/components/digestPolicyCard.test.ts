// Admin → Notifications → Notification digest (components/DigestPolicyCard,
// GET/PATCH /api/admin/notifications/digest): how long the kinds that are not
// urgent are held (1-15 minutes) and which kinds are urgent for everybody who
// did not choose otherwise. Out of the box every kind is (the owner's
// decision, 2026-10-06), and the card says so in words.
//
// ⚠ Red before the digest: the card does not exist.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils';
import { createPinia, setActivePinia } from 'pinia';
import { createI18n } from 'vue-i18n';

import en from '@/locales/en.json';
import tr from '@/locales/tr.json';
import type { DigestPolicy } from '@/api/types';

const EVENTS = ['file.uploaded', 'file.infected', 'comment.added', 'admin_test', 'update_available', 'disk_full'];

/** The built-in defaults: every kind told at once. */
const POLICY: DigestPolicy = {
  window_minutes: 1,
  urgent_events: EVENTS,
  saved: false,
  scope: 'tenant',
  tenant: { id: 7, name: 'Alfa' },
  defaults: { window_minutes: 1, urgent_events: EVENTS },
  events: EVENTS,
  admin_events: ['update_available', 'disk_full'],
  window_min: 1,
  window_max: 15,
};

const getDigestPolicy = vi.fn<[], Promise<DigestPolicy>>();
const updateDigestPolicy = vi.fn<[{ window_minutes?: number; urgent_events?: string[] | null }], Promise<DigestPolicy>>();

vi.mock('@/api/notifications', () => ({
  NotificationsApi: {
    getDigestPolicy: (...a: unknown[]) => getDigestPolicy(...(a as [])),
    updateDigestPolicy: (...a: unknown[]) =>
      updateDigestPolicy(...(a as [{ window_minutes?: number; urgent_events?: string[] | null }])),
  },
}));

import DigestPolicyCard from '@/components/DigestPolicyCard.vue';

const live: VueWrapper[] = [];

function mountCard(locale = 'en') {
  const i18n = createI18n({ legacy: false, locale, fallbackLocale: 'en', messages: { en, tr } });
  const w = mount(DigestPolicyCard, { global: { plugins: [i18n] }, attachTo: document.body });
  live.push(w);
  return w;
}

function toggleOf(w: VueWrapper, id: string) {
  return w.find(`[data-testid="${id}"] [role="switch"]`);
}

describe('the digest defaults card', () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    vi.clearAllMocks();
    getDigestPolicy.mockResolvedValue(POLICY);
    updateDigestPolicy.mockImplementation(async (p) => ({
      ...POLICY,
      saved: true,
      window_minutes: p.window_minutes ?? POLICY.window_minutes,
      urgent_events: p.urgent_events === null ? POLICY.defaults.urgent_events : (p.urgent_events ?? POLICY.urgent_events),
    }));
  });
  afterEach(() => {
    while (live.length) live.pop()!.unmount();
  });

  it('says whose defaults they are, and that every kind is told at once', async () => {
    const w = mountCard('tr');
    await flushPromises();
    expect(w.find('[data-testid="notif-digest-scope"]').text()).toBe('Alfa kiracısı için');
    expect(w.find('[data-testid="notif-digest-all-urgent"]').text()).toBe(
      'Varsayılan: her tür hemen bildirilir. Özet için hiçbir şey bekletilmez.',
    );
    expect(toggleOf(w, 'notif-digest-urgent-file.infected').attributes('aria-checked')).toBe('true');
    expect(toggleOf(w, 'notif-digest-urgent-file.uploaded').attributes('aria-checked')).toBe('true');
    expect(toggleOf(w, 'notif-digest-urgent-admin').attributes('aria-checked')).toBe('true');
    // The digest itself is never held: no switch.
    expect(w.find('[data-testid="notif-digest-urgent-notification.digest"]').exists()).toBe(false);
  });

  it('holds kinds by default and saves the window, keeping a kind it does not show', async () => {
    const w = mountCard();
    await flushPromises();
    await w.find('input[name="digest-window"]').setValue('5');
    await toggleOf(w, 'notif-digest-urgent-file.uploaded').trigger('click');
    await toggleOf(w, 'notif-digest-urgent-admin').trigger('click');
    // Something is held now: the card no longer says nothing is.
    expect(w.find('[data-testid="notif-digest-all-urgent"]').exists()).toBe(false);
    await w.find('[data-testid="notif-digest-save"]').trigger('click');
    await flushPromises();
    expect(updateDigestPolicy).toHaveBeenCalledTimes(1);
    const sent = updateDigestPolicy.mock.calls[0][0];
    expect(sent.window_minutes).toBe(5);
    expect([...(sent.urgent_events ?? [])].sort()).toEqual(['admin_test', 'comment.added', 'file.infected']);
  });

  it('refuses a window outside 1-15 minutes before it asks the server', async () => {
    const w = mountCard();
    await flushPromises();
    await w.find('input[name="digest-window"]').setValue('16');
    expect(w.find('[data-testid="notif-digest-save"]').attributes('disabled')).toBeDefined();
    expect(w.text()).toContain('Choose between 1 and 15 minutes.');
    expect(updateDigestPolicy).not.toHaveBeenCalled();
  });

  it('restores the built-in defaults', async () => {
    const w = mountCard();
    await flushPromises();
    await w.find('[data-testid="notif-digest-restore"]').trigger('click');
    await flushPromises();
    expect(updateDigestPolicy).toHaveBeenCalledWith({ window_minutes: 1, urgent_events: null });
  });
});
