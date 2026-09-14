// gorunum:v1-preview — the two pure halves of the card preview.
//
// ⚠ Same reasoning as fileFilters.test.ts: both live in @brftech/filex-core,
// the core package has no test runner of its own, and both are pure — so they
// are exercised here, in the app that ships them. The LOADER is not tested
// here: it is a fetch + an IntersectionObserver, neither of which exists in
// jsdom, and a fake of both would only pin the fake.
//
// What is worth pinning is not "does a mapping map". It is the handful of
// answers this can quietly get wrong in a way nobody sees for a release:
//
//   - `.xlsx` is in the `sheet` FAMILY and is a zip container: reading its
//     first bytes gives binary, so it must not be claimed as a table;
//   - `.svg` is text but belongs to `image`, which has a REAL thumbnail — the
//     preview must not take the box away from it;
//   - a truncated read ends mid-line, and half a line of source on a card
//     reads as a bug rather than as a preview;
//   - a CSV value containing the separator must not become two columns;
//   - an unmapped extension must print SOMETHING (the uppercased suffix), not
//     an empty Type cell.
import { describe, it, expect } from 'vitest';
import { previewKindFor, parsePreview, splitCells } from '@brftech/filex-core/src/lib/filePreview';
import { typeLabelKey, typeLabelFor } from '@brftech/filex-core/src/lib/fileIcons';
import type { FileNode } from '@brftech/filex-core/src/types/FileNode';

function node(basename: string, over: Partial<FileNode> = {}): FileNode {
  const ext = basename.includes('.') ? basename.split('.').pop()! : '';
  return {
    path: `qldemo://${basename}`,
    basename,
    extension: ext,
    type: 'file',
    size: 1024,
    ...over,
  } as FileNode;
}

const bytes = (s: string) => new TextEncoder().encode(s);

describe('previewKindFor — which files we are willing to read', () => {
  it('claims code, plain text and the two delimited formats', () => {
    expect(previewKindFor(node('server.ts'))).toBe('code');
    expect(previewKindFor(node('compose.yml'))).toBe('code');
    expect(previewKindFor(node('README.md'))).toBe('text');
    expect(previewKindFor(node('notes.txt'))).toBe('text');
    expect(previewKindFor(node('sales.csv'))).toBe('table');
    expect(previewKindFor(node('inventory.tsv'))).toBe('table');
  });

  it('leaves anything with a real thumbnail alone', () => {
    // The whole design rests on this: the kinds claimed above are exactly the
    // ones the backend can only render a generic extension-card for.
    for (const n of ['photo.jpg', 'icon.svg', 'clip.mp4', 'song.mp3', 'report.pdf']) {
      expect(previewKindFor(node(n)), n).toBeNull();
    }
  });

  it('does not mistake a zip container for a spreadsheet', () => {
    // `xlsx`/`ods` share the `sheet` icon family with `csv`. Their first
    // kilobytes are PK\x03\x04, not a table.
    expect(previewKindFor(node('budget.xlsx'))).toBeNull();
    expect(previewKindFor(node('budget.ods'))).toBeNull();
    expect(previewKindFor(node('sales.csv'))).toBe('table');
  });

  it('refuses folders, sentinels, empty, sizeless and oversized files', () => {
    expect(previewKindFor(node('src', { type: 'dir', extension: '' }))).toBeNull();
    expect(previewKindFor(node('.trash', { type: 'dir', extension: '' }))).toBeNull();
    expect(previewKindFor(node('empty.ts', { size: 0 }))).toBeNull();
    expect(previewKindFor(node('unknown.ts', { size: undefined }))).toBeNull();
    expect(previewKindFor(node('huge.log', { size: 8 * 1024 * 1024 }))).toBeNull();
    expect(previewKindFor(node('fine.log', { size: 3 * 1024 * 1024 }))).toBe('text');
  });

  it('refuses a deleted row BOTH ways filex marks one', () => {
    // A normal listing sets `trashed`. The trash VIEW does not — it hand-rolls
    // its rows from TrashEntry and stamps extra_metadata.deleted_at instead
    // (FileExplorer.loadTrash). A row there points at the trash KEY, so a
    // content read would 404 once per row.
    expect(previewKindFor(node('old.ts', { trashed: true }))).toBeNull();
    expect(
      previewKindFor(
        node('old.ts', { extra_metadata: { deleted_at: '2026-09-01T10:00:00Z', ttl_days: 30 } }),
      ),
    ).toBeNull();
  });
});

describe('parsePreview — bytes to something drawable', () => {
  it('keeps interior blank lines but trims the ends', () => {
    const p = parsePreview(bytes('\n\nfirst\n\nsecond\n\n'), 'text', 'txt', false);
    expect(p?.lines.map((l) => l.indent + l.tint + l.text)).toEqual(['first', '', 'second']);
  });

  it('drops the last line when the byte cap cut it', () => {
    const whole = parsePreview(bytes('alpha\nbeta\ngam'), 'text', 'txt', false);
    expect(whole?.lines.at(-1)?.text).toBe('gam');
    const cut = parsePreview(bytes('alpha\nbeta\ngam'), 'text', 'txt', true);
    expect(cut?.lines.map((l) => l.text)).toEqual(['alpha', 'beta']);
  });

  it('reads CRLF the same as LF', () => {
    const p = parsePreview(bytes('one\r\ntwo\r\n'), 'text', 'txt', false);
    expect(p?.lines.map((l) => l.text)).toEqual(['one', 'two']);
  });

  it('tints a leading keyword, and only a keyword', () => {
    const p = parsePreview(bytes("import x from 'y';\nwidget.render();"), 'code', 'ts', false);
    expect(p?.lines[0].tint).toBe('import');
    expect(p?.lines[1].tint).toBe(''); // plain text beats fake colour
  });

  it('tints markdown markers in a text preview', () => {
    const p = parsePreview(bytes('# Title\nplain line\n- bullet'), 'text', 'md', false);
    expect(p?.lines.map((l) => l.tint)).toEqual(['#', '', '-']);
  });

  it('returns null for bytes that are not text', () => {
    expect(parsePreview(new Uint8Array([0x50, 0x4b, 0x03, 0x04, 0x00, 0x11]), 'code', 'ts', false)).toBeNull();
    // Ciphertext: no NUL, but it does not decode.
    const noisy = new Uint8Array(400).map((_, i) => (i % 3 === 0 ? 0x41 : 0xc3));
    expect(parsePreview(noisy, 'code', 'ts', false)).toBeNull();
  });

  it('decodes UTF-8 rather than mangling it', () => {
    const p = parsePreview(bytes('şirket ağırlık İstanbul'), 'text', 'txt', false);
    expect(p?.lines[0].text).toBe('şirket ağırlık İstanbul');
  });

  it('pads table rows to a rectangle and reports the column count', () => {
    const p = parsePreview(bytes('a,b,c\n1,2\n'), 'table', 'csv', false);
    expect(p?.cols).toBe(3);
    expect(p?.rows).toEqual([
      ['a', 'b', 'c'],
      ['1', '2', ''],
    ]);
  });

  it('splits a .tsv on tabs and a European .csv on semicolons', () => {
    expect(parsePreview(bytes('a\tb\n1\t2'), 'table', 'tsv', false)?.cols).toBe(2);
    expect(parsePreview(bytes('a;b;c\n1;2;3'), 'table', 'csv', false)?.cols).toBe(3);
  });
});

describe('splitCells — quoting', () => {
  it('keeps a separator inside quotes in one cell', () => {
    expect(splitCells('Bursa,"Nilufer, Ozluce",12', ',')).toEqual(['Bursa', 'Nilufer, Ozluce', '12']);
  });
  it('reads a doubled quote as one quote', () => {
    expect(splitCells('a,"say ""hi""",b', ',')).toEqual(['a', 'say "hi"', 'b']);
  });
});

describe('typeLabel — the Type column, in words', () => {
  const t = (k: string) => `t:${k}`;

  it('names the extension when it has a name of its own', () => {
    expect(typeLabelKey(node('app.ts'))).toBe('ftype.typescript');
    expect(typeLabelKey(node('notes.md'))).toBe('ftype.markdown');
    expect(typeLabelKey(node('data.csv'))).toBe('ftype.sheet');
    expect(typeLabelKey(node('report.pdf'))).toBe('ftype.pdf');
  });

  it('falls back to the family when the extension has none', () => {
    expect(typeLabelKey(node('photo.heic'))).toBe('ftype.image');
    expect(typeLabelKey(node('clip.mkv'))).toBe('ftype.video');
    expect(typeLabelKey(node('lib.rar'))).toBe('ftype.archive');
  });

  it('reuses node.folder rather than inventing a second word for it', () => {
    expect(typeLabelKey(node('src', { type: 'dir', extension: '' }))).toBe('node.folder');
  });

  it('prints the uppercased extension when nothing maps, never an empty cell', () => {
    expect(typeLabelKey(node('main.zig'))).toBeNull();
    expect(typeLabelFor(node('main.zig'), t)).toBe('ZIG');
    expect(typeLabelFor(node('LICENSE', { extension: '' }), t)).toBe('—');
  });

  it('is case-insensitive about the extension the backend reports', () => {
    expect(typeLabelFor(node('APP.TS', { extension: 'TS' }), t)).toBe('t:ftype.typescript');
  });
});
