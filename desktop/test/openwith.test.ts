// "Open with filex" — the real implementation, against the shared case table
// plus the pieces that only exist on the real side (the session store, the
// change fingerprint, the type list).
//
// Run:  node --experimental-strip-types --test desktop/test/openwith.test.ts
// The red proof for the same cases:  node desktop/scripts/openwith-red.mjs

import assert from 'node:assert/strict';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';

import {
  OFFICE_EXTENSIONS,
  SessionStore,
  classifyArgv,
  extensionOf,
  fingerprint,
  hasChanged,
  isOfficeDocument,
  needsRecovery,
  newSessionId,
  orphanScratchEntries,
  recoveryPathFor,
  resolveSyncTwin,
  scratchBasename,
  scratchRemoteDir,
  scratchRemotePath,
  staleSessions,
  writeBackAtomic,
  type OpenWithSession,
} from '../src/openwith.ts';
import { CASES, type Impl } from './openwith-cases.ts';

const REAL: Impl = {
  classifyArgv,
  resolveSyncTwin,
  scratchBasename,
  writeBackAtomic,
  staleSessions,
  orphanScratchEntries,
};

function tmp(): string {
  return fs.mkdtempSync(path.join(os.tmpdir(), 'filex-openwith-'));
}

for (const c of CASES) {
  test(`${c.group}: ${c.name}`, async () => {
    const dir = tmp();
    try {
      await c.run(REAL, dir);
    } finally {
      await fs.promises.rm(dir, { recursive: true, force: true });
    }
  });
}

// ── the pieces with no naive counterpart ─────────────────────────────

test('types: office documents only — an image or a PDF is left to whatever opens it today', () => {
  for (const ext of OFFICE_EXTENSIONS) assert.equal(isOfficeDocument(`a.${ext}`), true, ext);
  for (const ext of ['png', 'pdf', 'txt', 'md', 'zip', 'exe', '']) {
    assert.equal(isOfficeDocument(`a.${ext}`), false, ext);
  }
  assert.equal(isOfficeDocument('C:\\x\\REPORT.DOCX'), true, 'the extension is matched case-insensitively');
  assert.equal(isOfficeDocument('C:\\docx'), false, 'a folder named like an extension is not a document');
  assert.equal(extensionOf('/a/b/.docx'), '', 'a dotfile has no extension');
});

test('change detection: an etag wins, size+mtime is the fallback, a vanished file is not an edit', () => {
  assert.equal(hasChanged({ etag: 'a' }, { etag: 'b' }), true);
  assert.equal(hasChanged({ etag: 'a' }, { etag: 'a' }), false);
  // ⚠ Same size, later mtime: a one-character edit in a compressed .docx very
  // often comes back the same length, so size alone would miss the save.
  assert.equal(hasChanged({ size: 12, lastModified: 1 }, { size: 12, lastModified: 2 }), true);
  assert.equal(hasChanged({ size: 12, lastModified: 1 }, { size: 12, lastModified: 1 }), false);
  assert.equal(hasChanged({ size: 12 }, null), false, 'a deleted scratch copy is not an edit to write back');
  assert.equal(hasChanged(null, { size: 12 }), true, 'the first sighting counts as new');
  assert.equal(fingerprint(null), '');
});

test('wire paths: the scratch folder and the copy inside it', () => {
  assert.equal(scratchRemoteDir('docs'), 'docs://.filex-open');
  assert.equal(scratchRemotePath('docs', 'a1-Bütçe.xlsx'), 'docs://.filex-open/a1-Bütçe.xlsx');
});

test('session ids are unique enough that two documents opened at once cannot collide', () => {
  const seen = new Set<string>();
  for (let i = 0; i < 2000; i++) seen.add(newSessionId());
  assert.equal(seen.size, 2000);
  assert.match([...seen][0]!, /^[0-9a-f]{12}$/);
});

test('recovery files sit beside the document and keep its extension', () => {
  const p = recoveryPathFor(path.join('C:', 'x', 'Bütçe Özeti.xlsx'), '20260904T101500');
  assert.equal(path.basename(p), 'Bütçe Özeti.filex-recovered-20260904T101500.xlsx');
  assert.equal(path.dirname(p), path.join('C:', 'x'));
});

test('session store: survives a round trip, and one unreadable record does not blind the sweep', async () => {
  const dir = tmp();
  try {
    const store = new SessionStore(path.join(dir, 'openwith'));
    const s: OpenWithSession = {
      id: 'abc123',
      accountId: 'acc',
      serverUrl: 'https://files.example',
      storage: 'docs',
      localPath: path.join(dir, 'Bütçe.xlsx'),
      remote: 'docs://.filex-open/abc123-Bütçe.xlsx',
      createdAt: '2026-09-04T10:00:00.000Z',
      updatedAt: '2026-09-04T10:00:00.000Z',
      seen: { size: 10, lastModified: 5 },
      ownerPid: 4242,
    };
    await store.put(s);
    assert.deepEqual(await store.list(), [s]);

    await fs.promises.writeFile(path.join(dir, 'openwith', 'broken.json'), '{not json');
    const after = await store.list();
    assert.deepEqual(after.map((x) => x.id), ['abc123'], 'a corrupt record hid the good ones');
    assert.equal(
      fs.existsSync(path.join(dir, 'openwith', 'broken.json')),
      false,
      'a record nothing can act on was left to be re-read forever',
    );

    await store.remove('abc123');
    assert.deepEqual(await store.list(), []);
  } finally {
    await fs.promises.rm(dir, { recursive: true, force: true });
  }
});

test('recovery is needed only when the copy moved on after the last write-back', () => {
  const s: OpenWithSession = {
    id: 'a', accountId: 'x', serverUrl: 'https://s', storage: 'docs',
    localPath: 'C:\\a.docx', remote: 'docs://.filex-open/a-a.docx',
    createdAt: '2026-09-04T10:00:00Z', updatedAt: '2026-09-04T10:00:00Z',
    seen: { etag: 'v1' }, ownerPid: 1,
  };
  assert.equal(needsRecovery(s, { etag: 'v2' }), true);
  assert.equal(needsRecovery(s, { etag: 'v1' }), false);
  assert.equal(needsRecovery(s, null), false, 'a copy that is already gone has nothing to recover');
});

test('write-back keeps the document readable by whoever could read it before', async (t) => {
  if (process.platform === 'win32') {
    t.skip('POSIX modes only');
    return;
  }
  const dir = tmp();
  try {
    const target = path.join(dir, 'a.docx');
    await fs.promises.writeFile(target, 'OLD');
    await fs.promises.chmod(target, 0o644);
    await writeBackAtomic(target, Buffer.from('NEW'));
    assert.equal((await fs.promises.stat(target)).mode & 0o777, 0o644);
  } finally {
    await fs.promises.rm(dir, { recursive: true, force: true });
  }
});
