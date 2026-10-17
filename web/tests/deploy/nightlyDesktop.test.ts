// The nightly desktop packages on GitHub's runners (task #266,
// .github/workflows/nightly-desktop.yml): every hour, the nightly source the
// build host pushed to ghcr, when it is new, built into every desktop package
// (Windows x64 and arm64, macOS, Linux x64 and arm64) and the arm64 snap; the
// packages to ghcr as one OCI artifact, the snap to edge alone.
//
// ⚠ Why (the owner, 2026-10-15): the desktop app had no nightly but a weekly
// Linux/Windows one on the build host, which cannot make macOS packages or an
// arm64 snap; "every night, Windows, Linux and macOS, on GitHub's runners,
// kept on ghcr, shown on filex.sh/nightly; no GitHub prerelease". What these
// hold is what keeps a nightly from looking like a release - nothing signed,
// nothing on a Release, a store's stable channel or a package manager - and
// from building the same source every hour.
//
// The workflow lives in the PUBLIC checkout only; the change reaches it as
// packaging/ci/nightly-desktop.patch, and the release tool names these
// workflow tests in its guards.
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

/**
 * Runs the source job's script against a fake `oras`: `manifests` maps a ref
 * to the annotations its manifest carries (none: not found). Returns its
 * output, what it wrote to $GITHUB_OUTPUT, and every oras call in order.
 */
function source(manifests: Record<string, Record<string, string>>, env: Record<string, string> = {}) {
  const tmp = fs.mkdtempSync(path.join(os.tmpdir(), 'nightly-source-'));
  const fwd = (p: string) => p.split(path.sep).join('/');
  try {
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
        '  pull) printf src > filex-nightly-src.tar.gz; exit 0 ;;',
        '  push) exit 0 ;;',
        'esac',
        'exit 1',
        '',
      ].join('\n'),
    );
    fs.chmodSync(path.join(bin, 'oras'), 0o755);
    const script = path.join(tmp, 'step.sh');
    fs.writeFileSync(script, scriptOf(stepNamed(job(wf(), 'source'), 'The source and the last packages').text));
    const out = path.join(tmp, 'out');
    const r = spawnSync(bash!, [fwd(script)], {
      cwd: tmp,
      encoding: 'utf8',
      env: { ...process.env, PATH: `${bin}${path.delimiter}${process.env.PATH}`, GITHUB_OUTPUT: fwd(out), GITHUB_REPOSITORY_OWNER: 'Acme', GITHUB_ACTOR: 'alice', GH_TOKEN: 't', FORCE: '', ...env },
    });
    const read = (f: string) => (fs.existsSync(f) ? fs.readFileSync(f, 'utf8') : '');
    return { code: r.status, out: `${r.stdout}${r.stderr}`, output: read(out), calls: read(path.join(tmp, 'calls.log')).split('\n').filter(Boolean) };
  } finally {
    fs.rmSync(tmp, { recursive: true, force: true });
  }
}

const SRC = 'ghcr.io/acme/filex-nightly-src:nightly';
const OUT = 'ghcr.io/acme/filex-desktop-nightly';
const REV = 'c'.repeat(40);
const srcAnn = { 'org.opencontainers.image.revision': REV, 'sh.filex.nightly.desktop-version': '0.57.0-nightly.20261018+cccccccc', 'sh.filex.nightly.date': '2026-10-18' };

describe('the nightly desktop packages on GitHub (#266)', () => {
  it.runIf(!!DIR)('the nightly desktop workflow runs every hour and by hand, and publishes nothing a release does', () => {
    const text = wf();
    const on = text.slice(0, text.indexOf('\njobs:'));
    expect(on).toMatch(/schedule:\s*\n\s+- cron: "17 \* \* \* \*"/);
    expect(on).toMatch(/force:\s*\n\s+description:[^\n]*\n\s+type: boolean\s*\n\s+default: false/);
    expect(on).toMatch(/\npermissions:\s*\n\s+contents: read\s*\n\s+packages: write\s*\n/);
    expect(on).toMatch(/cancel-in-progress: false/);
    // Never a Release, a store's stable channel, a package manager, a signature.
    expect(text).not.toMatch(/gh release|prerelease|--release=(?:stable|candidate|beta)|wingetcreate|msstore|pnpm publish|git push|CSC_LINK|WIN_CSC/);
    // An input reaches a script through env alone.
    for (const l of text.split('\n').filter((x) => /\$\{\{[^}]*\binputs\./.test(x))) expect(l).toMatch(/^\s+[A-Z_]+: \$\{\{ inputs\.[a-z_]+ \}\}\s*$/);
  });

  it.runIf(!!DIR && hasJq)('builds a new source once: never again what it built or tried, unless forced', () => {
    const none = source({});
    expect(none.code).toBe(0);
    expect(none.output).toBe('build=false\n');
    expect(none.out).toMatch(/no nightly source at ghcr\.io\/acme\/filex-nightly-src:nightly yet/);
    const built = source({ [SRC]: srcAnn, [`${OUT}:nightly`]: { 'org.opencontainers.image.revision': REV } });
    expect(built.output).toBe('build=false\n');
    const tried = source({ [SRC]: srcAnn, [`${OUT}:nightly`]: { 'org.opencontainers.image.revision': 'old' }, [`${OUT}:attempted`]: { 'org.opencontainers.image.revision': REV } });
    expect(tried.output).toBe('build=false\n');
    expect(tried.out).toMatch(/was built or tried/);
    const fresh = source({ [SRC]: srcAnn, [`${OUT}:nightly`]: { 'org.opencontainers.image.revision': 'old' } });
    expect(fresh.code, fresh.out).toBe(0);
    expect(fresh.output).toBe(`build=true\nversion=0.57.0-nightly.20261018+cccccccc\nrevision=${REV}\ndate=2026-10-18\n`);
    // The attempt is recorded after the source is in hand and before anything is built.
    const pull = fresh.calls.findIndex((c) => c === `oras pull ${SRC}`);
    const attempt = fresh.calls.findIndex((c) => c.startsWith(`oras push ${OUT}:attempted --annotation org.opencontainers.image.revision=${REV} `));
    expect(pull).toBeGreaterThan(0);
    expect(attempt).toBeGreaterThan(pull);
    expect(source({ [SRC]: srcAnn, [`${OUT}:nightly`]: { 'org.opencontainers.image.revision': REV } }, { FORCE: 'true' }).output).toMatch(/^build=true\n/);
    const broken = source({ [SRC]: { 'org.opencontainers.image.revision': REV } });
    expect(broken.code).not.toBe(0);
    expect(broken.out).toMatch(/::error::.*carries no sh\.filex\.nightly\.desktop-version or sh\.filex\.nightly\.date/);
  });

  it.runIf(!!DIR)('every row is made by the nightly job script from the source, unsigned, and the arm64 snap goes to edge alone', () => {
    const desktop = job(wf(), 'desktop');
    expect(jobIf(desktop)).toBe("needs.source.outputs.build == 'true'");
    for (const row of [
      '{ os: ubuntu-latest, label: linux-x64, targets: "linux-x64" }',
      '{ os: ubuntu-24.04-arm, label: linux-arm64, targets: "linux-arm64,snap-arm64" }',
      '{ os: windows-latest, label: windows, targets: "windows" }',
      '{ os: macos-15, label: macos, targets: "mac" }',
    ]) {
      expect(desktop).toContain(row);
    }
    expect(desktop).toMatch(/fail-fast: false/);
    const pkg = stepNamed(desktop, 'The packages');
    expect(pkg.text).toMatch(/continue-on-error: true/);
    expect(pkg.text).toContain('run: ND_JOB_SRC="$PWD/src" ND_JOB_OUT="$PWD/out" bash src/scripts/chain/nightly-desktop-job.sh');
    const snap = stepNamed(desktop, 'The arm64 snap');
    expect(snap.cond).toBe("matrix.label == 'linux-arm64'");
    expect(scriptOf(snap.text)).toMatch(/grep -qx 'snap-arm64 built' out\/targets\.txt/);
    expect(scriptOf(snap.text)).toMatch(/electron-builder --linux snap --arm64 --publish never --prepackaged release\/linux-arm64-unpacked/);
    const edge = stepNamed(desktop, 'The arm64 snap to edge');
    expect(topLevelTerms(edge.cond)).toEqual(expect.arrayContaining(["matrix.label == 'linux-arm64'", "steps.snap.outcome == 'success'"]));
    expect(edge.text).toMatch(/SNAPCRAFT_STORE_CREDENTIALS: \$\{\{ secrets\.SNAPCRAFT_STORE_CREDENTIALS \}\}/);
    expect(edge.text).toMatch(/run: node src\/scripts\/chain\/nightly-snap-edge\.mjs out\/filex-desktop-arm64\.snap out\/snap-arm64\.json/);
    const keep = stepNamed(desktop, 'Keep the packages');
    expect(stepsOf(desktop).filter((s) => /upload-artifact/.test(s.text)).length).toBe(1);
    expect(keep.cond).toBe('always()');
    expect(keep.text).toMatch(/name: nightly-desktop-\$\{\{ matrix\.label \}\}/);
  });

  it.runIf(!!DIR)("the night goes to ghcr as one artifact with its manifest, as nightly-<date> and nightly; old nights go without failing the run, a missing row fails it", () => {
    const publish = job(wf(), 'publish');
    expect(topLevelTerms(jobIf(publish))).toEqual(expect.arrayContaining(['!cancelled[]', "needs.source.outputs.build == 'true'"]));
    const manifest = scriptOf(stepNamed(publish, "The night's manifest").text);
    expect(manifest).toMatch(/node src\/scripts\/chain\/nightly-desktop-manifest\.mjs --dir out --version "\$VERSION" --revision "\$REVISION"/);
    expect(manifest).toMatch(/--expect linux-x64,linux-arm64,snap-arm64,windows,mac/);
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

  it('the workflow patch is the documented one, on top of the AUR one', () => {
    const patch = fs.readFileSync(path.join(REPO, 'packaging', 'ci', 'nightly-desktop.patch'), 'utf8');
    expect(patch).toMatch(/^diff --git a\/\.github\/workflows\/nightly-desktop\.yml b\/\.github\/workflows\/nightly-desktop\.yml\nnew file mode 100644$/m);
    expect(patch).not.toMatch(/^diff --git a\/\.github\/workflows\/(?:release|ci)\.yml/m);
    const contributing = fs.readFileSync(path.join(REPO, 'docs', 'CONTRIBUTING.md'), 'utf8');
    expect(contributing).toMatch(/\n11\. `nightly-desktop\.patch` \(#266, 0\.57\), on top of 10:/);
  });
});
