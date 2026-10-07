// What the admin panel's search finds by itself (lib/adminSearch, task #168,
// docs/ADMIN-PANEL.md → Search): the menu's pages, their tabs and single
// settings - and only those the menu offers the reader.
//
// Pinned:
//   1. a delegated administrator does not find a page the menu leaves out for
//      them, nor that page's tabs or settings (the client half of "a restricted
//      administrator cannot see what they cannot open"; the server half is
//      backend handlers/panel_search_test.go);
//   2. a row answers to its label in the interface's language and in English,
//      and to its synonyms ("LDAP" → Identity providers, "2FA" → the setting);
//   3. every tab and setting names a route the router has and a key both
//      catalogues have, and the tabs are the pages' own (read from the views);
//   4. the server's rows and the file index's hits become rows that go to the
//      right page.
import { readFileSync } from 'node:fs';
import path from 'node:path';
import { describe, expect, it, vi } from 'vitest';

import type { MegaMenuEntry } from '@brftech/filex-core/src/lib/megaMenu';
import { panelSearchGroups, panelSearchRows, parsePanelQuery } from '@brftech/filex-core/src/lib/panelSearch';
import {
  ADMIN_SEARCH_SETTINGS,
  ADMIN_SEARCH_TABS,
  adminSearchItems,
  fileItem,
  remoteItem,
  type AdminSearchContext,
} from '@/lib/adminSearch';
import { buildAdminNav, pageOpenTo, type AdminNavContext } from '@/lib/adminNav';
import en from '@/locales/en.json';
import tr from '@/locales/tr.json';

const WEB = path.resolve(__dirname, '../..');

/** A key's text in one catalogue, '' when it has none. */
function textIn(catalogue: unknown): (key: string) => string {
  return (key) => {
    let node: unknown = catalogue;
    for (const part of key.split('.')) {
      if (!node || typeof node !== 'object') return '';
      node = (node as Record<string, unknown>)[part];
    }
    return typeof node === 'string' ? node : '';
  };
}

const META: Record<string, { requiresAdmin?: boolean; adminPerm?: string }> = {
  dashboard: { requiresAdmin: true, adminPerm: 'admin.monitor' },
  explore: {},
  sync: { requiresAdmin: true, adminPerm: 'admin.monitor' },
  usage: { requiresAdmin: true, adminPerm: 'admin.monitor' },
  queue: { requiresAdmin: true, adminPerm: 'admin.monitor' },
  users: { requiresAdmin: true, adminPerm: 'admin.users' },
  groups: { requiresAdmin: true, adminPerm: 'admin.users' },
  grants: { requiresAdmin: true, adminPerm: 'admin.grants' },
  shares: { requiresAdmin: true, adminPerm: 'admin.shares' },
  audit: { requiresAdmin: true, adminPerm: 'admin.audit' },
};

/** The search's context for a reader, the menu built the way useAdminNav builds it. */
function searchCtx(opts: { locale?: 'en' | 'tr'; isAdmin?: boolean; perms?: string[] } = {}): AdminSearchContext {
  const isAdmin = opts.isAdmin ?? true;
  const perms = new Set(opts.perms ?? []);
  const t = textIn(opts.locale === 'en' ? en : tr);
  const routes = new Map<string, { name: string; params?: Record<string, string> }>();
  const nav: AdminNavContext = {
    t,
    open: (to) => pageOpenTo(META[to.name] ?? { requiresAdmin: true }, { isAdmin, can: (p) => perms.has(p) }),
    href: (to) => {
      const href = `/admin/${to.name}${to.params ? `/${Object.values(to.params).join('/')}` : ''}`;
      routes.set(href, to);
      return href;
    },
    current: { name: 'dashboard', params: {} },
    tenants: false,
    tenantSelf: false,
    apps: isAdmin ? [{ key: 'convert/queue', plugin: 'convert', view: 'queue', label: 'Dönüştürmeler', icon: 'convert' }] : [],
  };
  const entries: MegaMenuEntry[] = buildAdminNav(nav);
  return { entries, routeFor: (href) => routes.get(href), t, en: textIn(en) };
}

const ids = (ctx: AdminSearchContext) => adminSearchItems(ctx).map((i) => i.id);
const firstFor = (ctx: AdminSearchContext, q: string) =>
  panelSearchRows(panelSearchGroups(adminSearchItems(ctx), parsePanelQuery(q)))[0];

describe('who finds what - the menu’s own rule', () => {
  it('an administrator finds every page, its tabs and the settings', () => {
    const all = ids(searchCtx());
    for (const id of ['page:dashboard', 'page:users', 'page:settings', 'page:auth-providers', 'tab:plugins:apps', 'tab:auth-providers:ldap', 'setting:require-2fa', 'setting:trash-retention']) {
      expect(all, id).toContain(id);
    }
    // An installed app's own screen, from the menu's Apps section.
    expect(adminSearchItems(searchCtx()).find((i) => i.id === 'page:app-convert/queue')?.kind).toBe('app');
  });

  it('a delegated administrator holding admin.users finds Files, Users and Groups, and nothing the menu leaves out', () => {
    const theirs = ids(searchCtx({ isAdmin: false, perms: ['admin.users'] }));
    expect(theirs.filter((id) => id.startsWith('page:')).sort()).toEqual(['page:explore', 'page:groups', 'page:users']);
    // Not the page, not its tabs, not its settings.
    expect(theirs).not.toContain('page:settings');
    expect(theirs.filter((id) => id.startsWith('tab:'))).toEqual([]);
    expect(theirs).toContain('setting:storage-quota'); // on the Users page, which they may open
    expect(theirs).not.toContain('setting:require-2fa'); // on Roles, which they may not
    expect(theirs).not.toContain('page:app-convert/queue');
  });

  it('what a delegated administrator types for a page they may not open finds nothing', () => {
    const ctx = searchCtx({ isAdmin: false, perms: ['admin.users'] });
    for (const q of ['ayarlar', 'settings', 'kimlik sağlayıcılar', 'ldap', 'eklentiler', '2fa']) {
      expect(firstFor(ctx, q), q).toBeUndefined();
    }
    expect(firstFor(ctx, 'kullanıcılar')?.id).toBe('page:users');
  });
});

describe('two languages and the synonyms', () => {
  it('a Turkish panel finds a page by its Turkish and its English name', () => {
    const ctx = searchCtx({ locale: 'tr' });
    expect(firstFor(ctx, 'kullanıcılar')?.id).toBe('page:users');
    expect(firstFor(ctx, 'kullanicilar')?.id).toBe('page:users');
    expect(firstFor(ctx, 'users')?.id).toBe('page:users');
    expect(firstFor(ctx, 'identity providers')?.id).toBe('page:auth-providers');
  });

  it('"Uygulamalar", "apps" and "uygulama" put the Apps page first', () => {
    const ctx = searchCtx({ locale: 'tr' });
    for (const q of ['Uygulamalar', 'apps', 'uygulama']) {
      expect(firstFor(ctx, q)?.id, q).toBe('tab:plugins:apps');
    }
    expect(firstFor(searchCtx({ locale: 'en' }), 'apps')?.id).toBe('tab:plugins:apps');
  });

  it('"LDAP" finds Identity providers', () => {
    expect(firstFor(searchCtx({ locale: 'tr' }), 'ldap')?.id).toBe('page:auth-providers');
    expect(firstFor(searchCtx({ locale: 'en' }), 'LDAP')?.id).toBe('page:auth-providers');
  });

  it('a single setting by its name or its synonym', () => {
    const ctx = searchCtx({ locale: 'tr' });
    expect(firstFor(ctx, '2fa')?.id).toBe('setting:require-2fa');
    expect(firstFor(ctx, 'çöp kutusu saklama')?.id).toBe('setting:trash-retention');
    expect(firstFor(ctx, 'trash retention')?.id).toBe('setting:trash-retention');
    expect(firstFor(ctx, 'mağaza')).toBeDefined();
  });

  it('a page row shows the menu’s short line, in the interface’s language', () => {
    const users = adminSearchItems(searchCtx({ locale: 'tr' })).find((i) => i.id === 'page:users')!;
    expect(users.label).toBe(tr.nav.users);
    expect(users.detail).toBe(tr.nav.hint.users);
    expect(users.terms).toContain(en.nav.users);
  });
});

describe('every tab and setting is real', () => {
  it('the tabs are the pages’ own', () => {
    const tabsOf = (file: string) => {
      const src = readFileSync(path.join(WEB, 'src/views', file), 'utf8');
      const m = /const TABS: Tab\[\] = \[([^\]]*)\]/.exec(src);
      expect(m, file).toBeTruthy();
      return [...m![1].matchAll(/'([^']+)'/g)].map((x) => x[1]);
    };
    expect(ADMIN_SEARCH_TABS.filter((t) => t.route === 'plugins').map((t) => t.tab)).toEqual(tabsOf('Plugins.vue'));
    expect(ADMIN_SEARCH_TABS.filter((t) => t.route === 'auth-providers').map((t) => t.tab)).toEqual(tabsOf('AuthProviders.vue'));
  });

  it('every label and synonym key is in both catalogues', () => {
    const keys = [
      ...ADMIN_SEARCH_TABS.map((t) => t.label),
      ...ADMIN_SEARCH_SETTINGS.flatMap((s) => (s.alias ? [s.label, s.alias] : [s.label])),
    ];
    for (const k of keys) {
      expect(textIn(en)(k), `en ${k}`).not.toBe('');
      expect(textIn(tr)(k), `tr ${k}`).not.toBe('');
    }
  });

  it('every route is the router’s', async () => {
    window.history.replaceState({}, '', '/admin/');
    vi.resetModules();
    const router = (await import('@/router')).default;
    for (const r of [...ADMIN_SEARCH_TABS.map((t) => t.route), ...ADMIN_SEARCH_SETTINGS.map((s) => s.route)]) {
      expect(router.hasRoute(r), r).toBe(true);
    }
    for (const r of ['users.edit', 'groups.edit', 'storages.edit', 'plugins.app', 'api-mcp', 'shares']) {
      expect(router.hasRoute(r), r).toBe(true);
    }
  }, 20_000);
});

describe('the server’s rows and the file index’s hits', () => {
  it('an app in the interface’s language, its other names as words it answers to', () => {
    const row = remoteItem({ kind: 'app', id: 'convert', app: 'convert', label: 'Convert', labels: { en: 'Convert', tr: 'Dönüştür' } }, 'tr');
    expect(row.label).toBe('Dönüştür');
    expect(row.terms).toContain('Convert');
    expect((row.target as { route: unknown }).route).toEqual({ name: 'plugins.app', params: { name: 'convert' } });
  });

  it('a person, a group, a storage and a key go to their own page', () => {
    const to = (kind: 'user' | 'group' | 'storage' | 'key' | 'share') =>
      (remoteItem({ kind, id: '7', label: 'x' }, 'en').target as { route: unknown }).route;
    expect(to('user')).toEqual({ name: 'users.edit', params: { id: '7' } });
    expect(to('group')).toEqual({ name: 'groups.edit', params: { id: '7' } });
    expect(to('storage')).toEqual({ name: 'storages.edit', params: { id: '7' } });
    expect(to('key')).toEqual({ name: 'api-mcp' });
    expect(to('share')).toEqual({ name: 'shares' });
  });

  it('a file opens where it is', () => {
    const row = fileItem({ id: 3, storage_id: 1, storage: 'depo', path: '/Raporlar/q3.xlsx', name: 'q3.xlsx', type: 'file' });
    expect(row).toMatchObject({ kind: 'file', label: 'q3.xlsx', detail: 'depo/Raporlar', isDir: false });
    expect(row.target).toEqual({ file: { storage: 'depo', path: 'Raporlar/q3.xlsx', dir: false } });
    expect(fileItem({ storage: 'depo', path: '/Arşiv', name: 'Arşiv', type: 'dir' })).toMatchObject({ detail: 'depo', isDir: true });
  });
});
