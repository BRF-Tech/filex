// How a failure is SAID (lib/errorWords) — the one table every screen uses.
//
// ⚠⚠ The owner's rule after the QA sweep of 2026-09-21: a translated sentence
// that says what happened and, where it applies, what to do; an administrator
// may see the technical detail as a second line; a regular user NEVER sees an
// environment variable, a status code or a JSON body. The sweep found all
// three on screen ("Config fetch 503: {…}", "set FILEX_SECRET_KEY…",
// "save failed: 500 {…}", "engine libreoffice is not installed on this host").
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

  it('a missing encryption key is said as a sentence — the env var stays out of the message', () => {
    const err = requestFailure(503, '{"error":"no_secret_key","admin_hint":"Set FILEX_SECRET_KEY on the server"}', 'tr');
    expect(err.message).toBe(tr('err.no_secret_key'));
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
});

describe('serverWords (the connection panels)', () => {
  it('says a known code in words, keeps a server sentence, hides plumbing', () => {
    expect(serverWords(requestFailure(503, '{"error":"no_secret_key"}', 'en'))).toBe(en('err.no_secret_key'));
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
  const op = { error: 'engine libreoffice is not installed on this host', error_code: 'engine_missing', error_engine: 'libreoffice' };

  it('a missing engine: a person is told to ask, an admin what to install — both in words', () => {
    const user = jobFailure(op, 'Convert failed', tr);
    expect(user.text).toBe(tr('opc.err.engine_missing', { engine: 'libreoffice' }));
    expect(user.text).not.toContain('not installed on this host');
    expect(user.detail).toBeUndefined();
    const admin = jobFailure(op, 'Convert failed', tr, { callerAdmin: true });
    expect(admin.text).toBe(tr('opc.err.engine_missing_admin', { engine: 'libreoffice' }));
    expect(admin.detail).toBe(op.error);
  });

  it('no document server for the office engine: connect ONLYOFFICE, never "install" (0.50)', () => {
    /* Red before 0.50: there was no `office_unconfigured`; the office engine
       was a binary and its failure read "This needs libreoffice, which is
       not installed ... Install it there". */
    const raw =
      'engine libreoffice is not configured on this host: office documents are converted by ONLYOFFICE Document Server, and none is connected (an administrator connects one under External services)';
    const op = { error: raw, error_code: 'office_unconfigured', error_engine: 'office' };
    const user = jobFailure(op, 'Dönüştür başarısız', tr);
    expect(user.text).toBe(tr('opc.err.office_unconfigured'));
    expect(user.text).toContain('ONLYOFFICE');
    expect(user.text).not.toContain('configured on this host');
    expect(user.detail).toBeUndefined();
    const admin = jobFailure(op, 'Dönüştür başarısız', tr, { callerAdmin: true });
    expect(admin.text).toBe(tr('opc.err.office_unconfigured_admin'));
    expect(admin.text).toContain('Dış servisler');
    expect(admin.detail).toBe(raw);
    expect(jobFailure(op, 'Convert failed', en, { callerAdmin: true }).text).not.toMatch(/install|libreoffice/i);
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

  it('a copy with no code: a read-only refusal is recognised, anything else is the fallback', () => {
    expect(jobFailure({ error: 'destination storage is read-only: arsiv' }, 'Operation failed', en).text).toBe(en('err.read_only'));
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
    for (const callerAdmin of [false, true]) {
      const center = useOperations();
      const w = mount(PendingOpsTray, { props: { ops: [], locale: 'tr', center, callerAdmin } });
      await w.setProps({ ops: [failed] });
      await nextTick();
      const row = [...center.active.value, ...center.history.value].find((o) => String(o.key).endsWith(':7'));
      expect(row?.error).toBe(tr(callerAdmin ? 'opc.err.engine_missing_admin' : 'opc.err.engine_missing', { engine: 'libreoffice' }));
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

describe('e2e_not_allowed — the reason decides the words (wiring:e2 policy)', () => {
  const REASONS = ['tenant_disabled', 'policy_off', 'admins_only', 'permission', 'approval_required'] as const;

  it('each reason has its own sentence, in both languages, with no code in it', () => {
    for (const reason of REASONS) {
      const key = `err.e2e_not_allowed.${reason}`;
      // A key missing from BOTH tables would make `t(key)` return the key and
      // the comparison below pass for the wrong reason.
      expect(en(key), key).not.toBe(key);
      expect(tr(key), key).not.toBe(key);
      const body = JSON.stringify({ error: 'e2e_not_allowed', reason, message: 'encryption is not allowed here' });
      for (const [lang, t] of [['en', en], ['tr', tr]] as const) {
        const err = requestFailure(403, body, lang);
        expect(err.message).toBe(t(key));
        expect(err.code).toBe('e2e_not_allowed');
        expect(err.reason).toBe(reason);
        expectPlainWords(err.message);
      }
    }
    expect(new Set(REASONS.map((r) => en(`err.e2e_not_allowed.${r}`))).size).toBe(REASONS.length);
  });

  it('a reason this build does not know still says encryption was refused — never "not allowed" alone', () => {
    const err = requestFailure(403, '{"error":"e2e_not_allowed","reason":"moon_phase"}', 'en');
    expect(err.message).toBe(en('err.e2e_not_allowed.other'));
    expect(err.message).not.toBe(en('err.status.403'));
    expect(requestFailure(403, '{"error":"e2e_not_allowed"}', 'tr').message).toBe(tr('err.e2e_not_allowed.other'));
  });

  it('is said again in the reader’s language — even when a long message pushed the reason past the clip', () => {
    const long = 'x'.repeat(400);
    // A Go map writes its keys in order: error, message, reason.
    const err = requestFailure(403, `{"error":"e2e_not_allowed","message":"${long}","reason":"approval_required"}`, 'en');
    expect(err.detail?.includes('approval_required')).toBe(false);
    expect(sayFailure(err, 'fallback', { t: tr }).text).toBe(tr('err.e2e_not_allowed.approval_required'));
    expect(serverWords(err, 'en')).toBe(en('err.e2e_not_allowed.approval_required'));
  });

  it('`reason` means nothing on a refusal that is not listed', () => {
    const err = requestFailure(403, '{"error":"permission_denied","reason":"policy_off"}', 'en');
    expect(err.message).toBe(en('err.status.403'));
  });
});

// `POST /api/files/e2e/requests` answers 400 when the listing the person asked
// from went stale: `kind_mismatch` — the kind asked for is not what is at the
// path (a folder was replaced by a file) — and `not_requestable` — the rule now
// says nothing can be requested there: `allowed` (an approval came, or the
// policy loosened), or `denied` with the `reason` of the refusal. Its other
// 400s are `bad_request`, which the dialog never provokes: it sends what the
// listing said and a reason.
describe('the encryption request’s refusals on a stale listing (wiring:e2 policy)', () => {
  const kindMismatch = JSON.stringify({
    error: 'kind_mismatch',
    message: 'kind must be what is there: folder for a folder, file for a file',
  });
  const notRequestable = (answer: string, reason?: string) =>
    JSON.stringify({
      error: 'not_requestable',
      message: 'no approval is needed here: go ahead and encrypt',
      answer,
      ...(reason ? { reason } : {}),
    });

  it('kind_mismatch is a sentence in both languages, not "Bad request" and not the server’s English', () => {
    for (const [lang, t] of [['en', en], ['tr', tr]] as const) {
      const key = 'err.e2e_request.kind_mismatch';
      expect(t(key), key).not.toBe(key);
      const err = requestFailure(400, kindMismatch, lang);
      expect(err.message).toBe(t(key));
      expect(err.code).toBe('kind_mismatch');
      expect(err.message).not.toBe(t('err.status.400'));
      expectPlainWords(err.message);
      expect(err.message).not.toContain('kind must be');
    }
    expect(tr('err.e2e_request.kind_mismatch')).not.toBe(en('err.e2e_request.kind_mismatch'));
  });

  it('not_requestable says the refusal’s own reason when the rule now says no', () => {
    for (const reason of ['tenant_disabled', 'policy_off', 'admins_only', 'permission']) {
      for (const [lang, t] of [['en', en], ['tr', tr]] as const) {
        const err = requestFailure(400, notRequestable('denied', reason), lang);
        expect(err.message).toBe(t(`err.e2e_not_allowed.${reason}`));
        expect(err.code).toBe('not_requestable');
      }
    }
  });

  it('not_requestable without a reason — the rule now says yes — says what changed, in both languages', () => {
    for (const [lang, t] of [['en', en], ['tr', tr]] as const) {
      const key = 'err.e2e_request.not_requestable';
      expect(t(key), key).not.toBe(key);
      const err = requestFailure(400, notRequestable('allowed'), lang);
      expect(err.message).toBe(t(key));
      expect(err.message).not.toBe(t('err.status.400'));
      expectPlainWords(err.message);
    }
    expect(tr('err.e2e_request.not_requestable')).not.toBe(en('err.e2e_request.not_requestable'));
  });

  it('are said again in the reader’s language, from the clipped detail', () => {
    const err = requestFailure(400, kindMismatch, 'en');
    expect(sayFailure(err, 'fallback', { t: tr }).text).toBe(tr('err.e2e_request.kind_mismatch'));
    expect(serverWords(err, 'en')).toBe(en('err.e2e_request.kind_mismatch'));
    // Even when a long message pushed the reason past the clip.
    const long = JSON.stringify({ error: 'not_requestable', message: 'x'.repeat(400), answer: 'denied', reason: 'policy_off' });
    const refused = requestFailure(400, long, 'en');
    expect(refused.detail?.includes('policy_off')).toBe(false);
    expect(sayFailure(refused, 'fallback', { t: tr }).text).toBe(tr('err.e2e_not_allowed.policy_off'));
  });

  it('says its own words only for its own codes', () => {
    expect(requestFailure(400, '{"error":"bad_request","message":"bad json"}', 'en').message).toBe(en('err.status.400'));
    expect(requestFailure(400, '{"error":"reason_required"}', 'en').message).toBe(en('err.status.400'));
  });
});

// `answerE2E` (backend handlers/e2e_policy_gate.go): a rule that could not be
// decided is not a yes, so every HTTP door that creates a key file or a `.fxe`
// answers `500 {"error":"could not check the encryption policy"}`. By status
// alone that reads "Server error" for ANY failure; here the failure is named.
// The explorer's three catch sites (a new encrypted folder, a folder encrypted
// in place, the conversion) all say a failure through `sayFailure`.
describe('an encryption rule that could not be decided (wiring:e2 policy)', () => {
  const KEY = 'err.e2e_policy.undecided';
  const body = JSON.stringify({ error: 'could not check the encryption policy' });

  it('is a sentence in both languages — what failed, what to do — not "Server error"', () => {
    for (const [lang, t] of [['en', en], ['tr', tr]] as const) {
      expect(t(KEY), KEY).not.toBe(KEY);
      const err = requestFailure(500, body, lang);
      expect(err.message).toBe(t(KEY));
      expect(err.message).not.toBe(t('err.status.500'));
      expect(err.code).toBe('could not check the encryption policy');
      expectPlainWords(err.message);
    }
    expect(tr(KEY)).not.toBe(en(KEY));
  });

  it('reaches a person from the client, and is said again in the reader’s language by the catch sites', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => new Response(body, { status: 500, headers: { 'Content-Type': 'application/json' } })),
    );
    const api = useFileApi({ apiBase: '', locale: 'en' });
    const err = await api.e2eRequest({ path: 'docs://a', kind: 'folder', reason: 'x' }).catch((e: Error) => e);
    expect((err as Error).message).toBe(en(KEY));
    expect(sayFailure(err, 'Could not create the encrypted folder', { t: tr }).text).toBe(tr(KEY));
    expect(serverWords(err, 'en')).toBe(en(KEY));
  });

  it('is said for this error only — any other 500 still says that the server failed', () => {
    for (const other of ['{"error":"database is locked"}', '{"error":"internal error"}', 'plain text', '']) {
      expect(requestFailure(500, other, 'en').message, other).toBe(en('err.status.500'));
    }
  });
});
