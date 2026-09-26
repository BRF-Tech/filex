// A job over ONE folder says how far it has got, in items.
//
// ⚠ A queue job counts its sources, and a folder is one source: a move of a
// folder of 4,000 files read "0/1" and drew a 0% badge until the very end,
// minutes later on an object store. The server now reports the objects the
// storage driver has found and finished (`objects_total` / `objects_done`),
// and the operations centre draws those. The badge also follows a row's own
// percentage: a cross-storage move of one file moved its bar by bytes while
// the badge sat at 0%.
import { describe, expect, it, vi } from 'vitest';
import { flushPromises, mount } from '@vue/test-utils';
import { nextTick } from 'vue';
import { createPinia, setActivePinia } from 'pinia';
import { createI18n } from 'vue-i18n';

import { opPercent } from '@brftech/filex-core/src/lib/opProgress';
import { normalizeOp, type PendingOp } from '@brftech/filex-core/src/composables/usePendingOps';
import { useOperations } from '@brftech/filex-core/src/composables/useOperations';
import CorePendingOpsTray from '@brftech/filex-core/src/components/PendingOpsTray.vue';
import OperationsCenter from '@brftech/filex-core/src/components/OperationsCenter.vue';
import { normalizeOp as adminNormalizeOp } from '@/api/ops';
import en from '@/locales/en.json';

let listed: Record<string, unknown>[] = [];

vi.mock('@/api/client', () => ({
  extractError: (_e: unknown, f: string) => f,
  api: {
    get: vi.fn(async (url: string) => {
      if (url.startsWith('/files/ops')) return { data: { ops: listed } };
      return { data: {} };
    }),
    post: vi.fn(),
  },
}));

function folderMove(objectsDone: number, objectsTotal: number): PendingOp {
  return normalizeOp({
    id: 11, kind: 'move', status: 'running', total: 1, done: 0,
    objects_total: objectsTotal, objects_done: objectsDone,
    sources: ['main://Projeler'], dest: 'main://Arsiv', storage_id: 1,
  });
}

describe('opPercent with objects', () => {
  it('draws a single folder by its objects', () => {
    expect(opPercent({ status: 'running', progress_total: 1, progress_done: 0, objects_total: 200, objects_done: 50 })).toBe(25);
  });

  it('has no percentage while the objects are not counted yet', () => {
    expect(opPercent({ status: 'running', progress_total: 1, progress_done: 0, objects_total: 0, objects_done: 0 })).toBeNull();
  });

  it('still counts sources when there are several', () => {
    expect(opPercent({ status: 'running', progress_total: 4, progress_done: 1, objects_total: 1000, objects_done: 900 })).toBe(25);
  });

  it('both clients read the counters off the wire', () => {
    const wire = { id: 3, kind: 'delete', status: 'running', total: 1, done: 0, objects_total: 80, objects_done: 20 };
    expect(normalizeOp(wire).objects_total).toBe(80);
    expect(adminNormalizeOp(wire).objects_done).toBe(20);
  });
});

describe('the operations centre', () => {
  it('draws a one-folder job by its items, in the row and in the badge', async () => {
    const center = useOperations();
    mount(CorePendingOpsTray, { props: { ops: [folderMove(25, 100)], locale: 'en', center } });
    await nextTick();
    const [row] = center.active.value;
    expect(row.percent).toBe(25);
    expect(center.overallPercent.value, 'the badge read 0/1').toBe(25);

    const w = mount(OperationsCenter, { props: { center, locale: 'en' }, attachTo: document.body });
    await nextTick();
    await w.find('.fe-opc__badge').trigger('click');
    await nextTick();
    const text = document.body.textContent ?? '';
    expect(text).toContain('25 of 100 items');
    expect(text).not.toContain('0/1');
    w.unmount();
  });

  it('the badge follows a one-file transfer by its bytes', async () => {
    const center = useOperations();
    const crossMove = normalizeOp({
      id: 12, kind: 'move', status: 'running', total: 1, done: 0, bytes_total: 4000, bytes_done: 1000,
      sources: ['a://big.bin'], dest: 'b://', storage_id: 1,
    });
    mount(CorePendingOpsTray, { props: { ops: [crossMove], locale: 'en', center } });
    await nextTick();
    expect(center.overallPercent.value).toBe(25);
  });
});

describe('the admin panel tray', () => {
  it('says how many items a one-folder job has done', async () => {
    setActivePinia(createPinia());
    listed = [{ id: 11, kind: 'delete', status: 'running', total: 1, done: 0, objects_total: 100, objects_done: 25, sources: ['main://Eski'] }];
    const { default: AdminTray } = await import('@/components/PendingOpsTray.vue');
    const i18n = createI18n({ legacy: false, locale: 'en', fallbackLocale: 'en', messages: { en } });
    const w = mount(AdminTray, { global: { plugins: [i18n] }, attachTo: document.body });
    await flushPromises();
    await vi.dynamicImportSettled?.();
    await flushPromises();
    expect(document.body.textContent ?? '').toContain('25 of 100 items · 25%');
    w.unmount();
  });
});
