// "Empty the trash" is an ops row (backend ops/trash_empty.go): the explorer's
// operations centre draws it as a kind of its own, lets an administrator stop
// it, and — like every row somebody cancels — shows a cancelled row as ended,
// never as "Queued".
import { describe, expect, it } from 'vitest';
import { mount } from '@vue/test-utils';
import { nextTick } from 'vue';

import { normalizeOp, type PendingOp } from '@brftech/filex-core/src/composables/usePendingOps';
import { useOperations } from '@brftech/filex-core/src/composables/useOperations';
import PendingOpsTray from '@brftech/filex-core/src/components/PendingOpsTray.vue';

function trashRow(status: string): PendingOp {
  return normalizeOp({ id: 7, kind: 'trash-empty', status, total: 61844, done: 120, storage_id: 0 });
}

async function centreRows(ops: PendingOp[], callerAdmin: boolean) {
  const center = useOperations();
  mount(PendingOpsTray, { props: { ops, locale: 'en', center, callerAdmin } });
  await nextTick();
  return center.active.value;
}

describe('the ops row of "empty the trash"', () => {
  it('a cancelled row is an ending, not "pending"', () => {
    // ⚠ `cancelled` fell through to 'pending': a job somebody cancelled sat
    // in the operations centre as "Queued" for as long as the list carried it.
    expect(normalizeOp({ id: 1, kind: 'plugin-action', status: 'cancelled' }).status).toBe('cancelled');
    expect(trashRow('cancelled').status).toBe('cancelled');
  });

  it('is drawn as a kind of its own, titled by its kind, with its counts', async () => {
    const [row] = await centreRows([trashRow('running')], true);
    expect(row.kind).toBe('trash');
    expect(row.name, 'it names no file; the kind is its title').toBe('');
    expect(row.doneCount).toBe(120);
    expect(row.totalCount).toBe(61844);
    expect(row.status).toBe('running');
  });

  it('an administrator may stop it; nobody else is offered a Cancel the server refuses', async () => {
    expect((await centreRows([trashRow('running')], true))[0].cancellable).toBe(true);
    expect((await centreRows([trashRow('running')], false))[0].cancellable).toBe(false);
    expect((await centreRows([trashRow('pending')], true))[0].cancellable, 'a queued one too').toBe(true);
  });

  it('a stopped row reads as cancelled, and cannot be cancelled again', async () => {
    const [row] = await centreRows([trashRow('cancelled')], true);
    expect(row.status).toBe('aborted');
    expect(row.cancellable).toBe(false);
  });
});
