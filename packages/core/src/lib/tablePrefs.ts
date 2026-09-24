/**
 * tablePrefs — where every table that is NOT the explorer's listing keeps its
 * columns and its sort.
 *
 * One corner of the per-person view document (`lib/viewPrefs` →
 * `viewPrefsSlot('t')`), keyed by a table id the table is given:
 *
 *   { "t": { "admin.users": { "c": {w, hidden, o}, "s": {k, d} }, … } }
 *
 * ⚠⚠ On the ACCOUNT, like the explorer's folders, and for the same reason the
 * owner gave for those ("tarayıcıya değil db'ye kaydedeceğiz"): a column
 * dragged wider in the Users table on the office machine is there at home,
 * and a second person signing into the same browser does not inherit it.
 * With nobody signed in (a public page, an app token) the slot is
 * session-only — the table still resizes and sorts, it just forgets on reload.
 *
 * ⚠ The id is the table's name, not its route: two tables on one page (the
 * Replica screen draws three) need three ids, and a table that moves to
 * another page keeps its arrangement. `web/tests/ui/tablePinnedActions.test.ts`
 * refuses a table with no id and two tables with the same one.
 */
import { viewPrefsSlot } from './viewPrefs';
import type { ColsState, ColumnBacking } from './tableColumns';

/** One table's remembered state. */
export interface TablePrefs {
  c?: ColsState;
  s?: { k: string; d: 'asc' | 'desc' };
}

type TableMap = Record<string, TablePrefs>;

/**
 * The slot is created lazily and ONCE: `viewPrefsSlot` throws for a reserved
 * name, and calling it at module load would make importing this file a side
 * effect in every bundle that never draws a table.
 */
let slot: ReturnType<typeof viewPrefsSlot<TableMap>> | null = null;
function tables() {
  if (!slot) slot = viewPrefsSlot<TableMap>('t');
  return slot;
}

function read(): TableMap {
  const v = tables().get();
  return v && typeof v === 'object' && !Array.isArray(v) ? v : {};
}

function write(id: string, patch: Partial<TablePrefs>): void {
  const all = read();
  const cur: TablePrefs = { ...(all[id] ?? {}) };
  for (const [k, v] of Object.entries(patch) as [keyof TablePrefs, unknown][]) {
    if (v === undefined) delete cur[k];
    else (cur as Record<string, unknown>)[k] = v;
  }
  const next = { ...all };
  if (Object.keys(cur).length) next[id] = cur;
  else delete next[id];
  tables().set(next);
}

/** The backing a `ColumnStore` for table `id` reads and writes through. */
export function tableColumnBacking(id: string): ColumnBacking {
  return {
    get: () => read()[id]?.c,
    set: (next) => {
      const says =
        Object.keys(next.w).length > 0 || next.hidden.length > 0 || (next.o?.length ?? 0) > 0;
      write(id, { c: says ? next : undefined });
    },
  };
}

/** The table's remembered sort, or null. */
export function tableSort(id: string): { key: string; dir: 'asc' | 'desc' } | null {
  const s = read()[id]?.s;
  if (!s || typeof s.k !== 'string' || (s.d !== 'asc' && s.d !== 'desc')) return null;
  return { key: s.k, dir: s.d };
}

/** Remember (or with null, forget) the table's sort. */
export function setTableSort(id: string, sort: { key: string; dir: 'asc' | 'desc' } | null): void {
  write(id, { s: sort ? { k: sort.key, d: sort.dir } : undefined });
}
