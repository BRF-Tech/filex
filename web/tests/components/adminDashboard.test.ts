// The admin dashboard says one thing about syncing, not two.
//
// ⚠⚠ Found by the v0.41.0 screenshot pass (2026-09-14): the landing page read
// "No sync runs recorded" in its Recent syncs card while a storage card beside
// it read "Last sync: 11 seconds ago", under a breadcrumb reading
// "Dashboard › Dashboard". Each half was reading something real — just not the
// same something:
//
//   • the storage card reads the storages list, whose `last_sync_at` is the
//     newest run's start;
//   • the Recent syncs card read `recent_syncs` off the DASHBOARD payload, a
//     field the handler has never sent (handlers/dashboard.go → Response), so
//     it was empty on every install that has ever synced;
//   • the run list it should have read ignored the page's `page_size` / `state`
//     / `page` (the handler reads `limit` / `status` / `offset`) and carries no
//     storage names;
//   • and the Recent activity card beside it read `at` from rows that carry
//     `created_at`, so every time in it (and on the Audit page) printed "—".
//
// These go through the REAL api modules with only the HTTP layer faked, because
// every one of those bugs lived in a mapping between the wire and the view.
import { describe, expect, it, vi, beforeEach } from 'vitest';
import { flushPromises, mount } from '@vue/test-utils';
import { createPinia, setActivePinia } from 'pinia';
import { createI18n } from 'vue-i18n';
import { createMemoryHistory, createRouter } from 'vue-router';

import Dashboard from '@/views/Dashboard.vue';
import Breadcrumbs from '@/components/Breadcrumbs.vue';
import en from '@/locales/en.json';
import tr from '@/locales/tr.json';

const gets: Array<{ url: string; params?: Record<string, unknown> }> = [];
const NOW = Date.now();
const iso = (msAgo: number) => new Date(NOW - msAgo).toISOString();

vi.mock('@/api/client', () => ({
  extractError: (_e: unknown, f: string) => f,
  api: {
    get: vi.fn(async (url: string, cfg?: { params?: Record<string, unknown> }) => {
      gets.push({ url, params: cfg?.params });
      if (url === '/admin/dashboard') {
        return {
          data: {
            storages: [{ id: 8, name: 'thumbfix', driver: 'local', total_files: 18, total_bytes: 1000, last_sync_at: iso(11_000), state: 'ok' }],
            total_users: 4,
            queue_depth: 0,
            recent_activity: [
              { id: 141, user_id: 1, action: 'user.delete', target_type: 'user', target_id: '14', created_at: iso(120_000), target_name: 'gone@example.com' },
              { id: 142, user_id: 1, action: 'app-plugins.create', target_type: 'app-plugins', target_id: '', created_at: iso(130_000) },
            ],
          },
        };
      }
      if (url === '/admin/storages') {
        return {
          data: [{ id: 8, name: 'thumbfix', driver: 'local', enabled: true, last_sync_at: iso(11_000), last_sync_state: 'ok', file_count: 12, total_bytes: 80_000 }],
        };
      }
      if (url === '/admin/sync-runs') {
        return {
          data: {
            entries: [
              { id: 149, storage_id: 8, started_at: iso(11_000), finished_at: iso(10_000), seen_count: 19, added: 1, updated: 0, deleted: 0, status: 'ok' },
            ],
            total: 149,
            limit: Number(cfg?.params?.limit ?? 50),
            offset: 0,
          },
        };
      }
      throw new Error(`unexpected GET ${url}`);
    }),
  },
}));

function i18n() {
  return createI18n({ legacy: false, locale: 'en', fallbackLocale: 'en', messages: { en } });
}

async function router(at: string) {
  const Blank = { template: '<div />' };
  const r = createRouter({
    history: createMemoryHistory('/admin/'),
    routes: [
      { path: '/dashboard', name: 'dashboard', component: Blank, meta: { breadcrumb: 'nav.dashboard' } },
      { path: '/storages', name: 'storages', component: Blank, meta: { breadcrumb: 'nav.storages' } },
      { path: '/storages/:id', name: 'storages.edit', component: Blank, meta: { parent: 'storages', breadcrumb: 'storages.editTitle' } },
      { path: '/audit', name: 'audit', component: Blank },
      { path: '/sync', name: 'sync', component: Blank, meta: { parent: 'dashboard', breadcrumb: 'nav.sync' } },
      { path: '/storages/new', name: 'storages.new', component: Blank },
    ],
  });
  await r.push(at);
  await r.isReady();
  return r;
}

beforeEach(() => {
  gets.length = 0;
  setActivePinia(createPinia());
});

describe('admin dashboard', () => {
  it('lists the runs that exist beside the storage card that timed the last one', async () => {
    const w = mount(Dashboard, { global: { plugins: [createPinia(), await router('/dashboard'), i18n()] } });
    await flushPromises();
    await flushPromises();

    const text = w.text();
    expect(text).toContain('Last sync:');
    expect(text).not.toContain('No sync runs recorded');
    const recent = w.find('[data-testid="dashboard-recent-syncs"]');
    expect(recent.exists(), 'the Recent syncs card is empty again').toBe(true);
    // A name, not an empty cell: the run row carries only storage_id.
    expect(recent.text()).toContain('thumbfix');
  });

  it('asks the run list for a handful, in the parameters the handler reads', async () => {
    mount(Dashboard, { global: { plugins: [createPinia(), await router('/dashboard'), i18n()] } });
    await flushPromises();
    const call = gets.find((g) => g.url === '/admin/sync-runs');
    expect(call?.params).toMatchObject({ limit: 5 });
    expect(call?.params).not.toHaveProperty('page_size');
  });

  // The storage card read "📁 · local · 78.6 KB · 12" — a bare number after a
  // size, with nothing to say it counts files (v0.41.0 screenshot).
  it('says what the storage card counts', async () => {
    const w = mount(Dashboard, { global: { plugins: [createPinia(), await router('/dashboard'), i18n()] } });
    await flushPromises();
    await flushPromises();
    const card = w.findAll('p').find((p) => p.text().includes('12 files'));
    expect(card, 'no storage line').toBeTruthy();
    expect(card!.text()).toMatch(/· 12 files$/);
    // The driver by NAME, in the same tags every storage list draws
    // (StorageTags, QA #34) — it printed the raw id ("local") behind an emoji.
    const tags = card!.find('[data-testid="storage-tags"]');
    expect(tags.exists(), 'the storage line names its driver with StorageTags').toBe(true);
    expect(tags.text()).not.toBe('local');
    expect(tags.find('[data-testid="storage-tag-driver"]').attributes('title')).toBe('local');
  });

  it('prints a time in Recent activity, not a dash', async () => {
    const w = mount(Dashboard, { global: { plugins: [createPinia(), await router('/dashboard'), i18n()] } });
    await flushPromises();
    await flushPromises();
    // ⚠ `.fe-list__row`, not `li` and not `tbody tr`: Recent activity was a
    // `<ul class="divide-y divide-zinc-200 …">`, then the imitation admin
    // table's `<tr>`, and is DataTable now — the explorer's own table, whose
    // row is `.fe-list__row` (role=row) everywhere in the product.
    const activity = w.findAll('.fe-list__row').find((tr) => tr.text().includes('User: deleted'));
    expect(activity, 'no activity row').toBeTruthy();
    expect(activity!.text()).toMatch(/minutes? ago/);
    // The wire name is what the row used to print; it stays in the title only.
    expect(activity!.text()).not.toContain('user.delete');
  });
});

describe('SyncApi speaks the handler’s query', () => {
  it('maps page/page_size/state to offset/limit/status', async () => {
    const { SyncApi } = await import('@/api/sync');
    await SyncApi.list({ page: 3, page_size: 50, state: 'error', storage_id: 8 });
    expect(gets.at(-1)?.params).toEqual({ storage_id: 8, status: 'failed', limit: 50, offset: 100 });
  });
});

describe('breadcrumbs', () => {
  async function crumbs(at: string) {
    const w = mount(Breadcrumbs, { global: { plugins: [await router(at), i18n()] } });
    await flushPromises();
    const nav = w.find('nav');
    return nav.exists() ? nav.findAll('a, span').map((e) => e.text().trim()).filter(Boolean) : [];
  }

  it('the dashboard does not name itself twice', async () => {
    expect(await crumbs('/dashboard')).toEqual([]);
  });

  it('a page under the dashboard does not repeat it either', async () => {
    expect(await crumbs('/sync')).toEqual(['Dashboard', 'Sync runs']);
  });

  it('every other trail is unchanged', async () => {
    expect(await crumbs('/storages')).toEqual(['Dashboard', 'Storages']);
    expect(await crumbs('/storages/8')).toEqual(['Dashboard', 'Storages', 'Edit storage']);
  });
});

// ── release-candidate sweep, 2026-09-21 ──────────────────────────────────

describe('admin dashboard says things in words', () => {
  function trI18n() {
    return createI18n({ legacy: false, locale: 'tr', fallbackLocale: 'en', messages: { en, tr } });
  }

  // QA #29: "Kullanıcı: silindi — Kullanıcı" said the kind of thing and not
  // the thing; "app plugins: oluşturuldu" was a resource the catalogue lacked.
  it('names the thing a row is about, and every resource in words', async () => {
    const w = mount(Dashboard, { global: { plugins: [createPinia(), await router('/dashboard'), trI18n()] } });
    await flushPromises();
    await flushPromises();
    const targets = w.findAll('[data-testid="dashboard-activity-target"]').map((x) => x.text());
    expect(targets).toContain('Kullanıcı “gone@example.com”');
    const actions = w.findAll('[data-testid="dashboard-activity-action"]').map((x) => x.text());
    expect(actions).toContain('Uygulama: oluşturuldu');
    expect(actions.join(' ')).not.toContain('app plugins');
  });

  // The storage card's badge and the Recent syncs chip printed the wire value
  // ("ok") in the Turkish panel; "Last sync" glued its colon on in the
  // template, which a French space before it could never survive.
  it('says a sync state in words and keeps the colon in the message', async () => {
    const w = mount(Dashboard, { global: { plugins: [createPinia(), await router('/dashboard'), trI18n()] } });
    await flushPromises();
    await flushPromises();
    expect(w.find('[data-testid="dashboard-storage-state"]').text()).toBe('Tamam');
    const chips = w.findAll('[data-testid="dashboard-sync-state"]').map((x) => x.text());
    expect(chips.length).toBeGreaterThan(0);
    expect(chips.every((c) => c === 'Tamam'), chips.join(',')).toBe(true);
    expect(w.text()).toMatch(/Son senkron: \d+ saniye önce/);
  });
});
