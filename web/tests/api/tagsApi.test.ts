// The tags client, and the one thing it used to throw away.
//
// ⚠⚠ `GET /files/manager/tagged` answers rows that already name their storage:
// handlers/meta.go → `rows()` attaches it, and that function exists BECAUSE a
// row carrying only `storage_id` cannot be addressed by the client (the tag
// view was empty in every multi-storage install until it did). The client
// mapped that name to the empty string, so the Tagged files page drew an em
// dash in every Storage cell — "this file belongs to no storage" — on a page
// whose whole job is finding one file across several of them. Seen while
// taking the v0.43.0 tags screenshot.

import { beforeEach, describe, expect, it, vi } from 'vitest';

const calls: Array<{ url: string; params?: unknown }> = [];
let answer: unknown = { tag: 'contracts', nodes: [] };

vi.mock('@/api/client', () => ({
  extractError: (_e: unknown, f: string) => f,
  api: {
    get: vi.fn(async (url: string, cfg?: { params?: unknown }) => {
      calls.push({ url, params: cfg?.params });
      return { data: answer };
    }),
    post: vi.fn(async () => ({ data: {} })),
  },
}));

import { TagsApi } from '@/api/tags';

const node = {
  id: 7,
  storage_id: 3,
  storage: 'demo',
  name: 'budget.csv',
  path: '/Documents/budget.csv',
  size: 54,
  mime: 'text/csv',
  backend_mtime: '2026-09-23T00:53:00Z',
};

describe('TagsApi.filesByTag', () => {
  beforeEach(() => {
    calls.length = 0;
    answer = { tag: 'contracts', nodes: [node] };
  });

  it('keeps the storage name the server attached', async () => {
    const [hit] = await TagsApi.filesByTag('contracts', 'team');
    expect(
      hit.storage_name,
      'the Storage column is drawn from this field; blanking it prints an em dash on every row',
    ).toBe('demo');
    expect(hit.storage_id).toBe(3);
  });

  it('asks the route with the tag and its kind', async () => {
    await TagsApi.filesByTag('contracts', 'team');
    expect(calls[0]).toEqual({ url: '/files/manager/tagged', params: { tag: 'contracts', kind: 'team' } });
    calls.length = 0;
    await TagsApi.filesByTag('contracts');
    expect(calls[0]).toEqual({ url: '/files/manager/tagged', params: { tag: 'contracts' } });
  });

  it('survives a server that names no storage, and a null list', async () => {
    answer = { tag: 'contracts', nodes: [{ ...node, storage: undefined }] };
    expect((await TagsApi.filesByTag('contracts'))[0].storage_name).toBe('');
    answer = { tag: 'contracts', nodes: null };
    expect(await TagsApi.filesByTag('contracts')).toEqual([]);
  });

  it('maps the rest of the row the table draws', async () => {
    const [hit] = await TagsApi.filesByTag('contracts');
    expect(hit.filename).toBe('budget.csv');
    expect(hit.path).toBe('/Documents/budget.csv');
    expect(hit.size).toBe(54);
    expect(hit.mime).toBe('text/csv');
    expect(hit.modified_at).toBe('2026-09-23T00:53:00Z');
  });
});
