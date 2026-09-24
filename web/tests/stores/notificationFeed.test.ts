// One notification list, refreshed from one place, read by the bell and the
// browser notifications alike.
//
// ⚠⚠ The bug (2026-09-14, measured as a non-admin): the App-level poll raised
// the badge while the bell's list — fetched once per page — never moved; badge
// 9 over a list whose newest row predated the arrival. The poll also fetched
// its OWN copy of the unread head to raise browser notifications. Two readers,
// two fetches, one of them never repeated.
//
// Pinned here, through the real watcher and the real store:
//   • a quiet tick costs one unread-count request and NO list fetch;
//   • a tick whose count moved refreshes `notif.feed` — the array the bell
//     draws — exactly once, and the browser notification is raised from that
//     same array rather than from a second request;
//   • a local "mark read" does not look like news to the next tick.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { defineComponent, h } from 'vue';
import { flushPromises, mount } from '@vue/test-utils';
import { createPinia, setActivePinia } from 'pinia';
import { createI18n } from 'vue-i18n';
import { createMemoryHistory, createRouter } from 'vue-router';

import { NOTIFY_POLL_MS, useNotificationWatcher } from '@/composables/useNotificationWatcher';
import { useAuthStore } from '@/stores/auth';
import { useNotificationsStore } from '@/stores/notifications';
import en from '@/locales/en.json';
import type { NotificationItem } from '@/api/types';

let rows: NotificationItem[] = [];
const listCalls = vi.fn();
const countCalls = vi.fn();

vi.mock('@/api/notifications', () => ({
  NotificationsApi: {
    list: vi.fn(async (p: unknown) => {
      listCalls(p);
      return { items: [...rows], total: rows.length, limit: 50, offset: 0 };
    }),
    unreadCount: vi.fn(async () => {
      countCalls();
      return rows.filter((r) => !r.read_at).length;
    }),
    markRead: vi.fn(async (id: number) => {
      rows = rows.map((r) => (r.id === id ? { ...r, read_at: '2026-09-14T08:00:00Z' } : r));
    }),
    markAllRead: vi.fn(async () => {}),
  },
}));

function row(id: number, name: string): NotificationItem {
  return {
    id,
    event: 'file.uploaded',
    severity: 'info',
    title: 'file.uploaded',
    body: '',
    meta: { node: { name, path: `/${name}` } },
    webhook_status: 'skipped' as NotificationItem['webhook_status'],
    created_at: '2026-09-14T07:00:00Z',
    read_at: null,
  };
}

const toasts: string[] = [];

async function mountWatcher() {
  const pinia = createPinia();
  setActivePinia(pinia);
  const auth = useAuthStore();
  auth.user = {
    id: 42,
    email: 'kaya@example.com',
    username: 'kaya',
    display_name: 'Kaya',
    role: 'user',
    created_at: '2026-09-01T00:00:00Z',
    updated_at: '2026-09-01T00:00:00Z',
  };
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/', component: { template: '<div/>' } }] });
  const i18n = createI18n({ legacy: false, locale: 'en', messages: { en } });
  const Host = defineComponent({
    setup() {
      useNotificationWatcher();
      return () => h('div');
    },
  });
  mount(Host, { global: { plugins: [pinia, router, i18n] } });
  await flushPromises();
  return useNotificationsStore();
}

async function tick() {
  await vi.advanceTimersByTimeAsync(NOTIFY_POLL_MS);
  await flushPromises();
}

beforeEach(() => {
  vi.useFakeTimers();
  listCalls.mockClear();
  countCalls.mockClear();
  toasts.length = 0;
  rows = [row(2, 'two.txt'), row(1, 'one.txt')];
  // ⚠ The BODY, not the title. Since the branding fix the title is the
  // instance's name (so the toast says who is notifying instead of printing
  // the bare origin) and the event's sentence is the body — see
  // `lib/browserNotify.brandedNotification`.
  const ctor = function (this: Record<string, unknown>, title: string, options?: NotificationOptions) {
    toasts.push(String(options?.body ?? title));
    this.close = () => {};
  } as unknown as { permission: string };
  ctor.permission = 'granted';
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  (window as any).Notification = ctor;
});

afterEach(() => {
  vi.useRealTimers();
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  delete (window as any).Notification;
});

describe('the one notification feed', () => {
  it('is filled when watching starts, with the count read beside it', async () => {
    const notif = await mountWatcher();
    expect(notif.feed.map((n) => n.id)).toEqual([2, 1]);
    expect(notif.unreadCount).toBe(2);
    expect(toasts).toEqual([]); // history is not news
  });

  it('a quiet tick fetches the count and nothing else', async () => {
    await mountWatcher();
    listCalls.mockClear();
    countCalls.mockClear();
    await tick();
    await tick();
    expect(countCalls).toHaveBeenCalledTimes(2);
    expect(listCalls).not.toHaveBeenCalled();
  });

  it('a tick whose count rose refreshes the feed once, and the toast comes from it', async () => {
    const notif = await mountWatcher();
    await tick(); // the baseline tick
    listCalls.mockClear();

    rows = [row(3, 'arrived.txt'), ...rows];
    await tick();
    expect(notif.unreadCount).toBe(3);
    expect(notif.feed.map((n) => n.id)).toEqual([3, 2, 1]);
    expect(listCalls).toHaveBeenCalledTimes(1);
    expect(toasts).toEqual(['New file: arrived.txt — /arrived.txt']);

    // The next quiet tick is quiet again.
    listCalls.mockClear();
    await tick();
    expect(listCalls).not.toHaveBeenCalled();
    expect(toasts).toEqual(['New file: arrived.txt — /arrived.txt']);
  });

  it('reading a row here does not make the next tick refetch or announce', async () => {
    const notif = await mountWatcher();
    await tick();
    listCalls.mockClear();
    await notif.markRead(2);
    expect(notif.unreadCount).toBe(1);
    expect(notif.feed.find((n) => n.id === 2)?.read_at).toBeTruthy();
    await tick();
    expect(listCalls).not.toHaveBeenCalled();
    expect(toasts).toEqual([]);
  });
});
