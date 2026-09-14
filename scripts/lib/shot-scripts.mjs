// Which files in e2e/shots/ are shot scripts — found, never listed.
//
// ⚠ A hard-coded list is exactly what the next new script is missing from.
// The rule instead: a file that imports @playwright/test (it drives a browser)
// and that no sibling imports is a script; a file some sibling imports is a
// module (fixtures.mjs, release.mjs). A file that is neither is returned as
// `stray` so the caller can refuse it — silently not running it would be the
// same rot this exists to stop.

import fs from 'node:fs';
import path from 'node:path';

export function findShotScripts(dir) {
  const files = fs.readdirSync(dir).filter((f) => f.endsWith('.mjs')).sort();
  const src = new Map(files.map((f) => [f, fs.readFileSync(path.join(dir, f), 'utf8')]));
  const imported = new Set();
  for (const text of src.values()) {
    for (const m of text.matchAll(/(?:from\s+|import\s*\(\s*)['"]\.\/([^'"]+\.mjs)['"]/g)) imported.add(m[1]);
  }
  const scripts = [];
  const modules = [];
  const stray = [];
  for (const f of files) {
    if (imported.has(f)) modules.push(f);
    else if (/from\s+['"](?:@playwright\/test|playwright)['"]/.test(src.get(f))) scripts.push(f);
    else stray.push(f);
  }
  return { scripts, modules, stray };
}
