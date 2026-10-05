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
// it cannot be built, says what to do and exits 78; outside a snap it never
// adds --no-sandbox. The AppImage entry carries a harmless switch instead.
//
// The snap (0.52): 0.50 and 0.51 asked for `browser-support` with
// `allow-sandbox: true` and refused to start until that plug was connected.
// The Snap Store grants allow-sandbox to trusted publishers only and reviews
// it by hand: the 0.50 and 0.51 revisions sat in "Manual review pending" and
// stable stayed on 0.49. Snapcraft's advice for Electron is --no-sandbox under
// strict confinement (AppArmor + seccomp + namespaces around the whole app),
// so inside a snap the launcher starts the app with --no-sandbox and asks
// snapd nothing; the snap asks for no allow-sandbox.

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

test('the snap asks for no allow-sandbox, and the launcher asks snapd nothing', () => {
  // ⚠ allow-sandbox is for trusted publishers only and is reviewed by hand:
  // with it the 0.50 and 0.51 revisions never left "Manual review pending".
  const snap = code(yamlBlock('snap'));
  assert.doesNotMatch(snap, /allow-sandbox/);
  assert.doesNotMatch(snap, /browser-sandbox/);
  // A browser-support plug of our own would be the same request under
  // another name; electron-builder's `default` already carries the plain one.
  assert.doesNotMatch(snap, /interface: browser-support/);
  assert.doesNotMatch(code(LAUNCHER), /snapctl|browser-sandbox|snap connect/);
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

test('the launcher adds --no-sandbox in a snap and nowhere else', () => {
  const lines = code(LAUNCHER).split('\n');
  // Every other start is the binary with exactly the arguments it was given.
  const execs = lines.filter((l) => /\bexec\b/.test(l));
  assert.ok(execs.length >= 4);
  const own = execs.filter((l) => !/exec "\$bin" "\$@"$/.test(l));
  assert.deepEqual(own.map((l) => l.trim()), ['[ "$kind" = snap ] && exec "$bin" --no-sandbox "$@"']);
  // --no-sandbox appears twice: the person's own switch (or the one the
  // snap's command.sh appends), compared; and the snap's start.
  const mentions = lines.filter((l) => l.includes('--no-sandbox'));
  assert.deepEqual(mentions.map((l) => l.trim()), [
    '[ "$a" = "--no-sandbox" ] && exec "$bin" "$@"',
    '[ "$kind" = snap ] && exec "$bin" --no-sandbox "$@"',
  ]);
  // `kind` is snap only for a launcher that lives inside $SNAP.
  assert.match(LAUNCHER, /if \[ -n "\$\{SNAP:-\}" \]; then\n\s+case "\$self" in "\$SNAP"\/\*\) kind=snap ;; esac\nfi/);
  // The refusal ends the script, with its own code.
  assert.match(LAUNCHER, /\nexit 78\n$/);
});

test('the launcher points at the docs headings that exist', () => {
  const desktop = fs.readFileSync(path.join(ROOT, '..', 'docs', 'DESKTOP.md'), 'utf8');
  const anchors = [...LAUNCHER.matchAll(/docs\.filex\.sh\/DESKTOP#([a-z0-9-]+)/g)].map((m) => m[1]);
  assert.ok(anchors.length >= 1);
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
    // Answers as 0.50's launcher expected, and notes that it was asked.
    const say = opts.snapctl.says ? `echo '${opts.snapctl.says}' >&2\n` : '';
    const asked = path.join(dir, 'snapctl-asked').split(path.sep).join('/');
    fs.writeFileSync(
      path.join(stubs, 'snapctl'),
      `#!/bin/sh\necho "$*" >> "${asked}"\n[ "$1 $2" = "is-connected browser-sandbox" ] || exit 9\n${say}exit ${opts.snapctl.code}\n`,
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

  await t.test("a snap starts with --no-sandbox under the snap's confinement, and snapd is not asked", () => {
    // The case 0.50 and 0.51 refused with exit 78: the old browser-sandbox
    // plug not connected (snapctl: exit 1, nothing said). `unshare` says no
    // too, as it does inside a snap (its AppArmor profile will not run it).
    const s = stage({ unshare: 'no', snapctl: { code: 1 } });
    try {
      const r = launch(s, ['filex://sign-in?code=1', '/home/ada/My Files/a b.docx', '--hidden'], {
        SNAP: snapDir(s.exe),
        SNAP_NAME: 'filex-app',
        LANG: 'en_GB.UTF-8',
      });
      assert.equal(r.code, 0, r.err);
      // First, so a `--` among the arguments cannot turn it into a file name.
      assert.equal(r.out.trim(), 'STARTED [--no-sandbox] [filex://sign-in?code=1] [/home/ada/My Files/a b.docx] [--hidden]');
      assert.equal(r.err, '');
      assert.ok(!fs.existsSync(path.join(s.dir, 'snapctl-asked')), 'the launcher asked snapctl');
    } finally {
      fs.rmSync(s.dir, { recursive: true, force: true });
    }
  });

  await t.test("a snap started by its command.sh, which already ends in --no-sandbox, gets it once", () => {
    const s = stage({ unshare: 'no' });
    try {
      const r = launch(s, ['--hidden', '--no-sandbox'], { SNAP: snapDir(s.exe), SNAP_NAME: 'filex-app' });
      assert.equal(r.code, 0, r.err);
      assert.equal(r.out.trim(), 'STARTED [--hidden] [--no-sandbox]');
    } finally {
      fs.rmSync(s.dir, { recursive: true, force: true });
    }
  });

  await t.test("SNAP set by somebody else's snap does not make a .deb a snap: the sandbox stays on", () => {
    // A terminal inside a snap (an editor's) hands its SNAP to what it starts;
    // the launcher is a snap's only when it lives under $SNAP.
    const env = { SNAP: '/snap/code/123', SNAP_NAME: 'code', LANG: 'en_US.UTF-8' };
    const yes = stage({ unshare: 'yes' });
    try {
      const r = launch(yes, ['--hidden'], env);
      assert.equal(r.code, 0, r.err);
      assert.equal(r.out.trim(), 'STARTED [--hidden]');
    } finally {
      fs.rmSync(yes.dir, { recursive: true, force: true });
    }
    const no = stage({ unshare: 'no' });
    try {
      const r = launch(no, [], env);
      assert.equal(r.code, 78);
      assert.equal(r.out, '');
      assert.match(r.err, /Install the \.deb or the \.rpm/);
    } finally {
      fs.rmSync(no.dir, { recursive: true, force: true });
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
