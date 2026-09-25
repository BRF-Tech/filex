// A stand-in for scripts/export-public.sh in the throwaway repository of
// web/tests/deploy/releaseCli.test.ts: the same contract, a smaller body.
//
//   - refuses a target with uncommitted changes;
//   - replaces every tracked file of the target with the source's, except
//     `.github/` (the public checkout's own) and `private/` (withheld);
//   - rewrites the project's host to example.com, keeping the contact address;
//   - stages the result and stops.
//
// The real script is private (it names what it withholds), so the public
// tree — where this test also runs — has no copy to call.

import { execFileSync } from 'node:child_process';
import fs from 'node:fs';
import path from 'node:path';
import process from 'node:process';
import { fileURLToPath } from 'node:url';

const src = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const exp = process.argv[2];
if (!exp) process.exit(2);
const git = (dir, ...args) => execFileSync('git', ['-C', dir, ...args], { encoding: 'utf8' });
const NUL = String.fromCharCode(0);
// Built from parts, so the real export's own rewrite leaves this file alone.
const HOST = ['brf', 'sh'].join('.');
const KEEP = [`security@${HOST}`, `hello@${HOST}`];

if (git(exp, 'status', '--porcelain').trim()) {
  console.error('target checkout has uncommitted changes; commit or discard them first');
  process.exit(1);
}
for (const f of git(exp, 'ls-files', '-z').split(NUL).filter(Boolean)) {
  if (!f.startsWith('.github/')) fs.rmSync(path.join(exp, f), { force: true });
}
let n = 0;
for (const f of git(src, 'ls-files', '-z').split(NUL).filter(Boolean)) {
  if (f.startsWith('private/')) continue;
  let text = fs.readFileSync(path.join(src, f), 'utf8');
  KEEP.forEach((k, i) => (text = text.split(k).join(`@@KEEP${i}@@`)));
  text = text.split(HOST).join('example.com');
  KEEP.forEach((k, i) => (text = text.split(`@@KEEP${i}@@`).join(k)));
  const out = path.join(exp, f);
  fs.mkdirSync(path.dirname(out), { recursive: true });
  fs.writeFileSync(out, text);
  n++;
}
git(exp, 'add', '-A');
console.log(`fixture export: ${n} files`);
