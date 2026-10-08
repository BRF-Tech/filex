// How a failure is SAID (lib/errorWords) — one helper every screen uses.
//
// ⚠⚠ The owner's rule after the QA sweep of 2026-09-21: a translated sentence
// that says what happened and, where it applies, what to do; an administrator
// may see the technical detail as a second line; a regular user NEVER sees an
// environment variable, a status code or a JSON body. The sweep found all
// three on screen ("Config fetch 503: {…}", "set FILEX_SECRET_KEY…",
// "save failed: 500 {…}", "engine libreoffice is not installed on this host").
//
// ⚠⚠ 0.54 (#209, audit A1/A2): the sentence is the SERVER's. A refusal carries
// `message` (backend internal/apierr), a failed queue row `error_text` (backend
// ops/errcode.go), both written in the reader's language; the client shows
// them and keeps no table of the server's codes or of its English (CODE_WORDS,
// JOB_WORDS, CODE_FIELD_WORDS, REASON_WORDS are gone). The server bodies below
// carry the sentences the server writes (srvtext en.json / tr.json).
import { afterEach, describe, expect, it, vi } from 'vitest';
import { mount } from '@vue/test-utils';
import { nextTick } from 'vue';

import {
  jobFailure,
  opFailure,
  looksTechnical,
  requestFailure,
  sayFailure,
  serverWords,
  statusWords,
  wordsIn,
} from '@brftech/filex-core/src/lib/errorWords';
import { useFileApi } from '@brftech/filex-core/src/composables/useFileApi';
import { useOperations } from '@brftech/filex-core/src/composables/useOperations';
import PendingOpsTray from '@brftech/filex-core/src/components/PendingOpsTray.vue';
import OperationsCenter from '@brftech/filex-core/src/components/OperationsCenter.vue';
import type { PendingOp } from '@brftech/filex-core/src/composables/usePendingOps';

const en = wordsIn('en');
const tr = wordsIn('tr');

afterEach(() => vi.unstubAllGlobals());

/** Nothing a regular user reads may carry any of these. */
function expectPlainWords(s: string) {
  expect(s, s).not.toMatch(/\b[1-5]\d\d\b/); // a status code
  expect(s, s).not.toMatch(/[{}]/); // a JSON body
  expect(s, s).not.toMatch(/FILEX_[A-Z_]+/); // an environment variable
}

describe('statusWords and requestFailure', () => {
  it('never prints a status code, not even for one it does not know', () => {
    for (const status of [400, 401, 403, 404, 409, 418, 451, 500, 502, 503, 504, 599]) {
      expectPlainWords(statusWords(status, 'en'));
      expectPlainWords(statusWords(status, 'tr'));
    }
    expect(statusWords(418, 'en')).toBe(en('err.status.other'));
    expect(statusWords(599, 'tr')).toBe(tr('err.status.500'));
  });

  it('a missing encryption key is said as the server’s sentence — the env var stays out of the message', () => {
    const said = 'Bu sunucuda şifreleme anahtarı tanımlı değil, bu yüzden erişim anahtarı verilemiyor. Yöneticinizden tanımlamasını isteyin.';
    const err = requestFailure(
      503,
      JSON.stringify({ error: 'no_secret_key', message: said, admin_hint: 'Set FILEX_SECRET_KEY on the server' }),
      'tr',
    );
    expect(err.message).toBe(said);
    expect(err.server).toBe(said);
    expectPlainWords(err.message);
    expect(err.code).toBe('no_secret_key');
    // The fix travels only because the server chose to send it (admins only).
    expect(err.hint).toContain('FILEX_SECRET_KEY');
  });
});

describe('looksTechnical', () => {
  it('flags plumbing', () => {
    for (const s of [
      '{"error":"onlyoffice not configured"}',
      '500 Internal Server Error',
      'protocolauth: no secret key configured; set FILEX_SECRET_KEY to issue S3 access keys',
      'dial tcp 10.0.0.5:587: connect: connection refused',
      'open /var/lib/filex/data/x: permission denied',
      'panic: runtime error: nil pointer dereference',
    ]) {
      expect(looksTechnical(s), s).toBe(true);
    }
  });
  it('lets a sentence through', () => {
    for (const s of ['Bu belge zaten imzalanmış.', 'The public key is not a valid OpenSSH key.', 'access keys are not available on this install']) {
      expect(looksTechnical(s), s).toBe(false);
    }
  });
});

describe('sayFailure', () => {
  it('a thrown library error is the fallback sentence; the raw words only for an admin', () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {});
    const boom = new Error('Monaco mount fail: Cannot read properties of undefined');
    expect(sayFailure(boom, 'The viewer could not be started.')).toEqual({ text: 'The viewer could not be started.' });
    expect(sayFailure(boom, 'The viewer could not be started.', { callerAdmin: true })).toEqual({
      text: 'The viewer could not be started.',
      detail: 'Monaco mount fail: Cannot read properties of undefined',
    });
    warn.mockRestore();
  });

  it('a refusal keeps its own sentence, re-said in the caller’s words when it has them', () => {
    const err = requestFailure(500, '{"error":"write failed: disk full"}', 'en');
    expect(sayFailure(err, 'fallback')).toEqual({ text: en('err.status.500') });
    expect(sayFailure(err, 'fallback', { t: tr }).text).toBe(tr('err.status.500'));
    expect(sayFailure(err, 'fallback', { callerAdmin: true }).detail).toContain('disk full');
  });

  it('a refusal the server said is re-said in the server’s words, not translated again', () => {
    const said = 'Bu depo salt okunur.';
    const err = requestFailure(403, JSON.stringify({ error: 'read_only', message: said }), 'tr');
    expect(sayFailure(err, 'fallback', { t: en }).text).toBe(said);
    expect(sayFailure(err, 'fallback', { t: tr }).text).toBe(said);
  });
});

describe('serverWords (the connection panels)', () => {
  it('says the server’s sentence, keeps an older sentence in `error`, hides plumbing and bare codes', () => {
    const said = 'This server has no encryption key set, so it cannot issue an access key. Ask an administrator to configure one.';
    expect(serverWords(requestFailure(503, JSON.stringify({ error: 'no_secret_key', message: said }), 'en'))).toBe(said);
    // A bare code with no sentence is the status's words (the client keeps no
    // table of codes any more).
    expect(serverWords(requestFailure(503, '{"error":"no_secret_key"}', 'en'))).toBe(en('err.status.503'));
    expect(serverWords(requestFailure(400, '{"error":"The public key is not a valid OpenSSH key."}', 'en'))).toBe(
      'The public key is not a valid OpenSSH key.',
    );
    const plumbing = requestFailure(
      503,
      '{"error":"protocolauth: no secret key configured; set FILEX_SECRET_KEY to issue S3 access keys"}',
      'en',
    );
    expect(serverWords(plumbing)).toBe(en('err.status.503'));
    expectPlainWords(serverWords(plumbing));
  });
});

describe('jobFailure (the operations centre)', () => {
  const raw = 'engine libreoffice is not installed on this host';
  const op = { error: raw, error_code: 'engine_missing', error_engine: 'libreoffice' };
  // What the server says for this row: the reader's form, and the
  // administrator's (who can install the program) - backend ops sayErrors.
  const USER = 'Bunun için sunucuda olmayan bir program gerekiyor (libreoffice). Yöneticinize başvurun.';
  const ADMIN = 'Bunun için libreoffice gerekiyor ve filex\'i çalıştıran sunucuda kurulu değil. Oraya kurup yeniden deneyin.';

  it('a missing engine is the server’s sentence; the raw words only for an admin', () => {
    const user = jobFailure({ ...op, error_text: USER }, 'Convert failed', tr);
    expect(user.text).toBe(USER);
    expect(user.text).not.toContain('not installed on this host');
    expect(user.detail).toBeUndefined();
    const admin = jobFailure({ ...op, error_text: ADMIN }, 'Convert failed', tr, { callerAdmin: true });
    expect(admin.text).toBe(ADMIN);
    expect(admin.detail).toBe(raw);
  });

  it('a code the server did not say is the fallback - the client words no job code of its own', () => {
    // RED before 0.54: the client turned `engine_missing` into opc.err.* of
    // its own; the server says it now (error_text).
    expect(jobFailure(op, 'Convert failed', tr)).toEqual({ text: 'Convert failed' });
    const office = { error: 'engine libreoffice is not configured on this host', error_code: 'office_unconfigured', error_engine: 'office' };
    expect(jobFailure(office, 'Dönüştür başarısız', tr)).toEqual({ text: 'Dönüştür başarısız' });
    expect(jobFailure(office, 'Dönüştür başarısız', tr, { callerAdmin: true }).detail).toBe(office.error);
  });

  it('a cancelled job is said as stopped', () => {
    expect(jobFailure({ error: 'cancelled', error_code: 'cancelled' }, 'fallback', en)).toEqual({ text: en('opc.status.aborted') });
  });

  it('the app’s own words are shown; the app’s plumbing is not', () => {
    expect(jobFailure({ error: 'Bu belge zaten imzalanmış.', error_code: 'app' }, 'fallback', tr)).toEqual({
      text: 'Bu belge zaten imzalanmış.',
    });
    const raw = 'sign: open /var/lib/filex/app-plugins/sign/x.pdf: no such file or directory';
    expect(jobFailure({ error: raw, error_code: 'app' }, 'İmzala başarısız', tr)).toEqual({ text: 'İmzala başarısız' });
    expect(jobFailure({ error: raw, error_code: 'app' }, 'İmzala başarısız', tr, { callerAdmin: true }).detail).toBe(raw);
  });

  it('keeps the app’s sentence and moves its bracketed plumbing to the admin’s line', () => {
    // Measured 2026-09-21 in a browser: converting a broken .docx.
    const raw =
      'hiçbir dosya dönüştürülemedi — broken.docx: dönüşüm başarısız oldu (office: not a zip package: zip: not a valid zip file)';
    const said = jobFailure({ error: raw, error_code: 'app' }, 'Dönüştür… başarısız', tr);
    expect(said).toEqual({ text: 'hiçbir dosya dönüştürülemedi — broken.docx: dönüşüm başarısız oldu' });
    expect(jobFailure({ error: raw, error_code: 'app' }, 'x', tr, { callerAdmin: true }).detail).toBe(raw);
  });

  it('the toast and the tray say the same thing about one queue row', () => {
    // ⚠ The toast read `op.error` — a field a queue row does not have — and
    // said the fallback while the tray said the app's words.
    const row = {
      op_type: 'plugin', label: 'Dönüştür…', error_message: 'Bu belge zaten imzalanmış.', error_code: 'app',
    };
    expect(opFailure(row, tr).text).toBe('Bu belge zaten imzalanmış.');
    expect(opFailure({ ...row, error_code: undefined, error_message: 'rename: EOF' }, tr).text).toBe(
      tr('plugin.failed', { label: 'Dönüştür…' }),
    );
  });

  it('a copy: the server’s sentence when it said one, the fallback otherwise - never its English', () => {
    expect(
      jobFailure(
        { error: 'destination storage is read-only: arsiv', error_code: 'read_only', error_text: 'This storage is read-only.' },
        'Operation failed',
        en,
      ).text,
    ).toBe('This storage is read-only.');
    // RED before 0.54: CODE_WORDS matched the English "read-only".
    expect(jobFailure({ error: 'destination storage is read-only: arsiv' }, 'Operation failed', en)).toEqual({ text: 'Operation failed' });
    expect(jobFailure({ error: 'rename x y: EOF' }, 'Operation failed', en)).toEqual({ text: 'Operation failed' });
  });

  it('the tray hands the centre the sentence — and the detail only to an admin', async () => {
    const failed: PendingOp = {
      id: 7, op_type: 'plugin', status: 'error', progress_total: 1, progress_done: 0,
      target_path: null, source_dir: null, source_count: 1,
      error_message: 'engine libreoffice is not installed on this host',
      error_code: 'engine_missing', error_engine: 'libreoffice',
      started_at: null, finished_at: null, created_at: null, plugin: 'convert', action: 'convert', label: 'Dönüştür',
    };
    // The server says the row for its reader: the administrator's form to an
    // administrator (backend ops sayErrors).
    const USER = 'Bunun için sunucuda olmayan bir program gerekiyor (libreoffice). Yöneticinize başvurun.';
    const ADMIN = 'Bunun için libreoffice gerekiyor ve filex\'i çalıştıran sunucuda kurulu değil. Oraya kurup yeniden deneyin.';
    for (const callerAdmin of [false, true]) {
      const center = useOperations();
      const w = mount(PendingOpsTray, { props: { ops: [], locale: 'tr', center, callerAdmin } });
      await w.setProps({ ops: [{ ...failed, error_text: callerAdmin ? ADMIN : USER }] });
      await nextTick();
      const row = [...center.active.value, ...center.history.value].find((o) => String(o.key).endsWith(':7'));
      expect(row?.error).toBe(callerAdmin ? ADMIN : USER);
      expect(row?.errorDetail ?? null).toBe(callerAdmin ? failed.error_message : null);
      w.unmount();
    }
  });
});

describe('the operations centre draws the admin’s second line — on a live row too', () => {
  // ⚠ Measured 2026-09-21: the detail was drawn only in the HISTORY list, and
  // a failed job stays in the active list until dismissed, so an admin never
  // saw it.
  for (const [where, status] of [['active', 'error']] as const) {
    it(`${where} row with a detail shows it; without one, nothing`, async () => {
      const center = useOperations();
      center.sync('ops', [
        {
          input: { id: 1, kind: 'plugin', name: 'Dönüştür…', percent: null, status, error: 'Uygulama durdu.', errorDetail: 'plugin_trap: unreachable' },
          actions: {},
        },
        { input: { id: 2, kind: 'plugin', name: 'Dönüştür…', percent: null, status, error: 'Uygulama durdu.' }, actions: {} },
      ]);
      const w = mount(OperationsCenter, { props: { center, locale: 'tr' } });
      await w.get('.fe-opc__badge').trigger('click');
      await nextTick();
      const details = w.findAll('[data-testid="opc-error-detail"]').map((d) => d.text());
      expect(details).toEqual(['plugin_trap: unreachable']);
      w.unmount();
    });
  }
});

describe('useFileApi says what it cannot reach', () => {
  it('no answer at all is a sentence, not "TypeError: Failed to fetch"', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => { throw new TypeError('Failed to fetch'); }));
    const api = useFileApi({ apiBase: '', locale: 'tr' });
    await expect(api.pluginActionRun('convert', 'convert', { paths: ['a://b'] })).rejects.toThrow(tr('err.network'));
  });

  it('an abort is handed back untouched — callers branch on it', async () => {
    const abort = new DOMException('The operation was aborted.', 'AbortError');
    vi.stubGlobal('fetch', vi.fn(async () => { throw abort; }));
    const api = useFileApi({ apiBase: '', locale: 'en' });
    await expect(api.pluginActionRun('convert', 'convert', { paths: ['a://b'] })).rejects.toBe(abort);
  });

  it('fetchBlob no longer prints "404 Not Found — {…}"', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response('{"error":"not found"}', { status: 404, statusText: 'Not Found' })));
    const api = useFileApi({ apiBase: '', locale: 'en' });
    const err = await api.fetchBlob('a://b.png').catch((e: Error) => e);
    expect((err as Error).message).toBe(en('err.status.404'));
    expectPlainWords((err as Error).message);
  });
});

// ⚠ 0.54 (#209): these refusals were worded by the client (REASON_WORDS,
// CODE_WORDS: e2e_not_allowed + reason, kind_mismatch, not_requestable,
// path_missing, too_many_pending, "could not check the encryption policy").
// The server words every one of them now (server.e2e.not_allowed.*,
// server.error.*); the client shows its `message`.
describe('e2e_not_allowed - the server’s sentence for the reason (wiring:e2 policy)', () => {
  const SAID: Record<string, string> = {
    tenant_disabled: 'Encryption is not available here: the platform operator has turned it off.',
    policy_off: 'An administrator has turned off encryption.',
    admins_only: 'Only an administrator can create an encrypted folder or file here.',
    permission: 'Your account is not allowed to encrypt here.',
    approval_required: 'Encrypting here needs an administrator\'s approval. Send a request for it first.',
  };

  it('each reason is the sentence the server wrote for it, with the code and the reason kept', () => {
    for (const [reason, said] of Object.entries(SAID)) {
      const err = requestFailure(403, JSON.stringify({ error: 'e2e_not_allowed', reason, message: said }), 'en');
      expect(err.message).toBe(said);
      expect(err.code).toBe('e2e_not_allowed');
      expect(err.reason).toBe(reason);
      expectPlainWords(err.message);
    }
  });

  it('a refusal with no sentence is the status’s words - the client no longer knows the reasons', () => {
    expect(requestFailure(403, '{"error":"e2e_not_allowed","reason":"moon_phase"}', 'en').message).toBe(en('err.status.403'));
  });

  it('survives the clip: a long sentence is said whole, by every re-sayer', () => {
    const long = 'x'.repeat(400) + ' approval';
    const err = requestFailure(403, JSON.stringify({ error: 'e2e_not_allowed', message: long, reason: 'approval_required' }), 'en');
    expect(err.detail?.includes('approval_required')).toBe(false);
    expect(sayFailure(err, 'fallback', { t: tr }).text).toBe(long);
    expect(serverWords(err, 'en')).toBe(long);
  });

  it('a permission refusal is said in the server’s words - its reason, not "not allowed" (A1)', () => {
    const said = 'The role “Contractors” does not allow you to delete files.';
    const err = requestFailure(403, JSON.stringify({ error: 'permission_denied', message: said, reason: 'policy_off' }), 'en');
    expect(err.message).toBe(said);
    expect(err.message).not.toBe(en('err.status.403'));
  });
});

// `POST /api/files/e2e/requests` answers 400 when the listing the person asked
// from went stale (`kind_mismatch`, `not_requestable`), 404 `path_missing` and
// 429 `too_many_pending` - each with the server's sentence (backend
// handlers/e2e_policy_files.go writeE2ERequestError).
describe('the encryption request’s refusals on a stale listing (wiring:e2 policy)', () => {
  const cases = [
    [400, 'kind_mismatch', 'This item has changed since the folder was listed. Refresh the folder and try again.'],
    [400, 'not_requestable', 'What you may do here has changed since the folder was listed. Refresh the folder to see what is offered now.'],
    [404, 'path_missing', 'This folder or file is no longer here. Refresh the folder and try again.'],
    [429, 'too_many_pending', 'You already have many encryption requests waiting. Wait until an administrator answers one of them, then ask again.'],
  ] as const;

  it('each is the server’s sentence, not the status’s words', () => {
    for (const [status, code, said] of cases) {
      const err = requestFailure(status, JSON.stringify({ error: code, message: said }), 'tr');
      expect(err.message, code).toBe(said);
      expect(err.code).toBe(code);
      expect(err.message).not.toBe(tr(`err.status.${status}`));
      expect(sayFailure(err, 'fallback', { t: tr }).text).toBe(said);
      expect(serverWords(err, 'tr')).toBe(said);
      expectPlainWords(err.message);
    }
  });

  it('one with no sentence is the status’s words', () => {
    expect(requestFailure(400, '{"error":"reason_required"}', 'en').message).toBe(en('err.status.400'));
  });
});

// `answerE2E` (backend handlers/e2e_policy_gate.go): a rule that could not be
// decided is not a yes, so every HTTP door that creates a key file or a `.fxe`
// answers `500 {"error":"e2e_policy_undecided","message":…}` (it used to put
// "could not check the encryption policy" in `error`, which the client
// matched). By status alone that reads "Server error" for ANY failure; the
// server's sentence names the failure.
describe('an encryption rule that could not be decided (wiring:e2 policy)', () => {
  const SAID = 'The encryption policy could not be checked. Please try again shortly.';
  const body = JSON.stringify({ error: 'e2e_policy_undecided', message: SAID });

  it('is the server’s sentence - what failed, what to do - not "Server error"', () => {
    const err = requestFailure(500, body, 'en');
    expect(err.message).toBe(SAID);
    expect(err.message).not.toBe(en('err.status.500'));
    expect(err.code).toBe('e2e_policy_undecided');
    expectPlainWords(err.message);
  });

  it('reaches a person from the client, and every catch site says the same sentence', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => new Response(body, { status: 500, headers: { 'Content-Type': 'application/json' } })),
    );
    const api = useFileApi({ apiBase: '', locale: 'en' });
    const err = await api.e2eRequest({ path: 'docs://a', kind: 'folder', reason: 'x' }).catch((e: Error) => e);
    expect((err as Error).message).toBe(SAID);
    expect(sayFailure(err, 'Could not create the encrypted folder', { t: tr }).text).toBe(SAID);
    expect(serverWords(err, 'en')).toBe(SAID);
  });

  it('any other 500 with no sentence still says that the server failed', () => {
    for (const other of [
      '{"error":"database is locked"}',
      '{"error":"internal error"}',
      '{"error":"could not check the encryption policy"}',
      'plain text',
      '',
    ]) {
      expect(requestFailure(500, other, 'en').message, other).toBe(en('err.status.500'));
    }
  });
});
