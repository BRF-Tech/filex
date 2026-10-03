// #103 - a file double-clicked while the explorer is between two listings
// opened read-only.
//
// A row with no `perm` of its own was judged by `dirPerm`, the level of the
// folder the explorer was showing at the moment of the click. Leaving a
// virtual view (Starred, Recent, a tag) for a folder clears the view at once
// and keeps its rows on screen until the folder answers; in that gap
// `dirPerm` is '' and `permCanEdit('')` is false, so the editor opened as a
// read-only preview. lib/rowLevel holds the two rules the explorer now uses:
// a folder's rows carry the folder's level from the moment they are listed,
// and an empty (unknown) folder level gates nothing.
import { describe, expect, it } from 'vitest';
import { fallbackRowLevel, withListingLevel } from '../../../packages/core/src/lib/rowLevel';
import type { FileNode } from '../../../packages/core/src/types/FileNode';

const row = (name: string, perm?: FileNode['perm']): FileNode =>
  ({ type: 'file', path: `docs://${name}`, basename: name, extension: 'md', ...(perm ? { perm } : {}) }) as FileNode;

/** permCanEdit, as FileExplorer defines it: undefined = ungated. */
const canEdit = (p: string | undefined) => p === undefined || p === 'editor' || p === 'owner';

describe('a folder listing hands its level to its rows', () => {
  it('a row with no level of its own takes the folder’s', () => {
    const [a] = withListingLevel([row('a.md')], 'editor');
    expect(a.perm).toBe('editor');
  });

  it('keeps it after the explorer has forgotten the folder (a view was opened, its answer not in yet)', () => {
    const [a] = withListingLevel([row('a.md')], 'viewer');
    // The row now answers for itself: nothing the explorer forgets later can
    // turn a viewer's row into an editable one, or an editor's into read-only.
    expect(typeof a.perm).toBe('string');
    expect(canEdit(a.perm)).toBe(false);
  });

  it('never overwrites a row’s own level (a file locked by an app, a per-file grant)', () => {
    const rows = withListingLevel([row('locked.md', 'viewer'), row('mine.md', 'owner')], 'editor');
    expect(rows.map((r) => r.perm)).toEqual(['viewer', 'owner']);
  });

  it('leaves the rows alone when the listing states no level (ACL off, an older host)', () => {
    const rows = [row('a.md')];
    expect(withListingLevel(rows, '')).toBe(rows);
    expect(withListingLevel(rows, undefined)).toBe(rows);
    expect(withListingLevel(rows, 'admin')).toBe(rows);
  });

  it('copies, never mutates, the rows it was given', () => {
    const original = row('a.md');
    withListingLevel([original], 'owner');
    expect(original.perm).toBeUndefined();
  });
});

describe('a row with no level, judged with no folder level known', () => {
  it('is NOT read-only - the gap between leaving a view and the folder’s answer', () => {
    // dirPerm is '' here: the virtual view forgot the folder on entry, and the
    // folder being opened has not answered yet. This is the state the
    // double-click landed in.
    expect(fallbackRowLevel(false, '')).toBeUndefined();
    expect(canEdit(fallbackRowLevel(false, ''))).toBe(true);
  });

  it('is ungated in a virtual view, as before', () => {
    expect(fallbackRowLevel(true, '')).toBeUndefined();
    expect(fallbackRowLevel(true, 'viewer')).toBeUndefined();
  });

  it('takes the folder’s level when one is known', () => {
    expect(fallbackRowLevel(false, 'viewer')).toBe('viewer');
    expect(canEdit(fallbackRowLevel(false, 'viewer'))).toBe(false);
    expect(fallbackRowLevel(false, 'owner')).toBe('owner');
  });
});
