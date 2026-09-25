/**
 * storageLine — WHICH number the storage line prints: the "Storage · 245.3 GB
 * used" at the foot of the navigation panel (web explorer and desktop app)
 * and the storage chip in the admin top bar.
 *
 * Two figures are in reach and they answer different questions:
 *
 *   - `GET /api/files/quota/me` — the person's upload counter,
 *     `SUM(nodes.size) WHERE owner_id = me`. Only the upload path sets an
 *     owner, so every file a storage sync discovered counts for nobody;
 *   - `GET /api/files/quota/storages` — how full each drive the person can
 *     open is (RBAC-filtered, the figure Home's drive cards print).
 *
 * The line used to print the first for everybody, under "Storage", in the
 * very sentence Home's card uses for the second. Measured on a production
 * install (2026-09-25): the card said 245.3 GB, the line 523.5 MB — 228.6 GB
 * of that drive had arrived through a sync and the rest was uploaded by
 * eleven people.
 *
 * So:
 *   - a person WITH a quota sees their share against it. That ceiling is what
 *     refuses their next upload, so it is the number that matters to them;
 *   - a person WITHOUT one sees the size of the drives the panel lists — the
 *     host's figures where it sent them (the Home cards print those), the
 *     measured ones for the rest;
 *   - a figure the server does not stand behind in full is a lower bound:
 *     a drive whose catalogue does not cover it yet, or one nobody measured;
 *   - and with nothing trustworthy to say, the line says nothing (null).
 */
import { incomplete, type CatalogCoverage } from './catalogCoverage';

/** What the line says. */
export interface StorageLine {
  /** The bytes it reports. */
  used: number;
  /** The person's ceiling; 0 when there is none. */
  total: number;
  /** No ceiling — `used` is the drives' size, not the person's uploads. */
  unlimited: boolean;
  /** `used` is a lower bound. */
  partial: boolean;
}

/** The fields of `GET /api/files/quota/me` (quota.Snapshot) the line reads. */
export interface PersonUsage {
  used_bytes: number;
  quota_bytes: number;
  unlimited?: boolean;
}

/** One drive as the navigation panel holds it (`ExplorerConfig.storages`). */
export interface PanelDrive {
  name: string;
  usedBytes?: number;
  usedPartial?: boolean;
}

/** One row of `GET /api/files/quota/storages` (handlers.StorageUsageRow). */
export interface MeasuredDrive {
  name: string;
  used_bytes: number;
  file_count?: number;
  coverage?: CatalogCoverage | null;
}

/** A byte count that can be added up. */
function bytes(n: unknown): number | null {
  return typeof n === 'number' && Number.isFinite(n) && n >= 0 ? n : null;
}

/** The person has a ceiling: a positive quota the server did not call unlimited. */
export function hasCeiling(p: PersonUsage | null | undefined): boolean {
  return !!p && !p.unlimited && p.quota_bytes > 0;
}

/**
 * Whether the line has to ask the server for drive sizes: there is no
 * ceiling, and the host left at least one of the panel's drives unsized (the
 * desktop app hands over names only) — or listed none at all.
 */
export function needsMeasuredDrives(
  p: PersonUsage | null | undefined,
  panel: readonly PanelDrive[],
): boolean {
  if (!p || hasCeiling(p)) return false;
  return panel.length === 0 || panel.some((d) => bytes(d.usedBytes) === null);
}

export function storageLine(
  p: PersonUsage | null | undefined,
  panel: readonly PanelDrive[],
  measured: readonly MeasuredDrive[] | null | undefined,
): StorageLine | null {
  if (!p) return null;
  if (hasCeiling(p)) {
    return { used: bytes(p.used_bytes) ?? 0, total: p.quota_bytes, unlimited: false, partial: false };
  }

  const rows = new Map<string, MeasuredDrive>();
  for (const r of measured ?? []) if (r && typeof r.name === 'string') rows.set(r.name, r);
  // A host that lists no drives gets every drive the server measured for it.
  const drives: readonly PanelDrive[] = panel.length ? panel : [...rows.keys()].map((name) => ({ name }));

  let used = 0;
  let counted = 0;
  let partial = false;
  const seen = new Set<string>();
  for (const d of drives) {
    if (seen.has(d.name)) continue; // one drive, one figure, however often it is listed
    seen.add(d.name);
    const own = bytes(d.usedBytes);
    const row = own === null ? rows.get(d.name) : undefined;
    const figure = own ?? bytes(row?.used_bytes);
    if (figure === null) {
      partial = true; // listed, but nobody could say how full it is
      continue;
    }
    used += figure;
    counted++;
    if (own !== null ? d.usedPartial === true : incomplete(row?.coverage) !== null) partial = true;
  }
  if (!counted || used <= 0) return null;
  return { used, total: 0, unlimited: true, partial };
}
