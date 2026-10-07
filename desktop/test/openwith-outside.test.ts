// Issue #184 — a document open through "Open with filex" that changes OUTSIDE
// filex while it is open (an agent rewrites the spreadsheet, another editor
// saves it, a sync client brings a newer copy down).
//
// Two things went wrong before: the open editor kept showing the old version,
// and the next save from filex was written over the new one without a look.
// The write-back half is measured in openwith-cases.ts (where the naive
// implementation is shown red too); this file measures the watching half:
// the local document is watched, a change from outside is reported (once),
// and filex's OWN writes are never mistaken for one.
//
// ⚠ The module is imported as a namespace, not by name, on purpose: run
// against a build that does not have these functions yet, every test fails on
// its own with "is not a function" instead of the whole file failing to load —
// which is what makes the red run readable.
//
// Run:  node --experimental-strip-types --test desktop/test/openwith-outside.test.ts

import assert from 'node:assert/strict';
import crypto from 'node:crypto';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';

import * as ow from '../src/openwith.ts';
import { versionOf, writeByRename } from './openwith-cases.ts';

// Loose on purpose (see above): a name that is missing reads as undefined.
const api = ow as unknown as Record<string, any>;

function tmp(): string {
  return fs.mkdtempSync(path.join(os.tmpdir(), 'filex-outside-'));
}

const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms));

/** Resolves with the first report, or rejects after `ms`. */
function nextReport(reports: Array<{ text: string }>, ms: number, from = 0): Promise<{ text: string }> {
  const until = Date.now() + ms;
  return new Promise((resolve, reject) => {
    const tick = () => {
      if (reports.length > from) return resolve(reports[from]!);
      if (Date.now() > until) return reject(new Error('no outside change was reported within ' + ms + ' ms'));
      setTimeout(tick, 25);
    };
    tick();
  });
}

async function watching(dir: string, name: string, content: string) {
  const file = path.join(dir, name);
  fs.writeFileSync(file, content);
  const reports: Array<{ text: string; version: ow.LocalVersion }> = [];
  const monitor = new api.LocalDocMonitor({
    path: file,
    baseline: versionOf(file),
    debounceMs: 150,
    onChange: (version: ow.LocalVersion, bytes: Buffer) => reports.push({ text: bytes.toString('utf8'), version }),
  });
  monitor.start();
  return { file, monitor, reports };
}

test('watch: an agent that writes a temp file and RENAMES it over the document is noticed within seconds', async () => {
  const dir = tmp();
  const { file, monitor, reports } = await watching(dir, 'Bütçe Özeti.xlsx', 'ORIGINAL');
  try {
    // ⚠ A watch on the FILE would go deaf here: the rename replaces the inode
    // the watch was on. The folder is watched, and the name filtered.
    await sleep(100);
    writeByRename(file, 'WHAT-THE-AGENT-WROTE');
    const got = await nextReport(reports, 5000);
    assert.equal(got.text, 'WHAT-THE-AGENT-WROTE');
  } finally {
    monitor.stop();
    fs.rmSync(dir, { recursive: true, force: true });
  }
});

test('watch: an editor that writes the document IN PLACE is noticed too', async () => {
  const dir = tmp();
  const { file, monitor, reports } = await watching(dir, 'rapor.docx', 'ORIGINAL');
  try {
    await sleep(100);
    fs.writeFileSync(file, 'WRITTEN-IN-PLACE');
    const got = await nextReport(reports, 5000);
    assert.equal(got.text, 'WRITTEN-IN-PLACE');
  } finally {
    monitor.stop();
    fs.rmSync(dir, { recursive: true, force: true });
  }
});

test("watch: filex's OWN write-back is never reported as a change from outside", async () => {
  const dir = tmp();
  const { file, monitor, reports } = await watching(dir, 'Bütçe.xlsx', 'ORIGINAL');
  try {
    await sleep(100);
    const mine = await monitor.ownWrite(() =>
      ow.writeBackAtomic(file, Buffer.from('FILEX-SAVED-THIS'), { expect: monitor.baseline }),
    );
    // Long past the debounce: the folder event of that rename has been seen
    // and judged.
    await sleep(1200);
    assert.deepEqual(reports.map((r) => r.text), [], "filex's own save was taken for somebody else's");
    assert.equal(monitor.baseline.sha256, mine.sha256, 'the write did not become the new baseline');
    // And a REAL outside change after it still is one.
    writeByRename(file, 'AGENT-AFTER-FILEX');
    const got = await nextReport(reports, 5000);
    assert.equal(got.text, 'AGENT-AFTER-FILEX');
  } finally {
    monitor.stop();
    fs.rmSync(dir, { recursive: true, force: true });
  }
});

test('watch: one outside version is reported ONCE, however many events and looks it takes; the next one again', async () => {
  const dir = tmp();
  const { file, monitor, reports } = await watching(dir, 'a.xlsx', 'ORIGINAL');
  try {
    await sleep(100);
    writeByRename(file, 'V1');
    await nextReport(reports, 5000);
    // The poll's safety-net look, several times over, and another event on
    // the same content (a touch).
    await monitor.check();
    await monitor.check();
    const later = new Date(Date.now() + 5000);
    fs.utimesSync(file, later, later);
    await sleep(800);
    assert.deepEqual(reports.map((r) => r.text), ['V1'], 'the same outside version was reported again');
    writeByRename(file, 'V2');
    const second = await nextReport(reports, 5000, 1);
    assert.equal(second.text, 'V2');
  } finally {
    monitor.stop();
    fs.rmSync(dir, { recursive: true, force: true });
  }
});

test("watch: the poll's look finds a change even where the folder watch heard nothing", async () => {
  // Network drives and some FUSE mounts deliver no events at all. The poll
  // that already runs every 2.5 s looks too.
  const dir = tmp();
  const file = path.join(dir, 'nas.xlsx');
  fs.writeFileSync(file, 'ORIGINAL');
  const reports: string[] = [];
  const monitor = new api.LocalDocMonitor({
    path: file,
    baseline: versionOf(file),
    onChange: (_v: ow.LocalVersion, bytes: Buffer) => reports.push(bytes.toString('utf8')),
  });
  try {
    // Never started: no watcher, only the look.
    writeByRename(file, 'FROM-THE-NAS');
    const drift = await monitor.check();
    assert.equal(drift.kind, 'changed');
    assert.deepEqual(reports, ['FROM-THE-NAS']);
  } finally {
    monitor.stop();
    fs.rmSync(dir, { recursive: true, force: true });
  }
});

test('drift: a timestamp that moved without the content changing is not a change (no false question)', async () => {
  const dir = tmp();
  const file = path.join(dir, 'touched.docx');
  fs.writeFileSync(file, 'SAME-BYTES');
  try {
    const seen = versionOf(file);
    const later = new Date(Date.now() + 60_000);
    fs.utimesSync(file, later, later);
    const drift = await api.localDrift(file, seen);
    assert.equal(drift.kind, 'same', 'a backup tool touching the file would have asked the user a question');
    assert.equal(drift.version.sha256, seen.sha256);
    assert.notEqual(drift.version.mtimeMs, seen.mtimeMs, 'the touched timestamp was not taken over');
    // …and a save after it lands: the gate is on content, not on the clock.
    await ow.writeBackAtomic(file, Buffer.from('FILEX-EDIT'), { expect: seen });
    assert.equal(fs.readFileSync(file, 'utf8'), 'FILEX-EDIT');
  } finally {
    fs.rmSync(dir, { recursive: true, force: true });
  }
});

test('drift: a document that is not there any more is "gone", not "changed"', async () => {
  const dir = tmp();
  const file = path.join(dir, 'gone.docx');
  fs.writeFileSync(file, 'X');
  try {
    const seen = versionOf(file);
    fs.rmSync(file);
    const drift = await api.localDrift(file, seen);
    assert.equal(drift.kind, 'gone');
  } finally {
    fs.rmSync(dir, { recursive: true, force: true });
  }
});

test('conflict copy: <name>.filex-conflict-<time>.<ext> beside the document, never over an existing file', async () => {
  const dir = tmp();
  const file = path.join(dir, 'Bütçe Özeti.xlsx');
  fs.writeFileSync(file, 'THEIRS');
  try {
    const now = new Date('2026-10-06T10:15:00Z');
    assert.equal(
      api.conflictPathFor(file, '20261006T101500'),
      path.join(dir, 'Bütçe Özeti.filex-conflict-20261006T101500.xlsx'),
    );
    const first = await api.writeConflictCopy(file, Buffer.from('MINE-1'), { now });
    const second = await api.writeConflictCopy(file, Buffer.from('MINE-2'), { now });
    assert.equal(path.dirname(first), dir);
    assert.match(path.basename(first), /^Bütçe Özeti\.filex-conflict-20261006T101500\.xlsx$/);
    assert.notEqual(second, first, 'the second copy in the same second overwrote the first');
    assert.equal(fs.readFileSync(first, 'utf8'), 'MINE-1');
    assert.equal(fs.readFileSync(second, 'utf8'), 'MINE-2');
    assert.equal(fs.readFileSync(file, 'utf8'), 'THEIRS', 'the document itself was touched');
  } finally {
    fs.rmSync(dir, { recursive: true, force: true });
  }
});

test('version: what filex records for a file is its size, its timestamp and the hash of its bytes', async () => {
  const dir = tmp();
  const file = path.join(dir, 'a.docx');
  fs.writeFileSync(file, 'HELLO');
  try {
    const got = await api.readLocalVersion(file);
    assert.ok(got, 'nothing was read');
    assert.equal(got.version.size, 5);
    assert.equal(got.version.sha256, crypto.createHash('sha256').update('HELLO').digest('hex'));
    assert.equal(got.bytes.toString('utf8'), 'HELLO');
    assert.equal(await api.readLocalVersion(path.join(dir, 'missing.docx')), null);
  } finally {
    fs.rmSync(dir, { recursive: true, force: true });
  }
});
