// "Sync now" says what the server did, and says when the scan ends.
//
// ⚠ All three buttons (Dashboard, Storages, a storage's own page) said "Sync
// started" whatever the server answered, "a scan is already running" included,
// and then nothing: the badge was not read again, so the end of the scan, and
// how it ended, went unsaid. The storage's own page did not even hold its
// button while the request was out.
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
let syncAnswer: Body = { ok: true, status: 'started' };

vi.mock('@/api/client', () => ({
  extractError: (_e: unknown, f: string) => f,
  api: {
    get: vi.fn(async (url: string) => {
      if (url === '/admin/storages') return { data: lists.length > 1 ? lists.shift() : lists[0] };
      return { data: [] };
    }),
    post: vi.fn(async (url: string) => {
      if (url.endsWith('/sync')) return { data: syncAnswer };
      throw new Error(`unexpected POST ${url}`);
    }),
    patch: vi.fn(),
    delete: vi.fn(),
  },
}));

function row(over: Body = {}): Body {
  return {
    id: 7,
    name: 'Arsiv',
    driver: 'local',
    mount_path: '/arsiv',
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

/** "Sync now" is in the row's Actions menu (#57 made the page a table). */
async function pressSync(w: VueWrapper) {
  await openRowMenu(w, 'storage-actions-7');
  await pickMenuItem('storage-actions-7-sync');
}

/** Whether the row's "Sync now" is held, as its menu shows it. The menu is
 *  closed the way a person closes it, by a click beside it: removing it from
 *  the page would leave it open as far as the row is concerned, and the next
 *  click on the row's control would close it instead. */
async function syncHeld(w: VueWrapper) {
  await openRowMenu(w, 'storage-actions-7');
  const held = menuEntries().find((e) => e.label === en.common.syncNow)?.disabled;
  document.querySelector<HTMLElement>('.fe-ctx-backdrop')?.click();
  await nextTick();
  return held;
}

beforeEach(() => {
  vi.useFakeTimers();
  syncAnswer = { ok: true, status: 'started' };
});

afterEach(() => {
  closeRowMenus();
  mounted.splice(0).forEach((w) => w.unmount());
  said.length = 0;
  lists = [];
  vi.useRealTimers();
});

describe('Sync now', () => {
  it('says it started, holds the button while the scan runs, and says when it is done', async () => {
    lists = [[row()]];
    const w = await mountPage();
    lists = [[row({ running: true })], [row({ running: true })], [row({ running: false, last_sync_state: 'ok' })]];

    await pressSync(w);
    await flushPromises();
    expect(said).toEqual([en.storages.syncStarted]);
    expect(await syncHeld(w), 'the entry let go while the scan ran').toBe(true);

    await vi.advanceTimersByTimeAsync(3000);
    await flushPromises();
    expect(said, 'the end was announced early').toHaveLength(1);

    await vi.advanceTimersByTimeAsync(3000);
    await flushPromises();
    expect(said.at(-1)).toBe(en.storages.syncDone.replace('{name}', 'Arsiv'));
    expect(await syncHeld(w)).toBe(false);
  });

  it('says a scan was already running instead of "Sync started"', async () => {
    lists = [[row({ running: true })]];
    const w = await mountPage();
    syncAnswer = { ok: true, status: 'running' };
    lists = [[row({ running: true })], [row({ running: false, last_sync_state: 'ok' })]];

    await pressSync(w);
    await flushPromises();
    expect(said).toEqual([en.storages.syncAlreadyRunning.replace('{name}', 'Arsiv')]);
  });

  it('says how a failed scan failed', async () => {
    lists = [[row()]];
    const w = await mountPage();
    lists = [[row({ running: true })], [row({ running: false, last_sync_state: 'failed', last_sync_error: 'bucket gone' })]];

    await pressSync(w);
    await flushPromises();
    await vi.advanceTimersByTimeAsync(3000);
    await flushPromises();
    expect(said.at(-1)).toBe(en.storages.syncFailed.replace('{name}', 'Arsiv').replace('{error}', 'bucket gone'));
  });
});
