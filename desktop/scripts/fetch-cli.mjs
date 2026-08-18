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
// Run: node scripts/fetch-cli.mjs [--platform win32|linux|darwin]

import { execFileSync } from 'node:child_process';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const DESKTOP = path.resolve(__dirname, '..');
const BACKEND = path.resolve(DESKTOP, '..', 'backend');
const OUT_DIR = path.join(DESKTOP, 'build', 'bin');

const argPlatform = process.argv.indexOf('--platform');
const platform = argPlatform > -1 ? process.argv[argPlatform + 1] : process.platform;
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
const goarch = process.env.GOARCH || (process.arch === 'arm64' ? 'arm64' : 'amd64');
try {
  execFileSync('go', ['build', '-trimpath', '-ldflags', '-s -w', '-o', dest, './cmd/filex'], {
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
console.log(`built ${path.relative(DESKTOP, dest)} (${(size / 1024 / 1024).toFixed(1)} MB)`);
announce();

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
