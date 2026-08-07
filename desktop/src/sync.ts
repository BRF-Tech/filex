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
// own — ~/.filex/sync/pairs.json is the single source of truth.

import { spawn, execFile } from 'node:child_process';
import { existsSync } from 'node:fs';
import path from 'node:path';
import { app } from 'electron';
import type { Account } from './accounts.js';

export interface Pair {
  id: string;
  local: string;
  remote: string;
  account?: string;
  paused?: boolean;
}

/** What the supervisor has observed about one account's sync process. */
export interface SyncStatus {
  accountId: string;
  running: boolean;
  lastLine: string;
  lastRunAt: string | null;
  lastError: string | null;
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
      { env: { ...process.env, ...env }, windowsHide: true, maxBuffer: 8 * 1024 * 1024 },
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

export async function addPair(local: string, remote: string, accountId: string): Promise<void> {
  await run(['sync', 'add', local, remote, '--account', accountId]);
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

  constructor(private onChange: () => void) {}

  statuses(): SyncStatus[] {
    return [...this.status.values()];
  }

  /** Starts watchers for accounts that have pairs, stops the rest. Safe to call
   *  whenever pairs or accounts change. */
  async reconcile(accounts: Account[], tokenFor: (id: string) => string | null): Promise<void> {
    if (this.stopping) return;
    const pairs = await listPairs();
    const wanted = new Set(
      accounts
        .filter((a) => pairs.some((p) => p.account === a.id && !p.paused))
        .map((a) => a.id),
    );

    for (const [id, proc] of this.procs) {
      if (!wanted.has(id)) {
        proc.kill();
        this.procs.delete(id);
        this.status.delete(id);
      }
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
      ['sync', 'run', '--account', acc.id, '--watch', WATCH_INTERVAL, '--quiet'],
      {
        env: { ...process.env, FILEX_URL: acc.serverUrl, FILEX_TOKEN: token },
        windowsHide: true,
        stdio: ['ignore', 'pipe', 'pipe'],
      },
    );
    const st: SyncStatus = {
      accountId: acc.id,
      running: true,
      lastLine: 'starting…',
      lastRunAt: null,
      lastError: null,
    };
    this.status.set(acc.id, st);
    this.procs.set(acc.id, proc);

    const absorb = (chunk: Buffer, isErr: boolean) => {
      for (const line of chunk.toString().split('\n')) {
        const t = line.trim();
        if (!t) continue;
        if (isErr) st.lastError = t;
        else {
          st.lastLine = t;
          st.lastRunAt = new Date().toISOString();
        }
      }
      this.onChange();
    };
    proc.stdout?.on('data', (c: Buffer) => absorb(c, false));
    proc.stderr?.on('data', (c: Buffer) => absorb(c, true));

    proc.on('exit', (code) => {
      this.procs.delete(acc.id);
      st.running = false;
      // A watcher is meant to run forever. Exiting means the server went away,
      // the token expired, or the binary crashed — say so instead of leaving a
      // panel that claims everything is fine.
      if (!this.stopping && code !== 0) {
        st.lastError = st.lastError ?? `sync stopped unexpectedly (exit ${code})`;
      }
      this.onChange();
    });
  }

  stopAll(): void {
    this.stopping = true;
    for (const p of this.procs.values()) p.kill();
    this.procs.clear();
  }
}
