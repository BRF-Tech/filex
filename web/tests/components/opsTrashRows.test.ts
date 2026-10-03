// A restore or a permanent delete in the operations centre says what it works
// on.
//
// ⚠ #61/#69 queue a restore and a purge by trash-entry id: the job has no
// destination and no source folder, the row's name came out empty, and the
// row read only its kind — "Restore" — with nothing to tell two of them, or
// what either was bringing back, apart.
import { describe, expect, it } from 'vitest';
import { mount } from '@vue/test-utils';
import { nextTick } from 'vue';

import { useOperations } from '@brftech/filex-core/src/composables/useOperations';
import OperationsCenter from '@brftech/filex-core/src/components/OperationsCenter.vue';
import PendingOpsTray from '@brftech/filex-core/src/components/PendingOpsTray.vue';
import { normalizeOp } from '@brftech/filex-core/src/composables/usePendingOps';

async function rowName(kind: string, sources: string[], locale: 'en' | 'tr') {
  const op = normalizeOp({ id: 9, kind, status: 'running', total: sources.length, done: 0, sources, dest: '', storage_id: 1 });
  const center = useOperations();
  mount(PendingOpsTray, { props: { ops: [op], locale, center } });
  await nextTick();
  const w = mount(OperationsCenter, { props: { center, locale }, attachTo: document.body });
  await nextTick();
  await w.find('.fe-opc__badge').trigger('click');
  return w.find('.fe-opc__name').text();
}

describe('a job on trash entries', () => {
  it('a restore says how many items it brings back', async () => {
    expect(await rowName('restore', ['11', '12', '13'], 'en')).toBe('3 items from the trash');
    expect(await rowName('restore', ['11', '12', '13'], 'tr')).toBe('Çöpten 3 öğe');
  });

  it('a permanent delete of one says so in the singular', async () => {
    expect(await rowName('purge', ['11'], 'en')).toBe('1 item from the trash');
  });
});
