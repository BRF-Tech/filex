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

import { DesktopNotifier, isUnauthorized, newRows, opensInWindow, type NotificationRow } from '../src/notifications.ts';
import type { NotificationDestination } from '../../web/src/lib/notificationTarget.ts';
import type { NotificationText } from '../../web/src/lib/notificationText.ts';

const ACC = { id: 'a1', serverUrl: 'https://files.example.com', token: 't' };

/** `GET /api/notifications?unread=true` answers `{items, total}`; the rig
 *  hands over the same shape, with `total` defaulting to "all of them". */
function page(rows: NotificationRow[], total = rows.length) {
  return { items: rows, total };
}

function rig(pages: NotificationRow[][], locale: 'en' | 'tr' = 'en') {
  const shown: NotificationRow[] = [];
  const texts: NotificationText[] = [];
  const opened: unknown[] = [];
  const unread: number[] = [];
  let at = 0;
  const n = new DesktopNotifier({
    account: () => ACC,
    enabled: () => true,
    onOpen: (_id, dest) => opened.push(dest),
    fetchRows: async () => page(pages[Math.min(at++, pages.length - 1)]),
    onUnread: (_id, count) => unread.push(count),
    locale: () => locale,
    show: (row, text, onClick) => {
      shown.push(row);
      texts.push(text);
      onClick();
    },
  });
  return { n, shown, texts, opened, unread };
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
    fetchRows: async () => {
      const rows = pages[Math.min(page++, pages.length - 1)];
      return { items: rows, total: rows.length };
    },
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

// ── the count on the icon ──────────────────────────────────────────
//
// Rule 3, docs/NOTIFICATIONS.md → "The bell, and who can reach it": the unread
// count lives ON the icon, on every surface. The desktop window has no bell in
// it, so the icon is the dock / taskbar one — and until this it showed nothing
// at all: a person who missed the toast had no way to learn anything was
// waiting.

test('the unread count is reported on the FIRST poll, before anything is announced', async () => {
  // ⚠ The first poll deliberately announces nothing (it is establishing the
  // baseline). The badge must still be right from that first tick, or the app
  // sits there blank until something new happens to arrive.
  const { n, shown, unread } = rig([[{ id: 7, event: 'file.uploaded', title: 'a' }]]);
  await n.poll();
  assert.deepEqual(shown, []);
  assert.deepEqual(unread, [1]);
});

test('the count comes from the page total, not from the rows on screen', async () => {
  // ⚠ The poll asks for ten rows. Somebody with forty unread must see forty,
  // not ten — which is exactly what counting `items.length` would say.
  const seen: number[] = [];
  const n = new DesktopNotifier({
    account: () => ACC,
    enabled: () => true,
    onOpen: () => {},
    fetchRows: async () => ({ items: [{ id: 1, event: 'x' }], total: 40 }),
    onUnread: (_id, count) => seen.push(count),
    show: () => {},
  });
  await n.poll();
  assert.deepEqual(seen, [40]);
});

test('a failed poll leaves the last known count alone', async () => {
  // ⚠ A number that vanishes on a dropped request reads as "you have read
  // everything" — a claim about somebody's mail made by a network error.
  const seen: number[] = [];
  let fail = false;
  const n = new DesktopNotifier({
    account: () => ACC,
    enabled: () => true,
    onOpen: () => {},
    fetchRows: async () => {
      if (fail) throw new Error('server asleep');
      return { items: [{ id: 1, event: 'x' }], total: 3 };
    },
    onUnread: (_id, count) => seen.push(count),
    show: () => {},
  });
  await n.poll();
  fail = true;
  await n.poll();
  assert.deepEqual(seen, [3], 'a failed poll reported a count of its own');
});

// ⚠ Merge seam (feat/043-internal × the desktop): the internal branch gave a
// `file.trashed` notice a destination of its own — the Trash view — and the
// window forwarded folders only, so clicking that toast raised the window and
// left it wherever it was. The kinds the window carries out are asked of ONE
// function, and the trash is one of them.
test('a trashed-file toast opens the Trash view in the window, selecting the item', async () => {
  const { n, opened } = rig([
    [],
    [
      {
        id: 1,
        event: 'file.trashed',
        title: 'report.pdf',
        target: { kind: 'trash', storage: 'qldemo', path: '/Documents/report.pdf' },
      },
    ],
  ]);
  await n.poll();
  await n.poll();
  assert.deepEqual(opened, [{ kind: 'trash', select: 'qldemo://Documents/report.pdf' }]);
  assert.equal(opensInWindow(opened[0] as NotificationDestination), true);
});

test('the window carries out folders and the Trash view, nothing else', () => {
  assert.equal(opensInWindow({ kind: 'folder', storage: 'qldemo', folder: 'Documents' }), true);
  assert.equal(opensInWindow({ kind: 'trash' }), true);
  assert.equal(opensInWindow({ kind: 'share', token: 'abc' }), false, 'a share goes to the system browser');
  // An app's home page (feat/043-signing): the desktop has no such page, so
  // the window comes forward and stops (docs/NOTIFICATIONS.md).
  assert.equal(opensInWindow({ kind: 'app', plugin: 'sign', view: 'envelopes' }), false);
  assert.equal(opensInWindow({ kind: 'none' }), false);
});

// ── a token the server no longer accepts ─────────────────────────────────
//
// After a token was revoked the log held nothing but
// `[notify] notifications: poll failed {"err":"Error: server said 401"}`, every
// 15 seconds, forever. The poll is also often the ONLY thing that talks to
// the server at all — an account with no synced folders has no watcher — so
// it is the one that has to notice.

function unauthorized(): Error {
  return Object.assign(new Error('server said 401'), { status: 401 });
}

test('two 401s in a row report the account once; a success in between resets', async () => {
  const reported: string[] = [];
  const answers: Array<'ok' | '401' | '500'> = ['ok', '401', 'ok', '401', '500', '401', '401', '401'];
  let i = 0;
  const n = new DesktopNotifier({
    account: () => ACC,
    enabled: () => true,
    onOpen: () => {},
    fetchRows: async () => {
      const a = answers[Math.min(i++, answers.length - 1)];
      if (a === '401') throw unauthorized();
      if (a === '500') throw Object.assign(new Error('server said 500'), { status: 500 });
      return { items: [], total: 0 };
    },
    show: () => {},
    onUnauthorized: (id) => reported.push(id),
  });
  for (let k = 0; k < 5; k++) await n.poll();
  // ok, 401, ok, 401, 500: never two 401s back to back.
  assert.deepEqual(reported, []);
  await n.poll(); // 401 after a 500 — the 500 broke the run of 401s
  assert.deepEqual(reported, []);
  await n.poll(); // second 401 in a row
  assert.deepEqual(reported, ['a1']);
  await n.poll(); // still 401 — already reported
  assert.deepEqual(reported, ['a1']);
});

test('isUnauthorized reads the status, not the wording', () => {
  assert.equal(isUnauthorized(unauthorized()), true);
  assert.equal(isUnauthorized(new Error('server said 401')), false);
  assert.equal(isUnauthorized(Object.assign(new Error('x'), { status: 403 })), false);
  assert.equal(isUnauthorized(undefined), false);
});
