// The Linux launcher in front of the Electron binary, and the two package
// settings that went with it (0.50).
//
// Run:  node --experimental-strip-types --test desktop/test/linux-launcher.test.ts
//
// Measured on 2026-10-01 (Ubuntu 26.04, kernel.apparmor_restrict_unprivileged_userns=1)
// with the 0.49.0 packages:
//   - the AppImage, started as a person starts it, stopped in Chromium's zygote
//     ("The SUID sandbox helper binary was found, but is not configured
//     correctly") before any window, with nothing on screen to say why;
//   - the image's own desktop entry said `Exec=AppRun --no-sandbox %U`, so an
//     AppImage added to the menu by an integrator ran with no sandbox at all;
//   - the snap's command.sh ended in `--no-sandbox`, and chrome-sandbox was not
//     in the package.
// The launcher (build/linux/launcher.sh) checks for the sandbox first and, when
// it cannot be built, says what to do and exits 78; it never adds
// --no-sandbox. The AppImage entry carries a harmless switch instead, and the
// snap asks for `browser-support` with `allow-sandbox`.

import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';
import { createRequire } from 'node:module';
import { fileURLToPath } from 'node:url';

import { LINUX_APP_NAME, STORE_IDS } from '../src/channel.ts';
import { linuxLauncherOf } from '../src/login-item.ts';
import { classifyArgv } from '../src/openwith.ts';

const ROOT = path.join(path.dirname(fileURLToPath(import.meta.url)), '..');
const read = (rel: string) => fs.readFileSync(path.join(ROOT, rel), 'utf8');
const YML = read('electron-builder.yml');
const LAUNCHER = read('build/linux/launcher.sh');
const require = createRequire(import.meta.url);
const { installLauncher } = require('../scripts/linux-launcher.cjs') as {
  installLauncher(dir: string, name: string): { exe: string; bin: string; moved: boolean };
};

/** The lines of one top-level YAML block (`linux:`, `snap:` …). */
function yamlBlock(key: string): string {
  const m = new RegExp(`^${key}:\\n((?:[ \\t].*\\n|\\s*\\n)*)`, 'm').exec(YML);
  assert.ok(m, `electron-builder.yml has no top-level "${key}:" block`);
  return m[1];
}
/** Uncommented lines only. */
const code = (text: string) =>
  text
    .split('\n')
    .filter((l) => !/^\s*#/.test(l))
    .join('\n');

// ── the package settings ───────────────────────────────────────────────────

test('every pack goes through after-pack.cjs, which runs the launcher step and the mac seal', () => {
  assert.match(code(YML), /^afterPack: scripts\/after-pack\.cjs$/m);
  const hook = read('scripts/after-pack.cjs');
  assert.match(hook, /require\('\.\/linux-launcher\.cjs'\)/);
  assert.match(hook, /require\('\.\/adhoc-sign\.cjs'\)/);
});

test("the AppImage's own desktop entry no longer turns the sandbox off", () => {
  const block = code(yamlBlock('appImage'));
  const args = [...block.matchAll(/^\s+-\s+(\S+)\s*$/gm)].map((m) => m[1]);
  // ⚠ Empty is not enough: electron-builder writes --no-sandbox for an empty
  // list too (`executableArgs.join(" ") || "--no-sandbox"`).
  assert.ok(args.length > 0, 'appImage.executableArgs must name at least one argument');
  for (const a of args) {
    assert.notEqual(a, '--no-sandbox');
    // The AppImage runtime takes every --appimage-* argument as its own option.
    assert.doesNotMatch(a, /^--appimage-/);
    // A switch the app does not read as a document or a link.
    assert.deepEqual(classifyArgv(['/x/filex-app-bin', a]), { deepLinks: [], files: [] });
  }
  assert.doesNotMatch(code(YML), /--no-sandbox/);
});

test('the snap asks for the sandbox, under the plug name the launcher prints', () => {
  const snap = yamlBlock('snap');
  const m = /^ {4}- ([a-z-]+):\n {8}interface: browser-support\n {8}allow-sandbox: true$/m.exec(snap);
  assert.ok(m, 'snap.plugs has no browser-support plug with allow-sandbox: true');
  assert.equal(m[1], 'browser-sandbox');
  assert.ok(LAUNCHER.includes(':browser-sandbox"'), 'the launcher prints another plug name');
  assert.ok(LAUNCHER.includes('${SNAP_INSTANCE_NAME:-${SNAP_NAME:-filex-app}}'));
  assert.equal(STORE_IDS.snap, LINUX_APP_NAME);
});

// ── the launcher file ──────────────────────────────────────────────────────

test('the launcher is a POSIX sh script with LF line ends', () => {
  assert.ok(LAUNCHER.startsWith('#!/bin/sh\n'));
  assert.ok(!LAUNCHER.includes('\r'), 'a CR after the shebang is "bad interpreter" on every machine');
  // bash-only: [[ ]], `function f`, ${x//a/b}, `local` (an AppArmor `<local/...>`
  // include in the printed profile is text, not the keyword).
  assert.doesNotMatch(code(LAUNCHER), /\[\[|^\s*function\s|\$\{[A-Za-z_]+\/\/|^\s*local\s/m, 'bash-only syntax in a /bin/sh script');
});

test('the launcher never starts the app without its sandbox on its own', () => {
  const lines = code(LAUNCHER).split('\n');
  // Every start is the binary with exactly the arguments it was given.
  const execs = lines.filter((l) => /\bexec\b/.test(l));
  assert.ok(execs.length >= 4);
  for (const l of execs) assert.match(l, /exec "\$bin" "\$@"$/, l);
  // --no-sandbox appears once, as a comparison: the person's own switch.
  const mentions = lines.filter((l) => l.includes('--no-sandbox'));
  assert.deepEqual(mentions.map((l) => l.trim()), ['[ "$a" = "--no-sandbox" ] && exec "$bin" "$@"']);
  // The refusal ends the script, with its own code.
  assert.match(LAUNCHER, /\nexit 78\n$/);
});

test('the launcher points at the docs headings that exist', () => {
  const desktop = fs.readFileSync(path.join(ROOT, '..', 'docs', 'DESKTOP.md'), 'utf8');
  const anchors = [...LAUNCHER.matchAll(/docs\.filex\.sh\/DESKTOP#([a-z0-9-]+)/g)].map((m) => m[1]);
  assert.ok(anchors.length >= 2);
  const slug = (h: string) => h.toLowerCase().replace(/[^a-z0-9 -]/g, '').trim().replace(/\s+/g, '-');
  const headings = [...desktop.matchAll(/^#{2,4} (.+)$/gm)].map((m) => slug(m[1]));
  for (const a of anchors) assert.ok(headings.includes(a), `docs/DESKTOP.md has no heading for #${a}`);
});

// ── the afterPack step ─────────────────────────────────────────────────────

function packedDir(): string {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'fx-launcher-'));
  fs.writeFileSync(path.join(dir, 'filex-app'), 'ELF-ish binary\n', { mode: 0o755 });
  fs.writeFileSync(path.join(dir, 'chrome-sandbox'), 'helper\n', { mode: 0o755 });
  return dir;
}

test('afterPack moves the binary to filex-app-bin and writes the launcher as filex-app', () => {
  const dir = packedDir();
  try {
    const r = installLauncher(dir, 'filex-app');
    assert.equal(r.moved, true);
    assert.equal(fs.readFileSync(path.join(dir, 'filex-app-bin'), 'utf8'), 'ELF-ish binary\n');
    assert.equal(fs.readFileSync(path.join(dir, 'filex-app'), 'utf8'), LAUNCHER);
    if (process.platform !== 'win32') {
      assert.equal(fs.statSync(path.join(dir, 'filex-app')).mode & 0o777, 0o755);
      assert.equal(fs.statSync(path.join(dir, 'filex-app-bin')).mode & 0o777, 0o755);
    }
    // Twice on the same directory: nothing moves again.
    assert.equal(installLauncher(dir, 'filex-app').moved, false);
    assert.equal(fs.readFileSync(path.join(dir, 'filex-app-bin'), 'utf8'), 'ELF-ish binary\n');
    // A -bin with something else in front of it is refused, not guessed at.
    fs.writeFileSync(path.join(dir, 'filex-app'), 'something else\n');
    assert.throws(() => installLauncher(dir, 'filex-app'), /is not the launcher/);
  } finally {
    fs.rmSync(dir, { recursive: true, force: true });
  }
});

test('afterPack refuses a directory with no binary in it', () => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'fx-launcher-'));
  try {
    assert.throws(() => installLauncher(dir, 'filex-app'), /is missing/);
  } finally {
    fs.rmSync(dir, { recursive: true, force: true });
  }
});

test('afterPack leaves Windows and macOS packs alone', async () => {
  const hook = require('../scripts/linux-launcher.cjs') as (c: unknown) => Promise<void>;
  const dir = packedDir();
  try {
    for (const platform of ['win32', 'darwin']) {
      await hook({ electronPlatformName: platform, appOutDir: dir, packager: { executableName: 'filex-app' } });
    }
    assert.ok(!fs.existsSync(path.join(dir, 'filex-app-bin')));
  } finally {
    fs.rmSync(dir, { recursive: true, force: true });
  }
});

test('the sign-in entry names the launcher: the binary name and the app name agree', () => {
  assert.equal(linuxLauncherOf(`/opt/filex/${LINUX_APP_NAME}-bin`), `/opt/filex/${LINUX_APP_NAME}`);
});

// ── the launcher, run ──────────────────────────────────────────────────────
//
// In a POSIX shell with stand-ins: `filex-app-bin` prints what it was started
// with, `unshare` answers yes or no. DISPLAY is cleared, so no dialog opens on
// the machine running the tests; the message goes to stderr as well.

function findSh(): string | null {
  if (process.env.FILEX_TEST_SH) return process.env.FILEX_TEST_SH;
  if (process.platform !== 'win32') return '/bin/sh';
  // ⚠ Never a bare "bash"/"sh" on Windows: CreateProcess finds WSL's in System32.
  const git = 'C:/Program Files/Git/usr/bin/sh.exe';
  return fs.existsSync(git) ? git : null;
}
const SH = findSh();

/** The commands the launcher needs, each a one-line stand-in pointing at the
 *  real one, so a test can leave `unshare` out of PATH altogether. */
const TOOLS = ['readlink', 'dirname', 'basename', 'stat', 'mktemp', 'rm', 'cat'];

function stage(opts: { unshare: 'yes' | 'no' | 'absent'; snapctl?: { code: number; says?: string } }): { dir: string; exe: string; path: string } {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'fx-launch-run-'));
  const app = path.join(dir, 'app');
  const stubs = path.join(dir, 'stubs');
  fs.mkdirSync(app);
  fs.mkdirSync(stubs);
  fs.writeFileSync(path.join(app, 'filex-app'), LAUNCHER, { mode: 0o755 });
  fs.writeFileSync(
    path.join(app, 'filex-app-bin'),
    '#!/bin/sh\nprintf "STARTED"\nfor a in "$@"; do printf " [%s]" "$a"; done\nprintf "\\n"\n',
    { mode: 0o755 },
  );
  for (const t of TOOLS) {
    const real = spawnSync(SH!, ['-c', `command -v ${t}`], { encoding: 'utf8' }).stdout.trim();
    assert.ok(real, `no ${t} in this shell`);
    fs.writeFileSync(path.join(stubs, t), `#!/bin/sh\nexec "${real}" "$@"\n`, { mode: 0o755 });
  }
  if (opts.unshare !== 'absent') {
    fs.writeFileSync(path.join(stubs, 'unshare'), `#!/bin/sh\nexit ${opts.unshare === 'yes' ? 0 : 1}\n`, { mode: 0o755 });
  }
  if (opts.snapctl) {
    const say = opts.snapctl.says ? `echo '${opts.snapctl.says}' >&2\n` : '';
    fs.writeFileSync(
      path.join(stubs, 'snapctl'),
      `#!/bin/sh\n[ "$1 $2" = "is-connected browser-sandbox" ] || exit 9\n${say}exit ${opts.snapctl.code}\n`,
      { mode: 0o755 },
    );
  }
  return { dir, exe: path.join(app, 'filex-app'), path: stubs };
}

function launch(s: { exe: string; path: string }, args: string[], env: Record<string, string> = {}) {
  const clean: Record<string, string> = { PATH: s.path, HOME: os.tmpdir(), TMPDIR: os.tmpdir() };
  if (process.platform === 'win32' && process.env.SYSTEMROOT) clean.SYSTEMROOT = process.env.SYSTEMROOT;
  const r = spawnSync(SH!, [s.exe.split(path.sep).join('/'), ...args], { encoding: 'utf8', env: { ...clean, ...env } });
  return { code: r.status, out: r.stdout, err: r.stderr };
}

test('the launcher, run', { skip: SH ? false : 'no POSIX sh on this machine (set FILEX_TEST_SH)' }, async (t) => {
  await t.test('starts the app when a user namespace can be made, with every argument intact', () => {
    const s = stage({ unshare: 'yes' });
    try {
      const r = launch(s, ['filex://sign-in?code=1', '/home/ada/My Files/a b.docx', '--hidden']);
      assert.equal(r.code, 0, r.err);
      assert.equal(r.out.trim(), 'STARTED [filex://sign-in?code=1] [/home/ada/My Files/a b.docx] [--hidden]');
      assert.doesNotMatch(r.out, /no-sandbox/);
    } finally {
      fs.rmSync(s.dir, { recursive: true, force: true });
    }
  });

  await t.test('an AppImage that cannot build the sandbox is told about the AppArmor profile, and does not start', () => {
    const s = stage({ unshare: 'no' });
    try {
      const r = launch(s, [], { APPIMAGE: '/home/ada/Apps/filex-desktop-x86_64.AppImage', LANG: 'en_US.UTF-8' });
      assert.equal(r.code, 78);
      assert.equal(r.out, '');
      assert.match(r.err, /does not start without Chromium's sandbox/);
      assert.match(r.err, /profile filex-appimage @\{HOME\}\/\*\*\/filex-desktop-\*\.AppImage flags=\(unconfined\)/);
      assert.match(r.err, /sudo apparmor_parser -r \/etc\/apparmor\.d\/filex-appimage/);
      assert.match(r.err, /docs\.filex\.sh\/DESKTOP#appimage-on-recent-ubuntu/);
      // In the home folder under its release name: nothing more to say.
      assert.doesNotMatch(r.err, /release name/);
    } finally {
      fs.rmSync(s.dir, { recursive: true, force: true });
    }
  });

  await t.test('an AppImage the profile would not cover is told so', () => {
    const s = stage({ unshare: 'no' });
    try {
      const r = launch(s, [], { APPIMAGE: '/opt/apps/filex.AppImage', LANG: 'C' });
      assert.equal(r.code, 78);
      assert.match(r.err, /This one is: \/opt\/apps\/filex\.AppImage/);
      assert.match(r.err, /give it back its release name/);
    } finally {
      fs.rmSync(s.dir, { recursive: true, force: true });
    }
  });

  await t.test('in Turkish when the session is Turkish', () => {
    const s = stage({ unshare: 'no' });
    try {
      const r = launch(s, [], { APPIMAGE: '/home/ada/filex-desktop-arm64.AppImage', LANG: 'tr_TR.UTF-8' });
      assert.equal(r.code, 78);
      assert.match(r.err, /filex, Chromium'un kum havuzu olmadan açılmaz\./);
      assert.match(r.err, /Ayrıntı: https:\/\/docs\.filex\.sh/);
    } finally {
      fs.rmSync(s.dir, { recursive: true, force: true });
    }
  });

  /** The snap's own directory, as the launcher's readlink -f spells it. */
  const snapDir = (exe: string) =>
    spawnSync(SH!, ['-c', 'readlink -f "$1"', 'sh', path.dirname(exe).split(path.sep).join('/')], { encoding: 'utf8' }).stdout.trim();

  await t.test('a snap whose browser-sandbox plug is not connected is told the snap connect command', () => {
    // `unshare` would say yes here: inside a snap it is snapd that is asked.
    const s = stage({ unshare: 'yes', snapctl: { code: 1 } });
    try {
      const r = launch(s, [], { SNAP: snapDir(s.exe), SNAP_NAME: 'filex-app', LANG: 'en_GB.UTF-8' });
      assert.equal(r.code, 78);
      assert.equal(r.out, '');
      assert.match(r.err, /sudo snap connect filex-app:browser-sandbox/);
      assert.match(r.err, /DESKTOP#the-snap-and-the-sandbox/);
      assert.doesNotMatch(r.err, /apparmor_parser/);
    } finally {
      fs.rmSync(s.dir, { recursive: true, force: true });
    }
  });

  await t.test('a snap with the plug connected starts, whatever unshare would say', () => {
    // Measured: the snap's AppArmor profile refuses to run /usr/bin/unshare at all.
    const s = stage({ unshare: 'no', snapctl: { code: 0 } });
    try {
      const r = launch(s, ['--hidden'], { SNAP: snapDir(s.exe), SNAP_NAME: 'filex-app' });
      assert.equal(r.code, 0, r.err);
      assert.equal(r.out.trim(), 'STARTED [--hidden]');
    } finally {
      fs.rmSync(s.dir, { recursive: true, force: true });
    }
  });

  await t.test('a snapd that cannot answer leaves it to Chromium', () => {
    const s = stage({ unshare: 'no', snapctl: { code: 1, says: 'error: unknown command is-connected' } });
    try {
      const r = launch(s, [], { SNAP: snapDir(s.exe), SNAP_NAME: 'filex-app' });
      assert.equal(r.code, 0, r.err);
      assert.equal(r.out.trim(), 'STARTED');
    } finally {
      fs.rmSync(s.dir, { recursive: true, force: true });
    }
  });

  await t.test('anything else is told to use the .deb or the .rpm', () => {
    const s = stage({ unshare: 'no' });
    try {
      const r = launch(s, [], { LANG: 'en_US.UTF-8' });
      assert.equal(r.code, 78);
      assert.match(r.err, /Install the \.deb or the \.rpm/);
    } finally {
      fs.rmSync(s.dir, { recursive: true, force: true });
    }
  });

  await t.test("a --no-sandbox the person typed is theirs: passed through, nothing added", () => {
    const s = stage({ unshare: 'no' });
    try {
      const r = launch(s, ['--no-sandbox'], { APPIMAGE: '/home/ada/filex-desktop-x86_64.AppImage' });
      assert.equal(r.code, 0, r.err);
      assert.equal(r.out.trim(), 'STARTED [--no-sandbox]');
    } finally {
      fs.rmSync(s.dir, { recursive: true, force: true });
    }
  });

  await t.test('with no unshare to ask, Chromium decides on its own', () => {
    const s = stage({ unshare: 'absent' });
    try {
      const r = launch(s, ['--hidden']);
      assert.equal(r.code, 0, r.err);
      assert.equal(r.out.trim(), 'STARTED [--hidden]');
    } finally {
      fs.rmSync(s.dir, { recursive: true, force: true });
    }
  });

  await t.test('a missing binary is an incomplete installation, not a sandbox problem', () => {
    const s = stage({ unshare: 'yes' });
    try {
      fs.rmSync(path.join(path.dirname(s.exe), 'filex-app-bin'));
      const r = launch(s, []);
      assert.equal(r.code, 127);
      assert.match(r.err, /installation is incomplete/);
    } finally {
      fs.rmSync(s.dir, { recursive: true, force: true });
    }
  });
});
