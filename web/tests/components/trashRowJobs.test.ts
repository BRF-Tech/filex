// A restore or a permanent delete from the admin's Trash page is a job of the
// queue.
//
// ⚠⚠ Both ran inside the request. A folder is moved back, or purged, one object
// at a time, and this page's HTTP client gives up after 30 s: the admin read
// "the server could not be reached" beside a raw "AxiosError: timeout of
// 30000ms exceeded" while the server carried on, and a second press was
// refused by the half that had already come back. A server that runs them as
// jobs says so (`capabilities.queued`); the page then asks for one, marks the
// row while it runs, and says how it ended.
import { afterEach, describe, expect, it, vi } from 'vitest';
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils';
import { createPinia, setActivePinia } from 'pinia';
import { createI18n } from 'vue-i18n';

import Trash from '@/views/Trash.vue';
import en from '@/locales/en.json';
import { useToastStore } from '@/stores/toast';
import { useCapabilitiesStore } from '@/stores/capabilities';
import { usePendingOpsStore } from '@/stores/pendingOps';
import { closeRowMenus, openRowMenu, pickMenuItem } from '../helpers/rowMenu';

type Body = Record<string, unknown>;

const calls: Array<{ method: string; url: string; body?: Body }> = [];
let listed: Body[] = [];
/** What a poll of each queued job answers. */
const jobs = new Map<number, Body>();
let nextOp = 40;
/** When set, every write is refused with it (or unanswered when `response` is absent). */
let refusal: unknown = null;

vi.mock('@/api/client', () => ({
  extractError: (_e: unknown, f: string) => f,
  api: {
    get: vi.fn(async (url: string) => {
      calls.push({ method: 'GET', url });
      if (url === '/admin/storages') return { data: [{ id: 2, name: 'Diyetlif-Bulut-Depolama' }] };
      if (url === '/files/manager/trash') {
        return { data: { entries: listed, total: listed.length, limit: 50, offset: 0 } };
      }
      if (url === '/admin/trash/empty') return { data: { running: false } };
      if (url === '/files/ops') return { data: { ops: [] } };
      const m = url.match(/^\/files\/ops\/(\d+)$/);
      if (m) return { data: jobs.get(Number(m[1])) ?? null };
      throw new Error(`unexpected GET ${url}`);
    }),
    post: vi.fn(async (url: string, body: Body) => {
      calls.push({ method: 'POST', url, body });
      if (refusal) throw refusal;
      if (url === '/files/manager/restore?queued=1') {
        const ids = body.node_ids as number[];
        const op = { id: ++nextOp, kind: 'restore', status: 'pending', total: ids.length, done: 0, storage_id: 2 };
        jobs.set(op.id, { ...op, status: 'running' });
        return { data: { ops: [op] } };
      }
      if (url === '/files/manager/restore') return { data: { ok: true } };
      throw new Error(`unexpected POST ${url}`);
    }),
    delete: vi.fn(async (url: string) => {
      calls.push({ method: 'DELETE', url });
      if (refusal) throw refusal;
      if (/^\/admin\/trash\/\d+\?queued=1$/.test(url)) {
        const op = { id: ++nextOp, kind: 'purge', status: 'pending', total: 1, done: 0, storage_id: 2 };
        jobs.set(op.id, { ...op, status: 'running' });
        return { data: { op } };
      }
      if (/^\/admin\/trash\/\d+$/.test(url)) return { data: { ok: true } };
      throw new Error(`unexpected DELETE ${url}`);
    }),
  },
}));

function entry(id: number, name: string): Body {
  return {
    id,
    storage_id: 2,
    storage_name: 'Diyetlif-Bulut-Depolama',
    path: `/${name}`,
    name,
    size: 0,
    deleted_at: '2026-09-18T14:54:41Z',
    ttl_days: 24,
  };
}

const mounted: VueWrapper[] = [];

async function mountTrash(queued: string[] | undefined) {
  const pinia = createPinia();
  setActivePinia(pinia);
  const caps = useCapabilitiesStore();
  caps.data = { ...caps.data, queued } as typeof caps.data;
  const i18n = createI18n({ legacy: false, locale: 'en', fallbackLocale: 'en', messages: { en } });
  const w = mount(Trash, { global: { plugins: [pinia, i18n] }, attachTo: document.body });
  mounted.push(w);
  await flushPromises();
  return w;
}

/** The job ends the way `status` says; the next poll sees it. */
async function jobEnds(id: number, status: 'ok' | 'failed', error = '') {
  const job = jobs.get(id)!;
  jobs.set(id, { ...job, status, done: status === 'ok' ? 1 : 0, failed: status === 'ok' ? 0 : 1, error });
  await usePendingOpsStore().poll();
  await flushPromises();
}

function toasts() {
  return useToastStore().toasts.map((t) => t.message);
}

function trashLoads() {
  return calls.filter((c) => c.method === 'GET' && c.url === '/files/manager/trash').length;
}

afterEach(() => {
  usePendingOpsStore().stop();
  mounted.splice(0).forEach((w) => w.unmount());
  closeRowMenus();
  calls.length = 0;
  jobs.clear();
  listed = [];
  refusal = null;
  vi.unstubAllGlobals();
});

describe('restore and purge on a server that queues them', () => {
  const QUEUES = ['rename', 'restore', 'purge'];

  it('restores a folder as a job, marks its row, and says when it is back', async () => {
    listed = [entry(7, 'Leon')];
    const w = await mountTrash(QUEUES);

    await openRowMenu(w, 'trash-actions-7');
    await pickMenuItem('trash-actions-7-restore');
    await flushPromises();
    expect(calls).toContainEqual({ method: 'POST', url: '/files/manager/restore?queued=1', body: { node_ids: [7] } });
    expect(w.find('[data-testid="trash-working-7"]').text()).toBe('Restoring…');
    expect(toasts(), 'nothing is announced before the job ends').toEqual([]);

    const loadsBefore = trashLoads();
    listed = [];
    await jobEnds(nextOp, 'ok');
    expect(toasts()).toContain('Leon restored');
    expect(trashLoads(), 'the list is read again when the job ends').toBeGreaterThan(loadsBefore);
    expect(w.find('[data-testid="trash-working-7"]').exists()).toBe(false);
  });

  it('purges a folder as a job', async () => {
    vi.stubGlobal('confirm', () => true);
    listed = [entry(8, 'Arsiv')];
    const w = await mountTrash(QUEUES);

    await openRowMenu(w, 'trash-actions-8');
    await pickMenuItem('trash-actions-8-purge');
    await flushPromises();
    expect(calls).toContainEqual({ method: 'DELETE', url: '/admin/trash/8?queued=1' });
    expect(w.find('[data-testid="trash-working-8"]').text()).toBe('Deleting permanently…');

    listed = [];
    await jobEnds(nextOp, 'ok');
    expect(toasts()).toContain('Arsiv permanently deleted');
  });

  it('takes one press at a time on a row that is being worked on', async () => {
    listed = [entry(7, 'Leon')];
    const w = await mountTrash(QUEUES);
    await openRowMenu(w, 'trash-actions-7');
    await pickMenuItem('trash-actions-7-restore');
    await flushPromises();

    // Every verb of the row is shut, so its one control is (core RowActions);
    // the row itself says what is happening to it.
    expect(w.find('[data-testid="trash-actions-7"]').attributes('disabled')).toBeDefined();
    expect(w.find('[data-testid="trash-working-7"]').exists()).toBe(true);
  });

  it('says why a restore did not happen when the place is taken', async () => {
    listed = [entry(7, 'Leon')];
    const w = await mountTrash(QUEUES);
    await openRowMenu(w, 'trash-actions-7');
    await pickMenuItem('trash-actions-7-restore');
    await flushPromises();

    await jobEnds(nextOp, 'failed', 'something already exists at this path: Leon');
    expect(toasts()).toContain(
      '“Leon” was not restored: something already has that name. Rename what is there, then restore again.',
    );
  });
});

describe('restore on a server that does not queue it', () => {
  it('is asked the old way', async () => {
    listed = [entry(7, 'Leon')];
    const w = await mountTrash(undefined);
    await openRowMenu(w, 'trash-actions-7');
    await pickMenuItem('trash-actions-7-restore');
    await flushPromises();
    expect(calls).toContainEqual({ method: 'POST', url: '/files/manager/restore', body: { node_id: 7 } });
    expect(toasts()).toContain('Leon restored');
  });

  it('never prints a request that got no answer as it came', async () => {
    listed = [entry(7, 'Leon')];
    refusal = Object.assign(new Error('timeout of 30000ms exceeded'), { name: 'AxiosError', code: 'ECONNABORTED' });
    const w = await mountTrash(undefined);
    await openRowMenu(w, 'trash-actions-7');
    await pickMenuItem('trash-actions-7-restore');
    await flushPromises();
    expect(toasts().join('\n')).not.toMatch(/AxiosError|timeout of/);
  });
});
