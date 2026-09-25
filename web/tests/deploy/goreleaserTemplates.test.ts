// .goreleaser.yml may only call template functions GoReleaser itself defines.
//
// ⚠⚠ v0.44.0 (release run 36091867656): the winget and Homebrew-tap sections
// guarded their tokens with `{{ envOrDefault "WINGET_TOKEN" "" }}`. The
// GoReleaser the release installs does not define `envOrDefault`, and the
// template is only evaluated when the publishers run — AFTER the GitHub
// Release, the CLI binaries, npm and the images had gone out. The binaries job
// failed on it and the desktop packages and every store step behind it were
// skipped, so 0.44.0 shipped half. `goreleaser check` and a snapshot build both
// passed locally: neither evaluates a publisher's token. This test reads every
// `{{ … }}` in the file and fails on a function that is not on the list below.
// `{{ if index .Env "NAME" }}` is the idiom for "is this secret set": `index`
// answers "" for a missing key instead of failing the run.

import { readFileSync } from 'node:fs';
import path from 'node:path';
import { describe, expect, it } from 'vitest';

const REPO = path.resolve(__dirname, '..', '..', '..');
const config = readFileSync(path.join(REPO, '.goreleaser.yml'), 'utf8');

// Go text/template keywords and builtins, plus the functions GoReleaser (OSS)
// documents. Add to this list only with the GoReleaser version that has it.
const KNOWN = new Set([
  'if', 'else', 'end', 'range', 'with', 'define', 'template', 'block',
  'and', 'or', 'not', 'eq', 'ne', 'lt', 'le', 'gt', 'ge', 'index', 'len',
  'print', 'printf', 'println', 'slice', 'call', 'html', 'js', 'urlquery',
  'incpatch', 'incminor', 'incmajor', 'replace', 'split', 'tolower', 'toupper',
  'trim', 'trimprefix', 'trimsuffix', 'title', 'dir', 'base', 'abs', 'filter',
  'reverseFilter', 'map', 'indexOrDefault', 'time', 'contains', 'list', 'in',
  'reReplaceAll', 'mdv2escape', 'envOrDefault_NOT_DEFINED',
]);

function functionsIn(expr: string): string[] {
  const body = expr.replace(/^\{\{-?|-?\}\}$/g, '').replace(/"(?:[^"\\]|\\.)*"/g, '""');
  // A bare word that is not a field (.Version), not a variable ($x) and not a
  // literal is a function or a keyword.
  return (body.match(/(?<![.$\w])[A-Za-z_][A-Za-z0-9_]*/g) ?? []).filter(
    (w) => !['true', 'false', 'nil'].includes(w),
  );
}

describe('.goreleaser.yml templates', () => {
  const exprs = config.match(/\{\{-?[\s\S]*?-?\}\}/g) ?? [];

  it('has templates to check (the file was read)', () => {
    expect(exprs.length).toBeGreaterThan(10);
  });

  it('calls only functions GoReleaser defines', () => {
    const unknown = [...new Set(exprs.flatMap(functionsIn))].filter((f) => !KNOWN.has(f));
    expect(unknown, `not a GoReleaser template function: ${unknown.join(', ')} — use {{ index .Env "NAME" }} for an optional secret`).toEqual([]);
  });

  it('never uses envOrDefault (it failed the v0.44.0 release after publishing)', () => {
    expect(config).not.toMatch(/envOrDefault/);
  });
});
