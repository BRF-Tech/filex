// Which drives a visitor may see — and, on the Home page, what the card under
// each name is allowed to SAY.
//
// ⚠ The usage figure is the trap, and the rule pinned here is that it is the
// SAME quantity for everybody or it is absent. `/api/files/quota/me` is a
// per-USER sum across every storage; a card printing that under a drive's name
// would be a number about the person wearing a label about the drive — wrong
// in a way no test of the fetch could see, because the request succeeds. The
// non-admin figure therefore comes from `/api/files/quota/storages`, which
// answers the admin row's own per-storage total, RBAC-filtered server-side;
// the shape helpers below still report nothing on their own.
import { afterEach, describe, expect, it, vi } from 'vitest';

import {
  fetchVisibleStorages,
  fromAdminStorages,
  fromManagerRoot,
  withStorageUsage,
} from '@/lib/visibleStorages';
import type { StorageRef } from '@/api/types';

const admin = (over: Partial<StorageRef> = {}): StorageRef =>
  ({
    id: 1,
    name: 'qldemo',
    driver: 'local',
    enabled: true,
    config: {},
    read_only: false,
    created_at: '2026-09-01T00:00:00Z',
    updated_at: '2026-09-01T00:00:00Z',
    stats: { file_count: 31, total_size_bytes: 85022 },
    ...over,
  }) as StorageRef;

describe('fromAdminStorages', () => {
  it('carries the size and the count the panel already shows', () => {
    const [s] = fromAdminStorages([admin()]);
    expect(s).toMatchObject({ name: 'qldemo', usedBytes: 85022, fileCount: 31 });
  });

  it('falls back to the cached columns when the live aggregate is absent', () => {
    const [s] = fromAdminStorages([
      admin({ stats: undefined, total_bytes: 512, file_count: 4 }),
    ]);
    expect(s.usedBytes).toBe(512);
    expect(s.fileCount).toBe(4);
  });

  it('keeps read-only and the driver, which the explorer root needs', () => {
    const [s] = fromAdminStorages([admin({ read_only: true, driver: 's3' })]);
    expect(s.readOnly).toBe(true);
    expect(s.driver).toBe('s3');
  });
});

describe('fromManagerRoot', () => {
  it('takes the names the RBAC-filtered manager root answered with', () => {
    expect(fromManagerRoot({ storages: ['qldemo', 'team'] })).toEqual([
      { name: 'qldemo', label: 'qldemo' },
      { name: 'team', label: 'team' },
    ]);
  });

  it('reports NO usage — there is none to report, and a guess would be a lie', () => {
    const [s] = fromManagerRoot({ storages: ['qldemo'] });
    expect(s.usedBytes).toBeUndefined();
    expect(s.fileCount).toBeUndefined();
  });

  it('marks a read-only drive for a person who cannot read the admin list', () => {
    // ⚠ QA, 2026-09-21: the admin saw "Salt okunur" on the drive, a
    // non-admin never learnt it until the menu refused them — `read_only`
    // was only ever said for the storage being LISTED.
    const out = fromManagerRoot({
      storages: ['depo', 'arsiv'],
      storage_info: [
        { name: 'depo', read_only: false },
        { name: 'arsiv', read_only: true },
      ],
    });
    expect(out).toEqual([
      { name: 'depo', label: 'depo', readOnly: false },
      { name: 'arsiv', label: 'arsiv', readOnly: true },
    ]);
    // An older server sends no `storage_info`: nothing is marked, nothing breaks.
    expect(fromManagerRoot({ storages: ['arsiv'], storage_info: null })).toEqual([{ name: 'arsiv', label: 'arsiv' }]);
  });

  it('survives a body without a storages array', () => {
    expect(fromManagerRoot({})).toEqual([]);
    expect(fromManagerRoot(null)).toEqual([]);
    expect(fromManagerRoot({ storages: ['ok', 42, ''] }).map((s) => s.name)).toEqual(['ok']);
  });
});

describe('withStorageUsage', () => {
  const listed = [
    { name: 'qldemo', label: 'qldemo' },
    { name: 'team', label: 'team' },
  ];

  it('fills the figure the RBAC-filtered endpoint answered with', () => {
    const out = withStorageUsage(listed, {
      storages: [{ name: 'qldemo', used_bytes: 85022, file_count: 31 }],
    });
    expect(out[0]).toMatchObject({ name: 'qldemo', usedBytes: 85022, fileCount: 31 });
  });

  // ⚠ The one that matters: the endpoint drops a storage whose count failed,
  // and it drops every storage RBAC hides. Rebuilding the list from the usage
  // body would make those drives disappear from Home entirely.
  it('keeps a storage the usage body never mentions, without a figure', () => {
    const out = withStorageUsage(listed, {
      storages: [{ name: 'qldemo', used_bytes: 1, file_count: 1 }],
    });
    expect(out.map((s) => s.name)).toEqual(['qldemo', 'team']);
    expect(out[1].usedBytes).toBeUndefined();
  });

  it('never invents a figure out of a broken body', () => {
    for (const body of [null, {}, { storages: 'nope' }, { storages: [{ name: 'qldemo' }] }]) {
      const out = withStorageUsage(listed, body);
      expect(out.map((s) => s.name)).toEqual(['qldemo', 'team']);
      expect(out.every((s) => s.usedBytes === undefined)).toBe(true);
    }
  });
});

describe('fetchVisibleStorages', () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  /** Answers the manager root and the usage endpoint, and records the URLs. */
  function stubFetch(usage: unknown | 'fail') {
    const seen: string[] = [];
    vi.stubGlobal(
      'fetch',
      vi.fn(async (url: string) => {
        seen.push(url);
        if (url.startsWith('/api/files/manager')) {
          return { ok: true, json: async () => ({ storages: ['qldemo', 'team'] }) } as Response;
        }
        if (usage === 'fail') return { ok: false, json: async () => ({}) } as Response;
        return { ok: true, json: async () => usage } as Response;
      }),
    );
    return seen;
  }

  it('back-fills the non-admin list from the per-storage usage endpoint', async () => {
    const seen = stubFetch({ storages: [{ name: 'qldemo', used_bytes: 4096, file_count: 2 }] });
    const out = await fetchVisibleStorages([]);
    expect(seen).toContain('/api/files/quota/storages');
    expect(out.find((s) => s.name === 'qldemo')?.usedBytes).toBe(4096);
    // team is visible to the explorer but absent from the usage answer.
    expect(out.find((s) => s.name === 'team')?.usedBytes).toBeUndefined();
  });

  it('still lists the drives when the usage endpoint is unavailable', async () => {
    stubFetch('fail');
    const out = await fetchVisibleStorages([]);
    expect(out.map((s) => s.name)).toEqual(['qldemo', 'team']);
    expect(out.every((s) => s.usedBytes === undefined)).toBe(true);
  });

  // ⚠ The admin already holds the same aggregate from /api/admin/storages.
  // A second request per page load buys a number the caller is holding.
  it('asks for nothing extra on the admin path', async () => {
    const seen = stubFetch({ storages: [] });
    const out = await fetchVisibleStorages([admin()]);
    expect(seen).toEqual([]);
    expect(out[0].usedBytes).toBe(85022);
  });
});
