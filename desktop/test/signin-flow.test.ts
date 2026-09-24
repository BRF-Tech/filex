// Issue #36 — "the desktop app drops to the link state after a failed login".
//
// Run:  node --experimental-strip-types --test desktop/test/signin-flow.test.ts
//
// The sign-in window used to draw its waiting screen (address to copy, code
// box) from a variable in the page, and it took a pending attempt back only on
// the `#/reconnect` route. Any reload — a failed sign-in link re-opened the
// window on `#/connect`, and so did the tray, the Dock and a second launch —
// put the server-address form on screen while the attempt was still pending
// behind it, unreachable. v0.42.2's rule, written out, is `v0422View` below;
// every case marked "the report" is RED against it.
//
// The whole end-to-end path (a real window, a real server, a failed link, a
// reload, then a finished sign-in) is desktop/scripts/signin-retry-e2e.mjs.

import assert from 'node:assert/strict';
import test from 'node:test';

import { failureOf, signInView, type SignInAttempt, type SignInView } from '../src/signin-flow.ts';

const ATTEMPT: SignInAttempt = {
  serverUrl: 'https://files.example.org',
  authUrl: 'https://files.example.org/admin/login?desktop_state=s1&desktop_challenge=c1',
};

/** What v0.42.2's page drew after a (re)load: the attempt only on #/reconnect. */
function v0422View(pending: SignInAttempt | null, hash: string): SignInView['view'] {
  return pending && hash.startsWith('#/reconnect') ? 'waiting' : 'connect';
}

test('the report: a failed sign-in link keeps the waiting screen of the attempt', () => {
  // The window is re-opened after the failure; v0.42.2 re-opened it on #/connect.
  const v = signInView(ATTEMPT, failureOf('s1', 's1', new Error('exchange failed (403): code or verifier mismatch')));
  assert.equal(v.view, 'waiting');
  assert.notEqual(v0422View(ATTEMPT, '#/connect'), v.view, 'v0.42.2 would have drawn the server form here');
  assert.equal(v.view === 'waiting' && v.authUrl, ATTEMPT.authUrl, 'the SAME attempt, not a new one');
  assert.equal(v.failure?.kind, 'spent');
});

test('the report: the tray, the Dock or a second launch reloading the window keep it too', () => {
  // route() opens the window on #/connect whatever is pending; the page reloads.
  const v = signInView(ATTEMPT, null);
  assert.equal(v.view, 'waiting');
  assert.equal(v0422View(ATTEMPT, '#/connect'), 'connect', 'v0.42.2 dropped it');
});

test('a link from an earlier attempt is "stale" — the current one is still good', () => {
  assert.equal(failureOf('s2', 's1', new Error('authorization does not match the pending request')).kind, 'stale');
  assert.equal(failureOf(null, 's1', new Error('no sign-in is waiting — start again')).kind, 'stale');
});

test('a refused exchange is "spent" — only a new attempt can finish', () => {
  const f = failureOf('s1', 's1', new Error('exchange failed (400): unknown or expired authorization'));
  assert.equal(f.kind, 'spent');
  assert.match(f.detail, /unknown or expired/);
});

test('nothing pending: the server form, with the reason when there was one', () => {
  assert.deepEqual(signInView(null, null), { view: 'connect', failure: null });
  const f = failureOf(null, 'old', new Error('no sign-in is waiting — start again'));
  assert.deepEqual(signInView(null, f), { view: 'connect', failure: f });
});
