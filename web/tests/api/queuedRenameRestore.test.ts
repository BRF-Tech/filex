// `api.renameQueued` and `api.restoreQueued` — a rename and a restore asked
// as jobs of the operations queue (`queued=1`).
//
// Both used to run inside the request: a folder on an object store is one
// request per object, so the dialog waited with nothing on screen until the
// proxy gave up, then said the change had failed while the server carried on.
import { afterEach, describe, expect, it, vi } from 'vitest';

import { useFileApi } from '@brftech/filex-core/src/composables/useFileApi';

afterEach(() => vi.unstubAllGlobals());

function answers(...replies: Array<[number, unknown]>) {
  const calls: Array<{ url: string; body: unknown }> = [];
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string, init?: RequestInit) => {
      calls.push({ url: String(url), body: JSON.parse(String(init?.body ?? '{}')) });
      const [status, body] = replies.shift() ?? [500, { error: 'no reply scripted' }];
      return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
    }),
  );
  return calls;
}

describe('api.renameQueued', () => {
  it('asks the manager for a job, with the rename it already knows', async () => {
    const calls = answers([202, { op: { id: 7, kind: 'rename', status: 'pending', total: 1 } }]);
    const out = await useFileApi({ apiBase: '', locale: 'en' }).renameQueued('main://', 'main://Leon', 'Leo');
    expect(calls).toHaveLength(1);
    expect(calls[0].url).toContain('action=rename');
    expect(calls[0].url).toContain('queued=1');
    expect(calls[0].body).toEqual({ path: 'main://', item: 'main://Leon', name: 'Leo' });
    expect(out.op?.id).toBe(7);
  });

  it('hands back no job when the server renamed it inside the request', async () => {
    answers([200, { files: [], dirname: 'main://' }]);
    const out = await useFileApi({ apiBase: '', locale: 'en' }).renameQueued('main://', 'main://Leon', 'Leo');
    expect(out.op).toBeUndefined();
  });

  it('still throws a refusal, for the dialog to say', async () => {
    answers([409, { code: 'NAME_TAKEN', name: 'Leo', error: 'something with that name already exists here' }]);
    await expect(
      useFileApi({ apiBase: '', locale: 'en' }).renameQueued('main://', 'main://Leon', 'Leo'),
    ).rejects.toMatchObject({ status: 409 });
  });
});

describe('api.restoreQueued', () => {
  it('sends the whole selection in one request and asks for a job', async () => {
    const calls = answers([202, { ops: [{ id: 9, kind: 'restore', status: 'pending', total: 2 }] }]);
    const out = await useFileApi({ apiBase: '', locale: 'en' }).restoreQueued([11, 12]);
    expect(calls).toHaveLength(1);
    expect(calls[0].url).toMatch(/\/api\/files\/manager\/restore\?queued=1$/);
    expect(calls[0].body).toEqual({ node_ids: [11, 12] });
    expect(out.ops.map((o) => o.id)).toEqual([9]);
  });
});
