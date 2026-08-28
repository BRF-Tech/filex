// Where did the user drop it?
//
// The OS copies a dragged file at the moment of the drop, from a real path.
// That is why a big file used to have to be downloaded BEFORE the drag could
// start. The way round it is the one every "drag out of an archive" program
// ends up at: hand the shell a **placeholder** — an empty file with the right
// name, which copies in microseconds — then find out where that copy landed and
// put the real bytes there instead.
//
// Finding it is this module's whole job. `fs.watch(root, {recursive: true})` on
// Windows is `ReadDirectoryChangesW` over a volume: a new file anywhere on the
// drive arrives as an event within milliseconds (measured 2026-08-29: ~6 ms
// from write to callback across a whole C:). We watch every local drive for a
// short window, filter by the exact placeholder names, and the first hit tells
// us the folder.
//
// ⚠⚠ What this cannot do, stated plainly rather than discovered later: if the
// drop target is an APPLICATION rather than a folder — a chat window, an editor
// — nothing is written to disk, the app receives the empty placeholder, and no
// amount of watching will find anything. That is why the caller keeps the
// prepared-copy path for small selections: those are the ones people drop into
// programs. A large file dropped somewhere we cannot see is reported to the
// user, never silently forgotten.
//
// ⚠ `recursive: true` is supported on Windows and macOS. On Linux it is not
// (Node falls back to non-recursive, which would only see the drive root), so
// callers there stay on prepared copies.

import fs from 'node:fs';
import path from 'node:path';

export interface DropLocation {
  /** The folder the placeholder landed in. */
  dir: string;
  /** The placeholder file the shell created there (already the real name). */
  droppedPath: string;
  /** Which of the watched names it was. */
  name: string;
  /** How long the drop took to appear, for the log. */
  ms: number;
}

/** Local drive roots worth watching: `C:\`, `D:\`, … Cheap — no process spawn. */
export function localDriveRoots(): string[] {
  if (process.platform !== 'win32') return [path.parse(process.cwd()).root];
  const out: string[] = [];
  for (let c = 'A'.charCodeAt(0); c <= 'Z'.charCodeAt(0); c++) {
    const root = `${String.fromCharCode(c)}:\\`;
    try {
      if (fs.existsSync(root)) out.push(root);
    } catch {
      /* an unreadable drive letter is not a drop target */
    }
  }
  return out;
}

export interface WatchOptions {
  /** Basenames to look for — the placeholders handed to the OS drag. */
  names: string[];
  /** Our own cache: a hit inside it is the placeholder itself, not a drop. */
  ignoreDirs: string[];
  /** Give up after this long. */
  timeoutMs?: number;
  /** Roots to watch; defaults to every local drive. */
  roots?: string[];
}

/**
 * Watches for one of `names` to appear anywhere on the local drives.
 *
 * Resolves with the location, or `null` on timeout/cancel. Always tears its
 * watchers down — a recursive volume watcher left running is a stream of every
 * file event on the machine.
 */
export function watchForDrop(opts: WatchOptions): { promise: Promise<DropLocation | null>; cancel: () => void } {
  const started = Date.now();
  const names = new Set(opts.names.map((n) => n.toLowerCase()));
  const ignore = opts.ignoreDirs.map((d) => path.resolve(d).toLowerCase());
  const roots = opts.roots ?? localDriveRoots();
  const watchers: fs.FSWatcher[] = [];
  let settled = false;
  let timer: NodeJS.Timeout | undefined;
  let finish: (v: DropLocation | null) => void = () => {};

  const stop = () => {
    if (timer) clearTimeout(timer);
    for (const w of watchers) {
      try {
        w.close();
      } catch {
        /* already closed */
      }
    }
    watchers.length = 0;
  };

  const promise = new Promise<DropLocation | null>((resolve) => {
    finish = (v) => {
      if (settled) return;
      settled = true;
      stop();
      resolve(v);
    };

    for (const root of roots) {
      try {
        const w = fs.watch(root, { recursive: true }, (_event, file) => {
          if (!file) return;
          const rel = String(file);
          if (!names.has(path.basename(rel).toLowerCase())) return;
          const full = path.join(root, rel);
          const lower = path.resolve(full).toLowerCase();
          if (ignore.some((d) => lower.startsWith(d))) return; // our own copy
          // ⚠⚠ The name alone is not proof. A file called `rapor.txt` can
          // appear anywhere on the machine while we are watching — a backup
          // job, another download — and writing the user's file into THAT
          // folder would be worse than not filling the drop in at all. What we
          // dropped is known precisely: an EMPTY file (or an empty directory),
          // created inside this drag's window. Anything else is somebody
          // else's file and is ignored.
          let st: fs.Stats;
          try {
            st = fs.statSync(full);
          } catch {
            // The event can arrive before the copy is closed, or after it has
            // moved on again; either way there is nothing to act on.
            return;
          }
          if (st.isDirectory()) {
            try {
              if (fs.readdirSync(full).length !== 0) return;
            } catch {
              return;
            }
          } else if (st.size !== 0) {
            return;
          }
          if (st.birthtimeMs && st.birthtimeMs + 1000 < started) return;
          finish({ dir: path.dirname(full), droppedPath: full, name: path.basename(full), ms: Date.now() - started });
        });
        // A drive that refuses a recursive watch (network share, permissions)
        // is skipped, not fatal: the others still cover the common targets.
        w.on('error', () => {
          try {
            w.close();
          } catch {
            /* nothing to close */
          }
        });
        watchers.push(w);
      } catch {
        /* same reasoning as the error handler */
      }
    }

    if (watchers.length === 0) {
      finish(null);
      return;
    }
    timer = setTimeout(() => finish(null), opts.timeoutMs ?? 30_000);
  });

  return { promise, cancel: () => finish(null) };
}
