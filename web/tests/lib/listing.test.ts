// The virtual `.trash` row, and the one listing it must stay out of.
//
// ⚠ Same reasoning as connectionGuides.test.ts and shareCli.test.ts: the helper
// lives in @brftech/filex-core, the core package has no test runner of its own,
// and this is a pure function — so it is exercised here, in the app that ships
// it.
//
// The bug this pins (found in a v0.31.0 screenshot, where a card named "Trash"
// sat next to the one real hit of a search for "brief"): a search answers with
// the SCOPE it searched, so `?action=search&path=main://&filter=brief` comes
// back carrying `dirname: "main://"` — byte-identical to the folder listing of
// the same path. `injectTrashRow` decided from `dirname` alone, so it could not
// tell the two apart and unshifted the sentinel into the search results.
//
// Measured before the fix: the server sends no `.trash` row for either action
// (`action=index` → ['Q3 campaign', 'notes.txt'], `action=search` → []), and
// `isStorageRootDir("main://")` is true for both responses. The row was always
// the client's, and the rule that placed it could not see the difference.
import { describe, it, expect } from 'vitest';
import {
  injectTrashRow,
  isStorageRootDir,
  isTrashListing,
  makeTrashRow,
} from '@brftech/filex-core/src/lib/listing';
import type { FileNode } from '@brftech/filex-core/src/types/FileNode';

function listing(...names: string[]): FileNode[] {
  return names.map(
    (n) =>
      ({
        path: `main://${n}`,
        basename: n,
        type: n.includes('.') ? 'file' : 'dir',
        size: 10,
      }) as FileNode,
  );
}

const names = (rows: FileNode[]) => rows.map((r) => r.basename);

describe('the virtual trash row — where it belongs', () => {
  it('goes at the FRONT of a storage-root folder listing', () => {
    const rows = listing('Documents', 'notes.txt');
    expect(injectTrashRow(rows, 'main', 'main://', true)).toBe(true);
    expect(names(rows)).toEqual(['.trash', 'Documents', 'notes.txt']);
  });

  it('also treats the legacy `fileman` root as the root', () => {
    const rows = listing('notes.txt');
    expect(injectTrashRow(rows, 'main', 'main://fileman', true)).toBe(true);
    expect(names(rows)[0]).toBe('.trash');
  });

  it('stays out of a subfolder', () => {
    const rows = listing('brief.md');
    expect(injectTrashRow(rows, 'main', 'main://Documents', true)).toBe(false);
    expect(names(rows)).toEqual(['brief.md']);
  });

  it('never lands inside the trash view itself', () => {
    const rows = listing('deleted.txt');
    expect(injectTrashRow(rows, 'main', 'main://fileman/.trash', true)).toBe(false);
    expect(names(rows)).toEqual(['deleted.txt']);
  });

  it('is not drawn at all when the host turned it off', () => {
    const rows = listing('notes.txt');
    expect(injectTrashRow(rows, 'main', 'main://', false)).toBe(false);
    expect(names(rows)).toEqual(['notes.txt']);
  });
});

describe('the virtual trash row — the search leak', () => {
  // ⚠ THE regression. Without the fifth argument this call is indistinguishable
  // from the root-listing case above, and that is exactly how the sentinel got
  // into a search result.
  it('stays out of a search result scoped to the storage root', () => {
    const hits = listing('brief.md');
    expect(injectTrashRow(hits, 'main', 'main://', true, { isSearchResult: true })).toBe(false);
    expect(names(hits)).toEqual(['brief.md']);
  });

  it('a search result and a folder listing are the SAME dirname — which is why the flag exists', () => {
    // The premise the old rule got wrong, pinned so nobody "simplifies" the
    // flag away by arguing dirname is enough.
    expect(isStorageRootDir('main://')).toBe(true);
    expect(isTrashListing('main://')).toBe(false);

    const asListing = listing('brief.md');
    const asSearch = listing('brief.md');
    injectTrashRow(asListing, 'main', 'main://', true, { isSearchResult: false });
    injectTrashRow(asSearch, 'main', 'main://', true, { isSearchResult: true });
    expect(names(asListing)).toContain('.trash');
    expect(names(asSearch)).not.toContain('.trash');
  });

  it('a search that found nothing shows nothing, not a lone Trash card', () => {
    const none: FileNode[] = [];
    expect(injectTrashRow(none, 'main', 'main://', true, { isSearchResult: true })).toBe(false);
    expect(none).toEqual([]);
  });

  it('the row it would have injected is a synthetic dir, not anything on disk', () => {
    // Why the leak was misleading rather than merely untidy: it renders as a
    // 0-byte folder with a path no listing will ever return.
    const row = makeTrashRow('main');
    expect(row.type).toBe('dir');
    expect(row.basename).toBe('.trash');
    expect(row.size).toBe(0);
    expect(row.path).toBe('main://fileman/.trash');
  });
});

describe('the virtual trash row — the second door', () => {
  // The row exists for one reason: a listing with no other way into the bin.
  // Once the navigation panel carries a Trash entry that reason is gone, and
  // what is left is a 0-byte folder that is not a folder, sitting among real
  // ones, three inches from the panel entry that goes to the same place.
  // Owner's decision, this release: do not offer the same door twice.

  it('is NOT drawn when the panel is already offering Trash', () => {
    const rows = listing('Documents', 'notes.txt');
    expect(injectTrashRow(rows, 'main', 'main://', true, { navOffersTrash: true })).toBe(false);
    expect(names(rows)).toEqual(['Documents', 'notes.txt']);
  });

  it('IS still drawn when there is no panel to offer it', () => {
    // The other direction, and the one that keeps the feature alive: an embed
    // with `sideNav: false` (or the default under `rootPath`) has no other door.
    const rows = listing('Documents', 'notes.txt');
    expect(injectTrashRow(rows, 'main', 'main://', true, { navOffersTrash: false })).toBe(true);
    expect(names(rows)).toEqual(['.trash', 'Documents', 'notes.txt']);
  });

  it('says nothing = the historical behaviour, so an old caller keeps its row', () => {
    const rows = listing('notes.txt');
    expect(injectTrashRow(rows, 'main', 'main://', true)).toBe(true);
    expect(names(rows)[0]).toBe('.trash');
  });

  it('the host switch outranks the panel: trashVisible false means no Trash anywhere', () => {
    // Both orders, because this is the one that must not be reachable by
    // reasoning "the panel is not offering it, so the listing should".
    const a = listing('notes.txt');
    const b = listing('notes.txt');
    expect(injectTrashRow(a, 'main', 'main://', false, { navOffersTrash: true })).toBe(false);
    expect(injectTrashRow(b, 'main', 'main://', false, { navOffersTrash: false })).toBe(false);
    expect(names(a)).toEqual(['notes.txt']);
    expect(names(b)).toEqual(['notes.txt']);
  });

  it('the two suppressions are independent — either one alone is enough', () => {
    const panelOnly = listing('brief.md');
    const searchOnly = listing('brief.md');
    const both = listing('brief.md');
    expect(
      injectTrashRow(panelOnly, 'main', 'main://', true, {
        navOffersTrash: true,
        isSearchResult: false,
      }),
    ).toBe(false);
    expect(
      injectTrashRow(searchOnly, 'main', 'main://', true, {
        navOffersTrash: false,
        isSearchResult: true,
      }),
    ).toBe(false);
    expect(
      injectTrashRow(both, 'main', 'main://', true, {
        navOffersTrash: true,
        isSearchResult: true,
      }),
    ).toBe(false);
    for (const rows of [panelOnly, searchOnly, both]) expect(names(rows)).toEqual(['brief.md']);
  });
});
