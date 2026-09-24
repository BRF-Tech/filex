// The rules behind the folder chooser, without a DOM.
//
// These exist because filex had no folder chooser at all: `Move to` and
// `Copy to` were absent from the selection bar not because the operation was
// missing (transferItems has always moved and copied, across storages) but
// because nothing in the client could say WHERE. The rules below are the part
// that has to be right before any pixel is drawn.

import { describe, it, expect } from 'vitest';
import {
  DRIVES,
  blockedReason,
  crumbsOfWire,
  destinationRows,
  driveRows,
  initialLocation,
  isAtOrInside,
  joinWire,
  labelOfWire,
  parentOfWire,
  permAllowsWrite,
  splitWire,
} from '@brftech/filex-core/src/lib/destinationTree';
import type { FileNode } from '@brftech/filex-core/src/types/FileNode';

const dir = (path: string, perm?: string): FileNode =>
  ({ path, basename: path.split('/').pop() || path, type: 'dir', perm }) as FileNode;
const file = (path: string): FileNode =>
  ({ path, basename: path.split('/').pop() || path, type: 'file' }) as FileNode;

describe('wire paths', () => {
  it('splits and rejoins', () => {
    expect(splitWire('main://a/b')).toEqual(['main', 'a/b']);
    expect(splitWire('main://')).toEqual(['main', '']);
    expect(splitWire('a/b')).toEqual(['', 'a/b']);
    expect(joinWire('main', 'a/b')).toBe('main://a/b');
    expect(joinWire('main', '')).toBe('main://');
    expect(joinWire('main', '/a/')).toBe('main://a');
  });

  it('walks up, and stops at the storage root when there is only one storage', () => {
    expect(parentOfWire('main://a/b', true)).toBe('main://a');
    expect(parentOfWire('main://a', true)).toBe('main://');
    // With several storages, above a root is the list of drives…
    expect(parentOfWire('main://', true)).toBe(DRIVES);
    // …and with one it is nowhere, so the Up button is disabled rather than
    // landing the user on a one-row list of the drive they are already in.
    expect(parentOfWire('main://', false)).toBeNull();
    expect(parentOfWire(DRIVES, true)).toBeNull();
  });

  it('builds a clickable breadcrumb from the root down', () => {
    expect(crumbsOfWire('main://a/b/c')).toEqual([
      { label: 'main', path: 'main://' },
      { label: 'a', path: 'main://a' },
      { label: 'b', path: 'main://a/b' },
      { label: 'c', path: 'main://a/b/c' },
    ]);
    expect(crumbsOfWire(DRIVES)).toEqual([]);
  });

  it('labels a path by its last segment', () => {
    expect(labelOfWire('main://a/b', 'Drives')).toBe('b');
    expect(labelOfWire('main://', 'Drives')).toBe('main');
    expect(labelOfWire(DRIVES, 'Drives')).toBe('Drives');
  });
});

describe('permAllowsWrite', () => {
  // ⚠ Absent AND empty both mean "this storage does not enforce ACL", which is
  // the pre-RBAC default and full access. Reading '' as "no permission" would
  // grey out every folder on every storage that has RBAC switched off — which
  // is most of them.
  it('treats an unenforced storage as writable', () => {
    expect(permAllowsWrite(undefined)).toBe(true);
    expect(permAllowsWrite('')).toBe(true);
  });
  it('follows the same levels as the toolbar', () => {
    expect(permAllowsWrite('owner')).toBe(true);
    expect(permAllowsWrite('editor')).toBe(true);
    expect(permAllowsWrite('viewer')).toBe(false);
    expect(permAllowsWrite('none')).toBe(false);
  });
});

describe('isAtOrInside', () => {
  it('is path-boundary aware', () => {
    expect(isAtOrInside('main://a/b', 'main://a')).toBe(true);
    expect(isAtOrInside('main://a', 'main://a')).toBe(true);
    // "ab" is not inside "a" — a prefix match without the boundary would say
    // it was, and would block a perfectly good destination.
    expect(isAtOrInside('main://ab', 'main://a')).toBe(false);
    expect(isAtOrInside('main://a', 'main://a/b')).toBe(false);
  });
  it('a storage root contains everything in its own storage and nothing outside', () => {
    expect(isAtOrInside('main://a/b', 'main://')).toBe(true);
    expect(isAtOrInside('other://a/b', 'main://')).toBe(false);
  });
});

describe('blockedReason', () => {
  it('names the two mistakes separately', () => {
    expect(blockedReason('main://docs', ['main://docs'])).toBe('self');
    expect(blockedReason('main://docs/2026', ['main://docs'])).toBe('descendant');
    expect(blockedReason('main://other', ['main://docs'])).toBeNull();
  });

  // ⚠ Two storages that happen to use the same folder name are two different
  // trees. Blocking this would break a legitimate cross-storage move, which
  // internal/ops explicitly supports.
  it('does not confuse the same name on another storage', () => {
    expect(blockedReason('other://docs', ['main://docs'])).toBeNull();
    expect(blockedReason('other://docs/x', ['main://docs'])).toBeNull();
  });

  it('normalizes trailing slashes before comparing', () => {
    expect(blockedReason('main://docs', ['main://docs/'])).toBe('self');
  });

  it('checks every folder being moved, not just the first', () => {
    expect(blockedReason('main://b/inner', ['main://a', 'main://b'])).toBe('descendant');
  });

  it('nothing is blocked when nothing is being moved', () => {
    expect(blockedReason('main://docs', undefined)).toBeNull();
    expect(blockedReason('main://docs', [])).toBeNull();
  });
});

describe('destinationRows', () => {
  it('lists folders only', () => {
    const rows = destinationRows([dir('main://a'), file('main://f.txt'), dir('main://b')]);
    expect(rows.map((r) => r.label)).toEqual(['a', 'b']);
  });

  // ⚠ An unwritable folder stays LISTED and stays openable: the grant that
  // allows writing may live on a subfolder, so hiding the parent makes the
  // destination unreachable. Hiding it also tells the user their folder is
  // gone rather than that they may not write into it.
  it('keeps an unwritable folder, marked', () => {
    const rows = destinationRows([dir('main://ro', 'viewer')]);
    expect(rows).toHaveLength(1);
    expect(rows[0].writable).toBe(false);
    expect(rows[0].blocked).toBeNull();
  });

  it('marks the folder being moved and its children', () => {
    const rows = destinationRows(
      [dir('main://docs'), dir('main://docs2'), dir('main://other')],
      ['main://docs'],
    );
    expect(rows.find((r) => r.label === 'docs')!.blocked).toBe('self');
    // "docs2" starts with "docs" but is not inside it.
    expect(rows.find((r) => r.label === 'docs2')!.blocked).toBeNull();
    expect(rows.find((r) => r.label === 'other')!.blocked).toBeNull();
  });
});

describe('driveRows', () => {
  it('turns storage names into roots', () => {
    expect(driveRows(['main', 's3'])).toEqual([
      { path: 'main://', label: 'main', writable: true, blocked: null, kind: 'dir' },
      { path: 's3://', label: 's3', writable: true, blocked: null, kind: 'dir' },
    ]);
  });
  it('blocks a drive whose whole root is being moved', () => {
    expect(driveRows(['main'], ['main://'])[0].blocked).toBe('self');
  });
});

describe('destinationRows — files', () => {
  const files = [
    { path: 'main://docs', basename: 'docs', type: 'dir' as const },
    { path: 'main://nda.pdf', basename: 'nda.pdf', type: 'file' as const },
  ];
  it('lists folders only by default', () => {
    expect(destinationRows(files).map((r) => r.path)).toEqual(['main://docs']);
  });
  it('lists files too when asked, as un-blocked, readable choices', () => {
    const rows = destinationRows(files, ['main://docs'], { files: true });
    expect(rows.map((r) => [r.path, r.kind, r.blocked])).toEqual([
      ['main://docs', 'dir', 'self'],
      ['main://nda.pdf', 'file', null],
    ]);
  });
});

describe('initialLocation', () => {
  // The picker opens where the person is standing, because that is where most
  // destinations are.
  it('opens at the current folder', () => {
    expect(initialLocation('main://a/b', ['main', 's3'])).toBe('main://a/b');
  });
  it('falls back to the drives list when there are several storages', () => {
    expect(initialLocation('', ['main', 's3'])).toBe(DRIVES);
    expect(initialLocation(undefined, ['main', 's3'])).toBe(DRIVES);
  });
  it('falls back to the only storage root when there is just one', () => {
    expect(initialLocation('', ['main'])).toBe('main://');
  });
});
