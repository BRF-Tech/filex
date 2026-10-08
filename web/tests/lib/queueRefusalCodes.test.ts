// The queue's doors answer with a code a person can be told about, and the
// SERVER says it: `{"error": "<code>", "code": "<CODE>", "message": "<the
// reader's sentence>"}` (backend internal/apierr, docs/API-ERRORS.md). The
// explorer and the admin panel show that `message`, one helper each
// (lib/errorWords `requestFailure` / `serverSaid`).
//
// The backend review (fix/berk-backend) added: BAD_KIND (400 - the generic
// POST /api/files/ops takes copy, move and delete only), READ_ONLY (403 - a
// queued move, delete or copy touching a read-only storage), NOT_CANCELLABLE
// (409 - a rename, restore or purge cannot be stopped once it has started),
// FINISHED (409 - cancelling a job that already ended) and TOO_MANY (400 - a
// queued restore of more than `max` entries). Their `error` field was the
// server's English, and the client kept a table of codes to word them
// (CODE_FIELD_WORDS). ⚠⚠ 0.54 (#209): the server words them; the table is
// gone, and the upper-case `code` stays for the clients that read it.
import { describe, expect, it } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

import { opFailure, requestFailure, sayFailure, serverSaid, wordsIn } from '@brftech/filex-core/src/lib/errorWords';

const en = wordsIn('en');
const tr = wordsIn('tr');

const body = (error: string, code: string, message: string, extra: Record<string, unknown> = {}) =>
  JSON.stringify({ error, code, message, ...extra });

describe('a queue refusal', () => {
  const cases = [
    [400, 'bad_kind', 'BAD_KIND', 'Sunucu bu türde bir işi bu yoldan kuyruğa almıyor.'],
    [403, 'read_only', 'READ_ONLY', 'Bu depo salt okunur.'],
    [409, 'not_cancellable', 'NOT_CANCELLABLE', 'Bu iş başladıktan sonra durdurulamaz - kendiliğinden tamamlanır.'],
    [409, 'finished', 'FINISHED', 'Bu iş zaten bitti.'],
  ] as const;

  for (const [status, error, code, said] of cases) {
    it(`${code} is the server's sentence, as it came`, () => {
      const err = requestFailure(status, body(error, code, said), 'tr');
      expect(err.message).toBe(said);
      expect(err.code).toBe(error);
      // Said again by a caller with its own translator: still the server's
      // words - it wrote them in the reader's language already.
      expect(sayFailure(err, en('toast.failed'), { t: tr }).text).toBe(said);
      expect(err.message).not.toBe(tr(`err.status.${status}`));
    });
  }

  it('a refusal with no sentence is said by its status, never from its code', () => {
    // RED before 0.54: CODE_FIELD_WORDS turned `code: FINISHED` into the
    // client's own words. Now only the server's `message` says more than the
    // status.
    const err = requestFailure(409, JSON.stringify({ error: 'already finished', code: 'FINISHED' }), 'tr');
    expect(err.message).toBe(tr('err.status.409'));
  });

  it('TOO_MANY says how many at most - the server fills the number', () => {
    const err = requestFailure(
      400,
      body('too_many', 'TOO_MANY', 'At most 1000 items can be restored at once. Restore them in smaller groups.', {
        max: 1000,
        params: { max: '1000' },
      }),
      'en',
    );
    expect(err.message).toContain('1000');
  });

  it('the explorer says a refused Cancel in those words', () => {
    // It said "could not cancel the app's job" whatever was cancelled and
    // whatever the server answered.
    const src = readFileSync(resolve(__dirname, '../../../packages/core/src/FileExplorer.vue'), 'utf8');
    const cancel = src.slice(src.indexOf('async function cancelPendingOp'), src.indexOf('async function submitNewFolder'));
    expect(cancel).toMatch(/showToast\(\{ message: archive \? archiveRequestError\(err\) : failureText\(err, t\('plugin\.cancel_failed'\)\) \}, ERROR_TOAST_MS\)/);
  });

  it('a queued purge of an entry restored meanwhile says it deleted nothing - in the server’s words', () => {
    // The backend leaves a live row alone (trash.ErrNotInTrash) and keeps the
    // code `not_in_trash` on the row; said as "failed" it read like a lost file.
    const said = 'Artık çöpte değil - bu arada geri getirilmiş ya da kaldırılmış; hiçbir şey silinmedi.';
    const row = { op_type: 'purge', error_message: 'trash entry not found', error_code: 'not_in_trash', error_text: said };
    expect(opFailure(row, tr).text).toBe(said);
  });

  it('the admin panel reads the same sentence from an answer’s data', () => {
    const said = 'Bu iş zaten bitti.';
    expect(serverSaid({ error: 'finished', code: 'FINISHED', message: said }, 'tr')).toBe(said);
    expect(serverSaid({ error: 'finished', code: 'FINISHED', message: said }, 'tr')).toBe(
      requestFailure(409, body('finished', 'FINISHED', said), 'tr').message,
    );
    expect(serverSaid({ error: 'pretty' }, 'tr')).toBe('');
    expect(serverSaid(null, 'tr')).toBe('');
  });
});
