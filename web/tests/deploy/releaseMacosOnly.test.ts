// A release that shipped without its macOS desktop packages gets them from a
// run started by hand, and that run builds and publishes the macOS packages
// and nothing else:
//
//   gh workflow run release.yml -R BRF-Tech/filex -f tag=v1.2.3 -f only=macos -f publish=true
//
// ⚠ Why (v0.52.0, 2026-10-06): GitHub Actions was down when v0.52.0 was
// tagged, and its tag run stopped at `verify`. Every other channel was
// published from a PC; the macOS packages need a Mac to be built on, so they
// come from GitHub afterwards, from a run modelled on only=arm64. The tag run
// itself is never re-run: goreleaser cannot recreate the Release, and
// everything else it would publish is already out.
//
// What such a run must and must not do, each one careless edit away:
//   - it builds the macos row of a full run, exactly (the same runner, script
//     and arch), and nothing else;
//   - no binaries, images, npm, AUR or CLI smoke test, and no
//     desktop-arm64-check: its rows install Linux and Windows packages this
//     run does not build, and would fail for want of them;
//   - the .dmg, the .zip and latest-mac.yml are attached to the Release, and
//     the Homebrew desktop cask (filex-app) is written from that .dmg and
//     committed to the tap, as the tag push would have done; winget and the
//     Store are left alone;
//   - with publish=true, `verify` asks of the tag's commit exactly what it
//     asks for only=arm64: CI and a dry run of everything passed on it.
//
// The workflows live in the PUBLIC checkout only (see
// releaseGatesImages.test.ts): read from `<repo>/.github/workflows` or
// FILEX_WORKFLOWS_DIR, and skipped when neither exists. The release tool
// names these titles in its workflow guards (scripts/release/plan.mjs,
// WORKFLOW_GUARDS), so a skip there is red. The change reaches the public
// checkout as its own commit (packaging/ci/release-macos-only.patch).
//
// The readers, the condition evaluator and verify's fake `gh` are shared with
// releaseSnapArm64Only.test.ts (tests/helpers/releaseWorkflow.ts).
import { describe, expect, it } from 'vitest';

import {
  DIR,
  KEYS,
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
  stepsOf,
  topLevelTerms,
  verifyScript,
  type Row,
} from '../helpers/releaseWorkflow';

/** The macos row of a full run, as the tag push builds it. */
const MAC: Row = { os: 'macos-15', label: 'macos', script: 'dist:mac', arches: 'arm64' };

describe('a run that adds the macOS desktop packages to a release (only=macos)', () => {
  it.runIf(!!DIR)('only=macos needs a tag, may publish, and is no full run', () => {
    expectAddsToARelease(code(), 'macos');
  });

  it.runIf(!!DIR)('only=macos builds the macos row of a full run, and nothing else', () => {
    const plan = job(code(), 'plan');
    const found = matrices(plan);
    expect(found, 'plan writes two desktop matrices (only=arm64, then all)').toHaveLength(2);
    const [arm64Only, full] = found;
    expect(full.rows.filter((r) => r.label === 'macos')).toEqual([MAC]);
    expect(arm64Only.rows.some((r) => r.label === 'macos')).toBe(false);
    // The full matrix is the one written for anything but only=arm64...
    expect(plan).toMatch(/if \[ "\$only" = arm64 \]; then\s+desktop='\{[\s\S]*?\}'\s+else\s+desktop='\{[\s\S]*?\}'\s+fi\n/);
    // ...and only=macos keeps its macos row alone, after it and before the outputs.
    const pick = narrowing(plan, 'macos');
    expect(pick, 'plan keeps the macos row alone for only=macos').not.toBeNull();
    expect(pick!.filter).toBe('{include: [.include[] | select(.label == "macos")]}');
    expect(pick!.at).toBeGreaterThan(full.at);
    expect(pick!.at).toBeLessThan(plan.indexOf('echo "desktop=$(echo "$desktop" | jq -c .)"'));
    // Where jq is at hand (GitHub's runners), the filter itself runs on the full matrix.
    const out = jq(pick!.filter, { include: full.rows });
    if (out !== undefined) expect(out).toEqual({ include: [MAC] });
  });

  it.runIf(!!DIR)('only=macos runs plan, verify and desktop, and no other job', () => {
    const release = code();
    expect(jobsThatRun(release, 'macos')).toEqual(['plan', 'verify', 'desktop']);
    // desktop-arm64-check would otherwise run past a desktop job that built
    // none of its packages, and cli-smoke past a skipped `binaries`.
    for (const j of ['cli-smoke', 'desktop-arm64-check']) {
      expect(topLevelTerms(jobIf(job(release, j))), j).toContain("needs.plan.outputs.only != 'macos'");
    }
  });

  it.runIf(!!DIR)('only=macos attaches the macOS packages and commits the Homebrew cask, and leaves winget and the Store alone', () => {
    const release = code();
    const desktop = job(release, 'desktop');
    expect(desktop).toMatch(/\n {6}ONLY: \$\{\{ needs\.plan\.outputs\.only \}\}\n/);
    const run = { publish: true, full: false, only: 'macos' };

    const withKey = desktopRuns(release, desktopEnv(run, MAC, KEYS), MAC);
    expect(withKey.publishing).toEqual(['Attach to the release', 'Commit the cask to the tap']);
    for (const s of ['Build installer', 'Package-manager manifests', 'Keep the manifests']) expect(withKey.runs, s).toContain(s);
    expect(withKey.runs).not.toContain('Package-manager submission skipped');
    expect(withKey.runs.indexOf('Package-manager manifests')).toBeLessThan(withKey.runs.indexOf('Commit the cask to the tap'));

    // No tap key: nothing is committed, and the run says so.
    const noKey = desktopRuns(release, desktopEnv(run, MAC, { ...KEYS, HOMEBREW_TAP_DEPLOY_KEY: '' }), MAC);
    expect(noKey.publishing).toEqual(['Attach to the release']);
    expect(noKey.runs).toContain('Package-manager submission skipped');

    // What is attached: the .dmg, the .zip and latest-mac.yml, over what is there.
    const attach = stepsOf(desktop).find((s) => s.name === 'Attach to the release')!;
    for (const f of ['desktop/release/*.dmg', 'desktop/release/*.zip', 'desktop/release/latest*.yml']) expect(attach.text).toContain(f);
    expect(attach.text).toContain('gh release upload "${TAG}" "${files[@]}" --clobber');
    // The cask: written from the .dmg this job built, committed to the tap.
    const manifests = stepsOf(desktop).find((s) => s.name === 'Package-manager manifests')!;
    expect(manifests.text).toMatch(/--mac desktop\/release\/filex-desktop-arm64\.dmg --out pkg-out/);
    const commit = stepsOf(desktop).find((s) => s.name === 'Commit the cask to the tap')!;
    expect(commit.text).toContain('cp pkg-out/homebrew/Casks/filex-app.rb tap/Casks/');

    // A full release is what it was: the macos row the same, the windows row
    // still submits to winget. And only=arm64 still leaves the package
    // managers alone.
    const full = { publish: true, full: true, only: 'all' };
    expect(desktopRuns(release, desktopEnv(full, MAC, KEYS), MAC).publishing).toEqual(['Attach to the release', 'Commit the cask to the tap']);
    const win = { os: 'windows-latest', label: 'windows', script: 'dist:win', arches: 'x64 arm64' };
    expect(desktopRuns(release, desktopEnv(full, win, KEYS), win).publishing).toEqual([
      'Attach to the release',
      'Submit to winget',
      'Sign the CLA on the winget pull request',
      'Close the superseded winget pull requests',
    ]);
    const armWin = { os: 'windows-latest', label: 'windows', script: '', arches: 'arm64' };
    const arm = desktopRuns(release, desktopEnv({ publish: true, full: false, only: 'arm64' }, armWin, KEYS), armWin);
    expect(arm.publishing).toEqual(['Attach to the release']);
    expect(arm.runs).not.toContain('Package-manager manifests');
  });

  it.runIf(!!DIR)('a dry run of only=macos publishes nothing, and says what it was', () => {
    const release = code();
    const dry = desktopRuns(release, desktopEnv({ publish: false, full: false, only: 'macos' }, MAC, KEYS), MAC);
    expect(dry.publishing).toEqual([]);
    // The cask is still written and kept, to be looked at.
    expect(dry.runs).toContain('Package-manager manifests');
    expect(dry.runs).toContain('Keep the manifests');
    // (#174 reworded it: a dry run waits for no test suite either.)
    expect(verifyScript(release)).toContain('echo "A dry run (only=$ONLY): nothing is published');
    expect(job(release, 'verify')).toMatch(/\n\s+ONLY: \$\{\{ needs\.plan\.outputs\.only \}\}\n/);
  });
});

describe('verify, for a run that adds the macOS packages', () => {
  it.runIf(!!DIR && !!bash)("verify asks the tag's commit for CI and a dry run of everything, for only=macos as for only=arm64", () => {
    expectVerifyAsksAsForArm64('macos');
  });

  it.runIf(!!DIR && !!bash)('a dry run of only=macos is let through without asking, like one of only=arm64', () => {
    expectDryRunLetThrough('macos');
  });
});
