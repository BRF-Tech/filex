/**
 * The New document dialog's name (#56).
 *
 * The field holds the WHOLE file name. The chosen type decides the bytes the
 * server writes and the editor the file opens in; it only PREFILLS the name
 * (`Untitled.txt`), and the person may change the extension or remove it —
 * `LICENSE`, `Makefile`, `test.conf` and `example.custom` are all Plain text.
 *
 * ⚠ Except where the editor needs it. An office document or a diagram is a
 * container its editor finds by extension (OnlyOffice picks Word/Excel/
 * PowerPoint from it; "report" with no extension is a ZIP nothing opens), so
 * those types keep theirs: the server appends it when it is missing and the
 * dialog says so before Create, rather than locking the field. Which types
 * those are is the server's answer (`ext_required`), not a list kept here.
 *
 * Pure functions, so the rules can be read and tested without a DOM
 * (tests/lib/newDocName.test.ts); NewDocumentModal only wires them.
 */
import type { NewDocType } from '../types/FileNode';
import { isInternalName } from './internalPaths';

/**
 * Must a file of this type carry its extension?
 *
 * ⚠ `undefined` counts as yes. A server from before #56 publishes no
 * `ext_required` and appends the extension to EVERY type, so a dialog that
 * promised it `LICENSE` would hand back `LICENSE.txt`. Treating that server's
 * types as locked makes the dialog tell the truth ("will be created as
 * LICENSE.txt") instead.
 */
export function extLocked(ty: Pick<NewDocType, 'ext_required'>): boolean {
  return ty.ext_required !== false;
}

/** The extension of a name the way the server reads it (Go's `path.Ext`):
 *  after the last dot, a leading dot included — `.docx` is an extension. */
function extOf(name: string): string {
  const dot = name.lastIndexOf('.');
  return dot >= 0 ? name.slice(dot + 1).toLowerCase() : '';
}

function endsWithExt(name: string, ext: string): boolean {
  return name.toLowerCase().endsWith('.' + ext.toLowerCase());
}

/**
 * Where the stem ends — the selection a rename makes, so typing replaces the
 * name and keeps the extension. A dot at position 0 starts the NAME of a
 * dotfile (`.gitignore`), not an extension, so it selects everything.
 */
export function stemEnd(name: string): number {
  const dot = name.lastIndexOf('.');
  return dot > 0 ? dot : name.length;
}

/** The first `<base>.<ext>` / `<base> (n).<ext>` not in `taken` (lowercased
 *  basenames of the destination). */
export function suggestDocName(base: string, ext: string, taken: Set<string>): string {
  const first = `${base}.${ext}`;
  if (!taken.has(first.toLowerCase())) return first;
  for (let i = 2; i < 100; i++) {
    const cand = `${base} (${i}).${ext}`;
    if (!taken.has(cand.toLowerCase())) return cand;
  }
  return first;
}

/**
 * The name, carried across a change of type.
 *
 * The extension is swapped only while it is still the PREVIOUS type's default
 * — `notes.txt` → `notes.md` — because that is the part the dialog put there.
 * A name with no extension or one the person chose (`LICENSE`, `test.conf`)
 * is theirs and stays. Switching to a type whose editor needs its extension
 * adds it, since the server would.
 */
export function retypeDocName(name: string, prev: NewDocType | null, next: NewDocType): string {
  if (!name.trim()) return name;
  if (prev && endsWithExt(name, prev.ext) && name.length > prev.ext.length + 1) {
    return name.slice(0, name.length - prev.ext.length - 1) + '.' + next.ext;
  }
  if (extLocked(next) && !endsWithExt(name, next.ext)) return `${name}.${next.ext}`;
  return name;
}

/** The name the server will write — what the collision check and the
 *  "will be created as" line quote. */
export function finalDocName(name: string, ty: NewDocType): string {
  const n = name.trim();
  if (!n || !extLocked(ty) || endsWithExt(n, ty.ext)) return n;
  return `${n}.${ty.ext}`;
}

export type DocNameProblem =
  | { kind: 'slash' }
  | { kind: 'dots' }
  | { kind: 'bare_ext' }
  | { kind: 'reserved'; name: string }
  | { kind: 'ext_needs_type'; ext: string };

/**
 * Why this name cannot be created, or null. The same refusals the server
 * makes (manager_newdoc.go resolveNewDocName + the reserved-name gate), said
 * before the click; the server still makes them.
 *
 * `all` is every type the server can create — withheld ones included, because
 * an empty `x.docx` made as Plain text is refused whether or not this install
 * can open Word documents.
 */
export function docNameProblem(name: string, ty: NewDocType, all: NewDocType[]): DocNameProblem | null {
  const n = name.trim();
  if (!n) return null;
  if (/[\\/]/.test(n)) return { kind: 'slash' };
  if (/^\.+$/.test(n)) return { kind: 'dots' };
  const final = finalDocName(n, ty);
  if (final.toLowerCase() === '.' + ty.ext.toLowerCase()) return { kind: 'bare_ext' };
  if (isInternalName(final)) return { kind: 'reserved', name: final };
  if (!extLocked(ty)) {
    const ext = extOf(n);
    if (ext && ext !== ty.ext && all.some((o) => o.ext === ext && extLocked(o))) {
      return { kind: 'ext_needs_type', ext };
    }
  }
  return null;
}
