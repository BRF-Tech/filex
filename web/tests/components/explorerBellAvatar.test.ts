// The bell and the avatar as CORE components — what the desktop app draws.
//
// 2026-09-27, owner: *"masaüstü uygulamasına bildirimler geliyor ama
// uygulamanın içinde onlara bakılacak bir panel yok … ne bildirimler menüsü var
// ne de kullanıcı ayarları bölgesi"*. The web had both, in web/src, bound to its
// pinia store, its router and vue-i18n — none of which a `<filex-explorer>`
// host has. They moved into packages/core; the explorer draws them itself when
// its host asks (`config.notifications`, `config.account`).
//
// What is pinned here, and why each needs saying:
//   1. The core components work with NO pinia, NO router, NO vue-i18n — over a
//      plain `reactive(createNotificationFeed(…))`. Otherwise the desktop would
//      be handed a component that only runs inside the admin app.
//   2. The feed is ONE implementation: the web store composes it (no second
//      copy of "is this row read"), and a count handed over by a host that
//      polls itself refreshes the list only when it disagrees.
//   3. The transport addresses the server under a sub-path (lesson #594/#595:
//      a root-relative URL drops FILEX_BASE_PATH) and reads with POST; it
//      names the language on screen (`lang=`), because the server says every
//      row in it (#191) - the rows are shown as the server said them.
//   4. The avatar's rows are ordered by ONE rule for the web page and the
//      explorer (lib/accountMenu).
//   5. The explorer draws them ONLY for a host that asked, claims its own "⋯"
//      ONLY then, and exposes the reveal the desktop hands an OS notification to
//      — and the web component forwards it (a custom element exposes its OWN
//      setup, so `el.openAppTarget` had never existed on the desktop).
import { readFileSync } from 'node:fs';
import path from 'node:path';
import { describe, expect, it, vi, beforeEach } from 'vitest';
import { flushPromises, mount } from '@vue/test-utils';
import { reactive } from 'vue';

import NotificationBell from '@brftech/filex-core/src/components/NotificationBell.vue';
import AccountMenu from '@brftech/filex-core/src/components/AccountMenu.vue';
import {
  createNotificationFeed,
  notificationsTransport,
  type NotificationFeed,
  type NotificationRowData,
} from '@brftech/filex-core/src/composables/useNotificationFeed';
import { accountMenuRows, explorerRowKey } from '@brftech/filex-core/src/lib/accountMenu';
import { en } from '@brftech/filex-core/src/locales/en';
import { tr } from '@brftech/filex-core/src/locales/tr';
import { unmountAll } from '../helpers/teardown';

const REPO = path.resolve(__dirname, '../../..');
const read = (rel: string) => readFileSync(path.join(REPO, rel), 'utf8');

/** A row as the list answers it: the facts and the sentence the SERVER said
 *  for them, in the bell's language (#191, backend notify say.go). */
function row(p: Partial<NotificationRowData>): NotificationRowData {
  return {
    id: 1,
    event: 'file.uploaded',
    severity: 'info',
    title: 'Yeni dosya',
    body: '',
    meta: {},
    created_at: '2026-09-27T04:22:00Z',
    read_at: null,
    ...p,
  };
}

let rows: NotificationRowData[] = [];
const calls: string[] = [];
function transport() {
  return {
    list: vi.fn(async () => {
      calls.push('list');
      return { items: rows.map((r) => ({ ...r })), total: rows.length };
    }),
    unreadCount: vi.fn(async () => {
      calls.push('count');
      return rows.filter((r) => !r.read_at).length;
    }),
    markRead: vi.fn(async (id: number) => {
      calls.push(`read:${id}`);
      rows = rows.map((r) => (r.id === id ? { ...r, read_at: 'now' } : r));
    }),
    markAllRead: vi.fn(async () => {
      calls.push('read-all');
    }),
  };
}

beforeEach(() => {
  calls.length = 0;
  rows = [
    row({
      id: 67,
      title: 'Yeni dosya: rapor.txt',
      body: '/Belgeler/rapor.txt',
      target: { kind: 'file', storage: 'qldemo', path: 'Belgeler/rapor.txt' },
      meta: { node: { name: 'rapor.txt', path: '/Belgeler/rapor.txt' } },
    }),
    row({
      id: 51,
      event: 'update_available',
      title: 'filex v0.39.1 yayınlandı',
      body: 'Bu sunucu v0.39.0 sürümünde çalışıyor.',
      meta: { version: 'v0.39.1', current: 'v0.39.0' },
    }),
  ];
});

describe('the core bell runs with no app around it', () => {
  function bell(props: Record<string, unknown> = {}) {
    const onRead = vi.fn();
    const api = transport();
    const feed = reactive(createNotificationFeed({ transport: api, onRead })) as NotificationFeed;
    const w = mount(NotificationBell, {
      props: { feed, locale: 'tr', ...props },
      attachTo: document.body,
    });
    return { w, feed, api, onRead };
  }

  it('draws the count on the button and says it in the reader’s language', async () => {
    const { w, feed } = bell();
    await feed.refreshFeed();
    await flushPromises();
    expect(w.find('[data-testid="unread-badge"]').text()).toBe('2');
    expect(w.find('[data-testid="notification-bell"]').attributes('aria-label')).toBe(
      `${tr['notifications.bell']} - 2 okunmamış`,
    );
  });

  it('a row click marks THAT row read, then asks the host to go — and tells the host a read happened', async () => {
    const { w, api, onRead } = bell();
    await w.find('[data-testid="notification-bell"]').trigger('click');
    await flushPromises();
    await flushPromises();
    const first = document.body.querySelector<HTMLButtonElement>('[data-testid="notification-row"]')!;
    // The server's words, as the list brought them.
    expect(first.textContent).toContain('Yeni dosya: rapor.txt');
    expect(first.textContent).toContain('/Belgeler/rapor.txt');
    first.click();
    await flushPromises();
    expect(api.markRead).toHaveBeenCalledWith(67);
    expect(onRead).toHaveBeenCalledTimes(1);
    const opened = w.emitted('open');
    expect(opened?.[0]?.[0]).toMatchObject({ id: 67 });
    // Marked before the host was asked to land — a list re-read on arrival
    // must not show the row unread.
    expect(calls.indexOf('read:67')).toBeGreaterThan(-1);
  });

  it('the administrators’ door is drawn only when the host names one', async () => {
    const plain = bell();
    await plain.w.find('[data-testid="notification-bell"]').trigger('click');
    await flushPromises();
    expect(document.body.querySelector('[data-testid="notification-manage"]')).toBeNull();
    unmountAll();

    const admin = bell({ manageHref: 'https://files.example.com/admin/notifications' });
    await admin.w.find('[data-testid="notification-bell"]').trigger('click');
    await flushPromises();
    const a = document.body.querySelector<HTMLAnchorElement>('[data-testid="notification-manage"]')!;
    expect(a.getAttribute('href')).toBe('https://files.example.com/admin/notifications');
    expect(a.textContent!.trim()).toBe(tr['notifications.manageAdmin']);
    a.click();
    await flushPromises();
    expect(admin.w.emitted('manage')).toHaveLength(1);
  });
});

describe('the desktop avatar: interface settings, no Sign out', () => {
  // Owner, 2026-09-27: the avatar holds the person's INTERFACE settings; the
  // desktop app's ⚙ holds the APP's, and signing an account out of the app is
  // an app setting (⚙ → Accounts). "User settings" and "Admin panel" stay.
  const app = read('desktop/ui/app.html');
  const block = app.slice(app.indexOf('          account: {'), app.indexOf('          // #47 — the palette'));

  it('the desktop hands the explorer no Sign out row', () => {
    expect(block, 'the account block moved').toContain('account: {');
    expect(block).not.toMatch(/signout|sign-out/);
    expect(block).not.toMatch(/\btail:/);
  });

  it('…and opens User settings IN the window, not in a browser', () => {
    expect(block).toMatch(/settings: true/);
    expect(block).not.toMatch(/user-settings/);
    expect(app).not.toMatch(/openUserSettings/);
    expect(read('desktop/src/preload-app.cts')).not.toMatch(/openUserSettings/);
    expect(read('desktop/src/main.ts')).not.toMatch(/account:openUserSettings/);
  });

  it('a menu with no tail invents no exit of its own', () => {
    const rows = accountMenuRows([{ key: 'admin', label: 'Admin panel' }], [{ key: 'tour', label: 'Tour' }]);
    expect(rows.map((r) => r.key)).toEqual(['admin', 'fe:tour']);
  });

  it('the explorer opens its own settings row, first, and leaves out what the dialog carries', () => {
    const fe = read('packages/core/src/FileExplorer.vue');
    const rows = fe.slice(fe.indexOf('const accountRows'), fe.indexOf('function onAccountSelect'));
    expect(rows).toMatch(/acc\.settings\s*\?\s*\[\{ key: SETTINGS_ROW, label: t\('userSettings\.open'\)/);
    expect(fe).toMatch(/const SETTINGS_DIALOG_ROWS = \['theme', 'density', 'timezone'\]/);
    expect(fe).toMatch(/<UserSettingsDialog\s+v-if="settingsOpen"/);
    const select = fe.slice(fe.indexOf('function onAccountSelect'), fe.indexOf('const SETTINGS_ROW'));
    expect(select).toMatch(/key === SETTINGS_ROW && props\.config\.account\?\.settings/);
  });
});

describe('drawn outside `.fe`, it holds its own shape', () => {
  // The panels are teleported to <body>, where the HOST page's rules reach
  // them. The desktop window styles every <button> with a fixed height, and
  // the two-line rows came out 34px tall, drawn over each other (measured in
  // the app, 2026-09-27 — desktop/scripts/notif-e2e.mjs "the rows do not
  // overlap"). happy-dom has no layout, so this pins the declaration; the
  // e2e measures the result.
  it('a row and every panel button say their own height', () => {
    const row = read('packages/core/src/components/NotificationRow.vue');
    expect(row).toMatch(/\.fx-nrow \{[^}]*height: auto;[^}]*\}/);
    expect(row).toMatch(/\.fx-nrow \{[^}]*justify-content: flex-start;[^}]*\}/);
    const bell = read('packages/core/src/components/NotificationBell.vue');
    for (const cls of ['fx-bell__markall', 'fx-bell__all']) expect(bell).toMatch(new RegExp(`\\.${cls} \\{[^}]*height: auto;`));
    const panel = read('packages/core/src/components/NotificationsPanel.vue');
    for (const cls of ['fx-nall__tool', 'fx-nall__page']) expect(panel).toMatch(new RegExp(`\\.${cls} \\{[^}]*height: auto;`));
    expect(read('packages/core/src/components/AccountMenu.vue')).toMatch(/\.fx-acctmenu__item \{[^}]*height: auto;/);
  });
});

describe('ONE feed, whoever polls', () => {
  it('a count handed over by the host refreshes the list only when it disagrees', async () => {
    const api = transport();
    const feed = createNotificationFeed({ transport: api });
    await feed.refreshFeed();
    const lists = api.list.mock.calls.length;
    await feed.acceptCount(2);
    expect(api.list.mock.calls.length, 'the same count re-read the list').toBe(lists);
    rows = [row({ id: 70 }), ...rows];
    await feed.acceptCount(3);
    expect(api.list.mock.calls.length).toBe(lists + 1);
    expect(feed.feed.value[0].id).toBe(70);
    expect(feed.unreadCount.value).toBe(3);
  });

  it('marking every row read tells the host too', async () => {
    const onRead = vi.fn();
    const feed = createNotificationFeed({ transport: transport(), onRead });
    await feed.refreshFeed();
    await feed.markAllRead();
    expect(onRead).toHaveBeenCalledTimes(1);
    expect(feed.unreadCount.value).toBe(0);
  });

  it('the web store is a composition of it, not a second copy', () => {
    const store = read('web/src/stores/notifications.ts');
    expect(store).toMatch(/createNotificationFeed/);
    // The feed's own verbs are not written again in the store.
    for (const verb of ['async function refreshFeed', 'async function markRead', 'async function fetchMine', 'async function syncUnread']) {
      expect(store, verb).not.toContain(verb);
    }
  });
});

describe('the explorer’s own transport', () => {
  it('addresses the server UNDER its base path, and reads with POST', async () => {
    const seen: Array<{ url: string; method: string }> = [];
    const jsonFetch = vi.fn(async (url: string, init?: RequestInit) => {
      seen.push({ url, method: init?.method ?? 'GET' });
      if (url.endsWith('/unread-count')) return { count: 4 } as never;
      if (url.includes('/api/notifications?')) return { items: [], total: 0 } as never;
      return undefined as never;
    });
    const t = notificationsTransport(jsonFetch, 'https://example.com/filex/');
    await t.list({ unread: true, limit: 10, offset: 0 });
    expect(await t.unreadCount()).toBe(4);
    await t.markRead(9);
    await t.markAllRead();
    expect(seen).toEqual([
      { url: 'https://example.com/filex/api/notifications?unread=true&limit=10&offset=0', method: 'GET' },
      { url: 'https://example.com/filex/api/notifications/unread-count', method: 'GET' },
      { url: 'https://example.com/filex/api/notifications/9/read', method: 'POST' },
      { url: 'https://example.com/filex/api/notifications/read-all', method: 'POST' },
    ]);
  });

  it('names NO language to the server: the rows are said in the account language', async () => {
    // #191, translated at the last stop: the server says every row in the
    // language of the reader's ACCOUNT (backend notify PersonLang). The
    // screen's language is the account's, so there is nothing to name - and
    // an embed drawn in its host's language must not turn the person's
    // notifications into that language either.
    const urls: string[] = [];
    const jsonFetch = vi.fn(async (url: string) => {
      urls.push(url);
      return { items: [], total: 0 } as never;
    });
    const t = notificationsTransport(jsonFetch, 'https://example.com/filex/');
    await t.list({ limit: 15, offset: 0 });
    await t.list({ unread: true, limit: 25, offset: 50 });
    expect(urls).toEqual([
      'https://example.com/filex/api/notifications?limit=15&offset=0',
      'https://example.com/filex/api/notifications?unread=true&limit=25&offset=50',
    ]);
    expect(urls.join(' ')).not.toContain('lang=');
    // The explorer names none, and asks again when the host's language (the
    // account's, written by the host) changes.
    const fe = read('packages/core/src/FileExplorer.vue');
    expect(fe).toMatch(/notificationsTransport\(api\.jsonFetch, connectionsBase\(props\.config\)\)/);
    expect(fe).toMatch(/watch\(locale, \(\) => \{\s*if \(!notifFeed\) return;\s*void notifFeed\.refreshFeed\(\);/);
    // Nor does the admin panel's own client.
    const web = read('web/src/api/notifications.ts');
    expect(web).not.toMatch(/\blang:/);
  });
});

describe('the avatar', () => {
  it('draws the person from props — no auth store — with the version at its foot', async () => {
    const w = mount(AccountMenu, {
      props: {
        actions: [{ key: 'user-settings', label: 'Kullanıcı ayarları ↗', icon: 'account' }],
        user: { display_name: 'İsmail', email: 'ismail@example.com' },
        version: '0.47.0',
        locale: 'tr',
        fallbackLabel: tr['explore.account'],
      },
      attachTo: document.body,
    });
    // Turkish capital of a dotted i — `toUpperCase()` would print I.
    expect(w.find('.fx-account__initial').text()).toBe('İ');
    await w.find('[data-testid="explore-account"]').trigger('click');
    await flushPromises();
    const menu = document.body.querySelector('[data-testid="explore-account-menu"]')!;
    expect(menu.textContent).toContain('İsmail');
    expect(menu.textContent).toContain('filex 0.47.0');
    document.body.querySelector<HTMLButtonElement>('[data-testid="explore-user-settings"]')!.click();
    await flushPromises();
    expect(w.emitted('select')?.[0]).toEqual(['user-settings']);
  });

  it('its rows are ordered by ONE rule: own doors, the explorer’s settings, then Sign out', () => {
    const rows = accountMenuRows(
      [{ key: 'user-settings', label: 'Settings' }],
      [
        { key: 'theme', label: 'Theme' },
        { key: 'x', label: '', divider: true },
        { key: 'timezone', label: 'Time zone' },
        { key: 'tour', label: 'Tour' },
      ],
      { omit: ['timezone'], tail: [{ key: 'signout', label: 'Sign out' }] },
    );
    expect(rows.map((r) => r.key)).toEqual(['user-settings', 'fe:theme', 'fe:tour', 'signout']);
    expect(rows.map((r) => !!r.separated)).toEqual([false, true, false, true]);
    expect(rows[1].icon).toBe('theme');
    expect(explorerRowKey('fe:theme')).toBe('theme');
    expect(explorerRowKey('signout')).toBeNull();
  });

  it('neither avatar repeats a control the explorer draws itself (view switcher, ⓘ, panel toggle)', () => {
    // Measured in the desktop window, 2026-09-27: Grid, Gallery and Details sat
    // in the avatar two inches from the view switcher and the ⓘ that do the
    // same job. The web page had dropped them since v4-hostmenu; both now take
    // the list from one place.
    const fe = read('packages/core/src/FileExplorer.vue');
    expect(fe).toMatch(/EXPLORER_DRAWN_ROWS\.filter\(/);
    expect(fe).toMatch(/omit: \[\.\.\.drawn,/);
    const explore = read('web/src/views/Explore.vue');
    expect(explore).toMatch(/\.\.\.EXPLORER_DRAWN_ROWS,/);
    const drawn = ['view-list', 'view-grid', 'view-gallery', 'inspector', 'nav'];
    const rows = accountMenuRows([], drawn.map((key) => ({ key, label: key })).concat({ key: 'theme', label: 'Theme' }), {
      omit: drawn,
    });
    expect(rows.map((r) => r.key)).toEqual(['fe:theme']);
  });

  it('the web page orders its menu with the same rule', () => {
    const explore = read('web/src/views/Explore.vue');
    expect(explore).toMatch(/accountMenuRows\(/);
    expect(explore).toMatch(/explorerRowKey\(/);
    expect(explore, 'a hand-written fe: prefix is a second rule').not.toMatch(/`fe:\$\{/);
  });
});

describe('the explorer draws them for a host that asks — and only then', () => {
  const fe = read('packages/core/src/FileExplorer.vue');
  const slot = fe.match(/<template #header-actions>([\s\S]*?)<\/template>/)?.[1] ?? '';

  it('bell, then avatar, then the host’s own slot — each behind its config', () => {
    const bellAt = slot.indexOf('<NotificationBell');
    const avatarAt = slot.indexOf('<AccountMenu');
    const hostAt = slot.indexOf('<slot name="header-actions">');
    expect(bellAt).toBeGreaterThan(-1);
    expect(avatarAt).toBeGreaterThan(bellAt);
    expect(hostAt).toBeGreaterThan(avatarAt);
    expect(slot).toMatch(/<NotificationBell\s+v-if="notifFeed"/);
    expect(slot).toMatch(/<AccountMenu\s+v-if="config\.account"/);
    expect(fe).toMatch(/const notifFeed: NotificationFeed \| null = props\.config\.notifications/);
  });

  it('claims its own "⋯" only when the host asked for the avatar', () => {
    const claim = fe.slice(fe.indexOf('function onOwnHeaderMenu'), fe.indexOf('const accountRows'));
    expect(claim).toMatch(/if \(!props\.config\.account\) return;/);
    expect(claim.indexOf('if (!props.config.account) return;')).toBeLessThan(claim.indexOf('detail.claimed = true'));
  });

  it('exposes the reveal, and the web component forwards it to the element', () => {
    expect(fe).toMatch(/defineExpose\(\{[\s\S]*revealNotification,[\s\S]*\}\);/);
    const wc = read('packages/webcomponent/src/index.ts');
    expect(wc).toMatch(/expose\(\{[\s\S]*revealNotification:[\s\S]*openAppTarget:|expose\(\{[\s\S]*openAppTarget:[\s\S]*revealNotification:/);
    expect(wc).toMatch(/ref: inner,/);
  });

  it('the strings it draws are in both catalogues, with the admin app’s words', () => {
    const web = {
      en: JSON.parse(read('web/src/locales/en.json')),
      tr: JSON.parse(read('web/src/locales/tr.json')),
    };
    const at = (o: Record<string, unknown>, k: string) =>
      k.split('.').reduce<unknown>((a, p) => (a as Record<string, unknown> | undefined)?.[p], o);
    // The push devices (#191, Web Push) are a section of core's own settings
    // dialog only: the admin app draws no such section, so it has no words
    // of its own for them to agree with (a copy there would be a dead key a
    // translator translates twice).
    const coreOnly = (k: string) => k.startsWith('notifications.prefs.push');
    for (const key of Object.keys(en).filter((k) => k.startsWith('notifications.') && !coreOnly(k))) {
      expect(tr[key], `tr ${key}`).toBeTruthy();
      expect(en[key], `en ${key}`).toBe(at(web.en, key));
      expect(tr[key], `tr ${key}`).toBe(at(web.tr, key));
    }
    // …and those are still said in both of core's languages.
    for (const key of Object.keys(en).filter(coreOnly)) expect(tr[key], `tr ${key}`).toBeTruthy();
  });
});

