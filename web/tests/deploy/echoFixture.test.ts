// The `echo` app module the e2e specs install (e2e/helpers/echoFixture.ts).
//
// ⚠⚠ v0.50.0 pretag (lesson #960, #139): the Windows checkout's echo.wasm
// predated the change to the fixture's main.go that spec 192 tested. Every
// echo spec asked only whether the file EXISTED, so 192 installed the old app
// and failed on a button it did not have, after an hour of the release chain,
// with a red that pointed at the product. The Go tests had refused a stale
// module since 2026-09-25 (internal/testutil/wasmfixture); the e2e side had no
// such rule. These hold it: one helper, the Go rule's files, and every spec
// that installs echo going through it.
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { afterEach, describe, expect, it } from 'vitest';

type Helper = typeof import('../../../e2e/helpers/echoFixture');

const REPO = path.resolve(__dirname, '..', '..', '..');
const HELPER = path.join(REPO, 'e2e', 'helpers', 'echoFixture.ts');
// Imported when a test runs, so a tree without the helper fails each test
// with that sentence instead of failing to load the file.
const HELPER_MODULE = '../../../e2e/helpers/echoFixture';

async function helper(): Promise<Helper> {
  expect(fs.existsSync(HELPER), 'no shared echo helper (e2e/helpers/echoFixture.ts): a spec cannot tell a stale echo.wasm from a current one').toBe(true);
  return (await import(/* @vite-ignore */ HELPER_MODULE)) as Helper;
}

const temps: string[] = [];
afterEach(() => {
  for (const d of temps.splice(0)) fs.rmSync(d, { recursive: true, force: true });
});

const T0 = new Date('2026-09-30T12:00:00Z');
const at = (seconds: number) => new Date(T0.getTime() + seconds * 1000);

/** A fixture directory: name → seconds after T0 of its mtime (a directory when the name ends in /). */
function fixtureDir(files: Record<string, number>): string {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'filex-echo-'));
  temps.push(dir);
  for (const [name, s] of Object.entries(files)) {
    const p = path.join(dir, name);
    if (name.endsWith('/')) fs.mkdirSync(p);
    else fs.writeFileSync(p, name === 'manifest.json' ? JSON.stringify({ name: 'echo', version: '0.0.1', permissions: ['state'], languages: ['en', 'tr'] }) : name);
    fs.utimesSync(p, at(s), at(s));
  }
  return dir;
}

/** The echo specs: every spec that names the fixture or its helper. */
function echoSpecs(): { file: string; src: string }[] {
  const dir = path.join(REPO, 'e2e', 'tests');
  return fs
    .readdirSync(dir)
    .filter((f) => f.endsWith('.spec.ts'))
    .map((file) => ({ file, src: fs.readFileSync(path.join(dir, file), 'utf8') }))
    .filter(({ src }) => /testdata\/echo|echo\.wasm|echoFixture/.test(src));
}

/**
 * The source without its comments, and the string literals in it. A small
 * scanner, not a regex: a block comment's lines need not start with `*`
 * (95 names testdata/echo inside one), and a string may hold a `/*`.
 */
function scan(src: string): { code: string; strings: string[] } {
  let code = '';
  const strings: string[] = [];
  for (let i = 0; i < src.length; ) {
    const c = src[i];
    const n = src[i + 1];
    if (c === '/' && (n === '*' || n === '/')) {
      const end = src.indexOf(n === '*' ? '*/' : '\n', i + 2);
      i = end < 0 ? src.length : end + (n === '*' ? 2 : 0);
      continue;
    }
    if (c === "'" || c === '"' || c === '`') {
      let j = i + 1;
      while (j < src.length && src[j] !== c) j += src[j] === '\\' ? 2 : 1;
      strings.push(src.slice(i + 1, j));
      code += src.slice(i, j + 1);
      i = j + 1;
      continue;
    }
    code += c;
    i++;
  }
  return { code, strings };
}

describe('the echo app module the e2e specs install', () => {
  it('every spec that installs it reads it through the helper and its guard, never through a path of its own', () => {
    const specs = echoSpecs();
    // 94, 95, 171, 179 and 192 on 2026-10-03; an empty list proves nothing.
    expect(specs.length).toBeGreaterThanOrEqual(5);
    const own = specs.filter(({ src }) => {
      const { code, strings } = scan(src);
      const throughHelper = /from '\.\.\/helpers\/echoFixture'/.test(code) && /\bechoFixture\(\)/.test(code) && /\bguardFixture\(/.test(code);
      const ownPath = strings.some((s) => /testdata\/echo|echo\.wasm/.test(s));
      return !throughHelper || ownPath;
    });
    expect(
      own.map((s) => s.file),
      'these specs decide on their own whether echo.wasm can be installed (a stale one runs): use echoFixture() and guardFixture()',
    ).toEqual([]);
  });

  it('a module older than a source beside it is a failure that names the source and the command, not a skip', async () => {
    const { echoFixture, REBUILD } = await helper();
    const fx = echoFixture(fixtureDir({ 'manifest.json': -60, 'main.go': 60, 'echo.wasm': 0 }));
    expect(fx.present).toBe(true);
    expect(fx.skipReason).toBe('');
    expect(fx.failReason, 'a module older than its main.go was taken for a current one').toBeTypeOf('string');
    expect(fx.failReason).toContain('older than main.go');
    expect(fx.failReason).toContain(REBUILD);
    expect(REBUILD).toBe('bash scripts/build-wasm-fixture.sh');

    // The newest offender is the one named.
    const both = echoFixture(fixtureDir({ 'manifest.json': 120, 'main.go': 60, 'echo.wasm': 0 }));
    expect(both.failReason).toContain('older than manifest.json');
  });

  it('a module at least as new as every source is installed as it is', async () => {
    const { echoFixture } = await helper();
    for (const wasmAt of [60, 0]) {
      const dir = fixtureDir({ 'manifest.json': 0, 'main.go': 0, 'go.mod': -10, 'echo.wasm': wasmAt });
      const fx = echoFixture(dir);
      expect(fx.failReason, `echo.wasm at +${wasmAt}s`).toBeUndefined();
      expect(fx.present).toBe(true);
      expect(fx.wasm).toBe(path.join(dir, 'echo.wasm'));
      expect(fx.manifestPath).toBe(path.join(dir, 'manifest.json'));
      expect(fx.manifest?.permissions).toEqual(['state']);
      expect(fx.languages).toEqual(['en', 'tr']);
    }
  });

  it('without a module the spec skips, saying how to build one', async () => {
    const { echoFixture, REBUILD } = await helper();
    const fx = echoFixture(fixtureDir({ 'manifest.json': 0, 'main.go': 60 }));
    expect(fx.present).toBe(false);
    expect(fx.failReason).toBeUndefined();
    expect(fx.skipReason).toContain(REBUILD);
  });

  it('only what the module is built from makes it stale', async () => {
    const { echoFixture } = await helper();
    const fx = echoFixture(fixtureDir({ 'main.go': 0, 'manifest.json': 0, 'echo.wasm': 10, '.gitignore': 60, 'README.md': 60, 'sub/': 60 }));
    expect(fx.failReason).toBeUndefined();
  });

  it("counts the same files as the Go tests' rule (wasmfixture.Stale)", async () => {
    const { isSource } = await helper();
    const go = fs.readFileSync(path.join(REPO, 'backend', 'internal', 'testutil', 'wasmfixture', 'wasmfixture.go'), 'utf8');
    const suffixes = [...go.matchAll(/strings\.HasSuffix\(n, "([^"]+)"\)/g)].map((m) => m[1]);
    const names = [...go.matchAll(/\bn == "([^"]+)"/g)].map((m) => m[1]);
    // .go, .json / go.mod, go.sum on 2026-10-03; nothing read is no rule.
    expect(suffixes.length).toBeGreaterThanOrEqual(2);
    expect(names.length).toBeGreaterThanOrEqual(2);
    const goRule = (n: string) => suffixes.some((s) => n.endsWith(s)) || names.includes(n);
    const echo = path.join(REPO, 'backend', 'internal', 'wasmplugin', 'testdata', 'echo');
    const probes = [
      ...fs.readdirSync(echo),
      ...suffixes.map((s) => `x${s}`),
      ...names,
      'echo.wasm', '.gitignore', 'README.md', 'go.work', 'notes.txt', 'main.go.orig', 'Makefile', 'x.json5',
    ];
    for (const n of probes) expect(isSource(n), n).toBe(goRule(n));
  });

  it('looks where the fixture is', async () => {
    const { ECHO_DIR } = await helper();
    // relative(): a drive letter may come back in either case on Windows.
    expect(path.relative(REPO, ECHO_DIR).split(path.sep).join('/')).toBe('backend/internal/wasmplugin/testdata/echo');
    expect(fs.existsSync(path.join(ECHO_DIR, 'main.go'))).toBe(true);
    expect(fs.existsSync(path.join(ECHO_DIR, 'manifest.json'))).toBe(true);
  });
});
