// `api.restoreIds` — restore from the trash, one request per item.
//
// ⚠ A failure that was not "the name is taken" (409 EXISTS) was skipped
// without a word: a folder whose restore timed out at the proxy, or that the
// server refused, simply did not count, and the explorer said "2 items
// restored" over a selection of three. It is counted now, with the words of the
// first one, so the explorer can say what did not happen.
import { afterEach, describe, expect, it, vi } from 'vitest';

import { useFileApi } from '@brftech/filex-core/src/composables/useFileApi';

afterEach(() => vi.unstubAllGlobals());

function answers(...replies: Array<[number, unknown]>) {
  const bodies: unknown[] = [];
  vi.stubGlobal(
    'fetch',
    vi.fn(async (_url: string, init?: RequestInit) => {
      bodies.push(JSON.parse(String(init?.body ?? '{}')));
      const [status, body] = replies.shift() ?? [500, { error: 'no reply scripted' }];
      return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
    }),
  );
  return bodies;
}

describe('api.restoreIds', () => {
  it('counts what was restored, what was refused for its name, and what failed', async () => {
    const bodies = answers(
      [200, { ok: true }],
      [409, { code: 'EXISTS', name: 'Kayıtlar' }],
      [504, { error: 'gateway timeout' }],
    );
    const api = useFileApi({ apiBase: '', locale: 'en' });
    const out = await api.restoreIds([11, 12, 13]);
    expect(bodies).toEqual([{ node_id: 11 }, { node_id: 12 }, { node_id: 13 }]);
    expect(out.restored).toBe(1);
    expect(out.taken).toEqual(['Kayıtlar']);
    expect(out.failed, 'the timed-out item is not silently dropped').toBe(1);
    expect(out.failure, 'and the first failure is kept to be said').toBeTruthy();
  });

  it('has nothing to report when everything came back', async () => {
    answers([200, {}], [200, {}]);
    const out = await useFileApi({ apiBase: '', locale: 'en' }).restoreIds([1, 2]);
    expect(out).toMatchObject({ restored: 2, taken: [], failed: 0 });
    expect(out.failure).toBeUndefined();
  });
});
