// "Catalog everything" follows the scan it started to its end — on the same
// signal, and through the same follower, as the admin panel's "Sync now"
// (lib/storageWatch; its rules are tests/lib/storageWatch.test.ts).
//
// ⚠ The button started the storage's full scan, flashed "started" for 2.5 s,
// and then the banner and the button stayed exactly as they were for the
// hours a large storage takes: no sign it was running, and no word when it
// ended or how. #69 then followed it through the runs table with heuristics
// and a 60 s give-up (lib/catalogRun) — a second follower of a scan #66 was
// already following on the worker's own `running` flag — flashed "Started"
// before the POST had succeeded, held ONE busy flag for every storage, said
// its ends for 2.5 s and printed the raw run error after a colon.
import { describe, expect, it } from 'vitest';
import { readFileSync, existsSync } from 'node:fs';
import { resolve } from 'node:path';

const CORE = resolve(__dirname, '../../../packages/core/src');
const src = readFileSync(resolve(CORE, 'FileExplorer.vue'), 'utf8');
const fn = src.slice(src.indexOf('async function catalogAll'), src.indexOf('function undoToast'));

describe('the explorer’s "Catalog everything"', () => {
  it('follows the scan through the one storage follower, not a second one', () => {
    expect(fn).toMatch(/catalogWatch\(\)\.scan\(id\)/);
    expect(fn).not.toContain('catalogAndFollow(');
    expect(existsSync(resolve(CORE, 'lib/catalogRun.ts')), 'the runs-table follower is still there').toBe(false);
  });

  it('says "started" only once the POST has answered, and "already running" when it was', () => {
    const post = fn.indexOf("{ method: 'POST' }");
    const started = fn.indexOf("t('coverage.catalog_started')");
    expect(post).toBeGreaterThan(-1);
    expect(started, '"Started" before the POST succeeded').toBeGreaterThan(post);
    expect(fn).toMatch(/res\?\.status === 'running'\s*\?\s*t\('coverage\.catalog_joined', \{ storage \}\)/);
  });

  it('says every end for long enough to read, with no raw run error', () => {
    for (const key of ['catalog_failed', 'catalog_stopped', 'catalog_unfollowed', 'catalog_gone']) {
      expect(fn, key).toMatch(new RegExp(`showToast\\(\\{ message: t\\('coverage\\.${key}', \\{ storage \\}\\) \\}, ERROR_TOAST_MS\\)`));
    }
    expect(fn).toContain("t('coverage.catalog_done', { storage })");
    expect(fn, 'the raw error after a colon').not.toMatch(/error: end\.error/);
    expect(fn).not.toMatch(/sayFailure\(/);
    expect(fn).toMatch(/showToast\(\{ message: failureText\(err\) \}, ERROR_TOAST_MS\)/);
  });

  it('holds the button and the banner for the storage being followed only', () => {
    expect(src).not.toMatch(/const catalogAllBusy = ref\(false\)/);
    expect(src).toMatch(/:disabled="catalogBusy\(coverageShown\.storage\)"/);
    expect(src).toMatch(/:aria-busy="catalogBusy\(coverageShown\.storage\) \? 'true' : undefined"/);
    expect(src).toMatch(/catalogBusy\(coverageShown\.storage\)\s*\?\s*t\('coverage\.cataloging', \{ storage: coverageShown\.storage \?\? '' \}\)/);
  });

  it('stops following when the explorer goes', () => {
    expect(src).toMatch(/catalogWatcher\?\.stop\(\);/);
  });
});
