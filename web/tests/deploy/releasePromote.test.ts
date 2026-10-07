// A tag run promotes what the dry run of its commit built; it builds nothing
// again (#174).
//
// ⚠⚠ Why (#165, measured on 0.51/0.52): the tag run built every image,
// binary and desktop package a second time - 30-50 minutes between a tag and
// its images, for bytes the dry run of the same commit had built and smoke-
// tested an hour earlier - and, until #174, every dry run also waited for a
// second copy of the test suite. Now:
//   - a dry run packages at once; the test is the push run of the commit,
//     and a tag run publishes only when that run was ci.yml's FULL matrix;
//   - the dry run of a version no tag has yet is a release candidate: it
//     stamps the version itself, pushes both images by digest with no tag
//     anybody pulls, and keeps each desktop row's files with their sums;
//   - the tag run checks each digest on its own architecture (built from its
//     commit, as its version, and `filex --version` says so) before tagging
//     it, and checks each desktop file byte for byte before publishing it;
//   - while GitHub has no macOS runner, a dry run without the macOS row is
//     allowed, and the release goes out without it (only=macos adds it);
//   - a commit CircleCI tested while GitHub Actions was down has no dry run
//     (#181): its tag run takes CircleCI's word and builds instead of
//     promoting - releaseVerifyCircleci.test.ts holds that side.
//
// The workflows live in the PUBLIC checkout only (see
// releaseGatesImages.test.ts), and the release tool names these titles in its
// workflow guards (scripts/release/plan.mjs, WORKFLOW_GUARDS), so a skip there
// is red. The change reaches the public checkout as its own commit, landed
// with the export that brings these guards (packaging/ci/release-promote.patch).
import { spawnSync } from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { describe, expect, it } from 'vitest';

import { completeName } from '../../../scripts/ci-parts.mjs';
import { DIR, KEYS, TAG_SHA, bash, code, desktopEnv, desktopRuns, job, jobIf, jobNames, jq, matrices, stepsOf, verify, verifyRun, verifyScript, type Row } from '../helpers/releaseWorkflow';

const hasJq = !spawnSync('jq', ['--version'], { encoding: 'utf8' }).error;

/** verify's env in a tag run of v1.2.3, the desktop rows given. */
const tagRun = (rows: Row[] = []) => ({ EVENT: 'push', SHA: TAG_SHA, ONLY: 'all', PUBLISH: 'true', TAG: 'v1.2.3', DESKTOP: JSON.stringify({ include: rows }) });

/** The full run's desktop rows, as plan writes them. */
const fullRows = () => matrices(job(code(), 'plan'))[1].rows;

/** The steps of the desktop job that BUILD something (a tag run runs none of them). */
const BUILDS = [
  /actions\/setup-go@/,
  /pnpm install --frozen-lockfile/,
  /pnpm -r --filter='\.\/packages\/\*' build/,
  /npm version "\$VER"/,
  /apt-get install -y -qq rpm/,
  /gem install --no-document fpm/,
  /pnpm --filter \.\/desktop run/,
  /electron-builder/,
  /fetch-cli\.mjs/,
  /merge-latest-yml\.mjs/,
  /--channel=8\.x\/stable/,
  /AllowDevelopmentWithoutDevLicense/,
  /makeappx/,
  /arch-of\.mjs/,
];
const builds = (text: string) => BUILDS.some((re) => re.test(text));

/** Runs release-files.mjs (from the public checkout) with `args`. */
function releaseFiles(...args: string[]) {
  const r = spawnSync(process.execPath, [path.join(DIR!, 'scripts', 'release-files.mjs'), ...args], { encoding: 'utf8' });
  return { code: r.status, out: `${r.stdout}${r.stderr}` };
}

describe('a tag run promotes what the dry run of its commit built', () => {
  it.runIf(!DIR)('has no workflows to read here (they live in the public checkout)', () => {
    // Not a pass: the private tree has no .github/workflows. The local release
    // run sets FILEX_WORKFLOWS_DIR so the cases below run there too.
    expect(DIR).toBeUndefined();
  });

  it.runIf(!!DIR && !!bash)("a tag run publishes only after ci.yml's full matrix passed on its commit", () => {
    // ci.yml passed, but not as the full matrix: nothing is published (and
    // CircleCI, red here, cannot stand in - releaseVerifyCircleci.test.ts).
    const notFull = verify(tagRun(), { ci: true, dry: true, full: false });
    expect(notFull.code).not.toBe(0);
    expect(notFull.out).toContain('not as the full matrix');
    expect(notFull.out).toMatch(/::error::v1\.2\.3 publishes nothing/);
    const jobs = notFull.calls.find((c) => c.includes('/actions/runs/1/jobs'));
    expect(jobs, 'verify reads the jobs of the ci.yml run it found').toBeTruthy();
    expect(jobs).toContain('-f filter=latest');
    // The last part's name is the one scripts/ci-parts.mjs gives ci.yml.
    expect(code()).toContain(`export COMPLETE="${completeName('full')}"`);
    // With all three, a run that adds to a release publishes and hands on its dry run.
    const arm = verify(verifyRun('arm64', true), { ci: true, dry: true });
    expect(arm.code, arm.out).toBe(0);
    expect(arm.outputs.dry_run).toBe('2');
    expect(arm.summary).toContain('the full matrix');
    expect(arm.calls.some((c) => c.includes('/artifacts')), 'a run that adds to a release promotes nothing').toBe(false);
  });

  it.runIf(!!DIR)('a release candidate is a dry run of everything on an untagged version: it stamps the version and pushes its images by digest only', () => {
    const release = code();
    const plan = job(release, 'plan');
    expect(plan).toContain('candidate: ${{ steps.p.outputs.candidate }}');
    expect(plan).toMatch(/if \[ "\$EVENT" != push \] && \[ -z "\$tag" \] && \[ "\$only" = all \] && \[ "\$publish" != true \]; then/);
    expect(plan).toContain('gh api "repos/$GITHUB_REPOSITORY/git/ref/tags/v$version" --silent');
    expect(plan).toMatch(/candidate=true\n\s+stamp="v\$version"/);
    expect(plan).toContain('echo "candidate=$candidate"');
    const docker = job(release, 'docker');
    // (#181: and in a tag run with no dry run to promote - releaseVerifyCircleci.test.ts)
    expect(jobIf(docker)).toBe("needs.plan.outputs.full == 'true' && (github.event_name != 'push' || needs.verify.outputs.promote == 'false')");
    expect(docker).toContain("PUSH: ${{ needs.plan.outputs.candidate == 'true' || needs.verify.outputs.promote == 'false' }}");
    const outputs = docker.split('\n').filter((l) => /outputs: type=image/.test(l));
    expect(outputs).toHaveLength(2);
    for (const l of outputs) {
      expect(l).toContain('push-by-digest=true');
      expect(l).toContain("push=${{ env.PUSH == 'true' }}");
    }
    expect(docker, 'a dry run tags nothing').not.toMatch(/\n\s+tags: ghcr\.io/);
    const labels = [...docker.matchAll(/org\.opencontainers\.image\.revision=\$\{\{ github\.sha \}\}\n\s+org\.opencontainers\.image\.version=\$\{\{ needs\.plan\.outputs\.stamp \}\}/g)];
    expect(labels, 'both pushed images say what they were built from').toHaveLength(2);
    const steps = stepsOf(docker);
    expect(steps.find((s) => s.name === 'Record digests')?.cond).toBe("env.PUSH == 'true'");
    const keep = steps.find((s) => /name: digests-\$\{\{ matrix\.arch \}\}/.test(s.text));
    expect(keep?.cond).toBe("env.PUSH == 'true'");
    expect(keep!.text).toContain('overwrite: true');
    expect(Number(/retention-days: (\d+)/.exec(keep!.text)?.[1]), 'kept until the tag run').toBeGreaterThanOrEqual(7);
  });

  it.runIf(!!DIR)("a tag run that promotes builds no image: it checks the dry run's digests on their own architecture, then tags them", () => {
    const release = code();
    expect(job(release, 'verify')).toContain('dry_run: ${{ steps.v.outputs.dry_run }}');
    expect(job(release, 'verify')).toContain('promote: ${{ steps.v.outputs.promote }}');
    const check = job(release, 'promote-check');
    // A tag run's; with nothing to promote (#181) it checks this run's own
    // digests the same way (releaseVerifyCircleci.test.ts).
    expect(jobIf(check)).toBe(
      "!cancelled() && needs.plan.outputs.full == 'true' && github.event_name == 'push' && needs.verify.result == 'success' && (needs.verify.outputs.promote == 'true' || needs.docker.result == 'success')",
    );
    expect(check).toMatch(/arch: amd64\n\s+runner: ubuntu-latest/);
    expect(check).toMatch(/arch: arm64\n\s+runner: ubuntu-24\.04-arm/);
    expect(check).toContain('name: digests-${{ matrix.arch }}');
    expect(check).toContain('run-id: ${{ needs.verify.outputs.dry_run || github.run_id }}');
    for (const must of ['[ "$rev" = "$GITHUB_SHA" ]', '[ "$ver" = "$TAG" ]', '[ "$arch" = "$ARCH" ]', 'docker run --rm --entrypoint /usr/local/bin/filex "$ref" --version', '*"$TAG"*"$GITHUB_SHA"*)']) {
      expect(check, must).toContain(must);
    }
    expect(check, 'a check builds and tags nothing').not.toMatch(/build-push-action|imagetools create/);
    const manifest = job(release, 'docker-manifest');
    expect(manifest).toContain('pattern: digests-*');
    expect(manifest).toContain('run-id: ${{ needs.verify.outputs.dry_run || github.run_id }}');
    expect(manifest).toContain('join full "latest" "${ver}" "full" "full-${ver}"');
    expect(manifest).toContain('join slim "slim" "slim-${ver}"');
    expect(manifest).not.toMatch(/build-push-action/);
    // No tag run that promotes builds an image: a job that builds one runs in
    // a tag run only when verify found no dry run to promote (#181).
    for (const n of jobNames(release)) {
      const b = job(release, n);
      if (/docker\/build-push-action/.test(b)) expect(jobIf(b), n).toContain("(github.event_name != 'push' || needs.verify.outputs.promote == 'false')");
    }
  });

  it.runIf(!!DIR)('a tag run that promotes builds no desktop package: it checks and publishes the files the dry run kept', () => {
    const release = code();
    const desktop = job(release, 'desktop');
    // verify's word, not the event: a tag run with no dry run builds (#181).
    expect(desktop).toContain("PROMOTE: ${{ needs.verify.outputs.promote == 'true' }}");
    expect(desktop).toContain('CANDIDATE: ${{ needs.plan.outputs.candidate }}');
    expect(desktop).toContain('matrix: ${{ fromJSON(needs.verify.outputs.desktop || needs.plan.outputs.desktop) }}');
    const steps = stepsOf(desktop);
    const building = steps.filter((s) => builds(s.text));
    expect(building.length, 'the markers still find the build steps').toBeGreaterThanOrEqual(15);
    for (const s of building) {
      // (only=snap-arm64 never runs on a push: plan sets only=all there)
      expect(s.cond, `${s.name} builds in a tag run`).toMatch(/env\.PROMOTE != 'true'|env\.ONLY == 'snap-arm64'/);
    }
    for (const row of fullRows()) {
      const promoted = desktopRuns(release, { ...desktopEnv({ publish: true, full: true, only: 'all' }, row, KEYS), PROMOTE: 'true' }, row);
      expect(promoted.steps.filter((s) => builds(s.text)).map((s) => s.name), `${row.label}: built in a tag run`).toEqual([]);
      const download = promoted.runs.indexOf('The files the dry run built');
      const sums = promoted.runs.indexOf('They are the files the dry run kept, for this version');
      expect(download, row.label).toBeGreaterThanOrEqual(0);
      expect(sums, row.label).toBeGreaterThan(download);
      for (const p of promoted.publishing) expect(promoted.runs.indexOf(p), `${row.label}: ${p} before the files were checked`).toBeGreaterThan(sums);
      expect(promoted.publishing.length, `${row.label} publishes`).toBeGreaterThan(0);
      // A candidate keeps its files; a plain dry run does not.
      const candidate = desktopRuns(release, { ...desktopEnv({ publish: false, full: true, only: 'all' }, row, KEYS), CANDIDATE: 'true' }, row);
      expect(candidate.runs, row.label).toContain('Keep the release files for the tag run');
      expect(candidate.runs.indexOf('Sum the release files for the tag run')).toBeLessThan(candidate.runs.indexOf('Keep the release files for the tag run'));
      expect(candidate.publishing, `${row.label}: a dry run published`).toEqual([]);
      const plain = desktopRuns(release, desktopEnv({ publish: false, full: true, only: 'all' }, row, KEYS), row);
      expect(plain.runs, row.label).not.toContain('Keep the release files for the tag run');
    }
    const download = steps.find((s) => s.name === 'The files the dry run built')!;
    expect(download.text).toContain('name: release-files-${{ matrix.label }}');
    expect(download.text).toContain('path: desktop/release');
    expect(download.text).toContain('run-id: ${{ needs.verify.outputs.dry_run }}');
    expect(steps.find((s) => s.name === 'They are the files the dry run kept, for this version')!.text).toContain('release-files.mjs check desktop/release --version "$VER"');
    // What the candidate keeps is every file the attach step publishes, and its sums.
    const keep = steps.find((s) => s.name === 'Keep the release files for the tag run')!;
    expect(keep.text).toContain('name: release-files-${{ matrix.label }}');
    expect(keep.text).toContain('desktop/release/release-files.sha256');
    const attach = steps.find((s) => s.name === 'Attach to the release')!;
    const attached = [...attach.text.matchAll(/desktop\/release\/([^\s)]+)/g)].map((m) => m[1]);
    expect(attached).toEqual(expect.arrayContaining(['*.exe', '*.AppImage', '*.dmg', 'latest*.yml']));
    for (const glob of attached) expect(keep.text, glob).toContain(`desktop/release/${glob}`);
    for (const glob of ['*.appx', '*.msixbundle']) expect(keep.text, `the Store row's ${glob}`).toContain(`desktop/release/${glob}`);
  });

  it.runIf(!!DIR)('release-files.mjs passes exactly the files the dry run kept, for this version', () => {
    const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'release-files-'));
    try {
      const put = (name: string, text: string) => fs.writeFileSync(path.join(dir, name), text);
      put('filex-desktop-x64.exe', 'an installer');
      put('filex-desktop-x64.exe.blockmap', 'its blockmap');
      put('latest.yml', 'version: 1.2.3\npath: filex-desktop-x64.exe\n');
      put('builder-debug.yml', 'not published');
      let r = releaseFiles('check', dir, '--version', '1.2.3');
      expect(r.code, 'no sums: these are no files a candidate kept').toBe(1);
      expect(r.out).toContain('release-files.sha256');
      r = releaseFiles('keep', dir);
      expect(r.code, r.out).toBe(0);
      const kept = fs.readFileSync(path.join(dir, 'release-files.sha256'), 'utf8').trim().split('\n');
      expect(kept.map((l) => l.split('  ')[1])).toEqual(['filex-desktop-x64.exe', 'filex-desktop-x64.exe.blockmap', 'latest.yml']);
      expect(releaseFiles('check', dir, '--version', '1.2.3').code).toBe(0);
      // a changed byte
      put('filex-desktop-x64.exe', 'an installeR');
      r = releaseFiles('check', dir, '--version', '1.2.3');
      expect(r.code).toBe(1);
      expect(r.out).toContain('filex-desktop-x64.exe is not the file the dry run kept');
      put('filex-desktop-x64.exe', 'an installer');
      // a file nobody kept
      put('filex-desktop-arm64.exe', 'another');
      r = releaseFiles('check', dir, '--version', '1.2.3');
      expect(r.code).toBe(1);
      expect(r.out).toContain('filex-desktop-arm64.exe is here and not in release-files.sha256');
      fs.rmSync(path.join(dir, 'filex-desktop-arm64.exe'));
      // a kept file gone
      fs.renameSync(path.join(dir, 'filex-desktop-x64.exe.blockmap'), path.join(dir, 'gone'));
      r = releaseFiles('check', dir, '--version', '1.2.3');
      expect(r.code).toBe(1);
      expect(r.out).toContain('filex-desktop-x64.exe.blockmap was kept by the dry run and is missing here');
      fs.renameSync(path.join(dir, 'gone'), path.join(dir, 'filex-desktop-x64.exe.blockmap'));
      // another version's feed
      r = releaseFiles('check', dir, '--version', '1.2.4');
      expect(r.code).toBe(1);
      expect(r.out).toContain('latest.yml offers 1.2.3, not 1.2.4');
      expect(releaseFiles('check', dir, '--version', '1.2.3').code).toBe(0);
    } finally {
      fs.rmSync(dir, { recursive: true, force: true });
    }
  });

  it.runIf(!!DIR)('a macOS-less dry run leaves the macos row out, and only a run of everything can', () => {
    const release = code();
    const on = release.slice(0, release.indexOf('\njobs:'));
    expect(on).toMatch(/macos:\s*\n\s+description:[^\n]*\n\s+type: boolean\s*\n\s+default: true/);
    const plan = job(release, 'plan');
    expect(plan).toContain('IN_MACOS: ${{ inputs.macos }}');
    expect(plan, 'a tag push always builds macOS').toMatch(/tag="\$GITHUB_REF_NAME"; only=all; publish=true; macos=true/);
    const narrow = /\n\s+if \[ "\$only" = all \] && \[ "\$macos" = false \]; then\n\s+desktop=\$\(echo "\$desktop" \| jq -c '([^']+)'\)\n\s+fi\n/.exec(plan);
    expect(narrow, 'plan leaves the macos row out of a run of everything').not.toBeNull();
    expect(narrow![1]).toContain('select(.label != "macos")');
    const full = fullRows();
    const out = jq(narrow![1], { include: full }) as { include: Row[] } | undefined;
    if (out) expect(out.include.map((r) => r.label)).toEqual(full.map((r) => r.label).filter((l) => l !== 'macos'));
    // …and verify, in the tag run, lets that one row and no other go missing.
    const script = verifyScript(release);
    expect(script).toMatch(/if \[ "\$label" = macos \]; then\n\s+echo "::warning title=macOS::/);
    expect(script).toMatch(/else\n\s+echo "::error::the dry run \$runs\/\$dry kept no files for the \$label desktop row/);
  });

  it.runIf(!!DIR && !!bash && hasJq)('verify promotes the rows the dry run kept, and lets only macOS go missing', () => {
    const rows = fullRows();
    const every = ['digests-amd64', 'digests-arm64', ...rows.map((r) => `release-files-${r.label}`)];
    const ok = verify(tagRun(rows), { ci: true, dry: true, artifacts: every });
    expect(ok.code, ok.out).toBe(0);
    expect(ok.outputs.dry_run).toBe('2');
    expect(JSON.parse(ok.outputs.desktop).include.map((r: Row) => r.label)).toEqual(rows.map((r) => r.label));
    const noMac = verify(tagRun(rows), { ci: true, dry: true, artifacts: every.filter((a) => a !== 'release-files-macos') });
    expect(noMac.code, noMac.out).toBe(0);
    expect(JSON.parse(noMac.outputs.desktop).include.map((r: Row) => r.label)).not.toContain('macos');
    expect(noMac.out).toContain('::warning title=macOS::');
    expect(noMac.summary).toContain('without the macOS packages');
    const noLinux = verify(tagRun(rows), { ci: true, dry: true, artifacts: every.filter((a) => a !== 'release-files-linux') });
    expect(noLinux.code).not.toBe(0);
    expect(noLinux.out).toContain('kept no files for the linux desktop row');
    const noDigest = verify(tagRun(rows), { ci: true, dry: true, artifacts: every.filter((a) => a !== 'digests-arm64') });
    expect(noDigest.code).not.toBe(0);
    expect(noDigest.out).toContain('kept no digests-arm64');
  });
});
