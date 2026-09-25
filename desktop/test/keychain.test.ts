// Whether the account store may be written — the promise that an account's
// token is never left on disk in a form anyone could read — and what the
// sign-in window says when it may not.
//
// Run:  node --experimental-strip-types --test desktop/test/keychain.test.ts
//
// Measured on the v0.43.0 .deb (GNOME session, no keyring): the refusal came
// only after the browser round trip had spent the one-time code, and the
// account stayed in the running app's state. The window now knows before a
// sign-in starts — which is only as good as this decision.

import assert from 'node:assert/strict';
import test from 'node:test';

import { keychainAdvice, keychainState, snapConnectCommand } from '../src/keychain.ts';

test("Linux's basic_text is refused even when Electron calls it available", () => {
  // Electron 31 answers `available: false` for it (measured with
  // --password-store=basic); setUsePlainTextEncryption, or another Electron,
  // answers true — and the "encryption" is a key printed in Chromium's source.
  assert.equal(keychainState({ platform: 'linux', available: true, backend: 'basic_text' }), 'plaintext');
  assert.equal(keychainState({ platform: 'linux', available: false, backend: 'basic_text' }), 'unavailable');
});

test('a real Linux keyring is used', () => {
  for (const backend of ['gnome_libsecret', 'kwallet', 'kwallet5', 'kwallet6']) {
    assert.equal(keychainState({ platform: 'linux', available: true, backend }), 'ok', backend);
  }
});

test('a Linux keyring that does not answer is refused', () => {
  // GNOME session, no gnome-keyring running (measured): libsecret selected,
  // encryption unavailable.
  assert.equal(keychainState({ platform: 'linux', available: false, backend: 'gnome_libsecret' }), 'unavailable');
});

test('a Linux backend that cannot be named yet is not trusted', () => {
  // `unknown` is what Electron answers before `ready`; no answer at all is no
  // better. "Store it anyway" is the wrong default for a token.
  assert.equal(keychainState({ platform: 'linux', available: true, backend: 'unknown' }), 'unavailable');
  assert.equal(keychainState({ platform: 'linux', available: true, backend: null }), 'unavailable');
  assert.equal(keychainState({ platform: 'linux', available: true }), 'unavailable');
});

test('no encryption at all is refused everywhere', () => {
  for (const platform of ['linux', 'win32', 'darwin']) {
    assert.equal(keychainState({ platform, available: false, backend: 'gnome_libsecret' }), 'unavailable', platform);
  }
});

test('Windows and macOS have no fallback to refuse: available means the OS protects it', () => {
  // DPAPI and the macOS Keychain; getSelectedStorageBackend is Linux-only and
  // its answer must not leak into the decision there.
  assert.equal(keychainState({ platform: 'win32', available: true }), 'ok');
  assert.equal(keychainState({ platform: 'darwin', available: true, backend: null }), 'ok');
  assert.equal(keychainState({ platform: 'win32', available: true, backend: 'basic_text' }), 'ok');
});

test('the advice names the fix that applies here', () => {
  assert.equal(keychainAdvice('ok', 'linux', 'snap'), null);
  // A snap's keyring plug does not connect by itself: one command fixes it.
  assert.equal(keychainAdvice('unavailable', 'linux', 'snap'), 'snap-connect');
  assert.equal(keychainAdvice('plaintext', 'linux', 'snap'), 'snap-connect');
  for (const ch of [null, 'aur', 'flatpak']) assert.equal(keychainAdvice('unavailable', 'linux', ch), 'linux-keyring');
  assert.equal(keychainAdvice('plaintext', 'linux', null), 'linux-keyring');
  assert.equal(keychainAdvice('unavailable', 'win32', null), 'os-storage');
  assert.equal(keychainAdvice('unavailable', 'darwin', null), 'os-storage');
});

test("a snap is told the command for ITS name, from snapd — never a typed-in one", () => {
  // The snap was renamed once (filex → filex-app); a hard-coded name would
  // send the user to `snap connect` a snap that is not installed.
  assert.equal(snapConnectCommand({ SNAP_NAME: 'filex-app' }), 'snap connect filex-app:password-manager-service');
  // A parallel install (`snap install filex-app_beta`) is addressed by its
  // instance name.
  assert.equal(
    snapConnectCommand({ SNAP_NAME: 'filex-app', SNAP_INSTANCE_NAME: 'filex-app_beta' }),
    'snap connect filex-app_beta:password-manager-service',
  );
  assert.equal(snapConnectCommand({ SNAP_NAME: 'filex-app' }, 'removable-media'), 'snap connect filex-app:removable-media');
  // Not a snap: no command to show.
  assert.equal(snapConnectCommand({}), null);
});
