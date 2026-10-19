// The snap's plugs that the person connects by hand - `password-manager-service`
// and `removable-media` - and what the app does until they have.
//
// Run:  node --experimental-strip-types --test desktop/test/snap-plugs.test.ts
//
// The Snap Store declined to auto-connect both (forum.snapcraft.io/t/53584,
// 2026-10-17) and asked for this instead: ask snapd with `snapctl
// is-connected`, and when a plug is not connected, say what connecting it
// allows and show the command. snapctl is never run here: every check takes
// the runner as a parameter.

import assert from 'node:assert/strict';
import test from 'node:test';

import {
  REMOVABLE_MEDIA_ROOTS,
  exitCodeOf,
  onRemovableMedia,
  plugStateOf,
  removableMediaAdvice,
  snapPlugState,
  syncFolderAdvice,
  type SnapctlRun,
} from '../src/snap-plugs.ts';

/** A snapctl that answers `code` and records what it was asked. */
function fakeSnapctl(code: number | null): SnapctlRun & { calls: string[][] } {
  const calls: string[][] = [];
  const run = (async (args: readonly string[]) => {
    calls.push([...args]);
    return code;
  }) as SnapctlRun & { calls: string[][] };
  run.calls = calls;
  return run;
}

const SNAP_ENV = { SNAP: '/snap/filex-app/42', SNAP_NAME: 'filex-app' };

test("snapctl is-connected's exit code: 0 is connected, 1 is not, anything else is no answer", () => {
  assert.equal(plugStateOf(0), 'connected');
  assert.equal(plugStateOf(1), 'disconnected');
  // snapctl's other codes (10: a classic snap, 11: not a snap, 2: a usage
  // error) and no exit code at all are not a "no".
  for (const code of [2, 10, 11, 127, -1, null, undefined]) assert.equal(plugStateOf(code), 'unknown', String(code));
});

test("an execFile error is read as the process's exit code, and only then", () => {
  assert.equal(exitCodeOf(null), 0);
  assert.equal(exitCodeOf(undefined), 0);
  assert.equal(exitCodeOf(Object.assign(new Error('Command failed'), { code: 1 })), 1);
  // No snapctl: spawn fails with a string code, never an exit status.
  assert.equal(exitCodeOf(Object.assign(new Error('spawn snapctl ENOENT'), { code: 'ENOENT' })), null);
  // Killed by the timeout: no code at all.
  assert.equal(exitCodeOf(Object.assign(new Error('killed'), { killed: true, signal: 'SIGTERM', code: null })), null);
});

test('the plug is asked of snapd by name', async () => {
  const run = fakeSnapctl(1);
  assert.equal(await snapPlugState('password-manager-service', run), 'disconnected');
  assert.deepEqual(run.calls, [['is-connected', 'password-manager-service']]);
  assert.equal(await snapPlugState('removable-media', fakeSnapctl(0)), 'connected');
  assert.equal(await snapPlugState('removable-media', fakeSnapctl(null)), 'unknown');
  // A runner that throws is no answer, not a crash in the middle of a pick.
  assert.equal(
    await snapPlugState('removable-media', async () => {
      throw new Error('boom');
    }),
    'unknown',
  );
});

test('the removable-media roots and everything under them, and nothing else', () => {
  assert.deepEqual([...REMOVABLE_MEDIA_ROOTS], ['/media', '/run/media', '/mnt']);
  for (const dir of [
    '/media',
    '/media/',
    '/media/alice/USB STICK',
    '/run/media',
    '/run/media/alice/disk/photos',
    '/mnt',
    '/mnt/backup/',
    // Normalized first: these are under /media and /run/media too.
    '/run//media/alice/usb',
    '/media/./alice/usb',
    '/home/alice/../../media/usb',
    '//media/usb',
  ]) {
    assert.equal(onRemovableMedia(dir), true, dir);
  }
  for (const dir of [
    '/home/alice/filex',
    '/home/alice/media',
    '/mediafiles',
    '/mnt2/x',
    '/run/mediax',
    '/run',
    '/var/mnt',
    // `..` leaves the root behind.
    '/media/../home/alice',
    '/mnt/..',
    // Not a Linux absolute path.
    'media/usb',
    'C:\\mnt\\x',
    '',
  ]) {
    assert.equal(onRemovableMedia(dir), false, dir);
  }
});

test('a folder there is refused only when snapd says the plug is NOT connected', () => {
  assert.deepEqual(removableMediaAdvice('disconnected', SNAP_ENV), {
    plug: 'removable-media',
    command: 'sudo snap connect filex-app:removable-media',
  });
  // Connected: nothing to say. Not known: what the app did before it asked.
  assert.equal(removableMediaAdvice('connected', SNAP_ENV), null);
  assert.equal(removableMediaAdvice('unknown', SNAP_ENV), null);
  // The command names this copy, as snapd runs it (a parallel install too).
  assert.equal(
    removableMediaAdvice('disconnected', { ...SNAP_ENV, SNAP_INSTANCE_NAME: 'filex-app_beta' })?.command,
    'sudo snap connect filex-app_beta:removable-media',
  );
});

test('choosing a sync folder in the snap asks snapd only for a folder under the roots', async () => {
  const no = fakeSnapctl(1);
  assert.deepEqual(await syncFolderAdvice('/media/alice/usb', 'snap', SNAP_ENV, no), {
    plug: 'removable-media',
    command: 'sudo snap connect filex-app:removable-media',
  });
  assert.deepEqual(no.calls, [['is-connected', 'removable-media']]);

  assert.equal(await syncFolderAdvice('/mnt/data', 'snap', SNAP_ENV, fakeSnapctl(0)), null);
  // snapctl missing, timed out or answering something else: paired as chosen.
  assert.equal(await syncFolderAdvice('/run/media/alice/disk', 'snap', SNAP_ENV, fakeSnapctl(null)), null);
  assert.equal(await syncFolderAdvice('/run/media/alice/disk', 'snap', SNAP_ENV, fakeSnapctl(10)), null);

  // A folder the snap reaches anyway (the home interface): snapctl is not run.
  const home = fakeSnapctl(1);
  assert.equal(await syncFolderAdvice('/home/alice/filex/example.com', 'snap', SNAP_ENV, home), null);
  assert.deepEqual(home.calls, []);

  // Every other copy of the app: never asked, never refused.
  for (const channel of [null, 'flatpak', 'aur', 'msstore']) {
    const run = fakeSnapctl(1);
    assert.equal(await syncFolderAdvice('/media/alice/usb', channel, SNAP_ENV, run), null, String(channel));
    assert.deepEqual(run.calls, [], String(channel));
  }
});
