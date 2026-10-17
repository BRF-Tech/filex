// The admin panel's menu, as data (lib/adminNav, GitHub #82, 0.51.0).
//
// The owner's grouping (2026-10-15, task #258, renewing the three entries of
// 2026-10-03): the dashboard as a plain link, then Files & storage / People /
// Settings / Maintenance & records, each a panel of sections. What is pinned
// here:
//   1. every admin page the router has (bar sub-pages and redirects) is in
//      the menu EXACTLY once - a page added to the router without a place in
//      the menu fails here instead of being unreachable from the panel;
//   2. no page moved: the addresses are the ones 0.50 had;
//   3. the grouping is the owner's, section by section;
//   4. who sees what is the router guard's own rule, per page: an
//      administrator everything, a delegated administrator (admin.* without
//      the role) the pages naming a permission they hold, plus Files;
//      Tenants / My tenant on a multi-tenant install only; the pages about
//      the instance never to a tenant's administrator; the apps' rows;
//   5. what is marked as the page being looked at.
import { describe, expect, it, vi } from 'vitest';
import type { RouteRecordRaw } from 'vue-router';

import { pruneMegaMenu, type MegaMenuEntry } from '@brftech/filex-core/src/lib/megaMenu';
import {
  ADMIN_APP_ROUTE,
  ADMIN_NAV,
  adminNavPages,
  adminNavSectionOf,
  buildAdminNav,
  pageOpenTo,
  type AdminNavContext,
} from '@/lib/adminNav';
import TrashFull from '@/components/icons/TrashFull.vue';
import en from '@/locales/en.json';

async function realRouter() {
  // ⚠ The mount base is read once, at module load (router/index.ts): import it fresh.
  window.history.replaceState({}, '', '/admin/');
  vi.resetModules();
  return (await import('@/router')).default;
}

/** The admin panel's own pages: the children of the AdminLayout record. */
async function adminChildren(): Promise<RouteRecordRaw[]> {
  const router = await realRouter();
  const layout = router.options.routes.find((r) => r.meta?.requiresAdmin === true && Array.isArray(r.children));
  expect(layout, 'the AdminLayout record').toBeTruthy();
  return layout!.children!;
}

describe('every admin page has one place in the menu', () => {
  it('each page of the panel (not a sub-page, not a redirect) is in the menu exactly once', async () => {
    const pages = (await adminChildren())
      .filter((r) => typeof r.name === 'string' && !r.redirect && !r.meta?.parent && r.name !== ADMIN_APP_ROUTE)
      .map((r) => String(r.name));
    // The drive is not a panel page but it is the panel's way back to the files.
    const want = [...pages, 'explore'].sort();
    const have = adminNavPages().map((p) => p.route);
    const twice = have.filter((r, i) => have.indexOf(r) !== i);
    expect(twice, 'a page listed twice').toEqual([]);
    expect([...have].sort()).toEqual(want);
  }, 20_000);

  it('the apps’ screens have their section, and every menu route is a real route', async () => {
    const router = await realRouter();
    for (const p of adminNavPages()) expect(router.hasRoute(p.route), p.route).toBe(true);
    expect(router.hasRoute(ADMIN_APP_ROUTE)).toBe(true);
    expect(adminNavSectionOf(ADMIN_APP_ROUTE)?.section.id).toBe('apps');
  }, 20_000);

  it('no address moved: every page is where 0.50 had it', async () => {
    const router = await realRouter();
    const paths = Object.fromEntries(adminNavPages().map((p) => [p.route, router.resolve({ name: p.route }).path]));
    expect(paths).toEqual({
      dashboard: '/dashboard',
      explore: '/explore',
      'admin-files': '/files',
      shares: '/shares',
      trash: '/trash',
      tagged: '/tagged',
      duplicates: '/duplicates',
      search: '/search',
      storages: '/storages',
      connections: '/connections',
      sync: '/sync',
      replica: '/replica',
      usage: '/usage',
      users: '/users',
      groups: '/groups',
      roles: '/roles',
      grants: '/grants',
      tenants: '/tenants',
      'tenant-self': '/my-tenant',
      'auth-providers': '/auth-providers',
      'login-security': '/login-security',
      encryption: '/encryption',
      protection: '/protection',
      'api-mcp': '/api-mcp',
      'ai-assistant': '/ai-assistant',
      plugins: '/plugins',
      external: '/external',
      webhooks: '/webhooks',
      notifications: '/notifications',
      settings: '/settings',
      tenancy: '/tenancy',
      branding: '/branding',
      appearance: '/appearance',
      archives: '/archives',
      queue: '/queue',
      tools: '/tools',
      audit: '/audit',
      updates: '/updates',
      about: '/about',
    });
  }, 20_000);
});

describe('the owner’s grouping', () => {
  // The owner, 2026-10-15 (task #258): a fourth entry, Settings, holds every
  // page that changes how the instance behaves, in three columns - General /
  // Security & data / File processing & integrations. It renews the menu of
  // 2026-10-03, whose "People & security" and "System" mixed settings with
  // the things the instance has.
  it('four entries, their sections, and the pages in each', () => {
    const shape = ADMIN_NAV.map((e) => [e.id, e.sections.map((s) => [s.id, s.pages.map((p) => p.route)])]);
    expect(shape).toEqual([
      [
        'files',
        [
          ['files', ['explore', 'admin-files', 'shares', 'trash', 'tagged', 'duplicates', 'search']],
          ['apps', []],
          ['storage', ['storages', 'connections', 'sync', 'replica', 'usage']],
        ],
      ],
      ['people', [['people', ['users', 'groups', 'roles', 'grants', 'tenants']]]],
      [
        'settings',
        [
          ['general', ['settings', 'branding', 'appearance', 'tenancy', 'tenant-self']],
          ['security', ['auth-providers', 'login-security', 'encryption', 'protection']],
          ['integrations', ['archives', 'plugins', 'external', 'webhooks', 'notifications', 'api-mcp', 'ai-assistant']],
        ],
      ],
      ['maintenance', [['maintenance', ['queue', 'tools', 'audit', 'updates', 'about']]]],
    ]);
  });

  // Settings comes BEFORE Maintenance & records, not last - the order the
  // owner approved: Dashboard | Files & storage | People | Settings |
  // Maintenance & records.
  it('the entries read Files & storage, People, Settings, Maintenance & records', () => {
    const top = (en as { nav: { top: Record<string, string> } }).nav.top;
    expect(ADMIN_NAV.map((e) => top[e.label.split('.').pop()!])).toEqual([
      'Files & storage',
      'People',
      'Settings',
      'Maintenance & records',
    ]);
    // The Settings page is the General column's "General": a page called
    // Settings under a menu called Settings would name nothing.
    expect(adminNavPages().find((p) => p.route === 'settings')!.label).toBe('nav.general');
  });

  it('every label, heading and short line is in the catalogue', () => {
    const flat = (o: Record<string, unknown>, pre = ''): string[] =>
      Object.entries(o).flatMap(([k, v]) => (v && typeof v === 'object' ? flat(v as Record<string, unknown>, `${pre}${k}.`) : [`${pre}${k}`]));
    const keys = new Set(flat(en as Record<string, unknown>));
    const used = [
      ...adminNavPages().flatMap((p) => [p.label, p.hint]),
      ...ADMIN_NAV.flatMap((e) => [e.label, ...e.sections.map((s) => s.label)]),
    ];
    expect(used.filter((k) => !keys.has(k))).toEqual([]);
  });

  it('a sub-page is placed by its parent; the dashboard and the public pages by nothing', () => {
    expect(adminNavSectionOf('users')?.section.id).toBe('people');
    expect(adminNavSectionOf('protection')?.section.id).toBe('security');
    expect(adminNavSectionOf('protection')?.entry.id).toBe('settings');
    expect(adminNavSectionOf('audit')?.section.id).toBe('maintenance');
    expect(adminNavSectionOf('tenant-self')?.section.id).toBe('general');
    expect(adminNavSectionOf('api-mcp')?.section.id).toBe('integrations');
    expect(adminNavSectionOf('dashboard')).toBeNull();
    expect(adminNavSectionOf('login')).toBeNull();
  });
});

/* ── who sees what ─────────────────────────────────────────────────────── */

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
const metaOf = (route: string) => META[route] ?? { requiresAdmin: true };

function ctx(over: Partial<AdminNavContext> & { isAdmin?: boolean; perms?: string[] } = {}): AdminNavContext {
  const isAdmin = over.isAdmin ?? true;
  const perms = new Set(over.perms ?? []);
  return {
    t: (k) => k,
    open: (to) => pageOpenTo(metaOf(to.name), { isAdmin, can: (p) => perms.has(p) }),
    href: (to) => `/admin/${to.name}${to.params ? `/${Object.values(to.params).join('/')}` : ''}`,
    current: { name: 'dashboard', params: {} },
    tenants: false,
    tenantSelf: false,
    apps: [],
    ...over,
  };
}

/** What is drawn: entry → its item ids (the core prune applied). */
function drawn(entries: MegaMenuEntry[]): Record<string, string[]> {
  return Object.fromEntries(
    pruneMegaMenu(entries).map((e) => [e.id, e.sections ? e.sections.flatMap((s) => s.items.map((i) => i.id)) : ['(link)']]),
  );
}

/** The pages about the instance (`only: 'platform'`, task #258). */
const PLATFORM_PAGES = [
  'search', 'replica', 'usage', 'queue', 'updates', 'settings', 'appearance', 'tenancy',
  'auth-providers', 'login-security', 'protection', 'archives', 'plugins', 'external', 'webhooks',
  // The AI assistant (task #269): the instance's provider and key.
  'ai-assistant',
];

describe('who sees what - the router guard’s rule, per page', () => {
  it('an administrator of a single-tenant install: the dashboard and every page but the two tenant pages', () => {
    const d = drawn(buildAdminNav(ctx({ platform: true })));
    expect(Object.keys(d)).toEqual(['dashboard', 'files', 'people', 'settings', 'maintenance']);
    const all = Object.values(d).flat();
    expect(all).toContain('encryption');
    expect(all).toContain('tenancy');
    expect(all).not.toContain('tenants');
    expect(all).not.toContain('tenant-self');
    expect(all).toHaveLength(1 + 38 - 2); // dashboard link + 38 pages (the AI assistant's since #269) - the two tenant pages
  });

  it('Multi-tenant mode to whoever may configure the instance (#167), under Settings → General', () => {
    const d = drawn(buildAdminNav(ctx({ platform: true })));
    expect(d.settings).toContain('tenancy');
    expect(adminNavSectionOf('tenancy')?.section.id).toBe('general');
    // A delegated administrator is not offered it: the route names no admin.* permission.
    const theirs = Object.values(drawn(buildAdminNav(ctx({ isAdmin: false, perms: ['admin.users'], platform: true })))).flat();
    expect(theirs).not.toContain('tenancy');
  });

  it('a delegated administrator holding admin.users: Files, Users and Groups - nothing else', () => {
    const d = drawn(buildAdminNav(ctx({ isAdmin: false, perms: ['admin.users'] })));
    expect(d).toEqual({ files: ['explore'], people: ['users', 'groups'] });
  });

  it('a delegated administrator holding admin.monitor: the dashboard, Sync, Usage and the Queue', () => {
    const d = drawn(buildAdminNav(ctx({ isAdmin: false, perms: ['admin.monitor'], platform: true })));
    expect(d).toEqual({ dashboard: ['(link)'], files: ['explore', 'sync', 'usage'], maintenance: ['queue'] });
  });

  it('a delegated administrator holding admin.audit: the Audit log alone under Maintenance & records', () => {
    const d = drawn(buildAdminNav(ctx({ isAdmin: false, perms: ['admin.audit'], platform: true })));
    expect(d.maintenance).toEqual(['audit']);
    expect(d.people).toBeUndefined();
    expect(d.settings).toBeUndefined();
  });

  it('Tenants to the platform operator, My tenant to a tenant’s administrator, never both', () => {
    const op = Object.values(drawn(buildAdminNav(ctx({ tenants: true, platform: true })))).flat();
    expect(op).toContain('tenants');
    expect(op).not.toContain('tenant-self');
    const own = Object.values(drawn(buildAdminNav(ctx({ tenantSelf: true, platform: false })))).flat();
    expect(own).toContain('tenant-self');
    expect(own).not.toContain('tenants');
  });

  // Task #258: a tenant's administrator is not offered a page about the
  // instance - each one's API answers them 403 on read and on write
  // (backend handlers/admin_menu_platform_pages_test.go holds the server to
  // the same list). The summary defaults on Notifications are their
  // tenant's, so that page stays.
  it('the pages a tenant’s administrator is not offered', () => {
    expect(adminNavPages().filter((p) => p.only === 'platform').map((p) => p.route).sort()).toEqual([...PLATFORM_PAGES].sort());
    const d = drawn(buildAdminNav(ctx({ tenantSelf: true, platform: false })));
    const theirs = Object.values(d).flat();
    for (const page of PLATFORM_PAGES) expect(theirs, page).not.toContain(page);
    expect(d).toEqual({
      dashboard: ['(link)'],
      files: ['explore', 'admin-files', 'shares', 'trash', 'tagged', 'duplicates', 'storages', 'connections', 'sync'],
      people: ['users', 'groups', 'roles', 'grants'],
      settings: ['branding', 'tenant-self', 'encryption', 'notifications', 'api-mcp'],
      maintenance: ['tools', 'audit', 'about'],
    });
    expect(Object.keys(d)).toEqual(['dashboard', 'files', 'people', 'settings', 'maintenance']);
  });

  it('the platform’s own administrator of a multi-tenant install is offered them all', () => {
    const theirs = Object.values(drawn(buildAdminNav(ctx({ tenants: true, platform: true })))).flat();
    for (const page of PLATFORM_PAGES) expect(theirs, page).toContain(page);
  });

  it('an installed app’s screen is a row of the Apps section, by its icon NAME, for an administrator', () => {
    const app = { key: 'sign/envelopes', plugin: 'sign', view: 'envelopes', label: 'Signatures', icon: 'sign' };
    const files = buildAdminNav(ctx({ apps: [app] })).find((e) => e.id === 'files')!;
    const apps = files.sections!.find((s) => s.id === 'apps')!;
    expect(apps.items).toEqual([
      { id: 'app-sign/envelopes', label: 'Signatures', href: '/admin/admin-app/sign/envelopes', active: false, iconName: 'sign' },
    ]);
    // A delegated administrator is not offered it: the route names no admin.* permission.
    const theirs = buildAdminNav(ctx({ isAdmin: false, perms: ['admin.users'], apps: [app] })).find((e) => e.id === 'files')!;
    expect(theirs.sections!.find((s) => s.id === 'apps')!.items).toEqual([]);
  });
});

describe('the page being looked at', () => {
  const items = (entries: MegaMenuEntry[]) => entries.flatMap((e) => (e.sections ?? []).flatMap((s) => s.items));

  it('marks the page itself, and the parent of a sub-page', () => {
    const on = items(buildAdminNav(ctx({ current: { name: 'users.edit', params: { id: '4' }, parent: 'users' } })));
    expect(on.filter((i) => i.active).map((i) => i.id)).toEqual(['users']);
  });

  it('marks only the app being looked at, though every app row shares one route', () => {
    const apps = [
      { key: 'sign/envelopes', plugin: 'sign', view: 'envelopes', label: 'Signatures', icon: 'sign' },
      { key: 'convert/queue', plugin: 'convert', view: 'queue', label: 'Conversions', icon: 'convert' },
    ];
    const on = items(buildAdminNav(ctx({ apps, current: { name: ADMIN_APP_ROUTE, params: { plugin: 'convert', view: 'queue' } } })));
    expect(on.filter((i) => i.active).map((i) => i.id)).toEqual(['app-convert/queue']);
  });

  it('the dashboard link is marked on the dashboard', () => {
    const [dash] = buildAdminNav(ctx());
    expect(dash).toMatchObject({ id: 'dashboard', href: '/admin/dashboard', active: true });
  });

  it('the trash row wears the full bin when the trash holds something', () => {
    const trash = items(buildAdminNav(ctx({ trashFull: true }))).find((i) => i.id === 'trash')!;
    expect(trash.icon).toBe(TrashFull);
    const empty = items(buildAdminNav(ctx())).find((i) => i.id === 'trash')!;
    expect(empty.icon).not.toBe(TrashFull);
  });
});
