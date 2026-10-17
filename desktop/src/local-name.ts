/**
 * local-name — what a SERVER name becomes when the app writes a copy of it
 * onto this computer: the drag-out cache, a folder the user dropped onto, the
 * mirror folder of a kept server folder.
 *
 * ⚠⚠ ONE rule. It is the TypeScript side of the Go package
 * backend/internal/localname (localname.Safe), which the sync engine and
 * `filex decrypt` follow; desktop/test/local-name.test.ts holds this file to
 * the hostile names every surface of filex is tested with
 * (backend/internal/testutil/hostilenames/names.json, task #238). It replaced
 * two copies that disagreed: drag-out's `safeName` (which removed control
 * characters, trimmed the leading space Windows allows and let `report.`,
 * `CON` and an RLO through) and main.ts's `fsSegment`.
 *
 * What a name becomes, character by character, then as a whole:
 *   - `\ / : * ? " < > |`, control characters, DEL and the bidi controls
 *     become `_`. A bidi control in a file name is how an extension is hidden
 *     (`invoice` + RIGHT-TO-LEFT OVERRIDE + `fdp.exe` shows as
 *     `invoiceexe.pdf`): the copy on the person's desktop never carries one,
 *     the same rule the server applies to a download's name;
 *   - a name too long for one name (255 UTF-16 units on Windows, 255 bytes
 *     elsewhere), less `reserve` for a `.part` suffix, loses characters from
 *     the end of its stem, never its extension;
 *   - trailing dots and spaces become `_` (Windows drops them, and `report.`
 *     would land on `report`);
 *   - a Windows device name (`CON`, `aux.txt`, `com1.log`) gets `_` in front.
 *   A leading space stays: Windows allows it, and it is part of the name.
 *
 * The rules are applied on every platform for a drag-out (a drop may land on
 * a FAT stick or a Windows share); `fsSegment` in main.ts applies them on
 * Windows only.
 */

/** The most one name may be: 255 bytes of UTF-8 (ext4, APFS) or 255 UTF-16
 *  units (NTFS). */
export const MAX_NAME = 255;

const FORBIDDEN = '\\/:*?"<>|';

const DEVICE_NAMES: ReadonlySet<string> = new Set([
  'CON', 'PRN', 'AUX', 'NUL',
  'COM1', 'COM2', 'COM3', 'COM4', 'COM5', 'COM6', 'COM7', 'COM8', 'COM9',
  'LPT1', 'LPT2', 'LPT3', 'LPT4', 'LPT5', 'LPT6', 'LPT7', 'LPT8', 'LPT9',
]);

/** Unicode's bidi controls: ALM, LRM, RLM, LRE..RLO, LRI..PDI. */
export function isBidiControl(cp: number): boolean {
  return cp === 0x061c || cp === 0x200e || cp === 0x200f || (cp >= 0x202a && cp <= 0x202e) || (cp >= 0x2066 && cp <= 0x2069);
}

function badChar(ch: string): boolean {
  const cp = ch.codePointAt(0) ?? 0;
  return cp < 0x20 || cp === 0x7f || FORBIDDEN.includes(ch) || isBidiControl(cp);
}

/** Is this a Windows device name, with or without an extension? */
export function isDeviceName(name: string): boolean {
  const dot = name.indexOf('.');
  const stem = (dot >= 0 ? name.slice(0, dot) : name).replace(/ +$/, '');
  return DEVICE_NAMES.has(stem.toUpperCase());
}

function nameLength(s: string, platform: string): number {
  return platform === 'win32' ? s.length : Buffer.byteLength(s, 'utf8');
}

/**
 * `name` as a copy of it may be called on this computer (see the file's
 * comment). `reserve` is room kept for a suffix the copy wears while it is
 * written (`.part`, `.filexpart`); `platform` decides how a name's length is
 * counted.
 */
export function safeLocalName(
  name: string,
  opts: { reserve?: number; platform?: string } = {},
): string {
  const platform = opts.platform ?? process.platform;
  const max = MAX_NAME - (opts.reserve ?? 0);
  let s = Array.from(String(name ?? ''), (ch) => (badChar(ch) ? '_' : ch)).join('');
  if (nameLength(s, platform) > max) {
    const dot = s.lastIndexOf('.');
    const ext = dot > 0 && s.length - dot <= 16 ? s.slice(dot) : '';
    const stem = Array.from(ext ? s.slice(0, dot) : s);
    while (stem.length > 0 && nameLength(stem.join('') + ext, platform) > max) stem.pop();
    s = stem.join('') + ext;
  }
  const chars = Array.from(s);
  for (let i = chars.length - 1; i >= 0 && (chars[i] === '.' || chars[i] === ' '); i--) chars[i] = '_';
  s = chars.join('');
  if (isDeviceName(s)) s = '_' + s;
  return s || 'file';
}

/** Two names this computer may hold as one file (NTFS ignores case, APFS
 *  case and the normalisation form): compared through this key. */
export function collisionKey(name: string): string {
  return name.normalize('NFC').toLowerCase();
}

/**
 * `name`, or `name (2)`, `name (3)`... before its extension: the first whose
 * collisionKey is not in `taken`, which then holds it. Two server names that
 * are one name here (`Rapor.docx` and `rapor.docx`, or `a:b.txt` beside
 * `a_b.txt` once made safe) both arrive in a dragged folder instead of one
 * landing on the other.
 */
export function uniqueLocalName(name: string, taken: Set<string>): string {
  let out = name;
  const dot = name.lastIndexOf('.');
  const stem = dot > 0 ? name.slice(0, dot) : name;
  const ext = dot > 0 ? name.slice(dot) : '';
  for (let n = 2; taken.has(collisionKey(out)); n++) out = `${stem} (${n})${ext}`;
  taken.add(collisionKey(out));
  return out;
}

/**
 * The server's pair-relative path for a path here. `names` is the sync
 * engine's record of the items it keeps here under another name
 * (names/<pair-id>.json: server path → path here, e.g. `rapor:2024.txt` →
 * `rapor_2024.txt`, `docs` → `docs (2)`); it is read backwards, through the
 * deepest pairing that is the path or one of its folders - the engine's own
 * rule (backend/internal/filesync nameMap.remoteRel). A path with no pairing
 * is called the same on the server.
 */
export function serverRelFor(localRel: string, names: Readonly<Record<string, string>>): string {
  const inverse = new Map<string, string>();
  for (const [server, here] of Object.entries(names)) inverse.set(here, server);
  if (!inverse.size) return localRel;
  for (let p = localRel; p !== ''; p = p.includes('/') ? p.slice(0, p.lastIndexOf('/')) : '') {
    const to = inverse.get(p);
    if (to !== undefined) return to + localRel.slice(p.length);
  }
  return localRel;
}

/**
 * The path under `root` that holds exactly `segments` - every one a name this
 * computer keeps as it is (safeLocalName on Windows leaves it alone), and,
 * where the file system ignores case or the normalisation form, present in its
 * folder under exactly that spelling - or null.
 *
 * ⚠ The sync engine's mirror is only a copy of a server item when it is that
 * item: `rapor.docx` asked of a folder holding `Rapor.docx` opens the OTHER
 * twin on Windows and macOS, and `a:b.txt`'s Windows-safe `a_b.txt` is some
 * other file (the engine keeps names it cannot hold out of the mirror). Either
 * way the bytes handed over would be another file's.
 */
export function exactLocalPath(
  root: string,
  segments: readonly string[],
  readdir: (dir: string) => readonly string[] | null,
  join: (...parts: string[]) => string,
  platform: string = process.platform,
): string | null {
  let dir = root;
  for (const seg of segments) {
    if (!seg || seg === '.' || seg === '..') return null;
    if (platform === 'win32' && safeLocalName(seg, { platform }) !== seg) return null;
    if (platform === 'win32' || platform === 'darwin') {
      const entries = readdir(dir);
      if (!entries || !entries.includes(seg)) return null;
    }
    dir = join(dir, seg);
  }
  return dir;
}
