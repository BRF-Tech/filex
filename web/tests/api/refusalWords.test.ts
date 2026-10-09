// What a refusal SAYS. A read-only storage answered in two shapes — `409
// {"error":"read_only"}` from the app and public-API paths, `403 {"error":
// "storage is read-only"}` from the manager's mutating verbs — and by status
// alone they read "Already exists / conflict" and "You are not allowed to do
// this": a conflict that is not there and a permission problem the person
// does not have (QA, 2026-09-21: "Dönüştür…" on a read-only drive).
//
// ⚠⚠ 0.54 (#209, audit A1/A2): both doors now answer one envelope, `{"error":
// "read_only", "message": "<the reader's sentence>"}` (backend
// internal/apierr), and the explorer shows the server's sentence. It no
// longer recognises "read-only" in the server's English (CODE_WORDS is gone).
//
// Exercised through the real `useFileApi`, with `fetch` answering as the
// server does, because every caller — a menu action, an app dialog's submit —
// reads `err.message` and nothing else.
import { afterEach, describe, expect, it, vi } from 'vitest';

import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

import { useFileApi } from '@brftech/filex-core/src/composables/useFileApi';
import { sayFailure, wordsIn } from '@brftech/filex-core/src/lib/errorWords';
import { en as coreEn } from '@brftech/filex-core/src/locales/en';

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

describe('a read-only storage is said in the server’s sentence', () => {
  it('409 read_only — the app path', async () => {
    answer(409, '{"error":"read_only","message":"Bu depo salt okunur."}');
    expect((await refusal('tr')).message).toBe('Bu depo salt okunur.');
    answer(409, '{"error":"read_only","message":"This storage is read-only."}');
    expect((await refusal('en')).message).toBe('This storage is read-only.');
  });

  it('403 read_only — the manager path, which is NOT a permission problem', async () => {
    answer(403, '{"error":"read_only","message":"Bu depo salt okunur."}');
    const err = await refusal('tr');
    expect(err.message).toBe('Bu depo salt okunur.');
    // The status still travels, for callers that branch on it.
    expect(err.status).toBe(403);
  });

  it('a refusal with no sentence keeps its status words - nothing is read out of its English', async () => {
    answer(409, '{"error":"exists"}');
    expect((await refusal('en')).message).toBe('Already exists / conflict');
    answer(403, 'not json at all');
    expect((await refusal('en')).message).toBe('You are not allowed to do this');
    // RED before 0.54: the English "storage is read-only" was matched.
    answer(403, '{"error":"storage is read-only"}');
    expect((await refusal('en')).message).toBe('You are not allowed to do this');
  });
});

// 0.55 (#209 leftovers): an app action refused on a read-only storage (409
// read_only, handlers/app_plugins.go writeReadOnly) is the server's sentence.
// The explorer's runPluginAction caught it first and said its own
// `plugin.read_only` - the server's `message` never reached the person.
describe('an app action on a read-only storage', () => {
  it("is said in the server's sentence, through the explorer's failure words", async () => {
    answer(409, '{"error":"read_only","message":"Bu depo salt okunur."}');
    const err = await refusal('tr');
    expect(sayFailure(err, 'failed', { t: wordsIn('tr') }).text).toBe('Bu depo salt okunur.');
  });

  it('the explorer keeps no sentence of its own for it', () => {
    const src = readFileSync(resolve(__dirname, '../../../packages/core/src/FileExplorer.vue'), 'utf8');
    const run = src.slice(src.indexOf('async function runPluginAction'), src.indexOf('function onPluginOpQueued'));
    expect(run.length).toBeGreaterThan(0);
    // No branch on the refusal's code that says words of its own.
    expect(run).not.toContain("includes('read_only')");
    expect(run).not.toContain("t('plugin.read_only')");
    expect(Object.keys(coreEn)).not.toContain('plugin.read_only');
  });
});

// The same for an action whose rule does not fit the selection (422
// not_applicable, handlers/app_plugins.go): its sentence was English for
// every reader, so the explorer said `plugin.not_applicable` itself. The
// server says it now (server.error.not_applicable).
describe('an app action that does not apply to the selection', () => {
  it("is said in the server's sentence", async () => {
    answer(422, '{"error":"not_applicable","message":"Bu işlem seçime uygulanamaz."}');
    const err = await refusal('tr');
    expect(sayFailure(err, 'failed', { t: wordsIn('tr') }).text).toBe('Bu işlem seçime uygulanamaz.');
  });

  it('the explorer no longer words it from the code', () => {
    const src = readFileSync(resolve(__dirname, '../../../packages/core/src/FileExplorer.vue'), 'utf8');
    const run = src.slice(src.indexOf('async function runPluginAction'), src.indexOf('function onPluginOpQueued'));
    expect(run.length).toBeGreaterThan(0);
    expect(run).not.toContain("includes('not_applicable')");
    expect(run).not.toContain("t('plugin.not_applicable')");
  });
});
