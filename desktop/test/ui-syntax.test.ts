// Every inline <script> in desktop/ui/*.html must at least PARSE.
//
// Run:  node --experimental-strip-types --test desktop/test/ui-syntax.test.ts
//
// The pages are hand-written HTML with their whole program inline, and much of
// it is HTML built in template literals. A backtick anywhere inside one — even
// inside an HTML comment in the markup — ends the literal early, and the
// browser rejects the ENTIRE module: no explorer, no Settings, a window that
// draws its static markup and does nothing. v0.43.0-v0.43.2 shipped exactly
// that (a comment quoting `syncOverlays()` in the Settings template; measured
// 2026-09-24: "Unexpected identifier 'syncOverlays'"). typecheck does not read
// these files and the unit tests never load them, so nothing caught it.

import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';
import { fileURLToPath } from 'node:url';

const UI = path.join(path.dirname(fileURLToPath(import.meta.url)), '..', 'ui');

for (const page of fs.readdirSync(UI).filter((f) => f.endsWith('.html'))) {
  test(`ui/${page}: every inline script parses`, () => {
    const html = fs.readFileSync(path.join(UI, page), 'utf8');
    const scripts = [...html.matchAll(/<script(\s[^>]*)?>([\s\S]*?)<\/script>/g)]
      .map((m) => m[2])
      .filter((s) => s.trim());
    const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'filex-ui-syntax-'));
    try {
      scripts.forEach((src, i) => {
        // .mjs: the pages' programs are modules (they `import './filex.js'`).
        const file = path.join(dir, `${page}.${i}.mjs`);
        fs.writeFileSync(file, src);
        const r = spawnSync(process.execPath, ['--check', file], { encoding: 'utf8' });
        assert.equal(r.status, 0, `ui/${page} script #${i} does not parse:\n${r.stderr}`);
      });
    } finally {
      fs.rmSync(dir, { recursive: true, force: true });
    }
  });
}
