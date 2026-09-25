// `api.storageUsage()` — the explorer's own read of `GET /api/files/quota/storages`
// ("how full is this drive", RBAC-filtered server-side).
//
// The storage line needs it wherever the host did not hand the drives' sizes
// over — the desktop app hands over names only — and it has to behave like
// its sibling `quotaMe()`: derived from the manager endpoint (a proxy that
// forwards /api/files/* keeps working, the desktop's absolute server URL
// included), and null on ANY failure, so an older server without the route
// leaves the panel as it was instead of putting an error where a status line
// goes.
import { afterEach, describe, expect, it, vi } from 'vitest';

import { useFileApi } from '@brftech/filex-core/src/composables/useFileApi';

afterEach(() => vi.unstubAllGlobals());

function answer(status: number, body: string) {
  const asked: string[] = [];
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string) => {
      asked.push(url);
      return new Response(body, { status, headers: { 'Content-Type': 'application/json' } });
    }),
  );
  return asked;
}

describe('api.storageUsage', () => {
  it('asks the quota route beside the manager, and hands back its rows', async () => {
    const asked = answer(
      200,
      JSON.stringify({
        storages: [
          { name: 'Diyetlif-Bulut-Depolama', used_bytes: 245276276422, file_count: 153943 },
          { name: 'arsiv', used_bytes: 10, file_count: 1, coverage: { complete: false, reason: 'lazy_on_open' } },
        ],
      }),
    );
    const api = useFileApi({ apiBase: 'https://files.example.com', locale: 'en' });
    const rows = await api.storageUsage();
    expect(asked).toEqual(['https://files.example.com/api/files/quota/storages']);
    expect(rows).toEqual([
      { name: 'Diyetlif-Bulut-Depolama', used_bytes: 245276276422, file_count: 153943 },
      { name: 'arsiv', used_bytes: 10, file_count: 1, coverage: { complete: false, reason: 'lazy_on_open' } },
    ]);
  });

  it('is null for a server without the route', async () => {
    answer(404, '{"error":"not found"}');
    expect(await useFileApi({ apiBase: '', locale: 'en' }).storageUsage()).toBeNull();
  });

  it('is null for a body that is not the usage shape', async () => {
    answer(200, '{"used_bytes":1}');
    expect(await useFileApi({ apiBase: '', locale: 'en' }).storageUsage()).toBeNull();
  });
});
