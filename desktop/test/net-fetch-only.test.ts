import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readdirSync, readFileSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

// Every request the desktop main process makes goes through Electron's
// `net.fetch` (Chromium's network stack: the system certificate store and
// proxy). Node's global `fetch` ignores both, so a server behind a private CA
// that the browser trusts answered the sign-in exchange with a bare "fetch
// failed" and the one-time code was lost (issue #36, v0.43.0).

const SRC = path.join(path.dirname(fileURLToPath(import.meta.url)), '..', 'src');

function sources(dir: string): string[] {
  return readdirSync(dir, { withFileTypes: true }).flatMap((e) => {
    const p = path.join(dir, e.name);
    if (e.isDirectory()) return sources(p);
    return /\.(ts|mts|js|mjs)$/.test(e.name) && !/\.test\./.test(e.name) ? [p] : [];
  });
}

// A call to `fetch(` that is not a property access (`net.fetch(`, `x.fetch(`)
// and not a declaration of something named fetch.
const BARE_FETCH = /(^|[^.\w$])fetch\s*\(/;

function stripComments(code: string): string {
  // Block comments keep their newlines so the reported line numbers stay true.
  return code
    .replace(/\/\*[\s\S]*?\*\//g, (m) => m.replace(/[^\n]/g, ''))
    .replace(/(^|[^:])\/\/.*$/gm, '$1');
}

test('the desktop main process never calls the global fetch', () => {
  const offenders: string[] = [];
  for (const file of sources(SRC)) {
    stripComments(readFileSync(file, 'utf8'))
      .split('\n')
      .forEach((line, i) => {
        if (BARE_FETCH.test(line) && !/function\s+fetch|async\s+fetch/.test(line)) {
          offenders.push(`${path.relative(SRC, file)}:${i + 1}: ${line.trim()}`);
        }
      });
  }
  assert.deepEqual(offenders, [], `use net.fetch from 'electron' instead:\n${offenders.join('\n')}`);
});
