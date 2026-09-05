// Where a portable copy keeps its files.
//
// Run:  node --experimental-strip-types --test desktop/test/portable.test.ts
//
// The decision is tested here rather than through the app because two of its
// three answers are awkward to stage against a real disk (a folder that cannot
// be written, and a Windows-only rule) — and because getting it wrong is not a
// cosmetic bug: the wrong answer either loses the user's accounts on every
// launch or leaves them on a machine that is not theirs.

import assert from 'node:assert/strict';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';

import { PORTABLE_DATA_DIRNAME, decidePortableDataDir, directoryIsWritable } from '../src/portable.ts';

const yes = () => true;
const no = () => false;

test('a portable copy keeps its data in a folder beside the .exe', () => {
  const mode = decidePortableDataDir({ PORTABLE_EXECUTABLE_DIR: 'D:\sticks\filex' }, yes, 'win32');
  assert.equal(mode.portable, true);
  assert.equal(mode.dataDir, path.join('D:\sticks\filex', PORTABLE_DATA_DIRNAME));
});

test('…and the sync engine’s store goes inside that same folder', () => {
  // The engine's trash holds real copies of files it deleted. Left at its
  // default it would seed ~/.filex/sync on a borrowed machine.
  const mode = decidePortableDataDir({ PORTABLE_EXECUTABLE_DIR: 'D:\sticks\filex' }, yes, 'win32');
  assert.equal(mode.portable && mode.dataDir !== null, true);
  if (mode.portable && mode.dataDir) {
    assert.equal(mode.syncDir, path.join(mode.dataDir, 'sync'));
  }
});

test('no PORTABLE_EXECUTABLE_DIR means NOT portable — never a guessed location', () => {
  // ⚠ The guess would be process.execPath, which under this target is the
  // extraction temp directory: deleted on exit, so the account store would
  // empty itself between launches.
  assert.deepEqual(decidePortableDataDir({}, yes, 'win32'), { portable: false });
  assert.deepEqual(decidePortableDataDir({ PORTABLE_EXECUTABLE_DIR: '' }, yes, 'win32'), { portable: false });
  assert.deepEqual(decidePortableDataDir({ PORTABLE_EXECUTABLE_DIR: '   ' }, yes, 'win32'), { portable: false });
});

test('the variable is ignored off Windows — the target only exists there', () => {
  for (const platform of ['linux', 'darwin'] as NodeJS.Platform[]) {
    assert.deepEqual(
      decidePortableDataDir({ PORTABLE_EXECUTABLE_DIR: '/tmp/somewhere' }, yes, platform),
      { portable: false },
      platform,
    );
  }
});

test('an unwritable location falls back instead of failing — and says so', () => {
  // C:\Program Files, a read-only stick, a share mounted without write access.
  // dataDir null is what makes Settings show "its files are here instead"
  // rather than a promise the copy is no longer keeping.
  const mode = decidePortableDataDir({ PORTABLE_EXECUTABLE_DIR: 'C:\Program Files\filex' }, no, 'win32');
  assert.equal(mode.portable, true);
  assert.equal(mode.dataDir, null);
  assert.equal(mode.portable && mode.dataDir === null ? mode.exeDir : '', 'C:\Program Files\filex');
});

test('directoryIsWritable creates the folder, proves a write, and leaves no probe', () => {
  const base = fs.mkdtempSync(path.join(os.tmpdir(), 'filex-portable-'));
  const dir = path.join(base, 'nested', PORTABLE_DATA_DIRNAME);

  assert.equal(directoryIsWritable(dir), true);
  assert.equal(fs.existsSync(dir), true);
  // ⚠ The probe file must not survive: it would show up next to the .exe as
  // litter in the one folder the user is meant to be able to read.
  assert.deepEqual(fs.readdirSync(dir), []);

  fs.rmSync(base, { recursive: true, force: true });
});

test('…and answers false rather than throwing when the folder cannot exist', () => {
  const base = fs.mkdtempSync(path.join(os.tmpdir(), 'filex-portable-'));
  // A file where the directory would have to be: mkdir cannot succeed, and an
  // exception here would take the whole app down before it drew a window.
  const blocker = path.join(base, 'blocked');
  fs.writeFileSync(blocker, 'not a directory');

  assert.equal(directoryIsWritable(path.join(blocker, PORTABLE_DATA_DIRNAME)), false);

  fs.rmSync(base, { recursive: true, force: true });
});
