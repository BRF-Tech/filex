// "Mount as a drive" — the platform command builder and its runner.
//
// Run:  node --experimental-strip-types --test desktop/test/drive.test.ts
//
// ⚠ The credential must never reach a command line. Every case below asserts
// the token appears in NO argv and in NO logged detail — only ever in the
// injected exec's stdin. That is the whole security property of this feature,
// and it is measured, not assumed.

import assert from 'node:assert/strict';
import test from 'node:test';

import {
  freeDriveLetters,
  lastJson,
  mount,
  pickDriveLetter,
  planMount,
  unmount,
  windowsProblem,
  WINDOWS_MOUNT_PS,
  type DriveDeps,
  type MountRequest,
} from '../src/drive.ts';

const TOKEN = 'sekritTOKEN-do-not-leak-0123456789';

/** A fake exec that records every call and returns a scripted reply. */
function recorder(replies: Record<string, { code: number; stdout?: string; stderr?: string }>) {
  const calls: { file: string; args: string[]; input: string }[] = [];
  const logs: { tag: string; step: string; detail?: unknown }[] = [];
  const deps: DriveDeps = {
    exec: async (file, args, input) => {
      calls.push({ file, args, input });
      const r = replies[file] ?? { code: 0 };
      return { code: r.code, stdout: r.stdout ?? '', stderr: r.stderr ?? '' };
    },
    exists: (p) => /^[C]:\\$/.test(p), // only C: is taken
    log: (tag, step, detail) => logs.push({ tag, step, detail }),
    home: '/home/ada',
  };
  return { deps, calls, logs };
}

function assertNoLeak(token: string, calls: { args: string[] }[], logs: { detail?: unknown }[]) {
  for (const c of calls) {
    assert.ok(!c.args.some((a) => a.includes(token)), `token leaked into argv: ${c.args.join(' ')}`);
  }
  for (const l of logs) {
    assert.ok(!JSON.stringify(l.detail ?? '').includes(token), 'token leaked into a log line');
  }
}

const base: MountRequest = {
  platform: 'win32',
  serverUrl: 'https://fm.example.com',
  storage: 'docs',
  user: 'ada@example.com',
  password: TOKEN,
};

test('free drive letters go from Z down to D and skip taken ones', () => {
  const letters = freeDriveLetters((p) => p === 'C:\\' || p === 'Z:\\');
  assert.equal(letters[0], 'Y:');
  assert.ok(!letters.includes('Z:'));
  assert.ok(!letters.includes('C:'));
  assert.equal(letters[letters.length - 1], 'D:');
});

test('pickDriveLetter honours a free choice and refuses a taken one', () => {
  assert.equal(pickDriveLetter((p) => p === 'C:\\', 'X:'), 'X:');
  assert.equal(pickDriveLetter((p) => p === 'X:\\', 'X:'), null);
  assert.equal(pickDriveLetter((p) => p === 'C:\\'), 'Z:');
});

test('the Windows plan targets the WebDAV UNC and warns about the large-file limit', () => {
  const plan = planMount(base, (p) => p === 'C:\\');
  assert.equal(plan.letter, 'Z:');
  assert.equal(plan.unc, '\\\\fm.example.com@SSL\\dav\\docs');
  assert.ok(plan.warnings.includes('windows-large-files'));
  assert.ok(!plan.warnings.includes('plain-http'));
});

test('a plain-http address is flagged', () => {
  const plan = planMount({ ...base, serverUrl: 'http://127.0.0.1:5330' }, (p) => p === 'C:\\');
  assert.ok(plan.warnings.includes('plain-http'));
});

test('Windows mount: token only on stdin, script only as -EncodedCommand, none in argv or log', async () => {
  const { deps, calls, logs } = recorder({ 'powershell.exe': { code: 0, stdout: '{"ok":true,"started":true}' } });
  const res = await mount(base, deps);
  assert.equal(res.ok, true);
  assert.equal(res.letter, 'Z:');
  const ps = calls.find((c) => c.file === 'powershell.exe')!;
  assert.ok(ps.args.includes('-EncodedCommand'), 'the script must be passed encoded, not inline');
  assert.ok(ps.input.includes(TOKEN), 'the token must reach PowerShell on stdin');
  // The stdin payload is JSON with the token in the password field.
  const payload = JSON.parse(ps.input.trim());
  assert.equal(payload.password, TOKEN);
  assert.equal(payload.unc, '\\\\fm.example.com@SSL\\dav\\docs');
  assertNoLeak(TOKEN, calls, logs);
});

test('Windows mount maps WebClient states to problems', async () => {
  for (const [stdout, problem] of [
    ['{"ok":false,"problem":"webclient-disabled"}', 'webclient-disabled'],
    ['{"ok":false,"problem":"webclient-stopped"}', 'webclient-stopped'],
    ['{"ok":false,"problem":"webclient-missing"}', 'webclient-missing'],
    ['{"ok":false,"code":1244}', 'auth'],
    ['{"ok":false,"code":85}', 'in-use'],
  ] as const) {
    const { deps, calls, logs } = recorder({ 'powershell.exe': { code: 0, stdout } });
    const res = await mount(base, deps);
    assert.equal(res.ok, false);
    assert.equal(res.problem, problem, `stdout ${stdout}`);
    assertNoLeak(TOKEN, calls, logs);
  }
});

test('Windows mount refuses when no drive letter is free', async () => {
  const { deps } = recorder({});
  deps.exists = () => true; // every letter taken
  const res = await mount(base, deps);
  assert.equal(res.ok, false);
  assert.equal(res.problem, 'no-free-letter');
});

test('windowsProblem maps the documented error numbers', () => {
  assert.equal(windowsProblem(1244), 'auth');
  assert.equal(windowsProblem(1790), 'tls');
  assert.equal(windowsProblem(85), 'in-use');
  assert.equal(windowsProblem(12345), 'failed');
});

test('the PowerShell helper reads its payload from stdin, not from an argument', () => {
  assert.match(WINDOWS_MOUNT_PS, /\[Console\]::In\.ReadToEnd\(\)/);
  assert.match(WINDOWS_MOUNT_PS, /MapNetworkDrive\(\$in\.letter, \$in\.unc, \$in\.persistent, \$in\.user, \$in\.password\)/);
  // It must decide the WebClient story itself.
  assert.match(WINDOWS_MOUNT_PS, /webclient-disabled/);
});

test('lastJson returns the final JSON line past PowerShell banner noise', () => {
  assert.deepEqual(lastJson('Preparing modules...\n{"ok":true}\n'), { ok: true });
  assert.equal(lastJson('no json here'), null);
});

test('unmount on Windows carries no credential and only the letter', async () => {
  const { deps, calls, logs } = recorder({ 'net.exe': { code: 0 } });
  const res = await unmount({ platform: 'win32', letter: 'Z:' }, deps);
  assert.equal(res.ok, true);
  const net = calls.find((c) => c.file === 'net.exe')!;
  assert.deepEqual(net.args, ['use', 'Z:', '/delete', '/y']);
  assert.equal(net.input, '');
  assertNoLeak(TOKEN, calls, logs);
});

test('Linux plan puts the user in the gio URI and the token stays off argv', async () => {
  const linux: MountRequest = { ...base, platform: 'linux' };
  const plan = planMount(linux, () => false, '/home/ada');
  assert.equal(plan.gio, 'davs://ada%40example.com@fm.example.com/dav/docs/');
  const { deps, calls, logs } = recorder({ gio: { code: 0 } });
  const res = await mount(linux, deps);
  assert.equal(res.ok, true);
  const gio = calls.find((c) => c.file === 'gio')!;
  assert.deepEqual(gio.args, ['mount', 'davs://ada%40example.com@fm.example.com/dav/docs/']);
  assert.ok(gio.input.includes(TOKEN), 'gio takes the password on stdin');
  assertNoLeak(TOKEN, calls, logs);
});

test('Linux mount reports a missing gio as no-tool', async () => {
  const linux: MountRequest = { ...base, platform: 'linux' };
  const { deps } = recorder({ gio: { code: 127, stderr: 'gio: command not found' } });
  const res = await mount(linux, deps);
  assert.equal(res.problem, 'no-tool');
});

// ⚠ It targeted /Volumes/filex-<storage>. A user cannot make a folder under
// /Volumes (measured on macOS 26: "Permission denied"), the mkdir's failure
// was ignored, and mount_webdav then failed on a folder that did not exist:
// the feature could not have worked for anyone. The folder is the user's own,
// beside — never inside or instead of — the ~/filex sync folder.
test('macOS plan targets a folder in the home, not /Volumes, and the token stays off argv', async () => {
  const mac: MountRequest = { ...base, platform: 'darwin' };
  const plan = planMount(mac, () => false, '/Users/ada');
  assert.equal(plan.mountDir, '/Users/ada/filex-drives/filex-docs');
  const { deps, calls, logs } = recorder({ '/bin/mkdir': { code: 0 }, '/sbin/mount_webdav': { code: 0 } });
  const res = await mount(mac, deps);
  assert.equal(res.ok, true);
  const mw = calls.find((c) => c.file === '/sbin/mount_webdav')!;
  assert.ok(!mw.args.some((a) => a.includes(TOKEN)));
  assert.ok(mw.input.includes(TOKEN), 'mount_webdav takes the password on stdin');
  assertNoLeak(TOKEN, calls, logs);
});

test('macOS says so when the mount folder cannot be made, and does not mount', async () => {
  const mac: MountRequest = { ...base, platform: 'darwin' };
  const { deps, calls } = recorder({ '/bin/mkdir': { code: 1, stderr: 'mkdir: /x: Permission denied' } });
  const res = await mount(mac, deps);
  assert.equal(res.ok, false);
  assert.equal(res.problem, 'failed');
  assert.match(res.detail ?? '', /filex-drives\/filex-docs/);
  assert.match(res.detail ?? '', /Permission denied/);
  assert.ok(!calls.some((c) => c.file === '/sbin/mount_webdav'), 'it mounted onto a folder it could not make');
});
