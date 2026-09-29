// Puts the `filex` CLI into build/bin so electron-builder can ship it.
//
// The CLI is the sync engine — the app has no transfer code of its own — so a
// package without it is a package that silently syncs nothing. This script
// therefore FAILS the build rather than quietly producing one.
//
// ⚠ This copy is built WITHOUT the embedded server UI. `filex` normally carries
// the admin SPA inside it so `filex serve` can host it; that is 85 MB, and the
// desktop app already ships exactly those files in app/. Bundling the full
// binary put a second copy of the same interface in every installer: measured
// 156 MB against 45 MB for this one. What the app uses — `filex sync` and
// `filex client` — is identical either way; only `serve` (which nobody runs out
// of an app's resources folder) would come up without a UI.
//
// Source order:
//   1. $FILEX_CLI_BIN — an already-built binary (what CI passes in).
//   2. A local `go build` of ../backend, if Go is available.
//
// Run: node scripts/fetch-cli.mjs [--platform win32|linux|darwin] [--arch x64|arm64]
//
// --arch picks the CPU of the copy it BUILDS (x64/amd64 or arm64/aarch64 —
// electron-builder's and Go's spellings both work); without it GOARCH, and
// without that the host. An installer for another architecture needs it: the
// Windows arm64 package is cross-built on x64, and an x64 CLI inside it would
// install, open, and fail at the first sync. With FILEX_CLI_BIN it only
// checks: the binary given must be that architecture, or nothing is copied.

import { execFileSync } from 'node:child_process';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

import { cliLdflags, goModulePath } from './lib/cli-version.mjs';

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const DESKTOP = path.resolve(__dirname, '..');
const BACKEND = path.resolve(DESKTOP, '..', 'backend');
const OUT_DIR = path.join(DESKTOP, 'build', 'bin');

const argPlatform = process.argv.indexOf('--platform');
const platform = argPlatform > -1 ? process.argv[argPlatform + 1] : process.platform;
const argArch = process.argv.indexOf('--arch');
const GOARCH_OF = { x64: 'amd64', amd64: 'amd64', x86_64: 'amd64', arm64: 'arm64', aarch64: 'arm64' };
if (argArch > -1 && !GOARCH_OF[process.argv[argArch + 1]]) {
  console.error(`--arch ${process.argv[argArch + 1]}: use x64 or arm64`);
  process.exit(1);
}
const requestedArch = argArch > -1 ? GOARCH_OF[process.argv[argArch + 1]] : null;
const exeName = platform === 'win32' ? 'filex.exe' : 'filex';
const dest = path.join(OUT_DIR, exeName);

fs.mkdirSync(OUT_DIR, { recursive: true });

// ⚠⚠ The obvious way to point this at an existing binary is to point it at the
// one already sitting in build/bin — and that used to DESTROY it: the cleanup
// below ran first, deleted the file, and the copy then failed with ENOENT on a
// path that had existed a moment earlier. 147 MB of downloaded release binary,
// gone, with the build broken and no obvious cause. Measured 2026-08-10.
const sourceIsDest =
  process.env.FILEX_CLI_BIN &&
  path.resolve(process.env.FILEX_CLI_BIN) === path.resolve(dest);

if (sourceIsDest) {
  if (requestedArch && binaryArch(dest) !== requestedArch) {
    console.error(`${path.relative(DESKTOP, dest)} is ${binaryArch(dest)}, not ${requestedArch}`);
    process.exit(1);
  }
  console.log(`FILEX_CLI_BIN is already ${path.relative(DESKTOP, dest)} — keeping it as it is`);
  announce();
  process.exit(0);
}

// Clear stale copies: shipping a binary for the wrong OS is worse than none,
// because the app then looks armed and fails at spawn time on the user's
// machine instead of on this one.
for (const f of fs.readdirSync(OUT_DIR)) {
  if (f === 'filex' || f === 'filex.exe') fs.rmSync(path.join(OUT_DIR, f));
}

if (process.env.FILEX_CLI_BIN) {
  if (requestedArch) {
    const have = binaryArch(process.env.FILEX_CLI_BIN);
    if (have !== requestedArch) {
      console.error(`FILEX_CLI_BIN is ${have}, not ${requestedArch}: ${process.env.FILEX_CLI_BIN}`);
      process.exit(1);
    }
  }
  fs.copyFileSync(process.env.FILEX_CLI_BIN, dest);
  fs.chmodSync(dest, 0o755);
  console.log(`copied ${process.env.FILEX_CLI_BIN} -> ${path.relative(DESKTOP, dest)}`);
  announce();
  process.exit(0);
}

// //go:embed refuses to compile against a directory with no files in it, and
// the embed directories are build output (gitignored), so a fresh checkout has
// nothing there. A placeholder is what keeps this copy slim: it satisfies the
// directive without pulling the 85 MB SPA in.
for (const sub of ['admin', 'web']) {
  const dir = path.join(BACKEND, 'embed', sub);
  fs.mkdirSync(dir, { recursive: true });
  if (fs.readdirSync(dir).length === 0) fs.writeFileSync(path.join(dir, '.keep'), '');
}

const goos = platform === 'win32' ? 'windows' : platform;
// ⚠ GOARCH must follow the HOST, not default to amd64. This used to be
// `process.env.GOARCH || 'amd64'`, which put an x86_64 sync engine inside an
// arm64 filex.app: the app itself launched native, then spawned the CLI under
// Rosetta and macOS 26 greeted every launch with the "Intel-based apps will no
// longer be supported" deprecation alert (measured 2026-08-18). CI is
// unaffected — its x64 runners resolve to amd64 exactly as before, and an
// explicit GOARCH still wins for cross-builds.
// --arch, when given, wins over both.
const goarch = requestedArch || process.env.GOARCH || (process.arch === 'arm64' ? 'arm64' : 'amd64');
try {
  // Stamped like the released CLI (goreleaser): without it the app's sync
  // engine answered `filex --version` with "0.1.0-dev" in every package.
  const ldflags = cliLdflags({
    module: goModulePath(fs.readFileSync(path.join(BACKEND, 'go.mod'), 'utf8')),
    version: JSON.parse(fs.readFileSync(path.join(DESKTOP, 'package.json'), 'utf8')).version,
    commit: shortCommit(),
    date: new Date().toISOString().replace(/\.\d+Z$/, 'Z'),
  });
  execFileSync('go', ['build', '-trimpath', '-ldflags', ldflags, '-o', dest, './cmd/filex'], {
    cwd: BACKEND,
    stdio: 'inherit',
    env: { ...process.env, GOOS: goos, GOARCH: goarch, CGO_ENABLED: '0' },
  });
} catch (err) {
  console.error(
    '\nCould not produce the filex CLI, and the desktop app cannot sync without it.\n' +
      'Either install Go, or point FILEX_CLI_BIN at a built binary for this platform.\n',
  );
  process.exit(1);
}

fs.chmodSync(dest, 0o755);
const { size } = fs.statSync(dest);
console.log(`built ${path.relative(DESKTOP, dest)} (${(size / 1024 / 1024).toFixed(1)} MB, ${goos}/${binaryArch(dest)})`);
announce();

/** The short commit being packaged, or 'unknown' outside a git checkout. */
function shortCommit() {
  try {
    return execFileSync('git', ['rev-parse', '--short=7', 'HEAD'], { cwd: DESKTOP }).toString().trim() || 'unknown';
  } catch {
    return 'unknown';
  }
}

/** amd64 | arm64 | other, from the binary's own ELF / PE / Mach-O header. */
function binaryArch(file) {
  const b = Buffer.alloc(4096);
  const fd = fs.openSync(file, 'r');
  try {
    const n = fs.readSync(fd, b, 0, b.length, 0);
    if (n >= 20 && b.readUInt32BE(0) === 0x7f454c46) {
      const m = b[5] === 2 ? b.readUInt16BE(18) : b.readUInt16LE(18);
      return m === 0x3e ? 'amd64' : m === 0xb7 ? 'arm64' : `elf-0x${m.toString(16)}`;
    }
    if (n >= 0x40 && b[0] === 0x4d && b[1] === 0x5a) {
      const pe = Buffer.alloc(6);
      fs.readSync(fd, pe, 0, 6, b.readUInt32LE(0x3c));
      const m = pe.readUInt16LE(4);
      return m === 0x8664 ? 'amd64' : m === 0xaa64 ? 'arm64' : `pe-0x${m.toString(16)}`;
    }
    if (n >= 8 && b.readUInt32LE(0) === 0xfeedfacf) {
      const t = b.readUInt32LE(4);
      return t === 0x01000007 ? 'amd64' : t === 0x0100000c ? 'arm64' : `macho-0x${t.toString(16)}`;
    }
    return 'unknown';
  } finally {
    fs.closeSync(fd);
  }
}

/** Says out loud WHICH commit is about to be packaged.
 *
 * ⚠ A `git checkout <tag>` that aborts (a dirty working tree is enough) leaves
 * the build running happily against the previous commit, and every later step —
 * including the tests — passes, because the old code is perfectly good code. It
 * just is not the code being released. Measured: a v0.13.1 package was built
 * from v0.13.0 and nothing said so. */
function announce() {
  let head = 'unknown';
  try {
    const rev = execFileSync('git', ['rev-parse', '--short', 'HEAD'], { cwd: DESKTOP }).toString().trim();
    let tag = '';
    try {
      tag = ' ' + execFileSync('git', ['describe', '--tags', '--always'], { cwd: DESKTOP }).toString().trim();
    } catch {
      /* not on a tag */
    }
    const dirty = execFileSync('git', ['status', '--porcelain'], { cwd: DESKTOP }).toString().trim();
    head = `${rev}${tag}${dirty ? '  (WORKING TREE DIRTY)' : ''}`;
  } catch {
    /* not a git checkout — nothing to announce */
  }
  const version = JSON.parse(fs.readFileSync(path.join(DESKTOP, 'package.json'), 'utf8')).version;
  console.log(`\n  packaging filex ${version} from ${head}\n`);
}
