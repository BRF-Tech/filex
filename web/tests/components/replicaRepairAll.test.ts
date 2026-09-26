// "Fix all" on the Replica page holds its button while the request is out and
// says what it did.
//
// ⚠ The button had no busy state, and the list came back looking the same (a
// retry has not run yet when the request answers), which invited another
// press: each one queued the whole set again. The server now absorbs a retry
// that is already waiting (`already_queued`), and the page says so instead of
// "0 retries queued".
import { afterEach, describe, expect, it, vi } from 'vitest';
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils';
import { createPinia, setActivePinia } from 'pinia';
import { createI18n } from 'vue-i18n';
import { createMemoryHistory, createRouter } from 'vue-router';

import Replica from '@/views/Replica.vue';
import en from '@/locales/en.json';
import { useToastStore } from '@/stores/toast';

type Body = Record<string, unknown>;

let fixAllReply: () => Promise<{ data: Body }> = async () => ({ data: { queued: 1, already_queued: 0 } });
let fixOneReply: () => Promise<{ data: Body }> = async () => ({ data: { ok: true, queued: true } });

const FAILURE = {
  id: 1,
  path: '/a.txt',
  op: 'write',
  error_code: 'EIO',
  error: 'boom',
  attempts: 1,
  last_attempt_at: '2026-09-26T00:00:00Z',
  resolved_at: null,
};

vi.mock('@/api/client', () => ({
  extractError: (_e: unknown, f: string) => f,
  api: {
    get: vi.fn(async (url: string) => {
      if (url === '/admin/replica/failures') return { data: { items: [FAILURE], total: 1 } };
      if (url === '/admin/replica/failures/count') return { data: { count: 1 } };
      if (url === '/admin/replica/rules') return { data: { items: [] } };
      if (url === '/admin/replica/report') return { status: 204, data: null };
      if (url === '/admin/replication-targets') return { data: [] };
      if (url === '/admin/storages') return { data: [] };
      if (url.includes('driver')) return { data: [] };
      return { data: {} };
    }),
    post: vi.fn(async (url: string) => {
      if (url === '/admin/replica/fix') return fixAllReply();
      if (url === '/admin/replica/fix-one') return fixOneReply();
      throw new Error(`unexpected POST ${url}`);
    }),
    put: vi.fn(),
    patch: vi.fn(),
    delete: vi.fn(),
  },
}));

const mounted: VueWrapper[] = [];

async function mountPage() {
  setActivePinia(createPinia());
  const blank = { template: '<div/>' };
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:p(.*)*', component: blank }] });
  const i18n = createI18n({ legacy: false, locale: 'en', fallbackLocale: 'en', messages: { en } });
  const w = mount(Replica, { global: { plugins: [i18n, router] }, attachTo: document.body });
  mounted.push(w);
  await flushPromises();
  return w;
}

function fixAllButton(w: VueWrapper) {
  return w.findAll('button').find((b) => b.text().trim() === en.replica.failures.fixAll)!;
}

function toasts() {
  return useToastStore().toasts.map((t) => t.message);
}

afterEach(() => {
  mounted.splice(0).forEach((w) => w.unmount());
  document.body.innerHTML = '';
});

describe('Replica — Fix all', () => {
  it('holds its button while the request is out, then says what it queued', async () => {
    let answer: (v: { data: Body }) => void = () => {};
    fixAllReply = () => new Promise((r) => (answer = r));
    const w = await mountPage();

    await fixAllButton(w).trigger('click');
    await flushPromises();
    expect(fixAllButton(w).attributes('disabled'), 'a second press could go out').toBeDefined();

    answer({ data: { queued: 2, already_queued: 0 } });
    await flushPromises();
    expect(toasts()).toContain('2 retries queued');
    expect(fixAllButton(w).attributes('disabled')).toBeUndefined();
  });

  it('says the retries were already waiting, not "0 retries queued"', async () => {
    fixAllReply = async () => ({ data: { queued: 0, already_queued: 3 } });
    const w = await mountPage();

    await fixAllButton(w).trigger('click');
    await flushPromises();
    expect(toasts()).toContain(en.replica.failures.alreadyQueued.split(' | ')[1].replace('{n}', '3'));
    expect(toasts().join(' ')).not.toContain('0 retries queued');
  });
});
