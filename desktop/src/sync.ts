// Folder sync, driven by the `filex` CLI that ships inside the app.
//
// The engine itself lives in the Go binary (backend/internal/filesync). Electron
// does NOT reimplement it, for two reasons:
//
//   - Sync deletes files. That logic is tested against a real server and an
//     in-memory one; a second implementation in TypeScript would be a second
//     set of bugs, in the least forgiving part of the product.
//   - The same binary is the CLI users asked for. One implementation, two
//     front ends: `filex sync run` in a terminal and this app do the same thing
//     to the same state file, so they can never disagree about what is paired.
//
// The app therefore shells out for everything and keeps no pairing state of its
// own — ~/.filex/sync/pairs.json is the single source of truth. (A portable
// build moves that one directory next to its .exe with FILEX_SYNC_DIR; see
// engineEnv below. It is still one store, just not in the home directory of a
// machine the user is only borrowing.)

import { spawn, execFile } from 'node:child_process';
import { existsSync } from 'node:fs';
import path from 'node:path';
import { app } from 'electron';
import type { Account } from './accounts.js';
import { portableMode } from './portable.js';
import { SyncStatusTracker, type SyncStatus } from './sync-output.js';
import { wantedWatchers, watchArgs, type WatchPrefs } from './sync-policy.js';

export type { SyncActivity, SyncStatus } from './sync-output.js';

export interface Pair {
  id: string;
  local: string;
  remote: string;
  account?: string;
  paused?: boolean;
  /** Single-file pair: local is a file path, remote names a file. */
  file?: boolean;
  /** The engine is holding items here that the server does not have (or has
   *  differently) until someone decides — see heldItems in sync-policy.ts. */
  hold_new?: boolean;
  /** How many items the last run held back. */
  held?: number;
}

/** How often each account's watcher re-checks. Frequent enough to feel live,
 *  slow enough that a folder of thousands of files is not re-walked constantly. */
const WATCH_INTERVAL = '30s';

/**
 * Locates the bundled CLI.
 *
 * ⚠ Returns null rather than a guessed path when it is missing. The UI shows
 * that plainly — a sync panel that looks armed but silently transfers nothing is
 * worse than one that says the engine is not installed.
 */
export function cliPath(): string | null {
  const exe = process.platform === 'win32' ? 'filex.exe' : 'filex';
  const appPath = app.getAppPath();
  const candidates = [
    process.env.FILEX_CLI, // explicit override
    // Packaged: electron-builder copies build/bin -> resources/bin.
    path.join(process.resourcesPath ?? '', 'bin', exe),
    // ⚠ Unpackaged (`electron .`): getAppPath() is the desktop/ folder itself,
    // so the binary sits at desktop/build/bin — NOT one level up. Without this
    // the app ran from source with "the sync engine is missing", while the
    // tests passed because they set FILEX_CLI and never exercised this
    // resolution at all.
    path.join(appPath, 'build', 'bin', exe),
    path.join(appPath, '..', 'bin', exe),
  ].filter(Boolean) as string[];
  return candidates.find((p) => existsSync(p)) ?? null;
}

/**
 * The environment every engine invocation gets.
 *
 * ⚠ FILEX_SYNC_DIR is what keeps a PORTABLE copy portable. The engine's store
 * — the pairs, the per-pair baselines, and the local trash holding real copies
 * of files it deleted — defaults to `~/.filex/sync`, which on a borrowed
 * machine is somebody else's home directory. There is one home for this app's
 * files and it is the folder next to the .exe. See src/portable.ts.
 */
function engineEnv(extra: NodeJS.ProcessEnv = {}): NodeJS.ProcessEnv {
  const p = portableMode();
  return {
    ...process.env,
    ...(p.portable && p.dataDir ? { FILEX_SYNC_DIR: p.syncDir } : {}),
    ...extra,
  };
}

function run(args: string[], env: NodeJS.ProcessEnv = {}): Promise<string> {
  const bin = cliPath();
  if (!bin) {
    return Promise.reject(
      new Error('The sync engine is not bundled with this build (filex CLI not found).'),
    );
  }
  return new Promise((resolve, reject) => {
    execFile(
      bin,
      args,
      { env: engineEnv(env), windowsHide: true, maxBuffer: 8 * 1024 * 1024 },
      (err, stdout, stderr) => {
        if (err) {
          reject(new Error((stderr || stdout || err.message).trim().split('\n').slice(-1)[0]));
          return;
        }
        resolve(stdout);
      },
    );
  });
}

export async function listPairs(): Promise<Pair[]> {
  try {
    return JSON.parse(await run(['sync', 'list', '--json'])) as Pair[];
  } catch {
    // No engine, or nothing paired yet. Either way there is nothing to show,
    // and the panel says which.
    return [];
  }
}

export async function addPair(
  local: string,
  remote: string,
  accountId: string,
  isFile = false,
): Promise<void> {
  const args = ['sync', 'add', local, remote, '--account', accountId];
  if (isFile) args.push('--file');
  await run(args);
}

export interface TrashItem {
  pair: string;
  rel: string;
  deleted: string;
  size: number;
}

/** What sync removed from this machine and can still put back. */
export async function listTrash(pairId?: string): Promise<TrashItem[]> {
  try {
    const args = ['sync', 'trash', '--json'];
    if (pairId) args.push('--pair', pairId);
    return JSON.parse(await run(args)) as TrashItem[];
  } catch {
    return [];
  }
}

export async function removePair(id: string): Promise<void> {
  await run(['sync', 'remove', id]);
}

/** Repoints a pair at a folder that physically moved. The engine keeps the
 *  pair's baseline, so the next run is an ordinary incremental pass. */
export async function movePair(id: string, newLocal: string): Promise<void> {
  await run(['sync', 'move', id, newLocal]);
}

/** The items a pair holds go to the server on its next run. Returns the
 *  engine's one-line answer. */
export async function confirmHeld(id: string): Promise<string> {
  return (await run(['sync', 'confirm', id])).trim();
}

/** The items a pair holds move into its local sync trash (kept 30 days).
 *  Returns the engine's one-line answer. */
export async function discardHeld(id: string): Promise<string> {
  return (await run(['sync', 'discard', id])).trim();
}

/**
 * Keeps one `filex sync run --watch` process alive per signed-in account.
 *
 * ⚠ One process per account, never one for all of them: a token authenticates
 * against exactly one server, so a single process would try to sync account B's
 * folders with account A's credentials.
 */
export interface SupervisorHooks {
  /** Something about sync changed — repaint. */
  onChange: () => void;
  /** The server refused this account's token. The watcher is already
   *  stopped; the caller marks the account so that reconcile() does not start
   *  it again until the user reconnects. */
  onSignedOut?: (accountId: string) => void;
  /** A pair's run held items for a decision: the cue to re-read the pair
   *  list, which carries the count the app shows. */
  onHold?: (accountId: string, pairId: string, count: number) => void;
  /** The bandwidth limits and sync window a watcher is started with (read at
   *  start; a change means stop + reconcile). */
  watchPrefs?: () => WatchPrefs;
}

export class SyncSupervisor {
  private procs = new Map<string, ReturnType<typeof spawn>>();
  private trackers = new Map<string, SyncStatusTracker>();
  private stopping = false;
  private readonly onChange: () => void;
  private readonly onSignedOut: (accountId: string) => void;
  private readonly onHold: (accountId: string, pairId: string, count: number) => void;
  private readonly watchPrefs: () => WatchPrefs;

  constructor(hooks: SupervisorHooks) {
    this.onChange = hooks.onChange;
    this.onSignedOut = hooks.onSignedOut ?? (() => {});
    this.onHold = hooks.onHold ?? (() => {});
    this.watchPrefs = hooks.watchPrefs ?? (() => ({}));
  }

  statuses(): SyncStatus[] {
    return [...this.trackers.values()].map((t) => t.status);
  }

  /** Starts watchers for accounts that have pairs, stops the rest. Safe to call
   *  whenever pairs or accounts change. */
  async reconcile(accounts: Account[], tokenFor: (id: string) => string | null): Promise<void> {
    if (this.stopping) return;
    const pairs = await listPairs();
    const wanted = wantedWatchers(accounts, pairs);

    for (const [id, proc] of this.procs) {
      if (!wanted.has(id)) {
        proc.kill();
        this.procs.delete(id);
      }
    }
    // Including a watcher that had already exited on its own: its account
    // has nothing left to sync, so its last words are not news any more.
    for (const id of [...this.trackers.keys()]) {
      if (!wanted.has(id)) this.trackers.delete(id);
    }
    // A running watcher is not restarted for an unpaired folder (it re-reads
    // pairs.json between rounds), so the folder's last error goes here.
    for (const [id, tracker] of this.trackers) {
      tracker.retainPairs(new Set(pairs.filter((p) => p.account === id).map((p) => p.id)));
    }
    for (const acc of accounts) {
      if (wanted.has(acc.id) && !this.procs.has(acc.id)) {
        this.start(acc, tokenFor(acc.id));
      }
    }
    this.onChange();
  }

  private start(acc: Account, token: string | null): void {
    const bin = cliPath();
    if (!bin || !token) return;

    const proc = spawn(
      bin,
      // --limit-down / --limit-up / --window only when Settings asks for them.
      watchArgs(acc.id, this.watchPrefs(), WATCH_INTERVAL),
      {
        env: engineEnv({ FILEX_URL: acc.serverUrl, FILEX_TOKEN: token }),
        windowsHide: true,
        stdio: ['ignore', 'pipe', 'pipe'],
      },
    );
    // The engine's output is parsed in ONE place (src/sync-output.ts) — line
    // by line, not chunk by chunk — and the UI gets typed data.
    const tracker = new SyncStatusTracker(acc.id);
    this.trackers.set(acc.id, tracker);
    this.procs.set(acc.id, proc);

    // The server refused the token. Said once per watcher; an older engine
    // that keeps looping on the 401 instead of exiting is stopped here.
    let refused = false;
    const signedOut = () => {
      if (refused || !tracker.status.signedOut) return;
      refused = true;
      if (this.procs.get(acc.id) === proc) {
        proc.kill();
        this.procs.delete(acc.id);
      }
      tracker.status.running = false;
      tracker.status.active = null;
      this.onSignedOut(acc.id);
    };

    const holds = () => {
      for (const h of tracker.takeHolds()) this.onHold(acc.id, h.pairId, h.count);
    };

    proc.stdout?.on('data', (c: Buffer) => {
      if (!tracker.feed(c, 'out')) return;
      holds();
      this.onChange();
    });
    proc.stderr?.on('data', (c: Buffer) => {
      if (!tracker.feed(c, 'err')) return;
      signedOut();
      this.onChange();
    });

    // ⚠ 'close', not 'exit': 'exit' can fire while the pipes still hold the
    // process's last words — the one line that says WHY it stopped.
    proc.on('close', (code, signal) => {
      // A watcher this supervisor already let go of — stop() during a root
      // move, reconcile() after a sign-out — must not touch the bookkeeping
      // of its successor. Its exit can land AFTER the replacement started,
      // and deleting the new process's entry here would make the next
      // reconcile() start a second watcher for the same account: two
      // engines racing over one baseline.
      if (this.procs.get(acc.id) !== proc) return;
      this.procs.delete(acc.id);
      tracker.end();
      tracker.exited(code, this.stopping, signal);
      holds();
      signedOut();
      this.onChange();
    });
  }

  /** Kills ONE account's watcher. Root migration renames mirrors on disk;
   *  a watcher mid-round holds the old paths in memory and would read the
   *  half-moved tree as a mass local delete. reconcile() restarts it. */
  stop(accountId: string): void {
    const proc = this.procs.get(accountId);
    if (proc) {
      proc.kill();
      this.procs.delete(accountId);
    }
    const st = this.trackers.get(accountId)?.status;
    if (st) {
      st.running = false;
      st.active = null;
    }
    this.onChange();
  }

  stopAll(): void {
    this.stopping = true;
    for (const p of this.procs.values()) p.kill();
    this.procs.clear();
  }
}
