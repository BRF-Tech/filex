// Catalog coverage (issue #45, docs/LAZY-CATALOGUE.md → "Coverage in the UI"):
// the rules the explorer's banner, its folder sizes and Home's drive cards
// draw the server's `coverage` / `size_partial` by. One module, so the three
// cannot tell a person different things about one storage.
import { describe, expect, it } from 'vitest';
import {
  coverageByStorage,
  coverageMessage,
  coverageNotice,
  coveragePercent,
  type CatalogCoverage,
} from '@brftech/filex-core/src/lib/catalogCoverage';
import { useLocale } from '@brftech/filex-core/src/composables/useLocale';
import { withStorageUsage, fromAdminStorages } from '@/lib/visibleStorages';
import type { StorageRef } from '@/api/types';

const filling: CatalogCoverage = { complete: false, reason: 'lazy_filling', catalogued_folders: 300, pending_folders: 100 };
const onOpen: CatalogCoverage = { complete: false, reason: 'lazy_on_open', catalogued_folders: 12, pending_folders: 400 };
const firstScan: CatalogCoverage = { complete: false, reason: 'first_scan' };

describe('coverage rules', () => {
  it('the percentage is a floor that never says 100 while anything waits', () => {
    expect(coveragePercent(filling)).toBe(75);
    expect(coveragePercent({ complete: false, catalogued_folders: 999, pending_folders: 1 })).toBe(99);
    expect(coveragePercent({ complete: false, catalogued_folders: 0, pending_folders: 0 })).toBe(0);
  });

  it('one sentence per reason', () => {
    expect(coverageMessage(filling)).toEqual({ key: 'coverage.lazy_filling', vars: { pct: 75 } });
    expect(coverageMessage(onOpen).key).toBe('coverage.lazy_on_open');
    expect(coverageMessage(firstScan).key).toBe('coverage.first_scan');
  });

  it('storage_info → only the storages whose catalog is incomplete', () => {
    const map = coverageByStorage([
      { name: 'arsiv', coverage: onOpen },
      { name: 'belgeler' },
      { name: 'eski', coverage: { complete: true } },
      { name: '' },
      null,
    ]);
    expect(map).toEqual({ arsiv: onOpen, belgeler: null, eski: null });
    expect(coverageByStorage(undefined)).toEqual({});
  });

  it('a listing speaks for its storage; only an administrator is offered the full sync, only in behaviour B', () => {
    const map = { arsiv: onOpen, belgeler: null, dolum: filling };
    expect(coverageNotice({ map, adapter: 'belgeler', searching: false, crossStorage: false, admin: true })).toBeNull();
    const b = coverageNotice({ map, adapter: 'arsiv', searching: false, crossStorage: false, admin: true });
    expect(b).toMatchObject({ key: 'coverage.lazy_on_open', storage: 'arsiv', offerCatalogAll: true });
    expect(coverageNotice({ map, adapter: 'arsiv', searching: false, crossStorage: false, admin: false })?.offerCatalogAll).toBe(false);
    expect(coverageNotice({ map, adapter: 'dolum', searching: true, crossStorage: false, admin: true })?.offerCatalogAll).toBe(false);
  });

  it('a search across every storage names the ones it cannot fully see', () => {
    const map = { zeta: onOpen, belgeler: null, alfa: firstScan };
    const n = coverageNotice({ map, adapter: 'belgeler', searching: true, crossStorage: true, admin: true });
    expect(n).toMatchObject({ key: 'coverage.search_some', vars: { names: 'alfa, zeta' }, offerCatalogAll: false });
    const one = coverageNotice({ map: { zeta: onOpen, belgeler: null }, adapter: 'belgeler', searching: true, crossStorage: true, admin: true });
    expect(one).toMatchObject({ key: 'coverage.lazy_on_open', storage: 'zeta' });
  });
});

describe('a partial folder size', () => {
  const en = useLocale(() => 'en');
  const tr = useLocale(() => 'tr');

  it('is a lower bound, or not a number at all', () => {
    expect(en.formatNodeSize({ size: 1_200_000, size_partial: true })).toBe('≥ 1.2 MB');
    expect(tr.formatNodeSize({ size: 1_200_000, size_partial: true })).toBe('≥ 1,2 MB');
    expect(en.formatNodeSize({ size: 0, size_partial: true })).toBe('—');
    expect(en.formatNodeSize({ size: 0 })).toBe('0 B');
    expect(en.formatNodeSize({ size: 1_200_000 })).toBe('1.2 MB');
  });

  it('says why on hover', () => {
    expect(tr.nodeSizeHint({ size: 5, size_partial: true })).toBe('Bu klasörün bir kısmı henüz kataloglanmadı; boyutu en az bu kadar.');
    expect(tr.nodeSizeHint({ size: 0, size_partial: true })).toBe('Bu klasör henüz kataloglanmadı; boyutu bilinmiyor.');
    expect(en.nodeSizeHint({ size: 5 })).toBeUndefined();
  });
});

describe("Home's drive figure", () => {
  it('is partial when the usage endpoint says so', () => {
    const out = withStorageUsage([{ name: 'arsiv', label: 'arsiv' }, { name: 'tam', label: 'tam' }], {
      storages: [
        { name: 'arsiv', used_bytes: 10, file_count: 1, coverage: onOpen },
        { name: 'tam', used_bytes: 20, file_count: 2 },
      ],
    });
    expect(out.map((s) => [s.name, s.usedPartial])).toEqual([
      ['arsiv', true],
      ['tam', false],
    ]);
  });

  it('is partial when the admin storage list says so', () => {
    const rows = [
      { id: 1, name: 'arsiv', driver: 'local', coverage: firstScan, stats: { file_count: 1, total_size_bytes: 5 } },
      { id: 2, name: 'tam', driver: 'local', stats: { file_count: 1, total_size_bytes: 5 } },
    ] as unknown as StorageRef[];
    expect(fromAdminStorages(rows).map((s) => s.usedPartial)).toEqual([true, false]);
  });
});
