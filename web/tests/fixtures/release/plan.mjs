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

import { execFileSync } from 'node:child_process';
import fs from 'node:fs';
import path from 'node:path';
import process from 'node:process';

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

function github() {
  const file = () => path.join(env.FIXTURE_GH, 'dispatched.json');
  const dispatched = () => (fs.existsSync(file()) ? JSON.parse(fs.readFileSync(file(), 'utf8')) : []);
  const publicMain = () =>
    execFileSync('git', ['-C', env.FIXTURE_EXPORT, 'ls-remote', 'origin', 'refs/heads/main'], { encoding: 'utf8' }).split(/\s+/)[0];
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
    // ⚠ The gate gives up waiting by the clock, and one of its polls spawns
    // git several times (this stand-in reads the public remote's main). On
    // Windows a `git ls-remote` costs ~200 ms, so a 400 ms limit ran out
    // during the FIRST poll - the one that starts the dry run - and every
    // case that expects the gate to finish read "GitHub has not finished"
    // instead (0.52.0 pretag, 2026-10-05). Long enough for any machine; the
    // case that wants the wait to run out asks for a short one.
    gateWait: { pollMs: 20, timeoutMs: Number(env.FIXTURE_GATE_TIMEOUT_MS || 120_000), appearMs: 120_000 },
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
