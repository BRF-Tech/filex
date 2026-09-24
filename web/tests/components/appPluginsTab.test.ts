import { closeRowMenus, menuEntries, openRowMenu } from '../helpers/rowMenu';
// The Apps tab: the runtime banner says whether apps can run here, the table
// draws each state with its own tone, and the shell learns whether Apps
// should be the default tab.
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { flushPromises, mount } from '@vue/test-utils';
import { createPinia, setActivePinia } from 'pinia';
import { createI18n } from 'vue-i18n';

import en from '@/locales/en.json';
import tr from '@/locales/tr.json';

let listAnswer: Record<string, unknown> = {};
let listStatus = 200;

vi.mock('@/api/client', () => ({
  extractError: (_e: unknown, f: string) => f,
  api: {
    get: vi.fn(async (url: string) => {
      if (url === '/admin/app-plugins') {
        if (listStatus !== 200) {
          throw Object.assign(new Error('nope'), { isAxiosError: true, response: { status: listStatus, data: {} } });
        }
        return { data: listAnswer };
      }
      if (url.endsWith('/logs')) return { data: { lines: [], next: 0 } };
      return { data: {} };
    }),
    post: vi.fn(),
    patch: vi.fn(),
    put: vi.fn(),
    delete: vi.fn(),
  },
}));

import AppPluginsTab from '@/components/plugins/AppPluginsTab.vue';

if (typeof HTMLDialogElement !== 'undefined' && !HTMLDialogElement.prototype.showModal) {
  HTMLDialogElement.prototype.showModal = function () { this.setAttribute('open', ''); };
  HTMLDialogElement.prototype.close = function () { this.removeAttribute('open'); };
}

function mountTab(locale = 'en') {
  const i18n = createI18n({ legacy: false, locale, fallbackLocale: 'en', messages: { en, tr } });
  return mount(AppPluginsTab, { global: { plugins: [i18n] } });
}

const rows = [
  { id: 1, name: 'sign', version: '1.0.0', label: { en: 'e-Signature', tr: 'e-İmza' }, enabled: true, state: 'running', source: 'github', signed: true, permissions: ['files:read', 'net:tsa'], actions: 2, views: 2, public_pages: 1, created_at: '', updated_at: '' },
  { id: 2, name: 'shrink', version: '0.3.0', label: { en: 'Shrink images' }, enabled: false, state: 'disabled', source: 'upload', signed: false, permissions: [], actions: 1, views: 0, public_pages: 0, created_at: '', updated_at: '' },
  { id: 3, name: 'broken', version: '2.0.0', label: { en: 'Broken' }, enabled: true, state: 'failed', state_error: 'describe: timeout', source: 'url', signed: false, permissions: ['files:read'], actions: 0, views: 0, public_pages: 0, created_at: '', updated_at: '' },
  { id: 4, name: 'refused', version: '1.1.0', label: { en: 'Refused' }, enabled: true, state: 'refused', source: 'bundle', signed: false, permissions: ['files:read'], actions: 0, views: 0, public_pages: 0, created_at: '', updated_at: '' },
];

describe('AppPluginsTab', () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    listStatus = 200;
    listAnswer = {
      runtime: {
        enabled: true,
        arch_ok: true,
        disabled_reason: '',
        requires_signature: false,
        engines: { ffmpeg: true, libreoffice: false },
        engine_names: { ffmpeg: 'FFmpeg', libreoffice: 'LibreOffice' },
      },
      plugins: rows,
    };
  });

  it('renders every state with its label, the error under a failed row, and the permission count', async () => {
    const w = mountTab();
    await flushPromises();

    expect(w.find('[data-testid="app-plugins-runtime"]').text()).toContain(en.appPlugins.runtime.on);
    expect(w.find('[data-testid="engine-ffmpeg"]').exists()).toBe(true);
    // An engine by the name a person reads — the server's — not its id.
    expect(w.find('[data-testid="engine-libreoffice"]').text()).toBe('LibreOffice');

    const text = w.text();
    expect(text).toContain(en.appPlugins.state.running);
    expect(text).toContain(en.appPlugins.state.disabled);
    expect(text).toContain(en.appPlugins.state.failed);
    expect(text).toContain(en.appPlugins.state.refused);
    expect(w.find('[data-testid="app-plugin-error"]').text()).toBe('describe: timeout');
    expect(text).toContain('2 permissions');
    expect(text).toContain('1 permission');
    expect(text).toContain('e-Signature');

    // ⚠ ONE control per row, pinned right, and the three verbs that used to
    // be loose buttons (details / upgrade / a bare bin icon) are its menu.
    // Nothing was lost in the move, and `Remove` finally has a name on screen.
    expect(w.findAll('.tbl-rowactions').length).toBe(rows.length);
    expect(w.find('[data-testid="app-plugin-actions-sign"]').exists()).toBe(true);
    // The frozen trailing column (DataTable's `.fe-list__col--menu`): one per
    // row plus the header's, which holds the column menu.
    expect(w.findAll('.fe-list__col--menu').length).toBeGreaterThanOrEqual(rows.length + 1);

    await openRowMenu(w, 'app-plugin-actions-sign');
    expect(menuEntries().map((e) => e.label)).toEqual([
      en.appPlugins.actions.details,
      en.appPlugins.actions.upgrade,
      en.appPlugins.actions.remove,
    ]);
    expect(menuEntries().find((e) => e.label === en.appPlugins.actions.remove)?.danger).toBe(true);
    closeRowMenus();

    expect(w.emitted('loaded')?.[0]).toEqual([{ enabled: true, count: 4 }]);
  });

  it('reads the label in Turkish and counts the same way', async () => {
    const w = mountTab('tr');
    await flushPromises();
    expect(w.text()).toContain('e-İmza');
    expect(w.text()).toContain('2 izin');
  });

  it('shows the off banner, the reason, and the empty state that explains the GitHub install', async () => {
    listAnswer = { runtime: { enabled: false, arch_ok: false, disabled_reason: 'FILEX_APP_PLUGINS=off', requires_signature: true, engines: {} }, plugins: [] };
    const w = mountTab();
    await flushPromises();
    const banner = w.find('[data-testid="app-plugins-runtime"]');
    expect(banner.text()).toContain(en.appPlugins.runtime.off);
    expect(banner.text()).toContain(en.appPlugins.runtime.archBad);
    expect(banner.text()).toContain('FILEX_APP_PLUGINS=off');
    expect(banner.text()).toContain(en.appPlugins.runtime.signature);
    expect(w.text()).toContain(en.appPlugins.empty.title);
    expect(w.text()).toContain(en.appPlugins.empty.description);
    expect(w.find('[data-testid="app-plugin-add"]').attributes('disabled')).toBeDefined();
    expect(w.emitted('loaded')?.[0]).toEqual([{ enabled: false, count: 0 }]);
  });

  it('tells a tenant admin whose surface this is on 403', async () => {
    listStatus = 403;
    const w = mountTab();
    await flushPromises();
    expect(w.find('[data-testid="app-plugins-forbidden"]').text()).toBe(en.appPlugins.supertenantOnly);
  });

  // ⚠⚠ A DataTable cell lays its children out in a ROW (base.css, beside
  // `.fe-list__cell:has(> .tbl-sub)` — the same trap caught the Panel's recent
  // activity and Duplicates' paths on 2026-09-22). A language pack's Label cell
  // is the one place in the product carrying THREE pieces: the name, the
  // "Language pack" badge and the coverage line. As siblings they were laid
  // side by side in a 180 px column and wrapped over each other into an
  // unreadable smear, found taking the v0.43.0 screenshot of this very table.
  // The fix is structural, so the assertion is structural: one wrapper, and
  // the coverage under the name rather than beside it.
  it('keeps a language pack row readable — one wrapper in the Label cell', async () => {
    listAnswer = {
      runtime: { enabled: true, arch_ok: true, disabled_reason: '', requires_signature: false, engines: {}, engine_names: {} },
      plugins: [
        {
          id: 9, name: 'lang-es', version: '0.1.0', kind: 'language_pack',
          label: { en: 'Spanish language pack' }, enabled: true, state: 'running',
          source: 'upload', signed: false, permissions: [], actions: 0, views: 0, public_pages: 0,
          languages: [{ code: 'es', keys: 3202, translated: 3202, unknown: 0, total: 3202, percent: 100, rtl: false }],
          created_at: '', updated_at: '',
        },
      ],
    };
    const w = mountTab();
    await flushPromises();

    const badge = w.element.querySelector('[data-testid="app-plugin-kind-lang-es"]');
    expect(badge, 'the row does not say it is a language pack').not.toBeNull();
    const cell = badge!.closest('.fe-list__cell');
    expect(cell, 'the badge is not inside a table cell any more').not.toBeNull();

    expect(
      cell!.children.length,
      'the Label cell has more than one child, and a cell is a flex ROW: the name, the badge and the ' +
        'coverage line will be drawn side by side in a 180 px column and overlap. Put them in one wrapper.',
    ).toBe(1);

    const wrapper = cell!.children[0]!;
    expect(wrapper.contains(badge!), 'the badge left the wrapper').toBe(true);
    // The coverage line is under the name, inside the same wrapper.
    expect(cell!.textContent).toContain('Spanish language pack');
    expect(cell!.textContent).toContain('100');
    const coverage = [...wrapper.children].find((el) => !el.contains(badge!) && /100/.test(el.textContent ?? ''));
    expect(coverage, 'the coverage line is not a sibling of the name+badge line inside the wrapper').toBeTruthy();
  });
});
