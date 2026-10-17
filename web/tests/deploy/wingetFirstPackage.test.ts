// The desktop app on winget waits for its moderator (task #68): while
// microsoft/winget-pkgs does not have BRFTech.filex-app and our first
// pull request for it is open, the release does not touch the package at
// all - no new pull request, no update of the open one, no CLA comment, no
// closing - and says so in one line
// (.github/workflows/scripts/winget-pending.sh). Once winget has it, every
// release submits a new version, as the CLI's (BRFTech.filex) always does.
//
// ⚠ Why (the owner's decision of 2026-10-16): fourteen pull requests of
// BRFTech.filex-app passed validation, and none was approved - a first
// submission ("New-Package") waits for a moderator, and each release opened a
// new one and closed the one before (winget-supersede.sh), so the queue
// started again every time. #451746 (0.56.3) waits now; nothing may move it.
//
// The scripts live in the PUBLIC checkout only (releaseGatesImages.test.ts);
// the change reaches it as packaging/ci/release-winget-first-package.patch,
// and the release tool names this file's workflow tests in its guards.
import { spawnSync } from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { describe, expect, it } from 'vitest';

import { wingetWaits } from '../../../scripts/release/plan.mjs';
import { DIR, REPO, WINGET_PUBLISHING, bash, code, job, stepNamed, stepsOf } from '../helpers/releaseWorkflow';

const ID = 'BRFTech.filex-app';
const CHECK = 'Does the desktop app wait for a winget moderator?';

interface Pr {
  number: number;
  title: string;
}

/**
 * Runs winget-pending.sh against a fake `gh` and `git`. `onWinget`: yes,
 * no (HTTP 404) or unknown (HTTP 502). Returns what it printed, wrote to
 * $GITHUB_OUTPUT, $GITHUB_ENV and the summary, and every gh and git call.
 */
function pending(ver: string, prs: Pr[], onWinget: 'yes' | 'no' | 'unknown') {
  const tmp = fs.mkdtempSync(path.join(os.tmpdir(), 'winget-pending-'));
  const fwd = (p: string) => p.split(path.sep).join('/');
  try {
    const bin = path.join(tmp, 'bin');
    fs.mkdirSync(bin);
    // Oldest first, as `sort_by(.createdAt)` gives them.
    fs.writeFileSync(path.join(tmp, 'open.tsv'), prs.map((p) => `${p.number}\t${p.title}\n`).join(''));
    fs.writeFileSync(path.join(tmp, 'on-winget'), onWinget);
    const fake = (name: string, body: string[]) => {
      fs.writeFileSync(path.join(bin, name), ['#!/usr/bin/env bash', `dir="${fwd(tmp)}"`, `echo "${name} $*" >> "$dir/calls.log"`, ...body, ''].join('\n'));
      fs.chmodSync(path.join(bin, name), 0o755);
    };
    fake('gh', [
      'case "$1 $2" in',
      '  "api "*) case "$(cat "$dir/on-winget")" in yes) echo 3; exit 0 ;; no) echo "gh: Not Found (HTTP 404)" >&2; exit 1 ;; *) echo "gh: Bad Gateway (HTTP 502)" >&2; exit 1 ;; esac ;;',
      '  "pr list") cat "$dir/open.tsv"; exit 0 ;;',
      'esac',
      'exit 0',
    ]);
    fake('git', ['exit 0']);
    const out = path.join(tmp, 'github-output');
    const env = path.join(tmp, 'github-env');
    const summary = path.join(tmp, 'summary');
    const r = spawnSync(bash!, [path.join(DIR!, 'scripts', 'winget-pending.sh'), ID, ver], {
      encoding: 'utf8',
      env: { ...process.env, PATH: `${bin}${path.delimiter}${process.env.PATH}`, GITHUB_OUTPUT: fwd(out), GITHUB_ENV: fwd(env), GITHUB_STEP_SUMMARY: fwd(summary) },
    });
    const read = (f: string) => (fs.existsSync(f) ? fs.readFileSync(f, 'utf8') : '');
    return {
      code: r.status,
      stdout: r.stdout,
      out: `${r.stdout}${r.stderr}`,
      output: read(out),
      env: read(env),
      summary: read(summary),
      calls: read(path.join(tmp, 'calls.log')).split('\n').filter(Boolean),
    };
  } finally {
    fs.rmSync(tmp, { recursive: true, force: true });
  }
}

/** Every call that would change something on winget: a push, a pull request edited, commented on, closed or opened. */
const touches = (calls: string[]) => calls.filter((c) => c.startsWith('git ') || /^gh pr (?:edit|comment|close|create|review)/.test(c) || /^gh api .*-X (?:POST|PATCH|PUT|DELETE)/.test(c));

describe('the desktop app on winget waits for its moderator (#68)', () => {
  it.runIf(!!DIR)('leaves the desktop app alone on winget while its first pull request waits for a moderator', () => {
    const desktop = job(code(), 'desktop');
    const steps = stepsOf(desktop).map((s) => s.name);
    // In the order the windows row runs them: the question first, then what it may skip.
    const at = [CHECK, ...WINGET_PUBLISHING].map((n) => steps.indexOf(n));
    for (const [i, a] of at.entries()) expect(a, [CHECK, ...WINGET_PUBLISHING][i]).toBeGreaterThanOrEqual(0);
    expect([...at].sort((a, b) => a - b)).toEqual(at);
    const check = stepNamed(desktop, CHECK);
    expect(check.text).toMatch(/bash "\$CI_SCRIPTS"\/winget-pending\.sh BRFTech\.filex-app "\$\{VER\}"\s*$/m);
    expect(check.text).toContain('GH_TOKEN: ${{ secrets.WINGET_TOKEN }}');
    // While it waits: no new pull request, no CLA comment, no closing.
    for (const name of WINGET_PUBLISHING) expect(stepNamed(desktop, name).cond, name).toBe(`${check.cond} && env.WINGET_DESKTOP_SKIP == ''`);
    // The script asks; it changes nothing on winget, and is never red.
    const script = fs.readFileSync(path.join(DIR!, 'scripts', 'winget-pending.sh'), 'utf8');
    expect(script).not.toMatch(/^\s*set\s+-[a-z]*e/m);
    expect(script).not.toMatch(/\bexit\s+[1-9]/);
    expect(script).not.toMatch(/\bgit\s|gh pr (?:edit|comment|close|create)|wingetcreate|-X (?:POST|PATCH|PUT|DELETE)/);
    // The CLI's own winget steps do not ask: BRFTech.filex goes on as it does.
    expect(code()).not.toMatch(/winget-pending\.sh BRFTech\.filex "/);
  });

  it.runIf(!!DIR && !!bash)("says 'skipped: BRFTech.filex-app waits for a moderator on #<number>' while winget lacks it and our pull request is open, and touches nothing", () => {
    const prs = [
      { number: 451746, title: `New version: ${ID} 0.56.3` },
      { number: 451800, title: 'New version: BRFTech.filex 0.56.3' },
      { number: 452000, title: `New version: ${ID} 0.56.4` },
    ];
    const r = pending('0.57.0', prs, 'no');
    expect(r.code, r.out).toBe(0);
    const line = `skipped: ${ID} waits for a moderator on #451746`;
    expect(r.stdout.split('\n')[0]).toBe(line);
    expect(r.out).toContain(`::notice title=winget::${line}`);
    expect(r.output).toBe('pending=451746\n');
    expect(r.env).toBe(`WINGET_DESKTOP_SKIP=${line}\n`);
    expect(r.summary).toContain(`### winget: ${line}`);
    expect(touches(r.calls)).toEqual([]);
    // A first submission titled the way winget's own tool names it waits the same way.
    expect(pending('0.57.0', [{ number: 451900, title: `New package: ${ID} version 0.56.3` }], 'no').output).toBe('pending=451900\n');
  });

  it.runIf(!!DIR && !!bash)('once winget has the package, or with no first pull request open: nothing, and the release submits; when it cannot tell, it skips and says why', () => {
    const prs = [{ number: 451746, title: `New version: ${ID} 0.56.3` }];
    const on = pending('0.57.0', prs, 'yes');
    expect(on.code).toBe(0);
    expect(on.output).toBe('');
    expect(on.env).toBe('');
    expect(on.out).toMatch(/BRFTech\.filex-app is on winget \(manifests\/b\/BRFTech\/filex-app, 3 version\(s\)\): the release submits 0\.57\.0 as a new version/);
    expect(on.calls.some((c) => c.startsWith('gh pr list'))).toBe(false);
    const none = pending('0.57.0', [{ number: 451800, title: 'New version: BRFTech.filex 0.56.3' }], 'no');
    expect(none.output).toBe('');
    expect(none.env).toBe('');
    expect(none.out).toMatch(/no pull request of ours is open for it: the release opens its first/);
    const unknown = pending('0.57.0', prs, 'unknown');
    expect(unknown.code).toBe(0);
    expect(unknown.stdout).toMatch(/^skipped: could not ask whether BRFTech\.filex-app is on winget \(gh: Bad Gateway \(HTTP 502\)\)/);
    expect(unknown.env).toMatch(/^WINGET_DESKTOP_SKIP=skipped: could not ask whether/);
    expect(unknown.env.split('\n').filter(Boolean)).toHaveLength(1);
    for (const r of [on, none, unknown]) expect(touches(r.calls)).toEqual([]);
  });

  it("the release's read-back says the desktop app waits, in one line, instead of a red gate", () => {
    const gh = (onWinget: 'yes' | 'no' | 'unknown', list = '') => (args: string[]) =>
      args[0] === 'api'
        ? onWinget === 'yes'
          ? { status: 0, stdout: '3', stderr: '' }
          : { status: 1, stdout: '', stderr: onWinget === 'no' ? 'gh: Not Found (HTTP 404)' : 'gh: Bad Gateway (HTTP 502)' }
        : { status: 0, stdout: list, stderr: '' };
    expect(wingetWaits(ID, { gh: gh('no', `451746\tNew version: ${ID} 0.56.3\n452000\tNew version: ${ID} 0.56.4\n`) })).toEqual({ pr: '451746' });
    expect(wingetWaits(ID, { gh: gh('no', '451800\tNew version: BRFTech.filex 0.56.3\n') })).toEqual({});
    expect(wingetWaits(ID, { gh: gh('yes') })).toEqual({});
    expect(wingetWaits(ID, { gh: gh('unknown') }).error).toMatch(/HTTP 502/);
    // The ci stage's winget gate asks it when the version's pull request is not there.
    const plan = fs.readFileSync(path.join(REPO, 'scripts', 'release', 'plan.mjs'), 'utf8');
    expect(plan).toMatch(/const waits = id === 'BRFTech\.filex-app' \? wingetWaits\(id\) : null;/);
    expect(plan).toContain('lines.push(`${id}: skipped - waits for a moderator on #${waits.pr} (not on winget yet: nothing submitted, moved or closed)`);');
  });

  it('the workflow patch is the documented one, after the snap channels', () => {
    const patch = fs.readFileSync(path.join(REPO, 'packaging', 'ci', 'release-winget-first-package.patch'), 'utf8');
    expect(patch).toMatch(/^diff --git a\/\.github\/workflows\/scripts\/winget-pending\.sh b\/\.github\/workflows\/scripts\/winget-pending\.sh\nnew file mode 100755$/m);
    expect(patch).toMatch(/^diff --git a\/\.github\/workflows\/scripts\/winget-supersede\.sh /m);
    expect(patch).toMatch(/^\+\s+- name: Does the desktop app wait for a winget moderator\?$/m);
    expect(patch).not.toMatch(/^\+.*(?:WINGET_UPDATED_PR|git push)/m);
    const contributing = fs.readFileSync(path.join(REPO, 'docs', 'CONTRIBUTING.md'), 'utf8');
    expect(contributing).toMatch(/\n9\. `release-winget-first-package\.patch` \(#68/);
  });
});
