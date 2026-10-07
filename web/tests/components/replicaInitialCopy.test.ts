// The initial copy on the Replication page (#186).
//
// A storage linked to a replication target copies what it ALREADY holds to
// the target in the background. The pairing row says how far it has come -
// counting, "40 of 100 files done" with a bar, waiting (with the target's
// error, it goes on by itself), done - with what the numbers are made of, and
// "Run again". A failure's "Fix" names its storage now: several storages
// replicate, and the same path can fail on two of them.
//
// Red before: the page had no initial copy (the server made none), and Fix
// sent {path, op} alone.
import { describe, expect, it, vi } from 'vitest';
import { flushPromises, mount } from '@vue/test-utils';
import { createPinia, setActivePinia } from 'pinia';
import { createI18n } from 'vue-i18n';
import { createMemoryHistory, createRouter } from 'vue-router';

import ReplicaInitialCopy from '@/components/ReplicaInitialCopy.vue';
import Replica from '@/views/Replica.vue';
import en from '@/locales/en.json';
import tr from '@/locales/tr.json';
import type { ReplicaInitialCopy as Copy } from '@/api/types';
import { closeRowMenus, openRowMenu, pickMenuItem } from '../helpers/rowMenu';

const posts: Array<{ url: string; body: unknown }> = [];

const COPY: Copy = {
  storage_id: 7,
  target_id: 3,
  storage_name: 'arsiv',
  target_name: 'storage-box',
  phase: 'copying',
  counted: true,
  total: 100,
  done: 40,
  copied: 30,
  present: 6,
  excluded: 3,
  failed: 1,
  copied_bytes: 2048,
  started_unix: 1_790_000_000,
  updated_unix: 1_790_000_100,
  finished_unix: 0,
};

vi.mock('@/api/client', () => ({
  extractError: (_e: unknown, f: string) => f,
  api: {
    get: vi.fn(async (url: string) => {
      if (url === '/admin/replica/failures') {
        return {
          data: {
            items: [{ id: 1, storage_id: 7, path: '/a.txt', op: 'write', error_code: 'E', error_msg: 'x', attempts: 1, last_attempt_at: '2026-10-06T00:00:00Z', resolved_at: null }],
            total: 1,
          },
        };
      }
      if (url === '/admin/replica/initial-copies') return { data: { items: [COPY] } };
      if (url === '/admin/replica/links') {
        return { data: { items: [{ storage_id: 7, target_id: 3, folder: 'arsiv', storage_name: 'arsiv', target_name: 'storage-box', created_unix: 1 }] } };
      }
      if (url === '/admin/replica/rules') return { data: { items: [] } };
      if (url === '/admin/replica/report') return { status: 204, data: null };
      if (url === '/admin/replication-targets') return { data: [{ id: 3, name: 'storage-box', driver: 'smb', config: {}, mode: 'async', enabled: true }] };
      if (url === '/admin/storages') return { data: [{ id: 7, name: 'arsiv', driver: 'local', replica_target_id: 3 }] };
      if (url.includes('driver')) return { data: [] };
      return { data: {} };
    }),
    post: vi.fn(async (url: string, body?: unknown) => {
      posts.push({ url, body });
      if (url === '/admin/replica/fix-one') return { data: { ok: true, queued: true } };
      if (url.endsWith('/restart')) return { data: { ...COPY, phase: 'pending', done: 0 } };
      throw new Error(`unexpected POST ${url}`);
    }),
    put: vi.fn(),
    patch: vi.fn(),
    delete: vi.fn(),
  },
}));

function i18n(locale: 'en' | 'tr' = 'en') {
  return createI18n({ legacy: false, locale, fallbackLocale: 'en', messages: { en, tr } });
}

function row(copy: Partial<Copy>, locale: 'en' | 'tr' = 'en') {
  return mount(ReplicaInitialCopy, { props: { copy: { ...COPY, ...copy } }, global: { plugins: [i18n(locale)] } });
}

describe('the initial copy on a pairing row', () => {
  it('copying: how far, the bar, and what "done" is made of', () => {
    const w = row({});
    expect(w.get('[data-testid="replica-initial-summary-7"]').text()).toBe('40 of 100 files done');
    expect(w.get('[role="progressbar"]').attributes('aria-valuenow')).toBe('40');
    expect(w.get('[data-testid="replica-initial-copied-7"]').text()).toContain('30 copied');
    expect(w.get('[data-testid="replica-initial-present-7"]').text()).toBe('6 already on the target');
    expect(w.get('[data-testid="replica-initial-excluded-7"]').text()).toBe('3 left out by a rule');
    expect(w.get('[data-testid="replica-initial-failed-7"]').text()).toBe('1 failed - see “Failures”');
    expect(w.find('[data-testid="replica-initial-restart-7"]').exists()).toBe(false);
  });

  it('counting: no bar yet, the count so far', () => {
    const w = row({ phase: 'counting', counted: false, total: 1234, done: 0, copied: 0, present: 0, excluded: 0, failed: 0 });
    expect(w.get('[data-testid="replica-initial-summary-7"]').text()).toBe('Counting the files on the storage: 1,234 so far');
    expect(w.find('[role="progressbar"]').exists()).toBe(false);
  });

  it('waiting: the target\'s error, and that it goes on by itself', () => {
    const w = row({ phase: 'waiting', last_error: 'replication target unreachable: connection refused' });
    expect(w.get('[data-testid="replica-initial-error-7"]').text()).toContain('connection refused');
    expect(w.get('[data-testid="replica-initial-error-7"]').text()).toContain('goes on by itself');
    expect(w.find('[data-testid="replica-initial-restart-7"]').exists()).toBe(true);
  });

  it('done: the total, and Run again', async () => {
    const w = row({ phase: 'done', done: 100, finished_unix: 1_790_001_000 });
    expect(w.get('[data-testid="replica-initial-summary-7"]').text()).toBe('Initial copy done: 100 files');
    await w.get('[data-testid="replica-initial-restart-7"]').trigger('click');
    expect(w.emitted('restart')).toHaveLength(1);
  });

  it('speaks Turkish with its own letters', () => {
    const w = row({ phase: 'waiting', last_error: 'x' }, 'tr');
    expect(w.get('[data-testid="replica-initial-phase-7"]').text()).toBe('Bekliyor');
    expect(w.get('[data-testid="replica-initial-summary-7"]').text()).toBe('100 dosyanın 40 tanesi tamamlandı');
    expect(w.get('[data-testid="replica-initial-error-7"]').text()).toContain('Kopya kendiliğinden devam edecek');
    expect(w.get('[data-testid="replica-initial-restart-7"]').text()).toBe('Yeniden çalıştır');
  });
});

async function mountPage() {
  setActivePinia(createPinia());
  const blank = { template: '<div/>' };
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:p(.*)*', component: blank }] });
  const w = mount(Replica, { global: { plugins: [i18n(), router] }, attachTo: document.body });
  await flushPromises();
  return w;
}

describe('the Replication page', () => {
  it('shows each linked storage\'s initial copy on its pairing row', async () => {
    const w = await mountPage();
    const pair = w.get('[data-testid="replica-pair-row-7"]');
    expect(pair.find('[data-testid="replica-initial-7"]').exists(), 'no initial copy on the row').toBe(true);
    expect(pair.get('[data-testid="replica-initial-summary-7"]').text()).toBe('40 of 100 files done');
    // Every storage writes into a folder of its own on the target, and the
    // row says which.
    expect(pair.get('[data-testid="replica-pair-folder-7"]').text()).toBe('Folder on the target: arsiv/');
    w.unmount();
  });

  it('Fix names the failure\'s storage', async () => {
    posts.length = 0;
    const w = await mountPage();
    await w.findAll('button').find((b) => b.text().trim() === en.replica.tabs.failures)!.trigger('click');
    await flushPromises();
    await openRowMenu(w, 'replica-failure-actions-1');
    await pickMenuItem('replica-failure-actions-1-fix');
    await flushPromises();
    const sent = posts.find((p) => p.url === '/admin/replica/fix-one');
    expect(sent?.body).toEqual({ storage_id: 7, path: '/a.txt', op: 'write' });
    closeRowMenus();
    w.unmount();
  });
});
