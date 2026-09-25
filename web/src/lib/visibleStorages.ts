/**
 * Which storages this visitor may see — the ONE answer, for every page in
 * this SPA that needs it.
 *
 * There are two sources and they are not interchangeable:
 *
 *   • an ADMIN reads `/api/admin/storages` (the pinia store), which also
 *     reports the per-storage size the panel already prints;
 *   • everybody else cannot touch `/api/admin/*` at all, so their list comes
 *     from the manager root — `GET /api/files/manager?action=index&path=`
 *     answers `{ storages: [...] }`, RBAC-filtered server-side (manager.go
 *     drops every storage whose grant set is not `StorageVisible`).
 *
 * Explore.vue owned this as a private `fetchVisibleStorages`; the Home page
 * needs exactly the same list, and two copies of "which drives may I show
 * you" is how one of them starts showing a drive the other hides. So it lives
 * here and both call it.
 *
 * ⚠ `usedBytes` is the trap, and the rule is: it is the SAME quantity for
 * everybody or it is absent. `/api/files/quota/me` is a per-USER sum across
 * every storage (`SUM(nodes.size) WHERE owner_id = me`), so printing it under
 * a drive's name would be a number about the person wearing a label about the
 * drive — it is never used here. What a non-admin gets instead is
 * `GET /api/files/quota/storages`, which answers the admin row's own
 * per-storage total for the storages RBAC lets them see, and nothing about the
 * ones it does not. When that call fails the field stays undefined and the
 * card falls back to naming the kind of thing: a card with no size line says
 * less; a card with the wrong size line says something false.
 */

import type { StorageRef } from '@/api/types';
import { readBearerToken, readCsrfCookie } from '@/lib/explorerConfig';

export interface VisibleStorage {
  name: string;
  label: string;
  driver?: string;
  readOnly?: boolean;
  /** Bytes held by this storage, when the server reported one (admin only). */
  usedBytes?: number;
  /** Files held by this storage, when the server reported one (admin only). */
  fileCount?: number;
  /**
   * `usedBytes` counts only part of the storage: the server sent `coverage`
   * beside the figure (its catalog does not cover all of it yet — a first
   * sync, a lazily cataloged storage). Home draws it as a lower bound.
   */
  usedPartial?: boolean;
}

/** The server's `coverage` beside a size figure says the figure is partial. */
function partial(coverage: unknown): boolean {
  const c = coverage as { complete?: unknown } | null | undefined;
  return !!c && typeof c === 'object' && c.complete !== true;
}

/** GET headers that carry whatever this SPA is authenticating with. */
export function fileApiHeaders(): Record<string, string> {
  const headers: Record<string, string> = {};
  const bearer = readBearerToken();
  if (bearer) headers['Authorization'] = `Bearer ${bearer}`;
  else {
    const csrf = readCsrfCookie();
    if (csrf) headers['X-CSRF-TOKEN'] = csrf;
  }
  return headers;
}

/** Map the admin storages list onto the shared shape. */
export function fromAdminStorages(items: readonly StorageRef[]): VisibleStorage[] {
  return items.map((s) => ({
    name: s.name,
    label: s.name,
    driver: s.driver,
    readOnly: s.read_only,
    usedBytes: s.stats?.total_size_bytes ?? s.total_bytes,
    fileCount: s.stats?.file_count ?? s.file_count,
    usedPartial: partial(s.coverage),
  }));
}

/**
 * Map the manager root's `storages` array onto the shared shape.
 *
 * ⚠ `storage_info` carries each drive's `read_only` beside the names. Without
 * it a person who cannot read `/api/admin/storages` never learnt a drive was
 * read-only until they were inside it: the admin saw "Salt okunur" and no
 * "New" there, the non-admin a "New" menu of greyed entries and no reason
 * (QA, 2026-09-21). An older server sends no `storage_info`, and the drive is
 * then simply not marked — the listing inside it still says so.
 */
export function fromManagerRoot(body: unknown): VisibleStorage[] {
  const b = body as { storages?: unknown; storage_info?: unknown };
  const names = b?.storages;
  if (!Array.isArray(names)) return [];
  const ro = new Map<string, boolean>();
  if (Array.isArray(b.storage_info)) {
    for (const i of b.storage_info) {
      const row = i as { name?: unknown; read_only?: unknown };
      if (typeof row?.name === 'string') ro.set(row.name, row.read_only === true);
    }
  }
  return names
    .filter((n): n is string => typeof n === 'string' && n !== '')
    .map((n) => (ro.has(n) ? { name: n, label: n, readOnly: ro.get(n) } : { name: n, label: n }));
}

/**
 * Merge `GET /api/files/quota/storages` onto a list of storages, by name.
 *
 * ⚠ Merge, never replace. The endpoint is RBAC-filtered server-side and can
 * legitimately answer for fewer drives than the caller can open (a storage
 * whose count failed is dropped rather than reported as 0), so a storage the
 * usage body does not mention keeps its undefined `usedBytes` and the card
 * goes back to naming the kind of thing. Rebuilding the list from the usage
 * body would make one drive vanish from Home because its size was unreadable.
 */
export function withStorageUsage(
  storages: readonly VisibleStorage[],
  body: unknown,
): VisibleStorage[] {
  const rows = (body as { storages?: unknown })?.storages;
  if (!Array.isArray(rows)) return [...storages];
  const byName = new Map<string, { used?: number; files?: number; partial: boolean }>();
  for (const r of rows) {
    const row = r as { name?: unknown; used_bytes?: unknown; file_count?: unknown; coverage?: unknown };
    if (typeof row?.name !== 'string' || row.name === '') continue;
    byName.set(row.name, {
      used: typeof row.used_bytes === 'number' && row.used_bytes >= 0 ? row.used_bytes : undefined,
      files: typeof row.file_count === 'number' && row.file_count >= 0 ? row.file_count : undefined,
      partial: partial(row.coverage),
    });
  }
  return storages.map((s) => {
    const hit = byName.get(s.name);
    if (!hit || hit.used === undefined) return { ...s };
    return { ...s, usedBytes: hit.used, fileCount: hit.files ?? s.fileCount, usedPartial: hit.partial };
  });
}

/** Best-effort read of the per-storage usage endpoint. */
async function fetchStorageUsage(): Promise<unknown> {
  try {
    const res = await fetch('/api/files/quota/storages', {
      headers: fileApiHeaders(),
      credentials: 'include',
    });
    if (!res.ok) return null;
    return await res.json();
  } catch {
    return null;
  }
}

/**
 * The storages this caller may open.
 *
 * `adminItems` is the admin store's list — pass it when it is already loaded
 * (it 403s for a non-admin, so it is simply empty for them) and the manager
 * root is used as the fallback.
 *
 * ⚠ The admin path does NOT ask for usage a second time: `/api/admin/storages`
 * already enriched every row with the same aggregate, and a second request per
 * page load buys a figure the caller is already holding.
 */
export async function fetchVisibleStorages(
  adminItems: readonly StorageRef[] = [],
): Promise<VisibleStorage[]> {
  if (adminItems.length) return fromAdminStorages(adminItems);
  try {
    const res = await fetch('/api/files/manager?action=index&path=', {
      headers: fileApiHeaders(),
      credentials: 'include',
    });
    if (!res.ok) return [];
    const listed = fromManagerRoot(await res.json());
    if (!listed.length) return listed;
    return withStorageUsage(listed, await fetchStorageUsage());
  } catch {
    return [];
  }
}
