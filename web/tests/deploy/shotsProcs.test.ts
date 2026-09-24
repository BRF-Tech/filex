// The shots run's process sweep must survive the machine it runs on.
//
// ⚠⚠ Measured 2026-09-23: `node scripts/shots.mjs --only sidenav,capture` took
// the capture scene's pictures and then died —
//
//     SyntaxError: Bad control character in string literal in JSON at position 179599
//         at listWindows (scripts/lib/procs.mjs)
//         at sweepRun (scripts/shots.mjs)
//
// — so `sidenav.mjs` never ran (half the pictures of the release silently not
// taken) and the sweep that exists precisely so a run leaves no process behind
// never ran either. The cause is not in this repository at all: PowerShell's
// `ConvertTo-Json` writes some control characters RAW into a string, and one
// unrelated program on that machine had one in its command line.
//
// Nothing here can stop such a program existing, so the parser has to survive
// it. That is `stripRawControlChars`, and this is the test that would have
// turned red before the run did.
import { describe, expect, it } from 'vitest';

// @ts-expect-error — plain .mjs helper, no types
import { stripRawControlChars } from '../../../scripts/lib/procs.mjs';

/** A `-Compress` payload with a raw control character inside a string value. */
function payloadWith(code: number): string {
  return `[{"pid":42,"cmd":"app.exe --flag${String.fromCharCode(code)}value"}]`;
}

describe('the shots process sweep parses what PowerShell actually emits', () => {
  it('a raw control character in a command line no longer kills the parse', () => {
    const raw = payloadWith(0x01);
    expect(() => JSON.parse(raw)).toThrow(); // the failure this fixes
    const rows = JSON.parse(stripRawControlChars(raw)) as Array<{ cmd: string }>;
    expect(rows[0].cmd).toBe('app.exe --flag value');
  });

  it('holds for every control code below 0x20, not just the one that bit', () => {
    for (let code = 0; code < 0x20; code++) {
      const rows = JSON.parse(stripRawControlChars(payloadWith(code))) as Array<{ cmd: string }>;
      expect(rows[0].cmd, `code ${code}`).toBe('app.exe --flag value');
    }
  });

  it('leaves ESCAPED control characters alone — they are two ordinary characters', () => {
    // `"a\nb"` in the payload is backslash + n, which JSON turns into a newline.
    // Mangling those would corrupt every command line that has one.
    const raw = String.raw`[{"cmd":"a\nb\tc"}]`;
    const rows = JSON.parse(stripRawControlChars(raw)) as Array<{ cmd: string }>;
    expect(rows[0].cmd).toBe('a\nb\tc');
  });

  it('does not touch ordinary text, including non-ASCII', () => {
    const raw = '[{"cmd":"C:\\\\Program Files\\\\filex — kopyası\\\\filex.exe"}]';
    expect(stripRawControlChars(raw)).toBe(raw);
  });
});
