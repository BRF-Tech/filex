// Which sync watchers should be running — decided here, carried out by the
// SyncSupervisor in src/sync.ts.
//
// ⚠ No `electron` import, so node:test can drive it (same boundary as
// src/notifications.ts).

import { pairView, unexpectedExitLine, type SyncActivity, type SyncStatus } from './syncstatus.ts';

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
//
// ⚠ The window is READ by the engine alone (B17, #213): Settings stores what
// `filex sync window --json` answered (sync.ts checkWindow, main.ts
// settings:set), and this file passes it on without parsing it. A parser of
// its own here was stricter than the engine's ("7:00-9:00" and spaces), so a
// window the engine ran with read as "no window" on the screen.

/** Settings' limit presets, KiB/s: unlimited, 10, 5 and 1 MB/s. */
export const LIMIT_PRESETS_KIB = [0, 10 * 1024, 5 * 1024, 1024] as const;

/** Settings' window presets: any time, evenings & nights, nights. */
export const WINDOW_PRESETS = ['', '19:00-08:00', '22:00-07:00'] as const;

/** What the watchers are started with — a slice of DesktopState. */
export interface WatchPrefs {
  limitDownKiB?: number;
  limitUpKiB?: number;
  /** The engine's canonical window (checkWindow), '' = any time. */
  syncWindow?: string;
  /** A language for `--lang`. ⚠ The app passes none (#191): the engine
   *  then says its messages in the language of the account it syncs, which
   *  it asks the server for (main.ts currentWatchPrefs). Kept for a caller
   *  that names one (the engine's own flag, verbatim). */
  lang?: string;
}

/** A whole, non-negative KiB/s; anything else (including a string) is 0 —
 *  no limit. */
export function normLimit(v: unknown): number {
  if (typeof v !== 'number' || !Number.isFinite(v) || v < 0) return 0;
  return Math.floor(v);
}

/** The stored window as it is passed on: a string, trimmed, else '' (any
 *  time). Not a judgement of it — that is the engine's (checkWindow). */
export function storedWindow(v: unknown): string {
  return typeof v === 'string' ? v.trim() : '';
}

/** A language tag for `--lang`, or '' (the engine then asks the server for
 *  the account's). Only the shape is checked: which languages exist is the
 *  engine's (and the server's language packs') to know. */
export function engineLangTag(v: unknown): string {
  if (typeof v !== 'string') return '';
  const t = v.trim();
  return /^[A-Za-z]{2,8}(?:[-_][A-Za-z0-9]{1,8})*$/.test(t) ? t : '';
}

/**
 * The argv of one account's watcher.
 *
 * `--json`: the engine reports in events, each with its sentence said in the
 * account's language (syncstatus.ts). ⚠ An engine older than 0.54 does not know the
 * flag and refuses to start; the app always ships its own engine, so that is
 * only a FILEX_CLI pointing at an older binary — the folder then says the
 * engine could not start, with the engine's own words (folderView noStream).
 *
 * ⚠ Any other flag is passed only when it asks for something.
 */
export function watchArgs(accountId: string, prefs: WatchPrefs, interval: string): string[] {
  const args = ['sync', 'run', '--account', accountId, '--watch', interval, '--quiet', '--json'];
  const lang = engineLangTag(prefs.lang);
  const down = normLimit(prefs.limitDownKiB);
  const up = normLimit(prefs.limitUpKiB);
  const win = storedWindow(prefs.syncWindow);
  if (lang) args.push('--lang', lang);
  if (down > 0) args.push('--limit-down', String(down));
  if (up > 0) args.push('--limit-up', String(up));
  if (win) args.push('--window', win);
  return args;
}

/** Equal for preferences the engine cannot tell apart — a change of this key
 *  is what restarts the watchers (a new language too: the engine says its
 *  messages in the one it was started with). */
export function watchPrefsKey(prefs: WatchPrefs): string {
  return `${normLimit(prefs.limitDownKiB)}/${normLimit(prefs.limitUpKiB)}/${storedWindow(prefs.syncWindow)}/${engineLangTag(prefs.lang)}`;
}

// ── the line under each folder in Settings ──────────────────────────────

/** What to say under one folder. A `message` is the engine's sentence, shown
 *  as it is; the window words the other kinds (the app's own states). */
export type FolderView =
  | { kind: 'paused' }
  | { kind: 'signed-out' }
  | { kind: 'starting' }
  | ({ kind: 'active' } & Omit<SyncActivity, 'pairId'>)
  | { kind: 'busy'; detail: string; message: string }
  | { kind: 'window'; window: string; message: string }
  | { kind: 'error'; message: string; exited?: string; reason?: string; restartAt?: number; noStream?: boolean }
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
  /** The time now (epoch ms): a wait for the window is over once the engine's
   *  `opensAt` has passed. */
  now: number;
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
  if (st.running && view.busy) return { kind: 'busy', detail: view.busy.detail, message: view.busy.message };
  // Its own error, or the engine's (which is every folder's). An engine that
  // stopped on its own carries its exit code, so the page says so in its
  // language, and when the supervisor starts it again.
  // ⚠ And WHY, when the engine said: its last line (lastError) used to be
  // replaced on screen by the code alone. The line the app writes itself for
  // an engine that said nothing is no reason and is left out.
  const own = view.error;
  if (own) {
    if (own === st.lastError && st.exited) {
      return {
        kind: 'error',
        message: own,
        exited: st.exited,
        ...(own !== unexpectedExitLine(st.exited) ? { reason: own } : {}),
        ...(st.restartAt ? { restartAt: st.restartAt } : {}),
        ...(st.noStream ? { noStream: true } : {}),
      };
    }
    return { kind: 'error', message: own };
  }
  // The engine said when the window opens: until then the folder waits for
  // it. Past that moment the engine's next pass is due, whatever it has
  // said since (a window that opened onto nothing to do prints no pass).
  const ww = st.waitingWindow;
  if (ww && (ww.opensAt === null || input.now < ww.opensAt)) {
    return { kind: 'window', window: ww.window, message: ww.message };
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

/**
 * After the supervisor's restart of an engine that stopped on its own:
 * whether to try again later (after restartDelay of one more crash).
 *
 * ⚠ The timer used to be armed only when a process CLOSED. A restart that
 * started no process — the pair list could not be read, the engine could not
 * be launched — was therefore the last one: the folder said "starting it
 * again shortly" for good, with a time long past. Tried again while the
 * account still has its stopped engine's status (reconcile drops the status
 * of an account that no longer gets a watcher: paused, moving, nothing
 * paired), nothing runs, no restart is pending and the server has not signed
 * it out.
 */
export function restartAgain(after: {
  stopping: boolean;
  running: boolean;
  pending: boolean;
  status: Pick<SyncStatus, 'exited' | 'signedOut'> | null | undefined;
}): boolean {
  if (after.stopping || after.running || after.pending) return false;
  const st = after.status;
  return !!st && !!st.exited && st.signedOut !== true;
}

/**
 * The accounts that keep or get a watcher, from the pair list — or, when the
 * list could not be READ (`pairs` null), the accounts that already have one
 * (running, or stopped and waiting for its restart: `current`).
 *
 * ⚠ A list that could not be read is not an empty list. `filex sync list`
 * failing — the CLI could not be run, pairs.json could not be read for a
 * moment — used to answer "nothing is paired": every watcher was killed, every
 * folder's state dropped, and nothing was started again. `accounts` is still
 * the gate (watcherAccounts): a paused or signed-out account is left out,
 * read or not.
 */
export function watchersWanted(
  accounts: readonly PolicyAccount[],
  pairs: readonly PolicyPair[] | null,
  current: ReadonlySet<string>,
): Set<string> {
  if (pairs) return wantedWatchers(accounts, pairs);
  return new Set(accounts.filter((a) => current.has(a.id)).map((a) => a.id));
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
  return parts.join(' - ');
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

/**
 * Whether removing a folder ("Stop syncing") has to stop its account's watcher
 * first: a pass of that folder is under way. The watcher only re-reads its
 * folders between passes, so the pass went on — for hours, for a first sync —
 * after the folder's card had gone, with nothing on screen to show it.
 */
export function stopForRemoval(st: SyncStatus | null | undefined, pairId: string): boolean {
  return !!st && st.running && st.active?.pairId === pairId;
}

/**
 * Takes a folder out of sync — Settings' "Stop syncing" and the explorer's
 * "Keep online only" alike: its account's watcher is stopped first when a pass
 * of THIS folder is under way (stopForRemoval), the pair is removed, and the
 * pair list is read again — which starts the watcher without it — whether the
 * remove worked or not.
 *
 * ⚠ One helper for both, because there were two ways and only one of them
 * stopped the pass: "Keep online only" removed the pair without it, and could
 * then move the folder to the Trash while that very pass was walking it.
 */
export async function removeFolder(steps: {
  status: SyncStatus | null | undefined;
  pairId: string;
  stop: () => void;
  remove: () => Promise<void>;
  refresh: () => Promise<void>;
}): Promise<void> {
  if (stopForRemoval(steps.status, steps.pairId)) steps.stop();
  try {
    await steps.remove();
  } finally {
    await steps.refresh();
  }
}
