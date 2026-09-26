// The admin Storages page (#57): the explorer's table, in the administrator's
// order, with the three ways to change it — Move up / Move down in a row's
// Actions menu, the arrow keys on a row's handle, and "Reset to default
// order" — each sending the WHOLE order to `PUT /api/admin/storages/order`.
// The drag itself is measured in a real browser (e2e 158), because happy-dom
// lays nothing out.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushPromises, mount, RouterLinkStub } from '@vue/test-utils';
import { createPinia, setActivePinia } from 'pinia';
import { createI18n } from 'vue-i18n';
import { routerKey } from 'vue-router';
import { DataTable } from '@brftech/filex-core';

import en from '@/locales/en.json';
import tr from '@/locales/tr.json';
import { closeRowMenus, menuEntries, openRowMenu, pickMenuItem } from '../helpers/rowMenu';

type Row = { id: number; name: string; driver: string; enabled: boolean; read_only: boolean; sort_order: number | null };

let server: Row[] = [];
const setOrder = vi.fn(async (ids: number[]) => {
  // What the server does: listed ids get 1..n, everything else NULL, and it
  // lists placed rows first.
  const pos = new Map(ids.map((id, i) => [id, i + 1]));
  server = server
    .map((s) => ({ ...s, sort_order: pos.get(s.id) ?? null }))
    .sort((a, b) => (a.sort_order ?? 1e9) - (b.sort_order ?? 1e9) || a.id - b.id);
});

vi.mock('@/api/storages', () => ({
  StoragesApi: {
    list: vi.fn(async () => server.map((s) => ({ ...s }))),
    setOrder: (ids: number[]) => setOrder(ids),
    syncNow: vi.fn(async () => ({ ok: true, status: 'started' })),
    remove: vi.fn(async () => undefined),
  },
}));

import Storages from '@/views/Storages.vue';

function mountPage(locale = 'en') {
  const i18n = createI18n({ legacy: false, locale, fallbackLocale: 'en', messages: { en, tr } });
  return mount(Storages, {
    global: {
      plugins: [i18n],
      stubs: { RouterLink: RouterLinkStub },
      provide: { [routerKey as symbol]: { push: vi.fn() } },
    },
    attachTo: document.body,
  });
}

/** The storage names, top to bottom, as the rows draw them. */
const names = (w: ReturnType<typeof mountPage>) =>
  w.findAll('[data-storage-row]').map((r) => r.findComponent(RouterLinkStub).text());

describe('admin Storages — the administrator order', () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    setOrder.mockClear();
    server = [
      { id: 1, name: 'Work', driver: 'local', enabled: true, read_only: false, sort_order: null },
      { id: 2, name: 'Photos', driver: 'local', enabled: true, read_only: false, sort_order: null },
      { id: 3, name: 'Archive', driver: 's3', enabled: true, read_only: true, sort_order: null },
    ];
  });
  afterEach(() => {
    closeRowMenus();
    document.body.innerHTML = '';
  });

  it('is THE table (DataTable) under its own id, with no sortable column to fight the order', async () => {
    const w = mountPage();
    await flushPromises();
    const table = w.findComponent(DataTable);
    expect(table.exists()).toBe(true);
    expect(table.props('tableId')).toBe('admin.storages');
    const cols = table.props('columns') as Array<{ sortable?: boolean }>;
    expect(cols.some((c) => c.sortable)).toBe(false);
    expect(names(w)).toEqual(['Work', 'Photos', 'Archive']);
    w.unmount();
  });

  it('Move down in a row menu sends the whole new order', async () => {
    const w = mountPage();
    await flushPromises();
    await openRowMenu(w, 'storage-actions-1');
    const labels = menuEntries().map((e) => e.label);
    expect(labels).toContain('Move up');
    expect(labels).toContain('Move down');
    expect(menuEntries().find((e) => e.label === 'Move up')?.disabled).toBe(true);
    await pickMenuItem('storage-actions-1-move-down');
    await flushPromises();
    expect(setOrder).toHaveBeenCalledWith([2, 1, 3]);
    expect(names(w)).toEqual(['Photos', 'Work', 'Archive']);
    w.unmount();
  });

  it('the arrow keys on a row handle move it', async () => {
    const w = mountPage();
    await flushPromises();
    await w.find('[data-testid="storage-order-handle-3"]').trigger('keydown', { key: 'ArrowUp' });
    await flushPromises();
    expect(setOrder).toHaveBeenCalledWith([1, 3, 2]);
    w.unmount();
  });

  it('Reset to default order is greyed with no order, and sends the empty order with one', async () => {
    const w = mountPage();
    await flushPromises();
    const reset = () => w.find('[data-testid="storages-order-reset"]');
    expect(reset().attributes('disabled')).toBeDefined();
    server[2].sort_order = 1;
    await w.find('button').trigger('click'); // Refresh
    await flushPromises();
    expect(reset().attributes('disabled')).toBeUndefined();
    await reset().trigger('click');
    await flushPromises();
    expect(setOrder).toHaveBeenCalledWith([]);
    w.unmount();
  });

  it('speaks Turkish with Turkish letters', async () => {
    const w = mountPage('tr');
    await flushPromises();
    expect(w.find('[data-testid="storages-order-reset"]').text()).toBe('Varsayılan sıraya döndür');
    await openRowMenu(w, 'storage-actions-2');
    const labels = menuEntries().map((e) => e.label);
    expect(labels).toContain('Yukarı taşı');
    expect(labels).toContain('Aşağı taşı');
    w.unmount();
  });
});
