// The nightly builds on GitHub's runners (tasks #266, #361;
// .github/workflows/nightly-desktop.yml): once ci.yml's full matrix is green
// on the `nightly` branch - the commit the build host pushes there every
// night - the nightly image and every desktop package (Windows x64 and arm64,
// macOS, Linux x64 and arm64) and both snaps are built from that commit; the
// image to ghcr as :nightly, the packages to ghcr as one OCI artifact, the
// snaps to edge alone.
//
// ⚠ Why (the owner, 2026-10-15, and the owner and the maintainer's
// coordinator, 2026-11-18): the desktop app had no nightly but a weekly
// Linux/Windows one on the build host, which cannot make macOS packages or an
// arm64 snap; "every night, Windows, Linux and macOS, on GitHub's runners,
// kept on ghcr, shown on filex.sh/nightly; no GitHub prerelease". Since #361
// the build host does not build at night at all: it pushes the commit, CI
// tests it, and this workflow builds from a green run. What these hold is
// what keeps a nightly from looking like a release - nothing signed, nothing
// on a Release, a store's stable channel or a package manager - from building
// what CI did not pass, from building a pull request's branch that happens to
// be called `nightly`, and from building the same commit twice.
//
// The workflow lives in the PUBLIC checkout only; the change reaches it as
// packaging/ci/nightly-desktop.patch and packaging/ci/nightly-on-github.patch,
// and the release tool names these workflow tests in its guards.
import { spawnSync } from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { describe, expect, it } from 'vitest';

import { DIR, REPO, bash, job, jobIf, scriptOf, stepNamed, stepsOf, topLevelTerms } from '../helpers/releaseWorkflow';

const FILE = 'nightly-desktop.yml';
/** The workflow without comment-only lines, LF. */
const wf = () =>
  fs
    .readFileSync(path.join(DIR!, FILE), 'utf8')
    .split(/\r?\n/)
    .filter((l) => !/^\s*#/.test(l))
    .join('\n');

const hasJq = !!bash && spawnSync(bash, ['-c', 'command -v jq'], { encoding: 'utf8' }).status === 0;
const fwd = (p: string) => p.split(path.sep).join('/');
const FIXED = { GIT_AUTHOR_DATE: '2026-11-18T23:00:00Z', GIT_COMMITTER_DATE: '2026-11-18T23:00:00Z' };
const git = (cwd: string, args: string[], env: Record<string, string> = {}) => {
  const r = spawnSync('git', ['-c', 'user.name=t', '-c', 'user.email=t@example.com', '-C', cwd, ...args], { encoding: 'utf8', env: { ...process.env, ...FIXED, ...env } });
  if (r.status !== 0) throw new Error(`git ${args.join(' ')}: ${r.stderr}`);
  return r.stdout.trim();
};

/**
 * Runs the source job's script in a directory with `src` as the checkout
 * actions/checkout leaves - a real repository whose `origin` is itself, so
 * `ls-remote` answers - and a fake `oras` and `gh`: `manifests` maps a ref to
 * the annotations its manifest carries (none: not found). `tip` moves the
 * branch first ('parent': one commit back). Returns its output, what it wrote
 * to $GITHUB_OUTPUT, the labels it wrote, every oras call in order, and the
 * two commits.
 */
function source(manifests: Record<string, Record<string, string>>, { env = {}, tip = 'head', trailer = true }: { env?: Record<string, string>; tip?: 'head' | 'parent'; trailer?: boolean } = {}) {
  const tmp = fs.mkdtempSync(path.join(os.tmpdir(), 'nightly-source-'));
  try {
    const src = path.join(tmp, 'src');
    fs.mkdirSync(path.join(src, 'packages', 'core'), { recursive: true });
    fs.mkdirSync(path.join(src, 'scripts', 'chain'), { recursive: true });
    fs.writeFileSync(path.join(src, 'packages', 'core', 'package.json'), '{ "version": "0.61.1" }\n');
    for (const f of ['nightly-meta.mjs', 'nightly-lib.mjs']) fs.copyFileSync(path.join(REPO, 'scripts', 'chain', f), path.join(src, 'scripts', 'chain', f));
    git(tmp, ['init', '-q', '-b', 'main', 'src']);
    git(src, ['add', '-A']);
    git(src, ['commit', '-q', '-m', 'v0.61.1 - the public main']);
    const base = git(src, ['rev-parse', 'HEAD']);
    fs.writeFileSync(path.join(src, 'NIGHTLY.txt'), 'tonight\n');
    git(src, ['add', '-A']);
    const msg = trailer ? 'nightly 2026-11-19: the tip of main\n\nNightly-Date: 2026-11-19\n' : 'a commit pushed by hand';
    git(src, ['commit', '-q', '-m', msg], { GIT_COMMITTER_DATE: '2026-11-18T23:00:00Z' });
    const rev = git(src, ['rev-parse', 'HEAD']);
    git(src, ['branch', '-q', 'nightly', tip === 'head' ? rev : base]);
    git(src, ['remote', 'add', 'origin', fwd(src)]);

    const bin = path.join(tmp, 'bin');
    const store = path.join(tmp, 'manifests');
    fs.mkdirSync(bin);
    fs.mkdirSync(store);
    const key = (ref: string) => ref.replace(/[/:@]/g, '_');
    for (const [ref, annotations] of Object.entries(manifests)) fs.writeFileSync(path.join(store, key(ref)), JSON.stringify({ annotations }));
    fs.writeFileSync(
      path.join(bin, 'oras'),
      [
        '#!/usr/bin/env bash',
        `dir="${fwd(tmp)}"`,
        'echo "oras $*" >> "$dir/calls.log"',
        'case "$1" in',
        '  login) cat > /dev/null; exit 0 ;;',
        '  manifest) f="$dir/manifests/$(printf %s "$3" | tr "/:@" "___")"; [ -f "$f" ] && cat "$f" && exit 0; echo "not found" >&2; exit 1 ;;',
        '  push) exit 0 ;;',
        'esac',
        'exit 1',
        '',
      ].join('\n'),
    );
    fs.writeFileSync(
      path.join(bin, 'gh'),
      [
        '#!/usr/bin/env bash',
        `echo "gh $*" >> "${fwd(tmp)}/calls.log"`,
        `echo '{"name":"Go vet + build","conclusion":"success"}'`,
        `echo '{"name":"Playwright (webkit 1/4)","conclusion":"skipped"}'`,
        `echo '{"name":"All tests (full)","conclusion":"success"}'`,
        '',
      ].join('\n'),
    );
    fs.chmodSync(path.join(bin, 'oras'), 0o755);
    fs.chmodSync(path.join(bin, 'gh'), 0o755);
    const script = path.join(tmp, 'step.sh');
    fs.writeFileSync(script, scriptOf(stepNamed(job(wf(), 'source'), 'The commit and the last packages').text));
    const out = path.join(tmp, 'out');
    const r = spawnSync(bash!, [fwd(script)], {
      cwd: tmp,
      encoding: 'utf8',
      env: {
        ...process.env,
        PATH: `${bin}${path.delimiter}${path.dirname(process.execPath)}${path.delimiter}${process.env.PATH}`,
        GITHUB_OUTPUT: fwd(out),
        GITHUB_REPOSITORY_OWNER: 'Acme',
        GITHUB_REPOSITORY: 'Acme/filex',
        GITHUB_ACTOR: 'alice',
        GH_TOKEN: 't',
        FORCE: '',
        CI_RUN: '',
        CI_URL: '',
        BRANCH: 'nightly',
        ...env,
      },
    });
    const read = (f: string) => (fs.existsSync(f) ? fs.readFileSync(f, 'utf8') : '');
    return {
      code: r.status,
      out: `${r.stdout}${r.stderr}`,
      output: read(out),
      labels: read(path.join(tmp, 'labels.txt')),
      tarball: fs.existsSync(path.join(tmp, 'filex-nightly-src.tar.gz')),
      calls: read(path.join(tmp, 'calls.log')).split('\n').filter(Boolean),
      base,
      rev,
    };
  } finally {
    fs.rmSync(tmp, { recursive: true, force: true });
  }
}

const OUT = 'ghcr.io/acme/filex-desktop-nightly';

describe('the nightly builds on GitHub (#266, #361)', () => {
  it.runIf(!!DIR)('run once a CI run on the nightly branch ended, and by hand; build only from a green push of this repository, and publish nothing a release does', () => {
    const text = wf();
    const on = text.slice(0, text.indexOf('\njobs:'));
    expect(on).toMatch(/workflow_run:\s*\n\s+workflows: \[CI\]\s*\n\s+types: \[completed\]\s*\n\s+branches: \[nightly\]/);
    expect(on).not.toMatch(/schedule:/);
    expect(on).toMatch(/force:\s*\n\s+description:[^\n]*\n\s+type: boolean\s*\n\s+default: false/);
    expect(on).toMatch(/\npermissions:\s*\n\s+contents: read\s*\n\s+packages: write\s*\n\s+actions: read\s*\n/);
    expect(on).toMatch(/cancel-in-progress: false/);
    // A pull request's CI run may name `nightly` as its head branch: only a
    // green run of a push in this repository builds.
    expect(jobIf(job(text, 'source'))).toBe(
      "github.event_name == 'workflow_dispatch' || (github.event.workflow_run.conclusion == 'success' && github.event.workflow_run.event == 'push' && github.event.workflow_run.head_repository.full_name == github.repository)",
    );
    // Everything after it waits for it.
    for (const j of ['image', 'desktop', 'publish']) expect(jobIf(job(text, j)), j).toMatch(/needs\.source\.outputs\.build == 'true'/);
    // Never a Release, a store's stable channel, a package manager, a signature.
    expect(text).not.toMatch(/gh release|prerelease|--release=(?:stable|candidate|beta)|wingetcreate|msstore|pnpm publish|git push|CSC_LINK|WIN_CSC/);
    // An input reaches a script through env alone.
    for (const l of text.split('\n').filter((x) => /\$\{\{[^}]*\binputs\./.test(x))) expect(l).toMatch(/^\s+[A-Z_]+: \$\{\{ inputs\.[a-z_]+ \}\}\s*$/);
    // So does what the triggering run says.
    for (const l of text.split('\n').filter((x) => /\bgithub\.event\.workflow_run\.(?:html_url|id)\b/.test(x))) expect(l).toMatch(/^\s+[A-Z_]+: \$\{\{ github\.event\.workflow_run\.(?:html_url|id) \}\}\s*$/);
  });

  it.runIf(!!DIR)('check the commit of the CI run out, never with a credential left in it', () => {
    const steps = stepsOf(job(wf(), 'source'));
    const co = steps.find((s) => /actions\/checkout@/.test(s.text));
    expect(co?.text).toMatch(/ref: \$\{\{ github\.event\.workflow_run\.head_sha \|\| env\.BRANCH \}\}/);
    expect(co?.text).toMatch(/persist-credentials: false/);
    expect(co?.text).toMatch(/path: src/);
  });

  it.runIf(!!DIR && hasJq)('build a new commit once: never again what was built or tried, never a commit a newer night replaced, unless forced', () => {
    const fresh = source({ [`${OUT}:nightly`]: { 'org.opencontainers.image.revision': 'old' } }, { env: { CI_RUN: '42', CI_URL: 'https://github.com/Acme/filex/actions/runs/42' } });
    expect(fresh.code, fresh.out).toBe(0);
    const v = `0.62.0-nightly.20261119+${fresh.rev.slice(0, 8)}`;
    expect(fresh.output).toBe(
      ['build=true', `base=${fresh.base}`, `revision=${fresh.rev}`, `version=${v}`, `desktop_version=${v}`, 'date=2026-11-19', 'ymd=20261119', 'tags=nightly,nightly-20261119', ''].join('\n'),
    );
    expect(fresh.tarball).toBe(true);
    // The image's labels: the CI run's jobs counted, the run named.
    expect(fresh.labels).toMatch(/^sh\.filex\.nightly\.tests=green: 2 passed, 1 skipped of 3 jobs \(https:\/\/github\.com\/Acme\/filex\/actions\/runs\/42\)$/m);
    expect(fresh.labels).toMatch(/^org\.opencontainers\.image\.source=https:\/\/github\.com\/BRF-Tech\/filex$/m);
    expect(fresh.calls).toContain('gh api --paginate repos/Acme/filex/actions/runs/42/jobs?filter=latest&per_page=100 --jq .jobs[] | {name, conclusion}');
    // The attempt is recorded after the commit was looked at and before anything is built.
    const looked = fresh.calls.findIndex((c) => c === `oras manifest fetch ${OUT}:attempted`);
    const attempt = fresh.calls.findIndex((c) => c.startsWith(`oras push ${OUT}:attempted --annotation org.opencontainers.image.revision=${fresh.rev} `));
    expect(looked).toBeGreaterThan(0);
    expect(attempt).toBeGreaterThan(looked);

    // Built or tried: nothing, unless forced. Every repository this makes
    // has the same commits (same trees, names and dates), so fresh.rev is
    // the commit of the next one too.
    for (const tag of ['nightly', 'attempted']) {
      const same = source({ [`${OUT}:${tag}`]: { 'org.opencontainers.image.revision': fresh.rev } });
      expect(same.rev).toBe(fresh.rev);
      expect(same.output, tag).toBe('build=false\n');
      expect(same.out).toMatch(/was built or tried/);
      expect(same.calls.filter((c) => c.startsWith('oras push'))).toEqual([]);
      expect(source({ [`${OUT}:${tag}`]: { 'org.opencontainers.image.revision': fresh.rev } }, { env: { FORCE: 'true' } }).output).toMatch(/^build=true\n/);
    }
    // A newer night was pushed: this run's commit is not the branch's tip.
    const stale = source({}, { tip: 'parent' });
    expect(stale.code).toBe(0);
    expect(stale.output).toBe('build=false\n');
    expect(stale.out).toMatch(/is not the tip of nightly/);
    expect(stale.calls.filter((c) => c.startsWith('oras push'))).toEqual([]);
    // By hand there is no CI run to count: the labels say nothing of tests.
    const byHand = source({});
    expect(byHand.labels).not.toMatch(/sh\.filex\.nightly\.tests/);
    expect(byHand.calls.filter((c) => c.startsWith('gh '))).toEqual([]);
  });

  it.runIf(!!DIR && hasJq)('name the night from the commit the build host made, and a commit pushed by hand from its own date', () => {
    const hand = source({}, { trailer: false });
    expect(hand.code, hand.out).toBe(0);
    expect(hand.output).toMatch(/^date=2026-11-18$/m);
    expect(hand.output).toMatch(new RegExp(`^version=0\\.62\\.0-nightly\\.20261118\\+${hand.rev.slice(0, 8)}$`, 'm'));
  });

  it.runIf(!!DIR)("build the image from the commit with the release's recipe, check what it reports, and push it as nightly-<date> and nightly", () => {
    const image = job(wf(), 'image');
    expect(jobIf(image)).toBe("needs.source.outputs.build == 'true'");
    expect(image).not.toMatch(/environment:/);
    const build = scriptOf(stepNamed(image, 'Build the image').text);
    expect(build).toMatch(/docker build --pull -f src\/docker\/Dockerfile/);
    expect(build).toMatch(/--build-arg "VERSION=\$VERSION" --build-arg "COMMIT=\$\{REVISION:0:8\}"/);
    for (const l of ['org.opencontainers.image.version=$VERSION', 'org.opencontainers.image.revision=$REVISION', 'sh.filex.nightly.public-base=$BASE']) expect(build).toContain(`--label "${l}"`);
    expect(build).toMatch(/done < labels\.txt/);
    expect(build).toMatch(/docker run --rm --entrypoint \/usr\/local\/bin\/filex filex-nightly:local --version/);
    const push = scriptOf(stepNamed(image, 'To ghcr.io, as nightly-<date> and nightly').text);
    expect(push).toMatch(/printf '%s' "\$GH_TOKEN" \| docker login ghcr\.io -u "\$GITHUB_ACTOR" --password-stdin/);
    expect(push).toMatch(/repo="ghcr\.io\/\$\{GITHUB_REPOSITORY_OWNER,,\}\/filex"/);
    expect(push).toMatch(/for tag in "nightly-\$YMD" nightly; do/);
    // The commit's own version, not the desktop one cut for the Snap Store.
    expect(image).toMatch(/VERSION: \$\{\{ needs\.source\.outputs\.image_version \}\}/);
  });

  it.runIf(!!DIR)('make every row with the nightly job script, unsigned, and pack both snaps for edge alone', () => {
    const desktop = job(wf(), 'desktop');
    expect(jobIf(desktop)).toBe("needs.source.outputs.build == 'true'");
    for (const row of [
      '{ os: ubuntu-latest, label: linux-x64, targets: "linux-x64,snap", snap: amd64, snap_target: snap, cpu: x64, unpacked: linux-unpacked }',
      '{ os: ubuntu-24.04-arm, label: linux-arm64, targets: "linux-arm64,snap-arm64", snap: arm64, snap_target: snap-arm64, cpu: arm64, unpacked: linux-arm64-unpacked }',
      '{ os: windows-latest, label: windows, targets: "windows" }',
      '{ os: macos-15, label: macos, targets: "mac" }',
    ]) {
      expect(desktop).toContain(row);
    }
    expect(desktop).toMatch(/fail-fast: false/);
    expect(desktop).toMatch(/\n {4}environment: release\n/);
    const pkg = stepNamed(desktop, 'The packages');
    expect(pkg.text).toMatch(/continue-on-error: true/);
    expect(pkg.text).toContain('run: ND_JOB_SRC="$PWD/src" ND_JOB_OUT="$PWD/out" bash src/scripts/chain/nightly-desktop-job.sh');
    expect(stepNamed(desktop, 'snapcraft + LXD for the snap').cond).toBe("matrix.snap != ''");
    const snap = stepNamed(desktop, 'The snap');
    expect(snap.cond).toBe("matrix.snap != ''");
    expect(scriptOf(snap.text)).toMatch(/grep -qx "\$SNAP_TARGET built" out\/targets\.txt/);
    expect(scriptOf(snap.text)).toMatch(/electron-builder --linux snap "--\$CPU" --publish never --prepackaged "release\/\$UNPACKED"/);
    const edge = stepNamed(desktop, 'The snap to edge');
    expect(topLevelTerms(edge.cond)).toEqual(expect.arrayContaining(["matrix.snap != ''", "steps.snap.outcome == 'success'"]));
    expect(edge.text).toMatch(/SNAPCRAFT_STORE_CREDENTIALS: \$\{\{ secrets\.SNAPCRAFT_STORE_CREDENTIALS \}\}/);
    expect(edge.text).toMatch(/run: node src\/scripts\/chain\/nightly-snap-edge\.mjs "out\/filex-desktop-\$SNAP\.snap" "out\/snap-\$SNAP\.json"/);
    const keep = stepNamed(desktop, 'Keep the packages');
    expect(stepsOf(desktop).filter((s) => /upload-artifact/.test(s.text)).length).toBe(1);
    expect(keep.cond).toBe('always()');
    expect(keep.text).toMatch(/name: nightly-desktop-\$\{\{ matrix\.label \}\}/);
    expect(keep.text).toMatch(/out\/snap-\*\.json/);
  });

  it.runIf(!!DIR)("send the night to ghcr as one artifact with its manifest, as nightly-<date> and nightly; old nights go without failing the run, a missing row fails it", () => {
    const publish = job(wf(), 'publish');
    expect(topLevelTerms(jobIf(publish))).toEqual(expect.arrayContaining(['!cancelled[]', "needs.source.outputs.build == 'true'"]));
    const manifest = scriptOf(stepNamed(publish, "The night's manifest").text);
    expect(manifest).toMatch(/node src\/scripts\/chain\/nightly-desktop-manifest\.mjs --dir out --version "\$VERSION" --revision "\$REVISION"/);
    expect(manifest).toMatch(/--expect linux-x64,snap,linux-arm64,snap-arm64,windows,mac/);
    const push = scriptOf(stepNamed(publish, 'To ghcr.io, as nightly-<date> and nightly').text);
    expect(push).toMatch(/files=\(nightly\.json:application\/json SHA256SUMS:text\/plain\)/);
    expect(push).toMatch(/oras push "\$repo:nightly-\$day"/);
    for (const a of ['org.opencontainers.image.revision=$REVISION', 'org.opencontainers.image.version=$VERSION', 'sh.filex.nightly.date=$DATE']) expect(push).toContain(`--annotation "${a}"`);
    expect(push).toMatch(/oras tag "\$repo:nightly-\$day" nightly/);
    const prune = stepNamed(publish, 'The nights before the last ones');
    expect(prune.text).toMatch(/continue-on-error: true/);
    expect(scriptOf(prune.text)).toMatch(/\(nightly\|attempted\)/);
    const red = scriptOf(stepNamed(publish, 'Red when a row failed').text);
    expect(red).toMatch(/jq -r '\.rows \| to_entries\[\] \| select\(\.value != "built"\) \| \.key' out\/nightly\.json/);
    expect(red).toMatch(/exit 1/);
  });

  it('the workflow patches are the documented ones: the workflow on top of the AUR one, and its move to the nightly branch on top of the server promote', () => {
    const patch = fs.readFileSync(path.join(REPO, 'packaging', 'ci', 'nightly-desktop.patch'), 'utf8');
    expect(patch).toMatch(/^diff --git a\/\.github\/workflows\/nightly-desktop\.yml b\/\.github\/workflows\/nightly-desktop\.yml\nnew file mode 100644$/m);
    expect(patch).not.toMatch(/^diff --git a\/\.github\/workflows\/(?:release|ci)\.yml/m);
    const moved = fs.readFileSync(path.join(REPO, 'packaging', 'ci', 'nightly-on-github.patch'), 'utf8');
    expect(moved.match(/^diff --git a\/\S+/gm)).toEqual(['diff --git a/.github/workflows/ci.yml', 'diff --git a/.github/workflows/nightly-desktop.yml']);
    expect(moved).toMatch(/^\+ {4}branches: \[main, nightly\]$/m);
    const contributing = fs.readFileSync(path.join(REPO, 'docs', 'CONTRIBUTING.md'), 'utf8');
    expect(contributing).toMatch(/\n11\. `nightly-desktop\.patch` \(#266, 0\.57\), on top of 10:/);
    expect(contributing).toMatch(/\n21\. `nightly-on-github\.patch` \(#361, 0\.62\), on top of 20:/);
  });
});
