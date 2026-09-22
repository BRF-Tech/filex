// "Start when I sign in" — who has the last word.
//
// Run:  node --experimental-strip-types --test desktop/test/login-item.test.ts
//
// The user has it: in Settings, or in the OS (Task Manager → Startup apps,
// System Settings → Login Items, removing the autostart entry). The app used
// to re-register itself on every start whenever its own preference was on, so
// a client someone had switched off in Task Manager came back at the next
// sign-in — with its sync — and "quit it" was not an answer to anything.
// Now only the Settings switch writes the login item; at startup the
// PREFERENCE follows the OS, never the other way round.

import assert from 'node:assert/strict';
import test from 'node:test';

import { loginItemWrite, osWillLaunch, preferenceAfterStartup } from '../src/login-item.ts';

const SPEC = { path: 'C:\\Users\\ada\\AppData\\Local\\Programs\\filex\\filex.exe', args: ['--hidden'] };

test('Windows: disabled in Task Manager means it will NOT start — whatever openAtLogin says', () => {
  // openAtLogin only says the Run value exists; the StartupApproved flag Task
  // Manager writes is what decides, and Electron reports it separately.
  const r = {
    platform: 'win32',
    openAtLogin: true,
    executableWillLaunchAtLogin: false,
    launchItems: [{ enabled: false }],
  };
  assert.equal(osWillLaunch(r), false);
  assert.equal(preferenceAfterStartup(true, true, r), false);
});

test('Windows: an entry removed from the registry turns the preference off, not back on', () => {
  const r = { platform: 'win32', openAtLogin: false, executableWillLaunchAtLogin: false, launchItems: [] };
  assert.equal(preferenceAfterStartup(true, true, r), false);
});

test('Windows: an enabled entry keeps the preference', () => {
  const r = { platform: 'win32', openAtLogin: true, executableWillLaunchAtLogin: true, launchItems: [{ enabled: true }] };
  assert.equal(osWillLaunch(r), true);
  assert.equal(preferenceAfterStartup(true, true, r), true);
});

test('Windows: an Electron that reports only launchItems is read from those', () => {
  assert.equal(osWillLaunch({ platform: 'win32', launchItems: [{ enabled: false }, { enabled: true }] }), true);
  assert.equal(osWillLaunch({ platform: 'win32', launchItems: [{ enabled: false }] }), false);
});

test('a preference that is off stays off — startup never writes or flips anything on', () => {
  const r = { platform: 'win32', openAtLogin: true, executableWillLaunchAtLogin: true };
  assert.equal(preferenceAfterStartup(false, true, r), false);
});

test('a run that cannot have a login item (from source) leaves the preference alone', () => {
  assert.equal(preferenceAfterStartup(true, false, { platform: 'win32' }), true);
});

test('Linux: the autostart entry is the login item', () => {
  assert.equal(preferenceAfterStartup(true, true, { platform: 'linux', autostartFile: false }), false);
  assert.equal(preferenceAfterStartup(true, true, { platform: 'linux', autostartFile: true }), true);
});

test('macOS: removed from Login Items turns it off; waiting for approval is not a refusal', () => {
  assert.equal(preferenceAfterStartup(true, true, { platform: 'darwin', openAtLogin: false, status: 'not-registered' }), false);
  assert.equal(preferenceAfterStartup(true, true, { platform: 'darwin', openAtLogin: true, status: 'enabled' }), true);
  const pending = { platform: 'darwin', openAtLogin: false, status: 'requires-approval' };
  assert.equal(osWillLaunch(pending), false, 'Settings still says the system has not accepted it');
  assert.equal(preferenceAfterStartup(true, true, pending), true);
});

test('Windows: the switch writes the command AND the Task Manager flag, both ways', () => {
  assert.deepEqual(loginItemWrite(true, 'win32', SPEC), { openAtLogin: true, enabled: true, ...SPEC });
  assert.deepEqual(loginItemWrite(false, 'win32', SPEC), { openAtLogin: false, enabled: false, ...SPEC });
});

test('macOS: path and args are Windows-only and are not sent', () => {
  assert.deepEqual(loginItemWrite(true, 'darwin', SPEC), { openAtLogin: true });
});
