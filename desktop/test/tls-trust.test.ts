import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

import { EMPTY_STATE, signIn, type DesktopState } from '../src/account-state.ts';
import {
  CERT_ACCEPT,
  CERT_CHROMIUMS_ANSWER,
  CertAnswers,
  ENV_SKIP_TLS_VERIFY,
  TLS_WORDS,
  certVerdict,
  engineTlsEnv,
  skippedHosts,
} from '../src/tls-trust.ts';

const here = path.dirname(fileURLToPath(import.meta.url));
const read = (rel: string) => readFileSync(path.join(here, '..', '..', rel), 'utf8');

function state(): DesktopState {
  return structuredClone(EMPTY_STATE);
}

test('a connection is checked unless its own switch says otherwise', () => {
  const s = state();
  signIn(s, { serverUrl: 'https://nas.example.com:8443', email: 'a@example.com', token: 't', insecureSkipTLSVerify: true });
  signIn(s, { serverUrl: 'https://files.example.com', email: 'a@example.com', token: 't' });

  const hosts = skippedHosts(s, null);

  assert.equal(certVerdict('nas.example.com', hosts), CERT_ACCEPT);
  assert.equal(certVerdict('NAS.example.com', hosts), CERT_ACCEPT);
  assert.equal(certVerdict('files.example.com', hosts), CERT_CHROMIUMS_ANSWER);
  assert.equal(certVerdict('anything.else', hosts), CERT_CHROMIUMS_ANSWER, 'the update feed, the store, everything else');
});

test('a sign-in under way counts before it has an account', () => {
  const s = state();
  assert.equal(skippedHosts(s, null).size, 0);

  const pending = { serverUrl: 'https://nas.example.com', insecureSkipTLSVerify: true };
  assert.equal(certVerdict('nas.example.com', skippedHosts(s, pending)), CERT_ACCEPT);

  const checked = { serverUrl: 'https://nas.example.com' };
  assert.equal(certVerdict('nas.example.com', skippedHosts(s, checked)), CERT_CHROMIUMS_ANSWER);
});

test('signing in again takes the answer of that sign-in', () => {
  const s = state();
  const first = signIn(s, { serverUrl: 'https://nas.example.com', email: 'a@example.com', token: 't1', insecureSkipTLSVerify: true });
  assert.equal(first.account.insecureSkipTLSVerify, true);

  const again = signIn(s, { serverUrl: 'https://nas.example.com', email: 'a@example.com', token: 't2' });

  assert.equal(again.existed, true);
  assert.equal(again.account.token, 't2');
  assert.equal(again.account.insecureSkipTLSVerify, undefined, 'the box was left empty: the check is on again');
});

// Chromium keeps its first answer for the life of the process, in both
// directions (measured, see src/tls-trust.ts). What it keeps is what Settings
// has to be honest about.
test('a switch turned off while a certificate stands accepted asks for a restart', () => {
  const answers = new CertAnswers();
  answers.note('NAS.example.com', -202, true);

  assert.deepEqual(answers.stale(new Set(['nas.example.com'])), []);
  assert.deepEqual(answers.stale(new Set()), ['nas.example.com']);
});

test('a switch turned on after a refusal asks for a restart', () => {
  const answers = new CertAnswers();
  answers.note('nas.example.com', -202, false);

  assert.equal(answers.refused('NAS.example.com'), true);
  assert.deepEqual(answers.stale(new Set()), []);
  assert.deepEqual(answers.stale(new Set(['nas.example.com'])), ['nas.example.com']);
});

test('a certificate that passes by itself is nobody\'s business', () => {
  const answers = new CertAnswers();
  answers.note('files.example.com', 0, false);
  answers.note('other.example.com', 0, true);

  assert.equal(answers.refused('files.example.com'), false);
  assert.deepEqual(answers.stale(new Set(['files.example.com'])), []);
  assert.deepEqual(answers.stale(new Set()), []);
});

test('the engine is told on and off', () => {
  assert.deepEqual(engineTlsEnv({ insecureSkipTLSVerify: true }), { [ENV_SKIP_TLS_VERIFY]: '1' });
  // ⚠ Off is said: the engine would otherwise read cli.yaml and this process's
  // own environment.
  assert.deepEqual(engineTlsEnv({}), { [ENV_SKIP_TLS_VERIFY]: '0' });
});

test('the variable is the one the engine reads', () => {
  assert.match(read('backend/internal/cliclient/config.go'), new RegExp(`EnvInsecureSkipTLSVerify = "${ENV_SKIP_TLS_VERIFY}"`));
});

test('the warning is the admin panel\'s, in both languages', () => {
  const en = JSON.parse(read('web/src/locales/en.json'));
  const tr = JSON.parse(read('web/src/locales/tr.json'));
  assert.equal(TLS_WORDS.note[0], en.securityOff.note);
  assert.equal(TLS_WORDS.note[1], tr.securityOff.note);
  assert.equal(TLS_WORDS.label[0], en.storages.fields.insecureSkipTlsVerify);
  assert.equal(TLS_WORDS.label[1], tr.storages.fields.insecureSkipTlsVerify);
});

test('the engine is started with the switch, in both places that give it an account', () => {
  const sync = readFileSync(path.join(here, '..', 'src', 'sync.ts'), 'utf8');
  const lines = sync.split('\n').filter((l) => l.includes('FILEX_TOKEN:'));
  assert.equal(lines.length, 2, 'a third place that hands the engine an account has to say the switch too');
  for (const l of lines) assert.match(l, /engineTlsEnv\(/);
});

// The page cannot be loaded here (it needs app:// modules): what ties its switch
// to the main process is three names, and a typo in one is a dead switch.
test('the page, the bridge and the main process name the same channels', () => {
  const read2 = (rel: string) => readFileSync(path.join(here, '..', rel), 'utf8');
  const page = read2('ui/app.html');
  const shell = read2('ui/index.html');
  const preload = read2('src/preload-app.cts');
  const shellPreload = read2('src/preload-shell.cts');
  const main = read2('src/main.ts');

  assert.match(page, /window\.filexApp\.setSkipTLSVerify\(/);
  assert.match(page, /window\.filexApp\.restartApp\(/);
  assert.match(preload, /setSkipTLSVerify: .*'account:setSkipTLSVerify'/);
  assert.match(preload, /restartApp: .*'app:restart'/);
  assert.match(main, /ipcMain\.handle\('account:setSkipTLSVerify'/);
  assert.match(main, /ipcMain\.handle\('app:restart'/);

  assert.match(shell, /insecureSkipTLSVerify: root\.querySelector\('#insecure'\)\.checked/);
  assert.match(shellPreload, /ipcRenderer\.invoke\('auth:begin', serverUrl, opts\)/);
  assert.match(main, /opts\?\.insecureSkipTLSVerify === true/);
});

test('both pages draw the warning from the one table', () => {
  for (const name of ['ui/app.html', 'ui/index.html']) {
    const html = readFileSync(path.join(here, '..', name), 'utf8');
    assert.match(html, /tlsWords/, name);
    assert.doesNotMatch(html, /not safe and not recommended/, `${name} has its own copy of the sentence`);
  }
  for (const k of ['label', 'help', 'note', 'risk', 'advanced', 'restart', 'restartNow', 'restartFirst']) {
    assert.ok(TLS_WORDS[k]?.[0] && TLS_WORDS[k]?.[1], k);
  }
});

test('every network path of the main process stays on the one session the verify callback watches', () => {
  const main = readFileSync(path.join(here, '..', 'src', 'main.ts'), 'utf8');
  assert.match(main, /session\.defaultSession\.setCertificateVerifyProc\(/);
  assert.doesNotMatch(main, /partition\s*:/, 'a window on another session would not hear the switch');
  assert.doesNotMatch(main, /session\.fromPartition/);
});
