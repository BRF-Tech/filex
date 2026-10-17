// What a server name becomes when the app writes a copy of it on this
// computer (drag-out, a kept folder's mirror) - src/local-name.ts, held to the
// hostile names every surface of filex is tested with (task #238).
//
// Run:  node --experimental-strip-types --test desktop/test/local-name.test.ts

import assert from 'node:assert/strict';
import path from 'node:path';
import test from 'node:test';

import { HOSTILE_NAMES, hostileName } from '../../e2e/helpers/hostile-names.mjs';
import {
  MAX_NAME,
  collisionKey,
  exactLocalPath,
  isBidiControl,
  isDeviceName,
  safeLocalName,
  serverRelFor,
  uniqueLocalName,
} from '../src/local-name.ts';
import { resolveSyncTwin } from '../src/openwith.ts';

/** What Windows refuses in a name, written out apart from the code under
 *  test: the list's own `windows: invalid` flags agree with it. */
function windowsRefuses(name: string): boolean {
  for (const ch of name) {
    const cp = ch.codePointAt(0)!;
    if (cp < 0x20 || '\\/:*?"<>|'.includes(ch)) return true;
  }
  return /[. ]$/.test(name) || isDeviceName(name);
}

test('every hostile name becomes a name Windows holds, carrying no bidi control, within 255 with room for .filexpart', () => {
  for (const n of HOSTILE_NAMES) {
    for (const platform of ['win32', 'linux', 'darwin']) {
      const got = safeLocalName(n.name, { reserve: '.filexpart'.length, platform });
      assert.ok(got.length > 0, n.id);
      assert.equal(windowsRefuses(got), false, `${n.id} on ${platform}: ${JSON.stringify(got)}`);
      for (const ch of got) {
        const cp = ch.codePointAt(0)!;
        assert.ok(!isBidiControl(cp) && cp !== 0x7f, `${n.id}: U+${cp.toString(16)} kept`);
      }
      const size = platform === 'win32' ? got.length : Buffer.byteLength(got);
      assert.ok(size <= MAX_NAME - '.filexpart'.length, `${n.id} on ${platform}: ${size}`);
    }
  }
});

test('a name Windows holds and no bidi control is in comes through exactly, leading space included', () => {
  for (const n of HOSTILE_NAMES) {
    const plain = !n.windowsInvalid && !n.tags.includes('bidi-control') && Buffer.byteLength(n.name) <= MAX_NAME - 10;
    if (!plain) continue;
    assert.equal(safeLocalName(n.name, { reserve: 10, platform: 'linux' }), n.name, n.id);
  }
  assert.equal(safeLocalName(hostileName('leading-space').name), ' leading.txt');
});

test('the RLO name shows its true extension, and the hostile Windows names their safe form', () => {
  assert.equal(safeLocalName(hostileName('rlo-extension').name), 'invoice_fdp.exe');
  assert.equal(safeLocalName(hostileName('trailing-dot').name), 'report_');
  assert.equal(safeLocalName(hostileName('three-dots').name), '___');
  assert.equal(safeLocalName(hostileName('win-con').name), '_CON');
  assert.equal(safeLocalName(hostileName('win-aux-ext').name), '_aux.txt');
  assert.equal(safeLocalName(hostileName('colon').name), '10_30 notes.txt');
  assert.equal(safeLocalName(hostileName('control-tab').name), 'tab_name.txt');
  assert.equal(safeLocalName(''), 'file');
});

test('a long name keeps its extension and is cut on a whole character', () => {
  const long = hostileName('chars-under-bytes-over').name;
  const got = safeLocalName(long, { reserve: 5, platform: 'linux' });
  assert.ok(got.endsWith('.txt'));
  assert.ok(Buffer.byteLength(got) <= MAX_NAME - 5);
  assert.ok(!got.includes(String.fromCodePoint(0xfffd)));
  // NTFS counts UTF-16 units: 204 characters fit as they are.
  assert.equal(safeLocalName(long, { reserve: 5, platform: 'win32' }), long);
});

test('twins and names that become one are kept apart in a folder', () => {
  const taken = new Set<string>();
  const a = uniqueLocalName(safeLocalName(hostileName('case-upper').name), taken);
  const b = uniqueLocalName(safeLocalName(hostileName('case-lower').name), taken);
  assert.equal(a, 'Rapor.docx');
  assert.equal(b, 'rapor (2).docx');
  const nfc = uniqueLocalName(hostileName('nfc-butce').name, taken);
  const nfd = uniqueLocalName(hostileName('nfd-butce').name, taken);
  assert.notEqual(collisionKey(nfc), collisionKey(nfd));
  assert.equal(uniqueLocalName(safeLocalName('a:b.txt'), taken), 'a_b.txt');
  assert.equal(uniqueLocalName('a_b.txt', taken), 'a_b (2).txt');
});

test('a mirror path is only the item itself, spelled exactly', () => {
  const disk: Record<string, string[]> = { '/m': ['Rapor.docx', 'Docs', 'a_b.txt'], '/m/Docs': ['x.txt'] };
  const readdir = (d: string) => disk[d] ?? null;
  const join = path.posix.join;
  for (const platform of ['win32', 'darwin']) {
    assert.equal(exactLocalPath('/m', ['Rapor.docx'], readdir, join, platform), '/m/Rapor.docx');
    assert.equal(exactLocalPath('/m', ['rapor.docx'], readdir, join, platform), null, `${platform}: the twin's bytes`);
    assert.equal(exactLocalPath('/m', ['docs', 'x.txt'], readdir, join, platform), null);
    assert.equal(exactLocalPath('/m', ['Docs', 'x.txt'], readdir, join, platform), '/m/Docs/x.txt');
  }
  // a:b.txt is not the mirror's a_b.txt.
  assert.equal(exactLocalPath('/m', ['a:b.txt'], readdir, join, 'win32'), null);
  assert.equal(exactLocalPath('/m', ['..', 'x'], readdir, join, 'linux'), null);
  assert.equal(exactLocalPath('/m', ['rapor.docx'], readdir, join, 'linux'), '/m/rapor.docx');
});

test('a path here is the server path the sync engine paired it with', () => {
  const names = { 'rapor:2024.txt': 'rapor_2024.txt', docs: 'docs (2)', 'report./CON': 'report_/_CON', 'report.': 'report_' };
  assert.equal(serverRelFor('rapor_2024.txt', names), 'rapor:2024.txt');
  assert.equal(serverRelFor('docs (2)/b.txt', names), 'docs/b.txt');
  assert.equal(serverRelFor('report_/_CON/x.txt', names), 'report./CON/x.txt');
  assert.equal(serverRelFor('report_/a.txt', names), 'report./a.txt');
  assert.equal(serverRelFor('Docs/a.txt', names), 'Docs/a.txt');
  assert.equal(serverRelFor('x.txt', {}), 'x.txt');
});

test('"Open with filex" on a file kept under another name opens the server item, not a new one', () => {
  const names = { 'rapor:2024.txt': 'rapor_2024.txt' };
  const got = resolveSyncTwin('C:\\mirror\\docs\\rapor_2024.txt', [{ id: 'p', local: 'C:\\mirror\\docs', remote: 'docs://' }], {
    platform: 'win32',
    serverRel: (pairId, rel) => (pairId === 'p' ? serverRelFor(rel, names) : rel),
  });
  assert.deepEqual(got, { pairId: 'p', remote: 'docs://rapor:2024.txt' });
});
