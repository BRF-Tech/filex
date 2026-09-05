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
