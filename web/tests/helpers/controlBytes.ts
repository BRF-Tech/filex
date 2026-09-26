/**
 * Raw control characters in a source file's bytes — anything under 0x20 but
 * tab, line feed and carriage return. A NUL makes git treat the file as
 * binary and hide its diffs (lesson #131); the others are invisible in an
 * editor and survive review the same way.
 */
export function rawControlBytes(bytes: Uint8Array): Array<{ line: number; byte: number }> {
  const out: Array<{ line: number; byte: number }> = [];
  let line = 1;
  for (const b of bytes) {
    if (b === 10) line++;
    else if (b < 32 && b !== 9 && b !== 13) out.push({ line, byte: b });
  }
  return out;
}
