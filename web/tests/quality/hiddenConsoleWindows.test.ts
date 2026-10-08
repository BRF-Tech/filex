// No script or test puts a console window on the screen of whoever runs it
// (#197).
//
// ⚠ Why: on 2026-10-07 `pnpm release 0.53.0` ran its pretag gates on the
// maintainer's Windows workstation, and node console windows opened on top of
// what he was typing, minutes apart; six of them sat there waiting for a key
// press. Measured: scripts/langpacks-nightly.mjs started its agent with
// `shell: true, detached: true`. On Windows a detached process has no console
// (DETACHED_PROCESS), so the console program it starts (cmd.exe -> node) is
// given a NEW console - a visible window, whose stdin is that window - and
// `windowsHide` on the detached process does not reach it. A window like that
// can take the focus: keystrokes meant for something else land in it.
//
// The rules, read from the source (tests/helpers/launchScan.ts):
//   1. Nothing is detached on Windows. A `detached` option is `false` or
//      depends on `process.platform` (a process group of its own where that
//      works, the parent's console on Windows), and the call carries
//      `windowsHide: true`. Read in every root below.
//   2. A process started in the background (spawn, execFile, exec, fork: the
//      parent goes on while it runs) carries `windowsHide: true` - in
//      scripts/, web/tests and web/scripts, where every program started is a
//      console one. With it a child that finds no console to share is given
//      one with no window (CREATE_NO_WINDOW), and its own children share
//      that. Not in desktop/scripts or e2e: they also start the desktop app,
//      a GUI program, whose windows the flag would hide from the run.
//   3. A run that promises nothing comes on screen (the Store package run,
//      its step 7) hides every program it starts, waited for or not.
// A file that stops before its work anywhere but Linux
// (`if (process.platform !== 'linux') { ... return }`, scripts/chain) is
// left out: nothing it starts runs on Windows.
//
// Run:  cd web && npx vitest run tests/quality/hiddenConsoleWindows.test.ts

import { existsSync, readdirSync, readFileSync } from 'node:fs';
import path from 'node:path';
import { describe, expect, it } from 'vitest';

import { BACKGROUND, type FileLaunches, type Launch, readLaunches } from '../helpers/launchScan';

const REPO = path.resolve(__dirname, '../../..');

/** Rule 2's roots. */
const BACKGROUND_ROOTS = ['scripts', 'web/tests', 'web/scripts'];
/** Rule 1's roots. */
const ROOTS = [...BACKGROUND_ROOTS, 'desktop/scripts', 'desktop/test', 'e2e'];
/** Rule 3: runs that promise nothing comes on screen. */
const SCREENLESS = ['desktop/scripts/store-e2e.mjs'];

const SKIP_DIRS = new Set(['node_modules', 'dist', 'test-results', 'playwright-report']);
const SOURCE = /\.(?:mjs|cjs|js|ts|mts|cts)$/;
const DECLARATION = /\.d\.[mc]?ts$/;

const DETACHED = 'detached on Windows too (make it depend on process.platform)';
const NO_HIDE = 'without windowsHide: true';
const OPAQUE = "imports child_process as a whole: import its functions by name, so this scan sees the calls";

function walk(dir: string, out: string[] = []): string[] {
  if (!existsSync(dir)) return out;
  for (const e of readdirSync(dir, { withFileTypes: true })) {
    if (e.name.startsWith('.') || SKIP_DIRS.has(e.name)) continue;
    const p = path.join(dir, e.name);
    if (e.isDirectory()) walk(p, out);
    else if (SOURCE.test(e.name) && !DECLARATION.test(e.name)) out.push(p);
  }
  return out;
}

/** What the rules say about one file. */
function problemsIn(rel: string, src: string): string[] {
  const read: FileLaunches = readLaunches(src);
  const out = read.opaque.map((line) => `${rel}:${line}: ${OPAQUE}`);
  if (read.linuxOnly) return out;
  const background = BACKGROUND_ROOTS.some((r) => rel.startsWith(`${r}/`));
  const screenless = SCREENLESS.includes(rel);
  for (const l of read.launches) {
    const detached = l.detached === 'on' || l.detached === 'platform';
    const mustHide = detached || screenless || (background && BACKGROUND.has(l.name));
    const why = [...(l.detached === 'on' ? [DETACHED] : []), ...(mustHide && !l.hidden ? [NO_HIDE] : [])];
    if (why.length) out.push(`${rel}:${l.line}: ${l.name}() ${why.join('; ')}`);
  }
  return out;
}

const files = ROOTS.flatMap((r) => walk(path.join(REPO, r))).map((p) => path.relative(REPO, p).split(path.sep).join('/'));
const sources = new Map(
  files.map((f) => [f, readFileSync(path.join(REPO, f), 'utf8')] as const).filter(([, src]) => src.includes('child_process')),
);
const launchesOf = (rel: string): Launch[] => readLaunches(sources.get(rel) ?? '').launches;

describe('no script or test puts a console window on the screen (#197)', () => {
  it('reads the launches it guards', () => {
    expect(files.filter((f) => f.startsWith('scripts/')).length).toBeGreaterThan(40);
    expect(files.filter((f) => f.startsWith('web/tests/')).length).toBeGreaterThan(100);
    expect(files.filter((f) => f.startsWith('e2e/')).length).toBeGreaterThan(50);
    for (const f of SCREENLESS) expect(existsSync(path.join(REPO, f)), f).toBe(true);
    // The two launches #197 is about, as the scan sees them now: the
    // translation agent detached on the build host alone, the gates hidden.
    expect(launchesOf('scripts/langpacks-nightly.mjs').filter((l) => l.detached !== 'none')).toEqual([
      expect.objectContaining({ name: 'spawn', detached: 'platform', hidden: true }),
    ]);
    const gates = launchesOf('scripts/release/engine.mjs').filter((l) => BACKGROUND.has(l.name));
    expect(gates.length).toBeGreaterThan(0);
    expect(gates.every((l) => l.hidden)).toBe(true);
    // Enough background launches read that an empty result means something.
    const background = [...sources.keys()]
      .filter((f) => BACKGROUND_ROOTS.some((r) => f.startsWith(`${r}/`)))
      .flatMap((f) => launchesOf(f))
      .filter((l) => BACKGROUND.has(l.name));
    expect(background.length).toBeGreaterThan(8);
  });

  it('nothing is detached on Windows, and every detached, background or screenless launch carries windowsHide: true', () => {
    const problems = [...sources].flatMap(([rel, src]) => problemsIn(rel, src));
    expect(problems).toEqual([]);
  });

  it('leaves out only files that stop anywhere but Linux before their work', () => {
    const left = [...sources].filter(([, src]) => readLaunches(src).linuxOnly).map(([rel]) => rel);
    expect(left).toEqual(expect.arrayContaining(['scripts/chain/nightly.mjs', 'scripts/chain/run.mjs']));
    for (const rel of left) expect(rel, 'a file outside the Linux build host tooling').toMatch(/^scripts\/(?:chain|gitlab-runner)\//);
  });

  it('the scan would catch the launches #197 found, and only those', () => {
    const said = (rel: string, ...lines: string[]) =>
      problemsIn(rel, ["import { spawn, spawnSync, execFile as run } from 'node:child_process';", ...lines].join('\n'));

    // The translation agent as 0.53 started it.
    expect(said('scripts/x.mjs', "spawn(bin, argv, { stdio: ['pipe', out, err], shell: true, detached: true, windowsHide: true });")).toEqual([
      `scripts/x.mjs:2: spawn() ${DETACHED}`,
    ]);
    expect(said('e2e/x.mjs', "spawn('bash', ['x.sh'], { detached: true });")).toEqual([`e2e/x.mjs:2: spawn() ${DETACHED}; ${NO_HIDE}`]);
    // A server started in the background without the flag, over several lines.
    expect(said('web/tests/x.test.ts', "const child = spawn(exe, ['serve'], {", '  env,', "  stdio: ['ignore', 'pipe', 'pipe'],", '});')).toEqual([
      `web/tests/x.test.ts:2: spawn() ${NO_HIDE}`,
    ]);
    // Under another name.
    expect(said('scripts/x.mjs', "run('powershell.exe', ['-Command', ps], (err) => done(err));")).toEqual([`scripts/x.mjs:2: execFile() ${NO_HIDE}`]);
    // A screenless run: a program it waits for, too.
    expect(problemsIn('desktop/scripts/store-e2e.mjs', "import { execFileSync } from 'node:child_process';\nexecFileSync('powershell.exe', args, { encoding: 'utf8' });")).toEqual([
      `desktop/scripts/store-e2e.mjs:2: execFileSync() ${NO_HIDE}`,
    ]);

    for (const ok of [
      "spawn(bin, argv, { shell: true, detached: process.platform !== 'win32', windowsHide: true });",
      "const group = process.platform === 'linux';\nspawn(bin, argv, { detached: group, windowsHide: true });",
      'spawn(bin, argv, { detached: !IS_WIN, windowsHide: true });',
      "spawn('node', ['x.mjs'], { detached: false, windowsHide: true });",
      "spawn('node', ['x.mjs'], { stdio: 'ignore', windowsHide: true });",
      "spawnSync('git', ['status'], { encoding: 'utf8' });",
    ]) {
      expect(said('scripts/x.mjs', ok), ok).toEqual([]);
    }
    // desktop/scripts and e2e also start the desktop app, a GUI program.
    expect(said('desktop/scripts/x.mjs', "spawn(ELECTRON_BIN, [DESKTOP], { stdio: 'ignore' });")).toEqual([]);

    // Commented, quoted, inside a template's text or a pattern, or a method of something else: no launch.
    const hole = '$' + '{cmd}';
    expect(
      said(
        'scripts/x.mjs',
        '// spawn(cmd, [], { detached: true })',
        '/* spawn(cmd, [], { detached: true }) */',
        "const s = 'spawn(cmd, [], { detached: true })';",
        `const t = \`spawn(${hole}, [], { detached: true })\`;`,
        'const r = /spawn\\(cmd, \\[\\], \\{ detached: true \\}\\)/;',
        're.exec(text); child.spawn(cmd); x.execFile(y);',
      ),
    ).toEqual([]);
    // A launch inside a template's expression is code.
    expect(said('scripts/x.mjs', `const t = \`a $${'{'}spawn('node', [])${'}'} b\`;`)).toEqual([`scripts/x.mjs:2: spawn() ${NO_HIDE}`]);

    // A whole-module import would hide every call from the scan.
    expect(problemsIn('scripts/x.mjs', "import * as cp from 'node:child_process';\ncp.spawn('x');")).toEqual([`scripts/x.mjs:1: ${OPAQUE}`]);

    // A file that stops anywhere but Linux before its work.
    expect(
      said('scripts/x.mjs', 'function main() {', "  if (process.platform !== 'linux') {", "    console.error('Linux only');", '    return 2;', '  }', "  spawn('bash', ['x.sh'], { detached: true });", '}'),
    ).toEqual([]);
  });
});
