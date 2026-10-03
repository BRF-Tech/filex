import { closeRowMenus, menuEntries, openRowMenu, pickMenuItem } from '../helpers/rowMenu';
// A storage plugin that names an update source (M5, plugin/updates.go).
// ⚠⚠ Nothing updates itself: the row SAYS a newer version is there, "Review
// update" shows the jump, the build's SHA-256 and the notes (Markdown), and
// only the administrator's Upgrade sends {from_source: true}.
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { flushPromises, mount } from '@vue/test-utils';
import { createPinia, setActivePinia } from 'pinia';
import { createI18n } from 'vue-i18n';

import en from '@/locales/en.json';
import tr from '@/locales/tr.json';

const SHA = 'ab'.repeat(32);
const base = {
  kind: 'binary', enabled: true, state: 'running', restarts: 0, field_count: 2, in_use: 1,
  load: { in_flight: 0, waited: 0, rejected: 0, max_in_flight: 10 }, created_at: '', updated_at: '',
};
let plugins: Record<string, unknown>[] = [];

vi.mock('@/api/client', () => ({
  extractError: (_e: unknown, f: string) => f,
  api: {
    get: vi.fn(async () => ({ data: { plugins, dir: '/data/plugins', requires_signature: false, conformance: 'enforce', update_check: true, updates_checked_at: null } })),
    post: vi.fn(),
    patch: vi.fn(),
    delete: vi.fn(),
  },
}));

import StoragePluginsTab from '@/components/plugins/StoragePluginsTab.vue';

if (typeof HTMLDialogElement !== 'undefined' && !HTMLDialogElement.prototype.showModal) {
  HTMLDialogElement.prototype.showModal = function () { this.setAttribute('open', ''); };
  HTMLDialogElement.prototype.close = function () { this.removeAttribute('open'); };
}

function mountTab(locale = 'en') {
  const i18n = createI18n({ legacy: false, locale, fallbackLocale: 'en', messages: { en, tr } });
  return mount(StoragePluginsTab, { global: { plugins: [i18n] }, attachTo: document.body });
}

describe('StoragePluginsTab — update source', () => {
  beforeEach(async () => {
    setActivePinia(createPinia());
    closeRowMenus();
    plugins = [
      {
        ...base, id: 3, name: 'myfs', binary: 'myfs', version: '1.0.0', driver: 'myfs', source: 'acme/filex-myfs',
        update: { status: 'available', version: '1.1.0', sha256: SHA, platform: 'linux/amd64', notes: 'Faster <b>listings</b>.' },
      },
      { ...base, id: 4, name: 'nas', binary: 'nas', version: '2.0.0', driver: 'nas' },
      { ...base, id: 5, name: 'far', kind: 'remote', address: 'https://far.example', version: '1.0.0', driver: 'far' },
    ];
    const { api } = await import('@/api/client');
    (api.post as unknown as ReturnType<typeof vi.fn>).mockReset();
    (api.patch as unknown as ReturnType<typeof vi.fn>).mockReset();
  });

  it('says a newer version on the row, and offers the review only there', async () => {
    const w = mountTab();
    await flushPromises();
    const line = w.find('[data-testid="plugin-update-myfs"]');
    expect(line.text()).toContain(en.plugins.update.available);
    expect(line.text()).toContain('1.0.0 → 1.1.0');
    expect(w.find('[data-testid="plugin-update-nas"]').exists(), 'no source, nothing said').toBe(false);

    await openRowMenu(w, 'plugin-actions-myfs');
    expect(menuEntries().map((e) => e.label)).toContain(en.plugins.actions.reviewUpdate);
    expect(menuEntries().map((e) => e.label)).toContain(en.plugins.actions.setSource);
    closeRowMenus();
    await openRowMenu(w, 'plugin-actions-nas');
    expect(menuEntries().map((e) => e.label)).not.toContain(en.plugins.actions.reviewUpdate);
    expect(menuEntries().map((e) => e.label)).toContain(en.plugins.actions.setSource);
    closeRowMenus();
    await openRowMenu(w, 'plugin-actions-far');
    expect(menuEntries().map((e) => e.label), 'a remote plugin is upgraded where it runs').not.toContain(en.plugins.actions.setSource);
    closeRowMenus();
    w.unmount();
  });

  // The notes are Markdown since #122, drawn through the explorer preview's
  // pipeline and sanitizer (ReleaseNotes, the component the Apps review uses
  // too); that nothing in them runs is releaseNotes.test.ts (jsdom).
  it('"Review update" shows the jump, the hash and the notes as Markdown; only Upgrade installs', async () => {
    const { api } = await import('@/api/client');
    (api.post as unknown as ReturnType<typeof vi.fn>).mockImplementation(async () => ({ data: { ...plugins[0], version: '1.1.0' } }));
    const w = mountTab('tr');
    await flushPromises();
    await openRowMenu(w, 'plugin-actions-myfs');
    await pickMenuItem('plugin-actions-myfs-review');
    await flushPromises();
    expect(document.querySelector('[data-testid="plugin-review-jump"]')?.textContent).toContain('Sürüm 1.0.0 → 1.1.0');
    expect(document.querySelector('[data-testid="plugin-review-sha"]')?.textContent).toBe(SHA);
    await vi.waitFor(
      async () => {
        await flushPromises();
        expect(document.querySelector('[data-testid="plugin-review-notes-md"]')).not.toBeNull();
      },
      { timeout: 5000 },
    );
    const notes = document.querySelector('[data-testid="plugin-review-notes-md"]')!;
    expect(notes.querySelector('b')?.textContent, 'the release body is rendered, not printed').toBe('listings');
    expect(notes.textContent).toContain('Faster listings.');
    expect(notes.textContent).not.toContain('<b>');
    expect((api.post as unknown as ReturnType<typeof vi.fn>).mock.calls, 'opening the review installs nothing').toHaveLength(0);

    (document.querySelector('[data-testid="plugin-review-install"]') as HTMLButtonElement).click();
    await flushPromises();
    const call = (api.post as unknown as ReturnType<typeof vi.fn>).mock.calls[0];
    expect(call[0]).toBe('/admin/plugins/3/upgrade');
    expect(call[1]).toEqual({ from_source: true });
    w.unmount();
  });

  it('"Check for updates" asks the server and says what waits', async () => {
    const { api } = await import('@/api/client');
    (api.post as unknown as ReturnType<typeof vi.fn>).mockImplementation(async () => ({
      data: { report: { checked_at: '', checked: 1, available: ['myfs'], failed: [] }, plugins },
    }));
    const w = mountTab('tr');
    await flushPromises();
    await w.find('[data-testid="plugins-check-updates"]').trigger('click');
    await flushPromises();
    expect((api.post as unknown as ReturnType<typeof vi.fn>).mock.calls[0][0]).toBe('/admin/plugins/updates/check');
    const { useToastStore } = await import('@/stores/toast');
    expect(useToastStore().toasts.map((x) => x.message)).toContain('Güncelleme denetimi bitti: 1 güncelleme onayınızı bekliyor.');
    w.unmount();
  });

  it('"Update source…" names the source, and an empty one clears it', async () => {
    const { api } = await import('@/api/client');
    (api.patch as unknown as ReturnType<typeof vi.fn>).mockImplementation(async (_u: string, body: { source: string }) => ({
      data: { ...plugins[1], source: body.source || undefined },
    }));
    const w = mountTab();
    await flushPromises();
    await openRowMenu(w, 'plugin-actions-nas');
    await pickMenuItem('plugin-actions-nas-source');
    await flushPromises();
    const input = document.querySelector('[data-testid="plugin-source-input"] input, input[data-testid="plugin-source-input"]') as HTMLInputElement;
    input.value = 'acme/filex-nas';
    input.dispatchEvent(new Event('input'));
    (document.querySelector('[data-testid="plugin-source-save"]') as HTMLButtonElement).click();
    await flushPromises();
    expect((api.patch as unknown as ReturnType<typeof vi.fn>).mock.calls[0]).toEqual(['/admin/plugins/4', { source: 'acme/filex-nas' }]);
    w.unmount();
  });

  it('installs from a source with the name and the source only', async () => {
    const { api } = await import('@/api/client');
    (api.post as unknown as ReturnType<typeof vi.fn>).mockImplementation(async () => ({ data: plugins[0] }));
    const w = mountTab();
    await flushPromises();
    const add = w.findAll('button').find((b) => b.text() === en.plugins.add)!;
    await add.trigger('click');
    await flushPromises();
    (document.querySelector('[data-testid="plugin-source-feed"]') as HTMLButtonElement).click();
    await flushPromises();
    const name = document.querySelector('input[placeholder="myfs"]') as HTMLInputElement;
    name.value = 'myfs';
    name.dispatchEvent(new Event('input'));
    const feed = document.querySelector('[data-testid="plugin-feed"] input, input[data-testid="plugin-feed"]') as HTMLInputElement;
    feed.value = 'acme/filex-myfs';
    feed.dispatchEvent(new Event('input'));
    (document.querySelector('[data-testid="plugin-install"]') as HTMLButtonElement).click();
    await flushPromises();
    const call = (api.post as unknown as ReturnType<typeof vi.fn>).mock.calls[0];
    expect(call[0]).toBe('/admin/plugins');
    expect(call[1]).toEqual({ name: 'myfs', source: 'acme/filex-myfs' });
    w.unmount();
  });
});
