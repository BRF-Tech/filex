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
import {
  absorbLine,
  LineReader,
  markExited,
  newStatus,
  refusalApplies,
  retainPairs,
  takeHolds,
  type SyncStatus,
} from './syncstatus.js';
import { restartDelay, wantedWatchers, watchArgs, type WatchPrefs } from './sync-policy.js';

export type { SyncActivity, SyncStatus, LiveState, PairHealth, LocalNote } from './syncstatus.js';

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
  /** How many items the pair holds. */
  held?: number;
}

/**
 * The SAFETY-NET interval. Since 0.43 this is not the speed: the engine syncs
 * a change the moment the server announces it (or the local file system
 * reports it), and at this interval it only CHECKS — the server's change log
 * for what the stream may have missed, the local tree for what the file-system
 * watcher may have missed, a failed folder that is due a retry — and walks a
 * pair only when one of those says so, or its --full-every safety net is due.
 * Measured before the live path: a browser save took 6–25 s to reach the
 * synced file (median 15 s), because it waited for this lap.
 */
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
  /** Look at the accounts again (the caller's reconcile): how an engine that
   *  stopped on its own is started again, after restartDelay. */
  restart?: () => void;
}

/** A run longer than this was healthy: a crash after it starts the backoff
 *  over rather than counting on (restartDelay). */
const HEALTHY_RUN_MS = 2 * 60_000;

/**
 * Keeps one `filex sync run --watch` process alive per signed-in account.
 *
 * ⚠ One process per account, never one for all of them: a token authenticates
 * against exactly one server, so a single process would try to sync account B's
 * folders with account A's credentials.
 */
export class SyncSupervisor {
  private procs = new Map<string, ReturnType<typeof spawn>>();
  private status = new Map<string, SyncStatus>();
  private stopping = false;
  private readonly onChange: () => void;
  private readonly onSignedOut: (accountId: string) => void;
  private readonly onHold: (accountId: string, pairId: string, count: number) => void;
  private readonly watchPrefs: () => WatchPrefs;
  private readonly restart: () => void;
  /** Per account: crashes in a row, and the pending restart. */
  private crashes = new Map<string, number>();
  private restartTimers = new Map<string, ReturnType<typeof setTimeout>>();

  constructor(hooks: SupervisorHooks) {
    this.onChange = hooks.onChange;
    this.onSignedOut = hooks.onSignedOut ?? (() => {});
    this.onHold = hooks.onHold ?? (() => {});
    this.watchPrefs = hooks.watchPrefs ?? (() => ({}));
    this.restart = hooks.restart ?? (() => {});
  }

  private cancelRestart(accountId: string): void {
    const t = this.restartTimers.get(accountId);
    if (t) clearTimeout(t);
    this.restartTimers.delete(accountId);
  }

  statuses(): SyncStatus[] {
    return [...this.status.values()];
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
    for (const id of [...this.status.keys()]) {
      if (!wanted.has(id)) this.status.delete(id);
    }
    // A running watcher is not restarted for an unpaired folder (it re-reads
    // pairs.json between passes), so the folder's last state goes here.
    for (const [id, st] of this.status) {
      retainPairs(st, new Set(pairs.filter((p) => p.account === id).map((p) => p.id)));
    }
    for (const acc of accounts) {
      if (wanted.has(acc.id) && !this.procs.has(acc.id)) {
        this.cancelRestart(acc.id);
        this.start(acc, tokenFor(acc.id));
      }
    }
    for (const id of [...this.restartTimers.keys()]) {
      if (!wanted.has(id)) this.cancelRestart(id);
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
    const st: SyncStatus = newStatus(acc.id);
    this.status.set(acc.id, st);
    this.procs.set(acc.id, proc);
    const startedAt = Date.now();

    // The server refused the token. Said once per watcher; an older engine
    // that keeps looping on the 401 instead of exiting is stopped here.
    let refused = false;
    const signedOut = () => {
      if (refused || !refusalApplies(this.status.get(acc.id), st)) return;
      refused = true;
      if (this.procs.get(acc.id) === proc) {
        proc.kill();
        this.procs.delete(acc.id);
      }
      st.running = false;
      st.active = null;
      this.onSignedOut(acc.id);
    };
    const holds = () => {
      for (const h of takeHolds(st)) this.onHold(acc.id, h.pairId, h.count);
    };

    // The engine's lines are turned into typed state HERE (syncstatus.ts), so
    // the string formats live in one place and the UI gets data. One reader
    // per pipe, fed the raw bytes: a read can end mid-line — or inside a
    // multi-byte character — and the tail waits for the rest.
    const out = new LineReader((line) => absorbLine(st, line, false));
    const err = new LineReader((line) => absorbLine(st, line, true));
    proc.stdout?.on('data', (c: Buffer) => {
      out.push(c);
      holds();
      this.onChange();
    });
    proc.stderr?.on('data', (c: Buffer) => {
      err.push(c);
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
      out.flush();
      err.flush();
      markExited(st, code, this.stopping, signal);
      holds();
      signedOut();
      // ⚠ An engine that stopped on its own is started again, less often the
      // more it keeps stopping (restartDelay). It used to stay stopped until
      // something else — a folder added, a setting changed — made the app
      // look at its accounts again, with one English line to show for it.
      if (!this.stopping && !refused && st.exited) {
        const n = Date.now() - startedAt > HEALTHY_RUN_MS ? 1 : (this.crashes.get(acc.id) ?? 0) + 1;
        this.crashes.set(acc.id, n);
        const delay = restartDelay(n);
        st.restartAt = Date.now() + delay;
        this.cancelRestart(acc.id);
        this.restartTimers.set(acc.id, setTimeout(() => {
          this.restartTimers.delete(acc.id);
          this.restart();
        }, delay));
      } else if (!st.exited) {
        this.crashes.delete(acc.id);
      }
      this.onChange();
    });
  }

  /** Kills ONE account's watcher. Root migration renames mirrors on disk;
   *  a watcher mid-round holds the old paths in memory and would read the
   *  half-moved tree as a mass local delete. reconcile() restarts it. */
  stop(accountId: string): void {
    this.cancelRestart(accountId);
    const proc = this.procs.get(accountId);
    if (proc) {
      proc.kill();
      this.procs.delete(accountId);
    }
    const st = this.status.get(accountId);
    if (st) {
      st.running = false;
      st.active = null;
    }
    this.onChange();
  }

  stopAll(): void {
    this.stopping = true;
    for (const id of [...this.restartTimers.keys()]) this.cancelRestart(id);
    for (const p of this.procs.values()) p.kill();
    this.procs.clear();
  }
}
