// One notification list, refreshed from one place, read by the bell and the
// browser notifications alike.
//
// ⚠⚠ The bug (2026-09-14, measured as a non-admin): the App-level poll raised
// the badge while the bell's list — fetched once per page — never moved; badge
// 9 over a list whose newest row predated the arrival. The poll also fetched
// its OWN copy of the unread head to raise browser notifications. Two readers,
// two fetches, one of them never repeated.
//
// Pinned here, through the real watcher, the real store and the real API
// module (only the HTTP client under it is a fake server):
//   • a quiet tick costs one unread-count request and NO list fetch;
//   • a tick whose count moved refreshes `notif.feed` — the array the bell
//     draws — exactly once, and the browser notification is raised from that
//     same array rather than from a second request;
//   • a local "mark read" does not look like news to the next tick;
//   • the words are the SERVER's (#191, backend notify say.go), said in the
//     language of the reader's ACCOUNT: the list names no language, a
//     language picked on screen is written to the account FIRST, and only
//     then is the list asked for again (it comes back said in the new one).
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { defineComponent, h } from 'vue';
import { flushPromises, mount } from '@vue/test-utils';
import { createPinia, setActivePinia } from 'pinia';
import { createMemoryHistory, createRouter } from 'vue-router';

import { configurePrefs } from '@brftech/filex-core';
import { NOTIFY_POLL_MS, useNotificationWatcher } from '@/composables/useNotificationWatcher';
import { i18n, setStoredLocale } from '@/i18n';
import { useAuthStore } from '@/stores/auth';
import { useNotificationsStore } from '@/stores/notifications';
import type { NotificationItem } from '@/api/types';

/** The rows the fake server holds: what happened, not yet said in any language. */
let rows: NotificationItem[] = [];
const listCalls = vi.fn();
const countCalls = vi.fn();
/** The account's language as the fake server holds it (users.locale). */
let accountLang = 'en';
/** What reached the fake server, in order: `put:<lang>` and `list`. */
const wire: string[] = [];

// ⚠ The CLIENT is the fake, not NotificationsApi: what the API module sends is
// part of what is pinned here. The fake server answers the list the way the
// real one does - each row said in the language of the ACCOUNT (`said`,
// below), whatever the request carries.
vi.mock('@/api/client', () => ({
  extractError: (_e: unknown, fallback?: string) => fallback ?? '',
  api: {
    get: vi.fn(async (url: string, cfg?: { params?: Record<string, unknown> }) => {
      if (url === '/notifications') {
        listCalls(cfg?.params);
        wire.push('list');
        return { data: { items: rows.map((r) => said(r, accountLang)), total: rows.length, limit: 50, offset: 0 } };
      }
      if (url === '/notifications/unread-count') {
        countCalls();
        return { data: { count: rows.filter((r) => !r.read_at).length } };
      }
      return { data: {} };
    }),
    post: vi.fn(async (url: string) => {
      const read = /^\/notifications\/(\d+)\/read$/.exec(url);
      if (read) {
        const id = Number(read[1]);
        rows = rows.map((r) => (r.id === id ? { ...r, read_at: '2026-09-14T08:00:00Z' } : r));
      }
      return { data: {} };
    }),
    patch: vi.fn(async () => ({ data: {} })),
    put: vi.fn(async () => ({ data: {} })),
    delete: vi.fn(async () => ({ data: {} })),
  },
}));

function row(id: number, name: string): NotificationItem {
  return {
    id,
    event: 'file.uploaded',
    severity: 'info',
    title: '',
    body: '',
    meta: { node: { name, path: `/${name}` } },
    webhook_status: 'skipped' as NotificationItem['webhook_status'],
    created_at: '2026-09-14T07:00:00Z',
    read_at: null,
  };
}

/** A row as the server answers it in `lang` (`server.notify.file.uploaded.*`). */
function said(r: NotificationItem, lang: string): NotificationItem {
  const node = (r.meta as { node: { name: string; path: string } }).node;
  return { ...r, title: lang === 'tr' ? `Yeni dosya: ${node.name}` : `New file: ${node.name}`, body: node.path };
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
  const Host = defineComponent({
    setup() {
      useNotificationWatcher();
      return () => h('div');
    },
  });
  // ⚠ The app's OWN i18n, as main.ts installs it: the watcher watches the
  // language on screen through it. Two instances would be two languages.
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
  i18n.global.locale.value = 'en';
  listCalls.mockClear();
  countCalls.mockClear();
  toasts.length = 0;
  accountLang = 'en';
  wire.length = 0;
  // The account's preference document: a PUT is the account's language
  // changing (the server mirrors `locale` into users.locale, userprefs.go).
  configurePrefs({
    surface: 'web',
    base: '',
    fetchImpl: (async (_url: string, init?: RequestInit) => {
      if (init?.method === 'PUT') {
        const doc = JSON.parse(String(init.body ?? '{}')) as { prefs?: { locale?: string } };
        if (doc.prefs?.locale) accountLang = doc.prefs.locale;
        wire.push(`put:${doc.prefs?.locale ?? ''}`);
      }
      return { ok: true, status: 200, json: async () => ({ ok: true }) } as unknown as Response;
    }) as typeof fetch,
  });
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
    // The server's words, as the list brought them: the toast composes none.
    expect(toasts).toEqual(['New file: arrived.txt - /arrived.txt']);

    // The next quiet tick is quiet again.
    listCalls.mockClear();
    await tick();
    expect(listCalls).not.toHaveBeenCalled();
    expect(toasts).toEqual(['New file: arrived.txt - /arrived.txt']);
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

  it('a language picked on screen is written to the ACCOUNT first, and the list asked again comes back said in it', async () => {
    // ⚠ #191, translated at the last stop: the server says the rows in the
    // account's language and the request names none (no `lang=`). Red on the
    // third-round code: the list named the screen's language (`lang=`), so
    // the bell could read Turkish while the push, the email and the desktop
    // toast of the same account read English.
    const notif = await mountWatcher();
    for (const [params] of listCalls.mock.calls) expect(params ?? {}).not.toHaveProperty('lang');
    expect(notif.feed.map((n) => n.title)).toEqual(['New file: two.txt', 'New file: one.txt']);
    // The full list is open too: it is said anew as well.
    notif.openPanel();
    await flushPromises();
    listCalls.mockClear();
    wire.length = 0;

    setStoredLocale('tr');
    await flushPromises();
    await flushPromises();
    await flushPromises();

    // The account's language changed BEFORE the list was asked again - asked
    // before, it would come back in the old language.
    expect(wire[0]).toBe('put:tr');
    expect(wire.filter((w) => w === 'list')).toHaveLength(2);
    expect(accountLang).toBe('tr');
    // The bell's feed and the open full list, each asked once more, naming
    // no language, and said in the account's new one.
    expect(listCalls).toHaveBeenCalledTimes(2);
    for (const [params] of listCalls.mock.calls) expect(params ?? {}).not.toHaveProperty('lang');
    expect(notif.feed.map((n) => n.title)).toEqual(['Yeni dosya: two.txt', 'Yeni dosya: one.txt']);
    expect(notif.mine.map((n) => n.title)).toEqual(['Yeni dosya: two.txt', 'Yeni dosya: one.txt']);
    // A language is not news: nothing is announced.
    expect(toasts).toEqual([]);
    setStoredLocale('en');
    await flushPromises();
  });
});
