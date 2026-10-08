// Every package that writes type declarations fails its build on a
// TypeScript diagnostic in them.
//
// ⚠⚠ the build host's nightly, nightly-20261007-230007Z: the core and web-component
// builds printed TS7022/TS7024 for DataTable's `useSlots()` loop while
// vite-plugin-dts wrote their declarations, then exited 0. vite-plugin-dts
// only prints what it finds; the `vue-tsc --noEmit` before it in each build
// script does not see everything the declaration build sees (it was green),
// and the published @brftech/filex-core had typed the table's `$slots` as
// `any` for months (fixed in 4578f6d2). The maintainer, 2026-10-08: a diagnostic there
// fails the build.
//
// So this file holds three things: every `dts({...})` in packages/*/vite.config.ts
// passes `afterDiagnostic: failOnDtsDiagnostics('<its package name>')`; the
// installed vite-plugin-dts still calls `afterDiagnostic` with what it found (an
// upgrade that renamed the hook would turn the option into a silently ignored
// key); and the helper throws on a diagnostic - one shaped like the TS7022 that
// shipped - and lets an empty list through.
//
// Red on the old code: scripts/vite-dts-strict.mjs did not exist (the import
// below fails) and no package's dts() passed afterDiagnostic.

import fs from 'node:fs';
import { createRequire } from 'node:module';
import path from 'node:path';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { dtsDiagnosticsMessage, failOnDtsDiagnostics, isWatchRun } from '../../../scripts/vite-dts-strict.mjs';

const REPO = path.resolve(__dirname, '../../..');
const PACKAGES = path.join(REPO, 'packages');

const configOf = (dir: string) => path.join(PACKAGES, dir, 'vite.config.ts');

/** The packages whose vite.config.ts uses vite-plugin-dts. */
function dtsPackages(): string[] {
  return fs
    .readdirSync(PACKAGES)
    .filter((d) => fs.existsSync(configOf(d)) && /from\s+['"]vite-plugin-dts['"]/.test(fs.readFileSync(configOf(d), 'utf8')))
    .sort();
}

/**
 * The argument of every `dts({ ... })` call, braces balanced. Whole-line `//`
 * comments go first, so a comment that names `dts()` is not a call.
 */
function dtsCalls(src: string): string[] {
  const code = src
    .split(/\r?\n/)
    .filter((l) => !/^\s*\/\//.test(l))
    .join('\n');
  const out: string[] = [];
  for (const m of code.matchAll(/\bdts\(\s*\{/g)) {
    const open = m.index! + m[0].length - 1;
    let depth = 0;
    for (let i = open; i < code.length; i++) {
      if (code[i] === '{') depth++;
      else if (code[i] === '}' && --depth === 0) {
        out.push(code.slice(open, i + 1));
        break;
      }
    }
  }
  return out;
}

const pkgName = (dir: string) => (JSON.parse(fs.readFileSync(path.join(PACKAGES, dir, 'package.json'), 'utf8')) as { name: string }).name;

describe('every package declaration build fails on a TypeScript diagnostic', () => {
  it('the packages that build declarations are found (the scan is live)', () => {
    // Without this the loop below could pass on an empty list.
    expect(dtsPackages()).toEqual(expect.arrayContaining(['app-ui', 'core', 'react', 'webcomponent']));
  });

  for (const dir of ['app-ui', 'core', 'react', 'webcomponent']) {
    it(`packages/${dir}/vite.config.ts passes afterDiagnostic: failOnDtsDiagnostics('<its name>') to dts()`, () => {
      const src = fs.readFileSync(configOf(dir), 'utf8');
      expect(src).toMatch(/import\s*\{\s*failOnDtsDiagnostics\s*\}\s*from\s*['"]\.\.\/\.\.\/scripts\/vite-dts-strict\.mjs['"]/);
      const calls = dtsCalls(src);
      expect(calls.length, `packages/${dir}/vite.config.ts calls dts({ ... })`).toBeGreaterThan(0);
      const name = pkgName(dir).replace(/[/@.-]/g, (c) => `\\${c}`);
      const hook = new RegExp(`\\bafterDiagnostic\\s*:\\s*failOnDtsDiagnostics\\(\\s*['"]${name}['"]\\s*\\)`);
      for (const call of calls) expect(call, `packages/${dir}: a dts() call without the strict hook`).toMatch(hook);
    });
  }

  it('a package added later that builds declarations passes the hook too', () => {
    for (const dir of dtsPackages()) {
      const calls = dtsCalls(fs.readFileSync(configOf(dir), 'utf8'));
      expect(calls.length, `packages/${dir}`).toBeGreaterThan(0);
      for (const call of calls) expect(call, `packages/${dir}`).toMatch(/\bafterDiagnostic\s*:\s*failOnDtsDiagnostics\(/);
    }
  });

  it('the installed vite-plugin-dts still has afterDiagnostic and calls it with the diagnostics', () => {
    const req = createRequire(path.join(PACKAGES, 'core', 'package.json'));
    const entry = req.resolve('vite-plugin-dts');
    const dist = path.dirname(entry);
    const types = fs.readFileSync(path.join(dist, 'index.d.ts'), 'utf8');
    expect(types).toMatch(/afterDiagnostic\?:\s*\(diagnostics:\s*readonly ts\.Diagnostic\[\]\)/);
    for (const f of ['index.mjs', 'index.cjs']) {
      const code = fs.readFileSync(path.join(dist, f), 'utf8');
      expect(code, `vite-plugin-dts dist/${f}`).toMatch(/afterDiagnostic\(diagnostics\)/);
    }
  });
});

/** A stand-in for a ts.SourceFile: a name and a position-to-line map. */
const fileAt = (fileName: string, line: number, col: number) => ({
  fileName,
  getLineAndCharacterOfPosition: () => ({ line: line - 1, character: col - 1 }),
});

/** Shaped like what the core build printed on 2026-10-07. */
const ts7022 = () => ({
  category: 1,
  code: 7022,
  file: fileAt(path.join(process.cwd(), 'src', 'components', 'DataTable.vue'), 567, 7),
  start: 21000,
  length: 5,
  messageText: "'slots' implicitly has type 'any' because it does not have a type annotation and is referenced directly or indirectly in its own initializer.",
});

describe('failOnDtsDiagnostics', () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('lets a declaration build with no diagnostics through', () => {
    expect(() => failOnDtsDiagnostics('@brftech/filex-core', { watch: false })([])).not.toThrow();
  });

  it('throws on one diagnostic, naming the package, the place and the code', () => {
    const hook = failOnDtsDiagnostics('@brftech/filex-core', { watch: false });
    expect(() => hook([ts7022()] as never)).toThrow(/\[vite:dts\] @brftech\/filex-core: 1 TypeScript diagnostic while writing the declarations/);
    expect(() => hook([ts7022()] as never)).toThrow(/src\/components\/DataTable\.vue:567:7 TS7022: 'slots' implicitly has type 'any'/);
  });

  it('reads a message chain and a diagnostic without a file', () => {
    const chained = {
      category: 1,
      code: 2322,
      file: undefined,
      start: undefined,
      length: undefined,
      messageText: {
        messageText: "Type 'string' is not assignable to type 'number'.",
        category: 1,
        code: 2322,
        next: [{ messageText: "The expected type comes from property 'size'.", category: 3, code: 6500 }],
      },
    };
    const text = dtsDiagnosticsMessage('@brftech/filex', [chained] as never);
    expect(text).toContain("TS2322: Type 'string' is not assignable to type 'number'. The expected type comes from property 'size'.");
  });

  it('lists the first twenty and counts the rest', () => {
    const many = Array.from({ length: 25 }, ts7022);
    const text = dtsDiagnosticsMessage('@brftech/filex-core', many as never);
    expect(text).toMatch(/25 TypeScript diagnostics/);
    expect(text.match(/TS7022/g)).toHaveLength(20);
    expect(text).toContain('... and 5 more');
  });

  it('only warns in watch mode, where the plugin would hand the same stale list to every rebuild', () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {});
    expect(() => failOnDtsDiagnostics('@brftech/filex-core', { watch: true })([ts7022()] as never)).not.toThrow();
    expect(warn).toHaveBeenCalledTimes(1);
    expect(String(warn.mock.calls[0][0])).toMatch(/TS7022/);
  });

  it('knows vite build --watch from vite build', () => {
    expect(isWatchRun(['node', 'vite', 'build', '--watch'])).toBe(true);
    expect(isWatchRun(['node', 'vite', 'build', '-w'])).toBe(true);
    expect(isWatchRun(['node', 'vite', 'build'])).toBe(false);
  });
});
