/**
 * e2evault/gc — what the lock holder may delete, and when it repacks
 * (docs/E2E-VAULT-FORMAT.md → "Garbage collection").
 *
 *   - Retention: generation g is EXPIRED when g ≤ latest - 3 and generation
 *     g + 1 was committed more than 15 minutes ago (the modification time of
 *     its index file; an index file that is gone counts as long ago).
 *   - Deleted, for good: index files of expired generations; graveyard packs
 *     whose last user (generation `died - 1`) is expired; orphans - packs in
 *     the listing that are in neither the latest pack table nor the
 *     graveyard, and that this writer did not upload since it took the lock.
 *   - Nothing while the latest generation is damaged, carries extension bytes
 *     or a version this build does not write, or while there is no index.
 *
 * Pure planning: the caller lists, deletes (`POST .../delete`) and commits.
 */
import { VAULT_DELETE_MAX, VAULT_KEEP_GENERATIONS, VAULT_RETENTION_MS, packDataSize } from './layout';
import { liveBytesByPack, type VaultIndexState } from './vindex';

export interface IndexFileInfo {
  generation: number;
  size?: number;
  /** Modification time, ms since 1970. */
  mtime: number;
}

export interface PackFileInfo {
  id: string;
  size?: number;
  mtime?: number;
}

/** Is generation `g` expired, given the index files present? */
export function isExpired(g: number, latest: number, indexes: ReadonlyMap<number, number>, now: number): boolean {
  if (g > latest - VAULT_KEEP_GENERATIONS) return false;
  const nextAt = indexes.get(g + 1);
  // An index file that is gone counts as long ago.
  if (nextAt === undefined) return true;
  return now - nextAt > VAULT_RETENTION_MS;
}

export interface CollectionPlan {
  indexes: number[];
  packs: string[];
}

export interface CollectionInput {
  /** The latest generation, decoded (and verified). */
  latest: VaultIndexState;
  indexes: IndexFileInfo[];
  packs: PackFileInfo[];
  /** Packs this writer uploaded since it took the lock: never orphans. */
  uploaded: ReadonlySet<string>;
  now: number;
  /** At most this many deletions in the pass. Default 1 000. */
  max?: number;
}

/** The deletions of one collection pass. Empty when nothing may be deleted. */
export function planCollection(input: CollectionInput): CollectionPlan {
  const { latest, now } = input;
  const max = Math.min(VAULT_DELETE_MAX, input.max ?? VAULT_DELETE_MAX);
  const plan: CollectionPlan = { indexes: [], packs: [] };
  if (latest.hasExt || latest.generation < 1) return plan;
  const gen = latest.generation;
  const at = new Map(input.indexes.map((i) => [i.generation, i.mtime]));
  const room = () => plan.indexes.length + plan.packs.length < max;

  // 1. Index files of expired generations (never one of the three newest).
  for (const i of [...input.indexes].sort((a, b) => a.generation - b.generation)) {
    if (!room()) return plan;
    if (i.generation < gen - (VAULT_KEEP_GENERATIONS - 1) && isExpired(i.generation, gen, at, now)) plan.indexes.push(i.generation);
  }
  // 2. Graveyard packs whose last user is expired.
  const listed = new Set(input.packs.map((p) => p.id));
  for (const g of latest.grave) {
    if (!room()) return plan;
    if (g.died - 1 >= 1 && !isExpired(g.died - 1, gen, at, now)) continue;
    // A graveyard pack already gone counts as deleted; sending it again is
    // harmless, and leaves it out of the next commit's graveyard.
    plan.packs.push(g.pack);
  }
  // 3. Orphans.
  const table = new Set(latest.packs);
  const grave = new Set(latest.grave.map((g) => g.pack));
  for (const id of [...listed].sort()) {
    if (!room()) return plan;
    if (table.has(id) || grave.has(id) || input.uploaded.has(id)) continue;
    plan.packs.push(id);
  }
  return plan;
}

/**
 * The packs to repack, or null. After a commit, for each pack of the latest
 * table: its live bytes. S = the packs with fewer live bytes than half a data
 * area. Repack when |S| ≥ 2, the live bytes of S fit in at least one pack
 * fewer than S has, and more than half of all the data areas in the table is
 * dead.
 */
export function repackCandidates(state: VaultIndexState, packLog2: number): Set<string> | null {
  const area = packDataSize(packLog2);
  const live = liveBytesByPack(state.tree);
  const S: string[] = [];
  let liveS = 0;
  let liveAll = 0;
  for (const id of state.packs) {
    const n = live.get(id) ?? 0;
    liveAll += n;
    if (n < area / 2) {
      S.push(id);
      liveS += n;
    }
  }
  if (S.length < 2) return null;
  if (Math.ceil(liveS / area) >= S.length) return null;
  const total = state.packs.length * area;
  if (total - liveAll <= total / 2) return null;
  return new Set(S);
}
