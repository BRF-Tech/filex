// "Catalog everything" follows the scan it started to its end.
//
// ⚠ The button started the storage's full scan, flashed "started" for 2.5 s,
// and then the banner and the button stayed exactly as they were for the
// hours a large storage takes: no sign it was running, and no word when it
// ended or how.
import { describe, expect, it } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

import { catalogAndFollow, type CatalogRun } from '@brftech/filex-core/src/lib/catalogRun';

/** The storage's newest runs as the server lists them. `before` answers the
 *  look before the press; `script` the n-th look after it (null: the read
 *  failed). */
function io(before: CatalogRun[] | null, script: Array<CatalogRun[] | null>) {
  let started = false;
  let looks = 0;
  return {
    started: () => started,
    io: {
      runs: async () => {
        const answer = started ? script[Math.min(looks++, script.length - 1)] : before;
        if (!answer) throw new Error('503');
        return answer;
      },
      start: async () => {
        started = true;
      },
      sleep: async () => {},
    },
  };
}

describe('catalogAndFollow', () => {
  it('follows the new run until it ends, and says how', async () => {
    const f = io([{ id: 4, status: 'ok' }], [
      [{ id: 4, status: 'ok' }], // the new run has not opened yet
      [{ id: 5, status: 'running' }, { id: 4, status: 'ok' }],
      [{ id: 5, status: 'running' }, { id: 4, status: 'ok' }],
      [{ id: 5, status: 'ok' }, { id: 4, status: 'ok' }],
    ]);
    await expect(catalogAndFollow(f.io)).resolves.toEqual({ status: 'ok' });
    expect(f.started()).toBe(true);
  });

  it('a failed run says why', async () => {
    const f = io([], [[{ id: 1, status: 'running' }], [{ id: 1, status: 'failed', error: 'bucket gone' }]]);
    await expect(catalogAndFollow(f.io)).resolves.toEqual({ status: 'failed', error: 'bucket gone' });
  });

  it('a scan already running when pressed is the one followed', async () => {
    const f = io([{ id: 7, status: 'running' }], [[{ id: 7, status: 'running' }], [{ id: 7, status: 'aborted' }]]);
    await expect(catalogAndFollow(f.io)).resolves.toEqual({ status: 'aborted' });
  });

  it('the run followed is told by its id, not by where it is listed', async () => {
    // Runs are listed by their start time, to the second: two that started in
    // the same second can come in either order.
    const f = io([{ id: 4, status: 'ok' }], [
      [{ id: 4, status: 'ok' }, { id: 5, status: 'running' }],
      [{ id: 4, status: 'ok' }, { id: 5, status: 'failed', error: 'denied' }],
    ]);
    await expect(catalogAndFollow(f.io)).resolves.toEqual({ status: 'failed', error: 'denied' });
  });

  it('a run that starts after the one followed does not take its place', async () => {
    const f = io([{ id: 7, status: 'running' }], [
      [{ id: 8, status: 'running' }, { id: 7, status: 'failed', error: 'timeout' }],
    ]);
    await expect(catalogAndFollow(f.io)).resolves.toEqual({ status: 'failed', error: 'timeout' });
  });

  it('without the runs from before the press, no old run is taken for the new one', async () => {
    const f = io(null, [[{ id: 4, status: 'ok' }]]);
    await expect(catalogAndFollow(f.io)).resolves.toEqual({ status: 'unknown' });
    expect(f.started(), 'the scan is still started').toBe(true);
  });

  it('an end this page does not know is not called a success', async () => {
    const f = io([], [[{ id: 1, status: 'partial' }]]);
    await expect(catalogAndFollow(f.io)).resolves.toEqual({ status: 'unknown' });
  });

  it('stops following when nobody is watching any more', async () => {
    let gone = false;
    const f = io([], [[{ id: 1, status: 'running' }]]);
    const p = catalogAndFollow({ ...f.io, stopped: () => gone, sleep: async () => { gone = true; } });
    await expect(p).resolves.toBeNull();
  });

  it('a run that never shows up is not waited for for ever', async () => {
    const f = io([{ id: 2, status: 'ok' }], [[{ id: 2, status: 'ok' }]]);
    await expect(catalogAndFollow(f.io, { pollMs: 1000, giveUpMs: 5000 })).resolves.toEqual({ status: 'unknown' });
  });

  it('nor is a list that cannot be read any more', async () => {
    const f = io([], [[{ id: 1, status: 'running' }], null]);
    await expect(catalogAndFollow(f.io, { pollMs: 1000, giveUpMs: 5000 })).resolves.toEqual({ status: 'unknown' });
  });
});

describe('the explorer', () => {
  // FileExplorer is not mounted in unit tests; its wiring is read off the source.
  const src = readFileSync(resolve(__dirname, '../../../packages/core/src/FileExplorer.vue'), 'utf8');
  const fn = src.slice(src.indexOf('async function catalogAll'), src.indexOf('function undoToast'));

  it('follows the scan "Catalog everything" started, and says every end', () => {
    expect(fn).toContain('catalogAndFollow(');
    for (const key of ['catalog_done', 'catalog_failed', 'catalog_stopped', 'catalog_unfollowed']) {
      expect(fn, key).toContain(`t('coverage.${key}'`);
    }
  });

  it('the banner and the button say it is running', () => {
    expect(src).toContain("t('coverage.cataloging'");
    expect(src).toContain("t('coverage.catalog_running')");
  });
});
