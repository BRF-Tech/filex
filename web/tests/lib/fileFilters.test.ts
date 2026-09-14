// The drive shell's filter row (surucu:d1 / GitHub #14).
//
// ⚠ Same reasoning as connectionGuides.test.ts and shareCli.test.ts: the
// predicate lives in @brftech/filex-core, the core package has no test runner
// of its own, and this is a pure function — so it is exercised here, in the app
// that ships it.
//
// What is worth pinning is not "does a filter filter". It is the three places
// this can quietly answer a question it was not asked:
//
//   - a FOLDER has `size: 0` from the projector, so a naive "under 1 MB" lists
//     every folder in the drive and looks like a measurement,
//   - a file with no extension still has a `mime_type`, and dropping it from
//     "Images" hides exactly the photos people cannot find by name,
//   - a row with no timestamp is UNKNOWN, not "today".
//
// The "everything off" case is pinned too, because the explorer hands the
// result straight to the views: a new array on every listing would remount
// every card and undo the thumbnail cache for no reason.
import { describe, it, expect } from 'vitest';
import {
  EMPTY_FILTERS,
  activeFilterCount,
  applyFilters,
  filtersActive,
  peopleOptions,
  type DriveFilters,
} from '@brftech/filex-core/src/lib/fileFilters';
import type { FileNode } from '@brftech/filex-core/src/types/FileNode';

const MB = 1024 * 1024;
const NOW = Date.parse('2026-09-05T12:00:00Z');
const daysAgo = (n: number) => NOW - n * 86_400_000;

function file(over: Partial<FileNode> & { basename: string }): FileNode {
  const ext = over.basename.includes('.') ? over.basename.split('.').pop()! : '';
  return {
    path: `demo://${over.basename}`,
    type: 'file',
    extension: ext,
    size: 1000,
    last_modified: NOW,
    ...over,
  } as FileNode;
}

function dir(basename: string, over: Partial<FileNode> = {}): FileNode {
  return {
    path: `demo://${basename}`,
    basename,
    type: 'dir',
    size: 0,
    last_modified: NOW,
    ...over,
  } as FileNode;
}

const F = (over: Partial<DriveFilters>): DriveFilters => ({ ...EMPTY_FILTERS, ...over });

const TREE: FileNode[] = [
  dir('Photos'),
  dir('Documents'),
  file({ basename: 'beach.png', size: 3 * MB, last_modified: daysAgo(2) }),
  file({ basename: 'mountains.jpg', size: 12 * MB, last_modified: daysAgo(40) }),
  // No extension at all — the case a name-only classifier gets wrong.
  file({ basename: 'IMG_0042', extension: '', mime_type: 'image/jpeg', size: 900 * 1024 }),
  file({ basename: 'Q3 budget.xlsx', size: 40 * 1024, last_modified: daysAgo(9) }),
  file({ basename: 'Proposal.docx', size: 80 * 1024, last_modified: daysAgo(400) }),
  file({ basename: 'overview.pdf', size: 2 * MB }),
  file({ basename: 'app.ts', size: 4 * 1024 }),
  file({ basename: 'archive.zip', size: 200 * MB }),
  file({ basename: 'demo.mp4', size: 24 * MB }),
  file({ basename: 'notes.txt', size: 300 }),
];

const names = (rows: FileNode[]) => rows.map((r) => r.basename).sort();

describe('drive filters — the empty state', () => {
  it('with nothing set, hands back the SAME array, not a copy', () => {
    expect(applyFilters(TREE, EMPTY_FILTERS, NOW)).toBe(TREE);
    expect(filtersActive(EMPTY_FILTERS)).toBe(false);
    expect(activeFilterCount(EMPTY_FILTERS)).toBe(0);
  });

  it('counts the chips that are set, for the row that offers to clear them', () => {
    expect(activeFilterCount(F({ type: 'image' }))).toBe(1);
    expect(activeFilterCount(F({ type: 'image', size: 'gt100' }))).toBe(2);
    expect(activeFilterCount(F({ type: 'image', size: 'gt100', modified: 'today' }))).toBe(3);
  });
});

describe('drive filters — Type', () => {
  it('separates images from everything else', () => {
    expect(names(applyFilters(TREE, F({ type: 'image' }), NOW))).toEqual([
      'IMG_0042',
      'beach.png',
      'mountains.jpg',
    ]);
  });

  it('classifies a file with NO extension by its mime type', () => {
    // The point of the case: `IMG_0042` is above. Without the mime fallback it
    // would be invisible under Images, which is the one filter someone with a
    // camera roll actually reaches for.
    const hit = applyFilters(TREE, F({ type: 'image' }), NOW).find(
      (n) => n.basename === 'IMG_0042',
    );
    expect(hit).toBeDefined();
  });

  it('keeps documents and spreadsheets apart, and neither is a folder', () => {
    expect(names(applyFilters(TREE, F({ type: 'document' }), NOW))).toEqual([
      'Proposal.docx',
      'notes.txt',
    ]);
    expect(names(applyFilters(TREE, F({ type: 'spreadsheet' }), NOW))).toEqual(['Q3 budget.xlsx']);
    expect(names(applyFilters(TREE, F({ type: 'pdf' }), NOW))).toEqual(['overview.pdf']);
    expect(names(applyFilters(TREE, F({ type: 'code' }), NOW))).toEqual(['app.ts']);
  });

  it('Folders selects only directories', () => {
    expect(names(applyFilters(TREE, F({ type: 'folder' }), NOW))).toEqual(['Documents', 'Photos']);
  });

  it('every non-folder choice drops the directories', () => {
    for (const t of ['image', 'video', 'audio', 'pdf', 'document', 'archive', 'code'] as const) {
      const rows = applyFilters(TREE, F({ type: t }), NOW);
      expect(rows.every((r) => r.type === 'file'), `${t} let a folder through`).toBe(true);
    }
  });
});

describe('drive filters — Size', () => {
  it('a folder is never a size answer', () => {
    // ⚠ The trap this exists for: a directory row carries `size: 0`, so
    // "Under 1 MB" would otherwise list every folder in the drive.
    const small = applyFilters(TREE, F({ size: 'lt1' }), NOW);
    expect(small.some((r) => r.type === 'dir')).toBe(false);
    expect(names(small)).toEqual(['IMG_0042', 'Proposal.docx', 'Q3 budget.xlsx', 'app.ts', 'notes.txt']);
  });

  it('the bands do not overlap and do not leave a gap', () => {
    const bands = ['lt1', '1to10', '10to100', 'gt100'] as const;
    const seen = bands.flatMap((b) => names(applyFilters(TREE, F({ size: b }), NOW)));
    const files = TREE.filter((r) => r.type === 'file').map((r) => r.basename).sort();
    expect(seen.sort()).toEqual(files);
  });

  it('picks the big file out on its own', () => {
    expect(names(applyFilters(TREE, F({ size: 'gt100' }), NOW))).toEqual(['archive.zip']);
  });
});

describe('drive filters — Modified', () => {
  it('reads the last 7 and 30 days off the row timestamp', () => {
    const week = applyFilters(TREE, F({ modified: '7d' }), NOW);
    expect(week.map((r) => r.basename)).toContain('beach.png');
    expect(week.map((r) => r.basename)).not.toContain('Q3 budget.xlsx');

    const month = applyFilters(TREE, F({ modified: '30d' }), NOW);
    expect(month.map((r) => r.basename)).toContain('Q3 budget.xlsx');
    expect(month.map((r) => r.basename)).not.toContain('mountains.jpg');
  });

  it('"This year" is the calendar year, not the last 365 days', () => {
    const y = applyFilters(TREE, F({ modified: 'year' }), NOW);
    // 400 days back from 2026-09-05 lands in 2025.
    expect(y.map((r) => r.basename)).not.toContain('Proposal.docx');
    expect(y.map((r) => r.basename)).toContain('beach.png');
  });

  it('a row with NO timestamp is unknown, not today', () => {
    const undated = file({ basename: 'ghost.txt', last_modified: undefined });
    const rows = applyFilters([...TREE, undated], F({ modified: 'today' }), NOW);
    expect(rows.map((r) => r.basename)).not.toContain('ghost.txt');
  });
});

describe('drive filters — combined', () => {
  it('two chips are an AND, not an OR', () => {
    const rows = applyFilters(TREE, F({ type: 'image', size: '1to10' }), NOW);
    expect(names(rows)).toEqual(['beach.png']);
  });

  it('an impossible combination returns nothing, which is what the empty state is for', () => {
    expect(applyFilters(TREE, F({ type: 'folder', size: 'gt100' }), NOW)).toEqual([]);
  });
});

/* === gorunum:v1 — "Filter in this folder…" ==============================
 * The name box is part of the SAME model as the chips (DriveFilters.name), so
 * it has to obey the same three rules: nothing set → the identical array back,
 * something set → an AND with the chips, and "Clear" → everything again. The
 * folding matters too: filex is used in Turkish, and a name box that cannot
 * find "İstanbul" by typing "ist" is a box that does not work in half the
 * places this product runs.
 */
describe('drive filters — the name box', () => {
  it('is a substring of the name, case-insensitively', () => {
    expect(names(applyFilters(TREE, F({ name: 'bud' }), NOW))).toEqual(['Q3 budget.xlsx']);
    expect(names(applyFilters(TREE, F({ name: 'BUD' }), NOW))).toEqual(['Q3 budget.xlsx']);
  });

  it('folds accents, so Turkish names answer to what a Turkish keyboard types', () => {
    const tree = [
      ...TREE,
      file({ basename: 'İstanbul planı.docx', size: 2000 }),
      file({ basename: 'Ödev.pdf', size: 2000 }),
    ];
    expect(names(applyFilters(tree, F({ name: 'ist' }), NOW))).toEqual(['İstanbul planı.docx']);
    expect(names(applyFilters(tree, F({ name: 'odev' }), NOW))).toEqual(['Ödev.pdf']);
  });

  it('matches folders too — hiding the folder you just typed is the one result you meant', () => {
    expect(names(applyFilters(TREE, F({ name: 'photo' }), NOW))).toContain('Photos');
  });

  it('empty or whitespace is not a filter (same array back, no "Clear" offered)', () => {
    expect(applyFilters(TREE, F({ name: '' }), NOW)).toBe(TREE);
    expect(applyFilters(TREE, F({ name: '   ' }), NOW)).toBe(TREE);
    expect(filtersActive(F({ name: '  ' }))).toBe(false);
    expect(activeFilterCount(F({ name: '  ' }))).toBe(0);
  });

  it('counts as one active filter, and ANDs with a chip', () => {
    expect(filtersActive(F({ name: 'a' }))).toBe(true);
    expect(activeFilterCount(F({ name: 'a' }))).toBe(1);
    expect(activeFilterCount(F({ name: 'ea', type: 'image' }))).toBe(2);
    // 'ea' is in beach.png and in nothing else that is an image.
    expect(names(applyFilters(TREE, F({ name: 'ea', type: 'image' }), NOW))).toEqual(['beach.png']);
  });

  it('no match is an empty listing, which is what the empty state is for', () => {
    expect(applyFilters(TREE, F({ name: 'zzz-nothing' }), NOW)).toEqual([]);
  });
});

/* === gorunum:v1-advsearch — the three dimensions the dialog added ==========
 *
 * Same reasoning as the block above: these are the places the new choices can
 * quietly answer a question they were not asked.
 *
 *   - "around a date" with no date yet is UNFINISHED, not empty. Narrowing to
 *     zero rows the moment the mode is picked reads as a broken search.
 *   - a bare date must be read as LOCAL midnight. `Date.parse('2026-09-05')`
 *     is UTC by spec, so a ±1 hour window would slide by the reader's offset.
 *   - an untouched custom range has both ends open and must behave as "any
 *     size", never as 0 bytes.
 *   - `here`/`skip` with no base to measure against is INERT: counting it as
 *     an active filter would make the empty state blame a filter that filters
 *     nothing, and `Documents` must not claim `Documents-old`.
 */
describe('advanced-search filter dimensions', () => {
  const around = (over: Partial<DriveFilters>) =>
    F({ modified: 'around', aroundSpan: 'd1', ...over });

  /** The value a `datetime-local` input would hold for this instant.
   *  ⚠ Built from the LOCAL parts on purpose. A hardcoded `'2026-09-03T12:00'`
   *  passed here (UTC+3) and failed at UTC, which is exactly the confusion the
   *  code under test exists to remove — a test that only holds in one time zone
   *  would have pinned the bug instead of the behaviour. */
  const localInput = (ms: number) => {
    const d = new Date(ms);
    const p = (n: number) => String(n).padStart(2, '0');
    return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}T${p(d.getHours())}:${p(d.getMinutes())}`;
  };

  it('around: no anchor yet is inert, not empty', () => {
    expect(applyFilters(TREE, around({ aroundDate: '' }), NOW).length).toBe(TREE.length);
  });

  it('around: the window is half-width on each side of the anchor', () => {
    const anchor = localInput(daysAgo(2)); // beach.png
    expect(names(applyFilters(TREE, around({ aroundDate: anchor, aroundSpan: 'h1' }), NOW))).toEqual(
      ['beach.png'],
    );
    // A week out reaches the 2-day-old file and nothing 9 or 40 days old.
    expect(names(applyFilters(TREE, around({ aroundDate: anchor, aroundSpan: 'w1' }), NOW))).toContain(
      'Q3 budget.xlsx',
    );
    expect(
      names(applyFilters(TREE, around({ aroundDate: anchor, aroundSpan: 'w1' }), NOW)),
    ).not.toContain('mountains.jpg');
  });

  it('around: a bare date means that day LOCAL, not UTC', () => {
    // The anchor is the same instant written two ways; a UTC reading of the
    // bare form would move the window by the runner's offset and the two
    // results would disagree.
    const bare = applyFilters(TREE, around({ aroundDate: '2026-09-03', aroundSpan: 'd1' }), NOW);
    const explicit = applyFilters(
      TREE,
      around({ aroundDate: '2026-09-03T00:00', aroundSpan: 'd1' }),
      NOW,
    );
    expect(names(bare)).toEqual(names(explicit));
  });

  it('around: a garbage anchor is inert rather than empty', () => {
    expect(applyFilters(TREE, around({ aroundDate: 'not a date' }), NOW).length).toBe(TREE.length);
  });

  it('size range: both ends open behaves as "any size"', () => {
    const f = F({ size: 'range', sizeMin: null, sizeMax: null });
    // Folders still drop out of every size choice, as they do for the chips.
    expect(names(applyFilters(TREE, f, NOW))).not.toContain('Photos');
    expect(applyFilters(TREE, f, NOW).length).toBe(TREE.filter((n) => n.type !== 'dir').length);
  });

  it('size range: each end is inclusive and may stand alone', () => {
    expect(names(applyFilters(TREE, F({ size: 'range', sizeMin: 24 * MB }), NOW))).toEqual([
      'archive.zip',
      'demo.mp4',
    ]);
    expect(names(applyFilters(TREE, F({ size: 'range', sizeMax: 300 }), NOW))).toEqual(['notes.txt']);
    expect(
      names(applyFilters(TREE, F({ size: 'range', sizeMin: 2 * MB, sizeMax: 3 * MB }), NOW)),
    ).toEqual(['beach.png', 'overview.pdf']);
  });

  it('size range: a zero ceiling means nothing passes, not "unset"', () => {
    expect(applyFilters(TREE, F({ size: 'range', sizeMax: 0 }), NOW)).toEqual([]);
  });

  it('folder: here/skip partition the rows, and a prefix is not a name', () => {
    const rows: FileNode[] = [
      { path: 'demo://Documents/a.txt', basename: 'a.txt', type: 'file', size: 1 },
      { path: 'demo://Documents-old/b.txt', basename: 'b.txt', type: 'file', size: 1 },
      { path: 'demo://c.txt', basename: 'c.txt', type: 'file', size: 1 },
    ] as FileNode[];
    const base = 'demo://Documents';
    expect(names(applyFilters(rows, F({ pathMode: 'here', pathBase: base }), NOW))).toEqual(['a.txt']);
    expect(names(applyFilters(rows, F({ pathMode: 'skip', pathBase: base }), NOW))).toEqual([
      'b.txt',
      'c.txt',
    ]);
  });

  it('folder: a mode with no base is inert and does not count as active', () => {
    expect(filtersActive(F({ pathMode: 'here', pathBase: '' }))).toBe(false);
    expect(activeFilterCount(F({ pathMode: 'here', pathBase: '' }))).toBe(0);
    expect(applyFilters(TREE, F({ pathMode: 'here', pathBase: '' }), NOW).length).toBe(TREE.length);
    expect(activeFilterCount(F({ pathMode: 'skip', pathBase: 'demo://x' }))).toBe(1);
  });
});


/* ── People ────────────────────────────────────────────────────────────────
 *
 * The rows carry ownership the way the backend sends it: a row with NO
 * `owner_id` is SYSTEM (nobody put it here through filex), `owner_self` is the
 * server answering "this is yours" so the embeddable core never has to know
 * which account the host's session belongs to, and `owner_name` is resolved
 * server-side in one batched lookup for the whole page.
 */
const OWNED: FileNode[] = [
  file({ basename: 'mine.txt', owner_id: 1, owner_self: true, owner_name: 'Ada Lovelace' } as never),
  file({ basename: 'also-mine.txt', owner_id: 1, owner_self: true, owner_name: 'Ada Lovelace' } as never),
  file({ basename: 'hers.txt', owner_id: 2, owner_name: 'Grace Hopper' } as never),
  file({ basename: 'his.txt', owner_id: 3, owner_name: 'Alan Turing' } as never),
  // The scanner found these: no owner key at all.
  file({ basename: 'found.bin' }),
  dir('Scanned'),
  // Handed in through a drop link: owned by the link's creator, marked as
  // having arrived from outside.
  file({ basename: 'submitted.pdf', owner_id: 2, owner_name: 'Grace Hopper', external_upload: true } as never),
];

describe('drive filters — People', () => {
  it('anyone is the neutral member and filters nothing', () => {
    expect(applyFilters(OWNED, F({ people: 'any' }), NOW)).toBe(OWNED);
    expect(filtersActive(F({ people: 'any' }))).toBe(false);
    expect(activeFilterCount(F({ people: 'any' }))).toBe(0);
  });

  it('you: only the rows the SERVER said are yours', () => {
    expect(names(applyFilters(OWNED, F({ people: 'me' }), NOW))).toEqual([
      'also-mine.txt',
      'mine.txt',
    ]);
    expect(activeFilterCount(F({ people: 'me' }))).toBe(1);
  });

  it('system: the ownerless rows, folders included — and NOT the ones that merely belong to someone else', () => {
    expect(names(applyFilters(OWNED, F({ people: 'system' }), NOW))).toEqual(['Scanned', 'found.bin']);
  });

  it('a named account keeps only that account, drop-link uploads included', () => {
    expect(names(applyFilters(OWNED, F({ people: 'u:2' }), NOW))).toEqual([
      'hers.txt',
      'submitted.pdf',
    ]);
    expect(names(applyFilters(OWNED, F({ people: 'u:3' }), NOW))).toEqual(['his.txt']);
  });

  it('an id nobody owns yields nothing rather than everything', () => {
    expect(applyFilters(OWNED, F({ people: 'u:99' }), NOW)).toEqual([]);
  });

  it('combines with the other dimensions instead of replacing them', () => {
    const rows = applyFilters(OWNED, F({ people: 'u:2', type: 'pdf' }), NOW);
    expect(names(rows)).toEqual(['submitted.pdf']);
    expect(activeFilterCount(F({ people: 'u:2', type: 'pdf' }))).toBe(2);
  });

  it('options: the fixed three always, then only the OTHER accounts on screen', () => {
    expect(peopleOptions(OWNED).map((o) => o.value)).toEqual([
      'any',
      'me',
      'system',
      'u:3',
      'u:2',
    ]);
    // …by name, so the menu reads like a list of people and not of ids.
    expect(peopleOptions(OWNED).map((o) => o.name)).toEqual([
      undefined,
      undefined,
      undefined,
      'Alan Turing',
      'Grace Hopper',
    ]);
  });

  it('options: your own account is never listed twice — it is "You"', () => {
    expect(peopleOptions(OWNED).filter((o) => o.value === 'u:1')).toEqual([]);
  });

  it('options: an empty listing still offers the three that need no data', () => {
    expect(peopleOptions([]).map((o) => o.value)).toEqual(['any', 'me', 'system']);
  });
});
