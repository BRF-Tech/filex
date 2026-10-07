// The admin panel's "Apps" section — a GENERIC door, not a signatures page.
//
// The owner's words, 2026-09-20: "app altındaki signatures menüsünde açtığım
// ongoing sign işlemlerini de görebiliyor ve durumlarına bakabiliyor olmalıyım
// (admin isem o da adminde de ayrı bir menüde signature tablosunu görebiliyor
// olmalıyım bence)". The panel answers the second half the only way that does
// not have to be written again for the next plugin: every installed, running
// plugin that ships a `home` view gets a menu row
// (`composables/usePluginHomeApps`), and the row opens that view inside the
// panel (`views/AppHome.vue` → the package's `PluginPageView`, embedded
// frame). Nothing in the panel names `sign`; the signing app's request table
// is simply the first thing to walk through the door.
//
// What is pinned here, in order:
//   1. no plugin with a `home` view → NO section at all (a heading over
//      nothing is a promise the deployment cannot keep);
//   2. one row per `home` view, in the reader's language, and `inspector` /
//      `modal` views stay out of it;
//   3. a non-administrator gets no section, however many apps are installed;
//   4. the row's address opens the app's screen INSIDE the panel, drawn by
//      the package's surface components rather than by a page of our own.
import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest';
import { flushPromises, mount } from '@vue/test-utils';
import { createPinia, setActivePinia } from 'pinia';
import { createI18n } from 'vue-i18n';
import { createMemoryHistory, createRouter, type Router } from 'vue-router';

import { invalidatePluginActions } from '@brftech/filex-core';

import AdminNav from '@/components/AdminNav.vue';
import AppHome from '@/views/AppHome.vue';
import { useAuthStore } from '@/stores/auth';
import { useCapabilitiesStore } from '@/stores/capabilities';
import en from '@/locales/en.json';
import tr from '@/locales/tr.json';

// The menu asks the trash how full it is on mount; irrelevant here and a
// real request in a test environment that has no server.
vi.mock('@/api/trash', () => ({
  trashApi: { list: vi.fn(async () => ({ total: 0, entries: [] })) },
}));

/** `GET /api/files/plugins/actions` — one `home` view among other placements. */
const ACTIONS = {
  actions: [
    {
      plugin: 'sign',
      id: 'sign',
      label: { en: 'Sign…', tr: 'İmzala…' },
      applies: { kind: 'file' },
      view: 'sign-self',
      view_placement: 'page',
    },
  ],
  views: [
    {
      plugin: 'sign',
      id: 'status',
      placement: 'inspector',
      label: { en: 'Signatures', tr: 'İmzalar' },
      icon: 'sign',
    },
    {
      plugin: 'sign',
      id: 'envelopes',
      placement: 'home',
      label: { en: 'Signatures', tr: 'İmzalar' },
      icon: 'sign',
    },
    // A SECOND app, on purpose: the section is the door for every plugin, and
    // two rows are what prove the panel is not a signatures page in disguise.
    {
      plugin: 'convert',
      id: 'queue',
      placement: 'home',
      label: { en: 'Conversions', tr: 'Dönüştürmeler' },
      icon: 'convert',
    },
  ],
};

/** `GET /api/files/plugins/views/sign/envelopes` — the app's own screen. */
const SURFACE = {
  surface: {
    title: { en: 'Signatures', tr: 'İmzalar' },
    size: 'xl',
    state: { view: 'envelopes' },
    nodes: [
      {
        type: 'text',
        props: { heading: true, text: { en: 'I asked for these', tr: 'Benim istediklerim' } },
      },
      {
        type: 'list',
        props: {
          columns: [
            { key: 'doc', label: { en: 'Document', tr: 'Belge' } },
            { key: 'state', label: { en: 'State', tr: 'Durum' } },
          ],
          rows: [
            {
              id: 'depo://teklif.pdf',
              cells: {
                doc: { en: 'teklif.pdf', tr: 'teklif.pdf' },
                state: { en: 'Sent', tr: 'Gönderildi' },
              },
            },
          ],
        },
      },
    ],
  },
};

const asked: string[] = [];

function body(data: unknown) {
  return {
    ok: true,
    status: 200,
    text: async () => JSON.stringify(data),
  } as unknown as Response;
}

function serve(actions: unknown = ACTIONS) {
  return vi.fn(async (input: RequestInfo | URL) => {
    const url = String(input);
    asked.push(url);
    if (url.includes('/api/files/plugins/actions')) return body(actions);
    if (url.includes('/api/files/plugins/views/sign/envelopes')) return body(SURFACE);
    throw new Error(`unexpected fetch ${url}`);
  });
}

function i18n(locale: 'en' | 'tr' = 'en') {
  return createI18n({ legacy: false, locale, fallbackLocale: 'en', messages: { en, tr } });
}

/** The panel's routes, only as far as this test walks them. */
async function router(at = '/dashboard'): Promise<Router> {
  const Blank = { template: '<div />' };
  const r = createRouter({
    history: createMemoryHistory('/admin/'),
    routes: [
      { path: '/dashboard', name: 'dashboard', component: Blank },
      { path: '/explore', name: 'explore', component: Blank },
      { path: '/admin-files', name: 'admin-files', component: Blank },
      { path: '/connections', name: 'connections', component: Blank },
      { path: '/storages', name: 'storages', component: Blank },
      { path: '/sync', name: 'sync', component: Blank },
      { path: '/shares', name: 'shares', component: Blank },
      { path: '/trash', name: 'trash', component: Blank },
      { path: '/search', name: 'search', component: Blank },
      { path: '/duplicates', name: 'duplicates', component: Blank },
      { path: '/tagged', name: 'tagged', component: Blank },
      { path: '/users', name: 'users', component: Blank },
      { path: '/groups', name: 'groups', component: Blank },
      { path: '/grants', name: 'grants', component: Blank },
      { path: '/roles', name: 'roles', component: Blank },
      { path: '/encryption', name: 'encryption', component: Blank },
      { path: '/auth-providers', name: 'auth-providers', component: Blank },
      { path: '/tenants', name: 'tenants', component: Blank },
      { path: '/my-tenant', name: 'tenant-self', component: Blank },
      { path: '/api-mcp', name: 'api-mcp', component: Blank },
      { path: '/login-security', name: 'login-security', component: Blank },
      { path: '/settings', name: 'settings', component: Blank },
      { path: '/tenancy', name: 'tenancy', component: Blank },
      { path: '/branding', name: 'branding', component: Blank },
      // tema:v1 — the Appearance screen. ⚠ This stub must carry EVERY
      // route the real menu links to: vue-router throws on an unknown
      // name while resolving a <RouterLink>, so one missing entry fails
      // every test in the file for a reason that has nothing to do with
      // what they measure.
      { path: '/appearance', name: 'appearance', component: Blank },
      { path: '/archives', name: 'archives', component: Blank },
      { path: '/protection', name: 'protection', component: Blank },
      { path: '/external', name: 'external', component: Blank },
      { path: '/replica', name: 'replica', component: Blank },
      { path: '/queue', name: 'queue', component: Blank },
      { path: '/tools', name: 'tools', component: Blank },
      { path: '/notifications', name: 'notifications', component: Blank },
      { path: '/webhooks', name: 'webhooks', component: Blank },
      { path: '/plugins', name: 'plugins', component: Blank },
      { path: '/usage', name: 'usage', component: Blank },
      { path: '/audit', name: 'audit', component: Blank },
      { path: '/updates', name: 'updates', component: Blank },
      { path: '/about', name: 'about', component: Blank },
      // The one under test — the same shape as the real record.
      { path: '/apps/:plugin/home/:view', name: 'admin-app', component: Blank },
    ],
  });
  await r.push(at);
  await r.isReady();
  return r;
}

/** An account, and a host that can run plugins. */
function signIn(role: 'admin' | 'user', plugins = true) {
  const auth = useAuthStore();
  auth.user = { id: 1, email: 'admin@local', username: 'admin', role } as never;
  const caps = useCapabilitiesStore();
  caps.data = { ...caps.data, app_plugins: { enabled: plugins } };
}

/* The admin menu (0.51, GitHub #82: core's MegaMenu in lib/adminNav's
   grouping), drawn as the phone drawer's list: every section open, so a row
   is in the document without a click. */
async function menu(locale: 'en' | 'tr' = 'en') {
  const w = mount(AdminNav, {
    props: { mode: 'list' },
    global: { plugins: [await router(), i18n(locale)] },
  });
  await flushPromises();
  return w;
}

beforeEach(() => {
  setActivePinia(createPinia());
  asked.length = 0;
  // ⚠ The actions list is a MODULE-LEVEL cache shared by every explorer on
  // the page (usePluginActions). Without this the second test in the file
  // reads the first test's answer and passes for the wrong reason.
  invalidatePluginActions();
  vi.stubGlobal('fetch', serve());
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('admin menu — Apps', () => {
  it('is absent when no installed plugin ships a home view', async () => {
    vi.stubGlobal('fetch', serve({ actions: [], views: ACTIONS.views.filter((v) => v.placement !== 'home') }));
    signIn('admin');
    const w = await menu();
    expect(w.find('[data-testid="nav-group-apps"]').exists()).toBe(false);
    // …and no heading is left floating over the absent rows.
    expect(w.text()).not.toContain('Apps');
  });

  it('makes no plugin call at all while the host cannot run plugins', async () => {
    signIn('admin', false);
    const w = await menu();
    expect(asked.filter((u) => u.includes('/plugins/'))).toEqual([]);
    expect(w.find('[data-testid="nav-group-apps"]').exists()).toBe(false);
  });

  it('lists one row per home view — inspector and modal views stay out', async () => {
    signIn('admin');
    const w = await menu();
    const group = w.find('[data-testid="nav-group-apps"]');
    expect(group.exists()).toBe(true);
    expect(group.find('p').text()).toBe('Apps');
    const rows = group.findAll('a');
    expect(rows.map((r) => r.text())).toEqual(['Signatures', 'Conversions']);
    // The address carries the plugin AND the view, so two apps cannot collide.
    expect(rows[0].attributes('href')).toBe('/admin/apps/sign/home/envelopes');
    expect(rows[1].attributes('href')).toBe('/admin/apps/convert/home/queue');
    // The manifest's icon name is drawn from the core icon library, never
    // from markup a plugin supplied.
    expect(rows[0].find('.fx-mega__glyph svg').exists()).toBe(true);
  });

  // ⚠ Every app row shares ONE route name and differs only in its params, so
  // a menu that compares names alone lights up every app in the list the
  // moment any one of them is open.
  it('marks only the app you are standing in', async () => {
    signIn('admin');
    const w = mount(AdminNav, {
      props: { mode: 'list' },
      global: { plugins: [await router('/apps/sign/home/envelopes'), i18n()] },
    });
    await flushPromises();
    const rows = w.find('[data-testid="nav-group-apps"]').findAll('a');
    expect(rows[0].attributes('aria-current')).toBe('page');
    expect(rows[1].attributes('aria-current')).toBeUndefined();
  });

  it("reads the view's label in the reader's language", async () => {
    signIn('admin');
    const w = await menu('tr');
    const group = w.find('[data-testid="nav-group-apps"]');
    expect(group.find('p').text()).toBe('Uygulamalar');
    expect(group.findAll('a').map((r) => r.text())).toEqual(['İmzalar', 'Dönüştürmeler']);
  });

  it('shows nothing to an account that is not an administrator', async () => {
    signIn('user');
    const w = await menu();
    expect(w.find('[data-testid="nav-group-apps"]').exists()).toBe(false);
  });
});

describe('admin panel — an app plugin\'s screen', () => {
  async function open(locale: 'en' | 'tr' = 'en') {
    const r = await router('/apps/sign/home/envelopes');
    const w = mount(AppHome, { global: { plugins: [r, i18n(locale)] } });
    await flushPromises();
    await flushPromises();
    return w;
  }

  it('draws the app\'s own surface inside the panel, with the panel\'s heading', async () => {
    signIn('admin');
    const w = await open();
    // The heading is the menu row's label, so the page is named the same
    // way in both places.
    expect(w.find('[data-testid="app-home-title"]').text()).toBe('Signatures');
    // The surface itself is the package's, asked for on the right view…
    expect(asked.some((u) => u.includes('/api/files/plugins/views/sign/envelopes'))).toBe(true);
    expect(w.find('[data-testid="plugin-page"]').exists()).toBe(true);
    // …and it is the app's real content, not a placeholder.
    expect(w.text()).toContain('I asked for these');
    expect(w.text()).toContain('teklif.pdf');
  });

  it('is EMBEDDED chrome: no tab title bar and no "back to the files"', async () => {
    signIn('admin');
    const w = await open();
    const frame = w.find('[data-testid="plugin-page"]');
    expect(frame.classes()).toContain('fe-apppage--embedded');
    // ⚠ The tab frame's own bar would be a second heading right under the
    // panel's, and its "back" button points at the explorer — which is not
    // where an administrator on this page came from.
    expect(w.find('[data-testid="plugin-page-title"]').exists()).toBe(false);
    expect(w.find('[data-testid="plugin-page-back"]').exists()).toBe(false);
  });

  it('reads the surface in the reader\'s language', async () => {
    signIn('admin');
    const w = await open('tr');
    expect(w.text()).toContain('Benim istediklerim');
    expect(w.text()).toContain('Gönderildi');
  });
});

// Tenants (docs/TENANT-ADMIN.md) is the platform operator's page on a
// multi-tenant install: a tenant's administrator would read 403 there, and a
// single-tenant install has one tenant, its own.
describe('admin menu - Tenants', () => {
  function caps(realm: boolean, callerAdmin: boolean) {
    const c = useCapabilitiesStore();
    c.data = { ...c.data, realm: realm ? { enabled: true, locked_realm: null } : undefined, caller_admin: callerAdmin };
  }

  it('is absent on a single-tenant install', async () => {
    signIn('admin');
    caps(false, true);
    const w = await menu();
    expect(w.find('[data-testid="nav-tenants"]').exists()).toBe(false);
  });

  it('is absent for an administrator of a tenant', async () => {
    signIn('admin');
    caps(true, false);
    const w = await menu();
    expect(w.find('[data-testid="nav-tenants"]').exists()).toBe(false);
  });

  it('is there for the platform operator of a multi-tenant install', async () => {
    signIn('admin');
    caps(true, true);
    const w = await menu();
    const row = w.get('[data-testid="nav-tenants"]');
    expect(row.text()).toBe(en.nav.tenants);
    expect(row.attributes('href')).toBe('/admin/tenants');
  });

  // #167: the server's `multi_tenant` is the one answer (composables/useTenancy).
  // Off, neither tenant page is offered, whatever else the answer carries.
  it('follows the server’s multi_tenant: off hides both tenant pages', async () => {
    signIn('admin');
    const c = useCapabilitiesStore();
    c.data = { ...c.data, multi_tenant: false, realm: { enabled: true, locked_realm: null }, caller_admin: true };
    let w = await menu();
    expect(w.find('[data-testid="nav-tenants"]').exists()).toBe(false);
    expect(w.find('[data-testid="nav-tenant-self"]').exists()).toBe(false);
    w.unmount();
    c.data = { ...c.data, multi_tenant: false, caller_admin: false };
    w = await menu();
    expect(w.find('[data-testid="nav-tenant-self"]').exists()).toBe(false);
    w.unmount();
    c.data = { ...c.data, multi_tenant: true, realm: undefined, caller_admin: true };
    w = await menu();
    expect(w.find('[data-testid="nav-tenants"]').exists()).toBe(true);
  });
});

// #167: Admin → Multi-tenant mode is the platform operator's switch - on a
// single-tenant install too, where it is the way the mode is turned on - and
// never a tenant administrator's.
describe('admin menu - Multi-tenant mode', () => {
  function caps(multi: boolean, callerAdmin: boolean) {
    const c = useCapabilitiesStore();
    c.data = { ...c.data, multi_tenant: multi, caller_admin: callerAdmin };
  }

  it('is under System for the administrator of a single-tenant install', async () => {
    signIn('admin');
    caps(false, true);
    const w = await menu();
    const row = w.get('[data-testid="nav-tenancy"]');
    expect(row.text()).toBe(en.nav.tenancy);
    expect(row.attributes('href')).toBe('/admin/tenancy');
  });

  it('is there for the platform operator of a multi-tenant install', async () => {
    signIn('admin');
    caps(true, true);
    const w = await menu();
    expect(w.find('[data-testid="nav-tenancy"]').exists()).toBe(true);
  });

  it('is absent for a tenant’s administrator', async () => {
    signIn('admin');
    caps(true, false);
    const w = await menu();
    expect(w.find('[data-testid="nav-tenancy"]').exists()).toBe(false);
  });
});

describe('admin menu — Encryption', () => {
  it('is among the security pages, for an administrator (a tenant’s administrator included)', async () => {
    signIn('admin');
    const w = await menu();
    const link = w.find('[data-testid="nav-encryption"]');
    expect(link.exists()).toBe(true);
    expect(link.text()).toBe('Encryption');
    expect(link.attributes('href')).toBe('/admin/encryption');
    expect(w.find('[data-testid="nav-group-security"]').find('[data-testid="nav-encryption"]').exists()).toBe(true);
  });

  it('reads in the reader’s language', async () => {
    signIn('admin');
    const w = await menu('tr');
    expect(w.find('[data-testid="nav-encryption"]').text()).toBe('Şifreleme');
  });
});
