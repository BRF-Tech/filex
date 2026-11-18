#!/usr/bin/env node
// What GitHub's nightly workflow (.github/workflows/nightly-desktop.yml, task
// #361) needs to know of a nightly commit, read from its checkout: the
// version every nightly build of it carries, the night, the tags - and, given
// the CI run's jobs, the labels of the nightly image.
//
//   node scripts/chain/nightly-meta.mjs --tree DIR [--jobs FILE --run URL --labels FILE]
//
//   DIR       a checkout of the nightly commit (its HEAD is the commit)
//   --jobs    the CI run's jobs, one JSON object a line ({ name, conclusion },
//             what `gh api .../jobs --jq '.jobs[]'` prints)
//   --run     the CI run's page, for the test label
//   --labels  where the image's extra labels go, one key=value a line
//             (nightly-lib.mjs nightlyLabels)
//
// Prints one key=value a line - revision, version, desktop_version, date,
// ymd, tags - for $GITHUB_OUTPUT (nightly-lib.mjs nightlyMeta: the night from
// the commit's `Nightly-Date:` trailer, the build host's). Exit: 0, 1 the
// commit names no version or night, 2 usage.

import { spawnSync } from 'node:child_process';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

import { ciVerdict, nightlyLabels, nightlyMeta } from './nightly-lib.mjs';

function git(dir, args) {
  const r = spawnSync('git', ['-C', dir, ...args], { encoding: 'utf8' });
  if (r.status !== 0) throw new Error(`git ${args.join(' ')}: ${String(r.stderr).trim()}`);
  return r.stdout.trim();
}

function main(argv) {
  const o = {};
  for (let i = 0; i < argv.length; i += 2) {
    const k = argv[i];
    const v = argv[i + 1];
    if (!k?.startsWith('--') || v === undefined) {
      console.error(`usage: see the head of ${path.basename(fileURLToPath(import.meta.url))}`);
      return 2;
    }
    o[k.slice(2)] = v;
  }
  if (!o.tree) {
    console.error('--tree is required');
    return 2;
  }
  let meta;
  try {
    const sha = git(o.tree, ['rev-parse', 'HEAD']);
    const message = git(o.tree, ['log', '-1', '--format=%B', sha]);
    const committedMs = Number(git(o.tree, ['log', '-1', '--format=%ct', sha])) * 1000;
    const released = JSON.parse(fs.readFileSync(path.join(o.tree, 'packages', 'core', 'package.json'), 'utf8')).version;
    meta = nightlyMeta({ released, sha, message, committedMs });
  } catch (e) {
    console.error(e.message);
    return 1;
  }
  for (const [k, v] of Object.entries(meta)) console.log(`${k}=${v}`);
  if (o.labels) {
    let counts = null;
    if (o.jobs) {
      const jobs = fs.readFileSync(o.jobs, 'utf8').split('\n').filter((l) => l.trim()).map((l) => JSON.parse(l));
      counts = ciVerdict({ run: { status: 'completed', conclusion: 'success' }, jobs }).counts;
    }
    const labels = nightlyLabels({ counts, run: o.run ?? '' });
    fs.writeFileSync(o.labels, Object.entries(labels).map(([k, v]) => `${k}=${v}\n`).join(''));
  }
  return 0;
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  process.exit(main(process.argv.slice(2)));
}
