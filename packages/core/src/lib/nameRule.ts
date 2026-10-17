/**
 * nameRule — which names the explorer lets a file or a folder be GIVEN, and
 * how a name is SHOWN (task #238).
 *
 * ⚠⚠ The server is the rule: backend/internal/leafname (Check and Visible).
 * This file says the same thing before the click, in the reader's language;
 * the server still refuses. web/tests/lib/nameRule.test.ts walks the shared
 * hostile names (backend/internal/testutil/hostilenames/names.json, read
 * through e2e/helpers/hostile-names.mjs) and fails when the two disagree.
 *
 * GIVING A NAME. Refused, and nothing else:
 *   - the empty name;
 *   - `/` or `\` (a name is one path segment; a backslash separates to Windows);
 *   - NUL;
 *   - any other control character (U+0001-U+001F, U+007F): a tab, a line
 *     break, an escape - nobody sees one, Windows refuses them, an FTP or an
 *     XML listing cannot carry them (the owner's decision for 0.57). A file
 *     that already has one stays readable: only a NEW name is asked;
 *   - `.`, `..`, and a name of nothing but dots and spaces that holds `..`
 *     (`...`, `.. `) — Windows trims trailing dots and spaces, so on a Windows
 *     host they ARE the folder or its parent, and the server's read side
 *     refuses every path through one.
 * Every other name is a name: any script, either Unicode normalisation form,
 * emoji, URL and shell characters, Windows device names, bidi controls.
 *
 * SHOWING A NAME. A bidi control in a name rearranges how it is DRAWN:
 * `invoice` + RIGHT-TO-LEFT OVERRIDE + `fdp.exe` is an .exe that renders as
 * `invoiceexe.pdf`, and `<bdi>` does not help — the override acts inside the
 * isolate. `visibleName` replaces every bidi control with U+FFFD for display,
 * so the true extension stays at the end where the reader looks for it. The
 * stored name is never changed: a link, a path, a search and an API call use
 * the real one.
 *
 * ⚠ No `\u` escape is written in this file (lesson #332: an editing tool turns
 * one into the invisible character itself). Every code point is a number.
 */

/** Why a name cannot be given, or null — leafname's reasons. */
export type NameRefusal = 'empty' | 'separator' | 'nul' | 'control' | 'dots';

/**
 * Nothing but dots and spaces, with at least one dot: `.`, `..`, `...`,
 * `. .`, ` . . . ` (leafname.OnlyDots). Windows trims trailing dots and
 * spaces, so each is the folder itself or its parent there (the owner's
 * decision for 0.57, task #238).
 */
export function onlyDots(name: string): boolean {
  return name.includes('.') && name.replace(/[. ]/g, '') === '';
}

/** One or more spaces and nothing else (leafname.OnlySpaces): empty on Windows. */
export function onlySpaces(name: string): boolean {
  return name !== '' && name.replace(/ /g, '') === '';
}

const NUL = String.fromCodePoint(0);

/**
 * A C0 control character or DEL (leafname.IsControl): U+0001-U+001F and
 * U+007F, as a character class built from its code points.
 */
const ANY_CONTROL = new RegExp(
  '[' + String.fromCodePoint(0x01) + '-' + String.fromCodePoint(0x1f) + String.fromCodePoint(0x7f) + ']',
);

/**
 * The trim the server applies to the name of a new folder and of a rename:
 * Go's strings.TrimSpace, i.e. Unicode White_Space at either end. Not
 * `String.prototype.trim`, which also trims U+FEFF and keeps U+0085 - the two
 * would disagree about which name was made.
 */
export function trimName(name: string): string {
  return name.replace(/^\p{White_Space}+|\p{White_Space}+$/gu, '');
}

/**
 * Why `name` may not be given to a file or a folder, or null when it may.
 * Asked of the name as it will be sent: a dialog that trims first trims
 * first, as the server does for rename and new folder.
 */
export function nameRefusal(name: string): NameRefusal | null {
  if (name === '' || onlySpaces(name)) return 'empty';
  if (name.includes('/') || name.includes('\\')) return 'separator';
  if (name.includes(NUL)) return 'nul';
  if (ANY_CONTROL.test(name)) return 'control';
  if (onlyDots(name)) return 'dots';
  return null;
}

/**
 * Unicode's Bidi_Control: ARABIC LETTER MARK, LEFT-TO-RIGHT MARK,
 * RIGHT-TO-LEFT MARK, the five embeddings and overrides (LRE RLE PDF LRO RLO)
 * and the four isolates (LRI RLI FSI PDI). The server's twin is
 * unicode.Bidi_Control in leafname.IsBidiControl.
 */
const BIDI_CONTROLS: ReadonlySet<number> = new Set([
  0x061c, 0x200e, 0x200f, 0x202a, 0x202b, 0x202c, 0x202d, 0x202e, 0x2066, 0x2067, 0x2068, 0x2069,
]);

/** The marker a hidden control is shown as. */
export const REPLACEMENT_CHARACTER = String.fromCodePoint(0xfffd);

/** Is `cp` a bidi control (Bidi_Control)? */
export function isBidiControl(cp: number): boolean {
  return BIDI_CONTROLS.has(cp);
}

/** A cheap test before the per-character walk: most names hold none. */
const ANY_BIDI = new RegExp(
  '[' + [...BIDI_CONTROLS].map((cp) => String.fromCodePoint(cp)).join('') + ']',
  'u',
);

/**
 * `name` as the interface SHOWS it: every bidi control replaced by U+FFFD.
 * For display only — never for a path, a link or a request.
 */
export function visibleName(name: string): string {
  if (!name || !ANY_BIDI.test(name)) return name;
  let out = '';
  for (const ch of name) {
    out += isBidiControl(ch.codePointAt(0) ?? 0) ? REPLACEMENT_CHARACTER : ch;
  }
  return out;
}

const LRI = 0x2066;
const RLI = 0x2067;
const FSI = 0x2068;
const PDI = 0x2069;

/** The embeddings and overrides: LRE RLE PDF LRO RLO. */
function isEmbeddingOrOverride(cp: number): boolean {
  return cp >= 0x202a && cp <= 0x202e;
}

/**
 * A value put into a sentence (`t()`), kept from rearranging the sentence
 * around it: every embedding and override, and every isolate that is not
 * closed inside the value (or a PDI that closes nothing), becomes U+FFFD.
 *
 * ⚠ Not `visibleName`. A value can be a sentence filex made itself — a size
 * or a count `t()` already rendered, which in a right-to-left language
 * carries its own BALANCED isolates (lib/direction) — or a date Intl wrote
 * with its own direction MARKS. Those stay: an isolate closed inside the
 * value cannot reach past it, and a mark cannot reorder letters. What a
 * file name smuggles in — an override, an isolate left open — is the part
 * that would turn `Renamed invoice<RLO>fdp.exe to x` around.
 */
export function containBidi(text: string): string {
  if (!text || !ANY_BIDI.test(text)) return text;
  const chars = [...text];
  const open: number[] = [];
  for (let i = 0; i < chars.length; i++) {
    const cp = chars[i].codePointAt(0) ?? 0;
    if (isEmbeddingOrOverride(cp)) {
      chars[i] = REPLACEMENT_CHARACTER;
    } else if (cp === LRI || cp === RLI || cp === FSI) {
      open.push(i);
    } else if (cp === PDI) {
      if (open.length) open.pop();
      else chars[i] = REPLACEMENT_CHARACTER;
    }
  }
  for (const i of open) chars[i] = REPLACEMENT_CHARACTER;
  return chars.join('');
}
