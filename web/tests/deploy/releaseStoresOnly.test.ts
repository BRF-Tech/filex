// A release whose tag run never sent its desktop packages to the stores gets
// them sent by a run started by hand, and that run sends what the Release has
// and builds nothing the Release has:
//
//   gh workflow run release.yml -R BRF-Tech/filex -f tag=v1.2.3 -f only=stores -f publish=true
//
// ⚠ Why (v0.53.0, 2026-10-07): GitHub failed to create the tag run's desktop
// jobs ("Release: Internal server error") after goreleaser, the images and
// npm were out, and the run could not be retried. The Windows and Linux
// packages were built on a PC and attached, macOS and the arm64 snap came
// from only=macos and only=snap-arm64 - and the amd64 snap, the desktop
// app's winget pull request and the Microsoft Store were left to the job that
// never ran, whose tokens live in GitHub alone.
//
// What such a run must and must not do, each one careless edit away:
//   - it keeps the windows, linux and store rows of a full run, and nothing
//     else; windows and linux build nothing (their scripts emptied, PROMOTE):
//     they download the Release's own installers and amd64 snap by name and
//     check them against the digests the Release gives, because the winget
//     manifest pins the files its URLs name;
//   - the store row sends the bundle the dry run of the tag's commit kept
//     (verify: store_files), checked against its sums; with none kept it
//     builds the bundle as a full run's store row does - no Release carries
//     a Store package;
//   - winget gets the manifest written from those installers, with its CLA
//     and the superseded pull requests closed; the Snap Store the amd64 snap;
//     the Microsoft Store the bundle;
//   - nothing is attached to the Release, and no binaries, images, npm, cask
//     or AUR, no CLI smoke test and no desktop-arm64-check;
//   - with publish=true, `verify` asks of the tag's commit what it asks for
//     only=arm64, and then looks for the kept bundle.
//
// The workflows live in the PUBLIC checkout only (see
// releaseGatesImages.test.ts), and the release tool names these titles in its
// workflow guards (scripts/release/plan.mjs, WORKFLOW_GUARDS), so a skip there
// is red. The change reaches the public checkout as its own commit
// (packaging/ci/release-stores.patch). The readers are shared with
// releaseMacosOnly.test.ts and releaseSnapArm64Only.test.ts
// (tests/helpers/releaseWorkflow.ts).
import { describe, expect, it } from 'vitest';

import {
  DIR,
  KEYS,
  TAG_SHA,
  bash,
  builds,
  code,
  desktopEnv,
  desktopRuns,
  expectAddsToARelease,
  expectDryRunLetThrough,
  holds,
  job,
  jobIf,
  jobsThatRun,
  jq,
  matrices,
  narrowing,
  stepNamed,
  topLevelTerms,
  verify,
  verifyRun,
  type Row,
} from '../helpers/releaseWorkflow';

const RUN = { publish: true, full: false, only: 'stores' };
const FETCH = "The Release's files for the stores";
const KEPT = 'The files the dry run built';
const KEPT_CHECK = 'They are the files the dry run kept, for this version';

/** The full run's rows, as plan writes them. */
const fullRows = () => matrices(job(code(), 'plan'))[1].rows;
const row = (label: string) => fullRows().find((r) => r.label === label)!;
/** The rows of only=stores: windows and linux without their own build, store as it is. */
const storesRows = (): Row[] => ['windows', 'linux', 'store'].map((l) => (l === 'store' ? row(l) : { ...row(l), script: '' }));

/**
 * The desktop job's PROMOTE for one row, read from the workflow and evaluated
 * with what plan and verify said: verify's `promote` (a tag run) and
 * `store_files` (the dry run that kept the Store bundle, or '').
 */
function promote(r: Row, only: string, said: { promote?: string; storeFiles?: string } = {}) {
  const line = /\n {6}PROMOTE: \$\{\{\s*(.+?)\s*\}\}\n/.exec(job(code(), 'desktop'));
  expect(line, 'the desktop job sets PROMOTE').not.toBeNull();
  const cond = line![1]
    .replace(/needs\.verify\.outputs\.promote/g, `'${said.promote ?? ''}'`)
    .replace(/needs\.verify\.outputs\.store_files/g, `'${said.storeFiles ?? ''}'`)
    .replace(/needs\.plan\.outputs\.only/g, `'${only}'`);
  return String(holds(cond, {}, r));
}

/** The desktop steps of one row of only=stores. */
function storesRun(r: Row, said: { storeFiles?: string } = {}, run = RUN, keys: Record<string, string> = KEYS) {
  const release = code();
  return desktopRuns(release, { ...desktopEnv(run, r, keys), PROMOTE: promote(r, run.only, said) }, r);
}

describe('a run that sends a release to the stores (only=stores)', () => {
  it.runIf(!!DIR)('only=stores needs a tag, may publish, and is no full run', () => {
    expectAddsToARelease(code(), 'stores');
  });

  it.runIf(!!DIR)('only=stores keeps the windows, linux and store rows of a full run, and empties the scripts of the two whose files the Release has', () => {
    const plan = job(code(), 'plan');
    const found = matrices(plan);
    expect(found, 'plan writes two desktop matrices (only=arm64, then all)').toHaveLength(2);
    const full = found[1];
    const pick = narrowing(plan, 'stores');
    expect(pick, 'plan narrows the full matrix for only=stores').not.toBeNull();
    expect(pick!.filter).toBe(
      '{include: [.include[] | select(.label == "windows" or .label == "linux" or .label == "store") | if .label == "store" then . else .script = "" end]}',
    );
    expect(pick!.at).toBeGreaterThan(full.at);
    expect(pick!.at).toBeLessThan(plan.indexOf('echo "desktop=$(echo "$desktop" | jq -c .)"'));
    expect(storesRows().map((r) => [r.label, r.script])).toEqual([
      ['windows', ''],
      ['linux', ''],
      ['store', 'dist:store'],
    ]);
    // Where jq is at hand (GitHub's runners), the filter itself runs on the full matrix.
    const out = jq(pick!.filter, { include: full.rows });
    if (out !== undefined) expect(out).toEqual({ include: storesRows() });
  });

  it.runIf(!!DIR)('only=stores runs plan, verify and desktop, and no other job', () => {
    const release = code();
    expect(jobsThatRun(release, 'stores')).toEqual(['plan', 'verify', 'desktop']);
    for (const j of ['cli-smoke', 'desktop-arm64-check']) {
      expect(topLevelTerms(jobIf(job(release, j))), j).toContain("needs.plan.outputs.only != 'stores'");
    }
  });

  it.runIf(!!DIR)("only=stores builds nothing on the windows and linux rows: it downloads the Release's files by name and checks them against the Release's digests", () => {
    const desktop = job(code(), 'desktop');
    for (const label of ['windows', 'linux']) {
      const r = storesRows().find((x) => x.label === label)!;
      for (const said of [{}, { storeFiles: '2' }]) expect(promote(r, 'stores', said), label).toBe('true');
      const { runs, steps, publishing } = storesRun(r);
      expect(steps.filter((s) => builds(s.text)).map((s) => s.name), `${label}: built`).toEqual([]);
      // Never the dry run's files, never the Release's files back onto it.
      for (const s of [KEPT, KEPT_CHECK, 'Attach to the release', 'Attach the arm64 snap to the release']) expect(runs, `${label}: ${s}`).not.toContain(s);
      const fetch = runs.indexOf(FETCH);
      expect(fetch, label).toBeGreaterThanOrEqual(0);
      for (const p of publishing) expect(runs.indexOf(p), `${label}: ${p} before the files were fetched`).toBeGreaterThan(fetch);
    }
    const fetch = stepNamed(desktop, FETCH);
    expect(fetch.cond).toBe("env.ONLY == 'stores' && matrix.label != 'store'");
    expect(fetch.text).toMatch(/\n\s+windows\) files=\(filex-desktop-x64\.exe filex-desktop-arm64\.exe\) ;;\n/);
    expect(fetch.text).toMatch(/\n\s+linux\) files=\(filex-desktop-amd64\.snap\) ;;\n/);
    expect(fetch.text).toContain('gh release download "$TAG" -R "$GITHUB_REPOSITORY" -p "$f" -D desktop/release');
    const lists = fetch.text.split('\n').filter((l) => /files=\(|gh release download/.test(l));
    expect(lists, 'the files are named').toHaveLength(3);
    for (const l of lists) expect(l, 'by name: no glob').not.toMatch(/\*/);
    // Each file is the one the Release serves: its sha256 is the asset's digest.
    expect(fetch.text).toMatch(/--jq "\.assets\[\] \| select\(\.name == \\"\$f\\"\) \| \.digest"/);
    expect(fetch.text).toMatch(/\n\s+\[ "\$have" = "\$want" \] \|\| \{ echo .*>&2; exit 1; \}\n/);
    // A full run and a tag run that promotes take no file from the Release.
    for (const r of fullRows()) {
      for (const p of ['', 'true']) {
        const full = desktopRuns(code(), { ...desktopEnv({ publish: true, full: true, only: 'all' }, r, KEYS), PROMOTE: p }, r);
        expect(full.runs, `${r.label} PROMOTE=${p}`).not.toContain(FETCH);
      }
    }
  });

  it.runIf(!!DIR)("only=stores sends the Release's installers to winget and its amd64 snap to the Snap Store, and attaches nothing", () => {
    const desktop = job(code(), 'desktop');
    const [win, linux] = storesRows();
    const w = storesRun(win);
    expect(w.publishing).toEqual(['Submit to winget', 'Sign the CLA on the winget pull request', 'Close the superseded winget pull requests']);
    const order = [FETCH, 'Package-manager manifests', 'Validate the winget manifests', 'Submit to winget', 'Keep the manifests'];
    const at = order.map((s) => w.runs.indexOf(s));
    for (const [i, a] of at.entries()) expect(a, order[i]).toBeGreaterThanOrEqual(0);
    expect([...at].sort((a, b) => a - b)).toEqual(at);
    // The manifest is written from the two installers the Release has.
    expect(stepNamed(desktop, 'Package-manager manifests').text).toMatch(
      /--windows desktop\/release\/filex-desktop-x64\.exe \\\n\s+--windows-arm64 desktop\/release\/filex-desktop-arm64\.exe --out pkg-out/,
    );
    // No token: nothing submitted, and the run says so.
    const noToken = storesRun(win, {}, RUN, { ...KEYS, WINGET_TOKEN: '' });
    expect(noToken.publishing).toEqual([]);
    expect(noToken.runs).toContain('Package-manager submission skipped');

    const l = storesRun(linux);
    expect(l.publishing).toEqual(['Upload to the Snap Store']);
    const snap = stepNamed(desktop, 'Upload to the Snap Store');
    expect(snap.text).toMatch(/\n\s+snap=desktop\/release\/filex-desktop-amd64\.snap\n/);
    expect(snap.text).toMatch(/snapcraft upload --release=stable "\$snap"/);

    // A full release and the other partial runs are what they were.
    const fullWin = row('windows');
    expect(desktopRuns(code(), desktopEnv({ publish: true, full: true, only: 'all' }, fullWin, KEYS), fullWin).publishing).toEqual([
      'Attach to the release',
      'Submit to winget',
      'Sign the CLA on the winget pull request',
      'Close the superseded winget pull requests',
    ]);
    for (const only of ['arm64', 'macos', 'snap-arm64']) {
      const r = desktopRuns(code(), desktopEnv({ publish: true, full: false, only }, { ...fullWin, script: '' }, KEYS), { ...fullWin, script: '' });
      expect(r.runs, only).not.toContain('Package-manager manifests');
      expect(r.runs, only).not.toContain(FETCH);
    }
  });

  it.runIf(!!DIR)('only=stores sends the bundle the dry run kept to the Microsoft Store, or builds it when none was kept', () => {
    const store = storesRows()[2];
    const STORE = ['Microsoft Store CLI', 'Submit to the Microsoft Store'];
    // Kept: promoted, checked against its sums, sent; nothing built.
    expect(promote(store, 'stores', { storeFiles: '2' })).toBe('true');
    const kept = storesRun(store, { storeFiles: '2' });
    expect(kept.steps.filter((s) => builds(s.text)).map((s) => s.name)).toEqual([]);
    expect(kept.publishing).toEqual(STORE);
    const order = [KEPT, KEPT_CHECK, 'Keep the Store package for Partner Center', ...STORE];
    const at = order.map((s) => kept.runs.indexOf(s));
    for (const [i, a] of at.entries()) expect(a, order[i]).toBeGreaterThanOrEqual(0);
    expect([...at].sort((a, b) => a - b)).toEqual(at);
    expect(kept.runs).not.toContain(FETCH);
    // None kept: the store row of a full run, built from the tag and sent.
    expect(promote(store, 'stores')).toBe('false');
    const built = storesRun(store);
    for (const s of ['Build installer', 'Install the Store package and check it as a Store copy', 'Build the arm64 Store package', 'Bundle the Store packages']) {
      expect(built.runs, s).toContain(s);
    }
    expect(built.runs).not.toContain(KEPT);
    expect(built.publishing).toEqual(STORE);
    expect(built.runs.indexOf('Bundle the Store packages')).toBeLessThan(built.runs.indexOf('Submit to the Microsoft Store'));
    // Only the store row ever builds in only=stores, and a tag run's PROMOTE is verify's word alone.
    for (const r of fullRows()) {
      expect(promote(r, 'all', { promote: 'true' }), r.label).toBe('true');
      expect(promote(r, 'all', { storeFiles: '2' }), r.label).toBe('false');
    }
  });

  it.runIf(!!DIR)('a dry run of only=stores publishes nothing, and writes and validates the winget manifest', () => {
    const dry = { ...RUN, publish: false };
    for (const r of storesRows()) {
      const d = storesRun(r, {}, dry);
      expect(d.publishing, r.label).toEqual([]);
    }
    const w = storesRun(storesRows()[0], {}, dry);
    for (const s of [FETCH, 'Package-manager manifests', 'Validate the winget manifests', 'Keep the manifests']) expect(w.runs, s).toContain(s);
  });
});

describe('verify, for a run that sends a release to the stores', () => {
  it.runIf(!!DIR && !!bash)("verify asks the tag's commit for CI and a dry run of everything, for only=stores as for only=arm64, and finds the Store bundle it kept", () => {
    const arm = verify(verifyRun('arm64', true), { ci: true, dry: true, artifacts: ['release-files-store'] });
    expect(arm.code, arm.out).toBe(0);
    expect(arm.outputs.store_files).toBeUndefined();
    const kept = verify(verifyRun('stores', true), { ci: true, dry: true, artifacts: ['release-files-windows', 'release-files-store'] });
    expect(kept.code, kept.out).toBe(0);
    // What only=arm64 asks, then the artifacts of that dry run (run 2).
    expect(kept.calls.slice(0, -1)).toEqual(arm.calls);
    expect(kept.calls.at(-1)).toMatch(/^api -X GET repos\/BRF-Tech\/filex\/actions\/runs\/2\/artifacts /);
    expect(kept.calls.at(-1)).toContain(`|SHA=${TAG_SHA}`);
    expect(kept.outputs.dry_run).toBe('2');
    expect(kept.outputs.store_files).toBe('2');
    expect(kept.summary).toContain('was tested before it was tagged');
    // No bundle kept: verify passes, and the store row builds it.
    const none = verify(verifyRun('stores', true), { ci: true, dry: true, artifacts: ['release-files-windows'] });
    expect(none.code, none.out).toBe(0);
    expect(none.outputs.store_files).toBeUndefined();
    expect(none.out).toMatch(/::notice title=Microsoft Store::the dry run \S+ kept no Store bundle/);
    // A commit CircleCI passed has no dry run: nothing kept, the store row builds.
    const cc = verify(verifyRun('stores', true), { circleci: 'green' });
    expect(cc.code, cc.out).toBe(0);
    expect(cc.outputs.evidence).toBe('circleci');
    expect(cc.outputs.store_files).toBeUndefined();
    // Untested: red, as for only=arm64.
    for (const answers of [{ ci: true }, { dry: true }, {}]) {
      const r = verify(verifyRun('stores', true), answers);
      expect(r.code, JSON.stringify(answers)).not.toBe(0);
      expect(r.out).toMatch(/::error::v1\.2\.3 publishes nothing/);
    }
  });

  it.runIf(!!DIR && !!bash)('a dry run of only=stores is let through without asking, like one of only=arm64', () => {
    expectDryRunLetThrough('stores');
  });
});
