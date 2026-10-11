// #243 (wiring:e2, #189) - a row of an encrypted folder or a vault never
// leaves the window by a drag, wherever it is listed.
//
// The drag-out hands a file to the operating system: the browser's
// `DownloadURL` (the plain download URL in a cookie session, a minted link in a
// bearer one) or the desktop shell's OS drag. For a file of an encrypted
// folder the bytes behind it are ciphertext, saved under the file's name as if
// they were the document; a vault row's path is its plaintext name, sent to
// the server by the download it starts.
//
// The refusal in FileExplorer handDragOut read the OPEN folder (`e2eActive`)
// and an `e2e_root` the rows handed to it never carried: `hitItem` (a ⌘K hit)
// and the listing's drag both kept only path, name and kind. So a ⌘K hit of
// such a file, or its row on Recent / Starred / a tag view / a search, dragged
// while a plain folder was open, went out (e2e 243 measures it in a browser).
// The rule now reads the row (lib/dragOut mayDragOut / dragOutItems), and the
// hit carries what the server says of it.
//
// FileExplorer.vue is never mounted whole; its wiring is pinned by reading it
// (the pattern of e2ePolicyWiring / sharingPermissionGate).
import { readFileSync } from 'node:fs';
import path from 'node:path';
import { describe, expect, it } from 'vitest';

import { dragItemOf, dragOutItems, mayDragOut, mayMintDragLink } from '../../../packages/core/src/lib/dragOut';
import { hitItem } from '../../../packages/core/src/lib/searchHit';
import type { GlobalSearchHit } from '../../../packages/core/src/composables/useFileApi';

const ctx = { configured: ['docs'], current: 'docs' };

/** A hit as `/api/files/search` answers it for a file inside an encrypted
 *  folder (handlers/search.go: `e2e_root`, `encrypted`). */
const inFolder: GlobalSearchHit = {
  id: 41,
  storage_id: 1,
  storage: 'docs',
  name: 'bütçe-2027.xlsx',
  path: 'Gizli/bütçe-2027.xlsx',
  type: 'file',
  e2e_root: 'docs://Gizli',
  encrypted: 'folder',
};
const inVault: GlobalSearchHit = {
  id: 42,
  storage_id: 1,
  storage: 'docs',
  name: 'p0001.pack',
  path: 'Kasa/v/p0001.pack',
  type: 'file',
  e2e_root: 'docs://Kasa',
  encrypted: 'vault',
};
const plain: GlobalSearchHit = { id: 43, storage_id: 1, storage: 'docs', name: 'rapor.pdf', path: 'Raporlar/rapor.pdf', type: 'file' };

describe('a ⌘K hit says what the server says of its encryption', () => {
  it('a file of an encrypted folder keeps its e2e_root and encrypted', () => {
    expect(hitItem(inFolder, ctx)).toEqual({
      path: 'docs://Gizli/bütçe-2027.xlsx',
      basename: 'bütçe-2027.xlsx',
      type: 'file',
      e2e_root: 'docs://Gizli',
      encrypted: 'folder',
    });
  });

  it('a plain file carries nothing more than path, name and kind', () => {
    expect(hitItem(plain, ctx)).toEqual({ path: 'docs://Raporlar/rapor.pdf', basename: 'rapor.pdf', type: 'file' });
  });

  it('so the drag rule refuses the encrypted hit and lets the plain one go', () => {
    expect(mayMintDragLink(hitItem(inFolder, ctx))).toBe(false);
    expect(mayMintDragLink(hitItem(inVault, ctx))).toBe(false);
    expect(mayMintDragLink(hitItem(plain, ctx))).toBe(true);
    expect(dragOutItems([hitItem(inFolder, ctx)!])).toBeNull();
    expect(dragOutItems([hitItem(inVault, ctx)!])).toBeNull();
    expect(dragOutItems([hitItem(plain, ctx)!])).toEqual([{ path: 'docs://Raporlar/rapor.pdf', basename: 'rapor.pdf', type: 'file' }]);
  });
});

describe('mayDragOut: the row says whether it may leave the window', () => {
  it('a plain file or folder may', () => {
    expect(mayDragOut({ type: 'file', basename: 'a.txt' })).toBe(true);
    expect(mayDragOut({ type: 'dir', basename: 'Belgeler' })).toBe(true);
  });

  it('a file of an encrypted folder may not, from the server’s stamp or the name view’s e2e_root', () => {
    expect(mayDragOut({ type: 'file', basename: 'a.txt', encrypted: 'folder' })).toBe(false);
    expect(mayDragOut({ type: 'file', basename: 'a.txt', e2e_root: 'docs://Gizli' })).toBe(false);
  });

  it('a vault’s row may not - its path is the plaintext name', () => {
    const row = {
      path: 'docs://Kasa/Bütçe planı.vtxt',
      type: 'file',
      basename: 'Bütçe planı.vtxt',
      e2e_root: 'docs://Kasa',
      vault_root: 'docs://Kasa',
      encrypted: 'vault',
    };
    expect(mayDragOut(row)).toBe(false);
    // a vault's folder row carries no `encrypted`, and is refused all the same
    expect(mayDragOut({ type: 'dir', basename: 'Faturalar', vault_root: 'docs://Kasa' })).toBe(false);
  });

  it('an encrypted folder itself may not (all of its files would go out with it)', () => {
    expect(mayDragOut({ type: 'dir', basename: 'Gizli', e2e: true })).toBe(false);
  });

  it('a single encrypted file (.fxe) is the file itself, and may', () => {
    expect(mayDragOut({ type: 'file', basename: 'rapor.pdf.fxe', encrypted: 'file' })).toBe(true);
  });

  it('the trash and nothing may not', () => {
    expect(mayDragOut({ type: 'dir', basename: '.trash' })).toBe(false);
    expect(mayDragOut(null)).toBe(false);
    expect(mayDragOut(undefined)).toBe(false);
  });
});

describe('dragOutItems: what the drag hands the shell or the browser', () => {
  const a = { path: 'docs://a.txt', basename: 'a.txt', type: 'file' as const };
  const b = { path: 'docs://Belgeler', basename: 'Belgeler', type: 'dir' as const };
  // A row on Recent: the server stamps it (handlers/e2e_rows.go stampRow).
  const recent = { path: 'docs://Gizli/b.txt', basename: 'b.txt', type: 'file' as const, e2e_root: 'docs://Gizli', encrypted: 'folder' };

  it('plain rows go out as plain items - nothing else reaches the host’s hook', () => {
    expect(dragOutItems([a, b])).toEqual([a, b]);
    expect(dragOutItems([{ ...a, e2e_root: '', encrypted: null }])).toEqual([a]);
  });

  it('all or nothing: one row that may not leave keeps the whole selection in', () => {
    expect(dragOutItems([a, recent])).toBeNull();
  });

  it('nothing goes out of a pane whose folder is encrypted, whatever its rows say', () => {
    // a subfolder's row in an encrypted folder's listing says nothing itself
    expect(dragOutItems([b], { inEncryptedFolder: true })).toBeNull();
    expect(dragOutItems([a], { inEncryptedFolder: false })).toEqual([a]);
  });

  it('an empty drag hands out nothing', () => {
    expect(dragOutItems([])).toBeNull();
  });

  it('dragItemOf keeps path, name and kind only', () => {
    expect(dragItemOf(recent)).toEqual({ path: 'docs://Gizli/b.txt', basename: 'b.txt', type: 'file' });
  });
});

const explorer = readFileSync(path.resolve(__dirname, '../../../packages/core/src/FileExplorer.vue'), 'utf8');

/** One top-level function of the explorer's script, whole. ⚠ Ends at a line
 *  that is exactly `}`: a multi-line parameter list closes with `): void {`. */
function fn(name: string): string {
  const body = explorer.match(new RegExp(`(?:async )?function ${name}\\([\\s\\S]*?\\n\\}\\n`))?.[0] ?? '';
  expect(body, `${name} was not found`).not.toBe('');
  return body;
}

describe('the explorer reads the ROW, on every path out', () => {
  it('handDragOut decides from the rows (and the pane), before the shell and before DownloadURL', () => {
    const body = fn('handDragOut');
    const guard = body.indexOf('const items = dragOutItems(rows, { inEncryptedFolder: paneEncrypted });');
    expect(guard, 'the rule').toBeGreaterThan(-1);
    expect(body).toMatch(/const items = dragOutItems\(rows, \{ inEncryptedFolder: paneEncrypted \}\);\s*if \(!items\) return;/);
    expect(body.indexOf('dragOut.value.start(items)')).toBeGreaterThan(guard);
    expect(body.indexOf("setData('DownloadURL'")).toBeGreaterThan(guard);
    // the open folder alone is not the rule any more
    expect(body).not.toMatch(/if \(e2eActive\.value/);
  });

  it('a listing row hands its rows (with their stamps) and its own pane’s folder', () => {
    const body = fn('onPaneItemDragStart');
    expect(body).toMatch(/handDragOut\(ev, rows, dirWire, node\.mime_type, paneInEncrypted\(pane\)\)/);
    // no stripping to {path, basename, type} before the rule has read them
    expect(body).not.toMatch(/\.map\(\(n\) => \(\{ path: n\.path, basename: n\.basename, type: n\.type \}\)\)/);
  });

  it('a ⌘K hit is refused before the host’s OS drag and before handDragOut, and is not draggable', () => {
    const drag = fn('onPaletteDragHit');
    const guard = drag.indexOf('!mayDragOut(item)');
    expect(guard).toBeGreaterThan(-1);
    expect(drag.indexOf('hook.dragStart(')).toBeGreaterThan(guard);
    expect(drag.indexOf('handDragOut(')).toBeGreaterThan(guard);
    expect(fn('paletteHitCan')).toMatch(/if \(action === 'drag' && !mayDragOut\(item\)\) return false;/);
    expect(fn('onPaletteWarmHit')).toMatch(/if \(item && mayMintDragLink\(item\)\) warmDragLink\(item\.path\);/);
  });

  it('the shell is not asked to fetch ahead what the drag would not hand out', () => {
    expect(explorer).toMatch(/const items = dragOutItems\(nodes, \{ inEncryptedFolder: e2eActive\.value \}\);\s*if \(!items\) return;/);
  });
});
