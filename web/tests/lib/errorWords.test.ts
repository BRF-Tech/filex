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
