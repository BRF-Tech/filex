// Reading release.yml for the guards of the runs that add packages to an
// existing release (only=macos, only=snap-arm64): releaseMacosOnly.test.ts
// and releaseSnapArm64Only.test.ts. One copy of the readers, the condition
// evaluator and verify's fake `gh`, so the two read the workflow the same way.
//
// The workflows live in the PUBLIC checkout only (see
// releaseGatesImages.test.ts): read from `<repo>/.github/workflows` or
// FILEX_WORKFLOWS_DIR. DIR is undefined when neither exists, and the tests
// that use these skip.
import { spawnSync } from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { expect } from 'vitest';

import { findBash, slash } from '../../../scripts/release/engine.mjs';

export const REPO = path.resolve(__dirname, '../../..');
export const DIR = [process.env.FILEX_WORKFLOWS_DIR, path.join(REPO, '.github', 'workflows')].find(
  (d) => d && fs.existsSync(path.join(d, 'release.yml')),
);
export const bash = findBash();

export type Row = Record<string, string>;

/** release.yml without comment-only lines, LF. */
export const code = () =>
  fs
    .readFileSync(path.join(DIR!, 'release.yml'), 'utf8')
    .split(/\r?\n/)
    .filter((l) => !/^\s*#/.test(l))
    .join('\n');

/** The lines of one job, up to the next job. */
export function job(text: string, name: string): string {
  const lines = text.split('\n');
  const start = lines.findIndex((l) => l === `  ${name}:`);
  expect(start, `job "${name}"`).toBeGreaterThanOrEqual(0);
  const next = lines.findIndex((l, i) => i > start && /^ {2}[A-Za-z0-9_-]+:\s*$/.test(l));
  return lines.slice(start, next < 0 ? undefined : next).join('\n');
}

/** Every job of the workflow, in order. */
export function jobNames(text: string): string[] {
  const lines = text.split('\n');
  const start = lines.findIndex((l) => l === 'jobs:');
  return lines.slice(start + 1).map((l) => /^ {2}([A-Za-z0-9_-]+):\s*$/.exec(l)?.[1]).filter((n): n is string => !!n);
}

/** A condition without its ${{ }}. */
const unwrap = (c: string) => c.trim().replace(/^\$\{\{\s*/, '').replace(/\s*\}\}$/, '');

/** The `if:` of a job (its own key, four spaces in), or ''. */
export const jobIf = (block: string) => unwrap(/\n {4}if:\s*(.+)/.exec(block)?.[1] ?? '');

/** The jobs a job's `needs:` names. */
function needs(block: string): string[] {
  const inline = /\n {4}needs:\s*(.+)/.exec(block)?.[1] ?? '';
  return inline.replace(/[[\]]/g, '').split(',').map((s) => s.trim()).filter(Boolean);
}

/**
 * The `&&` terms of a condition that must hold whatever its `||` groups say:
 * a parenthesised group demands nothing on its own.
 */
export function topLevelTerms(cond: string): string[] {
  let s = cond;
  for (let prev = ''; prev !== s; ) {
    prev = s;
    s = s.replace(/\([^()]*\)/g, '[]');
  }
  return s.split('&&').map((t) => t.trim());
}

/** The steps of a job: name, text, and `if:` (at the step's own key indent). */
export function stepsOf(block: string) {
  return block
    // [ \t]+, not \s+: \s also matches a newline, so a blank line before a
    // step split off an empty piece and the name lookup below threw.
    .split(/\n(?=[ \t]+- (?:name|uses|run):)/)
    .slice(1)
    .map((text) => {
      const lines = text.split('\n');
      const ind = lines[0].indexOf('-') + 2;
      const name = /- (?:name|uses|run):\s*(.+)/.exec(lines[0])![1].trim();
      const ifLine = lines.find((l) => l.startsWith(`${' '.repeat(ind)}if:`));
      return { name, text, cond: ifLine ? unwrap(ifLine.slice(ind + 3)) : '' };
    });
}

/** One step of a job, by name. */
export function stepNamed(block: string, name: string) {
  const s = stepsOf(block).find((x) => x.name === name);
  expect(s, `step "${name}"`).toBeTruthy();
  return s!;
}

/**
 * A step's `if:` for one matrix row, evaluated as GitHub would: the few
 * operators and functions these conditions use, and nothing else (anything
 * more fails the test instead of being guessed at). An env variable the job
 * does not set reads as '', as a missing secret does on GitHub.
 */
export function holds(cond: string, env: Record<string, string>, matrix: Row): boolean {
  if (!cond) return true;
  const rest = cond
    .replace(/'[^']*'/g, '')
    .replace(/\b(?:env|matrix)\.[A-Za-z_]+\b/g, '')
    .replace(/\b(?:contains|startsWith|always)\b/g, '')
    .replace(/[\s()!=&|,]/g, '');
  expect(rest, `a condition this test cannot read: ${cond}`).toBe('');
  const js = cond.replace(/!=/g, '!==').replace(/(?<![!=])==(?!=)/g, '===');
  const f = new Function('env', 'matrix', 'contains', 'startsWith', 'always', `return (${js});`);
  const vars = new Proxy(env, { get: (o, k) => (typeof k === 'string' && k in o ? o[k] : '') });
  return !!f(
    vars,
    matrix,
    (a: unknown, b: string) => String(a).includes(b),
    (a: unknown, b: string) => String(a).startsWith(b),
    () => true,
  );
}

/** Whatever makes something public from the desktop job. */
export const PUBLISHES = /gh release upload|snapcraft upload|msstore-submit|microsoft-store-apppublisher|wingetcreate|winget-cla\.sh|winget-supersede\.sh|git push/;

/**
 * What marks a step of the desktop job that BUILDS something: a tag run that
 * promotes runs none of them (releasePromote.test.ts), and neither do the
 * windows and linux rows of only=stores (releaseStoresOnly.test.ts).
 */
export const BUILDS = [
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
export const builds = (text: string) => BUILDS.some((re) => re.test(text));

/** The desktop job's env for one run, as plan's outputs set it. */
export function desktopEnv(run: { publish: boolean; full: boolean; only: string }, row: Row, secrets: Record<string, string> = {}) {
  return {
    TAG: 'v1.2.3',
    VER: '1.2.3',
    PUBLISH: String(run.publish),
    FULL: String(run.full),
    ONLY: run.only,
    LABEL: row.label,
    ARCHES: row.arches,
    ...secrets,
  };
}

/** Every secret the desktop job reads, set. */
export const KEYS = {
  HOMEBREW_TAP_DEPLOY_KEY: 'key',
  WINGET_TOKEN: 'token',
  MSSTORE_CLIENT_SECRET: 'secret',
  MSSTORE_PRODUCT_ID: 'product',
};

/** The desktop steps that run for one row, and the publishing ones among them. */
export function desktopRuns(release: string, env: Record<string, string>, row: Row) {
  const runs = stepsOf(job(release, 'desktop')).filter((s) => holds(s.cond, env, row));
  return {
    runs: runs.map((s) => s.name),
    steps: runs,
    publishing: runs.filter((s) => PUBLISHES.test(s.text)).map((s) => s.name),
  };
}

/** The desktop matrices plan writes: only=arm64's, then the full one. */
export function matrices(plan: string) {
  return [...plan.matchAll(/desktop='(\{[\s\S]*?\})'/g)].map((m) => ({ at: m.index!, rows: JSON.parse(m[1]).include as Row[] }));
}

/**
 * Where plan narrows the full matrix for one only= value:
 *   if [ "$only" = <only> ]; then desktop=$(echo "$desktop" | jq -c '<filter>'); fi
 * Its position in the plan job's text and the jq filter, or null.
 */
export function narrowing(plan: string, only: string) {
  const esc = only.replace(/[-]/g, '\\-');
  const m = new RegExp(`\\n\\s+if \\[ "\\$only" = ${esc} \\]; then\\n\\s+desktop=\\$\\(echo "\\$desktop" \\| jq -c '([^']+)'\\)\\n\\s+fi\\n`).exec(plan);
  return m ? { at: m.index, filter: m[1] } : null;
}

/** Runs a jq filter where jq is at hand (GitHub's runners); undefined elsewhere. */
export function jq(filter: string, input: unknown): unknown {
  const r = spawnSync('jq', ['-c', filter], { input: JSON.stringify(input), encoding: 'utf8' });
  return r.error ? undefined : JSON.parse(r.stdout);
}

/**
 * What plan says about a run that adds to a release: it needs a tag, it may
 * publish onto a Release that exists, it is no full run, and every job can
 * read what was asked (`only`).
 */
export function expectAddsToARelease(release: string, only: string) {
  const on = release.slice(0, release.indexOf('\njobs:'));
  const options = /only:\s*\n\s+description:([^\n]*)\n\s+type: choice\s*\n\s+options: \[([^\]]*)\]\s*\n\s+default: all/.exec(on);
  expect(options, 'inputs.only is a choice, all by default').not.toBeNull();
  expect(options![2].split(',').map((s) => s.trim())).toContain(only);
  expect(options![1], 'the input says what it does').toContain(`${only}:`);
  const plan = job(release, 'plan');
  const esc = only.replace(/[-]/g, '\\-');
  // No tag: refused, as only=arm64 is.
  expect(plan).toMatch(new RegExp(`if \\[ "\\$only" = ${esc} \\] && \\[ -z "\\$tag" \\]; then\\s+echo "::error::only=${esc} adds to a release: give its tag[^"]*"; exit 1\\s+fi`));
  // publish=true by hand: one of the runs that add to a release, onto a Release that exists.
  const guard = /if \[ "\$EVENT" != push \] && \[ "\$publish" = true \]; then\n([\s\S]*?)\n\s+fi\n/.exec(plan)?.[1] ?? '';
  const refuse = guard.split('\n').find((l) => l.includes('a full release publishes from its tag push only')) ?? '';
  expect(refuse.trim()).toMatch(/^(?:\[ "\$only" = [\w-]+ \] \|\| )+\{ echo "::error::/);
  const allowed = [...refuse.matchAll(/\[ "\$only" = ([\w-]+) \] \|\|/g)].map((m) => m[1]);
  expect(allowed).toEqual(expect.arrayContaining(['arm64', only]));
  expect(allowed, 'never a full run').not.toContain('all');
  expect(guard).toMatch(/gh release view "\$tag"/);
  // Not a full run (full is true for only=all alone), and every job can ask what was asked.
  expect(plan).toMatch(/echo "full=\$\(\[ "\$only" = all \] && echo true \|\| echo false\)"/);
  expect(plan).toMatch(/\n\s+echo "only=\$only"\n/);
  expect(plan).toMatch(/\n\s+only: \$\{\{ steps\.p\.outputs\.only \}\}\n/);
  // Named "publish <only> v1.2.3 <commit>" (or "dry run <only> ...") by run-name.
  const runName = release.split('\n').find((l) => l.startsWith('run-name:')) ?? '';
  expect(runName).toContain("inputs.publish && 'publish' || 'dry run', inputs.only, inputs.tag && format('{0} ', inputs.tag) || ''");
}

/**
 * The jobs that can run when full is 'false' and only is `only`. A job is left
 * out when a term all of its condition needs is false in that run, or - with
 * the implicit success() - when a job it needs is left out.
 */
export function jobsThatRun(release: string, only: string): string[] {
  const falseHere = ["needs.plan.outputs.full == 'true'", `needs.plan.outputs.only != '${only}'`];
  const leftOut = (name: string): boolean => {
    const block = job(release, name);
    const cond = jobIf(block);
    if (topLevelTerms(cond).some((t) => falseHere.includes(t))) return true;
    const free = /\b(?:always|cancelled|failure|success)\(\)/.test(cond);
    return !free && needs(block).some((n) => n !== 'plan' && leftOut(n));
  };
  return jobNames(release).filter((n) => !leftOut(n));
}

/** verify's script, dedented, as bash runs it. */
export function verifyScript(release: string): string {
  const lines = job(release, 'verify').split('\n');
  const at = lines.findIndex((l) => /^\s+run: \|\s*$/.test(l));
  expect(at, 'verify has a run: | block').toBeGreaterThan(0);
  const key = lines[at].length - lines[at].trimStart().length;
  const body: string[] = [];
  let indent = -1;
  for (const l of lines.slice(at + 1)) {
    if (!l.trim()) {
      body.push('');
      continue;
    }
    const ind = l.length - l.trimStart().length;
    if (ind <= key) break;
    if (indent < 0) indent = ind;
    body.push(l.slice(indent));
  }
  return `${body.join('\n')}\n`;
}

export const TAG_SHA = '7630a4e815f61fd4c0781a65a6a0139d930e8675';
export const RUN_SHA = 'b67fe6e1b67fe6e1b67fe6e1b67fe6e1b67fe6e1';

/** What the fake `gh` answers verify (see `verify` below). */
export interface VerifyAnswers {
  /** A successful ci.yml run started by a push of the commit (run 1). */
  ci?: boolean;
  /** A successful dry run named "dry run all <commit>" (run 2). */
  dry?: boolean;
  /** Asking for the tag's commit fails. */
  commitFails?: boolean;
  /** Run 1 has a successful "All tests (full)" job (#174). Default: when `ci`. */
  full?: boolean;
  /** The artifacts run 2 kept, by name (#174: what a tag run promotes). */
  artifacts?: string[];
  /**
   * What CircleCI says of the commit (#181), through a stand-in for
   * circleci-green.mjs: `green` (exit 0 and a workflow's page), `red` (the
   * default: exit 1), `running` (exit 1), `error` (exit 2, not asked).
   */
  circleci?: 'green' | 'red' | 'running' | 'error';
  /** CIRCLECI_TOKEN as the job hands it over (default: empty). */
  circleciToken?: string;
  /**
   * Whether the tag has a GitHub Release already (#181: one packaged off
   * GitHub): `yes`, `error` (GitHub does not answer), or not (the default:
   * 404).
   */
  release?: 'yes' | 'error';
}

/** The stand-in for circleci-green.mjs that `verify` below runs instead of asking CircleCI. */
const CIRCLECI_STAND_IN = [
  "import fs from 'node:fs';",
  "fs.appendFileSync(process.env.FAKE_CC_LOG, JSON.stringify({ argv: process.argv.slice(2), token: process.env.CIRCLECI_TOKEN || '' }) + '\\n');",
  "const answer = process.env.FAKE_CC || 'red';",
  "if (answer === 'green') {",
  "  console.log('https://app.circleci.com/pipelines/gh/BRF-Tech/filex/7/workflows/wf-7');",
  "} else if (answer === 'error') {",
  "  console.error('could not ask CircleCI (GET /project/gh/BRF-Tech/filex/pipeline?branch=main answered 401): unknown does not pass');",
  '  process.exitCode = 2;',
  '} else {',
  "  console.error('the workflow \"ci\" ended \"' + (answer === 'running' ? 'running' : 'failed') + '\"');",
  '  process.exitCode = 1;',
  '}',
  '',
].join('\n');

/**
 * Runs verify's own script against a fake `gh` that answers the commit of the
 * tag and, when told to, a successful CI run, its full matrix's last job, a
 * dry run and the artifacts it kept, and a stand-in for the CircleCI reader
 * (#181) in its CI_SCRIPTS. Returns the exit code, every `gh` call (with what
 * WANT and SHA were at the time), every call of the CircleCI reader (its
 * arguments and the token it was handed), the step summary and what the step
 * wrote to $GITHUB_OUTPUT. Nothing here reaches GitHub or CircleCI.
 */
export function verify(env: Record<string, string>, answers: VerifyAnswers = {}) {
  const tmp = fs.mkdtempSync(path.join(os.tmpdir(), 'release-verify-'));
  try {
    const script = path.join(tmp, 'verify.sh');
    fs.writeFileSync(script, verifyScript(code()));
    const scripts = path.join(tmp, 'ci-scripts');
    fs.mkdirSync(scripts);
    fs.writeFileSync(path.join(scripts, 'circleci-green.mjs'), CIRCLECI_STAND_IN);
    const ccLog = path.join(tmp, 'circleci.log');
    fs.writeFileSync(
      path.join(tmp, 'gh'),
      [
        '#!/usr/bin/env bash',
        'printf \'%s|WANT=%s|SHA=%s\\n\' "$*" "${WANT:-}" "${SHA:-}" >> "$FAKE_GH_LOG"',
        'case "$*" in',
        '  *"/commits/"*) [ -n "$FAKE_COMMIT_FAILS" ] && exit 1; echo "$FAKE_TAG_SHA" ;;',
        '  *"/actions/runs/"*"/jobs"*) [ -n "$FAKE_FULL" ] && echo "All tests (full)" ;;',
        '  *"/actions/runs/"*"/artifacts"*) [ -n "$FAKE_ARTIFACTS" ] && printf \'%s\\n\' "$FAKE_ARTIFACTS" ;;',
        '  *"workflows/ci.yml/runs"*) [ -n "$FAKE_CI" ] && echo 1 ;;',
        '  *"workflows/release.yml/runs"*) [ -n "$FAKE_DRY" ] && echo 2 ;;',
        '  *"/releases/tags/"*)',
        '    case "$FAKE_RELEASE" in',
        '      yes) echo "https://github.com/BRF-Tech/filex/releases/tag/v1.2.3" ;;',
        '      error) echo "gh: Bad Gateway (HTTP 502)" >&2; exit 1 ;;',
        '      *) echo "gh: Not Found (HTTP 404)" >&2; exit 1 ;;',
        '    esac ;;',
        '  *) echo "unexpected: gh $*" >&2; exit 3 ;;',
        'esac',
        'exit 0',
        '',
      ].join('\n'),
    );
    fs.chmodSync(path.join(tmp, 'gh'), 0o755);
    const log = path.join(tmp, 'gh.log');
    const summary = path.join(tmp, 'summary.md');
    const output = path.join(tmp, 'output.txt');
    const r = spawnSync(bash!, [script], {
      encoding: 'utf8',
      env: {
        ...process.env,
        PATH: `${tmp}${path.delimiter}${path.dirname(process.execPath)}${path.delimiter}${process.env.PATH}`,
        GITHUB_REPOSITORY: 'BRF-Tech/filex',
        CI_SCRIPTS: slash(scripts),
        CIRCLECI_PROJECT: 'gh/BRF-Tech/filex',
        CIRCLECI_TOKEN: answers.circleciToken ?? '',
        FAKE_CC: answers.circleci ?? '',
        FAKE_CC_LOG: slash(ccLog),
        GITHUB_STEP_SUMMARY: slash(summary),
        GITHUB_OUTPUT: slash(output),
        GH_TOKEN: 'not-a-token',
        FAKE_GH_LOG: slash(log),
        FAKE_TAG_SHA: TAG_SHA,
        FAKE_CI: answers.ci ? '1' : '',
        FAKE_DRY: answers.dry ? '1' : '',
        FAKE_FULL: (answers.full ?? answers.ci) ? '1' : '',
        FAKE_ARTIFACTS: (answers.artifacts ?? []).join('\n'),
        FAKE_COMMIT_FAILS: answers.commitFails ? '1' : '',
        FAKE_RELEASE: answers.release ?? '',
        ...env,
      },
    });
    const outputs: Record<string, string> = {};
    if (fs.existsSync(output)) {
      for (const line of fs.readFileSync(output, 'utf8').split('\n').filter(Boolean)) {
        const at = line.indexOf('=');
        if (at > 0) outputs[line.slice(0, at)] = line.slice(at + 1);
      }
    }
    const circleciCalls: Array<{ argv: string[]; token: string }> = fs.existsSync(ccLog)
      ? fs
          .readFileSync(ccLog, 'utf8')
          .split('\n')
          .filter(Boolean)
          .map((l) => JSON.parse(l))
      : [];
    return {
      code: r.status,
      out: `${r.stdout}${r.stderr}`,
      calls: fs.existsSync(log) ? fs.readFileSync(log, 'utf8').split('\n').filter(Boolean) : [],
      circleciCalls,
      summary: fs.existsSync(summary) ? fs.readFileSync(summary, 'utf8') : '',
      outputs,
    };
  } finally {
    fs.rmSync(tmp, { recursive: true, force: true });
  }
}

/** verify's env in a run started by hand that adds to v1.2.3. */
export const verifyRun = (only: string, publish: boolean) => ({
  EVENT: 'workflow_dispatch',
  SHA: RUN_SHA,
  ONLY: only,
  PUBLISH: String(publish),
  TAG: 'v1.2.3',
});

/**
 * verify, publishing for `only`, asks exactly what it asks for only=arm64: the
 * tag's commit (never the run's), CI started by its push and "dry run all
 * <that commit>"; either missing, or the commit unknown, is red.
 */
export function expectVerifyAsksAsForArm64(only: string) {
  const asked: Record<string, string[]> = {};
  for (const o of ['arm64', only]) {
    const both = verify(verifyRun(o, true), { ci: true, dry: true });
    expect(both.code, `${o}: ${both.out}`).toBe(0);
    asked[o] = both.calls;
    expect(both.calls[0]).toMatch(/^api repos\/BRF-Tech\/filex\/commits\/v1\.2\.3 --jq \.sha\|/);
    const ci = both.calls.find((c) => c.includes('workflows/ci.yml/runs')) ?? '';
    const dry = both.calls.find((c) => c.includes('workflows/release.yml/runs')) ?? '';
    expect(ci).toContain(`-f head_sha=${TAG_SHA} -f event=push -f status=success`);
    expect(dry).toContain(`-f head_sha=${TAG_SHA} -f event=workflow_dispatch -f status=success`);
    expect(dry).toContain(`|WANT=dry run all ${TAG_SHA}|SHA=${TAG_SHA}`);
    expect(`${ci}\n${dry}`).not.toContain(RUN_SHA);
    expect(both.summary).toContain('was tested before it was tagged');
    for (const answers of [{ ci: true }, { dry: true }, {}]) {
      const r = verify(verifyRun(o, true), answers);
      expect(r.code, `${o} ${JSON.stringify(answers)}`).not.toBe(0);
      expect(r.out).toMatch(/::error::v1\.2\.3 publishes nothing/);
    }
    const unknown = verify(verifyRun(o, true), { ci: true, dry: true, commitFails: true });
    expect(unknown.code).not.toBe(0);
    expect(unknown.calls).toHaveLength(1);
  }
  expect(asked[only]).toEqual(asked.arm64);
}

/** A dry run (publish off) of `only` passes verify without asking GitHub anything, as one of only=arm64 does. */
export function expectDryRunLetThrough(only: string) {
  for (const o of ['arm64', only]) {
    const r = verify(verifyRun(o, false));
    expect(r.code, r.out).toBe(0);
    expect(r.calls).toEqual([]);
  }
}
