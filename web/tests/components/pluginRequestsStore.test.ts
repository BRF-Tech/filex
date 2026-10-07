import { closeRowMenus, openRowMenu, pickMenuItem } from '../helpers/rowMenu';
// #162 - a request from the store screen on the Install requests list: where
// it came from (the store and the app there), the approval that installs
// NOTHING here - it asks the store for a fresh install link (with the
// license key the administrator may give) and opens it in the store review,
// the way a magic link does: the link waits in this tab's storage, the token
// goes into no address, and the review page is a route of this tab (never a
// frame).
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { flushPromises, mount } from '@vue/test-utils';
import { createPinia, setActivePinia } from 'pinia';
import { createI18n } from 'vue-i18n';

import en from '@/locales/en.json';
import tr from '@/locales/tr.json';

const posted: { url: string; body: unknown }[] = [];
let approveRefusal: { status: number; data: unknown } | null = null;

function storeRequest(): Record<string, unknown> {
  return {
    id: 9,
    kind: 'app',
    op: 'install',
    name: 'lang-eo',
    label: { en: 'Esperanto', tr: 'Esperanto dili' },
    source_kind: 'store',
    source: { store: 'https://apps.filex.sh', store_app: 'lang-eo', store_version: '1.0.0' },
    version: '1.0.0',
    sha256: 'ab'.repeat(32),
    permissions: [],
    permission_rows: [],
    requester: 'Ayşe',
    reason: 'Ekip için Esperanto',
    status: 'pending',
    expires_at: '2026-10-20T10:00:00Z',
    created_at: '2026-10-06T10:00:00Z',
  };
}

vi.mock('@/api/client', () => ({
  extractError: (_e: unknown, f: string) => f,
  api: {
    get: vi.fn(async (url: string) => {
      if (url === '/admin/plugin-requests') return { data: { requests: [storeRequest()], ttl_days: 14 } };
      if (url === '/admin/plugin-requests/9') return { data: { request: { ...storeRequest(), review: { manifest: { label: { en: 'Esperanto' } } } } } };
      return { data: {} };
    }),
    post: vi.fn(async (url: string, body: unknown) => {
      posted.push({ url, body });
      if (approveRefusal) {
        throw Object.assign(new Error('refused'), { isAxiosError: true, response: approveRefusal });
      }
      return {
        data: {
          request: storeRequest(),
          store_intent: { store: 'https://apps.filex.sh', token: 'tokentoken-9', app: 'lang-eo', version: '1.0.0', expires_at: '2026-10-06T10:30:00Z' },
        },
      };
    }),
    patch: vi.fn(),
    put: vi.fn(),
    delete: vi.fn(),
  },
}));

const pushes: unknown[] = [];
vi.mock('vue-router', () => ({ useRouter: () => ({ push: async (to: unknown) => pushes.push(to) }) }));

const toasts: { kind: string; msg: string }[] = [];
vi.mock('@/stores/toast', () => ({
  useToastStore: () => ({
    success: (msg: string) => toasts.push({ kind: 'success', msg }),
    info: (msg: string) => toasts.push({ kind: 'info', msg }),
    warn: (msg: string) => toasts.push({ kind: 'warn', msg }),
    error: (msg: string) => toasts.push({ kind: 'error', msg }),
  }),
}));

import PluginRequestsPanel from '@/components/plugins/PluginRequestsPanel.vue';

if (typeof HTMLDialogElement !== 'undefined' && !HTMLDialogElement.prototype.showModal) {
  HTMLDialogElement.prototype.showModal = function () { this.setAttribute('open', ''); };
  HTMLDialogElement.prototype.close = function () { this.removeAttribute('open'); };
}

function mountPanel(locale = 'en') {
  const i18n = createI18n({ legacy: false, locale, fallbackLocale: 'en', messages: { en, tr } });
  return mount(PluginRequestsPanel, { global: { plugins: [i18n] }, attachTo: document.body });
}

async function openReview(w: ReturnType<typeof mountPanel>) {
  await flushPromises();
  await openRowMenu(w, 'plugin-request-actions-9');
  await pickMenuItem('plugin-request-actions-9-review');
  await flushPromises();
  return w.find('[data-testid="plugin-request-review"]');
}

describe('PluginRequestsPanel - a request from the store screen', () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    posted.length = 0;
    pushes.length = 0;
    toasts.length = 0;
    approveRefusal = null;
    sessionStorage.clear();
    closeRowMenus();
  });

  it('says where it came from, and that the approval opens the store review', async () => {
    const w = mountPanel('tr');
    const dialog = await openReview(w);
    expect(dialog.find('[data-testid="plugin-request-source"]').text()).toBe('https://apps.filex.sh mağazası (lang-eo)');
    expect(dialog.find('[data-testid="plugin-request-from-store"]').exists()).toBe(true);
    // The review's "I understand" is the store review's: not asked twice.
    expect(dialog.find('input[name="plugin-request-understand"]').exists()).toBe(false);
    expect(dialog.find('[data-testid="plugin-request-approve"]').exists()).toBe(false);
    expect(dialog.find('[data-testid="plugin-request-approve-store"]').text()).toContain('Onayla: mağaza incelemesini aç');
    w.unmount();
  });

  it('asks the store for a fresh link and opens the store review with it, in this tab', async () => {
    const w = mountPanel();
    const dialog = await openReview(w);
    await dialog.find('input[name="plugin-request-store-license"]').setValue('  FXL-KEY1  ');
    await dialog.find('[data-testid="plugin-request-approve-store"]').trigger('click');
    await flushPromises();
    expect(posted).toEqual([{ url: '/admin/plugin-requests/9/approve', body: { license_key: 'FXL-KEY1' } }]);
    // The link waits in this tab, as a magic link's does (lib/storeLink).
    const kept = JSON.parse(sessionStorage.getItem('filex.storeLink') ?? '{}');
    expect(kept.store).toBe('https://apps.filex.sh');
    expect(kept.token).toBe('tokentoken-9');
    // The review is a route of this tab; the token is in no address.
    expect(pushes).toEqual([{ name: 'store-install' }]);
    expect(JSON.stringify(pushes)).not.toContain('tokentoken-9');
    expect(toasts.map((x) => x.kind)).toEqual(['info']);
    w.unmount();
  });

  it('says a store that is not connected in a sentence, and opens nothing', async () => {
    approveRefusal = { status: 409, data: { error: 'store_not_connected', message: 'not connected' } };
    const w = mountPanel();
    const dialog = await openReview(w);
    await dialog.find('[data-testid="plugin-request-approve-store"]').trigger('click');
    await flushPromises();
    expect(w.find('[data-testid="plugin-request-error"]').text()).toContain('This filex is not connected to the store');
    expect(pushes).toEqual([]);
    expect(sessionStorage.getItem('filex.storeLink')).toBeNull();
    w.unmount();
  });
});
