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
import fs from 'node:fs';
import path from 'node:path';
import test from 'node:test';
import { fileURLToPath } from 'node:url';

import { keychainAdvice, keychainCommand, keychainState, snapConnectCommand } from '../src/keychain.ts';

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

test("in a snap, snapd's answer about the keyring plug picks the advice", () => {
  // `snapctl is-connected password-manager-service` said no: the command is
  // the fix.
  assert.equal(keychainAdvice('unavailable', 'linux', 'snap', 'disconnected'), 'snap-connect');
  assert.equal(keychainAdvice('plaintext', 'linux', 'snap', 'disconnected'), 'snap-connect');
  // It said yes: running the command again changes nothing, the keyring
  // itself did not answer.
  assert.equal(keychainAdvice('unavailable', 'linux', 'snap', 'connected'), 'linux-keyring');
  assert.equal(keychainAdvice('plaintext', 'linux', 'snap', 'connected'), 'linux-keyring');
  // No answer (no snapctl, a timeout, another exit code): the advice as it
  // was before snapd was asked.
  assert.equal(keychainAdvice('unavailable', 'linux', 'snap', 'unknown'), 'snap-connect');
  // A working keychain needs no advice, whatever the plug.
  assert.equal(keychainAdvice('ok', 'linux', 'snap', 'disconnected'), null);
  // Outside a snap the plug means nothing.
  assert.equal(keychainAdvice('unavailable', 'linux', null, 'disconnected'), 'linux-keyring');
});

test('only the snap advice comes with a command', () => {
  const env = { SNAP: '/snap/filex-app/42', SNAP_NAME: 'filex-app' };
  assert.equal(keychainCommand('snap-connect', env), 'sudo snap connect filex-app:password-manager-service');
  assert.equal(keychainCommand('linux-keyring', env), null);
  assert.equal(keychainCommand('os-storage', env), null);
  assert.equal(keychainCommand(null, env), null);
});

test("a snap is told the command for ITS name, from snapd — never a typed-in one", () => {
  // The snap was renamed once (filex → filex-app); a hard-coded name would
  // send the user to `snap connect` a snap that is not installed.
  assert.equal(snapConnectCommand({ SNAP_NAME: 'filex-app' }), 'sudo snap connect filex-app:password-manager-service');
  // A parallel install (`snap install filex-app_beta`) is addressed by its
  // instance name.
  assert.equal(
    snapConnectCommand({ SNAP_NAME: 'filex-app', SNAP_INSTANCE_NAME: 'filex-app_beta' }),
    'sudo snap connect filex-app_beta:password-manager-service',
  );
  assert.equal(snapConnectCommand({ SNAP_NAME: 'filex-app' }, 'removable-media'), 'sudo snap connect filex-app:removable-media');
  // Not a snap: no command to show.
  assert.equal(snapConnectCommand({}), null);
});

/**
 * The sign-in window's notice (ui/index.html keychainNotice), as the page runs
 * it in `lang`: its own STRINGS, T and esc, read from the page's source.
 */
function signInNotice(lang: 'en' | 'tr'): (state: Record<string, unknown>) => string {
  const html = fs.readFileSync(path.join(path.dirname(fileURLToPath(import.meta.url)), '..', 'ui', 'index.html'), 'utf8');
  const between = (from: string, to: string) => {
    const start = html.indexOf(from);
    assert.ok(start >= 0, `ui/index.html has no ${from}`);
    const end = html.indexOf(to, start);
    assert.ok(end >= 0, `ui/index.html: ${from} does not end`);
    return html.slice(start, end + to.length);
  };
  const fn = (name: string) => {
    const start = html.indexOf(`function ${name}(`);
    assert.ok(start >= 0, `ui/index.html has no function ${name}`);
    let depth = 0;
    for (let i = html.indexOf('{', start); i < html.length; i++) {
      if (html[i] === '{') depth++;
      else if (html[i] === '}' && --depth === 0) return html.slice(start, i + 1);
    }
    throw new Error(`ui/index.html: function ${name} does not end`);
  };
  const src = [
    between('const esc = ', ');\n'),
    between('const STRINGS = {', '\n      };'),
    fn('T'),
    'let state = null;',
    fn('keychainNotice'),
    'return (s) => { state = s; return keychainNotice(); };',
  ].join('\n');
  return new Function('LANG', src)(lang) as (state: Record<string, unknown>) => string;
}

test('the sign-in window says what connecting the keyring plug allows BEFORE the command', () => {
  // The Snap Store left the plug to the person on this condition: they decide
  // knowing that the snap reaches the keyring's secrets, and that other apps
  // reaching the keyring can read what filex puts there.
  const state = {
    keychainAdvice: 'snap-connect',
    keychainCommand: 'sudo snap connect filex-app:password-manager-service',
  };
  for (const [lang, allows, keyring] of [
    ['en', 'The connection gives this snap access to the secrets stored in your keyring', 'other apps that can reach your keyring can also read the secrets filex puts there'],
    ['tr', 'Bu bağlantı snap’e anahtarlığında saklanan sırlara erişim verir', 'anahtarlığa erişebilen diğer uygulamalar da filex’in oraya koyduğu sırları okuyabilir'],
  ] as const) {
    const html = signInNotice(lang)(state);
    const said = html.indexOf(allows);
    const others = html.indexOf(keyring);
    const command = html.indexOf('<code>sudo snap connect filex-app:password-manager-service</code>');
    assert.ok(said > 0, `${lang}: what the connection allows is not said:\n${html}`);
    assert.ok(others > 0, `${lang}: that other apps can read filex's secrets is not said:\n${html}`);
    assert.ok(command > others, `${lang}: the command comes before the explanation:\n${html}`);
    assert.ok(html.includes('GNOME Keyring') && html.includes('KWallet'), lang);
  }
});

test('the keyring advice for a connected plug (or no snap) shows no command and no plug talk', () => {
  for (const lang of ['en', 'tr'] as const) {
    const html = signInNotice(lang)({ keychainAdvice: 'linux-keyring', keychainCommand: null });
    assert.ok(html.includes('GNOME Keyring'), lang);
    assert.equal(html.includes('<code>'), false, lang);
    assert.equal(html.includes('snap'), false, lang);
  }
  assert.equal(signInNotice('en')({ keychainAdvice: null }), '');
});
