import { closeRowMenus, menuEntries, openRowMenu, pickMenuItem } from '../helpers/rowMenu';
// The Apps tab: the runtime banner says whether apps can run here, the table
// draws each state with its own tone, and the shell learns whether Apps
// should be the default tab.
import { readFileSync } from 'node:fs';
import path from 'node:path';
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

// ── Updates ─────────────────────────────────────────────────────────────
//
// The rows here are the SERVER's own bytes for "Check now"'s answer
// (testdata/wire/app-plugin-update-check.json): an app whose newer release
// asks for a new permission, and a language pack whose newer version waits
// for an approval, which keeps the version its last approval replaced and is
// outside its own range for this filex. ⚠⚠ Nothing updates itself (0.48).
describe('AppPluginsTab — updates', () => {
  const WIRE = path.resolve(__dirname, '../../../backend/internal/api/handlers/testdata/wire');
  const checkAnswer = () => JSON.parse(readFileSync(path.join(WIRE, 'app-plugin-update-check.json'), 'utf8'));

  beforeEach(async () => {
    setActivePinia(createPinia());
    listStatus = 200;
    const { report: _r, ...list } = checkAnswer();
    listAnswer = list;
    const { api } = await import('@/api/client');
    (api.post as unknown as ReturnType<typeof vi.fn>).mockReset();
    (api.patch as unknown as ReturnType<typeof vi.fn>).mockReset();
    closeRowMenus();
  });

  it('says in the Version cell what the last check found, in ONE wrapper, and the range warning', async () => {
    const w = mountTab();
    await flushPromises();
    const cell = w.element.querySelector('[data-testid="app-plugin-updates-sign"]')!.closest('.fe-list__cell')!;
    expect(cell.children.length, 'the Version cell is a flex row: one wrapper, or its pieces overlap').toBe(1);
    expect(w.find('[data-testid="app-plugin-version-sign"]').text()).toBe('1.2.0');
    expect(w.find('[data-testid="app-plugin-update-approval-sign"]').text()).toBe(en.appPlugins.update.needsApproval);
    expect(w.find('[data-testid="app-plugin-updates-sign"]').text()).toContain('1.2.0 → 1.3.0 · new: mail:send');
    // The badge carries its tone as a prop, not a `variant` Vue would drop.
    expect(w.find('[data-testid="app-plugin-update-approval-sign"]').classes().join(' ')).toMatch(/amber/);

    expect(w.find('[data-testid="app-plugin-compat-lang-es"]').text()).toBe(en.appPlugins.compat.bad);
    expect(w.find('[data-testid="app-plugin-compat-lang-es"]').classes().join(' ')).toMatch(/rose/);
    expect(w.find('[data-testid="app-plugins-update-check"]').text()).toMatch(/^The apps’ sources were last checked for updates /);
    w.unmount();
  });

  it('offers "Review update" only where there is one, and it opens the review of the source’s version', async () => {
    const { api } = await import('@/api/client');
    const dry = JSON.parse(readFileSync(path.join(WIRE, 'app-plugin-upgrade-review.json'), 'utf8'));
    (api.post as unknown as ReturnType<typeof vi.fn>).mockImplementation(async () => ({ data: dry }));
    const w = mountTab();
    await flushPromises();

    await openRowMenu(w, 'app-plugin-actions-lang-es');
    expect(menuEntries().map((e) => e.label)).toEqual([
      en.appPlugins.actions.details,
      en.appPlugins.actions.reviewUpdate,
      'Back to 0.9.0',
      en.appPlugins.actions.upgrade,
      en.appPlugins.actions.remove,
    ]);
    closeRowMenus();

    await openRowMenu(w, 'app-plugin-actions-sign');
    expect(menuEntries().map((e) => e.label), 'nothing kept, nothing to go back to; no automatic switch').toEqual([
      en.appPlugins.actions.details,
      en.appPlugins.actions.reviewUpdate,
      en.appPlugins.actions.upgrade,
      en.appPlugins.actions.remove,
    ]);
    await pickMenuItem('app-plugin-actions-sign-update');
    await flushPromises();
    const call = (api.post as unknown as ReturnType<typeof vi.fn>).mock.calls[0];
    expect(call[0]).toBe('/admin/app-plugins/7/upgrade');
    expect(call[1]).toEqual({ from_source: true, permissions: [] });
    expect(call[2]).toMatchObject({ params: { dry_run: 1 } });
    // Straight on the review: the jump, what it adds, and — this review's
    // range leaves 0.47.0 out — why it cannot be installed.
    expect(w.find('[data-testid="app-plugin-upgrade-jump"]').text()).toContain('1.1.0 → 1.2.0');
    expect(w.find('[data-testid="app-plugin-incompatible"]').exists()).toBe(true);
    w.unmount();
  });

  it('"Check for updates" asks the server, redraws the list and says what waits — it installs nothing', async () => {
    const { api } = await import('@/api/client');
    (api.post as unknown as ReturnType<typeof vi.fn>).mockImplementation(async (url: string) => {
      if (url === '/admin/app-plugins/updates/check') return { data: checkAnswer() };
      return { data: {} };
    });
    listAnswer = { ...listAnswer, plugins: [] };
    const w = mountTab('tr');
    await flushPromises();
    expect(w.find('[data-testid="app-plugin-updates-sign"]').exists()).toBe(false);
    await w.find('[data-testid="app-plugins-check-updates"]').trigger('click');
    await flushPromises();
    const call = (api.post as unknown as ReturnType<typeof vi.fn>).mock.calls[0];
    expect(call[0]).toBe('/admin/app-plugins/updates/check');
    expect(call[2]?.timeout, 'a check reads every app’s source: it waits for all of them').toBeGreaterThanOrEqual(600_000);
    expect(w.find('[data-testid="app-plugin-updates-sign"]').exists(), 'the list is the answer’s').toBe(true);
    const { useToastStore } = await import('@/stores/toast');
    expect(useToastStore().toasts.map((x) => x.message)).toEqual(['Güncelleme denetimi bitti: 2 güncelleme onayınızı bekliyor.']);
    w.unmount();
  });

  it('"Back to <version>" puts the kept version back, after asking, and redraws the list', async () => {
    const { api } = await import('@/api/client');
    const back = { ...listAnswer.plugins[1], version: '0.9.0', previous: { version: '1.0.0', replaced_at: '2026-09-27T08:00:00Z' } };
    (api.post as unknown as ReturnType<typeof vi.fn>).mockImplementation(async (url: string) => {
      if (url === '/admin/app-plugins/9/rollback') return { data: back };
      return { data: {} };
    });
    const ask = vi.spyOn(window, 'confirm').mockReturnValueOnce(false).mockReturnValueOnce(true);
    const w = mountTab();
    await flushPromises();
    expect(w.find('[data-testid="app-plugin-updates-lang-es"]').text()).toContain('Version 0.9.0 is kept to go back to');

    await openRowMenu(w, 'app-plugin-actions-lang-es');
    await pickMenuItem('app-plugin-actions-lang-es-rollback');
    await flushPromises();
    expect(ask).toHaveBeenCalledTimes(1);
    expect(ask.mock.calls[0][0]).toContain('back to 0.9.0');
    expect((api.post as unknown as ReturnType<typeof vi.fn>).mock.calls, 'said no: nothing sent').toHaveLength(0);

    await openRowMenu(w, 'app-plugin-actions-lang-es');
    await pickMenuItem('app-plugin-actions-lang-es-rollback');
    await flushPromises();
    expect((api.post as unknown as ReturnType<typeof vi.fn>).mock.calls[0][0]).toBe('/admin/app-plugins/9/rollback');
    const { useToastStore } = await import('@/stores/toast');
    expect(useToastStore().toasts.map((x) => x.message)).toContain('Spanish language pack is back to 0.9.0.');
    w.unmount();
  });
});
