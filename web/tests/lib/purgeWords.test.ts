// "Delete permanently" in the explorer's Trash says what became of every item,
// once, in words that end.
//
// ⚠ On a server that runs purges as jobs, the notice said "Deleting 3 items
// permanently…" when the server had refused one of the three: the refusal was
// counted and then not said. Then #69's partial sentences ended on a dangling
// "…; 1 could not be", without the reason — and a queued purge of N items
// said N toasts, read the trash N times and asked /admin/protection N times,
// one per job that ended (lib/purgeWords `createPurgeBatches`).
import { describe, expect, it } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

import { createPurgeBatches, sayPurge } from '@brftech/filex-core/src/lib/purgeWords';
import { wordsIn } from '@brftech/filex-core/src/lib/errorWords';

const en = wordsIn('en');
const tr = wordsIn('tr');

describe('what a permanent delete says', () => {
  it('jobs of the queue are being deleted, not deleted', () => {
    expect(sayPurge({ queued: true, purged: 3, failed: 0 }, en)).toBe('Deleting 3 items permanently…');
  });

  it('a refusal is said with its reason, and the sentence ends', () => {
    const reason = en('err.status.404');
    for (const t of [en, tr]) {
      for (const queued of [true, false]) {
        const s = sayPurge({ queued, purged: 2, failed: 1, reason }, t);
        expect(s, s).toContain(reason);
        expect(s, `dangling: ${s}`).not.toMatch(/could not be$|;\s*$/);
      }
    }
    expect(sayPurge({ queued: false, purged: 2, failed: 1, reason }, en)).toBe(
      `2 items deleted permanently; 1 could not be deleted: ${reason}`,
    );
    expect(sayPurge({ queued: false, purged: 2, failed: 1, reason }, tr)).toBe(
      `2 öğe kalıcı olarak silindi; 1 öğe silinemedi: ${reason}`,
    );
  });

  it('nothing purged says why, not "0 items deleted"', () => {
    expect(sayPurge({ queued: false, purged: 0, failed: 2, reason: 'You are not allowed.' }, en)).toBe(
      'You are not allowed.',
    );
  });
});

describe('a queued purge of several items', () => {
  it('says ONE summary when its last job ends, with what failed and why', () => {
    const batches = createPurgeBatches();
    batches.start([71, 72, 73], { refused: 1, reason: 'Not found' });
    expect(batches.settle({ id: 71, ok: true })).toBeNull();
    expect(batches.settle({ id: 72, ok: false, reason: 'Timed out' })).toBeNull();
    expect(batches.settle({ id: 99, ok: true }), 'someone else’s job').toBeUndefined();
    expect(batches.settle({ id: 73, ok: true })).toEqual({ purged: 2, failed: 2, reason: 'Not found' });
  });

  it('knows which jobs are its own', () => {
    const batches = createPurgeBatches();
    batches.start([5], { refused: 0 });
    expect(batches.owns(5)).toBe(true);
    expect(batches.owns(6)).toBe(false);
    expect(batches.settle({ id: 5, ok: true })).toEqual({ purged: 1, failed: 0 });
    expect(batches.owns(5)).toBe(false);
  });
});

describe('the explorer', () => {
  // FileExplorer is not mounted in unit tests; its wiring is read off the source.
  const src = readFileSync(resolve(__dirname, '../../../packages/core/src/FileExplorer.vue'), 'utf8');
  const fn = src.slice(src.indexOf('async function purgeSelection'), src.indexOf('async function confirmDelete'));

  it('purges through the client, not a raw fetch', () => {
    expect(fn).toMatch(/await api\.purgeTrash\(id, \{ queued \}\)/);
    expect(fn).not.toMatch(/fetch\(/);
    expect(fn).not.toMatch(/new Error\(firstFailure\)/);
  });

  it('sends no second purge for a row already on its way', () => {
    expect(fn).toMatch(/filter\(\(id\) => !trashPurging\.value\.has\(id\)\)/);
    expect(fn).toMatch(/t\('trash\.row_busy'\)/);
  });

  it('follows a queued purge as one batch: one summary, one reading of the trash', () => {
    expect(fn).toMatch(/purgeBatches\.start\(/);
    const settled = src.match(/onSettled: \(op: PendingOp[^)]*\) => \{[\s\S]*?\n {2}\},\n\}\);/)?.[0] ?? '';
    expect(settled).toMatch(/purgeBatches\.owns\(op\.id\)/);
    expect(settled).not.toMatch(/t\('toast\.purged', \{ n: 1 \}\)/);
  });

  it('keeps the policy it knows while it asks again', () => {
    const probe = src.slice(src.indexOf('async function probeTrashPolicy'), src.indexOf('/** What the banner promises.'));
    expect(probe, 'the menu entry flickered away during every probe').not.toMatch(/trashCanEmpty\.value = false;\s*try/);
  });
});
