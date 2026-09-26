/**
 * Following a storage from the outside — a scan to its end, a deletion to the
 * storage leaving the list — for the admin panel's "Sync now" and delete
 * (web composables/useSyncNow, stores/storages) and the explorer's "Catalog
 * everything" (FileExplorer `catalogAll`): ONE follower, on ONE signal, with
 * ONE poll.
 *
 * The signal is the worker's own (#66): `GET /api/admin/storages` carries
 * `running` — a scan is walking the storage right now — and, once it drops,
 * `last_sync_state` / `last_sync_error` say how the scan ended. `POST
 * …/sync` answers only once the scan holds the storage (and answers
 * `status: "running"` when one already did), so following starts after that
 * answer, and `running` is true until the scan is over.
 *
 * ⚠⚠ It replaced two followers of the same scan: #66 read that list, and #69
 * (lib/catalogRun) read the storage's runs table instead, with heuristics to
 * tell the new run from older ones, and gave up after 60 s. And each storage
 * #66 followed read the WHOLE list again every 3 s on its own — five followed
 * storages, five lists every 3 s. Here every tick reads the list once, for
 * everything followed, and stops reading when nothing is.
 */
import { ref } from 'vue';

/** The fields of a storage-list row this reads. */
export interface WatchedStorageRow {
  id: number;
  running?: boolean;
  last_sync_state?: string | null;
  last_sync_error?: string | null;
}

/**
 * How a followed scan ended: `ok`, `failed` (with the server's `error` when it
 * wrote one), `aborted` (stopped before it finished), `gone` (the storage
 * left the list), `unknown` (the list could not be read, many times over, or
 * named an end this does not know).
 */
export type ScanEnd = { status: 'ok' | 'failed' | 'aborted' | 'gone' | 'unknown'; error?: string };

export type StorageWatchKind = 'scan' | 'deletion';

export interface StorageWatch {
  /** Follows the scan of storage `id` — call it once `POST …/sync` has
   *  answered — to its end; null when the watch was stopped. A storage
   *  already followed is joined, not followed twice. */
  scan(id: number): Promise<ScanEnd | null>;
  /** Follows a deletion the server is still doing: true once the storage has
   *  left the list, false when the list could not be read (many times over),
   *  null when the watch was stopped. */
  deletion(id: number): Promise<boolean | null>;
  /** What storage `id` is followed for, or null. Reactive. */
  watching(id: number): StorageWatchKind | null;
  /** Stops every follower (the page went away): each ends with null. */
  stop(): void;
}

/** How often the list is read while anything is followed. */
export const STORAGE_WATCH_MS = 3000;
/** Consecutive unreadable lists before a follower gives up. */
export const STORAGE_WATCH_GIVE_UP = 20;

function scanEnd(row: WatchedStorageRow): ScanEnd {
  const error = row.last_sync_error || undefined;
  switch (row.last_sync_state) {
    case 'ok':
      return { status: 'ok' };
    case 'failed':
    case 'error':
      return error ? { status: 'failed', error } : { status: 'failed' };
    case 'aborted':
      return { status: 'aborted' };
    default:
      return { status: 'unknown' };
  }
}

type Settle = (row: WatchedStorageRow | undefined, unreadable: boolean) => boolean;

export function createStorageWatch(io: {
  /** GET /api/admin/storages. */
  list(): Promise<WatchedStorageRow[]>;
  pollMs?: number;
  giveUpAfter?: number;
}): StorageWatch {
  const pollMs = io.pollMs ?? STORAGE_WATCH_MS;
  const giveUpAfter = io.giveUpAfter ?? STORAGE_WATCH_GIVE_UP;
  /** `${kind}:${id}` → the followers waiting on it. */
  const followers = new Map<string, { id: number; kind: StorageWatchKind; settle: Settle[] }>();
  /** Bumped when `followers` changes, so `watching` is reactive. */
  const version = ref(0);
  let timer: ReturnType<typeof setTimeout> | undefined;
  let reading = false;
  let stopped = false;
  let unreadable = 0;
  const pending = new Set<(v: null) => void>();

  function schedule() {
    if (stopped || timer !== undefined || reading || followers.size === 0) return;
    timer = setTimeout(() => {
      timer = undefined;
      void tick();
    }, pollMs);
  }

  async function tick() {
    if (stopped || followers.size === 0) return;
    reading = true;
    let rows: WatchedStorageRow[] | null = null;
    try {
      rows = await io.list();
      unreadable = 0;
    } catch {
      unreadable++;
    } finally {
      reading = false;
    }
    if (stopped) return;
    const lost = rows === null && unreadable >= giveUpAfter;
    if (rows !== null || lost) {
      for (const [key, f] of followers) {
        const row = rows?.find((r) => r.id === f.id);
        const left = f.settle.filter((s) => !s(row, lost));
        if (left.length === 0) followers.delete(key);
        else f.settle = left;
      }
      version.value++;
    }
    schedule();
  }

  function follow<T>(id: number, kind: StorageWatchKind, decide: (row: WatchedStorageRow | undefined, lost: boolean) => T | undefined): Promise<T | null> {
    if (stopped) return Promise.resolve(null);
    return new Promise<T | null>((resolve) => {
      const done = (v: T | null) => {
        pending.delete(done as (v: null) => void);
        resolve(v);
      };
      pending.add(done as (v: null) => void);
      const key = `${kind}:${id}`;
      const settle: Settle = (row, lost) => {
        const end = decide(row, lost);
        if (end === undefined) return false;
        done(end);
        return true;
      };
      const f = followers.get(key);
      if (f) f.settle.push(settle);
      else followers.set(key, { id, kind, settle: [settle] });
      version.value++;
      schedule();
    });
  }

  return {
    scan: (id) =>
      follow<ScanEnd>(id, 'scan', (row, lost) => {
        if (lost) return { status: 'unknown' };
        if (!row) return { status: 'gone' };
        return row.running ? undefined : scanEnd(row);
      }),
    deletion: (id) =>
      follow<boolean>(id, 'deletion', (row, lost) => {
        if (lost) return false;
        return row ? undefined : true;
      }),
    watching(id) {
      void version.value;
      if (followers.has(`deletion:${id}`)) return 'deletion';
      if (followers.has(`scan:${id}`)) return 'scan';
      return null;
    },
    stop() {
      stopped = true;
      if (timer !== undefined) clearTimeout(timer);
      timer = undefined;
      followers.clear();
      version.value++;
      for (const done of [...pending]) done(null);
    },
  };
}
