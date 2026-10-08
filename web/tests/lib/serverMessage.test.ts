// The server says the failure; every surface shows it (0.54 #209, audit A1,
// A2, A3, B15).
//
// ⚠⚠ The rule (2026-10-08): a sentence a person reads about a refusal is
// composed by the SERVER, in the reader's language (backend internal/apierr:
// `{"error": "<code>", "message": "<sentence>", "params": {…}}`; a failed
// queue row's `error_text`). The explorer, the admin panel, the desktop app,
// the CLI and an MCP agent print it as it came. The client's own words are
// only for what never reached the server.
//
// RED on the code before #209:
//   (a) the explorer threw `message` away for a code it did not know and said
//       the 403's status words ("You are not allowed to do this") where the
//       server had said WHICH role refused WHAT;
//   (b) `serverSaid` did not exist - the admin panel read `message` on its
//       own, by its own rules, beside a table of codes (`codeWords`);
//   (c) a queue row's `error_text` was ignored: the client worded the job's
//       code itself (opc.err.*) or matched its English (JOB_WORDS);
//   (d) a 423's toast was phrased by the client from the lock's fields;
//   (e) `accountChecker` did not exist - the forms judged with a mirror of
//       the server's rules.
import { afterEach, describe, expect, it, vi } from 'vitest';

import { jobFailure, requestFailure, sayFailure, serverSaid, wordsIn } from '@brftech/filex-core/src/lib/errorWords';
import { lockWords, lockedRefusal } from '@brftech/filex-core/src/lib/appLock';
import { accountChecker } from '@brftech/filex-core/src/lib/accountRules';

const en = wordsIn('en');
const ROLE_SAID = 'The role “Contractors” does not allow you to delete files.';
const BODY = JSON.stringify({ error: 'permission_denied', permission: 'files.delete', message: ROLE_SAID });

afterEach(() => vi.useRealTimers());

describe('a refusal the server said (A1)', () => {
  it('the explorer shows the server’s sentence, not the status’s words', () => {
    const err = requestFailure(403, BODY, 'en');
    expect(err.message).toBe(ROLE_SAID);
    expect(err.message).not.toBe(en('err.status.403'));
    expect(err.code).toBe('permission_denied');
    expect(sayFailure(err, 'fallback', { t: en }).text).toBe(ROLE_SAID);
  });

  it('the admin panel reads the same sentence by the same path', () => {
    expect(serverSaid(JSON.parse(BODY), 'en')).toBe(ROLE_SAID);
    expect(serverSaid(JSON.parse(BODY), 'en')).toBe(requestFailure(403, BODY, 'en').message);
    // Nothing said: nothing to show, the caller's own words follow.
    expect(serverSaid({ error: 'permission_denied' }, 'en')).toBe('');
    expect(serverSaid('<html>', 'en')).toBe('');
  });

  it('only a failure that never reached the server is said in the client’s words', () => {
    expect(requestFailure(403, '{"error":"permission_denied"}', 'en').message).toBe(en('err.status.403'));
  });
});

describe('a failed queue row (A2)', () => {
  it('shows the server’s sentence, keeping the English for an administrator', () => {
    const op = { error: 'something with that name already exists here', error_code: 'name_taken', error_text: 'Something with that name is already there.' };
    expect(jobFailure(op, 'fallback', en)).toEqual({ text: 'Something with that name is already there.' });
    expect(jobFailure(op, 'fallback', en, { callerAdmin: true }).detail).toBe(op.error);
  });
});

describe('an app’s freeze, 423 (A3)', () => {
  it('the toast is the server’s sentence', () => {
    const held = lockedRefusal({ status: 423, server: 'Echo locked this file.', detail: '{"error":"locked","plugin":"echo"}' });
    expect(lockWords(held, { t: en })).toBe('Echo locked this file.');
  });
});

describe('the account check while a form is typed (B15)', () => {
  it('asks once, for the last values, when the typing pauses', async () => {
    vi.useFakeTimers();
    const ask = vi.fn(async (q: { email?: string }) => (q.email === 'a,b@x' ? { email: { error: 'email_invalid', message: 'not an address' } } : {}));
    const check = accountChecker(ask, 200);
    const first = check({ email: 'a' });
    const last = check({ email: 'a,b@x' });
    await vi.advanceTimersByTimeAsync(250);
    expect(ask).toHaveBeenCalledTimes(1);
    expect(ask).toHaveBeenCalledWith({ email: 'a,b@x' });
    await expect(first).resolves.toBeNull();
    await expect(last).resolves.toEqual({ email: { error: 'email_invalid', message: 'not an address' } });
  });

  it('a check that could not be made says nothing (the save still judges)', async () => {
    vi.useFakeTimers();
    const check = accountChecker(async () => {
      throw new Error('offline');
    }, 100);
    const answer = check({ username: 'ada' });
    await vi.advanceTimersByTimeAsync(150);
    await expect(answer).resolves.toBeNull();
  });

  it('an answer that arrives after a newer question is dropped', async () => {
    vi.useFakeTimers();
    let release!: (v: Record<string, never>) => void;
    const ask = vi
      .fn()
      .mockImplementationOnce(() => new Promise((r) => (release = r)))
      .mockImplementation(async () => ({}));
    const check = accountChecker(ask, 50);
    const slow = check({ email: 'slow@x' });
    await vi.advanceTimersByTimeAsync(60);
    const fresh = check({ email: 'fresh@x' });
    release({});
    await expect(slow).resolves.toBeNull();
    await vi.advanceTimersByTimeAsync(60);
    await expect(fresh).resolves.toEqual({});
  });
});
