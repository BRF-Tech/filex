// What a transfer between two places MEANS — the question the explorer got
// wrong for every storage pair.
//
// Measured on 2026-08-29 (v0.26.1): with two storages configured, Ctrl+X in one
// storage and Ctrl+V in another queued a COPY. The user was told "move"
// nowhere — the toast said copy — but the gesture they made was cut, and the
// file stayed in both places. The server side of the same bug was worse: the
// paste was accepted and written into the SOURCE storage.

import { describe, expect, it } from 'vitest';

import { resolveTransfer, wireAdapterOf } from '../../../packages/core/src/lib/transfer';

describe('wireAdapterOf', () => {
  it('reads the depo off a wire path', () => {
    expect(wireAdapterOf('s3-test://a/b.txt')).toBe('s3-test');
  });
  it('is empty for a bare path — legacy embedders send those', () => {
    expect(wireAdapterOf('a/b.txt')).toBe('');
  });
});

describe('resolveTransfer', () => {
  const inAlpha = ['alpha://a.txt', 'alpha://b.txt'];

  it('drag inside one depo moves', () => {
    expect(resolveTransfer(inAlpha, 'alpha://hedef')).toEqual({ kind: 'move', cross: false });
  });

  it('drag across depolar copies — the rule Explorer and Finder taught everyone', () => {
    expect(resolveTransfer(inAlpha, 'beta://hedef')).toEqual({ kind: 'copy', cross: true });
  });

  it('CUT + paste across depolar is a MOVE, not a copy', () => {
    // The regression this file exists for: the explorer downgraded every
    // cross-storage transfer to a copy, so "cut" left the file in both places.
    expect(resolveTransfer(inAlpha, 'beta://hedef', 'move')).toEqual({ kind: 'move', cross: true });
  });

  it('COPY + paste is a copy wherever it lands', () => {
    expect(resolveTransfer(inAlpha, 'alpha://hedef', 'copy')).toEqual({ kind: 'copy', cross: false });
    expect(resolveTransfer(inAlpha, 'beta://hedef', 'copy')).toEqual({ kind: 'copy', cross: true });
  });

  it('notices when only SOME of the selection crosses', () => {
    expect(resolveTransfer(['alpha://a.txt', 'beta://b.txt'], 'beta://hedef')).toEqual({
      kind: 'copy',
      cross: true,
    });
  });

  it('an unprefixed source is not a depo change — it means "where I already am"', () => {
    expect(resolveTransfer(['a.txt'], 'alpha://hedef')).toEqual({ kind: 'move', cross: false });
  });

  it('a target with no prefix (single-storage embed) never counts as crossing', () => {
    expect(resolveTransfer(['alpha://a.txt'], 'hedef')).toEqual({ kind: 'move', cross: false });
  });
});
