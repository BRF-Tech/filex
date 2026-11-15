// #351 and #353 on the storage pages.
//
//  - The new-storage form starts from the driver's default sync mode as the
//    server states it (`default_sync_mode`): live watching for a local
//    storage, poll for the rest - never a mode of its own.
//  - A storage that polls on a local driver is offered live watching with one
//    click, on its page and in the list; the click sends the mode alone.
//  - A local storage on a network filesystem says why watches do not reach
//    it and where filex-watch is explained.
//  - The scan-interval advice is the SERVER's (internal/syncadvice): the form
//    asks while the interval is typed and words the answer.
//
// Through the REAL views, api modules and stores; only the HTTP layer is fake.
import { describe, expect, it, vi, beforeEach } from 'vitest';
import { flushPromises, mount } from '@vue/test-utils';
import { createPinia, setActivePinia } from 'pinia';
import { createI18n } from 'vue-i18n';
import { createMemoryHistory, createRouter } from 'vue-router';

import StorageNew from '@/views/StorageNew.vue';
import StorageEdit from '@/views/StorageEdit.vue';
import Storages from '@/views/Storages.vue';
import en from '@/locales/en.json';
import tr from '@/locales/tr.json';
import { chosenValue, listOffering, pickOption } from '../helpers/choiceSelect';

const calls: Array<{ method: string; url: string; body?: Record<string, unknown>; params?: Record<string, unknown> }> = [];

let editRow: Record<string, unknown> = {};
let listRows: Array<Record<string, unknown>> = [];
let advice: Record<string, unknown> | null = null;

vi.mock('@/api/client', () => ({
  extractError: (_e: unknown, f: string) => f,
  api: {
    get: vi.fn(async (url: string, config?: { params?: Record<string, unknown> }) => {
      calls.push({ method: 'get', url, params: config?.params });
      if (url === '/admin/storage-drivers') {
        return {
          data: [
            {
              driver: 'local',
              label: 'Local',
              i18n_key: 'storages.driver.local',
              capabilities: {},
              fields: [{ key: 'path', type: 'string', label: 'Filesystem path', i18n_key: 'storages.fields.path', required: true, root: true }],
              lazy_fields: [],
              default_sync_mode: 'lazy',
            },
            {
              driver: 's3',
              label: 'S3',
              i18n_key: 'storages.driver.s3',
              capabilities: {},
              fields: [{ key: 'prefix', type: 'string', label: 'Prefix', i18n_key: 'storages.fields.prefix', required: true, root: true }],
              default_sync_mode: 'poll',
            },
          ],
        };
      }
      if (url.endsWith('/interval-advice')) return { data: advice };
      if (url === '/admin/storages/7') return { data: editRow };
      if (url === '/admin/storages') return { data: listRows };
      return { data: [] };
    }),
    post: vi.fn(async (url: string, body: Record<string, unknown>) => {
      calls.push({ method: 'post', url, body });
      return { data: { id: 9, ...body } };
    }),
    patch: vi.fn(async (url: string, body: Record<string, unknown>) => {
      calls.push({ method: 'patch', url, body });
      return { data: { ...editRow, ...body } };
    }),
    put: vi.fn(),
    delete: vi.fn(),
  },
}));

async function mountPage(component: unknown, path: string, locale = 'en') {
  const i18n = createI18n({ legacy: false, locale, fallbackLocale: 'en', messages: { en, tr } });
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/storages', name: 'storages', component: { template: '<div />' } },
      { path: '/storages/new', name: 'storages.new', component: { template: '<div />' } },
      { path: '/storages/:id', name: 'storages.edit', component: { template: '<div />' } },
    ],
  });
  await router.push(path);
  await router.isReady();
  vi.spyOn(router, 'push').mockResolvedValue(undefined);
  vi.spyOn(router, 'replace').mockResolvedValue(undefined);
  const w = mount(component as never, { global: { plugins: [i18n, router] } });
  await flushPromises();
  return w;
}

const modeSelect = (w: ReturnType<typeof mount>) => w.find('[data-testid="storage-sync-mode"]');
const intervalInput = (w: ReturnType<typeof mount>) => w.find('[data-testid="storage-sync-interval"] input');
const adviceAsks = () => calls.filter((c) => c.method === 'get' && c.url.endsWith('/interval-advice'));
/** The advice asks after typing pauses (StorageIntervalAdvice, 400 ms). */
const afterTypingPause = async () => {
  await new Promise((r) => setTimeout(r, 450));
  await flushPromises();
};

beforeEach(() => {
  setActivePinia(createPinia());
  calls.length = 0;
  advice = null;
  listRows = [];
  editRow = {
    id: 7, name: 'arsiv', driver: 'local', enabled: true, read_only: false, rbac_enabled: false,
    sync_interval_s: 300, sync_mode: 'poll', config: { path: '/srv/arsiv' },
  };
});

describe('the default sync mode is the server\'s (#351)', () => {
  // Break: start syncMode at a literal 'poll' and never read
  // drivers.defaultSyncMode - a local storage would be created polling.
  it('a local storage starts on live watching, an S3 one on poll, and the create says so', async () => {
    const w = await mountPage(StorageNew, '/storages/new');
    expect(chosenValue(modeSelect(w))).toBe('lazy');
    expect(modeSelect(w).text()).toContain('Live watching');

    const driver = await listOffering('s3', w.element);
    await pickOption(driver!, 's3');
    await flushPromises();
    expect(chosenValue(modeSelect(w)), 'the new driver\'s own default').toBe('poll');

    await pickOption((await listOffering('local', w.element))!, 'local');
    await flushPromises();
    await w.find('input[required]').setValue('nas');
    await w.find('form').trigger('submit');
    await flushPromises();
    const post = calls.find((c) => c.method === 'post' && c.url === '/admin/storages');
    expect(post?.body?.sync_mode).toBe('lazy');
  });

  it('names the modes the way the owner chose: live watching, and watching the whole storage', async () => {
    const w = await mountPage(StorageNew, '/storages/new', 'tr');
    expect(modeSelect(w).text()).toContain('Canlı izleme');
    expect(modeSelect(w).text()).not.toContain('Tembel katalog');
  });
});

describe('live watching offered to a storage that polls (#351)', () => {
  // Break: send the whole form from the offer - unsaved edits would be saved
  // with it, and an old `push` row refused.
  it('the storage page switches it with the mode alone', async () => {
    editRow = { ...editRow, sync_suggestion: 'lazy' };
    const w = await mountPage(StorageEdit, '/storages/7');
    const offer = w.find('[data-testid="storage-live-offer"]');
    expect(offer.exists()).toBe(true);
    expect(offer.text()).toContain('Switch to live watching');
    await w.find('[data-testid="storage-live-offer-switch"]').trigger('click');
    await flushPromises();
    const patch = calls.find((c) => c.method === 'patch');
    expect(patch?.url).toBe('/admin/storages/7');
    expect(patch?.body).toEqual({ sync_mode: 'lazy' });
    expect(chosenValue(modeSelect(w)), 'the form follows, so its next Save keeps it').toBe('lazy');
  });

  it('a storage that already watches is offered nothing', async () => {
    editRow = { ...editRow, sync_mode: 'lazy' };
    const w = await mountPage(StorageEdit, '/storages/7');
    expect(w.find('[data-testid="storage-live-offer"]').exists()).toBe(false);
  });

  it('the list marks the storage and switches it in one click', async () => {
    listRows = [
      { id: 7, name: 'arsiv', driver: 'local', enabled: true, read_only: false, sync_mode: 'poll', sync_suggestion: 'lazy', config: {} },
      { id: 8, name: 'kova', driver: 's3', enabled: true, read_only: false, sync_mode: 'poll', config: {} },
    ];
    const w = await mountPage(Storages, '/storages', 'tr');
    const button = w.find('[data-testid="storage-live-offer-7"]');
    expect(button.exists()).toBe(true);
    expect(button.text()).toContain('Canlı izlemeye geç');
    expect(w.find('[data-testid="storage-live-offer-8"]').exists()).toBe(false);
    await button.trigger('click');
    await flushPromises();
    expect(calls.find((c) => c.method === 'patch')?.body).toEqual({ sync_mode: 'lazy' });
    expect(calls.filter((c) => c.method === 'get' && c.url === '/admin/storages').length, 'the list is read again').toBeGreaterThanOrEqual(2);
  });

  it('a network filesystem says why, and where filex-watch is explained', async () => {
    editRow = { ...editRow, sync_mode: 'lazy', filesystem: { type: 'NFS', network: true } };
    const w = await mountPage(StorageEdit, '/storages/7', 'tr');
    const note = w.find('[data-testid="storage-network-fs"]');
    expect(note.exists()).toBe(true);
    expect(note.text()).toContain('(NFS)');
    expect(note.text()).toContain('her klasör açıldığında depodan okunur');
    expect(note.text()).toContain('filex-watch kurun');
    expect(w.find('[data-testid="storage-network-fs-docs"]').attributes('href')).toBe('https://docs.filex.sh/FILEX-WATCH');
  });

  // The server scans an `fsnotify` storage on a network filesystem at its
  // interval instead of watching it (sync.loopFSNotify); the note says that,
  // not what live watching would do.
  it('a network filesystem under Watch the whole storage says it is scanned at its interval', async () => {
    editRow = { ...editRow, sync_mode: 'fsnotify', filesystem: { type: 'SMB2', network: true } };
    const w = await mountPage(StorageEdit, '/storages/7', 'tr');
    const note = w.find('[data-testid="storage-network-fs"]');
    expect(note.text()).toContain('tüm depoyu izlemek yerine onu tarama aralığıyla tarar');
    expect(note.text()).not.toContain('her klasör açıldığında');
  });
});

describe('the scan-interval advice is asked of the server (#353)', () => {
  // Break: compute the busy share in the component - nothing asks the
  // server, and the asks below are empty.
  it('asks with what is typed and offers the suggestion in one click', async () => {
    advice = {
      state: 'busy', mode: 'poll', interval_s: 300, busy_percent: 80, suggested_interval_s: 3000,
      target_busy_percent: 10, last_scan: { duration_s: 240, entries: 120000, finished_at: '2026-11-15T09:04:00Z' },
    };
    const w = await mountPage(StorageEdit, '/storages/7');
    expect(adviceAsks()[0]?.url).toBe('/admin/storages/7/interval-advice');
    expect(adviceAsks()[0]?.params).toMatchObject({ interval_s: 300, mode: 'poll' });
    const line = w.find('[data-testid="storage-interval-advice-line"]');
    expect(line.text()).toContain('80%');
    expect(line.text()).toContain('50 min');

    await intervalInput(w).setValue('2');
    await afterTypingPause();
    expect(adviceAsks().at(-1)?.params).toMatchObject({ interval_s: 120 });

    await w.find('[data-testid="storage-interval-advice-apply"]').trigger('click');
    await flushPromises();
    expect((intervalInput(w).element as HTMLInputElement).value).toBe('50');
  });

  it('says nothing is measured yet on a new storage', async () => {
    advice = { state: 'unmeasured', mode: 'poll', interval_s: 900, busy_percent: 0, target_busy_percent: 10 };
    const w = await mountPage(StorageNew, '/storages/new');
    // The form learns the driver's default after the descriptors arrive; the
    // question asked after that carries it.
    await afterTypingPause();
    expect(adviceAsks().at(-1)?.url).toBe('/admin/storages/interval-advice');
    expect(adviceAsks().at(-1)?.params).toMatchObject({ driver: 'local', mode: 'lazy' });
    expect(w.find('[data-testid="storage-interval-advice"]').attributes('data-state')).toBe('unmeasured');
    expect(w.find('[data-testid="storage-interval-advice-apply"]').exists()).toBe(false);
  });

  it('on live watching it says what the interval means there, with no load figure', async () => {
    advice = { state: 'lazy', mode: 'lazy', interval_s: 900, busy_percent: 0, target_busy_percent: 10 };
    editRow = { ...editRow, sync_mode: 'lazy' };
    const w = await mountPage(StorageEdit, '/storages/7', 'tr');
    const line = w.find('[data-testid="storage-interval-advice-line"]');
    expect(line.text()).toContain('güvenlik ağı');
    expect(line.text()).not.toContain('%');
  });

  it('warns before a scan runs filex out of memory', async () => {
    advice = {
      state: 'ok', mode: 'poll', interval_s: 900, busy_percent: 3, target_busy_percent: 10,
      last_scan: { duration_s: 30, entries: 5000, finished_at: '2026-11-15T09:00:30Z', peak_mem_bytes: 3_500_000_000 },
      memory: { peak_bytes: 3_500_000_000, limit_bytes: 4294967296, percent: 81, source: 'container', high: true },
    };
    const w = await mountPage(StorageEdit, '/storages/7');
    const mem = w.find('[data-testid="storage-interval-advice-memory"]');
    expect(mem.exists()).toBe(true);
    expect(mem.text()).toContain('81%');
    expect(mem.text()).toContain('container');
  });
});
