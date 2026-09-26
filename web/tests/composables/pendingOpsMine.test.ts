// The explorer says "Renamed", "N items restored" — and reads the listing
// again — for the jobs THIS tab queued, not for everybody's.
//
// ⚠ #58 lets an administrator see every account's queued jobs, and #61 made a
// folder rename and a restore jobs of the queue: an admin's explorer then said
// "Renamed" and "3 items restored", and read its listing again, for every
// colleague's job that ended. `onSettled` is now for the jobs registered here;
// the operations centre still shows everybody's while they run.
import { afterEach, describe, expect, it, vi } from 'vitest';
import { defineComponent, h } from 'vue';
import { mount, type VueWrapper } from '@vue/test-utils';

import { usePendingOps, type PendingOp } from '@brftech/filex-core/src/composables/usePendingOps';

const mounted: VueWrapper[] = [];
afterEach(() => {
  mounted.splice(0).forEach((w) => w.unmount());
  vi.useRealTimers();
});

function rig(polls: Array<Array<Record<string, unknown>>>) {
  const settled: PendingOp[] = [];
  const api = {
    endpoints: { opsList: '/api/files/ops' },
    withScreenLang: (u: string) => u,
    jsonFetch: vi.fn(async () => ({ ops: polls.length > 1 ? polls.shift()! : polls[0] })),
  };
  let ops!: ReturnType<typeof usePendingOps>;
  const C = defineComponent({
    setup() {
      ops = usePendingOps({ apiBase: '' } as never, api as never, { onSettled: (op) => settled.push(op) });
      return () => h('div');
    },
  });
  mounted.push(mount(C));
  return { settled, ops: () => ops };
}

const row = (id: number, status: string, kind = 'rename') => ({ id, kind, status, total: 1, done: status === 'ok' ? 1 : 0 });

describe('a job that ends', () => {
  it('is announced when this tab queued it, not when a colleague did', async () => {
    vi.useFakeTimers();
    const { settled, ops } = rig([
      [row(1, 'running'), row(2, 'running')],
      [row(1, 'ok'), row(2, 'ok', 'restore')],
    ]);
    ops().register(row(1, 'pending'));
    await vi.advanceTimersByTimeAsync(2100);
    expect(settled.map((o) => o.id), 'a colleague’s job was announced here').toEqual([1]);
  });

  it('is announced even when it ended before the first look', async () => {
    vi.useFakeTimers();
    const { settled, ops } = rig([[row(5, 'ok')]]);
    ops().register(row(5, 'pending'));
    await vi.advanceTimersByTimeAsync(10);
    expect(settled.map((o) => o.id), 'this tab’s own quick job went unsaid').toEqual([5]);
  });
});
