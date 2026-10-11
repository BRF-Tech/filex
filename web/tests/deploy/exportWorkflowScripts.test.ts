// scripts/export-public.sh refuses an export whose workflows run a
// `node scripts/<path>` the published tree does not have: v0.34.0's release
// run died on a renamed script AFTER the npm packages and the images were out.
//
// ⚠ 0.56.0 (2026-10-11): the check read the path with a pattern that stopped
// at the first `/`. ci.yml had just started running
// `node scripts/ci/browser-prep.mjs` (#181), the check looked for the file
// `scripts/ci`, found a directory, and refused an export that had the script.
//
// These run the check itself - the block of the real script, read at run time -
// against scratch workflow trees.
import { spawnSync } from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { afterEach, describe, expect, it } from 'vitest';

import { findBash } from '../../../scripts/release/engine.mjs';

const REPO = path.resolve(__dirname, '../../..');
const SCRIPT = path.join(REPO, 'scripts', 'export-public.sh');
// ⚠ The exporter is never published, so the public tree skips this whole
// file (exportRewriteScope.test.ts: v0.45.0's gate failed on ENOENT).
const script = fs.existsSync(SCRIPT) ? fs.readFileSync(SCRIPT, 'utf8') : '';
const bash = script ? findBash() : null;
const TIMEOUT = 60_000;

function checkBlock(): string {
  const start = script.indexOf('\nmissing=0\n');
  const endMark = 'echo "workflow scripts: all present"';
  const end = script.indexOf(endMark, start);
  if (start < 0 || end < 0) throw new Error('export-public.sh: the workflow-script check is no longer where this test reads it');
  return script.slice(start + 1, end + endMark.length) + '\n';
}

const temps: string[] = [];
afterEach(() => {
  for (const d of temps.splice(0)) fs.rmSync(d, { recursive: true, force: true });
});

function tree(files: Record<string, string>): string {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'fx-wfscripts-'));
  temps.push(dir);
  for (const [rel, body] of Object.entries(files)) {
    const p = path.join(dir, rel);
    fs.mkdirSync(path.dirname(p), { recursive: true });
    fs.writeFileSync(p, body);
  }
  return dir;
}

function run(exp: string) {
  const body = `set -euo pipefail\nexp="$1"\n${checkBlock()}`;
  const file = path.join(exp, '..', `${path.basename(exp)}.check.sh`);
  fs.writeFileSync(file, body);
  temps.push(file);
  const r = spawnSync(bash as string, [file, exp.split(path.sep).join('/')], { encoding: 'utf8', timeout: TIMEOUT });
  return { status: r.status, out: `${r.stdout}${r.stderr}` };
}

const WF = '.github/workflows/ci.yml';

describe.skipIf(!script)('export-public.sh: every script a workflow runs is in the export', () => {
  it('finds a script under a subdirectory of scripts/', { timeout: TIMEOUT }, () => {
    const exp = tree({
      [WF]: 'jobs:\n  e2e:\n    steps:\n      - run: node scripts/ci/browser-prep.mjs apps "$RUNNER_TEMP/apps"\n',
      'scripts/ci/browser-prep.mjs': '',
    });
    const r = run(exp);
    expect(r.out).toContain('workflow scripts: all present');
    expect(r.status).toBe(0);
  });

  it('names the whole path when that script is missing', { timeout: TIMEOUT }, () => {
    const exp = tree({
      [WF]: 'jobs:\n  e2e:\n    steps:\n      - run: node scripts/ci/browser-prep.mjs fonts x\n',
      'scripts/ci/other.mjs': '',
    });
    const r = run(exp);
    expect(r.out).toContain("runs 'scripts/ci/browser-prep.mjs', which is not in the export");
    expect(r.status).toBe(1);
  });

  it('still finds a root script and one under the directory a step changes into', { timeout: TIMEOUT }, () => {
    const exp = tree({
      [WF]: 'jobs:\n  a:\n    steps:\n      - run: node scripts/ci-parts.mjs list\n      - run: cd desktop && node scripts/fetch-cli.mjs\n',
      'scripts/ci-parts.mjs': '',
      'desktop/scripts/fetch-cli.mjs': '',
    });
    const r = run(exp);
    expect(r.out).toContain('workflow scripts: all present');
    expect(r.status).toBe(0);
  });
});
