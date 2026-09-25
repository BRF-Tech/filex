// The release plan of the THROWAWAY repository web/tests/deploy/releaseCli.test.ts
// builds. It is copied to <fixture>/scripts/release/plan.mjs, next to the
// real engine, stages and verify modules — so the test drives the real
// `pnpm release` and only the gates are the fixture's.
//
// Each gate is steered by an environment variable the test sets, so a test
// can make exactly one gate red and prove the release stops there. The
// published surfaces are files in a directory (FIXTURE_SITE) behind a fetch
// that never opens a socket.

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
