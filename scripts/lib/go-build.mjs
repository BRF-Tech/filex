// One way to run `go build` from a node script, on every machine this repo is
// built on.
//
// ⚠ Why this is not simply `go build`: on the maintainer's Windows workstation
// the Go toolchain lives in WSL while node and pnpm run on Windows. A plain
// `go build` there is ENOENT, and `pnpm run build:backend` — a documented step
// of `build:all` — could not run at all. The fallback cross-builds a Windows
// binary through WSL instead. That is the only platform-specific branch, and it
// is about where the TOOLCHAIN lives, not about filex behaving differently.
//
// ⚠ The WSL build writes into the Linux side's own /tmp and only then copies
// the result out. Building straight onto /mnt/<drive> means the linker writes a
// ~180 MB file through the 9p bridge in many small seeks; one copy is the
// cheaper and the more predictable of the two.
//
// Used by scripts/build-backend.mjs (the backend binary) and
// e2e/shots/capture.mjs (the example plugin its plugins screenshot installs).

import { spawnSync } from 'node:child_process';
import { existsSync, mkdirSync, rmSync, statSync } from 'node:fs';
import { randomBytes } from 'node:crypto';
import path from 'node:path';

const lastLines = (s, n) => String(s ?? '').trim().split(/\r?\n/).slice(-n).join('\n');

function nativeGo() {
  const r = spawnSync('go', ['version'], { encoding: 'utf8', windowsHide: true });
  return r.status === 0 ? r.stdout.trim() : null;
}

function wslGo() {
  if (process.platform !== 'win32') return null;
  const r = spawnSync('wsl', ['-e', 'bash', '-lc', 'go version'], { encoding: 'utf8', windowsHide: true });
  return r.status === 0 ? r.stdout.trim() : null;
}

/** C:\a\b → /mnt/c/a/b. Only drive-letter paths can be reached from WSL. */
export function toWslPath(winPath) {
  const abs = path.resolve(winPath);
  const m = /^([A-Za-z]):[\\/](.*)$/.exec(abs);
  if (!m) throw new Error(`cannot express ${abs} as a WSL path (not on a drive letter)`);
  return `/mnt/${m[1].toLowerCase()}/${m[2].replace(/\\/g, '/')}`;
}

/** Single-quote a word for bash. */
const shq = (s) => `'${String(s).split("'").join(`'"'"'`)}'`;

/**
 * goBuild builds `pkg` (relative to `cwd`, e.g. `./cmd/filex`) into `out`.
 *
 * Always CGO_ENABLED=0 — the same as CI and goreleaser, and the only setting
 * the WSL cross-build can honour. Throws with the compiler's own output when
 * the build fails, and with an explanation when there is no toolchain at all.
 *
 * Returns `{ out, toolchain }`, where toolchain says which branch was taken.
 */
export function goBuild({ cwd, pkg, out, ldflags = '-s -w', trimpath = true, log = () => {} }) {
  const outAbs = path.resolve(out);
  mkdirSync(path.dirname(outAbs), { recursive: true });
  const flags = [...(trimpath ? ['-trimpath'] : []), ...(ldflags ? [`-ldflags=${ldflags}`] : [])];

  const native = nativeGo();
  if (native) {
    log(`${native} → ${outAbs}`);
    const r = spawnSync('go', ['build', ...flags, '-o', outAbs, pkg], {
      cwd,
      env: { ...process.env, CGO_ENABLED: '0' },
      stdio: ['ignore', 'inherit', 'pipe'],
      encoding: 'utf8',
      windowsHide: true,
    });
    if (r.status !== 0) {
      throw new Error(`go build ${pkg} failed (exit ${r.status})\n${lastLines(r.stderr, 40)}`);
    }
    return { out: outAbs, toolchain: native };
  }

  const wsl = wslGo();
  if (!wsl) {
    throw new Error(
      'no Go toolchain: `go` is not on this PATH' +
        (process.platform === 'win32' ? ', and `wsl -e bash -lc "go version"` failed too' : '') +
        '. Install Go (backend/go.mod names the version) and re-run.',
    );
  }
  const scratch = `/tmp/filex-gobuild-${randomBytes(6).toString('hex')}${path.extname(outAbs)}`;
  const script = [
    `cd ${shq(toWslPath(cwd))}`,
    `CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build ${flags.map(shq).join(' ')} -o ${shq(scratch)} ${shq(pkg)}`,
    `cp ${shq(scratch)} ${shq(toWslPath(outAbs))}`,
  ].join(' && ');
  log(`${wsl} (WSL, cross-building for windows/amd64) → ${outAbs}`);
  rmSync(outAbs, { force: true });
  const r = spawnSync('wsl', ['-e', 'bash', '-lc', script], {
    stdio: ['ignore', 'inherit', 'pipe'],
    encoding: 'utf8',
    windowsHide: true,
  });
  spawnSync('wsl', ['-e', 'rm', '-f', scratch], { stdio: 'ignore', windowsHide: true });
  if (r.status !== 0) {
    throw new Error(`go build ${pkg} through WSL failed (exit ${r.status})\n${lastLines(r.stderr, 40)}`);
  }
  if (!existsSync(outAbs) || statSync(outAbs).size === 0) {
    throw new Error(`go build through WSL reported success but ${outAbs} is missing or empty`);
  }
  return { out: outAbs, toolchain: `${wsl} via WSL` };
}
