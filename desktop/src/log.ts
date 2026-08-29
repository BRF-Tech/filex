// A log file, because a packaged app has no console.
//
// `console.log` in the main process goes to a terminal — and a packaged
// Windows app has none, so everything printed there is simply lost. That is
// how a failed drag-out came to have no evidence at all except "the folder is
// empty" (2026-08-29): the trail existed, and nobody could read it.
//
// Everything worth reading later goes through here: one line per step, to
// `<userData>/logs/filex-desktop.log`, and to stdout as well for a dev run.

import { app } from 'electron';
import fs from 'node:fs';
import path from 'node:path';

/** Rotate at this size, keeping one previous file. Two megabytes is thousands
 *  of lines — long enough to hold a whole session, small enough to attach to a
 *  message. */
const MAX_BYTES = 2 * 1024 * 1024;

let file: string | null = null;

function logFile(): string | null {
  if (file) return file;
  try {
    const dir = path.join(app.getPath('userData'), 'logs');
    fs.mkdirSync(dir, { recursive: true });
    file = path.join(dir, 'filex-desktop.log');
    return file;
  } catch {
    // A log that cannot be written must never take the app down with it.
    return null;
  }
}

/** Where the log lives, for the UI to show and for a bug report to name. */
export function logPath(): string {
  return logFile() ?? '';
}

function rotate(p: string): void {
  try {
    const st = fs.statSync(p);
    if (st.size < MAX_BYTES) return;
    fs.rmSync(`${p}.1`, { force: true });
    fs.renameSync(p, `${p}.1`);
  } catch {
    /* nothing to rotate */
  }
}

/**
 * One line: `2026-08-29T01:07:57.204Z [drag] watch result {…}`.
 *
 * `tag` groups the lines (`drag`, `xfer`, `sync`…), `step` says what happened,
 * and `detail` is whatever is worth having when reading it back — it is
 * JSON-stringified, so it must not contain a credential.
 */
export function log(tag: string, step: string, detail?: unknown): void {
  const line = `${new Date().toISOString()} [${tag}] ${step}${
    detail === undefined ? '' : ' ' + safeJson(detail)
  }\n`;
  process.stdout.write(line);
  const p = logFile();
  if (!p) return;
  try {
    rotate(p);
    fs.appendFileSync(p, line);
  } catch {
    /* the log is a courtesy; never let it throw into the caller */
  }
}

function safeJson(v: unknown): string {
  try {
    return JSON.stringify(v);
  } catch {
    return String(v);
  }
}
