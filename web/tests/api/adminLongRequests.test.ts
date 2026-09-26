// The admin requests that take longer than the client's 30 s default are given
// the time the server takes.
//
// ⚠ Installing a storage plugin runs the server's conformance suite (up to
// 90 s) after the binary arrives, and a restart waits up to 30 s for the
// plugin to come up and 15 s for its health: at the client's 30 s the page
// said "timeout of 30000ms exceeded" about an install that was working.
// Deleting a large storage removes every one of its rows, which is minutes.
import { beforeEach, describe, expect, it, vi } from 'vitest';

const calls: Array<{ method: string; url: string; config?: { timeout?: number } }> = [];

vi.mock('@/api/client', () => ({
  extractError: (_e: unknown, f: string) => f,
  api: {
    get: vi.fn(async () => ({ data: {} })),
    post: vi.fn(async (url: string, _body?: unknown, config?: { timeout?: number }) => {
      calls.push({ method: 'post', url, config });
      return { data: {} };
    }),
    patch: vi.fn(async () => ({ data: {} })),
    delete: vi.fn(async (url: string, config?: { timeout?: number }) => {
      calls.push({ method: 'delete', url, config });
      return { data: {} };
    }),
  },
}));

import { PluginsApi } from '@/api/plugins';
import { StoragesApi } from '@/api/storages';

/** The server's own budget for a plugin install: conformance 90 s, plus start. */
const PLUGIN_SERVER_BUDGET_MS = 120_000;

beforeEach(() => {
  calls.length = 0;
});

describe('long admin requests', () => {
  it('a storage plugin install, upgrade and restart wait longer than the server takes', async () => {
    const file = new File(['x'], 'p.bin');
    await PluginsApi.upload('p', file);
    await PluginsApi.fromUrl('p', 'https://example.com/p.bin', 'ab'.repeat(32));
    await PluginsApi.upgrade(3, file);
    await PluginsApi.restart(3);
    expect(calls).toHaveLength(4);
    for (const c of calls) {
      expect(c.config?.timeout ?? 30_000, `${c.method} ${c.url}`).toBeGreaterThan(PLUGIN_SERVER_BUDGET_MS);
    }
  });

  it('deleting a storage waits minutes, not the default 30 s', async () => {
    await StoragesApi.remove(7);
    expect(calls[0].url).toBe('/admin/storages/7');
    expect(calls[0].config?.timeout ?? 30_000).toBeGreaterThanOrEqual(10 * 60_000);
  });
});
