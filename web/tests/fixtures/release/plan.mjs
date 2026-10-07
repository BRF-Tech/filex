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
//
// CircleCI is a stand-in too (`circleci` below, task #181), for
// `--gate circleci`, answering with the real circleci.mjs's mapping:
//   FIXTURE_CC_CI     the CircleCI state of the workflow `ci` in the pipeline
//                     the push of the public main started: success | failed |
//                     failing | running (unset: no pipeline at all)
//   FIXTURE_CC_ERROR  CircleCI cannot be asked: every answer is an error
// Every question either CI is asked is counted in FIXTURE_GH/asked.json
// ({ github: n, circleci: n }), so a test shows which one the gate read.

import { execFileSync } from 'node:child_process';
import fs from 'node:fs';
import path from 'node:path';
import process from 'node:process';

import { workflowRun, workflowUrl } from './circleci.mjs';
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
  });
  return {
    repo: 'fixture/fixture',
    runs({ workflow, sha, event }) {
      asked('github');
      if (workflow === 'ci.yml' && event === 'push') {
        if (!env.FIXTURE_GH_CI || publicMain() !== sha) return { runs: [] };
        return { runs: [asRun(1, sha, 'push', 'CI', env.FIXTURE_GH_CI)] };
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
    jobs({ runId }) {
      asked('github');
      if (runId !== 1) return { jobs: [] };
      const states = partStates();
      const jobs = [];
      PARTS.forEach((name, i) => {
        const state = states[name] ?? (env.FIXTURE_GH_CI === 'in_progress' ? 'in_progress' : 'success');
        if (state === 'missing') return;
        jobs.push({
          id: 1000 + i,
          name,
          status: state === 'in_progress' ? 'in_progress' : 'completed',
          conclusion: state === 'in_progress' ? null : state,
          url: `https://github.invalid/jobs/${1000 + i}`,
        });
      });
      return { jobs };
    },
    artifacts() {
      asked('github');
      return { names: (env.FIXTURE_GH_ARTIFACTS ?? [...KEPT, 'release-files-macos'].join(',')).split(',').filter(Boolean) };
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

export default function plan({ version, tag }) {
  return {
    exportDir: env.FIXTURE_EXPORT,
    signingKeys: env.FIXTURE_SIGNING_KEY ? [env.FIXTURE_SIGNING_KEY] : [],
    privateHosts: {
      allow: ['security@brf.sh', 'hello@brf.sh'],
      forbid: [/brf\.sh/, /gitlab\.com[/:]brftech/],
    },
    docSurfaces: ['README.md', 'docs/*.md'],
    audit: [readmePictures()],
    docs: [{ name: 'the fixture docs gate', cmd: redWhen('FIXTURE_DOCS_FAIL') }],
    pretag: [{ name: 'the fixture test suite', cmd: redWhen('FIXTURE_PRETAG_FAIL') }],
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
