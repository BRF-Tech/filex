// The admin Trash page's "Empty trash" says what it is doing.
//
// ⚠⚠ It said nothing. The page closed the dialog, sent one request and waited
// on it; a trash of 61,844 files took the server the better part of an hour to
// empty, so the request died — at thirty seconds in this page's own HTTP
// client, at sixty in nginx — and the admin saw no change, no progress and at
// best an English timeout toast. They pressed the button again, twice. The
// endpoint now answers within seconds: the final count when the purge is done,
// or its progress while it goes on in the background. These tests hold the
// page to showing both, through the real api module with only HTTP faked.
import { afterEach, describe, expect, it, vi } from 'vitest';
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils';
import { createPinia, setActivePinia } from 'pinia';
import { createI18n } from 'vue-i18n';

import Trash from '@/views/Trash.vue';
import en from '@/locales/en.json';
import { useToastStore } from '@/stores/toast';

type Body = Record<string, unknown>;

const posts: Array<{ url: string; body: Body }> = [];
let listed: Body[] = [];
/** Successive answers to GET /admin/trash/empty; the last one repeats. */
let statuses: Body[] = [];
let postReply: () => Promise<{ data: Body }> = async () => ({ data: { ok: true, running: false } });

vi.mock('@/api/client', () => ({
  extractError: (_e: unknown, f: string) => f,
  api: {
    get: vi.fn(async (url: string) => {
      if (url === '/admin/storages') return { data: [{ id: 2, name: 'Diyetlif-Bulut-Depolama' }] };
      if (url === '/files/manager/trash') {
        return { data: { entries: listed, total: listed.length, limit: 50, offset: 0 } };
      }
      if (url === '/admin/trash/empty') {
        return { data: (statuses.length > 1 ? statuses.shift() : statuses[0]) ?? { running: false } };
      }
      throw new Error(`unexpected GET ${url}`);
    }),
    post: vi.fn(async (url: string, body: Body) => {
      posts.push({ url, body });
      return postReply();
    }),
    delete: vi.fn(),
  },
}));

function entry(id: number): Body {
  return {
    id,
    storage_id: 2,
    storage_name: 'Diyetlif-Bulut-Depolama',
    path: `/Kopya ${id}.pdf`,
    name: `Kopya ${id}.pdf`,
    size: 1024,
    deleted_at: '2026-09-18T14:54:41Z',
    ttl_days: 24,
  };
}

/** Every run the server reports has a start; `{running: false}` alone is "no run known". */
const STARTED = '2026-09-24T10:37:07Z';

const mounted: VueWrapper[] = [];

async function mountTrash() {
  const pinia = createPinia();
  setActivePinia(pinia);
  const i18n = createI18n({ legacy: false, locale: 'en', fallbackLocale: 'en', messages: { en } });
  const w = mount(Trash, { global: { plugins: [pinia, i18n] } });
  mounted.push(w);
  await flushPromises();
  return w;
}

async function confirmEmpty(w: VueWrapper) {
  await w.get('[data-testid="trash-empty-open"]').trigger('click');
  await w.get('[data-testid="trash-empty-confirm"]').trigger('click');
  await flushPromises();
}

/** What the request body is on the wire: undefined fields are not sent. */
function wire(body: Body) {
  return JSON.parse(JSON.stringify(body));
}

function toasts() {
  return useToastStore().toasts.map((t) => t.message);
}

async function poll() {
  await vi.advanceTimersByTimeAsync(2000);
  await flushPromises();
}

afterEach(() => {
  while (mounted.length) mounted.pop()!.unmount();
  vi.useRealTimers();
  posts.length = 0;
  statuses = [];
  listed = [];
});

describe('Trash — empty trash', () => {
  it('a trash that empties within the wait: one press, the final count, the list reloaded', async () => {
    listed = [entry(1), entry(2), entry(3)];
    postReply = async () => {
      listed = [];
      return {
        data: { ok: true, running: false, total: 3, scanned: 3, purged: 3, failed: 0, bytes: 3072, started_at: STARTED },
      };
    };
    const w = await mountTrash();
    await confirmEmpty(w);

    expect(posts.map((p) => p.url)).toEqual(['/admin/trash/empty']);
    expect(toasts()).toContain('3 items purged');
    expect(w.text()).toContain('Trash is empty');
    expect(w.find('[data-testid="trash-emptying"]').exists()).toBe(false);
  });

  it('a trash too large for the wait: its progress is shown and followed until the run ends', async () => {
    vi.useFakeTimers();
    listed = [entry(1)];
    const w = await mountTrash();
    postReply = async () => ({
      data: { ok: true, running: true, total: 61844, scanned: 120, purged: 120, failed: 0, bytes: 122880 },
    });
    statuses = [
      { ok: true, running: true, total: 61844, scanned: 30922, purged: 30920, failed: 2, bytes: 34_000_000_000 },
      {
        ok: true, running: false, total: 61844, scanned: 61844, purged: 61842, failed: 2,
        bytes: 69_000_000_000, started_at: STARTED, finished_at: '2026-09-24T11:40:00Z',
      },
    ];
    await confirmEmpty(w);

    const progress = w.get('[data-testid="trash-emptying"]');
    expect(progress.attributes('role')).toBe('status');
    expect(progress.text()).toContain('120 of 61,844');
    expect(w.get('[data-testid="trash-empty-open"]').attributes('disabled'),
      'a second press while it runs is exactly what went wrong').toBeDefined();
    expect(toasts(), 'nothing is announced as done while it is not').toEqual([]);

    await poll();
    expect(w.get('[data-testid="trash-emptying"]').text()).toContain('30,922 of 61,844');

    listed = [];
    await poll();
    expect(w.find('[data-testid="trash-emptying"]').exists()).toBe(false);
    expect(toasts()).toContain('61,842 items purged');
    expect(toasts().some((m) => m.startsWith('2 items could not be purged'))).toBe(true);
    expect(w.text()).toContain('Trash is empty');
  });

  it('a days box typed in and cleared means any age, and sends no day count at all', async () => {
    listed = [entry(1)];
    postReply = async () => ({ data: { ok: true, running: false, total: 1, purged: 1, started_at: STARTED } });
    const w = await mountTrash();
    await w.get('select').setValue('2');
    await w.get('[data-testid="trash-empty-open"]').trigger('click');
    const days = w.get('[data-testid="trash-empty-days"]');
    await days.setValue('5');
    await days.setValue('');
    await w.get('[data-testid="trash-empty-confirm"]').trigger('click');
    await flushPromises();

    // ⚠ The cleared box used to go out as "older_than_days": "", a value the
    // server could not read — and it then dropped the storage_id beside it.
    expect(posts.map((p) => wire(p.body))).toEqual([{ storage_id: 2 }]);
  });

  it('a day count that is not a whole number stops the purge instead of widening it', async () => {
    listed = [entry(1)];
    const w = await mountTrash();
    await w.get('[data-testid="trash-empty-open"]').trigger('click');
    await w.get('[data-testid="trash-empty-days"]').setValue('1.5');

    const confirm = w.get('[data-testid="trash-empty-confirm"]');
    expect(confirm.attributes('disabled')).toBeDefined();
    await confirm.trigger('click');
    await flushPromises();
    expect(posts).toEqual([]);
    expect(w.text()).toContain('A whole number of days');
  });

  it('a run already under way when the page opens is shown, and the button waits for it', async () => {
    vi.useFakeTimers();
    listed = [entry(1)];
    statuses = [{ ok: true, running: true, total: 10, scanned: 4, purged: 4, failed: 0, bytes: 4096 }];
    const w = await mountTrash();

    expect(w.get('[data-testid="trash-emptying"]').text()).toContain('4 of 10');
    expect(w.get('[data-testid="trash-empty-open"]').attributes('disabled')).toBeDefined();
  });

  it('a run the server stops knowing (it restarted) reloads the list and claims nothing', async () => {
    vi.useFakeTimers();
    listed = [entry(1), entry(2)];
    statuses = [{ ok: true, running: true, total: 2, scanned: 1, purged: 1, failed: 0, bytes: 1024 }];
    const w = await mountTrash();
    expect(w.find('[data-testid="trash-emptying"]').exists()).toBe(true);

    listed = [entry(2)];
    statuses = [{ running: false }];
    await poll();
    expect(w.find('[data-testid="trash-emptying"]').exists()).toBe(false);
    expect(toasts(), 'no "0 items purged" for a run nobody can account for').toEqual([]);
    expect(w.text()).toContain('Kopya 2.pdf');
  });

  it('a run that finished before the page opened is not announced again', async () => {
    listed = [];
    statuses = [{ ok: true, running: false, total: 5, purged: 5, started_at: STARTED, finished_at: '2026-09-24T09:00:00Z' }];
    const w = await mountTrash();

    expect(w.find('[data-testid="trash-emptying"]').exists()).toBe(false);
    expect(toasts()).toEqual([]);
  });

  it('a second press from another tab is told the trash is already being emptied, and shown the run', async () => {
    vi.useFakeTimers();
    listed = [entry(1)];
    const w = await mountTrash();
    postReply = async () => {
      throw {
        response: {
          status: 409,
          data: {
            error: 'the trash is already being emptied',
            code: 'BUSY',
            job: { running: true, total: 9, scanned: 3, purged: 3, failed: 0, bytes: 0 },
          },
        },
      };
    };
    statuses = [{ ok: true, running: true, total: 9, scanned: 3, purged: 3, failed: 0, bytes: 0 }];
    await confirmEmpty(w);

    expect(toasts()).toContain('The trash is already being emptied.');
    expect(w.get('[data-testid="trash-emptying"]').text()).toContain('3 of 9');
  });
});
