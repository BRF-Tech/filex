// Issue #151 — a document opened with filex whose save comes back in ANOTHER
// format: ONLYOFFICE writes no Word 97, Excel 97 or PowerPoint 97 file, so an
// edited Rapor.doc comes back as DOCX, and a 0.51+ server writes it beside the
// working copy (`.filex-open/<session>-Rapor.docx`) instead of over it. The
// desktop watched only the working copy: Rapor.doc never changed, nobody was
// told, and the edit went with the working folder when the session ended.
//
// The maintainer's call: the edit goes BESIDE the person's document, in its
// new format (Rapor.doc stays, Rapor.docx is the edit), the person is told,
// and the session's next saves go to that same file. A .csv opens with filex
// too: saved as CSV it is written over the .csv (the server already put the
// file's own dialect back, KeepCSV); saved as anything else it goes beside it.
//
// ⚠ The module is imported as a namespace, not by name (as in
// openwith-outside.test.ts): run against a build without these functions,
// every test fails on its own with "is not a function" instead of the whole
// file failing to load - which is what makes the red run readable.
//
// Run:  node --experimental-strip-types --test desktop/test/openwith-beside.test.ts

import assert from 'node:assert/strict';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';

import * as ow from '../src/openwith.ts';
import { writeByRename } from './openwith-cases.ts';

// Loose on purpose (see above): a name that is missing reads as undefined.
const api = ow as unknown as Record<string, any>;

function tmp(): string {
  return fs.mkdtempSync(path.join(os.tmpdir(), 'filex-beside-'));
}

const ZIP_HEADER = [0x50, 0x4b, 0x03, 0x04];

/**
 * A zip whose first entry is `name`, stored, holding `content`, then the next
 * entry's header - the shape every OOXML and ODF package starts with.
 */
function zipWith(name: string, content: string): Buffer {
  const nameBytes = Buffer.from(name, 'latin1');
  const body = Buffer.from(content, 'latin1');
  const head = Buffer.alloc(30);
  head.writeUInt32LE(0x04034b50, 0);
  head.writeUInt16LE(20, 4);
  head.writeUInt16LE(0, 6);
  head.writeUInt16LE(0, 8);
  head.writeUInt32LE(body.length, 18);
  head.writeUInt32LE(body.length, 22);
  head.writeUInt16LE(nameBytes.length, 26);
  head.writeUInt16LE(0, 28);
  return Buffer.concat([head, nameBytes, body, Buffer.from(ZIP_HEADER), Buffer.from('word/document.xml', 'latin1')]);
}

/** An OOXML package (DOCX, XLSX, PPTX alike at this depth), tagged so two saves differ. */
const ooxml = (tag: string) => zipWith('[Content_Types].xml', '<Types>' + tag + '</Types>');
const ODS = zipWith('mimetype', 'application/vnd.oasis.opendocument.spreadsheet');
const ODT = zipWith('mimetype', 'application/vnd.oasis.opendocument.text');
const OLE = Buffer.concat([Buffer.from([0xd0, 0xcf, 0x11, 0xe0, 0xa1, 0xb1, 0x1a, 0xe1]), Buffer.alloc(56)]);

// ── what the server wrote beside a working copy ─────────────────────────

test('listing: the saves written beside a working copy in another format, oldest first - and nothing else', () => {
  const entries = [
    { basename: 'a1b2c3d4e5f6-Rapor.doc', type: 'file', size: 10, lastModified: 1 },
    { basename: 'a1b2c3d4e5f6-Rapor (2).docx', type: 'file', size: 30, lastModified: 3 },
    { basename: 'a1b2c3d4e5f6-Rapor.docx', type: 'file', size: 20, lastModified: 2, etag: 'e1' },
    // the working copy's own format (a conflict copy the server made of it)
    { basename: 'a1b2c3d4e5f6-Rapor.filex-conflict-20261006T101500.doc', type: 'file' },
    // a format the server never keeps beside a document
    { basename: 'a1b2c3d4e5f6-Rapor.pdf', type: 'file' },
    // another session's copy, another document of this session's name prefix
    { basename: 'ffffffffffff-Rapor.docx', type: 'file' },
    { basename: 'a1b2c3d4e5f6-Rapor eski.docx', type: 'file' },
    // a folder
    { basename: 'a1b2c3d4e5f6-Rapor.xlsx', type: 'dir' },
  ];
  const got = api.besideSaves('a1b2c3d4e5f6-Rapor.doc', entries);
  assert.deepEqual(
    got.map((b: { basename: string; ext: string; n: number }) => [b.basename, b.ext, b.n]),
    [
      ['a1b2c3d4e5f6-Rapor.docx', 'docx', 1],
      ['a1b2c3d4e5f6-Rapor (2).docx', 'docx', 2],
    ],
  );
  assert.deepEqual(got[0].stat, { size: 20, lastModified: 2, etag: 'e1' });

  // A copy whose own name ends in " (2)": the numbering is on top of it.
  const plan = api.besideSaves('a1-Plan (2).xls', [
    { basename: 'a1-Plan (2).xls' },
    { basename: 'a1-Plan (2) (2).xlsx' },
    { basename: 'a1-Plan (2).xlsx' },
    { basename: 'a1-Plan.xlsx' },
  ]);
  assert.deepEqual(plan.map((b: { basename: string }) => b.basename), ['a1-Plan (2).xlsx', 'a1-Plan (2) (2).xlsx']);
});

test('listing: a save the session brought home is not taken again - a newer one under that name is', () => {
  const copy = 'docs://.filex-open/a1-Rapor.doc';
  const brought = [{ remote: 'docs://.filex-open/a1-Rapor.docx', seen: { etag: 'e1' } }];
  assert.deepEqual(
    api.unseenBesideSaves(copy, [{ basename: 'a1-Rapor.doc' }, { basename: 'a1-Rapor.docx', etag: 'e1' }], brought),
    [],
    'the same save was brought home twice',
  );
  const newer = api.unseenBesideSaves(copy, [{ basename: 'a1-Rapor.docx', etag: 'e2' }, { basename: 'a1-Rapor (2).docx', etag: 'x' }], brought);
  assert.deepEqual(newer.map((b: { basename: string }) => b.basename), ['a1-Rapor.docx', 'a1-Rapor (2).docx']);
  assert.deepEqual(api.unseenBesideSaves(copy, [], undefined), [], 'an empty folder holds nothing to bring home');
});

test('listing: one look at the folder answers for a file, never for a folder of that name', () => {
  const entries = [
    { basename: 'a1-Rapor.doc', type: 'file', size: 7, lastModified: 5, etag: 'v' },
    { basename: 'a1-Klasör.doc', type: 'dir' },
  ];
  assert.deepEqual(api.statIn(entries, 'a1-Rapor.doc'), { size: 7, lastModified: 5, etag: 'v' });
  assert.equal(api.statIn(entries, 'a1-Klasör.doc'), null);
  assert.equal(api.statIn(entries, 'a1-missing.doc'), null);
});

// ── beside the person's document ────────────────────────────────────────

test('.doc → .docx: Rapor.doc stays as it was, the edit is Rapor.docx beside it', async () => {
  const dir = tmp();
  try {
    const doc = path.join(dir, 'Rapor.doc');
    fs.writeFileSync(doc, OLE);
    const got = await api.writeBesideSave(doc, 'docx', ooxml('EDIT-1'), null);
    assert.equal(got.outcome, 'created');
    assert.equal(got.target.path, path.join(dir, 'Rapor.docx'));
    assert.equal(got.target.ext, 'docx');
    assert.ok(fs.readFileSync(got.target.path).equals(ooxml('EDIT-1')), 'the edit is not in Rapor.docx');
    assert.ok(fs.readFileSync(doc).equals(OLE), 'Rapor.doc was touched');
    assert.deepEqual(fs.readdirSync(dir).sort(), ['Rapor.doc', 'Rapor.docx'], 'leftovers beside the document');
  } finally {
    fs.rmSync(dir, { recursive: true, force: true });
  }
});

test('the next save of the session goes to the SAME Rapor.docx - no new file per save', async () => {
  const dir = tmp();
  try {
    const doc = path.join(dir, 'Rapor.doc');
    fs.writeFileSync(doc, OLE);
    const first = await api.writeBesideSave(doc, 'docx', ooxml('EDIT-1'), null);
    const second = await api.writeBesideSave(doc, 'docx', ooxml('EDIT-2'), first.target);
    assert.equal(second.outcome, 'updated');
    assert.equal(second.target.path, first.target.path);
    const third = await api.writeBesideSave(doc, 'DOCX', ooxml('EDIT-3'), second.target);
    assert.equal(third.target.path, first.target.path, 'the format is compared case-insensitively');
    assert.ok(fs.readFileSync(first.target.path).equals(ooxml('EDIT-3')));
    assert.deepEqual(fs.readdirSync(dir).sort(), ['Rapor.doc', 'Rapor.docx'], 'a save made a file of its own');
    assert.ok(fs.readFileSync(doc).equals(OLE), 'Rapor.doc was touched');
  } finally {
    fs.rmSync(dir, { recursive: true, force: true });
  }
});

test('Rapor.docx changed outside filex: not written over - a conflict copy, where the next saves go', async () => {
  const dir = tmp();
  try {
    const doc = path.join(dir, 'Rapor.doc');
    fs.writeFileSync(doc, OLE);
    const now = new Date('2026-10-06T10:15:00Z');
    const first = await api.writeBesideSave(doc, 'docx', ooxml('EDIT-1'), null, { now });
    writeByRename(first.target.path, 'THE-PERSON-EDITED-IT');
    const second = await api.writeBesideSave(doc, 'docx', ooxml('EDIT-2'), first.target, { now });
    assert.equal(second.outcome, 'conflict');
    assert.equal(fs.readFileSync(first.target.path, 'utf8'), 'THE-PERSON-EDITED-IT', 'the outside change is gone');
    assert.equal(second.target.path, path.join(dir, 'Rapor.filex-conflict-20261006T101500.docx'));
    assert.ok(fs.readFileSync(second.target.path).equals(ooxml('EDIT-2')));
    // …and the save after that goes to the same conflict copy, not a third file.
    const third = await api.writeBesideSave(doc, 'docx', ooxml('EDIT-3'), second.target, { now });
    assert.equal(third.outcome, 'updated');
    assert.equal(third.target.path, second.target.path);
    assert.ok(fs.readFileSync(second.target.path).equals(ooxml('EDIT-3')));
    assert.deepEqual(
      fs.readdirSync(dir).sort(),
      ['Rapor.doc', 'Rapor.docx', 'Rapor.filex-conflict-20261006T101500.docx'],
    );
    assert.ok(fs.readFileSync(doc).equals(OLE), 'Rapor.doc was touched');
  } finally {
    fs.rmSync(dir, { recursive: true, force: true });
  }
});

test('a folder that takes no new file: the edit is kept elsewhere, and the error says where', async () => {
  const dir = tmp();
  try {
    const doc = path.join(dir, 'moved-away', 'Rapor.doc');
    const fallback = path.join(dir, 'recovered');
    let err: any = null;
    try {
      await api.writeBesideSave(doc, 'docx', ooxml('EDIT-1'), null, { fallbackDir: fallback });
    } catch (e) {
      err = e;
    }
    assert.ok(err, 'a save that went nowhere reported success');
    assert.equal(err.name, 'WriteBackError', 'the failure does not carry where the edit is: ' + String(err));
    assert.ok(err.keptAt && err.keptAt.startsWith(fallback), 'the edit was not kept: ' + err.keptAt);
    assert.ok(fs.readFileSync(err.keptAt).equals(ooxml('EDIT-1')));
  } finally {
    fs.rmSync(dir, { recursive: true, force: true });
  }
});

test('names: after the document, Turkish characters kept, the format in lower case; numbered as the server numbers', () => {
  const dir = path.join(os.tmpdir(), 'Belgeler');
  assert.equal(api.besidePathFor(path.join(dir, 'Bütçe Özeti.XLS'), 'XLSX'), path.join(dir, 'Bütçe Özeti.xlsx'));
  assert.equal(api.besidePathFor(path.join(dir, 'Sunum.ppt'), 'pptx'), path.join(dir, 'Sunum.pptx'));
  assert.equal(api.numberedPathFor(path.join(dir, 'Rapor.docx'), 2), path.join(dir, 'Rapor (2).docx'));
  assert.equal(api.numberedPathFor(path.join(dir, 'Rapor.docx'), 1), path.join(dir, 'Rapor.docx'));
});

// ── a .csv (#151: the maintainer's addition) ─────────────────────────────

test('.csv: saved as CSV it goes over the .csv itself (KeepCSV), as a spreadsheet beside it', async () => {
  // What a 0.51+ server writes over the working copy of a .csv: CSV text in
  // the file's own dialect - its delimiter, its byte order mark, its line ends.
  assert.equal(api.savedFormatOf('Tablo.csv', Buffer.from('ad;adet\r\nelma;3\r\n')), null);
  assert.equal(api.savedFormatOf('Tablo.csv', Buffer.concat([Buffer.from([0xef, 0xbb, 0xbf]), Buffer.from('a,b\n1,2\n')])), null);
  // What a Document Server with assemblyFormatAsOrigin off sends back, and a
  // server before 0.51 wrote under the .csv name.
  assert.equal(api.savedFormatOf('Tablo.csv', ooxml('SHEET')), 'xlsx', 'an XLSX would be written over the .csv');
  assert.equal(api.savedFormatOf('Tablo.csv', ODS), 'ods');
  assert.equal(api.savedFormatOf('Tablo.csv', OLE), 'xls');

  const dir = tmp();
  try {
    const csv = path.join(dir, 'Tablo.csv');
    fs.writeFileSync(csv, 'ad;adet\nelma;3\n');
    const got = await api.writeBesideSave(csv, 'xlsx', ooxml('SHEET'), null);
    assert.equal(got.target.path, path.join(dir, 'Tablo.xlsx'));
    assert.equal(fs.readFileSync(csv, 'utf8'), 'ad;adet\nelma;3\n', 'the .csv was touched');
  } finally {
    fs.rmSync(dir, { recursive: true, force: true });
  }
});

test('bytes: a save in another format is caught by its bytes when the server wrote it over the working copy', () => {
  // A server before 0.51 wrote ONLYOFFICE's DOCX over the copy of a .doc.
  assert.equal(api.savedFormatOf('Rapor.doc', ooxml('X')), 'docx');
  assert.equal(api.savedFormatOf('Tablo.xls', ooxml('X')), 'xlsx');
  assert.equal(api.savedFormatOf('Sunum.ppt', ooxml('X')), 'pptx');
  assert.equal(api.savedFormatOf('Mektup.rtf', ooxml('X')), 'docx');
  assert.equal(api.savedFormatOf('Not.odt', ooxml('X')), 'docx', 'assemblyFormatAsOrigin off: an ODT saved as DOCX');
  assert.equal(api.savedFormatOf('Tablo.xlsx', ODS), 'ods');
  // The document's own format: written over it, as always.
  assert.equal(api.savedFormatOf('Rapor.docx', ooxml('X')), null);
  assert.equal(api.savedFormatOf('Not.odt', ODT), null);
  assert.equal(api.savedFormatOf('Rapor.doc', OLE), null);
  assert.equal(api.savedFormatOf('RAPOR.DOCX', ooxml('X')), null, 'the extension is matched case-insensitively');
  // Nothing that can be told: as before.
  assert.equal(api.savedFormatOf('Rapor.doc', zipWith('word/document.xml', '<w/>')), null, 'a zip nobody can name');
  assert.equal(
    api.savedFormatOf('Rapor.docx', zipWith('mimetype', 'application/vnd.oasis.opendocument.text-template')),
    null,
    'an ODF template is not an ODT',
  );
  assert.equal(api.savedFormatOf('Rapor.docx', Buffer.from('plain text')), null);
  assert.equal(api.savedFormatOf('notes.txt', ooxml('X')), null, 'not a type filex opens');
});
