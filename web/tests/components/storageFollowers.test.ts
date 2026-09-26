// The admin panel follows storages through ONE follower (core lib/storageWatch,
// held by the storages store): one read of the list per tick for every scan
// and deletion it waits on.
//
// ⚠ #66 gave every "Sync now" its own follower, and each read the WHOLE
// storage list every 3 s — two storages followed, two lists every tick. Its
// "still deleting" toast promised the storage "leaves this list when it is
// done", and nothing read the list again: the row stayed until a reload, with
// "Sync now" still offered on a storage being deleted.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils';
import { nextTick } from 'vue';
import { createPinia, setActivePinia } from 'pinia';
import { createI18n } from 'vue-i18n';
import { createMemoryHistory, createRouter } from 'vue-router';

import Storages from '@/views/Storages.vue';
import en from '@/locales/en.json';
import { useToastStore } from '@/stores/toast';
import { closeRowMenus, menuEntries, openRowMenu, pickMenuItem } from '../helpers/rowMenu';

type Body = Record<string, unknown>;

/** Successive answers to the storage list; the last one repeats. */
let lists: Body[][] = [];
let listReads = 0;
let deleteNeverAnswers = false;

vi.mock('@/api/client', () => ({
  extractError: (_e: unknown, f: string) => f,
  api: {
    get: vi.fn(async (url: string) => {
      if (url === '/admin/storages') {
        listReads++;
        return { data: lists.length > 1 ? lists.shift() : lists[0] };
      }
      return { data: [] };
    }),
    post: vi.fn(async (url: string) => {
      if (url.endsWith('/sync')) return { data: { ok: true, status: 'started' } };
      throw new Error(`unexpected POST ${url}`);
    }),
    patch: vi.fn(),
    delete: vi.fn(async () => {
      // A proxy gave up on a delete the server goes on with (axios: no
      // response, the client's own time limit).
      if (deleteNeverAnswers) throw Object.assign(new Error('timeout'), { code: 'ECONNABORTED', request: {} });
      return { data: {} };
    }),
  },
}));

function row(id: number, over: Body = {}): Body {
  return {
    id,
    name: `S${id}`,
    driver: 'local',
    mount_path: `/s${id}`,
    enabled: true,
    sync_mode: 'ondemand',
    stats: { file_count: 3, total_size_bytes: 10 },
    last_sync_at: null,
    running: false,
    ...over,
  };
}

const said: string[] = [];
const mounted: VueWrapper[] = [];

async function mountPage() {
  setActivePinia(createPinia());
  const seen = new Set<number>();
  useToastStore().$subscribe(
    (_m, state) => {
      for (const t of state.toasts) {
        if (!seen.has(t.id)) {
          seen.add(t.id);
          said.push(t.message);
        }
      }
    },
    { flush: 'sync' },
  );
  const blank = { template: '<div/>' };
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/', component: blank },
      { path: '/storages/new', name: 'storages.new', component: blank },
      { path: '/storages/:id', name: 'storages.edit', component: blank },
      { path: '/:p(.*)*', component: blank },
    ],
  });
  const i18n = createI18n({ legacy: false, locale: 'en', fallbackLocale: 'en', messages: { en } });
  const w = mount(Storages, { global: { plugins: [i18n, router] }, attachTo: document.body });
  mounted.push(w);
  await flushPromises();
  return w;
}

async function entry(w: VueWrapper, id: number, label: string) {
  await openRowMenu(w, `storage-actions-${id}`);
  const found = menuEntries().find((e) => e.label === label);
  document.querySelector<HTMLElement>('.fe-ctx-backdrop')?.click();
  await nextTick();
  return found;
}

beforeEach(() => {
  vi.useFakeTimers();
  listReads = 0;
  deleteNeverAnswers = false;
});

afterEach(() => {
  closeRowMenus();
  mounted.splice(0).forEach((w) => w.unmount());
  said.length = 0;
  lists = [];
  vi.useRealTimers();
});

describe('storages followed by the admin panel', () => {
  it('read the list once per tick, however many are followed', async () => {
    lists = [[row(7), row(8)]];
    const w = await mountPage();
    lists = [[row(7, { running: true }), row(8, { running: true })]];
    await openRowMenu(w, 'storage-actions-7');
    await pickMenuItem('storage-actions-7-sync');
    await flushPromises();
    await openRowMenu(w, 'storage-actions-8');
    await pickMenuItem('storage-actions-8-sync');
    await flushPromises();

    const before = listReads;
    await vi.advanceTimersByTimeAsync(3000);
    await flushPromises();
    expect(listReads - before, 'each followed storage read the list on its own').toBe(1);
    await vi.advanceTimersByTimeAsync(3000);
    await flushPromises();
    expect(listReads - before).toBe(2);
  });

  it('a delete still under way: "Sync now" is not offered, and the row goes when the server is done', async () => {
    lists = [[row(7), row(8)]];
    const w = await mountPage();
    deleteNeverAnswers = true;
    // The server is still deleting S7 (the list still has it), then it is done.
    lists = [[row(7), row(8)], [row(7), row(8)], [row(8)]];

    await openRowMenu(w, 'storage-actions-7');
    await pickMenuItem('storage-actions-7-delete');
    await flushPromises();
    const yes = Array.from(document.querySelectorAll('button')).find(
      (b) => b.textContent?.trim() === en.common.yesDelete,
    ) as HTMLButtonElement;
    yes.click();
    await flushPromises();
    expect(said.at(-1)).toBe(en.storages.deleteStillRunning.replace('{name}', 'S7'));
    expect((await entry(w, 7, en.common.syncNow))?.disabled, 'Sync now offered on a storage being deleted').toBe(true);
    expect(w.text()).toContain(en.storages.deleting);

    await vi.advanceTimersByTimeAsync(3000);
    await flushPromises();
    await vi.advanceTimersByTimeAsync(3000);
    await flushPromises();
    expect(w.find('[data-testid="storage-actions-7"]').exists(), 'the deleted storage stayed in the list').toBe(false);
    expect(said.at(-1)).toBe(en.storages.deletedOk);
  });
});
