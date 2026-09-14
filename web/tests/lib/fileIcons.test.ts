// The two rows in a listing that are PLACES rather than file types.
//
// ⚠ Same reasoning as filePreview.test.ts and fileFilters.test.ts: this lives
// in @brftech/filex-core, the core package has no runner of its own, and the
// function is pure — so it is exercised here, in the app that ships it.
//
// What is worth pinning is not "does a mapping map". It is the ordering:
// a storage row and the trash row are BOTH `type: 'dir'`, so the folder
// branch is one line away from swallowing them, and the symptom of that would
// be a storage drawn as a plain folder — which is exactly what the "My files"
// root looked like before it was drawn as 💾 by three copies of one line in
// GridView, GalleryView and ListView.

import { describe, it, expect } from 'vitest';
import { iconFamilyFor, fileIconTile, iconSvg } from '@brftech/filex-core/src/lib/fileIcons';

describe('iconFamilyFor: places, not types', () => {
  it('a storage row is a storage, not a folder — even though it is a dir', () => {
    expect(iconFamilyFor({ type: 'dir', mime_type: 'inode/storage', basename: 'qldemo' }))
      .toBe('storage');
  });

  it('the trash row is trash, not a folder — even though it is a dir', () => {
    expect(iconFamilyFor({ type: 'dir', basename: '.trash' })).toBe('trash');
  });

  it('an ordinary folder is still a folder', () => {
    expect(iconFamilyFor({ type: 'dir', basename: 'Documents' })).toBe('folder');
  });

  it('the trash rule is on the NAME alone, whatever the node type says', () => {
    // ⚠ Deliberate, and worth pinning because it looks like an oversight: the
    // check is `basename === '.trash'` with no type test, which is exactly
    // what the three views' `specialEmojiFor` did before this moved into the
    // taxonomy. Adding a `type === 'dir'` condition here would be a guess
    // about how every caller builds that row — and `FileExplorer.loadTrash`
    // builds its rows by hand — so a file that happens to be called `.trash`
    // gets the bin glyph too. That is the pre-existing behaviour, not a new
    // decision, and it is a name nobody uses for a real file.
    expect(iconFamilyFor({ type: 'file', basename: '.trash', extension: '' })).toBe('trash');
  });

  it('a node with neither marker falls through to its extension', () => {
    expect(iconFamilyFor({ type: 'file', extension: 'pdf' })).toBe('pdf');
    expect(iconFamilyFor({ type: 'file', extension: 'zig' })).toBe('unknown');
  });
});

describe('the glyphs they get', () => {
  it('storage and trash render real SVG, never an emoji', () => {
    for (const family of ['storage', 'trash'] as const) {
      const svg = iconSvg(family);
      expect(svg).toContain('<svg');
      expect(svg).toContain(`fe-ficon--${family}`);
      // ⚠ The point of the change: no emoji anywhere in the markup. An emoji
      // is the operating system's idea of the shape, not ours, so it changes
      // between Windows, macOS and Linux and ignores the theme entirely.
      expect(svg).not.toMatch(/[\u{1F300}-\u{1FAFF}\u{2600}-\u{27BF}]/u);
    }
  });

  it('the tile a card, a gallery item and a list row all ask for is the same one', () => {
    const node = { type: 'dir', mime_type: 'inode/storage', basename: 'qldemo' };
    const tile = fileIconTile(node);
    expect(tile).toContain('fe-ftile--storage');
    // One definition: whatever the three views pass, they get this back.
    expect(fileIconTile({ ...node })).toBe(tile);
  });
});
