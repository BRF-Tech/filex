#!/usr/bin/env node
// The three-engine migration gate of `pnpm release`: the db suite on sqlite,
// on a FRESH PostgreSQL and on a FRESH MySQL, and proof that the last two
// really ran.
//
// ⚠⚠ TestSchemaParityAcrossEngines and TestMigrationsApplyOnEveryEngine SKIP
// postgres and mysql when their DSN is unset — they then compare sqlite with
// sqlite and PASS. Before 2026-09-20 every "schema parity green" meant only
// "sqlite green": migrations 00042-00051 had never been executed on postgres
// or mysql by anything, and filex ships on all three. A migration that only
// works on sqlite bricks the first boot after an upgrade for everybody else.
// So exit 0 is not enough here: the run must NAME both engines.
//
// ⚠ The ports are deliberately not 5432/3306: a local PostgreSQL or MySQL
// would be tested instead of a fresh one, and a fresh one is the point.
// ⚠ Under WSL the DSNs are written INTO the command WSL runs: an environment
// variable that failed to cross the Windows → WSL boundary would skip the
// engine, and a skipped engine is a pass.

import { spawnSync } from 'node:child_process';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

import { nativeGo, toWslPath, wslGo } from '../../lib/go-build.mjs';
import { docker as dockerRun } from '../engine.mjs';

const REPO = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..', '..', '..');
const PG = { name: 'filex-release-gate-pg', port: 5444 };
const MY = { name: 'filex-release-gate-mysql', port: 3317 };
const PG_DSN = `postgres://filex:filex@127.0.0.1:${PG.port}/postgres?sslmode=disable`;
const MY_DSN = `root:filex@tcp(127.0.0.1:${MY.port})/mysql?parseTime=true&loc=UTC&charset=utf8mb4`;

const sh = (bin, args, opts = {}) => spawnSync(bin, args, { encoding: 'utf8', windowsHide: true, maxBuffer: 256 * 1024 * 1024, ...opts });
const docker = (...args) => dockerRun(args);
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

function down() {
  docker('rm', '-f', PG.name, MY.name);
}

async function up() {
  down();
  const a = docker('run', '-d', '--name', PG.name, '-e', 'POSTGRES_PASSWORD=filex', '-e', 'POSTGRES_USER=filex', '-p', `${PG.port}:5432`, 'postgres:16-alpine');
  if (a.status !== 0) throw new Error(`postgres did not start: ${a.stderr}`);
  const b = docker('run', '-d', '--name', MY.name, '-e', 'MYSQL_ROOT_PASSWORD=filex', '-p', `${MY.port}:3306`, 'mysql:8.4');
  if (b.status !== 0) throw new Error(`mysql did not start: ${b.stderr}`);
  let pg = false;
  let my = false;
  for (let i = 0; i < 90 && !(pg && my); i++) {
    pg ||= docker('exec', PG.name, 'pg_isready', '-U', 'filex').status === 0;
    my ||= docker('exec', MY.name, 'mysqladmin', 'ping', '-uroot', '-pfilex').status === 0;
    if (!(pg && my)) await sleep(2000);
  }
  if (!pg) throw new Error('postgres never came up');
  if (!my) throw new Error('mysql never came up');
  console.log('both engines ready');
}

function goTest() {
  const backend = path.join(REPO, 'backend');
  const test = 'go test -count=1 -v ./internal/db/...';
  if (nativeGo()) {
    return sh('go', ['test', '-count=1', '-v', './internal/db/...'], {
      cwd: backend,
      env: { ...process.env, FILEX_TEST_PG_DSN: PG_DSN, FILEX_TEST_MYSQL_DSN: MY_DSN },
    });
  }
  if (wslGo()) {
    const q = (s) => `'${s.split("'").join(`'"'"'`)}'`;
    return sh('wsl', ['-e', 'bash', '-lc',
      `cd ${q(toWslPath(backend))} && export PATH=/usr/local/go/bin:$PATH GOFLAGS=-buildvcs=false && FILEX_TEST_PG_DSN=${q(PG_DSN)} FILEX_TEST_MYSQL_DSN=${q(MY_DSN)} ${test}`]);
  }
  return { status: 1, stdout: '', stderr: 'no Go toolchain: go is not on PATH, and WSL has none either' };
}

let code = 1;
try {
  await up();
  const r = goTest();
  process.stdout.write(r.stdout ?? '');
  process.stderr.write(r.stderr ?? '');
  const out = `${r.stdout}\n${r.stderr}`;
  const missing = ['parity/postgres', 'parity/mysql'].filter((m) => !out.includes(m));
  const skipped = (out.match(/^\s*--- SKIP/gm) ?? []).length;
  console.log(`\nskipped subtests: ${skipped}`);
  if (r.status !== 0) console.log(`go test exited ${r.status}`);
  else if (missing.length) console.log(`these engines never ran: ${missing.join(', ')} — the DSN did not reach the test`);
  else {
    console.log('postgres and mysql both ran for real');
    code = 0;
  }
} catch (e) {
  console.error(e.message);
} finally {
  down();
}
process.exit(code);
