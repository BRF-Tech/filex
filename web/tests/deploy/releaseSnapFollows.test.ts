// A release that goes out while the Snap Store is down, and the run that adds
// its snaps once the Store is back (v0.60.0, 2026-11-11):
//
//   gh variable set FILEX_SKIP_SNAP --body true -R BRF-Tech/filex   (before the dry run)
//   ... the dry run, the tag, the tag run ...
//   gh variable delete FILEX_SKIP_SNAP -R BRF-Tech/filex            (once the tag run ended)
//   gh workflow run release.yml -R BRF-Tech/filex --ref v1.2.3 -f snap_only=true
//
// ⚠ Why (v0.60.0): the Store went up and down for hours, and every dry run
// died at "snap install snapcraft" ("unable to contact snap store") on both
// Linux rows - and with them the AppImage, .deb, .rpm and the check machines.
// A tag push takes no input, so the switch is a repository variable that the
// dry run and the tag run of the same commit both read.
//
// What each careless edit away must hold:
//   - FILEX_SKIP_SNAP true: the Linux rows build no snap (no snapcraft, no
//     LXD, no "Build snap") and send none, still make the AppImage, .deb and
//     .rpm and attach them, and say on every Linux row that the snap follows;
//   - the Release's notes carry SNAP_FOLLOWS_LINE on a line of its own, word
//     for word: the snap releaser looks for it and deletes it, and the
//     release tool's Release check (plan.mjs, releaseAssetsCheck) does not
//     ask for the snaps while it is there;
//   - only=snap (snap_only=true: only=snap, published, on the tag the run was
//     started on) builds both Linux snaps from THAT tag without the rows'
//     own builds, checks them, attaches each by name and sends it to the
//     Store as a full run does - plan, verify and desktop, and no other job;
//   - FILEX_SKIP_SNAP never stops only=snap, the run that adds the snaps.
//
// The workflows live in the PUBLIC checkout only (releaseGatesImages.test.ts);
// the change reaches it as packaging/ci/release-snap-follows.patch.
import { spawnSync } from 'node:child_process';
import { describe, expect, it } from 'vitest';

import { RELEASE_SNAPS, SNAP_FOLLOWS_LINE, releaseAssetsCheck, snapsFollow } from '../../../scripts/release/plan.mjs';
import {
  DIR,
  KEYS,
  SNAP_UPLOAD,
  bash,
  code,
  desktopEnv,
  desktopRuns,
  expectAddsToARelease,
  expectDryRunLetThrough,
  expectVerifyAsksAsForArm64,
  job,
  jobIf,
  jobsThatRun,
  jq,
  matrices,
  narrowing,
  scriptOf,
  stepNamed,
  stepsOf,
  topLevelTerms,
  type Row,
} from '../helpers/releaseWorkflow';

const LINUX: Row = { os: 'ubuntu-latest', label: 'linux', script: 'dist:linux', arches: 'x64' };
const LINUX_ARM64: Row = { os: 'ubuntu-24.04-arm', label: 'linux-arm64', script: 'dist:linux', arches: 'arm64' };
const SNAP_STEPS = ['snapcraft + LXD for the snap', 'Build snap', 'Upload to the Snap Store'];
const FOLLOWS = 'The snap follows (FILEX_SKIP_SNAP)';
const BUILD = 'Build the app and the CLI for the snap alone';
const ATTACH_ROW = "Attach the row's snap to the release";
const FULL = { publish: true, full: true, only: 'all' };
const SNAP = { publish: true, full: false, only: 'snap' };

describe('a Release whose snaps follow (the release tool reads it)', () => {
  const want = ['checksums.txt', 'filex-desktop-amd64.deb', ...RELEASE_SNAPS];
  const assets = (...names: string[]) => names.map((name) => ({ name }));

  it('is checked without its snaps while its notes say, word for word, that they follow', () => {
    const body = `## Uploaders\n\nSome prose.\n\n${SNAP_FOLLOWS_LINE}\n`;
    expect(snapsFollow(body)).toBe(true);
    expect(snapsFollow(body.replace(/\n/g, '\r\n'))).toBe(true);
    const r = releaseAssetsCheck(want, { assets: assets('checksums.txt', 'filex-desktop-amd64.deb'), body });
    expect(r.ok).toBe(true);
    expect(r.detail).toContain('filex-desktop-amd64.snap and filex-desktop-arm64.snap follow');
    // Anything else missing is still missing.
    expect(releaseAssetsCheck(want, { assets: assets('checksums.txt'), body })).toMatchObject({ ok: false });
    expect(releaseAssetsCheck(want, { assets: assets('checksums.txt'), body }).detail).toContain('missing: filex-desktop-amd64.deb;');
  });

  it('wants both snaps again once the line is gone, or is anything but that line', () => {
    const have = { assets: assets('checksums.txt', 'filex-desktop-amd64.deb') };
    for (const body of ['', 'Snap package follows.', `> ${SNAP_FOLLOWS_LINE}`, SNAP_FOLLOWS_LINE.toLowerCase()]) {
      expect(snapsFollow(body), body).toBe(false);
      const r = releaseAssetsCheck(want, { ...have, body });
      expect(r.ok, body).toBe(false);
      expect(r.detail, body).toContain('missing: filex-desktop-amd64.snap, filex-desktop-arm64.snap');
    }
    expect(releaseAssetsCheck(want, { assets: assets(...want), body: '' })).toEqual({ ok: true, detail: '4 assets' });
  });
});

describe('FILEX_SKIP_SNAP: a release while the Snap Store is down', () => {
  it.runIf(!!DIR)('FILEX_SKIP_SNAP builds and sends no snap on either Linux row, still makes and attaches the rest, and says so', () => {
    const release = code();
    const desktop = job(release, 'desktop');
    expect(desktop).toContain("SKIP_SNAP: ${{ vars.FILEX_SKIP_SNAP == 'true' && needs.plan.outputs.only != 'snap' }}");
    for (const row of [LINUX, LINUX_ARM64]) {
      const off = desktopRuns(release, { ...desktopEnv(FULL, row, KEYS), SKIP_SNAP: 'true' }, row);
      for (const s of SNAP_STEPS) expect(off.runs, `${row.label}: ${s}`).not.toContain(s);
      for (const s of ['Build installer', 'The binaries inside are the architecture on the label', 'Attach to the release', FOLLOWS]) {
        expect(off.runs, `${row.label}: ${s}`).toContain(s);
      }
      expect(off.publishing, row.label).toEqual(['Attach to the release']);
      // Unset (or anything but true): the snaps as always, and no word of it.
      const on = desktopRuns(release, desktopEnv(FULL, row, KEYS), row);
      for (const s of SNAP_STEPS) expect(on.runs, `${row.label}: ${s}`).toContain(s);
      expect(on.runs, row.label).not.toContain(FOLLOWS);
    }
    // Rows that make no snap say nothing.
    const win: Row = { os: 'windows-latest', label: 'windows', script: 'dist:win', arches: 'x64 arm64' };
    expect(desktopRuns(release, { ...desktopEnv(FULL, win, KEYS), SKIP_SNAP: 'true' }, win).runs).not.toContain(FOLLOWS);
    // A warning and a line of the summary, naming the run that adds them.
    const follows = scriptOf(stepNamed(desktop, FOLLOWS).text);
    expect(follows).toMatch(/::warning title=Snap: follows::/);
    expect(follows).toContain('>> "$GITHUB_STEP_SUMMARY"');
    expect(follows).toContain('-f snap_only=true');
  });

  it.runIf(!!DIR)('FILEX_SKIP_SNAP puts the line the snaps follow by into the Release notes, word for word', () => {
    const binaries = job(code(), 'binaries');
    const notes = stepNamed(binaries, 'Release notes from CHANGELOG.md');
    expect(notes.text).toContain("SKIP_SNAP: ${{ vars.FILEX_SKIP_SNAP == 'true' }}");
    const script = scriptOf(notes.text);
    expect(script).toContain(`printf '\\n${SNAP_FOLLOWS_LINE}\\n' >> /tmp/release-notes.md`);
    expect(script.indexOf('if [ "$SKIP_SNAP" = true ]; then')).toBeGreaterThan(script.indexOf('> /tmp/release-notes.md'));
    // The notes go to goreleaser after that step, as before.
    const after = stepsOf(binaries).map((s) => s.name);
    expect(after.indexOf('goreleaser/goreleaser-action@v6')).toBeGreaterThan(after.indexOf('Release notes from CHANGELOG.md'));
  });
});

describe('only=snap (snap_only=true): both snaps for a release that went out without them', () => {
  it.runIf(!!DIR)('only=snap needs a tag, may publish, and is no full run; snap_only=true is only=snap, published, on its own tag', () => {
    const release = code();
    expectAddsToARelease(release, 'snap');
    const on = release.slice(0, release.indexOf('\njobs:'));
    expect(on).toMatch(/\n {6}snap_only:\n {8}description: [^\n]+\n {8}type: boolean\n {8}default: false\n/);
    const runName = release.split('\n').find((l) => l.startsWith('run-name:')) ?? '';
    expect(runName).toContain("github.event_name == 'workflow_dispatch' && inputs.snap_only && format('publish snap {0} {1}', inputs.tag || github.ref_name, github.sha)");
    // The mapping itself, run: plan's script up to it.
    // (`- id: p` starts no step of its own for stepsOf: it is the checkout's.)
    const plan = scriptOf(stepNamed(job(release, 'plan'), 'actions/checkout@v4').text);
    const cut = plan.indexOf('\nfi\n', plan.indexOf('IN_SNAP_ONLY:-false')) + 4;
    expect(cut, 'plan reads snap_only').toBeGreaterThan(4);
    const head = `${plan.slice(0, cut)}echo "$tag|$only|$publish"\n`;
    const ask = (env: Record<string, string>) => {
      const r = spawnSync(bash!, ['-c', head], {
        // As GitHub sets them: every input, empty when not given.
        env: { PATH: process.env.PATH ?? '', EVENT: 'workflow_dispatch', IN_TAG: '', IN_ONLY: '', IN_PUBLISH: '', IN_MACOS: '', IN_SNAP_ONLY: '', ...env },
        encoding: 'utf8',
      });
      return { code: r.status, out: r.stdout.trim(), err: r.stdout + r.stderr };
    };
    if (bash) {
      expect(ask({ IN_SNAP_ONLY: 'true', GITHUB_REF_TYPE: 'tag', GITHUB_REF_NAME: 'v1.2.3' }).out).toBe('v1.2.3|snap|true');
      expect(ask({ IN_SNAP_ONLY: 'true', IN_TAG: 'v1.2.4', GITHUB_REF_TYPE: 'branch', GITHUB_REF_NAME: 'main' }).out).toBe('v1.2.4|snap|true');
      expect(ask({ IN_SNAP_ONLY: 'true', GITHUB_REF_TYPE: 'branch', GITHUB_REF_NAME: 'main' }).out).toBe('|snap|true');
      const both = ask({ IN_SNAP_ONLY: 'true', IN_ONLY: 'stores', GITHUB_REF_TYPE: 'tag', GITHUB_REF_NAME: 'v1.2.3' });
      expect(both.code).not.toBe(0);
      expect(both.err).toContain('snap_only=true is only=snap');
      // Off, and on a tag push, nothing changes.
      expect(ask({ IN_SNAP_ONLY: 'false', IN_ONLY: 'all', GITHUB_REF_TYPE: 'tag', GITHUB_REF_NAME: 'v1.2.3' }).out).toBe('|all|false');
      expect(ask({ EVENT: 'push', IN_SNAP_ONLY: 'true', GITHUB_REF_TYPE: 'tag', GITHUB_REF_NAME: 'v1.2.3' }).out).toBe('v1.2.3|all|true');
    }
  });

  it.runIf(!!DIR)('only=snap keeps both Linux rows of a full run, without their own builds, and runs plan, verify and desktop alone', () => {
    const release = code();
    const plan = job(release, 'plan');
    const full = matrices(plan)[1];
    const pick = narrowing(plan, 'snap');
    expect(pick, 'plan keeps the two Linux rows for only=snap').not.toBeNull();
    expect(pick!.filter).toBe('{include: [.include[] | select(.label == "linux" or .label == "linux-arm64") | .script = ""]}');
    expect(pick!.at).toBeGreaterThan(full.at);
    const out = jq(pick!.filter, { include: full.rows });
    if (out !== undefined) expect(out).toEqual({ include: [{ ...LINUX, script: '' }, { ...LINUX_ARM64, script: '' }] });
    expect(jobsThatRun(release, 'snap')).toEqual(['plan', 'verify', 'desktop']);
    for (const j of ['cli-smoke', 'desktop-arm64-check']) {
      expect(topLevelTerms(jobIf(job(release, j))), j).toContain("needs.plan.outputs.only != 'snap'");
    }
  });

  it.runIf(!!DIR)('only=snap builds, checks, attaches by name and sends each Linux snap, and nothing else; FILEX_SKIP_SNAP does not stop it', () => {
    const release = code();
    const desktop = job(release, 'desktop');
    for (const row of [LINUX, LINUX_ARM64]) {
      const own = { ...row, script: '' };
      // The variable as the job's env computes it for only=snap: never true.
      const r = desktopRuns(release, { ...desktopEnv(SNAP, own, KEYS), SKIP_SNAP: 'false' }, own);
      for (const s of ['Build installer', 'rpmbuild for the .rpm', 'fpm for the arm64 .deb and .rpm', 'Attach to the release', FOLLOWS]) {
        expect(r.runs, `${row.label}: ${s}`).not.toContain(s);
      }
      const order = [BUILD, 'snapcraft + LXD for the snap', 'Build snap', 'The binaries inside are the architecture on the label', ATTACH_ROW, 'Upload to the Snap Store'];
      const at = order.map((s) => r.runs.indexOf(s));
      for (const [i, a] of at.entries()) expect(a, `${row.label}: ${order[i]}`).toBeGreaterThanOrEqual(0);
      expect([...at].sort((a, b) => a - b), row.label).toEqual(at);
      expect(r.publishing, row.label).toEqual([ATTACH_ROW, 'Upload to the Snap Store']);
      // A dry run of only=snap publishes nothing.
      expect(desktopRuns(release, desktopEnv({ ...SNAP, publish: false }, own, KEYS), own).publishing, row.label).toEqual([]);
    }
    const attach = scriptOf(stepNamed(desktop, ATTACH_ROW).text);
    expect(attach).toContain('snap=desktop/release/filex-desktop-amd64.snap');
    expect(attach).toContain('[ "$LABEL" = linux-arm64 ] && snap=desktop/release/filex-desktop-arm64.snap');
    expect(attach).toContain('gh release upload "${TAG}" "$snap" --clobber');
    expect(attach, 'one file by name, never a glob').not.toMatch(/\*|latest/);
    expect(scriptOf(stepNamed(desktop, 'Upload to the Snap Store').text)).toMatch(SNAP_UPLOAD);
  });
});

describe('verify, for a run that adds both snaps', () => {
  it.runIf(!!DIR && !!bash)("verify asks the tag's commit for CI and a dry run of everything, for only=snap as for only=arm64", () => {
    expectVerifyAsksAsForArm64('snap');
  });

  it.runIf(!!DIR && !!bash)('a dry run of only=snap is let through without asking, like one of only=arm64', () => {
    expectDryRunLetThrough('snap');
  });
});
