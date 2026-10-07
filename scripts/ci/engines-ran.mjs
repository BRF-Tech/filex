#!/usr/bin/env node
// `node scripts/ci/engines-ran.mjs <go test -json output>`: when a CI job
// tested ./internal/db, PostgreSQL and MySQL really ran (task #181).
//
// ⚠⚠ The engine suites of internal/db SKIP postgres and mysql when their DSN
// is unset, and the schema parity test then compares sqlite with sqlite and
// PASSES (scripts/release/gates/engines.mjs says how that hid migrations
// 00042-00051 from both engines until 2026-09-20). A CI job that runs the db
// package beside PostgreSQL and MySQL service containers proves nothing with
// its exit code: a DSN with a typo, a container that never came up, and the
// job is green. So this reads the job's own test events, the way the release
// gate reads its `-v` output: the parity subtest of each engine must have
// PASSED.
//
// Called after every Go shard. A shard that did not run internal/db passes
// (it had nothing to prove); the one that did must name both engines.
//
// Exit 0: internal/db was not tested here, or both engines ran. 1: it was
// tested and an engine never ran (named). 2: the file could not be read.

import fs from 'node:fs';
import path from 'node:path';
import { pathToFileURL } from 'node:url';

/** The engines the parity test must have run, besides sqlite. */
export const ENGINES = ['postgres', 'mysql'];

/**
 * What a `go test -json` stream says about the engines: whether package
 * `pkg` (an import path suffix) ran at all, and which of `engines` has no
 * passed `…/parity/<engine>` subtest in it. Lines that are not JSON (a build
 * error printed around the stream) are ignored.
 */
export function enginesRan(text, { pkg = 'internal/db', engines = ENGINES } = {}) {
  const ofPkg = (p) => typeof p === 'string' && (p === pkg || p.endsWith(`/${pkg}`));
  let tested = false;
  const passed = new Set();
  for (const line of String(text ?? '').split(/\r?\n/)) {
    if (!line.startsWith('{')) continue;
    let ev;
    try {
      ev = JSON.parse(line);
    } catch {
      continue;
    }
    if (!ofPkg(ev?.Package)) continue;
    tested = true;
    if (ev.Action !== 'pass' || typeof ev.Test !== 'string') continue;
    for (const e of engines) if (ev.Test.endsWith(`/parity/${e}`)) passed.add(e);
  }
  return { tested, missing: tested ? engines.filter((e) => !passed.has(e)) : [] };
}

function main(argv) {
  const file = argv[0];
  if (!file || argv.length !== 1) {
    console.error('usage: node scripts/ci/engines-ran.mjs <go test -json output>');
    return 2;
  }
  let text;
  try {
    text = fs.readFileSync(file, 'utf8');
  } catch (e) {
    console.error(`engines-ran: cannot read ${file}: ${e.message}`);
    return 2;
  }
  const r = enginesRan(text);
  if (!r.tested) {
    console.log('engines-ran: this run did not test internal/db - nothing to prove here');
    return 0;
  }
  if (r.missing.length) {
    console.error(`engines-ran: internal/db ran, and these engines never did: ${r.missing.join(', ')}.`);
    console.error('Their parity subtests did not pass: the DSN did not reach the test, or the server was not up.');
    console.error('A skipped engine is not a pass (scripts/release/gates/engines.mjs).');
    return 1;
  }
  console.log(`engines-ran: internal/db ran on sqlite, ${ENGINES.join(' and ')}`);
  return 0;
}

if (process.argv[1] && import.meta.url === pathToFileURL(path.resolve(process.argv[1])).href) {
  process.exit(main(process.argv.slice(2)));
}
