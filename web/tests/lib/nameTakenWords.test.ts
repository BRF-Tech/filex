// "Something with that name is already there" - only for the queue jobs that
// found a name taken, never for a refusal that merely MENTIONS "already exists".
//
// ⚠ #61 added `/already exists/i → err.name_taken` to the one table every
// HTTP refusal goes through (refusalWords, via requestFailure). New document's
// 503 EXISTS_CHECK_FAILED - "could not check whether that file already
// exists…", an outage - then told the person a file of that name was there,
// and to rename it.
//
// ⚠⚠ 0.54 (#209, audit A2): the client keeps no table of the server's English
// any more. A queue row that found the name taken carries the CODE
// (`error_code: name_taken`) and the server's sentence in the reader's
// language (`error_text`, backend ops/errcode.go); a refusal that merely
// mentions "already exists" is said by its own `message`, or the status's
// words when it has none. Nothing is recognised by its English.
import { describe, expect, it } from 'vitest';

import { opFailure, requestFailure, sayFailure, wordsIn } from '@brftech/filex-core/src/lib/errorWords';

const en = wordsIn('en');
const tr = wordsIn('tr');
/** What the server says for `name_taken` (srvtext server.error.name_taken). */
const TAKEN_EN = 'Something with that name is already there. Rename what is there, then try again.';
const TAKEN_TR = 'Aynı adla bir şey zaten var. Oradakinin adını değiştirip yeniden deneyin.';

describe('a refusal that mentions "already exists"', () => {
  it('is not said as a taken name when the server could not check', () => {
    const body = JSON.stringify({
      error: 'could not check whether that file already exists: storage unreachable',
      code: 'EXISTS_CHECK_FAILED',
    });
    const said = sayFailure(requestFailure(503, body, 'en'), en('toast.failed'), { t: en }).text;
    expect(said).not.toBe(TAKEN_EN);
    expect(said).toBe(en('err.status.503'));
  });

  it('keeps an archive or plugin conflict in its own words', () => {
    const archive = JSON.stringify({ error: 'an item named x already exists; choose another name or remove it first' });
    const said = sayFailure(requestFailure(409, archive, 'en'), en('toast.failed'), { t: en }).text;
    expect(said).not.toBe(TAKEN_EN);
    expect(said).toBe(en('err.status.409'));
  });
});

describe('a queue job that found the name taken', () => {
  it('a queued rename is said in the server’s sentence, in the reader’s language', () => {
    const op = { op_type: 'rename', error_message: 'something with that name already exists here', error_code: 'name_taken' };
    expect(opFailure({ ...op, error_text: TAKEN_EN }, en).text).toBe(TAKEN_EN);
    expect(opFailure({ ...op, error_text: TAKEN_TR }, tr).text).toBe(TAKEN_TR);
  });

  it('a queued restore is said in the server’s sentence; its English stays the admin’s line', () => {
    const op = {
      op_type: 'restore',
      error_message: 'something already exists at this path: rapor.pdf',
      error_code: 'name_taken',
      error_text: TAKEN_EN,
    };
    expect(opFailure(op, en)).toEqual({ text: TAKEN_EN });
    expect(opFailure(op, en, { callerAdmin: true }).detail).toBe(op.error_message);
  });

  it('a row with no sentence is never recognised by its English - it is the general words', () => {
    // RED before 0.54: JOB_WORDS matched "something with that name already
    // exists here" and said err.name_taken; the server now says it.
    for (const error_message of [
      'something with that name already exists here',
      'could not tell whether the name is free: database is locked',
    ]) {
      expect(opFailure({ op_type: 'rename', error_message }, en).text).toBe(en('toast.failed'));
    }
  });
});
