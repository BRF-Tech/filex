// A queued rename and a queued restore in the operations centre.
//
// A kind the tray did not know was drawn as an app job ("App", with the
// puzzle-piece icon), so a folder being renamed would have read as an app
// running. And a failed rename or restore said only "Failed": the server's
// reason — the name is taken — is English, and was the admin's second line.
import { afterEach, describe, expect, it } from 'vitest';
import { mount } from '@vue/test-utils';
import { nextTick } from 'vue';

import { normalizeOp } from '@brftech/filex-core/src/composables/usePendingOps';
import { useOperations } from '@brftech/filex-core/src/composables/useOperations';
import PendingOpsTray from '@brftech/filex-core/src/components/PendingOpsTray.vue';
import OperationsCenter from '@brftech/filex-core/src/components/OperationsCenter.vue';

afterEach(() => {
  document.body.innerHTML = '';
});

async function centreWith(rows: Array<Record<string, unknown>>, locale: 'en' | 'tr' = 'en') {
  const center = useOperations();
  mount(PendingOpsTray, { props: { ops: rows.map((r) => normalizeOp(r)), locale, center } });
  await nextTick();
  const w = mount(OperationsCenter, { props: { center, locale }, attachTo: document.body });
  await nextTick();
  await w.find('.fe-opc__badge').trigger('click');
  return { center, w };
}

describe('a queued rename or restore', () => {
  it('is drawn as what it is, not as an app job', async () => {
    const { center, w } = await centreWith([
      { id: 1, kind: 'rename', status: 'running', total: 1, done: 0, sources: ['Leon'], dest: 'Leo', storage_id: 1 },
      { id: 2, kind: 'restore', status: 'running', total: 3, done: 1, sources: ['11', '12', '13'], storage_id: 1 },
    ]);
    expect(center.active.value.map((o) => o.kind).sort()).toEqual(['rename', 'restore']);
    const text = w.text();
    expect(text).toContain('Rename');
    expect(text).toContain('Restore');
    expect(text).not.toContain('App');
    w.unmount();
  });

  it('says in Turkish what it is', async () => {
    const { w } = await centreWith(
      [{ id: 1, kind: 'rename', status: 'running', total: 1, done: 0, sources: ['Leon'], dest: 'Leo', storage_id: 1 }],
      'tr',
    );
    expect(w.text()).toContain('Yeniden adlandırma');
    w.unmount();
  });

  it('says why it failed when the name is taken', async () => {
    const { center, w } = await centreWith([
      {
        id: 3, kind: 'rename', status: 'failed', total: 1, done: 0, failed: 1, sources: ['Leon'], dest: 'Leo',
        storage_id: 1, error: 'something with that name already exists here',
      },
      {
        id: 4, kind: 'restore', status: 'partial', total: 2, done: 1, failed: 1, sources: ['11', '12'],
        storage_id: 1, error: 'something already exists at this path: a.txt',
      },
    ]);
    const said = [...center.active.value, ...center.history.value].map((o) => o.error);
    expect(said).toEqual(expect.arrayContaining([
      'Something with that name is already there. Rename what is there, then try again.',
    ]));
    expect(said.every((e) => e === 'Something with that name is already there. Rename what is there, then try again.')).toBe(true);
    w.unmount();
  });
});
