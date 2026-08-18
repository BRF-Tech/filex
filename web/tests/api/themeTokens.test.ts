// Every `var(--fe-*)` a core component references must be a token that
// packages/core/src/styles/variables.css actually declares.
//
// A phantom token does not error — it silently resolves to its fallback, or to
// nothing. That is how the API-tokens box on the Connections page shipped with
// no ground (`--fe-surface`), muted text at full strength (`--fe-muted`) and a
// mint button in a hardcoded blue that ignored the dark palette
// (`--fe-accent, #2f6df6`) while the button beside it used --fe-primary.
// Measured on demo.filex.sh in dark mode, 2026-08-18. Five phantom names
// across four files, none of them caught by vue-tsc, vite or the browser.
//
// A fallback value does not excuse it: `var(--fe-accent, #2f6df6)` is a
// hardcoded colour that no theme — dark, the theme gallery, a host override —
// can reach. Use the declared token; if a new one is genuinely needed, declare
// it in variables.css (light AND dark) in the same change.

import { readFileSync, readdirSync, statSync } from 'node:fs';
import path from 'node:path';
import { describe, expect, it } from 'vitest';

const CORE_SRC = path.resolve(__dirname, '../../../packages/core/src');
const VARIABLES = path.join(CORE_SRC, 'styles/variables.css');

function walk(dir: string): string[] {
  return readdirSync(dir).flatMap((name) => {
    const p = path.join(dir, name);
    return statSync(p).isDirectory() ? walk(p) : [p];
  });
}

const declared = new Set(
  Array.from(readFileSync(VARIABLES, 'utf8').matchAll(/^\s*(--fe-[a-z0-9-]+)\s*:/gm)).map((m) => m[1]),
);

describe('theme tokens', () => {
  it('declares a sane token set', () => {
    // A guard for the guard: if the parser above ever breaks, the test below
    // would pass vacuously by declaring nothing.
    expect(declared.size).toBeGreaterThan(20);
    for (const must of ['--fe-bg', '--fe-bg-elev', '--fe-text', '--fe-text-muted', '--fe-primary', '--fe-border']) {
      expect(declared.has(must), `${must} missing from variables.css`).toBe(true);
    }
  });

  it('every var(--fe-*) used in core resolves to a declared token', () => {
    const files = walk(CORE_SRC).filter((f) => /\.(vue|css|ts)$/.test(f));
    const offenders: string[] = [];
    for (const f of files) {
      const src = readFileSync(f, 'utf8');
      const lines = src.split('\n');
      lines.forEach((line, i) => {
        for (const m of line.matchAll(/var\(\s*(--fe-[a-z0-9-]+)/g)) {
          const token = m[1];
          if (!declared.has(token)) {
            offenders.push(`${path.relative(CORE_SRC, f)}:${i + 1} ${token}`);
          }
        }
      });
    }
    expect(
      offenders,
      'undeclared --fe-* token: declare it in styles/variables.css (light + dark) or use an existing one',
    ).toEqual([]);
  });
});
