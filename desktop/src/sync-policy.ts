// Which sync watchers should be running — decided here, carried out by the
// SyncSupervisor in src/sync.ts.
//
// ⚠ No `electron` import, so node:test can drive it (same boundary as
// src/notifications.ts).

import { pairView, type SyncActivity, type SyncStatus } from './syncstatus.ts';

/** The slice of an account this module reads. */
export interface PolicyAccount {
  id: string;
  /** Set while the server refuses the account's token (Account.signedOut). */
  signedOut?: string | null;
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

/** The preferences that decide which accounts may sync at all. */
export interface WatchGate {
  /** Settings / tray → Pause sync. Stored, so it survives a restart. */
  paused?: boolean;
  /** Accounts whose local filex folder is being moved. Their watcher was
   *  stopped for the move and must not be started again before it ends: one
   *  reading half-moved mirrors sees a mass local delete. */
  moving?: ReadonlySet<string>;
}

/**
 * The accounts handed to the supervisor. Paused means NONE: reconcile() then
 * stops every watcher and starts no new one — at startup too, which is the
 * point: the login item launches the app hidden, and a pause that lasted only
 * until the next sign-in would not be a pause.
 *
 * An account the server signed out is left out as well, until Reconnect: its
 * token is refused, and a watcher restarted with it would only be refused
 * again — every 30 seconds, and after every reboot.
 */
export function watcherAccounts<A extends PolicyAccount>(accounts: readonly A[], gate: WatchGate): A[] {
  if (gate.paused) return [];
  return accounts.filter((a) => !a.signedOut && !gate.moving?.has(a.id));
}

// ── bandwidth limits and the sync window ────────────────────────────────
//
// A first sync of 52.6 GiB filled the server's ~18 Mbit line for nine hours,
// and everyone else on that server slowed down. `--transfers` only changes how
// many files move at once. Settings now offers presets that become the
// engine's own flags:
//
//   --limit-down / --limit-up <KiB/s>   one budget per direction, shared by
//                                       every transfer of the watcher
//   --window HH:MM-HH:MM                local time, may wrap midnight; rounds
//                                       only start inside it

/** Settings' limit presets, KiB/s: unlimited, 10, 5 and 1 MB/s. */
export const LIMIT_PRESETS_KIB = [0, 10 * 1024, 5 * 1024, 1024] as const;

/** Settings' window presets: any time, evenings & nights, nights. */
export const WINDOW_PRESETS = ['', '19:00-08:00', '22:00-07:00'] as const;

/** What the watchers are started with — a slice of DesktopState. */
export interface WatchPrefs {
  limitDownKiB?: number;
  limitUpKiB?: number;
  syncWindow?: string;
}

/** A whole, non-negative KiB/s; anything else (including a string) is 0 —
 *  no limit. */
export function normLimit(v: unknown): number {
  if (typeof v !== 'number' || !Number.isFinite(v) || v < 0) return 0;
  return Math.floor(v);
}

const WINDOW_RE = /^(\d{2}):(\d{2})-(\d{2}):(\d{2})$/;

function windowMinutes(w: string): [start: number, end: number] | null {
  const m = WINDOW_RE.exec(w);
  if (!m) return null;
  const [sh, sm, eh, em] = [m[1], m[2], m[3], m[4]].map(Number) as [number, number, number, number];
  if (sh > 23 || eh > 23 || sm > 59 || em > 59) return null;
  const start = sh * 60 + sm;
  const end = eh * 60 + em;
  return start === end ? null : [start, end];
}

/** `HH:MM-HH:MM` that does not start where it ends, or '' — any time. The
 *  engine refuses anything else, and a watcher that will not start is worse
 *  than one with no window. */
export function normWindow(v: unknown): string {
  if (typeof v !== 'string') return '';
  const w = v.trim();
  return windowMinutes(w) ? w : '';
}

/** Whether a round may start at this minute of the (local) day. The end is
 *  exclusive; a window ending earlier than it starts runs over midnight.
 *  No window is any time. Same rule as the engine's own. */
export function windowContains(window: string, minuteOfDay: number): boolean {
  const mm = windowMinutes(window);
  if (!mm) return true;
  const [start, end] = mm;
  return start < end ? minuteOfDay >= start && minuteOfDay < end : minuteOfDay >= start || minuteOfDay < end;
}

/**
 * The argv of one account's watcher.
 *
 * ⚠ A flag is passed only when it asks for something. `--limit-down 0` would
 * mean the same as no flag to a current engine — and "unknown flag" to one
 * that predates it, which would then not start at all.
 */
export function watchArgs(accountId: string, prefs: WatchPrefs, interval: string): string[] {
  const args = ['sync', 'run', '--account', accountId, '--watch', interval, '--quiet'];
  const down = normLimit(prefs.limitDownKiB);
  const up = normLimit(prefs.limitUpKiB);
  const win = normWindow(prefs.syncWindow);
  if (down > 0) args.push('--limit-down', String(down));
  if (up > 0) args.push('--limit-up', String(up));
  if (win) args.push('--window', win);
  return args;
}

/** Equal for preferences the engine cannot tell apart — a change of this key
 *  is what restarts the watchers. */
export function watchPrefsKey(prefs: WatchPrefs): string {
  return `${normLimit(prefs.limitDownKiB)}/${normLimit(prefs.limitUpKiB)}/${normWindow(prefs.syncWindow)}`;
}

// ── the line under each folder in Settings ──────────────────────────────

/** What to say under one folder. The window turns `kind` into words. */
export type FolderView =
  | { kind: 'paused' }
  | { kind: 'signed-out' }
  | { kind: 'starting' }
  | ({ kind: 'active' } & Omit<SyncActivity, 'pairId'>)
  | { kind: 'busy'; detail: string }
  | { kind: 'window'; window: string }
  | { kind: 'error'; message: string; exited?: string; restartAt?: number }
  | { kind: 'pending' }
  | { kind: 'moving' }
  | { kind: 'watching' }
  | { kind: 'stopped' };

/**
 * One folder's line, decided here rather than in the page: it used to be the
 * account's LAST stdout line under every one of its folders — "pair-2:
 * already in step" under pair-1 — or the account's last error under all of
 * them. Which error belongs to which folder is syncstatus.ts's rule
 * (pairView); this only decides what the line leads with.
 */
export function folderView(input: {
  pairId: string;
  paused: boolean;
  signedOut: boolean;
  /** Its account's local filex folder is being moved (its watcher is off). */
  moving?: boolean;
  status: SyncStatus | null | undefined;
  minuteOfDay: number;
}): FolderView {
  // Before everything: the move is what is happening to it, and its stopped
  // watcher read "stopped" in red for the hours a copy to another drive takes.
  if (input.moving) return { kind: 'moving' };
  if (input.paused) return { kind: 'paused' };
  if (input.signedOut) return { kind: 'signed-out' };
  const st = input.status;
  if (!st) return { kind: 'starting' };
  if (st.running && st.active && st.active.pairId === input.pairId) {
    const { pairId: _pairId, ...activity } = st.active;
    return { kind: 'active', ...activity };
  }
  const view = pairView(st, input.pairId);
  // Another filex on this computer syncs this folder (the other copy of this
  // app, or the CLI). Before any error: nothing here runs for the folder, so
  // an error left from before it was taken is not what is true of it now —
  // and this is not a failure, the folder IS being synced.
  if (st.running && view.busy) return { kind: 'busy', detail: view.busy.detail };
  // Its own error, or the engine's (which is every folder's). An engine that
  // stopped on its own carries its exit code, so the page says so in its
  // language, and when the supervisor starts it again.
  const own = view.error;
  if (own) {
    if (own === st.lastError && st.exited) {
      return {
        kind: 'error',
        message: own,
        exited: st.exited,
        ...(st.restartAt ? { restartAt: st.restartAt } : {}),
      };
    }
    return { kind: 'error', message: own };
  }
  if (st.waitingWindow && !windowContains(st.waitingWindow, input.minuteOfDay)) {
    return { kind: 'window', window: st.waitingWindow };
  }
  // ⚠ "Watching for changes" only for a folder a pass has finished for: one
  // just added, waiting behind the others for its first sync, read like a
  // folder that is in step.
  if (st.running) return view.passed ? { kind: 'watching' } : { kind: 'pending' };
  return { kind: 'stopped' };
}

/**
 * How long the supervisor waits before starting an engine that stopped on its
 * own again, by how many times in a row it has: 5 s, 15 s, a minute, then
 * every five minutes. A run that lasted a while starts the count again
 * (sync.ts). It used to stay stopped until something else — a folder added, a
 * setting changed — made the app look at its accounts again.
 */
export function restartDelay(crashesInARow: number): number {
  const steps = [5_000, 15_000, 60_000, 300_000];
  return steps[Math.min(Math.max(crashesInARow, 1), steps.length) - 1];
}

/** What the tray's tooltip says (trayTooltip). */
export interface TrayFacts {
  paused: boolean;
  /** The unread count in words, or null. */
  unreadLabel: string | null;
  /** An engine is in the middle of a pass. */
  syncing: boolean;
  /** A folder, or an engine, is failing. */
  failing: boolean;
}

/**
 * The tray icon's tooltip, the one thing on screen when the window is closed.
 *
 * ⚠ One sentence from every fact, built in one place: the pause set it, and
 * the unread count's own tooltip overwrote it the same moment, so a paused
 * client looked like any other. Sync itself was not in it at all.
 */
export function trayTooltip(f: TrayFacts, words: { paused: string; syncing: string; failing: string }): string {
  const parts = ['filex'];
  if (f.paused) parts.push(words.paused);
  else if (f.failing) parts.push(words.failing);
  else if (f.syncing) parts.push(words.syncing);
  if (f.unreadLabel) parts.push(f.unreadLabel);
  return parts.join(' — ');
}

// ── held items ───────────────────────────────────────────────────────────

/** The slice of a pair (`filex sync list --json`) that says it is holding. */
export interface HoldingPair {
  /** Set by the engine when a FIRST run would have pushed a stale mirror's
   *  worth of local-only items into a server folder that has content. */
  hold_new?: boolean;
  /** How many items the last run held back. */
  held?: number;
}

/**
 * Carries out a person's answer to a hold (`sync confirm` / `sync discard`):
 * stop the account's watcher, run the engine's command, then re-read the pairs
 * (which restarts the watcher) — whether the command worked or not.
 *
 * ⚠ The watcher stops FIRST. A pass it has in flight writes the hold again
 * when it finishes (read, change, write the hold file); landing between the
 * command's own read and write, that write would undo the answer, silently.
 * The order used to be answer → stop.
 */
export async function answerHold(steps: {
  stop: () => void;
  act: () => Promise<string>;
  refresh: () => Promise<void>;
  onError: (err: unknown) => Promise<void> | void;
}): Promise<string | null> {
  steps.stop();
  try {
    return await steps.act();
  } catch (e) {
    await steps.onError(e);
    return null;
  } finally {
    await steps.refresh();
  }
}

/**
 * How many items this pair is waiting on a decision for — the number the
 * notice in Settings shows. Only while the pair is holding AND holds some: a
 * count left behind without the flag is history, and a pair that holds but
 * held nothing last time has nothing to decide.
 */
export function heldItems(p: HoldingPair): number {
  if (p.hold_new !== true) return 0;
  return typeof p.held === 'number' && Number.isFinite(p.held) && p.held > 0 ? Math.floor(p.held) : 0;
}
