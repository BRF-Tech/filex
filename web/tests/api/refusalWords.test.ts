// What a refusal SAYS. A read-only storage answers in two shapes — `409
// {"error":"read_only"}` from the app and public-API paths, `403 {"error":
// "storage is read-only"}` from the manager's mutating verbs — and by status
// alone they read "Already exists / conflict" and "You are not allowed to do
// this": a conflict that is not there and a permission problem the person
// does not have (QA, 2026-09-21: "Dönüştür…" on a read-only drive).
//
// Exercised through the real `useFileApi`, with `fetch` answering as the
// server does, because every caller — a menu action, an app dialog's submit —
// reads `err.message` and nothing else.
import { afterEach, describe, expect, it, vi } from 'vitest';

import { useFileApi } from '@brftech/filex-core/src/composables/useFileApi';

function answer(status: number, body: string) {
  vi.stubGlobal(
    'fetch',
    vi.fn(async () => new Response(body, { status, headers: { 'Content-Type': 'application/json' } })),
  );
}

async function refusal(locale: 'en' | 'tr'): Promise<Error & { status?: number }> {
  const api = useFileApi({ apiBase: '', locale });
  try {
    await api.pluginActionRun('convert', 'convert', { paths: ['arsiv://2025/a.pdf'] });
  } catch (e) {
    return e as Error & { status?: number };
  }
  throw new Error('the call did not fail');
}

afterEach(() => vi.unstubAllGlobals());

describe('a read-only storage is said as one', () => {
  it('409 read_only — the app path', async () => {
    answer(409, '{"error":"read_only","message":"this storage is read-only"}');
    expect((await refusal('tr')).message).toBe('Bu depo salt okunur');
    expect((await refusal('en')).message).toBe('This storage is read-only');
  });

  it('403 "storage is read-only" — the manager path, which is NOT a permission problem', async () => {
    answer(403, '{"error":"storage is read-only"}');
    const err = await refusal('tr');
    expect(err.message).toBe('Bu depo salt okunur');
    // The status still travels, for callers that branch on it.
    expect(err.status).toBe(403);
  });

  it('any other refusal keeps its status words', async () => {
    answer(409, '{"error":"exists"}');
    expect((await refusal('en')).message).toBe('Already exists / conflict');
    answer(403, 'not json at all');
    expect((await refusal('en')).message).toBe('You are not allowed to do this');
  });
});
