// The AUR package needs the Linux packages, not the whole desktop matrix
// (task #145): release.yml's `aur` job runs once every desktop row has ended,
// however each ended, as long as verify passed and the desktop job ran at all,
// and its first real step asks the Release for the .deb and the AppImage it
// repackages. Without AUR_SSH_PRIVATE_KEY it says "skipped: no AUR key" and
// does nothing else: green, never red.
//
// ⚠ Why: with `needs: desktop` and GitHub's implicit success(), a Windows row
// that failed (a Store hiccup, a winget timeout) skipped the AUR job too,
// though the Linux row had put the .deb on the Release - the AUR fell behind
// for reasons that had nothing to do with it. And the AUR's own account
// registration was closed when filex-app-bin was to be published, so a run
// has no key to push with: that is a decision, not a failure, and the run
// says so in one line instead of a warning that reads like one.
//
// The workflow lives in the PUBLIC checkout only (releaseGatesImages.test.ts);
// the change reaches it as packaging/ci/release-aur-linux-only.patch, and the
// release tool names this file's workflow tests in its guards.
import { spawnSync } from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { describe, expect, it } from 'vitest';

import { DIR, REPO, bash, code, holds, job, jobIf, scriptOf, stepNamed, stepsOf, topLevelTerms } from '../helpers/releaseWorkflow';

const KEY_STEP = 'The AUR key';
const LINUX_STEP = 'The Linux packages are on the Release';
const SKIPPED = 'skipped: no AUR key';

/** The jobs a job's `needs:` names. */
function needs(block: string): string[] {
  const inline = /\n {4}needs:\s*(.+)/.exec(block)?.[1] ?? '';
  return inline.replace(/[[\]]/g, '').split(',').map((s) => s.trim()).filter(Boolean);
}

interface Ended {
  cancelled?: boolean;
  plan?: string;
  verify?: string;
  desktop?: string;
  publish?: string;
  full?: string;
}

/**
 * Whether the aur job starts, its `if:` evaluated as GitHub would for a run
 * whose jobs ended as given: the few things the condition reads, and nothing
 * else (anything more fails the test instead of being guessed at).
 */
function aurStarts(cond: string, e: Ended): boolean {
  const results: Record<string, string> = { plan: e.plan ?? 'success', verify: e.verify ?? 'success', desktop: e.desktop ?? 'success' };
  const outputs: Record<string, string> = { publish: e.publish ?? 'true', full: e.full ?? 'true' };
  const js = cond
    .replace(/\bneeds\.([\w-]+)\.result\b/g, (_, n: string) => JSON.stringify(results[n] ?? ''))
    .replace(/\bneeds\.plan\.outputs\.(\w+)\b/g, (_, o: string) => JSON.stringify(outputs[o] ?? ''))
    .replace(/\bcancelled\(\)/g, String(!!e.cancelled));
  const rest = js
    .replace(/'[^']*'|"[^"]*"/g, '')
    .replace(/\b(?:true|false)\b/g, '')
    .replace(/[\s()!=&|]/g, '');
  expect(rest, `a condition this test cannot read: ${cond}`).toBe('');
  const strict = js.replace(/'/g, '"').replace(/!=/g, '!==').replace(/(?<![!=])==(?!=)/g, '===');
  return !!new Function(`return (${strict});`)();
}

/** Runs one step's script with bash: its output, exit code, and what it wrote to $GITHUB_ENV and the summary. */
function runStep(stepText: string, env: Record<string, string>, assets: string[] = []) {
  const tmp = fs.mkdtempSync(path.join(os.tmpdir(), 'aur-step-'));
  try {
    const bin = path.join(tmp, 'bin');
    fs.mkdirSync(bin);
    fs.writeFileSync(path.join(tmp, 'assets'), assets.map((a) => `${a}\n`).join(''));
    const fwd = (p: string) => p.split(path.sep).join('/');
    fs.writeFileSync(
      path.join(bin, 'gh'),
      [
        '#!/usr/bin/env bash',
        `dir="${fwd(tmp)}"`,
        'echo "gh $*" >> "$dir/calls.log"',
        'case "$1 $2" in',
        '  "release view") cat "$dir/assets"; exit 0 ;;',
        'esac',
        'exit 1',
        '',
      ].join('\n'),
    );
    fs.chmodSync(path.join(bin, 'gh'), 0o755);
    const script = path.join(tmp, 'step.sh');
    fs.writeFileSync(script, scriptOf(stepText));
    const ghEnv = path.join(tmp, 'github-env');
    const summary = path.join(tmp, 'summary');
    const r = spawnSync(bash!, [fwd(script)], {
      encoding: 'utf8',
      env: {
        ...process.env,
        AUR_SSH_PRIVATE_KEY: '',
        PATH: `${bin}${path.delimiter}${process.env.PATH}`,
        GITHUB_ENV: fwd(ghEnv),
        GITHUB_STEP_SUMMARY: fwd(summary),
        GITHUB_REF_NAME: 'v0.57.0',
        GITHUB_REPOSITORY: 'BRF-Tech/filex',
        ...env,
      },
    });
    const read = (f: string) => (fs.existsSync(f) ? fs.readFileSync(f, 'utf8') : '');
    return { code: r.status, out: `${r.stdout}${r.stderr}`, stdout: r.stdout, env: read(ghEnv), summary: read(summary), calls: read(path.join(tmp, 'calls.log')) };
  } finally {
    fs.rmSync(tmp, { recursive: true, force: true });
  }
}

const LINUX = ['filex-desktop-amd64.deb', 'filex-desktop-x86_64.AppImage'];

describe('the AUR package needs the Linux packages, and says when it has no key (#145)', () => {
  it.runIf(!DIR)('has no workflows to read here (they live in the public checkout)', () => {
    expect(DIR).toBeUndefined();
  });

  it.runIf(!!DIR)('the AUR waits for every desktop row to end and runs past a Windows row that failed, never past verify (#145)', () => {
    const aur = job(code(), 'aur');
    expect(needs(aur)).toEqual(expect.arrayContaining(['plan', 'verify', 'desktop']));
    const cond = jobIf(aur);
    // A status function, so a failed desktop row does not skip it; and with
    // one, every wait the implicit success() gave is named outright.
    expect(cond).toMatch(/!cancelled\(\)/);
    expect(cond).not.toMatch(/\balways\(\)/);
    const terms = topLevelTerms(cond);
    for (const t of ["needs.plan.result == 'success'", "needs.verify.result == 'success'", "needs.plan.outputs.publish == 'true'", "needs.plan.outputs.full == 'true'", "needs.desktop.result != 'skipped'"]) {
      expect(terms, t).toContain(t);
    }
    expect(terms, 'the whole desktop matrix green, again').not.toContain("needs.desktop.result == 'success'");
    // Every row green; a Windows row red while the Linux row published.
    expect(aurStarts(cond, {})).toBe(true);
    expect(aurStarts(cond, { desktop: 'failure' })).toBe(true);
    // Never past verify, never when the desktop job did not run at all, never
    // a cancelled run, a dry run or a run that only adds the ARM packages.
    expect(aurStarts(cond, { verify: 'failure', desktop: 'skipped' })).toBe(false);
    expect(aurStarts(cond, { verify: 'failure', desktop: 'failure' })).toBe(false);
    expect(aurStarts(cond, { desktop: 'skipped' })).toBe(false);
    expect(aurStarts(cond, { cancelled: true })).toBe(false);
    expect(aurStarts(cond, { publish: 'false' })).toBe(false);
    expect(aurStarts(cond, { full: 'false' })).toBe(false);
    expect(aurStarts(cond, { plan: 'failure' })).toBe(false);
  });

  it.runIf(!!DIR && !!bash)('without its key the AUR job says "skipped: no AUR key", stays green and does nothing else (#145)', () => {
    const aur = job(code(), 'aur');
    // The key is the job's: the first step reads it, the push step uses it.
    expect(aur).toMatch(/\n {4}env:\n {6}AUR_SSH_PRIVATE_KEY: \$\{\{ secrets\.AUR_SSH_PRIVATE_KEY \}\}\n/);
    const steps = stepsOf(aur);
    expect(steps[0].name, 'the key is asked about first').toBe(KEY_STEP);
    expect(steps[0].cond, 'the key step always runs').toBe('');
    const none = runStep(steps[0].text, { AUR_SSH_PRIVATE_KEY: '' });
    expect(none.code).toBe(0);
    expect(none.stdout.split('\n')).toContain(SKIPPED);
    expect(none.out).toMatch(/::notice title=AUR::skipped: no AUR key/);
    expect(none.out, 'a missing key is no warning and no error').not.toMatch(/::(?:warning|error)/);
    expect(none.env).toMatch(/^AUR_SKIP=1$/m);
    expect(none.summary).toContain(SKIPPED);
    const some = runStep(steps[0].text, { AUR_SSH_PRIVATE_KEY: 'a key' });
    expect(some.code).toBe(0);
    expect(some.out).not.toContain(SKIPPED);
    expect(some.env).not.toMatch(/AUR_SKIP/);
    // Everything after it is skipped without the key, and runs with it.
    for (const s of steps.slice(1)) {
      expect(holds(s.cond, { AUR_SKIP: '1' }, {}), `${s.name} runs without the key`).toBe(false);
      expect(holds(s.cond, {}, {}), `${s.name} does not run with the key`).toBe(true);
    }
    // And the old warning that read like a failure is gone.
    expect(aur).not.toMatch(/AUR: not pushed/);
  });

  it.runIf(!!DIR && !!bash)('the AUR job goes on only when the Release has the Linux packages it repackages (#145)', () => {
    const aur = job(code(), 'aur');
    const names = stepsOf(aur).map((s) => s.name);
    expect(names.indexOf(LINUX_STEP), 'the Linux packages are asked for before the PKGBUILD is made').toBeLessThan(names.indexOf('PKGBUILD and .SRCINFO for this tag'));
    expect(names.indexOf(LINUX_STEP)).toBeGreaterThan(0);
    const step = stepNamed(aur, LINUX_STEP);
    // Both there (beside the other rows' files): on.
    const ok = runStep(step.text, {}, [...LINUX, 'filex-desktop-x64.exe', 'filex-desktop-arm64.deb']);
    expect(ok.code).toBe(0);
    expect(ok.calls).toMatch(/gh release view v0\.57\.0 -R BRF-Tech\/filex --json assets/);
    // Either missing - the Linux row did not publish - red, and says which.
    for (const gone of LINUX) {
      const r = runStep(step.text, {}, LINUX.filter((f) => f !== gone).concat(`${gone}.blockmap`));
      expect(r.code, `${gone} missing`).not.toBe(0);
      expect(r.out).toContain(`::error title=AUR::v0.57.0 has no ${gone}`);
    }
  });

  it('the workflow patch is the documented one, on top of the winget one', () => {
    const patch = fs.readFileSync(path.join(REPO, 'packaging', 'ci', 'release-aur-linux-only.patch'), 'utf8');
    expect(patch).toMatch(/^\+\s+needs: \[plan, verify, desktop\]$/m);
    expect(patch).toMatch(/^\+\s+- name: The AUR key$/m);
    expect(patch).toMatch(/^\+\s+- name: The Linux packages are on the Release$/m);
    expect(patch).toMatch(/^\+\s+echo "skipped: no AUR key"$/m);
    const contributing = fs.readFileSync(path.join(REPO, 'docs', 'CONTRIBUTING.md'), 'utf8');
    expect(contributing).toMatch(/\n10\. `release-aur-linux-only\.patch` \(#145/);
  });
});
