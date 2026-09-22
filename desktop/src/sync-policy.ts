// Which sync watchers should be running — decided here, carried out by the
// SyncSupervisor in src/sync.ts.
//
// ⚠ No `electron` import, so node:test can drive it (same boundary as
// src/notifications.ts).

/** The slice of an account this module reads. */
export interface PolicyAccount {
  id: string;
}

/** The slice of a pair (`filex sync list --json`) this module reads. */
export interface PolicyPair {
  id: string;
  account?: string;
  paused?: boolean;
}

/**
 * The accounts that get a `filex sync run --watch` process: signed in on this
 * computer, and with at least one pair that is not paused.
 *
 * ⚠ One process per ACCOUNT, never one for all of them: a token authenticates
 * against exactly one server, so a single process would try to sync account
 * B's folders with account A's credentials. An account that is not in
 * `accounts` any more — signed out — has no watcher, whatever pairs.json still
 * records against it.
 */
export function wantedWatchers(accounts: readonly PolicyAccount[], pairs: readonly PolicyPair[]): Set<string> {
  return new Set(
    accounts.filter((a) => pairs.some((p) => p.account === a.id && !p.paused)).map((a) => a.id),
  );
}
