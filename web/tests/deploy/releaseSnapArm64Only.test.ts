// A release that shipped without its Linux arm64 snap gets it from a run
// started by hand, and that run builds the snap and nothing else, sends it to
// the Snap Store and touches no other file of the Release:
//
//   gh workflow run release.yml -R BRF-Tech/filex -f tag=v1.2.3 -f only=snap-arm64 -f publish=true
//
// ⚠ Why (v0.52.0, 2026-10-06): the release was published from a PC while
// GitHub Actions was down, and snapcraft cannot cross-build an arm64 snap: it
// needs an arm64 machine (ubuntu-24.04-arm) and LXD. Everything else of the
// release was out, and the Release's arm64 AppImage, .deb and .rpm, and both
// Windows installers, are pinned by hash in a winget pull request and in the
// update feeds: a run that built the linux-arm64 row whole and attached what
// it made would have replaced them with different bytes.
//
// What such a run must and must not do, each one careless edit away:
//   - it builds on the linux-arm64 row of a full run (the same runner and
//     arch) but not that row's own build, dist:linux, which makes the
//     AppImage, .deb and .rpm: only the app, the CLI and the snap, the way
//     desktop/package.json's dist:snap makes it;
//   - the app and the CLI inside are still checked to be arm64, before
//     anything leaves the runner;
//   - the snap goes to the Snap Store through the same step as in a full run,
//     and filex-desktop-arm64.snap, named, is the one file attached to the
//     Release: no glob, no latest*.yml;
//   - no binaries, images, npm, AUR, winget, Store or cask, no CLI smoke test
//     and no desktop-arm64-check (it installs a .deb and an AppImage this run
//     does not build);
//   - with publish=true, `verify` asks of the tag's commit exactly what it
//     asks for only=arm64.
//
// The workflows live in the PUBLIC checkout only (see
// releaseGatesImages.test.ts), and the release tool names these titles in its
// workflow guards (scripts/release/plan.mjs, WORKFLOW_GUARDS), so a skip there
// is red. The change reaches the public checkout as its own commit
// (packaging/ci/release-snap-arm64-only.patch). The readers are shared with
// releaseMacosOnly.test.ts (tests/helpers/releaseWorkflow.ts).
import fs from 'node:fs';
import path from 'node:path';
import { describe, expect, it } from 'vitest';

import {
  DIR,
  KEYS,
  REPO,
  bash,
  code,
  desktopEnv,
  desktopRuns,
  expectAddsToARelease,
  expectDryRunLetThrough,
  expectVerifyAsksAsForArm64,
  jobIf,
  job,
  jobsThatRun,
  jq,
  matrices,
  narrowing,
  stepNamed,
  topLevelTerms,
  type Row,
} from '../helpers/releaseWorkflow';

/** The linux-arm64 row of a full run, as the tag push builds it. */
const LINUX_ARM64: Row = { os: 'ubuntu-24.04-arm', label: 'linux-arm64', script: 'dist:linux', arches: 'arm64' };
/** The same row in a run of only=snap-arm64: no row-own build. */
const SNAP_ROW: Row = { ...LINUX_ARM64, script: '' };

const SNAP = { publish: true, full: false, only: 'snap-arm64' };
const BUILD = 'Build the app and the CLI for the snap alone';
const ATTACH_SNAP = 'Attach the arm64 snap to the release';

describe('a run that adds the Linux arm64 snap to a release (only=snap-arm64)', () => {
  it.runIf(!!DIR)('only=snap-arm64 needs a tag, may publish, and is no full run', () => {
    expectAddsToARelease(code(), 'snap-arm64');
  });

  it.runIf(!!DIR)('only=snap-arm64 builds on the linux-arm64 row of a full run, without its own build, and nothing else', () => {
    const plan = job(code(), 'plan');
    const found = matrices(plan);
    expect(found, 'plan writes two desktop matrices (only=arm64, then all)').toHaveLength(2);
    const full = found[1];
    expect(full.rows.filter((r) => r.label === 'linux-arm64')).toEqual([LINUX_ARM64]);
    // The linux-arm64 row alone, its script emptied, after the full matrix
    // and before the outputs.
    const pick = narrowing(plan, 'snap-arm64');
    expect(pick, 'plan keeps the linux-arm64 row alone for only=snap-arm64').not.toBeNull();
    expect(pick!.filter).toBe('{include: [.include[] | select(.label == "linux-arm64") | .script = ""]}');
    expect(pick!.at).toBeGreaterThan(full.at);
    expect(pick!.at).toBeLessThan(plan.indexOf('echo "desktop=$(echo "$desktop" | jq -c .)"'));
    // Where jq is at hand (GitHub's runners), the filter itself runs on the full matrix.
    const out = jq(pick!.filter, { include: full.rows });
    if (out !== undefined) expect(out).toEqual({ include: [SNAP_ROW] });
  });

  it.runIf(!!DIR)('only=snap-arm64 runs plan, verify and desktop, and no other job', () => {
    const release = code();
    expect(jobsThatRun(release, 'snap-arm64')).toEqual(['plan', 'verify', 'desktop']);
    for (const j of ['cli-smoke', 'desktop-arm64-check']) {
      expect(topLevelTerms(jobIf(job(release, j))), j).toContain("needs.plan.outputs.only != 'snap-arm64'");
    }
  });

  it.runIf(!!DIR)('only=snap-arm64 builds the app, the CLI and the snap alone, checks them arm64, and makes no AppImage, .deb or .rpm', () => {
    const release = code();
    const desktop = job(release, 'desktop');
    const { runs, steps } = desktopRuns(release, desktopEnv(SNAP, SNAP_ROW, KEYS), SNAP_ROW);
    // The row's own build and what only it needs do not run.
    for (const s of ['Build installer', 'rpmbuild for the .rpm', 'fpm for the arm64 .deb and .rpm', 'Attach to the release']) {
      expect(runs, s).not.toContain(s);
    }
    // In their place: the app and the CLI, LXD for snapcraft, the snap, the
    // architecture check - in that order, and all before anything is sent.
    const order = [BUILD, 'snapcraft + LXD for the snap', 'Build snap', 'The binaries inside are the architecture on the label', ATTACH_SNAP, 'Upload to the Snap Store'];
    const at = order.map((s) => runs.indexOf(s));
    for (const [i, a] of at.entries()) expect(a, order[i]).toBeGreaterThanOrEqual(0);
    expect([...at].sort((a, b) => a - b)).toEqual(at);
    // Every electron-builder call that runs packs the snap and publishes nothing by itself.
    const builders = steps.flatMap((s) => s.text.split('\n').filter((l) => /electron-builder|\$\{\{ matrix\.script \}\}/.test(l) && !/^\s*-?\s*name:/.test(l)));
    expect(builders.map((l) => l.trim())).toEqual(['run: pnpm --filter ./desktop exec electron-builder --linux snap --publish never']);
    // The app and the CLI are built as desktop/package.json's dist:snap builds
    // them; "Build snap" is its last command.
    const distSnap = JSON.parse(fs.readFileSync(path.join(REPO, 'desktop', 'package.json'), 'utf8')).scripts['dist:snap'] as string;
    const [build, cli, pack] = distSnap.split(' && ');
    expect(pack).toBe('electron-builder --linux snap --publish never');
    const own = stepNamed(desktop, BUILD);
    const lit = (s: string) => s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
    expect(own.text).toMatch(new RegExp(`\\n\\s+cd desktop\\n\\s+${lit(build)}\\n\\s+${lit(cli)}(?:\\n|$)`));
    expect(own.text).not.toMatch(/electron-builder|dist:linux/);
    // The snap is an arm64 app with an arm64 CLI: the launcher, then the
    // Electron binary and the CLI checked for their architecture.
    const arch = stepNamed(desktop, 'The binaries inside are the architecture on the label');
    // Every row that builds checks (a tag run builds nothing since #174: it
    // promotes what its dry run built and checked here).
    expect(arch.cond, 'the check runs on every row that builds').toBe("env.PROMOTE != 'true'");
    expect(arch.text).toMatch(/linux-arm64\)\s*\n\s+head -1 "\$r\/linux-arm64-unpacked\/filex-app" \| grep -qx '#!\/bin\/sh'\n\s+check "\$r\/linux-arm64-unpacked\/filex-app-bin" "\$r\/linux-arm64-unpacked\/resources\/bin\/filex" --expect arm64 ;;/);
  });

  it.runIf(!!DIR)('only=snap-arm64 sends the snap to the Snap Store and attaches it alone, and touches no other Release file', () => {
    const release = code();
    const desktop = job(release, 'desktop');
    const { publishing } = desktopRuns(release, desktopEnv(SNAP, SNAP_ROW, KEYS), SNAP_ROW);
    expect(publishing).toEqual([ATTACH_SNAP, 'Upload to the Snap Store']);
    // One file, named: no glob that could take a latest*.yml or anything else along.
    const attach = stepNamed(desktop, ATTACH_SNAP);
    expect(attach.text).toMatch(/\n\s+snap=desktop\/release\/filex-desktop-arm64\.snap\n/);
    expect(attach.text).toMatch(/\[ -f "\$snap" \] \|\| \{ echo "no \$snap to attach" >&2; exit 1; \}/);
    const uploads = attach.text.split('\n').filter((l) => l.includes('gh release upload'));
    expect(uploads.map((l) => l.trim())).toEqual(['gh release upload "${TAG}" "$snap" --clobber']);
    expect(attach.text).not.toMatch(/\*|latest/);
    // The Store: the same step and channel as a full run.
    const store = stepNamed(desktop, 'Upload to the Snap Store');
    expect(store.text).toMatch(/\[ "\$LABEL" = linux-arm64 \] && snap=desktop\/release\/filex-desktop-arm64\.snap/);
    expect(store.text).toMatch(/snapcraft upload --release=stable "\$snap"/);
    expect(store.text).toContain('SNAPCRAFT_STORE_CREDENTIALS: ${{ secrets.SNAPCRAFT_STORE_CREDENTIALS }}');

    // A full release, and one of only=arm64, are what they were: the
    // linux-arm64 row builds dist:linux, attaches what it made with the glob
    // and sends its snap; it never takes the snap-alone steps.
    for (const run of [{ publish: true, full: true, only: 'all' }, { publish: true, full: false, only: 'arm64' }]) {
      const r = desktopRuns(release, desktopEnv(run, LINUX_ARM64, KEYS), LINUX_ARM64);
      expect(r.publishing, run.only).toEqual(['Attach to the release', 'Upload to the Snap Store']);
      for (const s of ['Build installer', 'rpmbuild for the .rpm', 'fpm for the arm64 .deb and .rpm', 'Build snap']) expect(r.runs, `${run.only}: ${s}`).toContain(s);
      expect(r.runs, run.only).not.toContain(BUILD);
    }
    const x64 = { os: 'ubuntu-latest', label: 'linux', script: 'dist:linux', arches: 'x64' };
    expect(desktopRuns(release, desktopEnv({ publish: true, full: true, only: 'all' }, x64, KEYS), x64).runs).toContain('rpmbuild for the .rpm');
  });

  it.runIf(!!DIR)('a dry run of only=snap-arm64 publishes nothing, and keeps the snap', () => {
    const release = code();
    const dry = desktopRuns(release, desktopEnv({ ...SNAP, publish: false }, SNAP_ROW, KEYS), SNAP_ROW);
    expect(dry.publishing).toEqual([]);
    expect(dry.runs).toContain('Build snap');
    const keep = stepNamed(job(release, 'desktop'), 'Keep the arm64 packages for the check on arm64 machines');
    expect(dry.runs).toContain(keep.name);
    expect(keep.text).toContain('desktop/release/*arm64*.snap');
  });
});

describe('verify, for a run that adds the arm64 snap', () => {
  it.runIf(!!DIR && !!bash)("verify asks the tag's commit for CI and a dry run of everything, for only=snap-arm64 as for only=arm64", () => {
    expectVerifyAsksAsForArm64('snap-arm64');
  });

  it.runIf(!!DIR && !!bash)('a dry run of only=snap-arm64 is let through without asking, like one of only=arm64', () => {
    expectDryRunLetThrough('snap-arm64');
  });
});
