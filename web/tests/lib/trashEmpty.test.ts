// "Empty trash" in the explorer's trash view, followed to the end.
//
// ⚠⚠ The view read any 2xx from POST /api/admin/trash/empty as "emptied" and
// anything else as a failure named by its status code. For a large trash the
// old endpoint never answered — nginx gave up with a 504 at sixty seconds and
// the view said "504". The endpoint now answers within seconds: 200 with the
// final counts, or 202 `{running: true, …}` while the purge goes on in the
// background, reported by GET on the same path until it ends. So a 2xx is no
// longer the end; a status whose `running` is false is.
import { describe, expect, it, vi } from 'vitest';
import { emptyTrashAndFollow, TrashEmptyBusy } from '@brftech/filex-core/src/lib/trashEmpty';

function reply(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
}

const noWait = () => Promise.resolve();

describe('emptyTrashAndFollow', () => {
  it('a purge done within the wait is the end: nothing is asked again', async () => {
    const status = vi.fn();
    const end = await emptyTrashAndFollow({
      start: async () => reply(200, { ok: true, running: false, total: 3, purged: 3, started_at: 'x' }),
      status,
      sleep: noWait,
    });
    expect(end?.purged).toBe(3);
    expect(status).not.toHaveBeenCalled();
  });

  it('names the run it started once, done or not — the explorer puts it in the operations centre', async () => {
    const started: Array<number | undefined> = [];
    await emptyTrashAndFollow({
      start: async () => reply(200, { ok: true, op_id: 5, running: false, total: 1, purged: 1, started_at: 'x' }),
      status: vi.fn(),
      onStart: (st) => started.push(st.op_id),
      sleep: noWait,
    });
    const looks = [reply(200, { op_id: 6, running: false, total: 2, purged: 2, started_at: 'x' })];
    await emptyTrashAndFollow({
      start: async () => reply(202, { op_id: 6, running: true, total: 2 }),
      status: async () => looks.shift()!,
      onStart: (st) => started.push(st.op_id),
      sleep: noWait,
    });
    await emptyTrashAndFollow({
      start: async () => reply(409, { code: 'BUSY', job: { op_id: 7, running: true, total: 1 } }),
      status: async () => reply(200, { op_id: 7, running: false, total: 1, purged: 1, started_at: 'x' }),
      onStart: (st) => started.push(st.op_id),
      sleep: noWait,
    });
    expect(started).toEqual([5, 6, 7]);
  });

  it('a purge still going is followed until it ends, and each look is reported', async () => {
    const looks = [
      reply(200, { ok: true, running: true, total: 10, scanned: 6, purged: 6 }),
      reply(200, { ok: true, running: false, total: 10, scanned: 10, purged: 10, started_at: 'x' }),
    ];
    const seen: number[] = [];
    const sleep = vi.fn(noWait);
    const end = await emptyTrashAndFollow({
      start: async () => reply(202, { ok: true, running: true, total: 10, scanned: 2, purged: 2 }),
      status: async () => looks.shift()!,
      onProgress: (st) => seen.push(st.scanned ?? -1),
      sleep,
    });
    expect(seen).toEqual([2, 6]);
    expect(end?.purged).toBe(10);
    expect(sleep).toHaveBeenCalledTimes(2);
  });

  it('a look that fails is not the end of the run', async () => {
    const looks: Array<() => Promise<Response>> = [
      async () => {
        throw new TypeError('Failed to fetch');
      },
      async () => reply(502, { error: 'bad gateway' }),
      async () => reply(200, { running: false, purged: 1, started_at: 'x' }),
    ];
    const end = await emptyTrashAndFollow({
      start: async () => reply(202, { running: true, total: 1 }),
      status: () => looks.shift()!(),
      sleep: noWait,
    });
    expect(end?.purged).toBe(1);
  });

  it('stops watching when told to, with null — the purge itself carries on', async () => {
    const status = vi.fn();
    const end = await emptyTrashAndFollow({
      start: async () => reply(202, { running: true, total: 9 }),
      status,
      stopped: () => true,
      sleep: noWait,
    });
    expect(end).toBeNull();
    expect(status).not.toHaveBeenCalled();
  });

  it("BUSY with the caller's own run: that run is followed", async () => {
    const onBusy = vi.fn();
    const end = await emptyTrashAndFollow({
      start: async () =>
        reply(409, { error: 'the trash is already being emptied', code: 'BUSY', job: { running: true, total: 4, scanned: 1 } }),
      status: async () => reply(200, { running: false, total: 4, purged: 4, started_at: 'x' }),
      onBusy,
      sleep: noWait,
    });
    expect(onBusy).toHaveBeenCalledOnce();
    expect(end?.purged).toBe(4);
  });

  it('BUSY with no run this caller may see is a refusal, not an end', async () => {
    await expect(
      emptyTrashAndFollow({
        start: async () => reply(409, { error: 'the trash is already being emptied', code: 'BUSY' }),
        status: vi.fn(),
        sleep: noWait,
      }),
    ).rejects.toBeInstanceOf(TrashEmptyBusy);
  });

  it("any other refusal is thrown in the server's own words", async () => {
    await expect(
      emptyTrashAndFollow({
        start: async () => reply(400, { error: 'older_than_days must be a whole number of days, 0 or more' }),
        status: vi.fn(),
        sleep: noWait,
      }),
    ).rejects.toThrow('older_than_days must be a whole number of days, 0 or more');
    await expect(
      emptyTrashAndFollow({ start: async () => new Response('<html>', { status: 504 }), status: vi.fn(), sleep: noWait }),
    ).rejects.toThrow('504');
  });
});
