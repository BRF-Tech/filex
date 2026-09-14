// The notification bell in the explorer's header — the one a NON-admin sees.
//
// Until 2026-09-14 the bell was drawn only inside the admin panel's top nav, so
// on /home and /explore — the whole product for an ordinary account — there was
// none: browser notifications arrived and the person could never open the list,
// mark one read or follow one to what it was about. Owner's ruling, verbatim:
// *"üst bara zil koyalım."*
//
// What is pinned here:
//   1. Explore.vue mounts THE existing component in its header cluster, to the
//      left of the avatar — not a second bell (a duplicate list of the same rows
//      is how the two start to disagree).
//   2. The count is on the button, in its accessible name as well.
//   3. A row says what happened in the reader's language — never a raw event
//      key like `share.created`, which is what the bell used to print.
//   4. Clicking a row goes to its target (*"tıklandığında yollarına gitmesini
//      istiyorum"*) and marks it read — for a non-admin.
//   5. The two things a non-admin must NOT be sent to: "View all" (the admin
//      audit page) and, for a row with no target, that same page.
//   6. THE LIST FOLLOWS THE BADGE. Measured 2026-09-14 as a non-admin: badge 9
//      over a list whose newest row predated the arrival — after closing and
//      reopening, and with the panel left open while a tenth arrived. The rows
//      were fetched once per page (a hook on the panel COMPONENT, which Headless
//      UI keeps mounted while closed) and the poll kept its own copy.
import { readFileSync } from 'node:fs';
import path from 'node:path';
import { describe, expect, it, beforeEach, vi } from 'vitest';
import { flushPromises, mount } from '@vue/test-utils';
import { createPinia, setActivePinia } from 'pinia';
import { createI18n } from 'vue-i18n';
import { createMemoryHistory, createRouter } from 'vue-router';

import NotificationBell from '@/components/NotificationBell.vue';
import { useAuthStore } from '@/stores/auth';
import { useNotificationsStore } from '@/stores/notifications';
import en from '@/locales/en.json';
import tr from '@/locales/tr.json';
import type { NotificationItem, User } from '@/api/types';

const markRead = vi.fn(async () => {});
let rows: NotificationItem[] = [];
const listCalls = vi.fn();

vi.mock('@/api/notifications', () => ({
  NotificationsApi: {
    list: vi.fn(async (p: unknown) => {
      listCalls(p);
      return { items: [...rows], total: rows.length, limit: 50, offset: 0 };
    }),
    unreadCount: vi.fn(async () => rows.filter((r) => !r.read_at).length),
    markRead: (...a: unknown[]) => markRead(...(a as [])),
    markAllRead: vi.fn(async () => {}),
  },
}));
vi.mock('@brftech/filex-core/style.css', () => ({}));

const EXPLORE_SRC = readFileSync(path.resolve(__dirname, '../../src/views/Explore.vue'), 'utf8');

function row(p: Partial<NotificationItem>): NotificationItem {
  return {
    id: 1,
    event: 'file.uploaded',
    severity: 'info',
    title: 'file.uploaded',
    body: '',
    meta: {},
    webhook_status: 'skipped' as NotificationItem['webhook_status'],
    created_at: '2026-09-14T04:22:00Z',
    read_at: null,
    ...p,
  };
}

const USER: User = {
  id: 42,
  email: 'kaya@example.com',
  username: 'kaya',
  display_name: 'Kaya',
  role: 'user',
  created_at: '2026-09-01T00:00:00Z',
  updated_at: '2026-09-01T00:00:00Z',
};

async function setup(opts: { role?: 'user' | 'admin'; locale?: 'en' | 'tr' } = {}) {
  const pinia = createPinia();
  setActivePinia(pinia);
  const auth = useAuthStore();
  auth.user = { ...USER, role: opts.role ?? 'user' };
  const notif = useNotificationsStore();
  notif.unreadCount = rows.filter((r) => !r.read_at).length;

  const Blank = { template: '<div />' };
  const router = createRouter({
    history: createMemoryHistory('/admin/'),
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
  await router.push('/home');
  await router.isReady();
  const push = vi.spyOn(router, 'push');

  const i18n = createI18n({
    legacy: false,
    locale: opts.locale ?? 'en',
    fallbackLocale: 'en',
    messages: { en, tr },
  });
  const w = mount(NotificationBell, { global: { plugins: [pinia, router, i18n] }, attachTo: document.body });
  return { w, router, push, notif };
}

async function openPanel(w: Awaited<ReturnType<typeof setup>>['w']) {
  await w.find('[data-testid="notification-bell"]').trigger('click');
  await flushPromises();
  await flushPromises();
}

/** The titles the open panel is showing, top to bottom. */
function shownTitles(): string[] {
  const panel = document.body.querySelector('[data-testid="notification-panel"]');
  return Array.from(panel?.querySelectorAll('.fx-bell__title') ?? []).map((e) => e.textContent!.trim());
}

beforeEach(() => {
  document.body.innerHTML = '';
  markRead.mockClear();
  listCalls.mockClear();
  rows = [
    row({
      id: 67,
      event: 'file.uploaded',
      title: 'file.uploaded',
      target: { kind: 'file', storage: 'qldemo', path: 'agentbell-olcum2/olcum.txt' },
      meta: { node: { name: 'olcum.txt', path: '/agentbell-olcum2/olcum.txt' } },
    }),
    row({ id: 66, event: 'share.created', title: 'share.created', target: { kind: 'share', id: 'abc' } }),
    row({ id: 51, event: 'update_available', title: 'filex v0.39.1 available', body: 'policy is manual' }),
  ];
});

describe('Explore puts the existing bell in its header', () => {
  it('mounts NotificationBell.vue in the header cluster, to the LEFT of the avatar', () => {
    expect(EXPLORE_SRC).toMatch(/import NotificationBell from '@\/components\/NotificationBell\.vue'/);
    const slot = EXPLORE_SRC.match(/<template #header-actions>([\s\S]*?)<\/template>/);
    expect(slot, 'the header-actions slot is gone').not.toBeNull();
    const bellAt = slot![1].indexOf('<NotificationBell');
    const avatarAt = slot![1].indexOf('<AccountMenu');
    expect(bellAt, 'no bell in the explorer header').toBeGreaterThanOrEqual(0);
    expect(bellAt).toBeLessThan(avatarAt);
  });

  it('draws no bell of its own — the one component is reused', () => {
    expect(EXPLORE_SRC).not.toMatch(/data-testid="notification-bell"/);
  });
});

describe('NotificationBell', () => {
  it('shows the unread count on the button and says it out loud', async () => {
    const { w } = await setup();
    expect(w.find('[data-testid="notification-bell-count"]').text()).toBe('3');
    expect(w.find('[data-testid="notification-bell"]').attributes('aria-label')).toBe('Notifications — 3 unread');
  });

  it('lists what happened in words, never a raw event key — in both languages', async () => {
    for (const locale of ['en', 'tr'] as const) {
      const { w } = await setup({ locale });
      await openPanel(w);
      const panel = document.body.querySelector('[data-testid="notification-panel"]');
      expect(panel, 'the panel did not open').not.toBeNull();
      // Teleported: it must not live inside the header, whose `.fe` clips it.
      expect(panel!.parentElement).toBe(document.body);
      const titles = Array.from(panel!.querySelectorAll('.fx-bell__title')).map((e) => e.textContent!.trim());
      expect(titles).toHaveLength(3);
      for (const t of titles) expect(t).not.toMatch(/^[a-z_]+\.[a-z_.]+$/);
      expect(titles[0]).toBe(locale === 'tr' ? 'Yeni dosya: olcum.txt' : 'New file: olcum.txt');
      w.unmount();
      document.body.innerHTML = '';
    }
  });

  it('a click goes to the file, in its folder, with the row selected — and marks it read', async () => {
    const { w, push } = await setup();
    await openPanel(w);
    const first = document.body.querySelector<HTMLButtonElement>('[data-testid="notification-row"]')!;
    first.click();
    await flushPromises();
    expect(markRead).toHaveBeenCalledWith(67);
    expect(push).toHaveBeenCalledWith({
      name: 'explore',
      query: { select: 'qldemo://agentbell-olcum2/olcum.txt' },
      hash: '#qldemo/agentbell-olcum2',
    });
    expect(document.body.querySelector('[data-testid="notification-panel"]')).toBeNull();
  });

  it('a row with no target does not throw a non-admin off the page', async () => {
    const { w, push } = await setup({ role: 'user' });
    await openPanel(w);
    const noTarget = document.body.querySelectorAll<HTMLButtonElement>('[data-testid="notification-row"]')[2];
    noTarget.click();
    await flushPromises();
    expect(markRead).toHaveBeenCalledWith(51);
    // The target-less destination is the admin audit page; the route guard
    // would bounce a non-admin to their front door.
    expect(push).not.toHaveBeenCalled();
  });

  it('re-reads the list on EVERY open — a row that arrived since the last one is in it', async () => {
    const { w } = await setup();
    await openPanel(w);
    expect(shownTitles()[0]).toBe('New file: olcum.txt');
    // close
    await w.find('[data-testid="notification-bell"]').trigger('click');
    await flushPromises();
    expect(document.body.querySelector('[data-testid="notification-panel"]')).toBeNull();

    rows = [
      row({ id: 68, event: 'file.uploaded', meta: { node: { name: 'arrived.txt', path: '/arrived.txt' } } }),
      ...rows,
    ];
    await openPanel(w);
    expect(shownTitles()).toHaveLength(4);
    expect(shownTitles()[0]).toBe('New file: arrived.txt');
    // …and the badge was read at the same moment as the list.
    expect(w.find('[data-testid="notification-bell-count"]').text()).toBe('4');
  });

  it('a row that raises the badge while the panel is OPEN appears without reopening', async () => {
    const { w, notif } = await setup();
    await openPanel(w);
    expect(shownTitles()).toHaveLength(3);

    rows = [
      row({ id: 69, event: 'file.uploaded', meta: { node: { name: 'live.txt', path: '/live.txt' } } }),
      ...rows,
    ];
    // What the App-level watcher runs every 15 s.
    await notif.syncUnread();
    await flushPromises();
    expect(w.find('[data-testid="notification-bell-count"]').text()).toBe('4');
    expect(shownTitles()[0]).toBe('New file: live.txt');
    expect(shownTitles()).toHaveLength(4);
  });

  it('"View all" is offered to an admin only', async () => {
    const user = await setup({ role: 'user' });
    await openPanel(user.w);
    expect(document.body.querySelector('.fx-bell__all')).toBeNull();
    user.w.unmount();
    document.body.innerHTML = '';

    const admin = await setup({ role: 'admin' });
    await openPanel(admin.w);
    expect(document.body.querySelector('.fx-bell__all')?.textContent?.trim()).toBe('View all');
  });
});
