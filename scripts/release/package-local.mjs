#!/usr/bin/env node
// The release's packaging, off GitHub Actions: what release.yml builds and
// publishes on a tag, from a maintainer's machine (task #174).
//
//   node scripts/release/package-local.mjs vX.Y.Z                    the plan: every step, where it runs, what it publishes
//   node scripts/release/package-local.mjs vX.Y.Z --run              build everything; publish nothing
//   node scripts/release/package-local.mjs vX.Y.Z --run --publish    and publish: the GitHub Release, ghcr, the desktop files
//     --only binaries,images,desktop-windows,desktop-linux           a part of it (default: all four)
//     --export <dir>                                                 the public checkout (default: ../filex-export)
//
// ⚠⚠ Why (0.52.0, 2026-10-05): GitHub Actions could not run the tag, and the
// release went out from a PC by hand - goreleaser from the public tree, the
// images with QEMU for arm64, the Windows and Linux desktop packages - in an
// order and with commands nobody had written down. This is that path, written
// down and checked: the same commands release.yml runs, on the tag's own tree.
//
// It runs nothing in the private tree and nothing on the export checkout
// itself. The public tree is cloned at the tag into
// <export>/.git/filex-pc-package/<tag>/tree (Windows) and, for the Linux
// steps on Windows, into ~/wt/filex-pc-package/<tag> inside WSL - Go and Node
// on WSL's own disk, never on /mnt. What the Linux steps build is copied back
// into <export>/.git/filex-pc-package/<tag>/out/linux before it is uploaded.
//
// What it does NOT do, and says so at the end:
//   - macOS: a Mac builds the macOS packages. The release goes out without
//     them and its notes say so; `only=macos` adds them once GitHub has macOS
//     runners again (docs/CONTRIBUTING.md, Release process).
//   - the arm64 snap: snapcraft cannot cross-build it (`only=snap-arm64`).
//   - npm, the Microsoft Store, winget and Homebrew: a person, with their own
//     sign-in (the commands are printed).
// The partial runs above (`only=macos`, `only=snap-arm64`) publish for a
// commit gated on CircleCI once GitHub Actions is back: their `verify` takes
// CircleCI's green `ci` workflow when GitHub has no full matrix and dry run
// of the commit (#181). A release packaged here has made its GitHub Release,
// and its tag run, should it start once Actions is back, publishes nothing:
// `verify` stops a tag run on CircleCI's word whose tag has a Release, so it
// cannot tag images or attach files over the ones published from here.
//
// Preconditions, checked before anything runs: a signed tag vX.Y.Z in the
// public checkout, on its origin as the same commit; with --publish, `gh`
// signed in and, for the images, `docker login ghcr.io` done.

import { spawnSync } from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

import { toWslPath } from '../lib/go-build.mjs';
import { SEMVER } from './checks.mjs';
import { findBash, run, shq, slash } from './engine.mjs';

const HERE = path.dirname(fileURLToPath(import.meta.url));
const REPO = path.resolve(HERE, '..', '..');

export const PARTS = ['binaries', 'images', 'desktop-windows', 'desktop-linux'];

/** The GoReleaser the PC path used for 0.52.0 (release.yml asks for "~> v2"). */
export const GORELEASER_VERSION = 'v2.18.2';

/** The image tags of a release, as release.yml's docker-manifest makes them. */
export function imageTags(tag) {
  return { full: ['latest', tag, 'full', `full-${tag}`], slim: ['slim', `slim-${tag}`] };
}

/** The desktop files each part uploads to the Release (release.yml's attach step globs, by platform). */
export const DESKTOP_FILES = {
  'desktop-windows': ['*.exe', '*.blockmap', 'latest.yml'],
  'desktop-linux': ['*.AppImage', '*.deb', '*.rpm', '*-amd64.snap', 'latest-linux.yml', 'latest-linux-arm64.yml'],
};

/**
 * The steps of the PC path, in order: { id, part, title, where, cmd,
 * publishes }. `where` is `windows` (the maintainer's Windows, Git Bash) or
 * `linux` (natively on Linux, through WSL on Windows); `cmd` is a bash script
 * for that shell. Pure: the test reads it, the runner runs it.
 *
 *   tag, version   v1.2.3 and 1.2.3
 *   exportDir      the public checkout (its origin is GitHub)
 *   workDir        where the Windows tree, the logs and the copied-back
 *                  Linux files go (inside the export checkout's .git)
 *   linuxDir       the Linux tree (WSL's disk on Windows)
 *   linuxExport    the export checkout as the Linux shell reaches it
 *   linuxOut       workDir/out/linux as the Linux shell reaches it
 */
export function localPlan({ tag, version, exportDir, workDir, linuxDir, linuxExport, linuxOut, only = PARTS, publish = false, goreleaser = GORELEASER_VERSION }) {
  if (!SEMVER.test(version ?? '') || tag !== `v${version}`) throw new Error(`a release is vX.Y.Z: got ${tag}`);
  const unknown = only.filter((p) => !PARTS.includes(p));
  if (unknown.length) throw new Error(`--only ${unknown.join(',')}: the parts are ${PARTS.join(', ')}`);
  const want = (p) => only.includes(p);
  const win = slash(path.join(workDir, 'tree'));
  const out = slash(path.join(workDir, 'out'));
  const steps = [];
  // A Linux step finds Go where WSL keeps it (the release's Go gates do the same).
  const add = (s) => steps.push({ publishes: false, ...s, cmd: s.where === 'linux' ? `export PATH=/usr/local/go/bin:$HOME/go/bin:$PATH\n${s.cmd}` : s.cmd });
  const linux = want('binaries') || want('images') || want('desktop-linux');
  const R = '-R BRF-Tech/filex';

  if (want('desktop-windows')) {
    add({
      id: 'tree-windows', part: 'desktop-windows', where: 'windows', title: `the public tree at ${tag}, for the Windows packages`,
      cmd: `set -euo pipefail\nrm -rf ${shq(win)} ${shq(out)}/windows\nmkdir -p ${shq(out)}/windows\ngit clone -q ${shq(slash(exportDir))} ${shq(win)}\ngit -C ${shq(win)} checkout -q --detach ${shq(tag)}`,
    });
  }
  if (linux) {
    add({
      id: 'tree-linux', part: 'linux', where: 'linux', title: `the public tree at ${tag}, on Linux's own disk`,
      cmd:
        `set -euo pipefail\nT=${linuxDir}\nrm -rf "$T" ${shq(linuxOut)}\nmkdir -p "$(dirname "$T")" ${shq(linuxOut)}\n` +
        `git clone -q ${shq(linuxExport)} "$T"\ngit -C "$T" checkout -q --detach ${shq(tag)}\n` +
        `cd "$T" && pnpm install --frozen-lockfile && pnpm -r --filter './packages/*' build && pnpm --filter ./web build && node scripts/sync-embed.mjs`,
    });
  }
  if (want('binaries')) {
    // goreleaser from the tag's tree: the eight binaries, the archives, the
    // checksums and the string catalogue. Publishing, it creates the GitHub
    // Release with the CHANGELOG's notes (release.yml's binaries job). The
    // Homebrew cask and the winget pull request skip themselves without
    // HOMEBREW_TAP_DEPLOY_KEY and WINGET_TOKEN (.goreleaser.yml).
    const args = publish ? `release --clean --release-notes="$NOTES"` : 'release --clean --skip=publish';
    add({
      id: 'binaries', part: 'binaries', where: 'linux', publishes: publish,
      title: publish ? `goreleaser ${goreleaser}: the binaries, and the GitHub Release ${tag}` : `goreleaser ${goreleaser}: the binaries (dist/), nothing published`,
      cmd:
        `set -euo pipefail\ncd ${linuxDir}\nNOTES="$PWD/release-notes.md"\nnode scripts/release-notes.mjs --github ${shq(tag)} > "$NOTES"\n` +
        `${publish ? '[ -n "${GITHUB_TOKEN:-}" ] || { echo "GITHUB_TOKEN is not set (gh auth token)" >&2; exit 1; }\n' : ''}` +
        `GORELEASER_CURRENT_TAG=${shq(tag)} go run github.com/goreleaser/goreleaser/v2@${goreleaser} ${args}\n` +
        `ls dist`,
    });
  }
  if (want('images')) {
    // Both images for amd64 and arm64 (arm64 under QEMU: there is no arm64
    // runner here), with the labels release.yml's candidate writes, under
    // the tags its docker-manifest makes. Built only, unless --publish.
    const t = imageTags(tag);
    const tagsOf = (list) => list.map((x) => `-t ghcr.io/brf-tech/filex:${x}`).join(' ');
    const dest = publish ? '--push' : '--output type=cacheonly';
    add({
      id: 'images', part: 'images', where: 'linux', publishes: publish,
      title: publish ? `both images, amd64 + arm64, pushed to ghcr.io as ${tag}` : 'both images, amd64 + arm64, built only',
      cmd:
        `set -euo pipefail\ncd ${linuxDir}\n` +
        `docker run --privileged --rm tonistiigi/binfmt --install arm64 >/dev/null\n` +
        `docker buildx inspect filex-pc >/dev/null 2>&1 || docker buildx create --name filex-pc --driver docker-container >/dev/null\n` +
        `sha=$(git rev-parse HEAD)\ndate=$(date -u +%Y-%m-%dT%H:%M:%SZ)\n` +
        `common=(--builder filex-pc --platform linux/amd64,linux/arm64 --build-arg VERSION=${tag} --build-arg "COMMIT=$sha" --build-arg "DATE=$date" ` +
        `--label "org.opencontainers.image.revision=$sha" --label org.opencontainers.image.version=${tag} --label org.opencontainers.image.source=https://github.com/BRF-Tech/filex ${dest})\n` +
        `docker buildx build "\${common[@]}" -f docker/Dockerfile ${tagsOf(t.full)} .\n` +
        `docker buildx build "\${common[@]}" -f docker/Dockerfile.slim ${tagsOf(t.slim)} .` +
        (publish
          ? `\nfor img in ghcr.io/brf-tech/filex:${tag} ghcr.io/brf-tech/filex:slim-${tag}; do\n  docker pull -q --platform linux/amd64 "$img"\n  node .github/workflows/scripts/smoke-cli.mjs --image "$img" --expect-arch amd64 --expect-version ${version}\ndone`
          : ''),
    });
  }
  if (want('desktop-windows')) {
    // The windows row of release.yml: x64, then arm64 cross-built with an
    // arm64 CLI inside, one update feed for both (x64 first), and every
    // binary checked to be the architecture on its label.
    add({
      id: 'desktop-windows', part: 'desktop-windows', where: 'windows', title: 'Windows x64 + arm64: installers, portable copies, one latest.yml',
      cmd:
        `set -euo pipefail\ncd ${shq(win)}\npnpm install --frozen-lockfile\npnpm -r --filter './packages/*' build\n` +
        `(cd desktop && npm version ${version} --no-git-tag-version --allow-same-version)\n` +
        `pnpm --filter ./desktop run dist:win\nmv desktop/release/latest.yml ${shq(out)}/windows/latest-x64.yml\n` +
        `(cd desktop && pnpm run build && GOARCH=arm64 node scripts/fetch-cli.mjs --platform win32 && pnpm exec electron-builder --win --arm64 --publish never)\n` +
        `mv desktop/release/latest.yml ${shq(out)}/windows/latest-arm64.yml\n` +
        `node .github/workflows/scripts/merge-latest-yml.mjs --out desktop/release/latest.yml ${shq(out)}/windows/latest-x64.yml ${shq(out)}/windows/latest-arm64.yml\n` +
        `for a in x64 arm64; do d=desktop/release/win-unpacked; [ "$a" = arm64 ] && d=desktop/release/win-arm64-unpacked; node .github/workflows/scripts/arch-of.mjs "$d/filex.exe" "$d/resources/bin/filex.exe" --expect "$a"; done\n` +
        `ls -la desktop/release`,
    });
  }
  if (want('desktop-linux')) {
    // The linux row (x64 AppImage, .deb, .rpm, feed, the amd64 snap from
    // electron-builder's template) and the linux-arm64 row without its snap,
    // cross-built here. Copied back for the upload.
    add({
      id: 'desktop-linux', part: 'desktop-linux', where: 'linux', title: 'Linux x64 + arm64: AppImage, .deb, .rpm, feeds; the amd64 snap',
      cmd:
        `set -euo pipefail\ncd ${linuxDir}\ncommand -v rpmbuild >/dev/null || { echo "rpmbuild is missing: sudo apt-get install -y rpm" >&2; exit 1; }\n` +
        `(cd desktop && npm version ${version} --no-git-tag-version --allow-same-version)\n` +
        `pnpm --filter ./desktop run dist:linux\n` +
        `pnpm --filter ./desktop exec electron-builder --linux snap --publish never\n` +
        `(cd desktop && pnpm run build && GOARCH=arm64 node scripts/fetch-cli.mjs --platform linux && pnpm exec electron-builder --linux AppImage deb rpm --arm64 --publish never)\n` +
        `node .github/workflows/scripts/arch-of.mjs desktop/release/linux-unpacked/filex-app-bin desktop/release/linux-unpacked/resources/bin/filex --expect amd64\n` +
        `node .github/workflows/scripts/arch-of.mjs desktop/release/linux-arm64-unpacked/filex-app-bin desktop/release/linux-arm64-unpacked/resources/bin/filex --expect arm64\n` +
        `shopt -s nullglob\nfiles=(${DESKTOP_FILES['desktop-linux'].map((g) => `desktop/release/${g}`).join(' ')})\n` +
        `[ \${#files[@]} -gt 0 ] || { echo "no Linux desktop files" >&2; exit 1; }\ncp "\${files[@]}" ${shq(linuxOut)}/\nls -la ${shq(linuxOut)}`,
    });
  }
  if (publish && (want('desktop-windows') || want('desktop-linux'))) {
    // After the binaries: goreleaser is what creates the Release.
    const globs = [
      ...(want('desktop-windows') ? DESKTOP_FILES['desktop-windows'].map((g) => `${win}/desktop/release/${g}`) : []),
      ...(want('desktop-linux') ? DESKTOP_FILES['desktop-linux'].map((g) => `${out}/linux/${g}`) : []),
    ];
    add({
      id: 'upload', part: 'desktop', where: 'windows', publishes: true, title: `the desktop files, attached to the Release ${tag}`,
      cmd:
        `set -euo pipefail\nshopt -s nullglob\ngh release view ${shq(tag)} ${R} --json tagName >/dev/null || { echo "${tag} has no GitHub Release yet: the binaries step creates it" >&2; exit 1; }\n` +
        `files=(${globs.map((g) => g.replace(/ /g, '\\ ')).join(' ')})\n[ \${#files[@]} -gt 0 ] || { echo "nothing to attach" >&2; exit 1; }\n` +
        `printf 'attaching: %s\\n' "\${files[@]}"\ngh release upload ${shq(tag)} "\${files[@]}" --clobber ${R}`,
    });
  }

  const notes = [
    `macOS: not built here. The release goes out without the macOS packages: say so in its notes ("The macOS packages follow once GitHub can build them.").`,
    `  Once GitHub has tested ${tag}'s commit (its ci.yml push run and a dry run of release.yml on it, docs/CONTRIBUTING.md, Release process):`,
    `    gh workflow run release.yml ${R} -f tag=${tag} -f only=macos -f publish=true`,
    `The arm64 snap: snapcraft cannot cross-build it. The same way, after GitHub has tested the commit:`,
    `    gh workflow run release.yml ${R} -f tag=${tag} -f only=snap-arm64 -f publish=true`,
    `npm (a person, signed in with npm login; trusted publishing is GitHub's alone), in a tree at ${tag}:`,
    `    pnpm -r --filter './packages/*' exec npm version ${version} --no-git-tag-version --allow-same-version`,
    `    pnpm publish --filter './packages/*' --access public --no-git-checks --otp <code>`,
    `Microsoft Store: pnpm --filter ./desktop run dist:store (and dist:store:arm64), bundle, and submit in Partner Center by hand (0.52.0: Submission 8).`,
    `winget and Homebrew: goreleaser skipped them without their tokens; the desktop manifests come from node desktop/scripts/pkg-manifests.mjs (release.yml, desktop job).`,
    `Then the deploy checks, on the tagged commits:  pnpm release ${version} --resume --only deploy`,
  ];
  return { steps, notes };
}

// ── the command line ────────────────────────────────────────────────────────

function parse(argv) {
  const o = { tag: null, run: false, publish: false, only: PARTS, exportDir: path.resolve(REPO, '..', 'filex-export') };
  for (let i = 0; i < argv.length; i++) {
    const a = argv[i];
    if (a === '--run') o.run = true;
    else if (a === '--publish') o.publish = true;
    else if (a === '--only') o.only = String(argv[++i] ?? '').split(',').map((s) => s.trim()).filter(Boolean);
    else if (a === '--export') o.exportDir = path.resolve(argv[++i] ?? '');
    else if (!a.startsWith('--') && !o.tag) o.tag = a.startsWith('v') ? a : `v${a}`;
    else throw new Error(`unknown argument ${a}`);
  }
  if (!o.tag) throw new Error('usage: node scripts/release/package-local.mjs vX.Y.Z [--run [--publish]] [--only parts] [--export dir]');
  if (o.publish && !o.run) throw new Error('--publish runs the steps: give --run too');
  return o;
}

/** A refusal before anything runs, or null. */
function preflight(o) {
  const git = (...args) => run('git', ['-C', o.exportDir, ...args]);
  if (!fs.existsSync(path.join(o.exportDir, '.git'))) return `${o.exportDir} is no checkout (--export)`;
  const local = git('rev-parse', '--verify', '-q', `refs/tags/${o.tag}^{commit}`).stdout.trim();
  if (!local) return `${o.tag} is not a tag in ${o.exportDir}`;
  if (git('cat-file', '-t', `refs/tags/${o.tag}`).stdout.trim() !== 'tag' || git('verify-tag', `refs/tags/${o.tag}`).status !== 0) {
    return `${o.tag} in ${o.exportDir} is not a signed tag with a good signature (git tag -v ${o.tag})`;
  }
  const remote = git('ls-remote', 'origin', `refs/tags/${o.tag}^{}`).stdout.trim().split(/\s+/)[0];
  if (remote !== local) return `origin's ${o.tag} is ${remote || 'missing'}, not ${local}: push the signed tag first (docs/CONTRIBUTING.md, Release process, step 8)`;
  if (o.publish && run('gh', ['auth', 'status']).status !== 0) return 'gh is not signed in (gh auth login)';
  return null;
}

function main() {
  const o = parse(process.argv.slice(2));
  const version = o.tag.slice(1);
  const workDir = path.join(o.exportDir, '.git', 'filex-pc-package', o.tag);
  const onWindows = process.platform === 'win32';
  const linuxDir = onWindows ? `"$HOME/wt/filex-pc-package/${o.tag}"` : shq(slash(path.join(workDir, 'linux-tree')));
  const linuxExport = onWindows ? toWslPath(o.exportDir) : slash(o.exportDir);
  const linuxOut = onWindows ? `${toWslPath(workDir)}/out/linux` : slash(path.join(workDir, 'out', 'linux'));
  const { steps, notes } = localPlan({ tag: o.tag, version, exportDir: o.exportDir, workDir, linuxDir, linuxExport, linuxOut, only: o.only, publish: o.publish });

  console.log(`filex ${o.tag} - packaged off GitHub Actions${o.run ? (o.publish ? ' (PUBLISHES)' : ' (builds only)') : ' (the plan; nothing runs)'}`);
  console.log(`  public checkout  ${slash(o.exportDir)}\n  work             ${slash(workDir)}${onWindows ? `\n  linux tree       WSL ${linuxDir}` : ''}\n`);
  steps.forEach((s, i) => console.log(`${String(i + 1).padStart(2)}. ${s.title}  [${s.where}${s.publishes ? ', publishes' : ''}]`));
  if (!o.run) {
    console.log('\nThe commands, step by step:');
    for (const s of steps) console.log(`\n# ${s.id} (${s.where})\n${s.cmd}`);
  } else {
    const refused = preflight(o);
    if (refused) {
      console.error(`\nREFUSED  ${refused}`);
      process.exit(1);
    }
    const bash = findBash();
    if (!bash) throw new Error("no bash: Git for Windows' bash.exe was not found");
    const logs = path.join(workDir, 'logs');
    fs.mkdirSync(logs, { recursive: true });
    const token = o.publish ? run('gh', ['auth', 'token']).stdout.trim() : '';
    for (const s of steps) {
      const log = path.join(logs, `${s.id}.log`);
      // The script goes through a file: a many-line argument does not survive
      // the way into WSL (wsl.exe parses its own command line).
      const script = path.join(logs, `${s.id}.sh`);
      fs.writeFileSync(script, `${s.cmd}\n`);
      const env = { ...process.env, ...(token ? { GITHUB_TOKEN: token, WSLENV: `${process.env.WSLENV ? `${process.env.WSLENV}:` : ''}GITHUB_TOKEN/u` } : {}) };
      const [bin, args] = s.where === 'linux' && onWindows ? ['wsl', ['-e', 'bash', '-l', toWslPath(script)]] : [bash, [slash(script)]];
      console.log(`\n── ${s.id}: ${s.title}  (log ${slash(log)})`);
      const fd = fs.openSync(log, 'w');
      const r = spawnSync(bin, args, { cwd: os.tmpdir(), env, stdio: ['ignore', fd, fd] });
      fs.closeSync(fd);
      const tail = fs.readFileSync(log, 'utf8').trim().split('\n').slice(-15).join('\n');
      console.log(tail);
      if (r.status !== 0) {
        console.error(`\nFAILED  ${s.id} exited ${r.status ?? r.signal}. Nothing after it ran. Fix, then run again (each step starts from a clean tree).`);
        process.exit(1);
      }
    }
  }
  console.log(`\nNot done here, by design:\n${notes.map((n) => `  ${n}`).join('\n')}`);
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  try {
    main();
  } catch (e) {
    console.error(`package-local: ${e.message}`);
    process.exit(2);
  }
}
