// An installed app's details: a PAGE, in words.
//
// Release-candidate sweep, 2026-09-21. Plugins → Apps → Details opened a
// dialog holding settings, the menu actions table, the schedule table, the
// locks table and a live log; the address stayed /admin/plugins, so Back left
// the panel section altogether. Inside it: what the app was allowed to do
// was chips of keys (`engines:libreoffice files:lock …`), the signer's hidden
// `apply` action was offered as a switch in the overrides table, and the
// log's times were raw ISO (`2026-09-21T17:48:53.2871898+03:00`).
//
// What has to stay true:
//
//   · Details is a route of its own — `/plugins/apps/<name>` — so Back works
//     and the address can be sent; the list's "Details" goes there;
//   · the page is not a dialog;
//   · the permissions are the install review's sentences (one source, the
//     server's PermissionRows), with the key only as a tooltip;
//   · a hidden action is not listed among the switches;
//   · a log line's time is the product's date format, the full one on hover.
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { flushPromises, mount } from '@vue/test-utils';
import { createPinia, setActivePinia } from 'pinia';
import { createI18n } from 'vue-i18n';
import { createMemoryHistory, createRouter } from 'vue-router';

import en from '@/locales/en.json';
import tr from '@/locales/tr.json';
import { formatDate, formatDateFull } from '@/lib/format';
import { closeRowMenus, menuEntries, openRowMenu } from '../helpers/rowMenu';
import { api } from '@/api/client';

const LOG_TS = '2026-09-21T17:48:53.2871898+03:00';
const locksAnswer: { locks: unknown[] } = { locks: [] };

vi.mock('@/api/client', () => ({
  extractError: (_e: unknown, f: string) => f,
  api: {
    get: vi.fn(async (url: string) => {
      if (url.endsWith('/logs')) {
        return { data: { lines: [{ seq: 1, ts: LOG_TS, level: 'info', msg: 'signed 2 documents' }], next: 2 } };
      }
      if (url.endsWith('/locks')) return { data: locksAnswer };
      if (url === '/admin/app-plugins') return { data: { runtime, plugins: [app] } };
      if (/\/admin\/app-plugins\/\d+$/.test(url)) {
        // The wire shape: the reviewed rows arrive as the top-level
        // `permissions`, the ids as `granted`.
        return {
          data: {
            plugin: app,
            manifest: {
              manifest_version: 1,
              name: 'sign',
              version: '0.1.0',
              label: { en: 'Sign', tr: 'İmzala' },
              permissions: ['files:read', 'engines:libreoffice'],
              actions: [
                {
                  id: 'request',
                  label: { en: 'Request signatures', tr: 'İmza iste' },
                  // engine_ext: what the signing work adds — office files
                  // only while LibreOffice is on the server.
                  applies: { kind: 'file', ext: ['pdf'], engine_ext: { libreoffice: ['docx', 'odt'] } },
                },
                { id: 'apply', label: { en: 'Apply', tr: 'Uygula' }, hidden: true },
              ],
              views: [],
              settings: [],
            },
            granted: ['files:read', 'engines:libreoffice'],
            permissions: [
              { id: 'files:read', label: 'Read the files you choose', reason: { en: 'to show them', tr: 'göstermek için' } },
              { id: 'engines:libreoffice', label: 'Run LibreOffice on this server', reason: { en: 'to make PDFs', tr: 'PDF yapmak için' } },
            ],
            overrides: [],
            settings: {},
            schedule: [],
          },
        };
      }
      return { data: {} };
    }),
    post: vi.fn(async () => ({ data: {} })),
    patch: vi.fn(async () => ({ data: {} })),
    put: vi.fn(async () => ({ data: {} })),
    delete: vi.fn(async () => ({ data: {} })),
  },
}));

import AppPluginsTab from '@/components/plugins/AppPluginsTab.vue';
import AppPluginDetail from '@/components/plugins/AppPluginDetail.vue';

const runtime = { enabled: true, arch_ok: true, disabled_reason: '', requires_signature: false, engines: {} };
const app = {
  id: 7,
  name: 'sign',
  version: '0.1.0',
  label: { en: 'Sign', tr: 'İmzala' },
  enabled: true,
  state: 'running',
  source: 'github',
  sha256: 'a9950e0d84a62a1b30c952736b097e5f3cde019564724dc3c0432a36f7c0d091',
  signed: true,
  permissions: ['files:read', 'engines:libreoffice'],
  scheduled: false,
  actions: 2,
  views: 0,
  public_pages: 0,
  created_at: '2026-09-01T09:00:00Z',
  updated_at: '2026-09-01T09:00:00Z',
};

function i18nFor(locale: string) {
  return createI18n({ legacy: false, locale, fallbackLocale: 'en', messages: { en, tr } });
}

function testRouter() {
  return createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/plugins', name: 'plugins', component: { template: '<div />' } },
      { path: '/plugins/apps/:name', name: 'plugins.app', component: { template: '<div />' } },
      { path: '/queue', name: 'queue', component: { template: '<div />' } },
    ],
  });
}

beforeEach(() => {
  setActivePinia(createPinia());
  document.body.innerHTML = '';
});

describe('the app page', () => {
  it('is a route of the panel, addressed by the app’s name', async () => {
    const { default: router } = await import('@/router');
    const r = router.resolve({ name: 'plugins.app', params: { name: 'sign' } });
    expect(r.matched.length).toBeGreaterThan(0);
    expect(r.path.endsWith('/plugins/apps/sign')).toBe(true);
    expect(r.meta.parent).toBe('plugins');
    // Coming back lands on the tab it was opened from.
    expect(String(r.meta.breadcrumb)).toBe('appPlugins.page.breadcrumb');
  });

  it('Details in the list goes to that page, not a dialog', async () => {
    const r = testRouter();
    await r.push('/plugins');
    const w = mount(AppPluginsTab, { global: { plugins: [i18nFor('en'), r] }, attachTo: document.body });
    await flushPromises();

    await openRowMenu(w, 'app-plugin-actions-sign');
    const details = Array.from(document.querySelectorAll<HTMLButtonElement>('.fe-ctx .fe-ctx__item')).find(
      (b) => b.querySelector('.fe-ctx__label')?.textContent?.trim() === en.appPlugins.actions.details,
    );
    expect(details, `menu: ${JSON.stringify(menuEntries())}`).toBeDefined();
    details!.click();
    await flushPromises();

    expect(r.currentRoute.value.fullPath).toBe('/plugins/apps/sign');
    expect(document.querySelector('dialog[open]')).toBeNull();
  });

  it('says the permissions in words, lists no hidden action and dates the log', async () => {
    const r = testRouter();
    await r.push('/plugins/apps/sign');
    const w = mount(AppPluginDetail, {
      props: { plugin: app },
      global: { plugins: [i18nFor('tr'), r] },
      attachTo: document.body,
    });
    await flushPromises();

    // Not a dialog: the sections are in the page's flow, and nothing is open
    // over them (the confirmations inside it are closed dialogs of their own).
    const sections = w.find('[data-testid="app-plugin-detail"]');
    expect(sections.exists()).toBe(true);
    expect(sections.element.closest('dialog')).toBeNull();
    expect(document.querySelector('dialog[open]')).toBeNull();

    const granted = w.find('[data-testid="app-plugin-granted"]');
    expect(granted.text()).toContain(tr.appPlugins.detail.grantedTitle);
    expect(w.find('[data-testid="granted-engines:libreoffice"]').text()).toContain('Run LibreOffice on this server');
    expect(w.find('[data-testid="granted-engines:libreoffice"]').text()).toContain('PDF yapmak için');
    expect(w.find('[data-testid="granted-engines:libreoffice"]').attributes('title')).toBe('engines:libreoffice');
    // ⚠ The key is the tooltip, not the text.
    expect(granted.text()).not.toContain('engines:libreoffice');

    // The overrides table: the menu action has its switches, the hidden
    // one has none.
    expect(w.find('#override-enabled-request').exists()).toBe(true);
    expect(w.find('#override-enabled-apply').exists()).toBe(false);
    expect(w.find('#override-admin-apply').exists()).toBe(false);

    const logs = w.find('[data-testid="app-plugin-logs"]');
    expect(logs.text()).not.toContain(LOG_TS);
    expect(logs.text()).toContain(formatDate(LOG_TS, 'tr'));
    expect(logs.find(`[title="${formatDateFull(LOG_TS, 'tr')}"]`).exists()).toBe(true);
  });

  // ⚠⚠ The editor starts from the manifest's list AND the engine-gated one,
  // and says which are which; the server stores only the change (backend
  // wasmplugin/applies_override.go) and resolves engines at every read. A
  // copy of what the host had computed at Save froze the office types in or
  // out whatever became of LibreOffice afterwards.
  it('customising a rule starts from every type, the engine-gated ones said to be', async () => {
    const r = testRouter();
    await r.push('/plugins/apps/sign');
    const w = mount(AppPluginDetail, {
      // The engine's name is the server's (the list answer's engine_names).
      props: { plugin: app, engineNames: { libreoffice: 'LibreOffice' } },
      global: { plugins: [i18nFor('en'), r] },
      attachTo: document.body,
    });
    await flushPromises();

    await openRowMenu(w, 'override-actions-request');
    const customise = Array.from(document.querySelectorAll<HTMLButtonElement>('.fe-ctx .fe-ctx__item')).find(
      (b) => b.querySelector('.fe-ctx__label')?.textContent?.trim() === en.appPlugins.detail.overrides.customize,
    );
    expect(customise, JSON.stringify(menuEntries())).toBeDefined();
    customise!.click();
    await flushPromises();
    closeRowMenus();

    expect(w.find('[data-testid="applies-engine-note-request"]').text()).toBe(
      'Only while LibreOffice is on this server: .docx .odt',
    );
    await w.find('[data-testid="app-plugin-save-overrides"]').trigger('click');
    await flushPromises();
    const put = vi.mocked(api.put).mock.calls.find(([url]) => String(url).endsWith('/overrides'));
    expect(put, 'overrides were saved').toBeDefined();
    const sent = (put![1] as { actions: { id: string; applies: { ext: string[] } | null }[] }).actions.find(
      (a) => a.id === 'request',
    );
    expect(sent?.applies?.ext).toEqual(['pdf', 'docx', 'odt']);
  });

  // v0.43.0 wave 2 (2026-09-22): a frozen file was listed as "sözleşme.pdf —
  // Depo #1", a number no other screen shows. The storage's name; the id only
  // for a storage that is gone.
  it('names the storage a locked file is on', async () => {
    locksAnswer.locks = [
      {
        storage_id: 1,
        storage: 'depo',
        path: 'sözleşme.pdf',
        plugin: 'sign',
        reason: 'signatures are being collected',
        reason_text: { en: 'signatures are being collected', tr: 'imzalar toplanıyor' },
        created_at: '2026-09-22T11:00:00Z',
      },
      { storage_id: 9, path: 'eski.pdf', plugin: 'sign', reason: 'plain words', created_at: '2026-09-22T11:00:00Z' },
    ];
    try {
      const r = testRouter();
      await r.push('/plugins/apps/sign');
      const w = mount(AppPluginDetail, {
        props: { plugin: app },
        global: { plugins: [i18nFor('tr'), r] },
        attachTo: document.body,
      });
      await flushPromises();
      const names = w.findAll('[data-testid="app-plugin-lock-storage"]').map((c) => c.text());
      expect(names).toEqual(['depo', tr.appPlugins.detail.locks.storage.replace('{id}', '9')]);
      // The reason in the READER's language (a manifest message kept in
      // every language), the plain reason where there is no other.
      const reasons = w.findAll('[data-testid="app-plugin-lock-reason"]').map((c) => c.text());
      expect(reasons).toEqual(['imzalar toplanıyor', 'plain words']);
    } finally {
      locksAnswer.locks = [];
    }
  });
});
