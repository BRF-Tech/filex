// "Rebuild index" follows the rebuild to its end.
//
// ⚠ The rebuild runs in the background, and the page read its state once:
// "Rebuild started", then a Running badge that never went away and no word of
// the end. A second press answered 409 with the server's English, "rebuild
// already in progress". The page now looks again while the rebuild runs, keeps
// the button busy, and says when the new index is live.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils';
import { createPinia, setActivePinia } from 'pinia';
import { createI18n } from 'vue-i18n';

import SearchTest from '@/views/SearchTest.vue';
import en from '@/locales/en.json';
import { useToastStore } from '@/stores/toast';

type Body = Record<string, unknown>;

/** Successive answers to the stats; the last one repeats. */
let stats: Body[] = [];
let rebuildReply: () => Promise<{ data: Body }> = async () => ({ data: { ok: true } });
let rebuildCalls = 0;

vi.mock('@/api/client', () => ({
  extractError: (e: { response?: { data?: { error?: string } } }, f: string) => e?.response?.data?.error ?? f,
  api: {
    get: vi.fn(async (url: string) => {
      if (url === '/admin/search/stats') return { data: stats.length > 1 ? stats.shift() : stats[0] };
      throw new Error(`unexpected GET ${url}`);
    }),
    post: vi.fn(async (url: string) => {
      if (url === '/admin/search/rebuild') {
        rebuildCalls++;
        return rebuildReply();
      }
      throw new Error(`unexpected POST ${url}`);
    }),
  },
}));

function state(rebuilding: boolean): Body {
  return { document_count: 37, file_count: 28, folder_count: 9, index_size_bytes: 1024, last_built_at: null, rebuilding };
}

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
  const i18n = createI18n({ legacy: false, locale: 'en', fallbackLocale: 'en', messages: { en } });
  const w = mount(SearchTest, { global: { plugins: [i18n] } });
  mounted.push(w);
  await flushPromises();
  return w;
}

function rebuildButton(w: VueWrapper) {
  return w.findAll('button').find((b) => b.text().includes('Rebuild index'))!;
}

/** Every toast raised since the page was mounted, in order: a toast closes by
 *  itself after a few seconds, and these tests move the clock. */
const said: string[] = [];

function toasts() {
  return said;
}

beforeEach(() => {
  vi.useFakeTimers();
  rebuildCalls = 0;
  rebuildReply = async () => ({ data: { ok: true } });
});

afterEach(() => {
  mounted.splice(0).forEach((w) => w.unmount());
  said.length = 0;
  vi.useRealTimers();
});

describe('Rebuild index', () => {
  it('follows the rebuild while it runs and says when the new index is live', async () => {
    stats = [state(false)];
    const w = await mountPage();

    stats = [state(true), state(true), state(false)];
    await rebuildButton(w).trigger('click');
    await flushPromises();
    expect(toasts()).toEqual(['Rebuild started']);
    expect(rebuildButton(w).attributes('disabled'), 'no second press while it runs').toBeDefined();

    await vi.advanceTimersByTimeAsync(2000);
    await flushPromises();
    expect(toasts(), 'not done yet').toEqual(['Rebuild started']);

    await vi.advanceTimersByTimeAsync(2000);
    await flushPromises();
    expect(toasts()).toEqual(['Rebuild started', 'The rebuilt index is live']);
    expect(rebuildButton(w).attributes('disabled')).toBeUndefined();
  });

  it('says a rebuild already running in its own words, and follows that one', async () => {
    stats = [state(false)];
    const w = await mountPage();
    rebuildReply = async () => {
      throw { response: { status: 409, data: { error: 'rebuild already in progress' } } };
    };
    stats = [state(true), state(false)];
    await rebuildButton(w).trigger('click');
    await flushPromises();
    expect(toasts()).toEqual(['A rebuild is already running; this page follows it.']);

    await vi.advanceTimersByTimeAsync(2000);
    await flushPromises();
    expect(toasts()).toContain('The rebuilt index is live');
  });

  it('picks up a rebuild that was already running when the page opened', async () => {
    stats = [state(true), state(false)];
    const w = await mountPage();
    expect(rebuildButton(w).attributes('disabled')).toBeDefined();

    await vi.advanceTimersByTimeAsync(2000);
    await flushPromises();
    expect(toasts()).toEqual(['The rebuilt index is live']);
    expect(rebuildCalls).toBe(0);
  });
});
