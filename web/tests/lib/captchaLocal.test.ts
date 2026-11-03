// The local captcha fallback, against the SHARED vectors (#294).
//
// ⚠ The vectors are the operator's cross-product contract (the local-fallback
// spec; the same file gates the server's Go verifier and the other products'
// clients). They are READ from the backend's testdata, not copied: a copy here
// would go on passing after the contract changed.
//
// Two halves of the browser's side are held to them:
//   1. the worker's own text (CAPTCHA_WORKER_SOURCE, exactly what the `blob:`
//      worker runs) finds every positive vector's number within its maxnumber;
//   2. the solution the form sends is byte for byte the vector's base64.
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

import { describe, expect, it } from 'vitest';

import {
  captchaAnswerOf,
  captchaFormOn,
  captchaUrl,
  challengeOf,
  encodeCaptchaSolution,
  solveCaptchaChallenge,
  turnstileLanguage,
  type CaptchaChallenge,
} from '@brftech/filex-core/src/lib/captcha';
import { inThreadWorker } from '../helpers/captchaWorker';

interface Vector {
  id: string;
  kind: 'positive' | 'negative';
  input: { salt: string; number: number; maxnumber: number; expires: number };
  challenge: CaptchaChallenge;
  solution: { payload_json: string; payload_base64: string };
  expected: { verify: { ok: boolean; reason: string } };
}

const VECTORS = resolve(__dirname, '../../../backend/internal/captcha/testdata/captcha-local-vectors.json');
const file = JSON.parse(readFileSync(VECTORS, 'utf8')) as { algorithm: string; vectors: Vector[] };
const positives = file.vectors.filter((v) => v.kind === 'positive');

describe('the shared vectors', () => {
  it('are there to be read (a test over nothing proves nothing)', () => {
    expect(file.algorithm).toBe('SHA-256');
    expect(positives.length).toBeGreaterThanOrEqual(7);
  });

  it.each(positives.map((v) => [v.id, v] as const))('%s: the worker finds the number', async (_id, v) => {
    const solved = await solveCaptchaChallenge(v.challenge, { createWorker: inThreadWorker });
    expect(solved.number).toBe(v.input.number);
    expect(solved.number).toBeLessThanOrEqual(v.challenge.maxnumber);
  });

  // ⚠ P6 is the verifier's case: its SOLUTION was sent with an upper-case hash
  // and signature on purpose (the server lowers them). A browser sends the
  // challenge's fields as it got them, so P6 is not a payload it would make.
  const asSent = positives.filter((v) => v.id !== 'P6');
  it.each(asSent.map((v) => [v.id, v] as const))('%s: the solution is the vector’s base64, byte for byte', (_id, v) => {
    expect(encodeCaptchaSolution(v.challenge, v.input.number)).toBe(v.solution.payload_base64);
    // and it decodes to the five fields, number an integer (vector N11 refuses a string)
    const back = JSON.parse(atob(v.solution.payload_base64)) as Record<string, unknown>;
    expect(Object.keys(back).sort()).toEqual(['algorithm', 'hash', 'number', 'salt', 'signature']);
    expect(Number.isInteger(back.number)).toBe(true);
  });

  it('P6: an upper-case hash is searched for case-blind and sent as it came', async () => {
    const p6 = positives.find((v) => v.id === 'P6')!;
    const upper = { ...p6.challenge, hash: p6.challenge.hash.toUpperCase(), signature: p6.challenge.signature.toUpperCase() };
    const solved = await solveCaptchaChallenge(upper, { createWorker: inThreadWorker });
    expect(solved.number).toBe(p6.input.number);
    // the vector's own payload is exactly that: the upper-case fields, as they came
    expect(solved.payload).toBe(p6.solution.payload_base64);
  });

  it('a number past maxnumber is not found (the server refuses it as bad_number, N5)', async () => {
    const n5 = file.vectors.find((v) => v.id === 'N5')!;
    expect(n5.input.number).toBeGreaterThan(n5.challenge.maxnumber);
    await expect(solveCaptchaChallenge(n5.challenge, { createWorker: inThreadWorker })).rejects.toThrow('not_found');
  });

  it('a challenge that is not one is refused before any work', async () => {
    const bad = { ...positives[0].challenge, hash: 'not-hex' };
    await expect(solveCaptchaChallenge(bad, { createWorker: inThreadWorker })).rejects.toThrow('bad_challenge');
  });

  it('progress is reported as the scan goes and ends at 100', async () => {
    const p4 = positives.find((v) => v.id === 'P4')!;
    const seen: number[] = [];
    await solveCaptchaChallenge(p4.challenge, { createWorker: inThreadWorker, onProgress: (p) => seen.push(p) });
    expect(seen.length).toBeGreaterThan(2);
    expect(seen.at(-1)).toBe(100);
  });

  it('stopping the form stops the worker', async () => {
    const ctl = new AbortController();
    ctl.abort();
    await expect(
      solveCaptchaChallenge(positives[0].challenge, { createWorker: inThreadWorker, signal: ctl.signal }),
    ).rejects.toThrow('aborted');
  });
});

describe('reading the server', () => {
  it('builds the two routes under a server root, sub-path included', () => {
    expect(captchaUrl('', 'config')).toBe('/api/captcha/config');
    expect(captchaUrl('/filex/', 'local/challenge')).toBe('/filex/api/captcha/local/challenge');
  });

  it('a form is protected only when the provider is on and the form is listed', () => {
    const on = { provider: 'turnstile', forms: ['login', 'share_password'] } as never;
    expect(captchaFormOn(on, 'login')).toBe(true);
    expect(captchaFormOn(on, 'file_request')).toBe(false);
    expect(captchaFormOn({ provider: 'none', forms: ['login'] } as never, 'login')).toBe(false);
    expect(captchaFormOn(null, 'login')).toBe(false);
  });

  it('tells the three answers of a protected request apart, and leaves the rest alone', () => {
    const challenge = positives[0].challenge;
    expect(captchaAnswerOf({ status: 409, body: { captcha: 'fallback', challenge } })).toBe('fallback');
    expect(captchaAnswerOf({ status: 409, body: { captcha: 'fallback' } })).toBeNull();
    expect(captchaAnswerOf({ status: 422, body: { error: 'captcha_failed', captcha: 'invalid_token' } })).toBe('refused');
    expect(captchaAnswerOf({ status: 503, body: { captcha: 'unavailable' } })).toBe('unavailable');
    expect(captchaAnswerOf({ status: 401, body: { error: 'pin_wrong' } })).toBeNull();
    expect(captchaAnswerOf({ status: 422, body: { error: 'missing_pass' } })).toBeNull();
    expect(captchaAnswerOf(null)).toBeNull();
  });

  it('a challenge is read only when it has its fields', () => {
    expect(challengeOf({ salt: 's?expires=1', hash: 'h', signature: 'g', maxnumber: 10 })).toMatchObject({ algorithm: 'SHA-256' });
    expect(challengeOf({ salt: 's', hash: 'h', signature: 'g', maxnumber: -1 })).toBeNull();
    expect(challengeOf('nope')).toBeNull();
  });

  it('asks Turnstile for a language it has, and for `auto` otherwise', () => {
    expect(turnstileLanguage('tr')).toBe('tr');
    expect(turnstileLanguage('pt-br')).toBe('pt');
    expect(turnstileLanguage('xx')).toBe('auto');
    expect(turnstileLanguage(undefined)).toBe('auto');
  });
});
