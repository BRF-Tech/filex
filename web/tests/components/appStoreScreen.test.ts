// #162 - the store screen (views/AppStoreScreen.vue): filex's own page over
// the API, never a frame of the store.
//
// What has to stay true:
//   · an account the screen is not shown to is told so, and nothing else is
//     asked (no catalog read);
//   · the catalog is filex's answer (`/app-store/catalog`), drawn in the
//     explorer's table, its icons through filex's own address, never the
//     store's (the Content-Security-Policy does not change);
//   · a request carries the store, the app and the person's words, lands on
//     the person's requests, and an app already asked for or installed offers
//     no second request;
//   · a store that cannot be reached is said, and the last catalog filex
//     checked is marked as such;
//   · what a row is here (installed, asked for, nothing) is the SERVER's
//     `state` - the page works nothing out of the requests (#215);
//   · the store's storage plugins are on a tab of their own, with the
//     server's sentences, and one without a build for this server is offered
//     no request (#215).
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { flushPromises, mount } from '@vue/test-utils';
import { createPinia, setActivePinia } from 'pinia';
import { createI18n } from 'vue-i18n';

import en from '@/locales/en.json';
import tr from '@/locales/tr.json';
import { closeRowMenus, menuEntries, openRowMenu, pickMenuItem } from '../helpers/rowMenu';

const STORE = 'https://apps.filex.sh';
let status = { visible: true, stores: [STORE] };
let catalog: Record<string, unknown> = {};
let mine: Record<string, unknown>[] = [];
const gets: string[] = [];
const posted: { url: string; body: unknown }[] = [];

function app(name: string, extra: Record<string, unknown> = {}) {
  return {
    name,
    kind: 'app',
    label: { en: `App ${name}`, tr: `Uygulama ${name}` },
    summary: { en: `What ${name} does`, tr: `${name} ne yapar` },
    publisher: 'Acme',
    publisher_verified: true,
    categories: [],
    repo: `Owner/${name}`,
    version: '1.0.0',
    filex_range: '>=0.52.0',
    permissions: ['files:read'],
    permission_rows: [{ id: 'files:read', label: 'Read your files' }],
    state: 'none',
    ...extra,
  };
}

/** A storage plugin of the catalog, as the server answers it (#215). */
function storagePlugin(name: string, forHere = true, extra: Record<string, unknown> = {}) {
  return {
    ...app(name),
    kind: 'storage',
    permissions: [],
    permission_rows: [],
    platforms: ['linux/amd64', 'linux/arm64'],
    storage: {
      platform: 'linux/amd64',
      for_here: forHere,
      summary: forHere ? '9 checks passed on linux/amd64' : 'No build for this server (linux/riscv64)',
      capabilities: [{ id: 'write', label: 'writing' }, { id: 'range', label: 'ranged reads' }],
    },
    ...extra,
  };
}

vi.mock('@/api/client', () => ({
  extractError: (_e: unknown, f: string) => f,
  api: {
    get: vi.fn(async (url: string) => {
      gets.push(url);
      if (url === '/app-store') return { data: status };
      if (url === '/app-store/catalog') return { data: catalog };
      if (url === '/app-store/requests') return { data: { requests: mine } };
      return { data: {} };
    }),
    post: vi.fn(async (url: string, body: unknown) => {
      posted.push({ url, body });
      return { data: { request: { id: 3, name: 'pdfx', status: 'pending', source: { store: STORE, store_app: 'pdfx' } }, created: true } };
    }),
    patch: vi.fn(),
    put: vi.fn(),
    delete: vi.fn(),
  },
}));

const routerPush = vi.fn();
vi.mock('vue-router', () => ({ useRouter: () => ({ push: routerPush }) }));

const toasts: { kind: string; msg: string }[] = [];
vi.mock('@/stores/toast', () => ({
  useToastStore: () => ({
    success: (msg: string) => toasts.push({ kind: 'success', msg }),
    info: (msg: string) => toasts.push({ kind: 'info', msg }),
    warn: (msg: string) => toasts.push({ kind: 'warn', msg }),
    error: (msg: string) => toasts.push({ kind: 'error', msg }),
  }),
}));

import AppStoreScreen from '@/views/AppStoreScreen.vue';

if (typeof HTMLDialogElement !== 'undefined' && !HTMLDialogElement.prototype.showModal) {
  HTMLDialogElement.prototype.showModal = function () { this.setAttribute('open', ''); };
  HTMLDialogElement.prototype.close = function () { this.removeAttribute('open'); };
}

function mountPage(locale = 'en') {
  const i18n = createI18n({ legacy: false, locale, fallbackLocale: 'en', messages: { en, tr } });
  return mount(AppStoreScreen, { global: { plugins: [i18n] }, attachTo: document.body });
}

const lastOf = (id: string): HTMLElement | null => {
  const all = document.querySelectorAll<HTMLElement>(`[data-testid="${id}"]`);
  return all.length ? all[all.length - 1] : null;
};

describe('the store screen', () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    closeRowMenus();
    status = { visible: true, stores: [STORE] };
    catalog = {
      store: STORE,
      serial: 7,
      fetched_at: '2026-10-06T10:00:00Z',
      stale: false,
      apps: [
        app('pdfx', { icon: 'a'.repeat(64) + '.png' }),
        app('sign', { installed_version: '1.0.0', state: 'installed' }),
        app('drawio', { state: 'pending' }),
      ],
    };
    mine = [{ id: 1, name: 'drawio', status: 'pending', version: '1.0.0', source: { store: STORE, store_app: 'drawio' }, created_at: '2026-10-05T10:00:00Z' }];
    gets.length = 0;
    posted.length = 0;
    toasts.length = 0;
  });

  it('says so to an account it is not shown to, and reads no catalog', async () => {
    status = { visible: false, stores: [] };
    const w = mountPage('tr');
    await flushPromises();
    expect(w.find('[data-testid="store-screen-hidden"]').text()).toContain('Mağaza ekranı bu hesaba gösterilmiyor');
    expect(gets).toEqual(['/app-store']);
    w.unmount();
  });

  it('draws filex’s catalog in the explorer’s table, the icons through filex', async () => {
    const w = mountPage();
    await flushPromises();
    expect(gets).toContain('/app-store/catalog');
    expect(w.find('table').exists(), 'the explorer’s table, never a raw one').toBe(false);
    expect(w.find('[data-testid="store-app-pdfx"]').text()).toContain('App pdfx');
    const img = w.find('[data-testid="store-app-pdfx"] img');
    expect(img.attributes('src')).toMatch(/\/api\/app-store\/media\?store=https%3A%2F%2Fapps\.filex\.sh&file=a{64}\.png$/);
    expect(img.attributes('src')).not.toContain('apps.filex.sh/v1');
    // No frame of the store anywhere.
    expect(w.find('iframe').exists()).toBe(false);
    w.unmount();
  });

  it('offers a request only for an app neither installed nor already asked for', async () => {
    const w = mountPage();
    await flushPromises();
    await openRowMenu(w, 'store-app-actions-pdfx');
    expect(menuEntries().map((e) => e.label)).toEqual(['Ask for this app', 'Details']);
    closeRowMenus();
    await openRowMenu(w, 'store-app-actions-sign');
    expect(menuEntries().map((e) => e.label)).toEqual(['Details']);
    closeRowMenus();
    await openRowMenu(w, 'store-app-actions-drawio');
    expect(menuEntries().map((e) => e.label)).toEqual(['Details']);
    closeRowMenus();
    w.unmount();
  });

  it('sends the store, the app and the person’s words', async () => {
    const w = mountPage();
    await flushPromises();
    await openRowMenu(w, 'store-app-actions-pdfx');
    await pickMenuItem('store-app-actions-pdfx-request');
    await flushPromises();
    const send = lastOf('store-request-send') as HTMLButtonElement;
    expect(send.disabled, 'no reason, no request').toBe(true);
    const reason = document.querySelector<HTMLTextAreaElement>('textarea[name="store-request-reason"]')!;
    reason.value = '  For the contracts team  ';
    reason.dispatchEvent(new Event('input'));
    await flushPromises();
    (lastOf('store-request-send') as HTMLButtonElement).click();
    await flushPromises();
    expect(posted).toEqual([{ url: '/app-store/requests', body: { store: STORE, app: 'pdfx', reason: 'For the contracts team' } }]);
    expect(toasts[0]).toEqual({ kind: 'success', msg: 'Your request for App pdfx was sent to the administrators.' });
    // The requests are read again.
    expect(gets.filter((g) => g === '/app-store/requests').length).toBe(2);
    w.unmount();
  });

  it('marks a catalog the store could not be asked for again', async () => {
    catalog = { ...catalog, stale: true };
    const w = mountPage('tr');
    await flushPromises();
    expect(w.find('[data-testid="store-screen-stale"]').text()).toContain('Mağazaya şu anda ulaşılamıyor');
    w.unmount();
  });

  it('takes what a row is here from the server, never from the requests', async () => {
    // The person's requests say nothing about pdfx; the server says it is
    // asked for. The server is the one answer.
    catalog = { ...catalog, apps: [app('pdfx', { state: 'pending' })] };
    mine = [];
    const w = mountPage();
    await flushPromises();
    await openRowMenu(w, 'store-app-actions-pdfx');
    expect(menuEntries().map((e) => e.label)).toEqual(['Details']);
    closeRowMenus();
    expect(w.find('[data-testid="store-app-pdfx"]').text()).toContain('Requested');
    w.unmount();
  });

  it('lists the storage plugins on a tab of their own, in the server’s words', async () => {
    catalog = {
      ...catalog,
      storage_note: 'A storage plugin is a program an administrator installs on the server.',
      apps: [app('pdfx'), storagePlugin('myfs'), storagePlugin('farfs', false)],
    };
    const w = mountPage();
    await flushPromises();
    expect(w.find('[data-testid="store-app-myfs"]').exists(), 'apps first: no storage plugin on the Apps tab').toBe(false);
    await w.find('[data-testid="store-screen-kind-storage"]').trigger('click');
    await flushPromises();
    expect(w.find('[data-testid="store-app-pdfx"]').exists()).toBe(false);
    expect(w.find('[data-testid="store-screen-storage-note"]').text()).toContain('a program an administrator installs');
    expect(w.find('[data-testid="store-app-checks-myfs"]').text()).toBe('9 checks passed on linux/amd64');
    expect(w.find('[data-testid="store-app-checks-farfs"]').text()).toContain('No build for this server');
    await openRowMenu(w, 'store-app-actions-myfs');
    expect(menuEntries().map((e) => e.label)).toEqual(['Ask for this plugin', 'Details']);
    closeRowMenus();
    await openRowMenu(w, 'store-app-actions-farfs');
    expect(menuEntries().map((e) => e.label), 'no build for this server, nothing to ask for').toEqual(['Details']);
    closeRowMenus();
    w.unmount();
  });

  it('offers no Storage tab when the catalog holds no storage plugin', async () => {
    const w = mountPage();
    await flushPromises();
    expect(w.find('[data-testid="store-screen-kind-storage"]').exists()).toBe(false);
    w.unmount();
  });

  it('lists the person’s own requests and what became of them', async () => {
    mine = [{ id: 1, name: 'drawio', status: 'rejected', decision_note: 'Not now', version: '1.0.0', source: { store: STORE, store_app: 'drawio' }, created_at: '2026-10-05T10:00:00Z' }];
    const w = mountPage();
    await flushPromises();
    expect(w.find('[data-testid="store-request-1"]').text()).toContain('Not now');
    expect(w.find('[data-testid="store-request-status-1"]').text()).toBe('Rejected');
    w.unmount();
  });
});
