// Every release builds, checks and ships the arm64 packages by itself, and a
// dry run of the release publishes nothing.
//
// ⚠ Why (2026-09-28: the server, the CLI and the desktop app are to support
// ARM everywhere): until 0.48.0 the CLI and the images were built for
// arm64 and never once RUN on an arm64 machine, and the desktop app shipped
// arm64 only on macOS — Linux and Windows on Arm got nothing, the Snap Store
// had no arm64 revision, the Microsoft Store no arm64 package. Adding them is
// a matrix row here and a step there, and each of those is one careless edit
// away from being lost again, or from breaking the x64 users who exist:
//
//   - electron-updater reads ONE latest.yml on Windows whatever the CPU. An
//     arm64 build that writes its own latest.yml over the x64 one offers every
//     x64 install an arm64 installer. The feeds are joined, x64 first.
//   - the embedded CLI follows the HOST unless told otherwise: an arm64
//     installer cross-built on x64 carries an x64 sync engine unless GOARCH
//     says arm64, and would install, open, and fail at the first sync.
//   - a manually started run must never publish half a release.
//
// The workflows live in the PUBLIC checkout only (see
// releaseGatesImages.test.ts): read from `<repo>/.github/workflows` or
// FILEX_WORKFLOWS_DIR, and skipped when neither exists. The helper scripts
// under .github/workflows/scripts are imported and run from there too.
import { spawnSync } from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { pathToFileURL } from 'node:url';
import { describe, expect, it } from 'vitest';

import { findBash } from '../../../scripts/release/engine.mjs';

const REPO = path.resolve(__dirname, '../../..');
const DIR = [process.env.FILEX_WORKFLOWS_DIR, path.join(REPO, '.github', 'workflows')].find(
  (d) => d && fs.existsSync(path.join(d, 'release.yml')),
);

/** A file's text without comment-only lines, LF. */
const code = (rel: string) =>
  fs
    .readFileSync(path.join(DIR!, rel), 'utf8')
    .split(/\r?\n/)
    .filter((l) => !/^\s*#/.test(l))
    .join('\n');

/** The text of one step, from its `- name:` to the next step or job. */
function step(text: string, name: string): string {
  const at = text.indexOf(`- name: ${name}`);
  expect(at, `step "${name}"`).toBeGreaterThan(0);
  const rest = text.slice(at + 1);
  const next = rest.search(/\n\s+- (name|uses):|\n {2}[A-Za-z0-9_-]+:\s*\n/);
  return next < 0 ? rest : rest.slice(0, next);
}

/** The `if:` of a step's text, or ''. */
const cond = (stepText: string) => /\n\s+if:\s*(.+)/.exec(stepText)?.[1].trim() ?? '';

/** The lines of one job, up to the next job. */
function job(text: string, name: string): string {
  const lines = text.split('\n');
  const start = lines.findIndex((l) => l === `  ${name}:`);
  expect(start, `job "${name}"`).toBeGreaterThanOrEqual(0);
  const next = lines.findIndex((l, i) => i > start && /^ {2}[A-Za-z0-9_-]+:\s*$/.test(l));
  return lines.slice(start, next < 0 ? undefined : next).join('\n');
}

/** The desktop matrix plan writes for a full run and for only=arm64. */
function desktopRows(release: string) {
  const plan = job(release, 'plan');
  const blocks = [...plan.matchAll(/desktop='(\{[\s\S]*?\})'/g)].map((m) => JSON.parse(m[1]).include as Array<Record<string, string>>);
  expect(blocks, 'plan writes two desktop matrices (only=arm64, then all)').toHaveLength(2);
  const [arm64Only, full] = blocks;
  return { arm64Only, full };
}

/**
 * Runs `body` (the body of an async function with the helper module as `m`)
 * in a plain Node process and returns what it returns, as JSON.
 *
 * The helpers live in the public checkout, outside this project, where vite's
 * module server will not load from; a child process imports them as the
 * release does.
 */
function inNode<T>(script: string, body: string): T {
  const url = pathToFileURL(path.join(DIR!, 'scripts', script)).href;
  const src = `const m = await import(${JSON.stringify(url)}); const r = await (async () => { ${body} })(); process.stdout.write(JSON.stringify(r));`;
  const r = spawnSync(process.execPath, ['--input-type=module', '-e', src], { encoding: 'utf8' });
  if (r.status !== 0) throw new Error(`${script}: ${r.stderr}`);
  return JSON.parse(r.stdout) as T;
}

describe('the release ships arm64', () => {
  it.runIf(!!DIR)('builds the Linux arm64 packages natively on ubuntu-24.04-arm', () => {
    const { full, arm64Only } = desktopRows(code('release.yml'));
    for (const rows of [full, arm64Only]) {
      const row = rows.find((r) => r.label === 'linux-arm64');
      expect(row, 'a linux-arm64 row').toBeTruthy();
      expect(row!.os).toBe('ubuntu-24.04-arm');
      expect(row!.script).toBe('dist:linux');
    }
    // x64 users keep what they had: the same rows, on the same runners.
    const byLabel = Object.fromEntries(full.map((r) => [r.label, r]));
    expect(byLabel.linux.os).toBe('ubuntu-latest');
    expect(byLabel.windows.arches).toBe('x64 arm64');
    expect(byLabel.store.arches).toBe('x64 arm64');
    expect(byLabel.macos.os).toBe('macos-14');
    const release = code('release.yml');
    expect(step(release, 'fpm for the arm64 .deb and .rpm')).toMatch(/USE_SYSTEM_FPM=true/);
    const snap = step(release, 'snapcraft + LXD for the arm64 snap');
    expect(snap).toMatch(/lxd init --auto/);
    // snapcraft 9 refuses base core20 (dry run 36373259912); 8.x builds it.
    expect(snap).toMatch(/snap install snapcraft --classic --channel=8\.x\/stable/);
  });

  it.runIf(!!DIR)('builds Windows arm64 beside x64 and joins the two update feeds, x64 first', () => {
    const release = code('release.yml');
    const aside = release.indexOf('- name: Set the x64 Windows feed aside');
    const build = release.indexOf('- name: Build the Windows arm64 installers');
    const join = release.indexOf('- name: One Windows update feed for both architectures');
    expect(aside).toBeGreaterThan(0);
    expect(build).toBeGreaterThan(aside);
    expect(join).toBeGreaterThan(build);
    const arm = step(release, 'Build the Windows arm64 installers');
    expect(arm).toMatch(/GOARCH=arm64 node scripts\/fetch-cli\.mjs --platform win32/);
    expect(arm).toMatch(/electron-builder --win --arm64/);
    const merge = step(release, 'One Windows update feed for both architectures');
    const x64At = merge.indexOf('latest-x64.yml');
    const armAt = merge.indexOf('latest-arm64.yml');
    expect(x64At).toBeGreaterThan(0);
    expect(armAt).toBeGreaterThan(x64At);
    expect(merge).toMatch(/merge-latest-yml\.mjs --out desktop\/release\/latest\.yml/);
    // The feed the attach step uploads is the joined one; the halves stay out
    // of desktop/release, or `latest*.yml` would publish them too.
    expect(release).toMatch(/mv desktop\/release\/latest\.yml "\$RUNNER_TEMP\/latest-x64\.yml"/);
    expect(arm).toMatch(/mv release\/latest\.yml "\$RUNNER_TEMP\/latest-arm64\.yml"/);
  });

  it.runIf(!!DIR)('bundles both Store packages and submits the bundle', () => {
    const release = code('release.yml');
    // The x64 package is checked first: the arm64 build replaces the CLI in
    // build/bin that the e2e variant is built from.
    expect(release.indexOf('- name: Build the arm64 Store package')).toBeGreaterThan(
      release.indexOf('- name: Install the Store package and check it as a Store copy'),
    );
    expect(step(release, 'Build the arm64 Store package')).toMatch(/electron-builder --win appx --arm64/);
    const bundle = step(release, 'Bundle the Store packages');
    expect(bundle).toMatch(/makeappx[\s\S]*bundle \/d \$dir \/p desktop\/release\/filex-desktop\.msixbundle \/bv/);
    expect(bundle).toMatch(/ProcessorArchitecture/);
    const submit = step(release, 'Submit to the Microsoft Store');
    expect(submit.indexOf('*.msixbundle')).toBeGreaterThan(0);
    expect(submit.indexOf('*.msixbundle')).toBeLessThan(submit.indexOf('*.appx'));
    expect(step(release, 'Keep the Store package for Partner Center')).toMatch(/\*\.msixbundle/);
    const resubmit = code('msstore-resubmit.yml');
    expect(resubmit.indexOf('pkg/*.msixbundle')).toBeGreaterThan(0);
    expect(resubmit.indexOf('pkg/*.msixbundle')).toBeLessThan(resubmit.indexOf('pkg/*.appx'));
  });

  it.runIf(!!DIR)('checks every binary inside a package is the architecture on its label', () => {
    const s = step(code('release.yml'), 'The binaries inside are the architecture on the label');
    expect(s).toMatch(/arch-of\.mjs/);
    expect(s).toMatch(/win-arm64-unpacked/);
    // Linux: the Electron binary is filex-app-bin; `filex-app` is the launcher (0.50).
    expect(s).toMatch(/linux-arm64-unpacked\/filex-app-bin" "\$r\/linux-arm64-unpacked\/resources\/bin\/filex" --expect arm64/);
    expect(s).toMatch(/linux-unpacked\/filex-app-bin" "\$r\/linux-unpacked\/resources\/bin\/filex" --expect amd64/);
    expect(s).toMatch(/head -1 "\$r\/linux-unpacked\/filex-app" \| grep -qx '#!\/bin\/sh'/);
    expect(s).toMatch(/resources\/bin\/filex\.exe" --expect "\$a"/);
    expect(cond(s), 'every row checks').toBe('');
  });

  it.runIf(!!DIR)("uploads each Linux architecture's snap to the Snap Store", () => {
    const s = step(code('release.yml'), 'Upload to the Snap Store');
    expect(cond(s)).toMatch(/startsWith\(matrix\.label, 'linux'\)/);
    expect(s).toMatch(/\[ "\$LABEL" = linux-arm64 \] && snap=desktop\/release\/filex-desktop-arm64\.snap/);
    expect(s).toMatch(/snapcraft upload --release=stable "\$snap"/);
  });

  it.runIf(!!DIR)('runs the arm64 CLI, server and images on arm64 machines', () => {
    const release = code('release.yml');
    const smoke = job(release, 'cli-smoke');
    for (const [os, asset] of [['ubuntu-24.04-arm', 'filex-linux-arm64'], ['windows-11-arm', 'filex-windows-arm64.exe'], ['macos-14', 'filex-darwin-arm64']]) {
      expect(smoke, os).toMatch(new RegExp(`os: ${os}, label: [\\w-]+, asset: ${asset.replace('.', '\\.')}, arch: arm64`));
    }
    expect(smoke).toMatch(/smoke-cli\.mjs --binary "bin\/\$ASSET" --expect-arch/);
    // A dry run hands cli-smoke goreleaser's bare binaries under their release
    // names, which only dist/artifacts.json knows (dry run 36374963952 looked
    // for dist/filex-linux-arm64 and found nothing).
    expect(job(release, 'binaries')).toMatch(/jq -r [^\n]*"Binary"[^\n]*startswith\("filex-"\)[^\n]*dist\/artifacts\.json/);
    const docker = job(release, 'docker');
    expect(docker).toMatch(/smoke-cli\.mjs --image "\$img" --expect-arch "\$\{\{ matrix\.arch \}\}"/);
    expect(docker).toMatch(/runner: ubuntu-24\.04-arm/);
    // The smoke test runs before anything is tagged: docker-manifest waits for docker.
    expect(job(release, 'docker-manifest')).toMatch(/needs: \[plan, docker\]/);
  });

  it.runIf(!!DIR)('installs and opens the arm64 desktop packages on arm64 machines', () => {
    const check = job(code('release.yml'), 'desktop-arm64-check');
    expect(check).toMatch(/os: ubuntu-24\.04-arm, label: linux-arm64, artifact: desktop-arm64-linux-arm64/);
    expect(check).toMatch(/os: windows-11-arm, label: windows, artifact: desktop-arm64-windows/);
    expect(check).toMatch(/name: \$\{\{ matrix\.artifact \}\}/);
    expect(check).toMatch(/linux-arm64\) ARCH=arm64; DEB=arm64; IMG=arm64; SNAP=arm64 ;;/);
    expect(check).toMatch(/apt-get install -y "\.\/pkg\/filex-desktop-\$DEB\.deb"/);
    expect(check).toMatch(/look --exe \/usr\/bin\/filex-app --out/);
    expect(check).toMatch(/cp "pkg\/filex-desktop-\$IMG\.AppImage" "\$img"/);
    expect(check).toMatch(/Start-Process \$setup -ArgumentList '\/S', '\/currentuser'/);
    expect(check).toMatch(/desktop-look\.mjs" --exe \$app/);
    expect(check).toMatch(/filex-desktop-portable-arm64\.exe/);
    expect(check).toMatch(/desktop-arm64-screenshots/);
  });

  // 0.50: the 0.49 AppImage and snap opened windows with Chromium's sandbox
  // off, and the release check accepted them (it even retried the AppImage
  // with --no-sandbox). Now every Linux package, x64 and arm64, is opened with
  // the sandbox checked, and the two cases that cannot build it on Ubuntu
  // 24.04 must refuse with the launcher's message.
  it.runIf(!!DIR)('opens every Linux package with the sandbox on, and checks the two refusals', () => {
    const release = code('release.yml');
    const check = job(release, 'desktop-arm64-check');
    expect(check).toMatch(/os: ubuntu-24\.04, label: linux-x64, artifact: desktop-x64-linux/);
    const keep = step(release, 'Keep the x64 Linux packages for the check');
    expect(cond(keep)).toBe("matrix.label == 'linux'");
    expect(keep).toMatch(/name: desktop-x64-linux/);
    for (const f of ['x86_64.AppImage', 'amd64.deb', 'amd64.snap']) expect(keep).toContain(`desktop/release/*${f}`);
    const linux = step(release, '.deb, AppImage and snap on Linux, sandboxed');
    expect(cond(linux)).toBe("startsWith(matrix.label, 'linux')");
    // Never --no-sandbox: not as a retry, not as an argument. The one line
    // that names it checks the AppImage's menu entry does NOT carry it.
    const mentions = check.split('\n').filter((l) => l.includes('--no-sandbox'));
    expect(mentions.map((l) => l.trim())).toEqual([
      `if grep -q -- '--no-sandbox' "$RUNNER_TEMP"/squashfs-root/*.desktop; then echo "the AppImage's menu entry turns the sandbox off" >&2; exit 1; fi`,
    ]);
    // Every opening is checked for the sandbox: the .deb, the AppImage (twice:
    // as itself and as its own menu entry), the snap.
    const opens = [...linux.matchAll(/^\s*look --exe .*$/gm)].map((m) => m[0]);
    expect(opens).toHaveLength(4);
    for (const o of opens) expect(o).toMatch(/--expect-sandbox$/);
    // The refusals run with no display, so no dialog waits for a click.
    expect(linux).toMatch(/refuses\(\) \{ env -u DISPLAY -u WAYLAND_DISPLAY node "\$CI_SCRIPTS"\/desktop-look\.mjs "\$@" --expect-refusal; \}/);
    const order = ['refuses --exe "$img"', 'apparmor_parser -r /etc/apparmor.d/filex-appimage', 'look --exe "$img" --out', 'refuses --exe /snap/bin/filex-app', 'snap connect filex-app:browser-sandbox', 'look --exe /snap/bin/filex-app'];
    const at = order.map((o) => linux.indexOf(o));
    for (const [i, a] of at.entries()) expect(a, order[i]).toBeGreaterThan(0);
    expect([...at].sort((a, b) => a - b)).toEqual(at);
    // The restriction is on whatever the runner image says.
    expect(linux).toMatch(/sudo sysctl -w kernel\.apparmor_restrict_unprivileged_userns=1/);
    // The AppImage's own menu entry carries the harmless switch, not --no-sandbox.
    expect(linux).toMatch(/grep -qx 'Exec=AppRun --filex-desktop-entry %U'/);
    expect(check).toMatch(/node "\$CI_SCRIPTS"\/arch-of\.mjs \/opt\/filex\/filex-app-bin/);
  });

  it.runIf(!!DIR)('installs the AppArmor profile docs/DESKTOP.md gives, word for word', () => {
    const linux = step(code('release.yml'), '.deb, AppImage and snap on Linux, sandboxed');
    const profile = (text: string) => /profile filex-appimage [^\n]+\{\n\s*userns,\n\s*include if exists <local\/filex-appimage>\n\s*\}/.exec(text)?.[0].replace(/^\s+/gm, '');
    const desktop = fs.readFileSync(path.join(REPO, 'docs', 'DESKTOP.md'), 'utf8');
    const launcher = fs.readFileSync(path.join(REPO, 'desktop', 'build', 'linux', 'launcher.sh'), 'utf8').replace(/^\s*"\s*|"\s*\\?$/gm, '');
    expect(profile(linux)).toBeTruthy();
    expect(profile(desktop)).toBe(profile(linux));
    expect(profile(launcher)).toBe(profile(linux));
  });

  it.runIf(!!DIR)('a snap waiting for the Snap Store review does not fail the release', () => {
    const s = step(code('release.yml'), 'Upload to the Snap Store');
    expect(s).toMatch(/out=\$\(snapcraft upload --release=stable "\$snap" 2>&1\)/);
    expect(s).toMatch(/grep -qi 'manual review'/);
    expect(s).toMatch(/::warning title=Snap Store: manual review::/);
    // Anything else still fails the step.
    expect(s).toMatch(/exit "\$rc"/);
  });

  it.runIf(!!DIR)('names the arm64 installer in the winget manifest', () => {
    const s = step(code('release.yml'), 'Package-manager manifests');
    expect(s).toMatch(/--windows desktop\/release\/filex-desktop-x64\.exe \\\n\s+--windows-arm64 desktop\/release\/filex-desktop-arm64\.exe/);
  });
});

describe('a release run started by hand', () => {
  // Every command that makes something public, and the step it lives in.
  const PUBLISHING = [
    /gh release upload/,
    /snapcraft upload/,
    /msstore-submit\.ps1/,
    /wingetcreate\.exe submit/,
    /winget-cla\.sh/,
    /winget-supersede\.sh/,
    /git push origin HEAD/,
  ];

  it.runIf(!!DIR)('a dry run publishes nothing', () => {
    const release = code('release.yml');
    const offenders: string[] = [];
    // Steps are cut at every `- name:`/`- uses:`; each publishing one must be
    // switched by `publish` itself or sit in a job that is.
    const jobs = [...release.matchAll(/^ {2}([A-Za-z0-9_-]+):\s*$/gm)].map((m) => m[1]).filter((j) => j !== 'on');
    for (const j of jobs) {
      let text: string;
      try {
        text = job(release, j);
      } catch {
        continue;
      }
      const jobIf = /\n {4}if:\s*(.+)/.exec(text)?.[1] ?? '';
      const jobPublishes = /outputs\.publish == 'true'/.test(jobIf);
      const steps = text.split(/\n(?=\s+- (?:name|uses):)/);
      for (const s of steps) {
        if (!PUBLISHING.some((re) => re.test(s))) continue;
        const c = cond(s);
        if (!jobPublishes && !/env\.PUBLISH == 'true'/.test(c)) offenders.push(`${j}: ${s.trim().split('\n')[0]}`);
      }
    }
    expect(offenders).toEqual([]);
    // The two that publish through an argument rather than a step.
    expect(release).toMatch(/push=\$\{\{ env\.PUBLISH == 'true' \}\}/);
    expect(release).not.toMatch(/push=true/);
    expect(release).toMatch(/args: \$\{\{ env\.PUBLISH == 'true' && 'release --clean --release-notes=\/tmp\/release-notes\.md' \|\| 'release --snapshot --clean --skip=publish' \}\}/);
    // A dry run has no tag of its own, and the commit also carries the Go
    // module's backend/vX tag: goreleaser picked that one and --snapshot
    // failed ("current tag is not semver", dry run 36372592649).
    expect(release).toMatch(/GORELEASER_CURRENT_TAG: v\$\{\{ needs\.plan\.outputs\.version \}\}/);
    // And jobs that only ever publish.
    for (const j of ['docker-manifest', 'npm', 'aur']) {
      expect(job(release, j), j).toMatch(/\n {4}if: needs\.plan\.outputs\.publish == 'true'/);
    }
  });

  it.runIf(!!DIR)('a full release publishes from its tag push only', () => {
    const plan = job(code('release.yml'), 'plan');
    expect(plan).toMatch(/if \[ "\$EVENT" = push \]; then\s+tag="\$GITHUB_REF_NAME"; only=all; publish=true/);
    expect(plan).toMatch(/\[ "\$only" = arm64 \] \|\| \{ echo "::error::a full release publishes from its tag push only/);
    expect(plan).toMatch(/only=arm64 adds to a release: give its tag/);
    // The publish input is off unless someone turns it on.
    const on = code('release.yml').slice(0, code('release.yml').indexOf('\njobs:'));
    expect(on).toMatch(/publish:\s*\n\s+description:[^\n]*\n\s+type: boolean\s*\n\s+default: false/);
    // A typed-in tag never reaches a shell as script text: every read of an
    // input is an env mapping, the ref a checkout is given, or the
    // concurrency group (which never cancels: a hand-started run cannot stop
    // a release in flight).
    const text = code('release.yml');
    const reads = text.split('\n').filter((l) => /\$\{\{[^}]*\binputs\./.test(l));
    expect(reads.length).toBeGreaterThan(0);
    for (const l of reads) {
      expect(l, 'an input read outside env').toMatch(
        /^\s+(?:(?:[A-Z_]+|ref): \$\{\{ inputs\.[a-z_]+(?: \|\| github\.sha)? \}\}|group: release-\$\{\{ github\.ref \}\}-\$\{\{ inputs\.tag \}\})\s*$/,
      );
    }
    expect(text).toMatch(/\nconcurrency:\n\s+group: release-\$\{\{ github\.ref \}\}-\$\{\{ inputs\.tag \}\}\n\s+cancel-in-progress: false\n/);
  });
});

describe('winget keeps only the newest pull request open', () => {
  it.runIf(!!DIR)('closes superseded winget pull requests after the new one, for the CLI and the desktop app', () => {
    const release = code('release.yml');
    const cli = step(release, 'Did the CLI reach winget and Homebrew?');
    expect(cli.indexOf('winget-supersede.sh BRFTech.filex "${VER}"')).toBeGreaterThan(cli.indexOf('winget-cla.sh'));
    const submit = step(release, 'Submit to winget');
    const close = step(release, 'Close the superseded winget pull requests');
    expect(close).toMatch(/winget-supersede\.sh BRFTech\.filex-app "\$\{VER\}"/);
    expect(cond(close)).toBe(cond(submit));
    expect(release.indexOf('- name: Close the superseded winget pull requests')).toBeGreaterThan(
      release.indexOf('- name: Sign the CLA on the winget pull request'),
    );
    const script = fs.readFileSync(path.join(DIR!, 'scripts', 'winget-supersede.sh'), 'utf8');
    expect(script).not.toMatch(/^\s*set\s+-[a-z]*e/m);
    expect(script).not.toMatch(/\bexit\s+[1-9]/);
  });

  const bash = findBash();

  /** Runs winget-supersede.sh against a fake `gh`; returns what it closed. */
  function supersede(id: string, ver: string, open: Array<[number, string]>) {
    const tmp = fs.mkdtempSync(path.join(os.tmpdir(), 'winget-supersede-'));
    try {
      const log = path.join(tmp, 'closed.log');
      fs.writeFileSync(path.join(tmp, 'open.tsv'), open.map(([n, t]) => `${n}\t${t}\n`).join(''));
      fs.writeFileSync(
        path.join(tmp, 'gh'),
        [
          '#!/usr/bin/env bash',
          'dir="$(cd "$(dirname "$0")" && pwd)"',
          'if [ "$1 $2" = "pr close" ]; then echo "$3 $*" >> "$dir/closed.log"; exit 0; fi',
          'for a in "$@"; do',
          '  case "$a" in',
          '    \\"New\\ version:*) want="${a#\\"}"; want="${want%%\\" in:title}";',
          '      while IFS=$\'\\t\' read -r n t; do [ "$t" = "$want" ] && echo "$n"; done < "$dir/open.tsv"; exit 0 ;;',
          '  esac',
          'done',
          'cat "$dir/open.tsv"',
          '',
        ].join('\n'),
      );
      fs.chmodSync(path.join(tmp, 'gh'), 0o755);
      const r = spawnSync(bash!, [path.join(DIR!, 'scripts', 'winget-supersede.sh'), id, ver], {
        encoding: 'utf8',
        env: { ...process.env, PATH: `${tmp}${path.delimiter}${process.env.PATH}`, WINGET_SUPERSEDE_WAIT: '0', WINGET_SUPERSEDE_TRIES: '1' },
      });
      const closed = fs.existsSync(log)
        ? fs.readFileSync(log, 'utf8').split('\n').filter(Boolean).map((l) => Number(l.split(' ')[0]))
        : [];
      const comments = fs.existsSync(log) ? fs.readFileSync(log, 'utf8') : '';
      return { code: r.status, out: `${r.stdout}${r.stderr}`, closed: closed.sort(), comments };
    } finally {
      fs.rmSync(tmp, { recursive: true, force: true });
    }
  }

  it.runIf(!!DIR && !!bash)('closes only older versions of the same package, and nothing when the new pull request is missing', () => {
    const open: Array<[number, string]> = [
      [441070, 'New package: BRFTech.filex-app version 0.44.2'],
      [441715, 'New version: BRFTech.filex-app 0.46.1'],
      [441927, 'New version: BRFTech.filex-app 0.47.0'],
      [441924, 'New version: BRFTech.filex 0.47.0'],
      [442100, 'New version: BRFTech.filex-app 0.48.1'],
      [442200, 'New version: BRFTech.filex-app 0.49.0'],
      [442300, 'New version: BRFTech.filex-app 0.48.1-rc.1'],
    ];
    const r = supersede('BRFTech.filex-app', '0.48.1', open);
    expect(r.code, r.out).toBe(0);
    // Older: 0.44.2 (the "New package" title), 0.46.1, 0.47.0, and 0.48.1's
    // own release candidate. Not the CLI's 0.47.0 (BRFTech.filex is another
    // package, though its name is a prefix of this one), not 0.49.0, not itself.
    expect(r.closed).toEqual([441070, 441715, 441927, 442300]);
    expect(r.comments).toMatch(/--comment Superseded by #442100/);

    // No new pull request (the submission failed): nothing is closed.
    const none = supersede('BRFTech.filex-app', '0.48.2', open);
    expect(none.code).toBe(0);
    expect(none.closed).toEqual([]);
    expect(none.out).toMatch(/::warning title=winget::no open pull request titled "New version: BRFTech\.filex-app 0\.48\.2"/);
  });

  it.runIf(!!DIR && !!bash)('compares winget versions as semver', () => {
    const lt = (a: string, b: string) =>
      spawnSync(bash!, [path.join(DIR!, 'scripts', 'winget-supersede.sh'), '--semver-lt', a, b]).status === 0;
    expect(lt('0.47.0', '0.48.0')).toBe(true);
    expect(lt('0.9.0', '0.10.0')).toBe(true);
    expect(lt('0.48.0', '0.48.0')).toBe(false);
    expect(lt('0.48.1', '0.48.0')).toBe(false);
    expect(lt('0.48.0-rc.1', '0.48.0')).toBe(true);
    expect(lt('0.48.0', '0.48.0-rc.1')).toBe(false);
    expect(lt('0.48.0-rc.2', '0.48.0-rc.10')).toBe(true);
    expect(lt('1.0.0', '0.99.99')).toBe(false);
  });
});

describe('the helper scripts the release runs', () => {
  const X64 = `version: 0.48.1
files:
  - url: filex-desktop-x64.exe
    sha512: AAA=
    size: 100
path: filex-desktop-x64.exe
sha512: AAA=
releaseDate: '2026-09-28T10:00:00.000Z'
`;
  const ARM = `version: 0.48.1
files:
  - url: filex-desktop-arm64.exe
    sha512: BBB=
    size: 90
path: filex-desktop-arm64.exe
sha512: BBB=
releaseDate: '2026-09-28T10:05:00.000Z'
`;

  it.runIf(!!DIR)('joins the Windows feeds with x64 first, and keeps what an old x64 install reads', () => {
    const r = inNode<{
      both: Array<{ version: string; urls: string[]; path: string; sha512: string }>;
      alone: string;
      rebuilt: { n: number; sha: string };
    }>(
      'merge-latest-yml.mjs',
      `const X64 = ${JSON.stringify(X64)}, ARM = ${JSON.stringify(ARM)};
       const both = [[X64, ARM], [ARM, X64]].map((order) => {
         const out = m.parseFeed(m.renderFeed(m.mergeFeeds(order.map(m.parseFeed))));
         return { version: out.version, urls: out.files.map((f) => f.url), path: out.path, sha512: out.sha512 };
       });
       const alone = m.renderFeed(m.mergeFeeds([m.parseFeed(X64)]));
       const twice = m.mergeFeeds([X64, ARM, ARM.replace(/BBB=/g, 'CCC=')].map(m.parseFeed));
       return { both, alone, rebuilt: { n: twice.files.length, sha: twice.files[1].sha512 } };`,
    );
    for (const out of r.both) {
      expect(out.version).toBe('0.48.1');
      expect(out.urls).toEqual(['filex-desktop-x64.exe', 'filex-desktop-arm64.exe']);
      // The top-level fields the oldest updaters read name the x64 installer.
      expect(out.path).toBe('filex-desktop-x64.exe');
      expect(out.sha512).toBe('AAA=');
    }
    // Unchanged for an x64-only release: the feed is what electron-builder wrote.
    expect(r.alone).toBe(X64);
    // A rebuilt arm64 installer replaces its entry instead of adding a second.
    expect(r.rebuilt).toEqual({ n: 2, sha: 'CCC=' });
  });

  it.runIf(!!DIR)('refuses a Windows feed without x64 or with two versions', () => {
    const errors = inNode<string[]>(
      'merge-latest-yml.mjs',
      `const X64 = ${JSON.stringify(X64)}, ARM = ${JSON.stringify(ARM)};
       const tries = [
         () => m.mergeFeeds([m.parseFeed(ARM)]),
         () => m.mergeFeeds([m.parseFeed(X64), m.parseFeed(ARM.replace('0.48.1', '0.48.0'))]),
         () => m.parseFeed('version: 1.0.0\\nfiles:\\n  - url: a.exe\\n    sha512: x\\nstrange line\\n'),
       ];
       return tries.map((t) => { try { t(); return 'no error'; } catch (e) { return String(e.message); } });`,
    );
    expect(errors[0]).toMatch(/no x64 installer/);
    expect(errors[1]).toMatch(/different versions/);
    expect(errors[2]).toMatch(/unexpected line/);
  });

  it.runIf(!!DIR)("tells a sandboxed app from one running without it, from /proc", () => {
    const tmp = fs.mkdtempSync(path.join(os.tmpdir(), 'look-proc-'));
    try {
      const proc = (pid: number, argv: string[], nspid: string, joined = false) => {
        fs.mkdirSync(path.join(tmp, String(pid)));
        // Chromium rewrites a zygote child's title: ONE string, words joined by spaces.
        fs.writeFileSync(path.join(tmp, String(pid), 'cmdline'), joined ? argv.join(' ') + '\0' : argv.join('\0') + '\0');
        fs.writeFileSync(path.join(tmp, String(pid), 'status'), `Name:\tx\nNSpid:\t${nspid}\n`);
      };
      const bin = '/opt/filex/filex-app-bin';
      proc(10, [bin, '--remote-debugging-port=9333'], '10');
      proc(11, [bin, '--type=zygote'], '11');
      proc(12, [bin, '--type=renderer', '--lang=en'], '12 4', true);
      proc(13, ['/usr/bin/other', '--type=renderer'], '13');
      const sandboxed = inNode<{ types: string[]; problems: string[] }>(
        'desktop-look.mjs',
        `const ps = m.appProcesses(${JSON.stringify(tmp)}); return { types: ps.map((p) => p.type).sort(), problems: m.sandboxProblems(ps) };`,
      );
      expect(sandboxed.types).toEqual(['browser', 'renderer', 'zygote']);
      expect(sandboxed.problems).toEqual([]);
      // The same app with --no-sandbox: the renderer shares the browser's PID namespace.
      fs.rmSync(tmp, { recursive: true, force: true });
      fs.mkdirSync(tmp);
      proc(20, [bin, '--no-sandbox'], '20');
      proc(21, [bin, '--type=renderer', '--no-sandbox'], '21', true);
      const open = inNode<string[]>('desktop-look.mjs', `return m.sandboxProblems(m.appProcesses(${JSON.stringify(tmp)}));`);
      expect(open.join('\n')).toMatch(/pid 20 \(browser\) runs with --no-sandbox/);
      expect(open.join('\n')).toMatch(/renderer 21 shares the browser's PID namespace/);
      // Nothing of the app at all.
      fs.rmSync(tmp, { recursive: true, force: true });
      fs.mkdirSync(tmp);
      expect(inNode<string[]>('desktop-look.mjs', `return m.sandboxProblems(m.appProcesses(${JSON.stringify(tmp)}));`)).toEqual([
        'no browser process of filex-app-bin found',
        'no renderer process found',
      ]);
    } finally {
      fs.rmSync(tmp, { recursive: true, force: true });
    }
  });

  it.runIf(!!DIR)('reads the architecture from ELF, PE and Mach-O headers', () => {
    const tmp = fs.mkdtempSync(path.join(os.tmpdir(), 'arch-of-'));
    try {
      const file = (name: string, bytes: Buffer) => {
        const p = path.join(tmp, name);
        fs.writeFileSync(p, bytes);
        return p;
      };
      const elf = (machine: number) => {
        const b = Buffer.alloc(64);
        b.writeUInt32BE(0x7f454c46, 0);
        b[4] = 2;
        b[5] = 1;
        b.writeUInt16LE(machine, 18);
        return b;
      };
      const pe = (machine: number) => {
        const b = Buffer.alloc(0x100);
        b.write('MZ', 0, 'latin1');
        b.writeUInt32LE(0x80, 0x3c);
        b.writeUInt32BE(0x50450000, 0x80);
        b.writeUInt16LE(machine, 0x84);
        return b;
      };
      const macho = (cpu: number) => {
        const b = Buffer.alloc(32);
        b.writeUInt32LE(0xfeedfacf, 0);
        b.writeUInt32LE(cpu, 4);
        return b;
      };
      const files = {
        elfArm: file('a', elf(0xb7)),
        elfX64: file('b', elf(0x3e)),
        peArm: file('c.exe', pe(0xaa64)),
        peX64: file('d.exe', pe(0x8664)),
        machoArm: file('e', macho(0x0100000c)),
        machoX64: file('f', macho(0x01000007)),
        script: file('g', Buffer.from('#!/bin/sh\n')),
      };
      const r = inNode<Record<string, string>>(
        'arch-of.mjs',
        `const files = ${JSON.stringify(files)};
         const out = Object.fromEntries(Object.entries(files).map(([k, f]) => [k, m.archOf(f)]));
         out.x64 = m.normalizeArch('x64');
         out.aarch64 = m.normalizeArch('aarch64');
         try { m.normalizeArch('sparc'); out.sparc = 'accepted'; } catch { out.sparc = 'refused'; }
         return out;`,
      );
      expect(r).toMatchObject({
        elfArm: 'arm64', elfX64: 'amd64', peArm: 'arm64', peX64: 'amd64', machoArm: 'arm64', machoX64: 'amd64',
        x64: 'amd64', aarch64: 'arm64', sparc: 'refused',
      });
      expect(r.script).toMatch(/^unknown/);
    } finally {
      fs.rmSync(tmp, { recursive: true, force: true });
    }
  });
});
