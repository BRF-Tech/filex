import { test } from 'node:test';
import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { mkdtempSync, readdirSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

// The desktop windows keep their code inline in ui/*.html. A syntax error there
// is not an exception the app can catch or log: the browser drops the whole
// <script>, and the window stays an empty frame while sync keeps running
// behind it. v0.43.2 shipped exactly that — a comment inside the Settings
// template literal in app.html quoted a function name in backticks, the first
// backtick closed the template, and every desktop opened to a blank window
// ("Unexpected identifier 'syncOverlays'"). Nothing in the build parses these
// scripts, so this test does.

const UI = path.join(path.dirname(fileURLToPath(import.meta.url)), '..', 'ui');

type InlineScript = { module: boolean; code: string; line: number };

/** The pages, not the `._*` metadata files a macOS archive leaves beside them. */
function pages(): string[] {
  return readdirSync(UI).filter((f) => f.endsWith('.html') && !f.startsWith('.'));
}

function inlineScripts(html: string): InlineScript[] {
  const out: InlineScript[] = [];
  const re = /<script(\s[^>]*)?>([\s\S]*?)<\/script>/g;
  for (let m = re.exec(html); m; m = re.exec(html)) {
    const attrs = m[1] ?? '';
    if (/\bsrc\s*=/.test(attrs)) continue;
    const line = html.slice(0, m.index + m[0].indexOf('>') + 1).split('\n').length;
    out.push({ module: /\btype\s*=\s*["']?module\b/.test(attrs), code: m[2], line });
  }
  return out;
}

test('the desktop pages carry inline scripts to check', () => {
  const found = pages();
  assert.ok(found.includes('app.html') && found.includes('index.html'), `ui/ holds ${found.join(', ')}`);
  for (const page of found) {
    assert.ok(inlineScripts(readFileSync(path.join(UI, page), 'utf8')).length > 0, `${page} has no inline script`);
  }
});

test('every inline script in the desktop pages parses', () => {
  const dir = mkdtempSync(path.join(tmpdir(), 'filex-ui-scripts-'));
  const failures: string[] = [];
  try {
    for (const page of pages()) {
      inlineScripts(readFileSync(path.join(UI, page), 'utf8')).forEach((s, i) => {
        // Padded with the lines above it, so the error names the line in the page.
        const file = path.join(dir, `${page}.${i}.${s.module ? 'mjs' : 'cjs'}`);
        writeFileSync(file, '\n'.repeat(s.line - 1) + s.code);
        const r = spawnSync(process.execPath, ['--check', file], { encoding: 'utf8' });
        if (r.status !== 0) failures.push(`ui/${page}, <script> on line ${s.line}:\n${r.stderr.trim()}`);
      });
    }
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
  assert.deepEqual(failures, [], failures.join('\n\n'));
});
