// Tests for src/stores/storages.ts. StoragesApi is mocked module-wide.
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { setActivePinia, createPinia } from 'pinia';

vi.mock('@/api/storages', () => ({
  StoragesApi: {
    list: vi.fn(),
    get: vi.fn(),
    create: vi.fn(),
    update: vi.fn(),
    remove: vi.fn(),
    syncNow: vi.fn(),
    syncHistory: vi.fn(),
    drift: vi.fn(),
    testConnection: vi.fn(),
  },
}));

import { useStoragesStore } from '@/stores/storages';
import { StoragesApi } from '@/api/storages';
import type { StorageRef } from '@/api/types';

const fixture: StorageRef[] = [
  {
    id: 1,
    name: 'main',
    driver: 'local',
    enabled: true,
    config: { root: '/data' },
    read_only: false,
    created_at: '2026-01-01',
    updated_at: '2026-01-01',
  },
  {
    id: 2,
    name: 'backup',
    driver: 's3',
    enabled: true,
    config: {},
    read_only: true,
    created_at: '2026-01-02',
    updated_at: '2026-01-02',
  },
];

describe('stores/storages', () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    vi.clearAllMocks();
  });

  it('initial state is empty', () => {
    const s = useStoragesStore();
    expect(s.items).toEqual([]);
    expect(s.empty).toBe(true);
    expect(s.count).toBe(0);
  });

  it('fetch() loads items', async () => {
    (StoragesApi.list as ReturnType<typeof vi.fn>).mockResolvedValue(fixture);
    const s = useStoragesStore();
    await s.fetch();
    expect(s.count).toBe(2);
    expect(s.empty).toBe(false);
    expect(s.items[0].name).toBe('main');
  });

  it('fetch() failure surfaces as error', async () => {
    (StoragesApi.list as ReturnType<typeof vi.fn>).mockRejectedValue({ message: 'down' });
    const s = useStoragesStore();
    await s.fetch();
    // extractError() falls back to its second-arg default for plain objects
    // (no isAxiosError flag, not instanceof Error) — store passes
    // 'Failed to load storages' as the fallback in stores/storages.ts.
    expect(s.error).toBe('Failed to load storages');
    expect(s.items).toEqual([]);
  });

  it('create() appends to items', async () => {
    const created: StorageRef = { ...fixture[0], id: 99, name: 'new' };
    (StoragesApi.create as ReturnType<typeof vi.fn>).mockResolvedValue(created);
    const s = useStoragesStore();
    s.items = [fixture[0]];
    const out = await s.create({ name: 'new', driver: 'local', config: {} });
    expect(out.id).toBe(99);
    expect(s.items.find((x) => x.id === 99)).toBeTruthy();
    expect(s.count).toBe(2);
  });

  it('update() replaces the matching item in-place', async () => {
    const updated: StorageRef = { ...fixture[0], name: 'renamed' };
    (StoragesApi.update as ReturnType<typeof vi.fn>).mockResolvedValue(updated);
    const s = useStoragesStore();
    s.items = [...fixture];
    await s.update(1, { name: 'renamed' });
    expect(s.find(1)?.name).toBe('renamed');
    expect(s.count).toBe(2);
  });

  it('remove() drops the item', async () => {
    (StoragesApi.remove as ReturnType<typeof vi.fn>).mockResolvedValue(undefined);
    const s = useStoragesStore();
    s.items = [...fixture];
    await s.remove(1);
    expect(s.find(1)).toBeUndefined();
    expect(s.count).toBe(1);
  });

  it('syncNow() flips state to running optimistically', async () => {
    (StoragesApi.syncNow as ReturnType<typeof vi.fn>).mockResolvedValue({ run_id: 1 });
    const s = useStoragesStore();
    s.items = [...fixture];
    await s.syncNow(2);
    expect(s.find(2)?.last_sync_state).toBe('running');
  });
});
