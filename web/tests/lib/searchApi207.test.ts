// The admin search pages read the server's numbers (task #207, audit D7).
//
// web/src/api/search.ts used to send page / page_size the server never read,
// and to make up `total: items.length`, `score: 0` and an empty storage name -
// and drop `truncated` - so the Search test page's "more hits than shown"
// guard never fired and every score read 0.000.
import { beforeEach, describe, expect, it, vi } from 'vitest';

const { get } = vi.hoisted(() => ({ get: vi.fn() }));
vi.mock('@/api/client', () => ({ api: { get, post: vi.fn() } }));

import { SearchApi } from '@/api/search';

describe('SearchApi.query', () => {
  beforeEach(() => get.mockReset());

  it('passes the server\'s total, truncated, score and storage through', async () => {
    get.mockResolvedValue({
      data: {
        results: [{ id: 7, storage_id: 1, storage: 'depo', name: 'a.txt', path: '/a.txt', score: 812, type: 'file' }],
        truncated: true,
        total: 40,
      },
    });
    const page = await SearchApi.query({ q: 'a', limit: 1 });
    expect(page.total).toBe(40);
    expect(page.truncated).toBe(true);
    expect(page.items[0].score).toBe(812);
    expect(page.items[0].storage_name).toBe('depo');
  });

  it('sends limit and type, never page / page_size', async () => {
    get.mockResolvedValue({ data: { results: [], truncated: false, total: 0 } });
    await SearchApi.query({ q: 'plan', limit: 50, type: 'file', scope: 'name' });
    const params = get.mock.calls[0][1].params as Record<string, unknown>;
    expect(params).toMatchObject({ q: 'plan', limit: 50, type: 'file', scope: 'name' });
    expect(params).not.toHaveProperty('page');
    expect(params).not.toHaveProperty('page_size');
  });
});
