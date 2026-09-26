// "Something with that name is already there" — only for the queue jobs that
// found a name taken, never for a refusal that merely MENTIONS "already exists".
//
// ⚠ #61 added `/already exists/i → err.name_taken` to the one table every
// HTTP refusal goes through (refusalWords, via requestFailure). New document's
// 503 EXISTS_CHECK_FAILED — "could not check whether that file already
// exists…", an outage — then told the person a file of that name was there,
// and to rename it. The mapping belongs to the two job errors it was written
// for: ops.ErrNameTaken (a queued rename) and handlers.Trash RestoreNode (a
// queued restore).
import { describe, expect, it } from 'vitest';

import { opFailure, requestFailure, sayFailure, wordsIn } from '@brftech/filex-core/src/lib/errorWords';

const en = wordsIn('en');
const tr = wordsIn('tr');
const TAKEN_EN = en('err.name_taken');

describe('a refusal that mentions "already exists"', () => {
  it('is not said as a taken name when the server could not check', () => {
    const body = JSON.stringify({
      error: 'could not check whether that file already exists: storage unreachable',
      code: 'EXISTS_CHECK_FAILED',
    });
    const said = sayFailure(requestFailure(503, body, 'en'), en('toast.failed'), { t: en }).text;
    expect(said).not.toBe(TAKEN_EN);
  });

  it('keeps an archive or plugin conflict in its own words', () => {
    const archive = JSON.stringify({ error: 'an item named x already exists; choose another name or remove it first' });
    expect(sayFailure(requestFailure(409, archive, 'en'), en('toast.failed'), { t: en }).text).not.toBe(TAKEN_EN);
  });
});

describe('a queue job that found the name taken', () => {
  it('a queued rename is said as a taken name, in English and Turkish', () => {
    const op = { op_type: 'rename', error_message: 'something with that name already exists here' };
    expect(opFailure(op, en).text).toBe(TAKEN_EN);
    expect(opFailure(op, tr).text).toBe(tr('err.name_taken'));
  });

  it('a queued restore is said as a taken name', () => {
    const op = { op_type: 'restore', error_message: 'something already exists at this path: rapor.pdf' };
    expect(opFailure(op, en).text).toBe(TAKEN_EN);
  });

  it('a job that failed for another reason keeps the general words', () => {
    const op = { op_type: 'rename', error_message: 'could not tell whether the name is free: database is locked' };
    expect(opFailure(op, en).text).not.toBe(TAKEN_EN);
  });
});
