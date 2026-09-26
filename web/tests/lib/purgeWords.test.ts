// "Delete permanently" in the explorer's Trash says what became of every item.
//
// ⚠ On a server that runs purges as jobs, the notice said "Deleting 3 items
// permanently…" when the server had refused one of the three: the refusal was
// counted and then not said.
import { describe, expect, it } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

import { sayPurge } from '@brftech/filex-core/src/lib/purgeWords';

const words: Record<string, string> = {
  'toast.purging': 'DELETING {n}',
  'toast.purging_partly': 'DELETING {n}, {failed} REFUSED',
  'toast.purged': 'DELETED {n}',
  'toast.purged_partly': 'DELETED {n}, {failed} REFUSED',
};
const t = (k: string, vars?: Record<string, string | number>) =>
  Object.entries(vars ?? {}).reduce((s, [a, b]) => s.replace(`{${a}}`, String(b)), words[k] ?? k);

describe('what a permanent delete says', () => {
  it('jobs of the queue are being deleted, not deleted', () => {
    expect(sayPurge({ queued: true, purged: 3, failed: 0 }, t)).toBe('DELETING 3');
  });

  it('a refusal among the jobs is said', () => {
    expect(sayPurge({ queued: true, purged: 2, failed: 1 }, t)).toBe('DELETING 2, 1 REFUSED');
  });

  it('inside the request, deleted, and a refusal is said too', () => {
    expect(sayPurge({ queued: false, purged: 3, failed: 0 }, t)).toBe('DELETED 3');
    expect(sayPurge({ queued: false, purged: 2, failed: 1 }, t)).toBe('DELETED 2, 1 REFUSED');
  });
});

describe('the explorer', () => {
  // FileExplorer is not mounted in unit tests; its wiring is read off the source.
  const src = readFileSync(resolve(__dirname, '../../../packages/core/src/FileExplorer.vue'), 'utf8');
  const fn = src.slice(src.indexOf('async function purgeSelection'), src.indexOf('async function confirmDelete'));

  it('says a purge in these words', () => {
    expect(fn).toContain('sayPurge(');
  });
});
