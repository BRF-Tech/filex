// The filex CLI inside the desktop app reports the release it shipped with.
//
// Run:  node --experimental-strip-types --test desktop/test/cli-version.test.ts
//
// ⚠ Why: scripts/fetch-cli.mjs built the embedded CLI with `-ldflags "-s -w"`
// and nothing else, so every desktop package — every architecture, every
// release — carried a sync engine that answered `filex --version` with
// "0.1.0-dev" (measured on the 0.48.1 arm64 packages, dry runs 36373259912 and
// 36374018764). goreleaser stamps the released CLI with Version/Commit/Date;
// the copy the app runs now gets the same three, under the module path of the
// tree it is built from (the public export renames the Go module).

import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import test from 'node:test';
import { fileURLToPath } from 'node:url';

import { cliLdflags, goModulePath } from '../scripts/lib/cli-version.mjs';

const DESKTOP = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const REPO = path.resolve(DESKTOP, '..');

test('reads the Go module path, whatever the tree calls it', () => {
  assert.equal(goModulePath('module github.com/brf-tech/filex/backend\n\ngo 1.25.0\n'), 'github.com/brf-tech/filex/backend');
  assert.equal(goModulePath('// c\nmodule github.com/brf-tech/filex/backend\r\n'), 'github.com/brf-tech/filex/backend');
  assert.throws(() => goModulePath('go 1.25.0\n'), /no module line/);
  // The tree's own go.mod names the module the flags below are written for.
  assert.match(goModulePath(fs.readFileSync(path.join(REPO, 'backend', 'go.mod'), 'utf8')), /\/backend$/);
});

test('stamps Version, Commit and Date the way goreleaser does', () => {
  const flags = cliLdflags({ module: 'example.com/x/backend', version: '0.48.1', commit: 'e4becda', date: '2026-09-28T02:45:27Z' });
  assert.equal(
    flags,
    '-s -w -X example.com/x/backend/internal/version.Version=0.48.1 -X example.com/x/backend/internal/version.Commit=e4becda -X example.com/x/backend/internal/version.Date=2026-09-28T02:45:27Z',
  );
  assert.throws(() => cliLdflags({ module: 'm', version: '', commit: 'c', date: 'd' }), /version/);
  // The same three variables goreleaser sets, and the ones version.go declares.
  const gr = fs.readFileSync(path.join(REPO, '.goreleaser.yml'), 'utf8');
  const go = fs.readFileSync(path.join(REPO, 'backend', 'internal', 'version', 'version.go'), 'utf8');
  for (const v of ['Version', 'Commit', 'Date']) {
    assert.match(gr, new RegExp(`internal/version\\.${v}=`));
    assert.match(go, new RegExp(`^\\s+${v}\\s*=`, 'm'));
  }
});

test('fetch-cli.mjs builds the CLI with those flags, not a bare -s -w', () => {
  const src = fs.readFileSync(path.join(DESKTOP, 'scripts', 'fetch-cli.mjs'), 'utf8');
  assert.match(src, /const ldflags = cliLdflags\(\{/);
  assert.match(src, /'-ldflags', ldflags,/);
  assert.doesNotMatch(src, /'-ldflags',\s*'-s -w'/);
});
