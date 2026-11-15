// #352 - a storage's filex-watch card (components/StorageWatchAgent.vue) on
// the storage's own page (views/StorageEdit.vue): the key the agent on the
// machine that owns the disk reports with, what each key's agent last said,
// and the install commands.
//
// Through the REAL page, card and api module; only the HTTP layer is fake, so
// what is asserted is what the server is asked and what the page shows of
// its answers:
//   - the keys are THE table (core DataTable), the row's one verb "Revoke";
//   - a key is shown once, in the answer that made it, with the install
//     commands that answer carries (the key in them); once the person says
//     they copied it, it is gone from the page;
//   - the commands are the server's, one per platform, picked with the
//     segmented control - never a native select;
//   - the card says "Live watching is enough" for a disk on this machine,
//     in the panel's language;
//   - it is there only for a driver whose descriptor says an agent can serve
//     it (`watch_agent`) - an S3 storage gets no card and asks nothing.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushPromises, mount } from '@vue/test-utils';
import { createPinia, setActivePinia } from 'pinia';
import { createI18n } from 'vue-i18n';
import { createMemoryHistory, createRouter } from 'vue-router';
import { DataTable } from '@brftech/filex-core';

import StorageEdit from '@/views/StorageEdit.vue';
import en from '@/locales/en.json';
import tr from '@/locales/tr.json';
import { closeRowMenus, openRowMenu, pickMenuItem } from '../helpers/rowMenu';

const KEY = 'fxw_' + 'k'.repeat(43);

type Call = { method: string; url: string; body?: unknown };
const calls: Call[] = [];

const setupWith = (key: string) => ({
  server: 'https://files.example.com',
  storage_id: 7,
  release: '0.1.0',
  image: 'ghcr.io/brf-tech/filex-watch:0.1.0',
  linux: `printf '%s' '${key}' | sudo tee /etc/filex-watch/key\nsudo systemctl enable --now filex-watch`,
  macos: `printf '%s' '${key}' | sudo tee /usr/local/etc/filex-watch/key`,
  windows: `Set-Content -NoNewline -Path "$data\\key" -Value '${key}'`,
  docker: `docker run -d --name filex-watch ghcr.io/brf-tech/filex-watch:0.1.0 # ${key}`,
});

const state = {
  driver: 'local',
  keys: [] as Array<Record<string, unknown>>,
  maxKeys: 20,
  /** The watch status answers [] - what every other StorageEdit test's mock answers. */
  bare: false,
};

vi.mock('@/api/client', () => ({
  extractError: (_e: unknown, f: string) => f,
  api: {
    get: vi.fn(async (url: string) => {
      calls.push({ method: 'get', url });
      if (url === '/admin/storages/7') {
        return {
          data: {
            id: 7, name: 'nas', driver: state.driver, enabled: true, read_only: false, rbac_enabled: true,
            sync_interval_s: 900, config: state.driver === 's3' ? { bucket: 'b' } : { path: '/mnt/nas' },
          },
        };
      }
      if (url === '/admin/storage-drivers') {
        // The driver's own answer, as the server's descriptor carries it.
        return {
          data: [
            { driver: 'local', label: 'Local', i18n_key: 'storages.driver.local', capabilities: {}, fields: [], watch_agent: true },
            { driver: 's3', label: 'S3', i18n_key: 'storages.driver.s3', capabilities: {}, fields: [] },
          ],
        };
      }
      if (url === '/admin/storages/7/watch') {
        if (state.bare) return { data: [] };
        return {
          data: {
            storage_id: 7, storage: 'nas', server: 'https://files.example.com', release: '0.1.0',
            keys: state.keys, max_keys: state.maxKeys, setup: setupWith('PASTE-THE-KEY-HERE'),
          },
        };
      }
      return { data: [] };
    }),
    post: vi.fn(async (url: string, body: unknown) => {
      calls.push({ method: 'post', url, body });
      if (url === '/admin/storages/7/watch/keys') {
        const row = { id: 13, name: (body as { name: string }).name || 'filex-watch', created_at: '2026-11-15T10:00:00Z' };
        state.keys = [row, ...state.keys];
        return { data: { key: KEY, row, setup: setupWith(KEY) } };
      }
      return { data: {} };
    }),
    patch: vi.fn(async (url: string, body: unknown) => {
      calls.push({ method: 'patch', url, body });
      return { data: {} };
    }),
    delete: vi.fn(async (url: string) => {
      calls.push({ method: 'delete', url });
      state.keys = state.keys.filter((k) => !url.endsWith(`/${k.id}`));
      return { data: {} };
    }),
  },
}));

if (typeof HTMLDialogElement !== 'undefined' && !HTMLDialogElement.prototype.showModal) {
  HTMLDialogElement.prototype.showModal = function () {
    this.setAttribute('open', '');
  };
  HTMLDialogElement.prototype.close = function () {
    this.removeAttribute('open');
  };
}

async function mountEdit(locale = 'en') {
  const i18n = createI18n({ legacy: false, locale, fallbackLocale: 'en', messages: { en, tr } });
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/storages', name: 'storages', component: { template: '<div />' } },
      { path: '/storages/:id', name: 'storages.edit', component: { template: '<div />' } },
      { path: '/grants', name: 'grants', component: { template: '<div />' } },
    ],
  });
  await router.push('/storages/7');
  await router.isReady();
  const w = mount(StorageEdit, { global: { plugins: [i18n, router] }, attachTo: document.body });
  await flushPromises();
  return w;
}

const writes = () => calls.filter((c) => c.method !== 'get');

beforeEach(() => {
  setActivePinia(createPinia());
  calls.length = 0;
  state.driver = 'local';
  state.keys = [];
  state.maxKeys = 20;
  state.bare = false;
});
afterEach(() => {
  closeRowMenus();
  vi.restoreAllMocks();
});

describe('the filex-watch card on a storage page', () => {
  it('lists the keys in THE table, with what each agent last said', async () => {
    state.keys = [
      {
        id: 12, name: 'nas01', created_at: '2026-11-14T10:00:00Z', created_by: 'Alice',
        agent: {
          agent: { id: 'a1', version: '0.1.0', host: 'nas01', os: 'linux', arch: 'amd64', root: '/volume1/media' },
          seen_at: new Date().toISOString(), seq: 4, reports: 4, folders: 9, resyncs: 1,
        },
      },
      { id: 11, name: 'eski', created_at: '2026-11-01T10:00:00Z' },
    ];
    const w = await mountEdit();
    expect(calls).toContainEqual({ method: 'get', url: '/admin/storages/7/watch' });
    const card = w.get('[data-testid="storage-watch"]');
    expect(card.text()).toContain(en.storageWatch.title);
    expect(card.text()).toContain(en.storageWatch.hint);
    expect(w.get('[data-testid="storage-watch-number"]').text()).toBe('Storage number: 7');

    const table = w.findAllComponents(DataTable).find((d) => d.props('tableId') === 'admin.storage.watch');
    expect(table, 'THE table, remembered under its own id').toBeTruthy();
    const row = w.get('[data-testid="storage-watch-row-12"]');
    expect(row.text()).toContain('nas01');
    expect(row.text()).toContain('0.1.0 · nas01 · linux/amd64');
    expect(row.text()).toContain('/volume1/media');
    expect(row.text()).toContain('Made by Alice');
    expect(w.get('[data-testid="storage-watch-row-11"]').text()).toContain(en.storageWatch.noAgent);
    expect(w.find('select').exists(), 'no native select anywhere on the card').toBe(false);
    w.unmount();
  });

  it('shows a new key once, with the commands that carry it, and forgets it when told', async () => {
    const w = await mountEdit();
    // Before a key exists the commands carry the placeholder, and the page says so.
    expect(w.get('[data-testid="storage-watch-command"]').text()).toContain('PASTE-THE-KEY-HERE');
    expect(w.get('[data-testid="storage-watch-setup"]').text()).toContain(en.storageWatch.replaceBoth);

    await w.get('[data-testid="storage-watch-name"] input').setValue('nas01');
    await w.get('[data-testid="storage-watch-form"]').trigger('submit');
    await flushPromises();
    expect(writes().filter((c) => c.method === 'post')).toEqual([
      { method: 'post', url: '/admin/storages/7/watch/keys', body: { name: 'nas01' } },
    ]);
    expect(w.get('[data-testid="storage-watch-key"]').text()).toBe(KEY);
    expect(w.get('[data-testid="storage-watch-made"]').text()).toContain(en.storageWatch.shownOnce);
    expect(w.get('[data-testid="storage-watch-command"]').text(), 'the commands made with the key').toContain(KEY);
    expect(w.get('[data-testid="storage-watch-setup"]').text()).toContain(en.storageWatch.replaceFolder);
    expect(w.find('[data-testid="storage-watch-row-13"]').exists()).toBe(true);

    await w.get('[data-testid="storage-watch-made-done"]').trigger('click');
    await flushPromises();
    expect(w.find('[data-testid="storage-watch-made"]').exists()).toBe(false);
    expect(w.text(), 'the key is nowhere on the page any more').not.toContain(KEY);
    expect(w.get('[data-testid="storage-watch-command"]').text()).toContain('PASTE-THE-KEY-HERE');
    w.unmount();
  });

  it('switches the commands per platform with the segmented control', async () => {
    const w = await mountEdit();
    expect(w.get('[data-testid="storage-watch-command"]').text()).toContain('systemctl');
    await w.get('[data-testid="storage-watch-platform-docker"]').trigger('click');
    expect(w.get('[data-testid="storage-watch-command"]').text()).toContain('docker run');
    await w.get('[data-testid="storage-watch-platform-windows"]').trigger('click');
    expect(w.get('[data-testid="storage-watch-command"]').text()).toContain('Set-Content');
    w.unmount();
  });

  it('revokes a key by the row’s one verb', async () => {
    vi.spyOn(window, 'confirm').mockReturnValue(true);
    state.keys = [{ id: 12, name: 'nas01', created_at: '2026-11-14T10:00:00Z' }];
    const w = await mountEdit();
    await openRowMenu(w, 'storage-watch-actions-12');
    await pickMenuItem('storage-watch-actions-12-revoke');
    await flushPromises();
    expect(writes()).toEqual([{ method: 'delete', url: '/admin/storages/7/watch/keys/12' }]);
    expect(w.find('[data-testid="storage-watch-row-12"]').exists()).toBe(false);
    w.unmount();
  });

  it('offers no new key past the limit, and says why', async () => {
    state.maxKeys = 1;
    state.keys = [{ id: 12, name: 'nas01', created_at: '2026-11-14T10:00:00Z' }];
    const w = await mountEdit();
    expect(w.get('[data-testid="storage-watch-create"]').attributes('disabled')).toBeDefined();
    expect(w.get('[data-testid="storage-watch-full"]').text()).toContain('1');
    w.unmount();
  });

  it('speaks Turkish with Live watching’s Turkish name', async () => {
    const w = await mountEdit('tr');
    const card = w.get('[data-testid="storage-watch"]');
    expect(card.text()).toContain('Başka makinedeki değişiklikler (filex-watch)');
    expect(card.text()).toContain('Canlı izleme yeter');
    expect(w.get('[data-testid="storage-watch-number"]').text()).toBe('Depo numarası: 7');
    w.unmount();
  });

  it('is not there for a storage an agent cannot serve (an object store), and asks the server nothing', async () => {
    state.driver = 's3';
    const w = await mountEdit();
    expect(w.find('[data-testid="storage-watch"]').exists()).toBe(false);
    expect(calls.some((c) => c.url.endsWith('/watch'))).toBe(false);
    w.unmount();
  });

  it('stands on a server answer that is not the card’s (an older mock, an error page)', async () => {
    state.bare = true;
    const w = await mountEdit();
    expect(w.get('[data-testid="storage-watch"]').text()).toContain(en.storageWatch.empty);
    expect(w.find('[data-testid="storage-watch-setup"]').exists()).toBe(false);
    w.unmount();
  });
});
