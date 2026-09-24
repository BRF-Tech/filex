/**
 * listInput — the ONE splitter for "several things typed into one field":
 * recipients, extensions, tags, identities, a multi-select's stored answer.
 *
 * ⚠⚠ The comma is not one character. An Arabic, Persian or Urdu keyboard
 * types "،" (U+060C ARABIC COMMA) where an English one types ",", Chinese and
 * Japanese input methods type "，" (U+FF0C FULLWIDTH COMMA) or "、" (U+3001
 * IDEOGRAPHIC COMMA), and the semicolon has its own Arabic (U+061B "؛") and
 * fullwidth (U+FF1B "；") forms. Every list field used to split on the ASCII
 * characters only, so `a@x.com، b@y.com` — exactly what a person writing in
 * Arabic produces — became ONE malformed address and the share mail said
 * "enter a valid email" about a perfectly valid list. Five fields each carried
 * their own `split(/[,…]/)`; they all read through here now, so the next
 * script's comma is added once.
 *
 * Items are trimmed and empty ones dropped; nothing else is decided here
 * (lower-casing, de-duplication, "does it contain an @" stay with the caller,
 * which knows what an item is).
 */

/** , ، 、 ， — the characters people type between list items. */
const COMMAS = ',\u060C\u3001\uFF0C';
/** ; ؛ ； */
const SEMICOLONS = ';\u061B\uFF1B';

export interface SplitListOptions {
  /** Whitespace separates items too (never for tags, which may contain a space). */
  spaces?: boolean;
  /** Semicolons separate items too (a mail client's recipient habit). */
  semicolons?: boolean;
  /** Line breaks separate items too (a pasted column). Implied by `spaces`. */
  newlines?: boolean;
}

const cache = new Map<string, RegExp>();

function separatorRe(o: SplitListOptions): RegExp {
  const key = `${o.spaces ? 1 : 0}${o.semicolons ? 1 : 0}${o.newlines ? 1 : 0}`;
  let re = cache.get(key);
  if (!re) {
    const cls = COMMAS + (o.semicolons ? SEMICOLONS : '') + (o.newlines ? '\\n\\r' : '') + (o.spaces ? '\\s' : '');
    re = new RegExp(`[${cls}]+`, 'u');
    cache.set(key, re);
  }
  return re;
}

/** `"a، b ,c"` → `["a", "b", "c"]`. */
export function splitList(raw: string | null | undefined, opts: SplitListOptions = {}): string[] {
  return String(raw ?? '')
    .split(separatorRe(opts))
    .map((s) => s.trim())
    .filter((s) => s !== '');
}

/**
 * Does this key END a list item in a chip-style field (the typed address or
 * tag becomes a chip)? `,` and each of its siblings above — the same set the
 * splitter uses, so a field that splits a paste and a field that commits on a
 * keypress agree about what a comma is.
 */
export function isListSeparatorKey(key: string): boolean {
  return key.length === 1 && COMMAS.includes(key);
}
