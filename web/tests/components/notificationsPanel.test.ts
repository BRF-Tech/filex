// The full list, INSIDE the explorer — rule 2 of docs/NOTIFICATIONS.md → "The
// bell, and who can reach it".
//
// Owner, 2026-09-20, verbatim: *"Tüm bildirimleri gör butonu explore'dan
// dışarı çıkıyor; adam admin değilse göremez."*
//
// What it replaces: "see all" pushed `/notifications`, the admin panel's
// instance-wide audit page, behind `requiresAdmin`. So the footer was hidden
// from every non-admin and an ordinary person could read the newest fifteen
// rows in the bell and had no way to reach the sixteenth — their own mail
// behind a permission they do not have.
//
// What is pinned here:
//   1. it reads the USER-scoped endpoints and NOTHING under /api/admin —
//      measured by failing every admin call outright, the way the server
//      would for a person who is not one;
//   2. it navigates nowhere to open, so no route guard can bounce anybody and
//      nobody loses the folder they were standing in;
//   3. paging, the unread filter, mark-one and mark-all all work from there;
//   4. rule 1 holds in this list too: a row with no target is inert here as
//      well as in the bell — it is the same row component;
//   5. a click goes where the row points, through the one resolver.
import { describe, expect, it, beforeEach, vi } from 'vitest';
import { flushPromises, mount } from '@vue/test-utils';
import { createPinia, setActivePinia } from 'pinia';
import { createI18n } from 'vue-i18n';
import { createMemoryHistory, createRouter } from 'vue-router';

import NotificationsPanel from '@/components/NotificationsPanel.vue';
import { useAuthStore } from '@/stores/auth';
import { useNotificationsStore } from '@/stores/notifications';
import en from '@/locales/en.json';
import tr from '@/locales/tr.json';
import type { NotificationItem, User } from '@/api/types';

/** Every row the fake server holds, newest first. */
let all: NotificationItem[] = [];
const listCalls = vi.fn();
const markRead = vi.fn(async (id: number) => {
  all = all.map((n) => (n.id === id ? { ...n, read_at: '2026-09-20T10:00:00Z' } : n));
});
const markAllRead = vi.fn(async () => {
  all = all.map((n) => ({ ...n, read_at: n.read_at ?? '2026-09-20T10:00:00Z' }));
});

// ⚠⚠ The admin calls THROW (inline below, because `vi.mock`'s factory is
// hoisted above every top-level binding in this file). That is the whole point
// of the test: the panel is for somebody who cannot make those calls, and a
// panel that quietly fell back to an admin endpoint would pass a gentler mock
// while 403-ing in front of a real user.
vi.mock('@/api/notifications', () => ({
  NotificationsApi: {
    list: vi.fn(async (p: { unread?: boolean; limit?: number; offset?: number } = {}) => {
      listCalls(p);
      const pool = p.unread ? all.filter((n) => !n.read_at) : all;
      const offset = p.offset ?? 0;
      const limit = p.limit ?? 25;
      return { items: pool.slice(offset, offset + limit), total: pool.length, limit, offset };
    }),
    unreadCount: vi.fn(async () => all.filter((n) => !n.read_at).length),
    markRead: (...a: unknown[]) => markRead(...(a as [number])),
    markAllRead: () => markAllRead(),
    adminList: vi.fn(() => {
      throw new Error('403 - this account is not an administrator');
    }),
    sendTest: vi.fn(() => {
      throw new Error('403 - this account is not an administrator');
    }),
    getWebhookConfig: vi.fn(() => {
      throw new Error('403 - this account is not an administrator');
    }),
    updateWebhookConfig: vi.fn(() => {
      throw new Error('403 - this account is not an administrator');
    }),
    getSettings: vi.fn(async () => ({ in_app_enabled: true, muted_events: [] })),
    updateSettings: vi.fn(async () => ({ in_app_enabled: true, muted_events: [] })),
  },
}));
vi.mock('@brftech/filex-core/style.css', () => ({}));

function row(p: Partial<NotificationItem>): NotificationItem {
  return {
    id: 1,
    event: 'file.uploaded',
    severity: 'info',
    title: 'file.uploaded',
    body: '',
    meta: {},
    webhook_status: 'skipped' as NotificationItem['webhook_status'],
    created_at: '2026-09-20T04:22:00Z',
    read_at: null,
    ...p,
  };
}

const PLAIN_USER: User = {
  id: 42,
  email: 'gokcil@local',
  username: 'gokcil',
  display_name: 'Gökçil',
  role: 'user',
  created_at: '2026-09-01T00:00:00Z',
  updated_at: '2026-09-01T00:00:00Z',
};

async function setup(locale: 'en' | 'tr' = 'en') {
  const pinia = createPinia();
  setActivePinia(pinia);
  const auth = useAuthStore();
  auth.user = { ...PLAIN_USER };
  const notif = useNotificationsStore();
  notif.unreadCount = all.filter((n) => !n.read_at).length;

  const Blank = { template: '<div />' };
  const router = createRouter({
    history: createMemoryHistory('/drive/'),
    routes: [
      { path: '/home', name: 'home', component: Blank },
      { path: '/explore', name: 'explore', component: Blank, meta: { public: true } },
      {
        path: '/',
        component: { template: '<router-view />' },
        meta: { requiresAdmin: true },
        children: [{ path: 'notifications', name: 'notifications', component: Blank }],
      },
    ],
  });
  await router.push('/explore');
  await router.isReady();
  const push = vi.spyOn(router, 'push');

  const i18n = createI18n({ legacy: false, locale, fallbackLocale: 'en', messages: { en, tr } });
  const w = mount(NotificationsPanel, { global: { plugins: [pinia, router, i18n] }, attachTo: document.body });
  return { w, notif, push, router };
}

const screen = () => document.body.querySelector('[data-testid="notifications-screen"]');
const rowsOnScreen = () =>
  Array.from(document.body.querySelectorAll<HTMLElement>('[data-testid="notification-row"]'));
const titles = () =>
  Array.from(document.body.querySelectorAll('.fx-nrow__title')).map((e) => e.textContent!.trim());

async function openIt(notif: ReturnType<typeof useNotificationsStore>) {
  notif.openPanel();
  await flushPromises();
  await flushPromises();
}

beforeEach(() => {
  document.body.innerHTML = '';
  listCalls.mockClear();
  markRead.mockClear();
  markAllRead.mockClear();
  // 30 rows: more than one page of 25, which is what makes paging real.
  all = [
    row({
      id: 100,
      event: 'file.uploaded',
      target: { kind: 'file', storage: 'qldemo', path: 'depo/rapor.pdf' },
      meta: { node: { name: 'rapor.pdf', path: '/depo/rapor.pdf' } },
    }),
    row({ id: 99, event: 'update_available', title: 'filex v0.43.0 available', body: 'policy is manual' }),
    ...Array.from({ length: 28 }, (_, i) =>
      row({
        id: 90 - i,
        event: 'file.uploaded',
        meta: { node: { name: `f${i}.txt`, path: `/f${i}.txt` } },
      }),
    ),
  ];
});

describe('NotificationsPanel — a person reads their own notifications', () => {
  it('opens over the explorer and navigates NOWHERE to do it', async () => {
    const { notif, push } = await setup();
    expect(screen(), 'the screen is drawn before it is asked for').toBeNull();
    await openIt(notif);
    expect(screen(), 'the full list did not open').not.toBeNull();
    // ⚠ Not a route. A route would have to escape `requiresAdmin`, and it
    // would unmount the explorer to show a list — throwing away the folder
    // the person was standing in, which is the same complaint one layer down.
    expect(push).not.toHaveBeenCalled();
  });

  it('reads the user-scoped endpoint and never touches /api/admin', async () => {
    const { notif } = await setup();
    await openIt(notif);
    expect(listCalls).toHaveBeenCalled();
    // The admin calls are mocked to THROW; getting here at all proves none
    // were made, and the rows prove the user-scoped one was.
    expect(notif.mine.length).toBe(25);
    expect(notif.mineTotal).toBe(30);
    expect(titles().length).toBe(25);
  });

  it('pages: the sixteenth notification — and the thirtieth — are reachable', async () => {
    // The whole complaint in one assertion. The bell shows fifteen; this is
    // where the rest of them live.
    const { notif } = await setup();
    await openIt(notif);
    expect(titles()).toHaveLength(25);
    const pager = document.body.querySelector('[data-testid="notifications-screen-pager"]');
    expect(pager, 'no pager over 30 rows').not.toBeNull();

    const next = Array.from(pager!.querySelectorAll('button')).find((b) => /next/i.test(b.textContent ?? ''))!;
    next.click();
    await flushPromises();
    await flushPromises();
    expect(notif.mineOffset).toBe(25);
    expect(titles()).toHaveLength(5);
    expect(listCalls).toHaveBeenLastCalledWith({ unread: false, limit: 25, offset: 25 });
  });

  it('filters to unread, and marks one read without going anywhere', async () => {
    const { notif, push } = await setup();
    await openIt(notif);

    const one = document.body.querySelector<HTMLButtonElement>(
      '[data-testid="notifications-screen-mark-one"]',
    )!;
    one.click();
    await flushPromises();
    expect(markRead).toHaveBeenCalledWith(100);
    // ⚠ Marking read is not navigation. A long list is mostly USED for this.
    expect(push).not.toHaveBeenCalled();
    expect(screen(), 'the list closed when a row was marked read').not.toBeNull();

    const filter = document.body.querySelector<HTMLButtonElement>(
      '[data-testid="notifications-screen-unread"]',
    )!;
    filter.click();
    await flushPromises();
    await flushPromises();
    expect(listCalls).toHaveBeenLastCalledWith({ unread: true, limit: 25, offset: 0 });
    expect(notif.mineTotal).toBe(29);
  });

  it('marks all read, and the badge and the rows agree afterwards', async () => {
    const { notif } = await setup();
    await openIt(notif);
    const markAll = document.body.querySelector<HTMLButtonElement>(
      '[data-testid="notifications-screen-mark-all"]',
    )!;
    markAll.click();
    await flushPromises();
    expect(markAllRead).toHaveBeenCalled();
    expect(notif.unreadCount).toBe(0);
    // Every row in front of the person stops looking unread — including the
    // ones in the bell behind this panel, which is the same store.
    expect(notif.mine.every((n) => n.read_at)).toBe(true);
    expect(document.body.querySelector('[data-testid="unread-badge"]')).toBeNull();
  });

  it('rule 1 holds here too: a row with nothing to open is inert', async () => {
    const { notif, push } = await setup();
    await openIt(notif);
    const list = rowsOnScreen();
    const noTarget = list[1]; // update_available — nothing to open
    expect(noTarget.getAttribute('data-clickable')).toBe('no');
    expect(noTarget.tagName).toBe('DIV');
    noTarget.click();
    await flushPromises();
    expect(push).not.toHaveBeenCalled();
    expect(screen(), 'an inert row closed the list').not.toBeNull();
  });

  it('a click goes to the file — the one resolver, the explorer underneath', async () => {
    const { notif, push } = await setup();
    await openIt(notif);
    const first = rowsOnScreen()[0];
    expect(first.getAttribute('data-clickable')).toBe('yes');
    first.click();
    await flushPromises();
    expect(markRead).toHaveBeenCalledWith(100);
    expect(push).toHaveBeenCalledWith({
      name: 'explore',
      query: { select: 'qldemo://depo/rapor.pdf' },
      hash: '#qldemo/depo',
    });
    // ⚠ Closed FIRST: the destination is the explorer underneath, and a list
    // left open over it would be covering the file it just revealed.
    expect(screen()).toBeNull();
  });

  it('says what happened in the reader’s language, never a raw event key', async () => {
    const { notif } = await setup('tr');
    await openIt(notif);
    expect(titles()[0]).toBe('Yeni dosya: rapor.pdf');
    for (const t of titles()) expect(t).not.toMatch(/^[a-z_]+\.[a-z_.]+$/);
  });
});
