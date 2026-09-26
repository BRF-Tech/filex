// `api.purgeTrash` — "Delete permanently" in the explorer's Trash, through the
// same client every other request takes (jsonFetch → requestFailure).
//
// ⚠ #69 sent it with a raw `fetch`, and a refusal became
// `failureText(new Error(body.error || 'HTTP 403'))`: a plain Error, which
// lib/errorWords says as the generic "failed" — the server's reason was lost,
// and no Accept-Language, no connection notice, no auth-header path of the
// client's own.
import { afterEach, describe, expect, it, vi } from 'vitest';

import { useFileApi } from '@brftech/filex-core/src/composables/useFileApi';
import { sayFailure, wordsIn } from '@brftech/filex-core/src/lib/errorWords';

afterEach(() => vi.unstubAllGlobals());

function answers(...replies: Array<[number, unknown]>) {
  const calls: Array<{ url: string; method?: string }> = [];
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string, init?: RequestInit) => {
      calls.push({ url, method: init?.method });
      const [status, body] = replies.shift() ?? [500, { error: 'no reply scripted' }];
      return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
    }),
  );
  return calls;
}

describe('api.purgeTrash', () => {
  it('asks for a job of the queue, and hands the job back', async () => {
    const calls = answers([202, { op: { id: 41, kind: 'purge', status: 'pending' } }]);
    const out = await useFileApi({ apiBase: '', locale: 'en' }).purgeTrash(12, { queued: true });
    expect(calls).toEqual([{ url: '/api/admin/trash/12?queued=1', method: 'DELETE' }]);
    expect(out.op?.id).toBe(41);
  });

  it('purges inside the request the old way', async () => {
    const calls = answers([200, { ok: true }]);
    const out = await useFileApi({ apiBase: '', locale: 'en' }).purgeTrash(12);
    expect(calls[0].url).toBe('/api/admin/trash/12');
    expect(out.op).toBeUndefined();
  });

  it('keeps the server’s reason when it refuses', async () => {
    answers([404, { error: 'trash entry not found' }]);
    const api = useFileApi({ apiBase: '', locale: 'en' });
    const err = await api.purgeTrash(12, { queued: true }).catch((e: unknown) => e);
    const en = wordsIn('en');
    const said = sayFailure(err, en('toast.failed'), { t: en }).text;
    expect(said, 'the refusal was said as a bare "failed"').not.toBe(en('toast.failed'));
    expect(said).toBe(en('err.status.404'));
  });
});
