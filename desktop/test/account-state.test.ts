// The account store's rules — the part of src/accounts.ts that is not the
// keychain.
//
// Run:  node --experimental-strip-types --test desktop/test/account-state.test.ts
//
// The watcher that syncs an account's folders is started with that account's
// token in its ENVIRONMENT. So "who is signed in, with which token" is not
// bookkeeping: get it wrong and a process keeps syncing with a credential the
// user already took away, or keeps failing with one the server already
// revoked while the app holds a fresh one.

import assert from 'node:assert/strict';
import test from 'node:test';

import {
  EMPTY_STATE,
  findAccount,
  removeAccount,
  signIn,
  type DesktopState,
} from '../src/account-state.ts';
import { wantedWatchers } from '../src/sync-policy.ts';

function fresh(): DesktopState {
  return structuredClone(EMPTY_STATE);
}

test('signing in again to the same server as the same person is the SAME account', () => {
  const s = fresh();
  const first = signIn(s, { serverUrl: 'https://files.example.com', email: 'Ada@Example.com', token: 'old' });
  assert.equal(first.existed, false);
  first.account.syncRoot = '/home/ada/filex/files.example.com';
  first.account.openWithStorage = 'docs';

  // A trailing slash and a different case are the same server and person.
  const again = signIn(s, { serverUrl: 'https://FILES.example.com/', email: 'ada@example.com', token: 'new' });
  assert.equal(again.existed, true);
  assert.equal(again.account.id, first.account.id, 'the id — which the sync pairs are recorded against — survives');
  assert.equal(again.account.token, 'new');
  assert.equal(again.account.syncRoot, '/home/ada/filex/files.example.com');
  assert.equal(again.account.openWithStorage, 'docs');
  assert.equal(s.accounts.length, 1);
  assert.equal(s.activeId, first.account.id);
});

test('findAccount matches the way signIn does', () => {
  const s = fresh();
  const { account } = signIn(s, { serverUrl: 'https://a.example', email: 'x@a.example', token: 't' });
  assert.equal(findAccount(s, 'https://A.example/', 'X@a.example')?.id, account.id);
  assert.equal(findAccount(s, 'https://b.example', 'x@a.example'), null);
});

test('a signed-out account has no watcher — its pairs stay, inert', () => {
  const s = fresh();
  const a = signIn(s, { serverUrl: 'https://a.example', email: 'x@a.example', token: 't1' }).account;
  const b = signIn(s, { serverUrl: 'https://b.example', email: 'y@b.example', token: 't2' }).account;
  const pairs = [
    { id: 'pair-1', account: a.id },
    { id: 'pair-2', account: b.id },
  ];
  assert.deepEqual([...wantedWatchers(s.accounts, pairs)].sort(), [a.id, b.id].sort());

  removeAccount(s, a.id);
  assert.deepEqual([...wantedWatchers(s.accounts, pairs)], [b.id]);
  assert.equal(s.activeId, b.id);
});

test('an account whose folders are all paused has no watcher either', () => {
  const s = fresh();
  const a = signIn(s, { serverUrl: 'https://a.example', email: 'x@a.example', token: 't' }).account;
  assert.deepEqual([...wantedWatchers(s.accounts, [{ id: 'pair-1', account: a.id, paused: true }])], []);
  assert.deepEqual([...wantedWatchers(s.accounts, [])], []);
});
