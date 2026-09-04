// The RED PROOF for "Open with filex".
//
// Runs every case in test/openwith-cases.ts against the naive implementation in
// test/openwith-naive.ts — the version anyone would write first — and fails if
// a single one of them PASSES there.
//
// ⚠ That inversion is the point. A green suite proves nothing on its own: a
// case the first draft already satisfies is a case that is measuring nothing
// while looking like it measures something, and this repo has been bitten by
// exactly that (a boundary test that stayed green against the broken code
// because its HTTP fake ignored the parameters it was supposed to be
// exercising). Every case here has to be shown red before it is worth anything
// green.
//
// Run:  node --experimental-strip-types desktop/scripts/openwith-red.mjs

import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';

const { CASES } = await import('../test/openwith-cases.ts');
const { NAIVE } = await import('../test/openwith-naive.ts');

let unexpectedlyGreen = 0;
console.log(`Running ${CASES.length} cases against the NAIVE implementation.`);
console.log('Every one of them must FAIL — a case the first draft passes measures nothing.\n');

for (const c of CASES) {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'filex-openwith-red-'));
  let failure = null;
  try {
    await c.run(NAIVE, dir);
  } catch (err) {
    failure = String(err?.message ?? err).split('\n')[0];
  } finally {
    fs.rmSync(dir, { recursive: true, force: true });
  }
  if (failure) {
    console.log(`RED   ${c.group}: ${c.name}\n        ↳ ${failure}`);
  } else {
    unexpectedlyGreen++;
    console.log(`GREEN ${c.group}: ${c.name}\n        ↳ ⚠ passes without the fix — this case proves nothing`);
  }
}

console.log(
  `\n==== ${unexpectedlyGreen === 0
    ? `all ${CASES.length} cases are red against the naive implementation`
    : `${unexpectedlyGreen} case(s) passed without the fix`} ====`,
);
process.exit(unexpectedlyGreen === 0 ? 0 : 1);
