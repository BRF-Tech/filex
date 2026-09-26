// The corner badge says what the rows say.
//
// ⚠ useOperations' aggregate read a row's done/total counts before its
// percent. The queue counts what was SELECTED, so a move, copy or delete of one
// folder is "0 of 1" until it ends — minutes on an object store, where every
// object inside is its own request. The row drew a moving indicator (opProgress
// gives it no percent), and the badge beside it read "1 0%" over an empty ring
// for the whole run: the same "nothing is happening" the archive job showed.
//
// ⚠ An upload whose bytes are all in filex is not done either: the server then
// writes it to the storage, again minutes on a slow uplink. The row and the
// badge read 100% for all of it.
import { afterEach, describe, expect, it } from 'vitest';
import { mount } from '@vue/test-utils';
import { nextTick } from 'vue';

import { normalizeOp } from '@brftech/filex-core/src/composables/usePendingOps';
import { useOperations } from '@brftech/filex-core/src/composables/useOperations';
import type { UploadJob } from '@brftech/filex-core/src/composables/useUploadChunked';
import PendingOpsTray from '@brftech/filex-core/src/components/PendingOpsTray.vue';
import UploadProgress from '@brftech/filex-core/src/components/UploadProgress.vue';
import OperationsCenter from '@brftech/filex-core/src/components/OperationsCenter.vue';

afterEach(() => {
  document.body.innerHTML = '';
});

function queued(total: number, done: number) {
  return normalizeOp({
    id: 7, kind: 'delete', status: 'running', total, done,
    sources: ['Kayıtlar/2026'], storage_id: 1,
  });
}

async function openCenter(center: ReturnType<typeof useOperations>, locale: 'en' | 'tr' = 'en') {
  const w = mount(OperationsCenter, { props: { center, locale }, attachTo: document.body });
  await nextTick();
  return w;
}

describe('the badge over a queued job', () => {
  it('spins for a job of one folder instead of reading 0%', async () => {
    const center = useOperations();
    mount(PendingOpsTray, { props: { ops: [queued(1, 0)], locale: 'en', center } });
    await nextTick();
    expect(center.overallPercent.value, 'no honest number for one source').toBeNull();

    const w = await openCenter(center);
    expect(w.find('.fe-opc__pct').exists(), 'no percentage beside the count').toBe(false);
    expect(w.find('.fe-opc__ring--spin').exists(), 'the ring moves').toBe(true);

    await w.find('.fe-opc__badge').trigger('click');
    expect(w.find('.fe-opc__state').text(), 'the row does not read "0/1" either').not.toContain('0/1');
    w.unmount();
  });

  it('still counts a job of several sources', async () => {
    const center = useOperations();
    mount(PendingOpsTray, { props: { ops: [queued(4, 1)], locale: 'en', center } });
    await nextTick();
    expect(center.overallPercent.value).toBe(25);
  });
});

describe('an upload being written to the storage', () => {
  function job(status: UploadJob['status']): UploadJob {
    return {
      id: 'u1', file: new File(['x'], 'rapor.pdf'), path: 'main://', totalBytes: 100 << 20,
      uploadedBytes: 100 << 20, percent: 100, status, cancel() {},
    };
  }

  for (const status of ['committing', 'transferring'] as const) {
    it(`says so while ${status}, instead of reading 100%`, async () => {
      const center = useOperations();
      mount(UploadProgress, { props: { jobs: [job(status)], locale: 'tr', center } });
      await nextTick();
      const [row] = center.active.value;
      expect(row.percent, 'no percentage while the server writes it').toBeNull();
      expect(row.message).toMatch(/Depoya kaydediliyor/);
      expect(center.overallPercent.value).toBeNull();

      const w = await openCenter(center, 'tr');
      expect(w.find('.fe-opc__pct').exists()).toBe(false);
      await w.find('.fe-opc__badge').trigger('click');
      expect(w.find('.fe-opc__state').text()).toMatch(/Depoya kaydediliyor/);
      w.unmount();
    });
  }

  it('still shows the bytes while they are being sent', async () => {
    const center = useOperations();
    mount(UploadProgress, {
      props: { jobs: [{ ...job('uploading'), uploadedBytes: 25 << 20, percent: 25 }], locale: 'en', center },
    });
    await nextTick();
    expect(center.active.value[0].percent).toBe(25);
    expect(center.overallPercent.value).toBe(25);
  });
});
