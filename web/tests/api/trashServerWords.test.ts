// The trash says the server's numbers and the server's words (0.54, findings
// D1, D2, A4, A15).
//
// RED PROOF, D1: the explorer's "Empty the trash?" confirmation was
// `t(trashConfirmKey, { count: files.length, size: … })` - the rows the view
// had loaded, the first 50 of the listing - while the purge took every entry
// the caller reaches (61,844 on one install). The confirmation now shows the
// server's dry run (GET /api/admin/trash/empty/preview) and its button waits
// for it.
//
// RED PROOF, D2: a storage's virtual `.trash` row asked `?storage=<name>`,
// which the server did not read, and summed the newest 50 deletions of EVERY
// storage. It shows the server's summary for its own storage now.
//
// RED PROOF, A15: restore and "Delete permanently" were one request per item,
// tallied and worded in the browser (lib/restoreWords, lib/purgeWords). One
// request each now, and the server's `summary` is what is shown.
import { readFileSync } from 'node:fs';
import path from 'node:path';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { useFileApi } from '@brftech/filex-core/src/composables/useFileApi';
import { hydrateTrashRow } from '@brftech/filex-core/src/lib/listing';
import type { FileNode } from '@brftech/filex-core/src/types/FileNode';

afterEach(() => vi.unstubAllGlobals());

const asked: Array<{ url: string; method: string; body: unknown }> = [];

function answer(reply: (url: string) => [number, unknown]) {
  asked.length = 0;
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string, init?: RequestInit) => {
      asked.push({ url, method: init?.method ?? 'GET', body: init?.body ? JSON.parse(String(init.body)) : undefined });
      const [status, body] = reply(url);
      return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
    }),
  );
}

describe('the confirmation names the server count (D1)', () => {
  it("asks the purge's dry run in the screen's language and hands its sentence back as it is", async () => {
    const said = 'Bu işlem 61.844 öğeyi (12,3 GB) kalıcı olarak siler. Geri alınamaz.';
    answer(() => [200, { dry_run: true, count: 61844, bytes: 12_300_000_000, summary: said }]);
    const api = useFileApi({ apiBase: '', locale: 'tr' });
    const preview = await api.trashEmptyPreview();
    expect(asked).toHaveLength(1);
    expect(asked[0].method).toBe('GET');
    expect(asked[0].url).toBe('/api/admin/trash/empty/preview?lang=tr');
    expect(preview.count).toBe(61844);
    expect(preview.summary).toBe(said);
  });

  const explorer = readFileSync(
    path.resolve(__dirname, '../../../packages/core/src/FileExplorer.vue'),
    'utf8',
  );
  const confirm = explorer.slice(explorer.indexOf(':title="t(\'trash.empty_confirm_title\')"'));
  const dialog = confirm.slice(0, confirm.indexOf('</Modal>'));

  it("shows the server's sentence in the dialog, never a count of the rows on screen", () => {
    expect(dialog).toMatch(/\{\{ trashPreview\.summary \}\}/);
    expect(dialog).not.toMatch(/files\.length/);
    expect(dialog).not.toMatch(/trashConfirmKey|trashTotalBytes/);
    expect(explorer).not.toMatch(/empty_confirm_body/);
  });

  it('keeps the button shut until the server has counted, and while there is nothing to delete', () => {
    expect(dialog).toMatch(/data-testid="trash-empty-confirm"\s+:disabled="!trashPreview \|\| trashPreview\.count === 0"/);
    const open = explorer.match(/async function openTrashConfirm\(\)[\s\S]*?\n\}/)?.[0] ?? '';
    expect(open).toMatch(/await api\.trashEmptyPreview\(\)/);
    expect(explorer).toMatch(/data-testid="trash-empty"\s+@click="openTrashConfirm"/);
  });

  it("says a run's progress and its end in the server's words", () => {
    const run = explorer.match(/async function emptyTrash\(\)[\s\S]*?\n\}/)?.[0] ?? '';
    expect(run).toMatch(/showToast\(\{ message: end\.summary \}/);
    expect(run).not.toMatch(/emptied_partly|empty_stopped|trash\.emptied/);
    expect(explorer).toMatch(/return run\?\.running \? run\.summary \?\? '' : '';/);
  });
});

describe('the trash view is paged on the server count (D1)', () => {
  it('asks a page with its size and offset, and keeps the totals the server sends', async () => {
    answer(() => [
      200,
      {
        entries: [],
        total: 61844,
        total_bytes: 12_300_000_000,
        storages: [],
        summary: '61,844 items in the trash, 12.3 GB in all',
        limit: 200,
        offset: 200,
      },
    ]);
    const api = useFileApi({ apiBase: '', locale: 'en' });
    const page = await api.listTrash(undefined, { limit: 200, offset: 200 });
    expect(asked[0].url).toBe('/api/files/manager/trash?limit=200&offset=200&lang=en');
    expect(page.total).toBe(61844);
    expect(page.summary).toBe('61,844 items in the trash, 12.3 GB in all');
  });

  it('draws "Show more" while the server counts more than is on screen', () => {
    const explorer = readFileSync(path.resolve(__dirname, '../../../packages/core/src/FileExplorer.vue'), 'utf8');
    expect(explorer).toMatch(/v-if="trashLoaded < trashTotal"[\s\S]*?data-testid="trash-more"[\s\S]*?@click="loadMoreTrash"/);
    expect(explorer).toMatch(/api\.listTrash\(undefined, \{ limit: TRASH_PAGE, offset: at \}\)/);
  });
});

describe("a storage's .trash row shows that storage's numbers (D2)", () => {
  it("names the storage the server reads, and takes its summary - not a sum of every storage's newest rows", async () => {
    answer((url) => {
      expect(url).toContain('storage=Globex');
      return [
        200,
        {
          entries: [{ id: 1, storage_id: 9, storage_name: 'Other', size: 999_999, deleted_at: '2026-10-08T10:00:00Z' }],
          total: 70,
          storages: [{ storage_id: 2, storage_name: 'Globex', count: 70, bytes: 2940, newest_deleted_at: '2026-10-07T09:00:00Z' }],
        },
      ];
    });
    const api = useFileApi({ apiBase: '', locale: 'en' });
    const files = [{ basename: '.trash', path: 'Globex://.trash', type: 'dir' } as unknown as FileNode];
    await hydrateTrashRow(files, 'Globex', api);
    expect(asked[0].url).toMatch(/[?&]storage=Globex(&|$)/);
    expect(asked[0].url).toMatch(/[?&]limit=1(&|$)/);
    expect(files[0].size).toBe(2940);
    expect((files[0] as { last_modified?: number }).last_modified).toBe(Date.parse('2026-10-07T09:00:00Z'));
  });
});

describe('restore and permanent delete are one request each, said by the server (A15)', () => {
  it('restores a selection in one request and hands back the server summary', async () => {
    const said = '2 items restored - 1 item was not restored: something already has the name “b.txt”';
    answer(() => [200, { done: 2, failed: 1, reason_code: 'exists', taken: ['b.txt'], summary: said }]);
    const api = useFileApi({ apiBase: '', locale: 'en' });
    const out = await api.restoreBatch([11, 12, 13]);
    expect(asked).toHaveLength(1);
    expect(asked[0]).toEqual({ url: '/api/files/manager/restore?lang=en', method: 'POST', body: { node_ids: [11, 12, 13] } });
    expect(out.summary).toBe(said);
  });

  it('deletes a selection for good in one request, queued when asked', async () => {
    answer(() => [202, { done: 3, failed: 0, queued: true, ops: [{ id: 5, kind: 'purge' }], summary: 'Deleting 3 items permanently…' }]);
    const api = useFileApi({ apiBase: '', locale: 'en' });
    const out = await api.purgeBatch([1, 2, 3], { queued: true });
    expect(asked).toHaveLength(1);
    expect(asked[0]).toEqual({ url: '/api/admin/trash/purge?queued=1&lang=en', method: 'POST', body: { node_ids: [1, 2, 3] } });
    expect(out.summary).toBe('Deleting 3 items permanently…');
    expect(out.ops).toHaveLength(1);
  });

  it('the client sentence builders are gone', () => {
    const lib = path.resolve(__dirname, '../../../packages/core/src/lib');
    for (const gone of ['purgeWords.ts', 'restoreWords.ts']) {
      expect(() => readFileSync(path.join(lib, gone), 'utf8'), `${gone} is back`).toThrow();
    }
  });
});
