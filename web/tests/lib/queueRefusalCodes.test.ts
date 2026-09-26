// The queue's doors answer with a `code` a person can be told about — said in
// the reader's language, in one place (lib/errorWords), for the explorer and
// the admin panel alike.
//
// The backend review (fix/berk-backend) added: BAD_KIND (400 — the generic
// POST /api/files/ops takes copy, move and delete only), READ_ONLY (403 — a
// queued move, delete or copy touching a read-only storage), NOT_CANCELLABLE
// (409 — a rename, restore or purge cannot be stopped once it has started),
// FINISHED (409 — cancelling a job that already ended) and TOO_MANY (400 — a
// queued restore of more than `max` entries). Their `error` field is the
// server's English; printed as it came, a Turkish reader got
// "this operation cannot be stopped once it has started; it finishes on its own".
import { describe, expect, it } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

import { codeWords, opFailure, requestFailure, sayFailure, wordsIn } from '@brftech/filex-core/src/lib/errorWords';

const en = wordsIn('en');
const tr = wordsIn('tr');

const body = (code: string, error: string, extra: Record<string, unknown> = {}) =>
  JSON.stringify({ error, code, ...extra });

describe('a queue refusal', () => {
  const cases = [
    [400, 'BAD_KIND', 'kind must be copy, move or delete', 'err.bad_kind'],
    [403, 'READ_ONLY', 'destination storage is read-only: arsiv', 'err.read_only'],
    [409, 'NOT_CANCELLABLE', 'this operation cannot be stopped once it has started; it finishes on its own', 'err.not_cancellable'],
    [409, 'FINISHED', 'already finished', 'err.finished'],
  ] as const;

  for (const [status, code, error, key] of cases) {
    it(`${code} is said in the reader's words`, () => {
      const err = requestFailure(status, body(code, error), 'tr');
      expect(err.message).toBe(tr(key));
      expect(sayFailure(err, en('toast.failed'), { t: en }).text).toBe(en(key));
      expect(tr(key), `${key} has no Turkish`).not.toBe(key);
      expect(tr(key)).not.toBe(en(key));
    });
  }

  it('TOO_MANY says how many at most', () => {
    const err = requestFailure(400, body('TOO_MANY', 'at most 1000 entries per restore', { max: 1000 }), 'en');
    expect(err.message).toContain('1000');
    expect(requestFailure(400, body('TOO_MANY', 'x', { max: 1000 }), 'tr').message).toContain('1000');
  });

  it('the explorer says a refused Cancel in those words', () => {
    // It said "could not cancel the app's job" whatever was cancelled and
    // whatever the server answered.
    const src = readFileSync(resolve(__dirname, '../../../packages/core/src/FileExplorer.vue'), 'utf8');
    const cancel = src.slice(src.indexOf('async function cancelPendingOp'), src.indexOf('async function submitNewFolder'));
    expect(cancel).toMatch(/showToast\(\{ message: archive \? archiveRequestError\(err\) : failureText\(err, t\('plugin\.cancel_failed'\)\) \}, ERROR_TOAST_MS\)/);
  });

  it('a queued purge of an entry restored meanwhile says it deleted nothing', () => {
    // The backend now leaves a live row alone (trash.ErrNotInTrash); the job's
    // error is its English, and said as "failed" it read like a lost file.
    for (const error of ['trash: the item is not in the trash', 'trash entry not found']) {
      expect(opFailure({ op_type: 'purge', error_message: error }, tr).text).toBe(tr('err.not_in_trash'));
    }
    expect(en('err.not_in_trash')).toMatch(/nothing was deleted/);
  });

  it('the admin panel reads the same words from an answer’s data', () => {
    expect(codeWords({ error: 'already finished', code: 'FINISHED' }, 'tr')).toBe(tr('err.finished'));
    expect(codeWords({ error: 'pretty' }, 'tr')).toBe('');
  });
});
