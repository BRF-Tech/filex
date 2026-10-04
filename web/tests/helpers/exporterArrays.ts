// The bash arrays of scripts/export-public.sh (`private_files`, `private_dirs`,
// `private_names`, ...), read as text without running the script. Shared by the
// tests that hold a tree to the exporter's own lists (deploy/shopWindow.test.ts,
// deploy/exportScan.test.ts): the lists are written down in the script alone,
// which is never published, so no test repeats them.
//
// ⚠ Both spellings are in use: `private_files` is one entry per line with
// comments between them (whose prose contains brackets), `private_dirs` is a
// single line. Closing on "the first `)`" mangles the first, and closing on
// "a `)` in column 0" runs the second one past the end of the array and into
// the rest of the file - which is how this parser first read 63 private
// directories and still looked like it worked.
//
// The array has to open at the start of a line, as bash writes it and as the
// export's pre-push gate (scripts/hooks/export-pre-push.sh) reads it: a comment
// that mentions `name=(` is not the array.

/** The elements of the bash array `name` in `script` (the text of `file`). */
export function bashArray(script: string, name: string, file = 'scripts/export-public.sh'): string[] {
  const open = `${name}=(`;
  const at = `\n${script}`.indexOf(`\n${open}`);
  if (at < 0) throw new Error(`${file} has no ${name}=( … ) array`);
  const after = script.slice(at + open.length);
  const nl = after.indexOf('\n');
  const paren = after.indexOf(')');
  const body =
    paren >= 0 && (nl < 0 || paren < nl) ? after.slice(0, paren) : after.match(/^([\s\S]*?)\n\)/)![1]!;
  return body
    .split('\n')
    .map((l) => l.replace(/#.*$/, '').trim())
    .filter(Boolean)
    .flatMap((l) => l.split(/\s+/));
}
