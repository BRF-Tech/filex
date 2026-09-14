// What the desktop notifier is allowed to say, and where a click goes.
//
// Run:  node --experimental-strip-types --test desktop/test/notifications.test.ts
//
// Three things are worth a test here and nothing else is. (1) A baseline: an
// app that has just started must not replay the backlog as toasts — get this
// wrong and every launch throws ten notifications at the user. (2) The
// destination: it must come from the SHARED resolver, because the whole point
// of the field is that the bell, the browser toast and this one land in the
// same place. (3) The SENTENCE: same shared catalogue, same reason — and it
// must never be the raw event id, which is what a native toast actually showed
// before this was wired (`{title: "file.uploaded"}`, measured 2026-09-12).

import assert from 'node:assert/strict';
import test from 'node:test';

import { DesktopNotifier, newRows, type NotificationRow } from '../src/notifications.ts';
import type { NotificationText } from '../../web/src/lib/notificationText.ts';

const ACC = { id: 'a1', serverUrl: 'https://files.example.com', token: 't' };

function rig(pages: NotificationRow[][], locale: 'en' | 'tr' = 'en') {
  const shown: NotificationRow[] = [];
  const texts: NotificationText[] = [];
  const opened: unknown[] = [];
  let page = 0;
  const n = new DesktopNotifier({
    account: () => ACC,
    enabled: () => true,
    onOpen: (_id, dest) => opened.push(dest),
    fetchRows: async () => pages[Math.min(page++, pages.length - 1)],
    locale: () => locale,
    show: (row, text, onClick) => {
      shown.push(row);
      texts.push(text);
      onClick();
    },
  });
  return { n, shown, texts, opened };
}

test('the first poll establishes a baseline and announces nothing', async () => {
  const { n, shown } = rig([[{ id: 7, event: 'file.uploaded', title: 'a' }]]);
  await n.poll();
  assert.deepEqual(shown, []);
});

test('…and only rows above that baseline are announced, oldest first', async () => {
  const { n, shown } = rig([
    [{ id: 7, event: 'file.uploaded', title: 'old' }],
    [
      { id: 9, event: 'file.uploaded', title: 'second' },
      { id: 8, event: 'file.uploaded', title: 'first' },
      { id: 7, event: 'file.uploaded', title: 'old' },
    ],
  ]);
  await n.poll();
  await n.poll();
  assert.deepEqual(
    shown.map((r) => r.title),
    ['first', 'second'],
  );
  // A third poll over the same rows says nothing again.
  await n.poll();
  assert.equal(shown.length, 2);
});

test('a click resolves through the SHARED resolver, not a local guess', async () => {
  const { n, opened } = rig([
    [],
    [
      {
        id: 1,
        event: 'file.uploaded',
        title: 'report.pdf',
        target: { kind: 'file', storage: 'qldemo', path: 'Documents/report.pdf' },
      },
    ],
  ]);
  await n.poll();
  await n.poll();
  // The folder, plus the row to select — exactly what the browser bell gets.
  assert.deepEqual(opened, [
    { kind: 'folder', storage: 'qldemo', folder: 'Documents', select: 'qldemo://Documents/report.pdf' },
  ]);
});

test('the switch silences the toast but keeps the baseline moving', async () => {
  const shown: NotificationRow[] = [];
  let on = false;
  let page = 0;
  const pages: NotificationRow[][] = [
    [],
    [{ id: 1, event: 'x', title: 'while off' }],
    [
      { id: 2, event: 'x', title: 'after on' },
      { id: 1, event: 'x', title: 'while off' },
    ],
  ];
  const n = new DesktopNotifier({
    account: () => ACC,
    enabled: () => on,
    onOpen: () => {},
    fetchRows: async () => pages[Math.min(page++, pages.length - 1)],
    show: (row) => shown.push(row),
  });
  await n.poll(); // baseline
  await n.poll(); // arrives while the switch is off
  on = true;
  await n.poll();
  // Turning it back on must not replay what happened while it was off.
  assert.deepEqual(
    shown.map((r) => r.title),
    ['after on'],
  );
});

test('newRows is > baseline, never >=', () => {
  const rows: NotificationRow[] = [
    { id: 5, event: 'x' },
    { id: 6, event: 'x' },
  ];
  assert.deepEqual(newRows(rows, 5).map((r) => r.id), [6]);
  assert.deepEqual(newRows(rows, 6).map((r) => r.id), []);
});

// ── the sentence ─────────────────────────────────────────────────────────

test('the toast says a sentence in the reader language, never the event id', async () => {
  const row: NotificationRow = {
    id: 1,
    event: 'file.uploaded',
    // ⚠ Exactly what the server stores for this event: no real title (Send
    // substitutes the event id) and a bare path for a body.
    title: 'file.uploaded',
    body: 'Documents/rapor.pdf',
    meta: { origin: 'manager', node: { path: 'Documents/rapor.pdf', name: 'rapor.pdf' } },
  };

  const en = rig([[], [row]]);
  await en.n.poll();
  await en.n.poll();
  assert.deepEqual(en.texts, [{ title: 'New file: rapor.pdf', body: 'Documents/rapor.pdf' }]);

  const tr = rig([[], [row]], 'tr');
  await tr.n.poll();
  await tr.n.poll();
  assert.deepEqual(tr.texts, [{ title: 'Yeni dosya: rapor.pdf', body: 'Documents/rapor.pdf' }]);

  // The property that matters more than either string: whatever it says, it is
  // not the wire format.
  for (const t of [...en.texts, ...tr.texts]) {
    assert.ok(!t.title.includes('file.uploaded'), `raw event id leaked: ${t.title}`);
  }
});

test('an unknown event falls back to the server title, not to the id', async () => {
  const { n, texts } = rig([
    [],
    [{ id: 2, event: 'disk_full', title: 'Disk almost full', body: '/data at 96%' }],
  ]);
  await n.poll();
  await n.poll();
  assert.deepEqual(texts, [{ title: 'Disk almost full', body: '/data at 96%' }]);
});
