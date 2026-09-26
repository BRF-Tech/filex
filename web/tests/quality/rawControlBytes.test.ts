// No raw control character in the source — a NUL above all.
//
// ⚠ lib/filePreview.ts built its cache key with a literal NUL byte between the
// path and the version. It ran fine, but git decides a file with a NUL in it
// is BINARY: `git diff` said "Binary files differ" and every later change to
// the file went through review unseen (found 2026-09-26, beside the same
// byte in lib/dragOut.ts — lesson #131 already said not to write one). A
// separator that must be unprintable is written as an expression
// (`String.fromCharCode(0)`) or an escape, never as the byte itself.
import { readdirSync, readFileSync, statSync } from 'node:fs';
import path from 'node:path';
import { describe, expect, it } from 'vitest';

import { rawControlBytes } from '../helpers/controlBytes';

const REPO = path.resolve(__dirname, '../../..');
const ROOTS = ['packages/core/src', 'web/src'];
const EXT = /\.(ts|vue|css|json|mjs|js)$/;

function walk(dir: string, out: string[] = []): string[] {
  for (const name of readdirSync(dir)) {
    const p = path.join(dir, name);
    if (statSync(p).isDirectory()) walk(p, out);
    else if (EXT.test(name)) out.push(p);
  }
  return out;
}

const files = ROOTS.flatMap((r) => walk(path.join(REPO, r))).map((p) => path.relative(REPO, p).split(path.sep).join('/'));

describe('source files', () => {
  it('were found in both trees', () => {
    expect(files.filter((f) => f.startsWith('packages/core/src/')).length).toBeGreaterThan(100);
    expect(files.filter((f) => f.startsWith('web/src/')).length).toBeGreaterThan(50);
  });

  it('carry no raw control character (git would call them binary)', () => {
    const found = files
      .flatMap((f) => rawControlBytes(readFileSync(path.join(REPO, f))).map((b) => `${f}:${b.line} (0x${b.byte.toString(16)})`));
    expect(found).toEqual([]);
  });

  it('lib/filePreview.ts builds its key without the byte and still separates path and version', () => {
    const src = readFileSync(path.join(REPO, 'packages/core/src/lib/filePreview.ts'));
    expect(rawControlBytes(src)).toEqual([]);
    // The same key at run time: path, NUL, version.
    const text = src.toString('utf8');
    expect(text).toMatch(/const KEY_SEP = String\.fromCharCode\(0\);/);
    expect(text).toMatch(/return `\$\{node\.path\}\$\{KEY_SEP\}\$\{version\}`;/);
  });

});
