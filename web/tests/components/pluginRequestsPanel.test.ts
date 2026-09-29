import { closeRowMenus, menuEntries, openRowMenu, pickMenuItem } from '../helpers/rowMenu';
// The install requests an API key left (Plugins page): the explorer's table,
// who asked through which key and why, a review with the permission rows the
// install wizard shows, "I understand" before an approval, an optional reason
// for a rejection, and the source-changed answer said as such.
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { flushPromises, mount } from '@vue/test-utils';
import { createPinia, setActivePinia } from 'pinia';
import { createI18n } from 'vue-i18n';

import en from '@/locales/en.json';
import tr from '@/locales/tr.json';

let listStatus = 200;
let requests: Record<string, unknown>[] = [];
const posted: { url: string; body: unknown }[] = [];
let approveAnswer: 'ok' | 'superseded' = 'ok';

vi.mock('@/api/client', () => ({
  extractError: (_e: unknown, f: string) => f,
  api: {
    get: vi.fn(async (url: string) => {
      if (url === '/admin/plugin-requests') {
        if (listStatus !== 200) {
          throw Object.assign(new Error('nope'), { response: { status: listStatus, data: {} } });
        }
        return { data: { requests, ttl_days: 14 } };
      }
      const m = /^\/admin\/plugin-requests\/(\d+)$/.exec(url);
      if (m) return { data: { request: { ...requests.find((r) => r.id === Number(m[1])), review: review } } };
      return { data: {} };
    }),
    post: vi.fn(async (url: string, body: unknown) => {
      posted.push({ url, body });
      if (url.endsWith('/approve') && approveAnswer === 'superseded') {
        throw Object.assign(new Error('409'), {
          response: { status: 409, data: { error: 'superseded', message: 'the manifest changed', request: { ...requests[0], status: 'superseded' } } },
        });
      }
      const status = url.endsWith('/approve') ? 'approved' : 'rejected';
      return { data: { request: { ...requests[0], status } } };
    }),
    patch: vi.fn(),
    put: vi.fn(),
    delete: vi.fn(),
  },
}));

const toasts: { kind: string; msg: string }[] = [];
vi.mock('@/stores/toast', () => ({
  useToastStore: () => ({
    success: (msg: string) => toasts.push({ kind: 'success', msg }),
    warn: (msg: string) => toasts.push({ kind: 'warn', msg }),
    error: (msg: string) => toasts.push({ kind: 'error', msg }),
  }),
}));

import PluginRequestsPanel from '@/components/plugins/PluginRequestsPanel.vue';

if (typeof HTMLDialogElement !== 'undefined' && !HTMLDialogElement.prototype.showModal) {
  HTMLDialogElement.prototype.showModal = function () { this.setAttribute('open', ''); };
  HTMLDialogElement.prototype.close = function () { this.removeAttribute('open'); };
}

const review = {
  manifest: { name: 'sign', version: '0.1.1', permission_reasons: { 'files:read': { en: 'To read the PDF', tr: 'PDF’i okumak için' } } },
  permissions: [{ id: 'files:read', label: 'Read your files' }],
};

function pending(): Record<string, unknown> {
  return {
    id: 7,
    kind: 'app',
    op: 'install',
    name: 'sign',
    label: { en: 'e-Signature', tr: 'e-İmza' },
    source_kind: 'github',
    source: { github_repo: 'BRF-Tech/filex-sign', ref: 'v0.1.1' },
    version: '0.1.1',
    sha256: 'ab'.repeat(32),
    permissions: ['files:read'],
    permission_rows: [{ id: 'files:read', label: 'Read your files' }],
    requester: 'Ayşe',
    token_label: 'work-agent',
    reason: 'Sözleşmeleri imzalamak için gerekiyor',
    status: 'pending',
    expires_at: '2026-10-12T10:00:00Z',
    created_at: '2026-09-28T10:00:00Z',
  };
}

function mountPanel(locale = 'en') {
  const i18n = createI18n({ legacy: false, locale, fallbackLocale: 'en', messages: { en, tr } });
  return mount(PluginRequestsPanel, { global: { plugins: [i18n] }, attachTo: document.body });
}

describe('PluginRequestsPanel', () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    listStatus = 200;
    requests = [pending()];
    posted.length = 0;
    toasts.length = 0;
    approveAnswer = 'ok';
    closeRowMenus();
  });

  it('lists a request in the explorer table: the app, who asked through which key, and why', async () => {
    const w = mountPanel('tr');
    await flushPromises();
    expect(w.find('table').exists()).toBe(false);
    expect(w.find('[data-testid="plugin-request-7"]').text()).toContain('e-İmza');
    const text = w.text();
    expect(text).toContain('Ayşe');
    expect(text).toContain('API anahtarı “work-agent”');
    expect(text).toContain('Sözleşmeleri imzalamak için gerekiyor');
    expect(w.find('[data-testid="plugin-requests-count"]').text()).toBe('1 bekliyor');
    expect(w.find('[data-testid="plugin-request-status-7"]').text()).toBe('Bekliyor');
    w.unmount();
  });

  it('approves only after "I understand", with the permission rows and the app’s own reason', async () => {
    const w = mountPanel();
    await flushPromises();
    await openRowMenu(w, 'plugin-request-actions-7');
    expect(menuEntries().map((e) => e.label)).toEqual(['Review', 'Reject']);
    await pickMenuItem('plugin-request-actions-7-review');
    await flushPromises();

    const dialog = w.find('[data-testid="plugin-request-review"]');
    expect(dialog.exists()).toBe(true);
    expect(dialog.find('[data-testid="perm-files:read"]').text()).toContain('To read the PDF');
    expect(dialog.find('[data-testid="plugin-request-sha256"]').text()).toBe('ab'.repeat(32));
    expect(dialog.find('[data-testid="plugin-request-source"]').text()).toBe('github.com/BRF-Tech/filex-sign@v0.1.1');

    const approve = dialog.find('[data-testid="plugin-request-approve"]');
    expect(approve.attributes('disabled')).toBeDefined();
    await dialog.find('input[name="plugin-request-understand"]').setValue(true);
    expect(dialog.find('[data-testid="plugin-request-approve"]').attributes('disabled')).toBeUndefined();
    await dialog.find('[data-testid="plugin-request-approve"]').trigger('click');
    await flushPromises();

    expect(posted.map((p) => p.url)).toEqual(['/admin/plugin-requests/7/approve']);
    expect(toasts).toEqual([{ kind: 'success', msg: 'e-Signature 0.1.1 installed' }]);
    expect(w.emitted('installed')).toHaveLength(1);
    w.unmount();
  });

  it('rejects with the administrator’s reason', async () => {
    const w = mountPanel();
    await flushPromises();
    await openRowMenu(w, 'plugin-request-actions-7');
    await pickMenuItem('plugin-request-actions-7-reject');
    await flushPromises();
    const dialog = w.find('[data-testid="plugin-request-review"]');
    await dialog.find('textarea').setValue('  Not needed right now  ');
    await dialog.find('[data-testid="plugin-request-reject"]').trigger('click');
    await flushPromises();
    expect(posted).toEqual([{ url: '/admin/plugin-requests/7/reject', body: { reason: 'Not needed right now' } }]);
    expect(toasts[0]).toEqual({ kind: 'success', msg: 'Request for e-Signature rejected' });
    w.unmount();
  });

  it('says a source that changed as such, not as a failure', async () => {
    approveAnswer = 'superseded';
    const w = mountPanel('tr');
    await flushPromises();
    await openRowMenu(w, 'plugin-request-actions-7');
    await pickMenuItem('plugin-request-actions-7-review');
    await flushPromises();
    const dialog = w.find('[data-testid="plugin-request-review"]');
    await dialog.find('input[name="plugin-request-understand"]').setValue(true);
    await dialog.find('[data-testid="plugin-request-approve"]').trigger('click');
    await flushPromises();
    expect(toasts).toHaveLength(1);
    expect(toasts[0].kind).toBe('warn');
    expect(toasts[0].msg).toContain('e-İmza kaynağı istek bırakıldıktan sonra değişti');
    w.unmount();
  });

  it('draws nothing for an administrator who may not manage plugins', async () => {
    listStatus = 403;
    const w = mountPanel();
    await flushPromises();
    expect(w.find('[data-testid="plugin-requests"]').exists()).toBe(false);
    expect(toasts).toEqual([]);
    w.unmount();
  });
});
