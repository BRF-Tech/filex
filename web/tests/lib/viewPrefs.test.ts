// Per-folder view memory, and the table configuration beside it.
//
// ⚠⚠ Why this file exists. This is the kind of feature that rots silently:
// nothing on screen looks wrong when a folder stops being remembered, because
// the global default is a perfectly plausible answer. There is no error, no
// blank space, no red — just a product that used to do something and quietly
// does not any more. The two gates that keep it alive are therefore the two
// nobody would notice failing: a folder's setup survives a round trip through
// the stored document, and the cap really evicts.
//
// ⚠ The transport is injected (`attachViewPrefsStore`), which is the whole
// reason the module takes one: these tests drive the real save/load path with
// two functions and no server, so a change to the document's SHAPE breaks here
// rather than in production against somebody's saved arrangements.
import { beforeEach, describe, expect, it, vi } from 'vitest';

import {
  COLUMNS,
  FOLDER_CAP,
  NAME_AUTO,
  NAME_MIN,
  __flushViewPrefs,
  __resetViewPrefs,
  attachViewPrefsStore,
  canMoveColumn,
  columnHidden,
  columnOrder,
  columnWidth,
  columnsCustomised,
  freezeWidths,
  folderColumnStore,
  folderIsRemembered,
  folderKey,
  folderMemoryEnabled,
  folderPrefs,
  forgetAllFolders,
  forgetFolder,
  moveColumn,
  moveColumnBy,
  rememberFolder,
  rememberedCount,
  resetColumns,
  setColumnHidden,
  setColumnWidth,
  setFolderMemoryEnabled,
  tableLayout,
  touchFolder,
  viewPrefsSlot,
  widthsAreAuto,
  type ColumnId,
} from '@brftech/filex-core';

/** A fake server: one document, held in a variable. */
function attach(initial: unknown = {}) {
  const saved: unknown[] = [];
  attachViewPrefsStore({
    load: async () => initial,
    save: (doc) => saved.push(JSON.parse(JSON.stringify(doc))),
  });
  return saved;
}

/** The module loads asynchronously; let the microtask that resolves it run. */
const settle = () => new Promise<void>((r) => setTimeout(r, 0));

beforeEach(() => {
  __resetViewPrefs();
  vi.useRealTimers();
});

describe('folderKey', () => {
  it('is one key per folder, whatever punctuation the caller brought', () => {
    expect(folderKey('thumbfix', 'Photos/2026')).toBe('thumbfix/Photos/2026');
    expect(folderKey('thumbfix', '/Photos//2026/')).toBe('thumbfix/Photos/2026');
    expect(folderKey('thumbfix', 'thumbfix://Photos/2026')).toBe('thumbfix/Photos/2026');
  });

  it('has no trailing slash at a storage root', () => {
    // ⚠ Measured, 2026-09-13: `thumbfix/` and `thumbfix` were two keys for one
    // folder, and the seeded default for `.recent` never fired because the
    // explorer asked for `.recent/`.
    expect(folderKey('thumbfix', '')).toBe('thumbfix');
    expect(folderKey('.recent', '')).toBe('.recent');
  });

  it('does not collide across storages', () => {
    expect(folderKey('a', 'Docs')).not.toBe(folderKey('b', 'Docs'));
  });

  it('takes whatever storage REF the host gave it, uid included', () => {
    // The point of accepting a ref rather than a name: the moment a host fills
    // in `config.storages[].uid`, keys become rename-proof with no change here.
    const uid = '2f1c0b3a-7d41-4a9b-9c2e-5a6b7c8d9e0f';
    expect(folderKey(uid, 'Docs')).toBe(`${uid}/Docs`);
  });
});

describe('the per-folder memory is always on', () => {
  /* ⚠⚠ It used to be an opt-in, OFF by default — and off, every change in any
   * folder became the view of every folder (owner, 2026-09-21: "tüm
   * klasörlerde görünüm değişikliği geçerli oluyor"). A host that genuinely
   * wants no per-folder memory says so (`config.rememberFolderView: false`);
   * a person's settings no longer can. */
  it('is on with no document at all', async () => {
    attach({});
    await settle();
    expect(folderMemoryEnabled()).toBe(true);
  });

  it('reads the stored folders even from a document written with the old switch OFF', async () => {
    // A folder somebody set up while the switch was off was still written
    // down (the recorder always wrote); hiding it now would lose it.
    attach({ on: false, f: { 'a/b': { v: 'grid', t: 1 } } });
    await settle();
    expect(folderPrefs('a/b')).toEqual({ v: 'grid' });
    expect(folderIsRemembered('a/b')).toBe(true);
    expect(rememberedCount()).toBe(1);
  });

  it('the old switch is a no-op now', async () => {
    attach({});
    await settle();
    setFolderMemoryEnabled(false);
    rememberFolder('a/b', { v: 'grid' });
    expect(folderPrefs('a/b')).toEqual({ v: 'grid' });
  });

  it('answers Recent’s seeded sort, and a folder’s own choice beats the seed', async () => {
    attach({});
    await settle();
    expect(folderPrefs('.recent')).toEqual({ k: 'modified', d: 'desc' });
    // A view-mode-only memory keeps the seeded sort…
    rememberFolder('.recent', { v: 'grid' });
    expect(folderPrefs('.recent')).toEqual({ k: 'modified', d: 'desc', v: 'grid' });
    // …and a sort the person chose replaces it.
    rememberFolder('.recent', { k: 'name', d: 'asc' });
    expect(folderPrefs('.recent')).toEqual({ v: 'grid', k: 'name', d: 'asc' });
  });

  it('a folder keeps its own COLUMNS, and "reset" sends it back to the default ones', async () => {
    attach({});
    await settle();
    const a = folderColumnStore(() => 's/A');
    const b = folderColumnStore(() => 's/B');
    a.setWidth('size', 150);
    expect(a.width('size')).toBe(150);
    // ⚠ The other folder did not move — the leak, for columns.
    expect(b.width('size')).toBe(COLUMNS.find((c) => c.id === 'size')!.width);
    expect(folderPrefs('s/A')?.c?.w?.size).toBe(150);
    a.reset();
    expect(folderPrefs('s/A')).toBeNull();
    expect(a.width('size')).toBe(COLUMNS.find((c) => c.id === 'size')!.width);
  });
});

describe('a folder’s setup survives a round trip', () => {
  it('is written into the document and read back out of it', async () => {
    const saved = attach({ on: true });
    await settle();

    rememberFolder('thumbfix/Photos', { v: 'gallery', k: 'name', d: 'asc' });
    __flushViewPrefs();
    expect(saved.length).toBe(1);

    // Now boot a SECOND session from exactly what the first one saved — which
    // is the round trip that matters, and the one a shape change breaks.
    const doc = saved[saved.length - 1];
    __resetViewPrefs();
    attach(doc);
    await settle();

    expect(folderMemoryEnabled()).toBe(true);
    expect(folderPrefs('thumbfix/Photos')).toEqual({ v: 'gallery', k: 'name', d: 'asc' });
  });

  it('carries the columns in the same document', async () => {
    const saved = attach({ on: true });
    await settle();

    setColumnWidth('modified', 210);
    setColumnHidden('owner', true);
    moveColumn('size', 0);
    __flushViewPrefs();

    const doc = saved[saved.length - 1];
    __resetViewPrefs();
    attach(doc);
    await settle();

    expect(columnWidth('modified')).toBe(210);
    expect(columnHidden('owner')).toBe(true);
    /* Name is pinned first whatever the document says, so "moved to the front"
       means the front of the columns a person may actually move. */
    expect(columnOrder()[0]).toBe('name');
    expect(columnOrder()[1]).toBe('size');
  });

  it('never writes before the document has landed', async () => {
    // ⚠ The failure this prevents is "my settings reset themselves": a save
    // fired while the fetch is in flight writes this session's empty defaults
    // over everything the person had arranged.
    const saved: unknown[] = [];
    let release: (v: unknown) => void = () => {};
    attachViewPrefsStore({
      load: () => new Promise((r) => (release = r)),
      save: (doc) => saved.push(doc),
    });
    setColumnWidth('size', 120);
    __flushViewPrefs();
    expect(saved).toHaveLength(0);

    release({ on: true });
    await settle();
    setColumnWidth('size', 130);
    __flushViewPrefs();
    expect(saved).toHaveLength(1);
  });

  it('writes nothing at all when there is nobody to write for', async () => {
    // An embed on a shared app token, or a public share link: `load` resolves
    // null. It must degrade to "remember for this session only" — never to an
    // error, and never to a write that would 401 on every change.
    const saved: unknown[] = [];
    attachViewPrefsStore({ load: async () => null, save: (d) => saved.push(d) });
    await settle();
    rememberFolder('a/b', { v: 'grid' });
    setColumnWidth('size', 120);
    __flushViewPrefs();
    expect(saved).toHaveLength(0);
    // The folder DOES keep its view while the page is open — only the
    // session, but still per folder.
    expect(folderPrefs('a/b')).toEqual({ v: 'grid' });
  });
});

describe('the cap evicts', () => {
  it('keeps exactly FOLDER_CAP folders, dropping the least recently used', async () => {
    attach({ on: true });
    await settle();

    // One more than the cap, each "used" at a distinct moment.
    const now = Math.floor(Date.now() / 1000);
    vi.useFakeTimers();
    for (let i = 0; i < FOLDER_CAP + 20; i++) {
      vi.setSystemTime((now + i) * 1000);
      rememberFolder(`s/f${i}`, { v: 'grid' });
    }
    vi.useRealTimers();

    expect(rememberedCount()).toBe(FOLDER_CAP);
    // The oldest twenty are gone, the newest are kept.
    expect(folderIsRemembered('s/f0')).toBe(false);
    expect(folderIsRemembered('s/f19')).toBe(false);
    expect(folderIsRemembered('s/f20')).toBe(true);
    expect(folderIsRemembered(`s/f${FOLDER_CAP + 19}`)).toBe(true);
  });

  it('evicts by LAST USED, not by last written', async () => {
    // ⚠ A folder you keep visiting must not age out because you have not
    // re-sorted it lately — which is what evicting on write time would do.
    attach({ on: true });
    await settle();
    const now = Math.floor(Date.now() / 1000);
    vi.useFakeTimers();
    vi.setSystemTime(now * 1000);
    rememberFolder('s/old-but-loved', { v: 'grid' });
    // Fill to exactly the cap, so nothing has been evicted yet.
    for (let i = 0; i < FOLDER_CAP - 1; i++) {
      vi.setSystemTime((now + 1 + i) * 1000);
      rememberFolder(`s/f${i}`, { v: 'grid' });
    }
    expect(rememberedCount()).toBe(FOLDER_CAP);
    // Visiting it now, long after it was last written.
    vi.setSystemTime((now + 5000) * 1000);
    touchFolder('s/old-but-loved');
    // One more, which pushes exactly one entry out.
    vi.setSystemTime((now + 5001) * 1000);
    rememberFolder('s/newest', { v: 'grid' });
    vi.useRealTimers();

    expect(folderIsRemembered('s/old-but-loved')).toBe(true);
    expect(folderIsRemembered('s/f0')).toBe(false);
  });

  it('does not create an entry merely because a folder was opened', async () => {
    // The map is the set of folders somebody deliberately configured. If a
    // walk-through created entries, the cap would be a working limit rather
    // than a safety net, and "Apply to all folders" would report a number
    // nobody recognises.
    attach({ on: true });
    await settle();
    touchFolder('s/just-passing-through');
    expect(rememberedCount()).toBe(0);
  });
});

describe('the escape hatch', () => {
  it('forgets every folder, and one folder at a time', async () => {
    attach({ on: true });
    await settle();
    rememberFolder('s/a', { v: 'grid' });
    rememberFolder('s/b', { v: 'gallery' });
    expect(rememberedCount()).toBe(2);

    forgetFolder('s/a');
    expect(folderIsRemembered('s/a')).toBe(false);
    expect(folderIsRemembered('s/b')).toBe(true);

    forgetAllFolders();
    expect(rememberedCount()).toBe(0);
  });
});

describe('columns', () => {
  it('clamps a width to the column’s own bounds, in both directions', async () => {
    attach({ on: true });
    await settle();
    const spec = COLUMNS.find((c) => c.id === 'modified')!;
    setColumnWidth('modified', 5);
    expect(columnWidth('modified')).toBe(spec.min);
    setColumnWidth('modified', 9999);
    expect(columnWidth('modified')).toBe(spec.max);
  });

  it('clamps a width stored by an older build, on the way IN', async () => {
    // Otherwise a hand-edited or legacy document reproduces the crushed-Name
    // layout this module exists to prevent, and nothing in the UI explains it.
    attach({ on: true, c: { w: { modified: 5000 } } });
    await settle();
    expect(columnWidth('modified')).toBe(COLUMNS.find((c) => c.id === 'modified')!.max);
  });

  it('reconciles a stored order with the columns that exist today', async () => {
    // ⚠ A stored order is a list written by an OLDER build. Trusting it as-is
    // would make a column added since invisible to everyone who had ever
    // reordered anything — a feature that ships and then does not appear for
    // exactly the engaged users.
    attach({ on: true, c: { o: ['size', 'modified', 'gone-since'] } });
    await settle();
    const order = columnOrder();
    expect(order.slice(0, 3)).toEqual(['name', 'size', 'modified']);
    expect(order).not.toContain('gone-since' as ColumnId);
    for (const c of COLUMNS) expect(order).toContain(c.id);
  });

  it('moves one step at a time and stops at the ends', async () => {
    attach({ on: true });
    await settle();
    const movable = columnOrder().filter((id) => COLUMNS.find((c) => c.id === id)?.hideable);
    const first = movable[0];
    const last = movable[movable.length - 1];
    expect(canMoveColumn(first, -1)).toBe(false);
    expect(canMoveColumn(last, 1)).toBe(false);

    moveColumnBy(first, 1);
    expect(columnOrder().filter((id) => COLUMNS.find((c) => c.id === id)?.hideable)[1]).toBe(first);
  });

  it('will not move or hide the star — it is a control, not a value', async () => {
    attach({ on: true });
    await settle();
    const before = columnOrder().join();
    moveColumn('star', 0);
    setColumnHidden('star', true);
    expect(columnOrder().join()).toBe(before);
    expect(columnHidden('star')).toBe(false);
  });

  it('resets widths, order and visibility together', async () => {
    attach({ on: true });
    await settle();
    setColumnWidth('size', 150);
    setColumnHidden('type', true);
    moveColumn('size', 0);
    expect(columnsCustomised()).toBe(true);
    resetColumns();
    expect(columnsCustomised()).toBe(false);
    expect(columnWidth('size')).toBe(COLUMNS.find((c) => c.id === 'size')!.width);
  });
});

describe('tableLayout — a table, not a fit', () => {
  const all: ColumnId[] = ['type', 'location', 'owner', 'modified', 'size', 'star'];
  /* An ordinary folder: every row shares one location, so the caller does not
     offer that column at all. This is the case the owner reviews. */
  const folder: ColumnId[] = ['type', 'owner', 'modified', 'size', 'star'];
  /* 711px is the listing's measured width inside a 960px window. */
  const REVIEW = 711;

  it('never drops a column for want of room — that is what the scroll is for', async () => {
    // ⚠⚠ THE regression gate for this change. The pass that came before shed
    // the least informative column whenever the tracks stopped fitting and the
    // header menu said "no room"; the owner rejected the model outright. Width
    // has no vote now, at any width, including one no column could fit in.
    attach({});
    await settle();
    for (const w of [0, 200, 358, REVIEW, 960, 1400, 4000]) {
      expect(tableLayout(w, all).visible, `at ${w}px`).toEqual(all);
    }
  });

  it('is as wide as its columns, and scrolls when that is more than the pane', async () => {
    attach({});
    await settle();
    // Six columns, their minimums, plus Name's opening width and the chrome:
    // more than a phone has. The honest answer is a table wider than the pane.
    const phone = tableLayout(358, all);
    expect(phone.total).toBeGreaterThan(358);
    /* ⚠ Name opens at its own MINIMUM here, not at NAME_AUTO (v0.43.0).
       This line used to read `toBe(NAME_AUTO)`: 220 of a 358px pane, with
       every other column starting past the right edge, so a phone's first
       sight of the list was one column and a sliver. The lead gives way once
       the others have given everything they can — down to NAME_MIN, which is
       the width the product already calls the narrowest a person may drag
       Name to, never the 85px crush the case below guards. The table is
       still wider than the pane and still scrolls; what changed is what is
       painted before anybody scrolls. Measured in a browser:
       e2e/tests/117-narrow-table-columns.spec.ts. */
    expect(phone.widths.name).toBe(NAME_MIN);
    expect(phone.widths.name).toBeLessThan(NAME_AUTO);
    // And the sum really is the total, so the header and the rows agree.
    const tracks = phone.visible.reduce((s, id) => s + phone.widths[id], 0);
    expect(phone.total).toBe(28 + 28 + 24 + 8 * (phone.visible.length + 2) + phone.widths.name + tracks);
  });

  it('opens filling the pane exactly when the pane can hold it', async () => {
    // First sight, nothing ever dragged: the widths are derived from the pane,
    // so a table nobody has configured is neither clipped nor short.
    attach({});
    await settle();
    for (const w of [REVIEW, 960, 1191, 1400]) {
      expect(tableLayout(w, folder).total, `at ${w}px`).toBe(w);
    }
  });

  it('pays for Name out of the other columns before it gives up and scrolls', async () => {
    attach({});
    await settle();
    const wide = tableLayout(1191, folder);
    const review = tableLayout(REVIEW, folder);
    // At 1440 there is slack and Name takes it — it is the column being read.
    expect(wide.widths.name).toBeGreaterThan(NAME_AUTO);
    expect(wide.widths.modified).toBe(COLUMNS.find((c) => c.id === 'modified')!.width);
    // At 960 there is not, so Modified and the rest give ground toward their
    // own minimums rather than Name being crushed to 85px as it once was.
    expect(review.widths.name).toBeGreaterThanOrEqual(NAME_AUTO);
    expect(review.widths.modified).toBeLessThan(wide.widths.modified);
    expect(review.widths.modified).toBeGreaterThanOrEqual(
      COLUMNS.find((c) => c.id === 'modified')!.min,
    );
  });

  it('drops the pane’s opinion the moment somebody drags a column', async () => {
    // ⚠⚠ "I have never touched this" and "I deliberately made Name narrow"
    // must be different states, or the first resize is undone by the next
    // pane resize. The stored widths are the difference.
    attach({ on: true });
    await settle();
    expect(widthsAreAuto()).toBe(true);

    freezeWidths(tableLayout(REVIEW, folder).widths);
    setColumnWidth('name', 140);
    expect(widthsAreAuto()).toBe(false);

    // A different pane, and Name is still exactly where it was put.
    for (const w of [358, REVIEW, 1400]) {
      expect(tableLayout(w, folder).widths.name, `at ${w}px`).toBe(140);
    }
  });

  it('treats a document written before Name was a column as still auto', async () => {
    // ⚠⚠ The migration. Every document that exists today carries widths and has
    // never been asked about Name — the owner's own says `{size: 89, type: 125}`
    // from the layout he rejected. Reading that as "configured" would open his
    // table at Name's shipped default, wider than his pane and scrolled
    // sideways on first sight, at a width nobody chose.
    attach({ on: true, c: { w: { size: 89, type: 125 }, hidden: [], o: [] } });
    await settle();
    expect(widthsAreAuto()).toBe(true);
    const m = tableLayout(REVIEW, folder);
    expect(m.total).toBe(REVIEW);
    expect(m.widths.name).toBeGreaterThanOrEqual(NAME_AUTO);
    // And the two numbers he DID choose are the starting point, not discarded.
    expect(tableLayout(1400, folder).widths.type).toBe(125);
  });

  it('freezes the WHOLE row on that first drag, so narrowing one column does not widen Name', async () => {
    // The defect being fixed, in one assertion: Name used to be the flexible
    // track, so shrinking Size grew Name. It cannot, because Size's neighbours
    // were all committed before the drag began.
    attach({ on: true });
    await settle();
    const before = tableLayout(REVIEW, folder);
    freezeWidths(before.widths);
    setColumnWidth('size', COLUMNS.find((c) => c.id === 'size')!.min);
    const after = tableLayout(REVIEW, folder);
    expect(after.widths.name).toBe(before.widths.name);
    expect(after.total).toBeLessThan(before.total);
  });

  it('leaves the slack on the right when the table is narrower than the pane', async () => {
    // Rule 4: it does not stretch to fill and it does not centre. The layout's
    // job is to be narrower than the pane; the stylesheet packs the cells left.
    attach({ on: true });
    await settle();
    freezeWidths(tableLayout(REVIEW, folder).widths);
    for (const id of folder) {
      const spec = COLUMNS.find((c) => c.id === id)!;
      if (spec.resizable) setColumnWidth(id, spec.min);
    }
    setColumnWidth('name', COLUMNS.find((c) => c.id === 'name')!.min);
    expect(tableLayout(REVIEW, folder).total).toBeLessThan(REVIEW);
  });

  it('lets Name be dragged in BOTH directions, like every other column', async () => {
    attach({ on: true });
    await settle();
    const spec = COLUMNS.find((c) => c.id === 'name')!;
    expect(spec.resizable).toBe(true);
    setColumnWidth('name', 5);
    expect(columnWidth('name')).toBe(spec.min);
    setColumnWidth('name', 5000);
    expect(columnWidth('name')).toBe(spec.max);
  });

  it('keeps Name pinned first however the stored order was written', async () => {
    // A document written before Name was a column has it appended at the end
    // by the reconciliation; the table draws it first regardless, so the order
    // has to say so too.
    attach({ on: true, c: { o: ['size', 'modified'] } });
    await settle();
    expect(columnOrder()[0]).toBe('name');
    expect(tableLayout(1400, all).visible).not.toContain('name' as ColumnId);
  });

  it('draws only the columns the caller offered', async () => {
    attach({});
    await settle();
    // Location is meaningless in an ordinary folder; the caller says so.
    expect(tableLayout(1400, ['type', 'modified', 'size']).visible).not.toContain('location');
  });

  it('hides what the person hid, at any width', async () => {
    attach({ on: true });
    await settle();
    setColumnHidden('modified', true);
    expect(tableLayout(1400, all).visible).not.toContain('modified');
    expect(tableLayout(358, all).visible).not.toContain('modified');
  });
});

/* ────────────────────────────────────────────────────────────────────────
 * THE DOCUMENT BELONGS TO MORE THAN ONE FEATURE.
 *
 * ⚠⚠ Why these four are a gate and not a nicety. The endpoint stores an
 * OPAQUE object — `backend/internal/api/handlers/viewprefs.go` validates it as
 * JSON, bounds its size and deliberately does not look inside, so that the
 * shape can change without the server changing with it. The CLIENT was the
 * half that broke that promise: `currentDoc()` rebuilt `{on, f, c}` from
 * scratch on every save, so a key written by any other feature was erased by
 * the next column drag. Measured before the fix, and one resize was enough:
 *
 *     loaded: {"on":true,"f":{},"c":{},"install":{"dismissed":1}}
 *     saved:  {"on":true,"f":{},"c":{"w":{"size":130},"hidden":[]}}
 *
 * Nothing threw, nothing rendered wrong, nothing was logged. The failure is
 * INVISIBLE from inside the feature that lost its value — which is why it can
 * only be held by a test that writes a foreign key, makes an unrelated change,
 * and looks at what went out on the wire. The desktop-app reminder's "never
 * show me again" was kept in `localStorage` for precisely as long as this was
 * broken.
 * ──────────────────────────────────────────────────────────────────────── */
describe('somebody else’s keys survive our saves', () => {
  it('carries an unknown top-level key through a save it had nothing to do with', async () => {
    const saved = attach({ on: true, install: { dismissed: true }, futureThing: [1, 2, 3] });
    await settle();

    // An unrelated change: drag a column. This is the exact gesture that used
    // to drop the other keys.
    setColumnWidth('size', 130);
    __flushViewPrefs();

    expect(saved).toHaveLength(1);
    const doc = saved[0] as Record<string, unknown>;
    expect(doc.install).toEqual({ dismissed: true });
    expect(doc.futureThing).toEqual([1, 2, 3]);
    // …and our own half still went out with it (the default layout lives in
    // the person's default, `p.c`, since the columns became per folder).
    expect((doc.p as { c: { w: Record<string, number> } }).c.w.size).toBe(130);
  });

  it('gives a feature its own corner, readable and writable by name', async () => {
    const saved = attach({ on: true });
    await settle();
    const slot = viewPrefsSlot<{ dismissed?: boolean }>('install');

    expect(slot.get()).toBeUndefined();
    slot.set({ dismissed: true });
    expect(slot.get()).toEqual({ dismissed: true });
    __flushViewPrefs();
    expect((saved.at(-1) as Record<string, unknown>).install).toEqual({ dismissed: true });

    slot.clear();
    __flushViewPrefs();
    expect(saved.at(-1)).not.toHaveProperty('install');
  });

  it('reads what was already in the document, and only its own key', async () => {
    attach({ on: true, install: { dismissed: true }, other: { x: 1 } });
    await settle();
    expect(viewPrefsSlot<{ dismissed?: boolean }>('install').get()).toEqual({ dismissed: true });
    expect(viewPrefsSlot<{ x?: number }>('other').get()).toEqual({ x: 1 });
  });

  it('refuses the three names this module owns, rather than renaming them', () => {
    // A slot quietly relocated to `c2` would be a value the caller could never
    // read back — and one that landed ON `c` would erase the column setup.
    for (const reserved of ['on', 'f', 'c', 'p', 'g']) {
      expect(() => viewPrefsSlot(reserved)).toThrow(/reserved/);
    }
  });

  it('writes nothing at all when there is nobody to write for', async () => {
    // A share link / an app-token embed: `load` answers null. The slot must be
    // inert, not throw — its caller falls back to the browser (see
    // `web/src/composables/useInstallPrompt.ts`).
    const saved: unknown[] = [];
    attachViewPrefsStore({ load: async () => null, save: (d) => saved.push(d) });
    await settle();
    const slot = viewPrefsSlot<{ dismissed?: boolean }>('install');
    expect(slot.persistable()).toBe(false);
    slot.set({ dismissed: true });
    __flushViewPrefs();
    expect(saved).toHaveLength(0);
  });
});
