// The release plan of the THROWAWAY repository web/tests/deploy/releaseCli.test.ts
// builds. It is copied to <fixture>/scripts/release/plan.mjs, next to the
// real engine, stages and verify modules — so the test drives the real
// `pnpm release` and only the gates are the fixture's.
//
// Each gate is steered by an environment variable the test sets, so a test
// can make exactly one gate red and prove the release stops there. The
// published surfaces are files in a directory (FIXTURE_SITE) behind a fetch
// that never opens a socket.
//
// GitHub Actions is a stand-in too (`github` below), steered the same way:
//   FIXTURE_GH_CI    the ci.yml run of the push of the public main:
//                    success | failure | in_progress (unset: no run at all)
//   FIXTURE_GH_DRY   how the dry runs started by the gate end (same values)
//   FIXTURE_GH_NAME  "old": the workflow names its runs as before #76
// A dispatch is written to FIXTURE_GH/dispatched.json, on the commit the
// public remote's main is at that moment, as GitHub would run it.
//
// With FIXTURE_GH_MATRIX set the plan reads ci.yml part by part (#173/#174):
// it names the matrix's parts (PARTS), a heavy gate GitHub runs in two of
// them, and what the dry run must keep for the tag run to promote, and the
// stand-in answers the run's jobs and the dry run's artifacts:
//   FIXTURE_GH_PARTS      "<state>:<part>,…": a part that ended otherwise -
//                         failure | in_progress | missing; every other part
//                         is running while FIXTURE_GH_CI is, else success
//   FIXTURE_GH_ARTIFACTS  what the dry run kept, comma-separated (default:
//                         everything, macOS included)
//   FIXTURE_GH_STEPS      "<part>=<step>;…": the step a red part failed in
//                         (default "The part", a test step: the code's; `;`
//                         between parts, a step's name may hold a comma)
//   FIXTURE_GH_RERUN      how a part the gate asked GitHub to re-run ends
//                         (#165): success | failure (default failure); every
//                         `gh run rerun --failed` is written to
//                         FIXTURE_GH/reruns.json, and the run's attempt counts it
//   FIXTURE_GH_ERRORS     the first N questions about runs are answered with
//                         FIXTURE_GH_ERROR_TEXT (default "gh: Server Error
//                         (HTTP 502)"), as GitHub not answering
//   FIXTURE_GH_PULLS      the pull requests GitHub knows (--merge-pr), as JSON:
//                         { "<n>": { state, merged, base, label, title, head } };
//                         a head left out is the public remote's refs/pull/<n>/head
//
// CircleCI is a stand-in too (`circleci` below, task #181), for
// `--gate circleci`, answering with the real circleci.mjs's mapping:
//   FIXTURE_CC_CI     the CircleCI state of the workflow `ci` in the pipeline
//                     the push of the public main started: success | failed |
//                     failing | running (unset: no pipeline at all)
//   FIXTURE_CC_ERROR  CircleCI cannot be asked: every answer is an error
// Every question either CI is asked is counted in FIXTURE_GH/asked.json
// ({ github: n, circleci: n }), so a test shows which one the gate read.
//
// The preflight asks for the winget token as the real plan does (#290), with
// the real winget-token.mjs, but its token and GitHub's answer are stand-ins
// steered by FIXTURE_WINGET:
//   (unset)   200, public_repo, push to the fork, no expiry: green
//   expired   401 Bad credentials
//   expiring  green otherwise, and 3 days left
//   offline   GitHub cannot be reached
//   unset     FILEX_WINGET_TOKEN_CMD is not set: no command reads the token
// Every question it asks GitHub is counted in FIXTURE_GH/winget-asked.json.
//
// FIXTURE_DEFER set adds a pretag gate this machine cannot run, as the real
// plan's Store copy off Windows (#248): `deferred`, with the real acks'
// names, and a `check` that is red if anything ever runs it.

import { execFileSync } from 'node:child_process';
import fs from 'node:fs';
import path from 'node:path';
import process from 'node:process';

import { workflowRun, workflowUrl } from './circleci.mjs';
import { readWingetToken, wingetTokenGate } from './winget-token.mjs';
import { desktopFeeds, docsSite, readmePictures, runningRelease, updateManifest } from './verify.mjs';

const env = process.env;

async function fetch(url, init = {}) {
  const p = path.join(env.FIXTURE_SITE, decodeURIComponent(new URL(url).pathname));
  const file = [p, `${p}.html`].find((f) => fs.existsSync(f) && fs.statSync(f).isFile());
  if (!file) return new Response('not found', { status: 404 });
  return new Response(init.method === 'HEAD' ? null : fs.readFileSync(file), { status: 200 });
}

const redWhen = (name) => ['node', '-e', `process.exit(process.env.${name} ? 1 : 0)`];
const SITE = 'http://fixture.invalid';

const publicMain = () =>
  execFileSync('git', ['-C', env.FIXTURE_EXPORT, 'ls-remote', 'origin', 'refs/heads/main'], { encoding: 'utf8' }).split(/\s+/)[0];

/** Counts a question a CI was asked (FIXTURE_GH/asked.json: { github: n, circleci: n }). */
function asked(ci) {
  const f = path.join(env.FIXTURE_GH, 'asked.json');
  fs.mkdirSync(env.FIXTURE_GH, { recursive: true });
  const all = fs.existsSync(f) ? JSON.parse(fs.readFileSync(f, 'utf8')) : {};
  fs.writeFileSync(f, JSON.stringify({ ...all, [ci]: (all[ci] ?? 0) + 1 }));
}

function circleci() {
  const project = 'gh/fixture/fixture';
  return {
    project,
    async runs({ workflow, sha, branch }) {
      asked('circleci');
      if (env.FIXTURE_CC_ERROR) return { error: `GET /project/${project}/pipeline?branch=${branch} answered 503` };
      if (!env.FIXTURE_CC_CI || publicMain() !== sha) return { runs: [] };
      const wf = { id: 'wf-1', name: 'ci', status: env.FIXTURE_CC_CI };
      return { runs: wf.name === workflow ? [workflowRun(wf, { sha, url: workflowUrl(project, 7, wf.id) })] : [] };
    },
    where({ branch }) {
      return `https://circleci.invalid/${project}?branch=${branch}`;
    },
  };
}

/** The parts of the fixture's ci.yml matrix, and what its dry run keeps. */
const PARTS = ['Plan the matrix', 'Go (a)', 'Go (b)', 'All tests (full)'];
const KEPT = ['digests-amd64', 'digests-arm64', 'release-files-linux'];

/** FIXTURE_GH_PARTS as { part: state }. */
function partStates() {
  const out = {};
  for (const item of (env.FIXTURE_GH_PARTS ?? '').split(',').filter(Boolean)) {
    const at = item.indexOf(':');
    out[item.slice(at + 1)] = item.slice(0, at);
  }
  return out;
}

/** FIXTURE_GH_STEPS as { part: step }. */
function partSteps() {
  const out = {};
  for (const item of (env.FIXTURE_GH_STEPS ?? '').split(';').filter(Boolean)) {
    const at = item.indexOf('=');
    out[item.slice(0, at)] = item.slice(at + 1);
  }
  return out;
}

/** How often the gate asked GitHub to re-run run `id` (FIXTURE_GH/reruns.json). */
function rerunsOf(id) {
  const f = path.join(env.FIXTURE_GH, 'reruns.json');
  const all = fs.existsSync(f) ? JSON.parse(fs.readFileSync(f, 'utf8')) : {};
  return all[id] ?? 0;
}

/** Counts a question about runs; true while FIXTURE_GH_ERRORS says GitHub does not answer. */
function unanswered() {
  const f = path.join(env.FIXTURE_GH, 'errors.json');
  fs.mkdirSync(env.FIXTURE_GH, { recursive: true });
  const n = fs.existsSync(f) ? Number(fs.readFileSync(f, 'utf8')) : 0;
  fs.writeFileSync(f, String(n + 1));
  return n < Number(env.FIXTURE_GH_ERRORS || 0);
}

function github() {
  const file = () => path.join(env.FIXTURE_GH, 'dispatched.json');
  const dispatched = () => (fs.existsSync(file()) ? JSON.parse(fs.readFileSync(file(), 'utf8')) : []);
  const asRun = (id, sha, event, title, state) => ({
    id,
    headSha: sha,
    event,
    title,
    url: `https://github.invalid/runs/${id}`,
    status: state === 'in_progress' ? 'in_progress' : 'completed',
    conclusion: state === 'in_progress' ? '' : state,
    attempt: 1 + rerunsOf(id),
  });
  return {
    repo: 'fixture/fixture',
    runs({ workflow, sha, event }) {
      asked('github');
      if (unanswered()) return { error: env.FIXTURE_GH_ERROR_TEXT || 'gh: Server Error (HTTP 502)' };
      if (workflow === 'ci.yml' && event === 'push') {
        if (!env.FIXTURE_GH_CI || publicMain() !== sha) return { runs: [] };
        const state = rerunsOf(1) ? (env.FIXTURE_GH_RERUN ?? 'failure') : env.FIXTURE_GH_CI;
        return { runs: [asRun(1, sha, 'push', 'CI', state)] };
      }
      if (workflow === 'release.yml' && event === 'workflow_dispatch') {
        const state = env.FIXTURE_GH_DRY ?? 'success';
        return { runs: dispatched().filter((d) => d.sha === sha).map((d, i) => asRun(100 + i, sha, event, d.title, state)) };
      }
      return { runs: [] };
    },
    dispatch({ workflow, ref, inputs }) {
      const sha = publicMain();
      const title = env.FIXTURE_GH_NAME === 'old' ? 'Release' : `dry run all ${sha}`;
      fs.mkdirSync(env.FIXTURE_GH, { recursive: true });
      fs.writeFileSync(file(), JSON.stringify([...dispatched(), { workflow, ref, inputs, sha, title }]));
      return { ok: true, detail: `dispatched ${workflow} on ${ref}` };
    },
    command({ workflow, ref, inputs }) {
      return `gh workflow run ${workflow} --ref ${ref} ${Object.entries(inputs).map(([k, v]) => `-f ${k}=${v}`).join(' ')}`;
    },
    rerun(id) {
      return `gh run rerun ${id} --failed`;
    },
    rerunFailed({ runId }) {
      const f = path.join(env.FIXTURE_GH, 'reruns.json');
      const all = fs.existsSync(f) ? JSON.parse(fs.readFileSync(f, 'utf8')) : {};
      fs.writeFileSync(f, JSON.stringify({ ...all, [runId]: (all[runId] ?? 0) + 1 }));
      return { ok: true, detail: `gh run rerun ${runId} --failed` };
    },
    jobs({ runId }) {
      asked('github');
      if (runId !== 1) return { jobs: [] };
      const states = partStates();
      const steps = partSteps();
      const again = rerunsOf(1);
      const jobs = [];
      PARTS.forEach((name, i) => {
        let state = states[name] ?? (env.FIXTURE_GH_CI === 'in_progress' ? 'in_progress' : 'success');
        let id = 1000 + i;
        // A part the gate had re-run is a new job of the new attempt.
        if (again && state === 'failure') {
          state = env.FIXTURE_GH_RERUN ?? 'failure';
          id = 1000 * (again + 1) + i;
        }
        if (state === 'missing') return;
        jobs.push({
          id,
          name,
          status: state === 'in_progress' ? 'in_progress' : 'completed',
          conclusion: state === 'in_progress' ? null : state,
          url: `https://github.invalid/jobs/${id}`,
          failedSteps: state === 'failure' ? [steps[name] ?? 'The part'] : [],
        });
      });
      return { jobs };
    },
    artifacts() {
      asked('github');
      return { names: (env.FIXTURE_GH_ARTIFACTS ?? [...KEPT, 'release-files-macos'].join(',')).split(',').filter(Boolean) };
    },
    // FIXTURE_GH_PULLS: { "<n>": { state, merged, base, label, title, head } } -
    // the head, unless given, is the public remote's refs/pull/<n>/head, as
    // GitHub's API and that ref agree once a push to the pull request is in.
    pull({ number }) {
      asked('github');
      const all = JSON.parse(env.FIXTURE_GH_PULLS || '{}');
      const p = all[String(number)];
      if (!p) return { error: `gh: Not Found (HTTP 404): pull ${number}` };
      const head = p.head ?? execFileSync('git', ['-C', env.FIXTURE_EXPORT, 'ls-remote', 'origin', `refs/pull/${number}/head`], { encoding: 'utf8' }).split(/\s+/)[0];
      return { pull: { number, state: p.state ?? 'open', merged: !!p.merged, base: p.base ?? 'main', head, label: p.label ?? `someone:pr-${number}`, title: p.title ?? '' } };
    },
  };
}

/** What the plan says of ci.yml's matrix, with FIXTURE_GH_MATRIX set. */
function matrix(redWhenName) {
  if (!env.FIXTURE_GH_MATRIX) return {};
  return {
    heavy: [{ name: 'the fixture heavy suite', github: 'ci.yml', githubJobs: () => ['Go (a)', 'Go (b)'], cmd: redWhenName('FIXTURE_HEAVY_FAIL') }],
    githubMatrix: { workflow: 'ci.yml', parts: () => PARTS, complete: 'All tests (full)' },
    promotion: { required: KEPT, optional: ['release-files-macos'] },
  };
}

/** A pretag gate the release defers to another machine, as the Store copy off Windows. */
const DEFERRED = {
  name: 'the fixture Windows check',
  deferred: {
    ack: 'store-deferred',
    checkedAck: 'store-checked',
    why: 'the fixture package is checked on a Windows machine, not here',
    command: 'node scripts/windows-check.mjs',
    artifact: 'release-files-store',
    later: (c) => [`on Windows, in a clean checkout of ${c.tag} (${c.releaseCommit.slice(0, 10)}):`, '  node scripts/windows-check.mjs'],
  },
  check: () => ({ ok: false, detail: 'a deferred gate was run' }),
};

/** The token the stand-in vault holds; the release must never print it. */
export const FIXTURE_WINGET_TOKEN = 'fixture-winget-token-not-a-real-one';

/** The stand-in for GitHub's answer to GET /repos/<fork> (FIXTURE_WINGET). */
async function wingetFetch(url, init) {
  const f = path.join(env.FIXTURE_GH, 'winget-asked.json');
  fs.mkdirSync(env.FIXTURE_GH, { recursive: true });
  fs.writeFileSync(f, String((fs.existsSync(f) ? Number(fs.readFileSync(f, 'utf8')) : 0) + 1));
  const how = env.FIXTURE_WINGET ?? '';
  if (how === 'offline') throw new TypeError('fetch failed', { cause: { code: 'ENOTFOUND' } });
  if (init?.headers?.Authorization !== `Bearer ${FIXTURE_WINGET_TOKEN}`) return new Response('{"message":"Bad credentials"}', { status: 401 });
  if (how === 'expired') return new Response('{"message":"Bad credentials"}', { status: 401 });
  const headers = { 'x-oauth-scopes': 'public_repo' };
  if (how === 'expiring') headers['github-authentication-token-expiration'] = new Date(Date.now() + 3 * 86_400_000).toISOString().replace('T', ' ').replace(/\.\d+Z$/, ' UTC');
  return new Response(JSON.stringify({ full_name: 'fixture/winget-pkgs', permissions: { push: true } }), { status: 200, headers });
}

const wingetGate = () =>
  wingetTokenGate({
    fork: 'fixture/winget-pkgs',
    repo: 'fixture/fixture',
    // The real reader, the stand-in vault: a command that prints the token.
    readToken: (ctx) =>
      readWingetToken({
        bash: ctx?.bash,
        settings: () => ({ env: env.FIXTURE_WINGET === 'unset' ? {} : { FILEX_WINGET_TOKEN_CMD: `printf %s ${FIXTURE_WINGET_TOKEN}` }, file: '' }),
      }),
    fetch: wingetFetch,
    sleep: async () => {},
  });

export default function plan({ version, tag }) {
  return {
    exportDir: env.FIXTURE_EXPORT,
    signingKeys: env.FIXTURE_SIGNING_KEY ? [env.FIXTURE_SIGNING_KEY] : [],
    privateHosts: {
      allow: ['security@brf.sh', 'hello@brf.sh'],
      forbid: [/brf\.sh/, /gitlab\.com[/:]brftech/],
    },
    preflight: [wingetGate()],
    docSurfaces: ['README.md', 'docs/*.md'],
    audit: [readmePictures()],
    docs: [{ name: 'the fixture docs gate', cmd: redWhen('FIXTURE_DOCS_FAIL') }],
    pretag: [{ name: 'the fixture test suite', cmd: redWhen('FIXTURE_PRETAG_FAIL') }, ...(env.FIXTURE_DEFER ? [DEFERRED] : [])],
    exportGates: [{ name: 'the fixture export gate', cmd: redWhen('FIXTURE_EXPORT_FAIL') }],
    github: github(),
    ...matrix(redWhen),
    circleci: circleci(),
    circleciWorkflow: 'ci',
    // ⚠ The gate gives up waiting by the clock, and one of its polls spawns
    // git several times (this stand-in reads the public remote's main). On
    // Windows a `git ls-remote` costs ~200 ms, so a 400 ms limit ran out
    // during the FIRST poll - the one that starts the dry run - and every
    // case that expects the gate to finish read "GitHub has not finished"
    // instead (0.52.0 pretag, 2026-10-05). Long enough for any machine; the
    // case that wants the wait to run out asks for a short one.
    gateWait: { pollMs: 20, timeoutMs: Number(env.FIXTURE_GATE_TIMEOUT_MS || 120_000), appearMs: Number(env.FIXTURE_GATE_APPEAR_MS || 120_000) },
    published: [{ name: 'the fixture release workflow published', cmd: ['node', '-e', 'process.exit(process.env.FIXTURE_CI_DONE ? 0 : 1)'] }],
    deployChecklist: ['deploy the fixture ({tag})'],
    deployed: [
      runningRelease(`the fixture server runs ${tag}`, SITE, { fetch }),
      updateManifest(`the fixture manifest offers ${tag}`, `${SITE}/updates/stable.json`, { fetch }),
      desktopFeeds(`the fixture feed offers ${version}`, `${SITE}/desktop`, ['latest.yml'], { fetch }),
      docsSite(`the fixture docs serve ${tag}`, `${SITE}/docs`, { fetch }),
    ],
  };
}
