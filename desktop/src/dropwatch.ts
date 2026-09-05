// Where did the user drop it?
//
// The OS copies a dragged file at the moment of the drop, from a real path.
// That is why a big file used to have to be downloaded BEFORE the drag could
// start. The way round it is the one every "drag out of an archive" program
// ends up at: hand the shell a **placeholder** — an empty file with the right
// name, which copies in microseconds — then find out where that copy landed and
// put the real bytes there instead.
//
// Finding it is this module's whole job, and the hard part is not the watching.
// It is WHERE the watching runs:
//
// ⚠⚠ `webContents.startDrag()` hands control to the operating system's own drag
// loop, and on Windows that loop is modal — it does not return until the user
// lets go. The main process's JavaScript is therefore NOT RUNNING for the whole
// gesture, including the moment of the drop. And a recursive `fs.watch` whose
// loop is blocked does not merely deliver late: it MISSES the change outright.
// Measured 2026-08-29 — a file created during a 4-second block was never
// reported, before or after:
//
//	block ended (4065 ms), seen up to that point: null
//	seen after the block: NOTHING
//
// So the watchers live in a WORKER THREAD, whose event loop keeps running while
// the main thread is inside the drag loop. The same measurement with a worker
// reported the file the moment the main thread was free again. This is the
// difference between "the folder fills in" and Burak's "I drag a folder onto the
// desktop and its insides still come over empty" (translated from Turkish).
//
// ⚠ What this still cannot do: if the drop target is an APPLICATION rather than
// a folder, nothing is written to disk, so there is nothing to find. The caller
// keeps the prepared-copy path for small selections for exactly that reason.
//
// ⚠ `recursive: true` is supported on Windows and macOS. On Linux it is not, so
// callers there stay on prepared copies.

import fs from 'node:fs';
import path from 'node:path';
import { Worker } from 'node:worker_threads';

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
 * The worker's whole program, as source.
 *
 * Inlined and run with `eval: true` rather than shipped as a second file: the
 * main process is bundled to one `dist/main.js`, and a worker kept in its own
 * file would have to be built, copied into the package and found again at
 * runtime — three more things to get wrong at packaging time for twenty lines
 * of code.
 *
 * The guard lives here, with the watching: what we handed the shell is known
 * exactly — an EMPTY file (or an empty directory), created inside this drag's
 * window — so anything with content of its own belongs to somebody else and is
 * ignored. Without that, a file that merely shares the name (a backup job,
 * another download) would be treated as the drop and the user's file written
 * into that folder.
 */
const WORKER_SOURCE = `
const fs = require('node:fs');
const path = require('node:path');
const { parentPort, workerData } = require('node:worker_threads');

const started = workerData.started;
const names = new Set(workerData.names.map((n) => n.toLowerCase()));
const ignore = workerData.ignoreDirs.map((d) => path.resolve(d).toLowerCase());
const watchers = [];
let done = false;

function finish(loc) {
  if (done) return;
  done = true;
  for (const w of watchers) { try { w.close(); } catch (e) {} }
  parentPort.postMessage(loc);
}

for (const root of workerData.roots) {
  try {
    const w = fs.watch(root, { recursive: true }, (_event, file) => {
      if (!file || done) return;
      const rel = String(file);
      if (!names.has(path.basename(rel).toLowerCase())) return;
      const full = path.join(root, rel);
      const lower = path.resolve(full).toLowerCase();
      if (ignore.some((d) => lower.startsWith(d))) return;
      let st;
      try { st = fs.statSync(full); } catch (e) { return; }
      if (st.isDirectory()) {
        try { if (fs.readdirSync(full).length !== 0) return; } catch (e) { return; }
      } else if (st.size !== 0) {
        return;
      }
      if (st.birthtimeMs && st.birthtimeMs + 1000 < started) return;
      finish({ dir: path.dirname(full), droppedPath: full, name: path.basename(full), ms: Date.now() - started });
    });
    w.on('error', () => { try { w.close(); } catch (e) {} });
    watchers.push(w);
  } catch (e) { /* a drive that refuses a recursive watch is skipped */ }
}

// ⚠ Readiness is ANNOUNCED, not assumed. Spawning a worker and arming five
// recursive volume watchers takes a moment; a drag that starts before that is
// a drag whose drop nobody is listening for. The main thread waits for this
// line before it hands control to the OS.
parentPort.postMessage({ ready: true, watching: watchers.length });
if (watchers.length === 0) finish(null);
parentPort.on('message', (m) => { if (m === 'cancel') finish(null); });
`;

/**
 * Watches for one of `names` to appear anywhere on the local drives.
 *
 * Resolves with the location, or `null` on timeout/cancel. The worker is always
 * terminated — a recursive volume watcher left running is a stream of every
 * file event on the machine.
 */
export function watchForDrop(opts: WatchOptions): {
  promise: Promise<DropLocation | null>;
  /** Resolves once the worker's watchers are actually up. Await it before
   *  starting the OS drag: a drop that happens first is a drop nobody sees. */
  ready: Promise<void>;
  cancel: () => void;
} {
  const started = Date.now();
  const roots = opts.roots ?? localDriveRoots();
  let settled = false;
  let worker: Worker | null = null;
  let timer: NodeJS.Timeout | undefined;
  let finish: (v: DropLocation | null) => void = () => {};

  let markReady: () => void = () => {};
  const ready = new Promise<void>((resolve) => {
    markReady = resolve;
  });

  const promise = new Promise<DropLocation | null>((resolve) => {
    finish = (v) => {
      if (settled) return;
      settled = true;
      markReady(); // nobody may be left waiting on a watch that is over
      if (timer) clearTimeout(timer);
      void worker?.terminate().catch(() => undefined);
      worker = null;
      resolve(v);
    };

    try {
      worker = new Worker(WORKER_SOURCE, {
        eval: true,
        workerData: { names: opts.names, ignoreDirs: opts.ignoreDirs, roots, started },
      });
    } catch {
      markReady();
      finish(null);
      return;
    }
    worker.on('message', (msg: DropLocation | { ready: true } | null) => {
      if (msg && typeof msg === 'object' && 'ready' in msg) {
        markReady();
        return;
      }
      finish(msg as DropLocation | null);
    });
    worker.on('error', () => {
      markReady();
      finish(null);
    });
    // ⚠ `unref` so a watch still running cannot hold the app open at quit; the
    // drag is over long before anybody closes the window.
    worker.unref();
    timer = setTimeout(() => finish(null), opts.timeoutMs ?? 30_000);
  });

  return {
    promise,
    ready,
    cancel: () => {
      try {
        worker?.postMessage('cancel');
      } catch {
        /* already gone */
      }
      finish(null);
    },
  };
}
