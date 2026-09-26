/**
 * Is this response the file?
 *
 * ⚠ The question every download in this app has to ask, because "2xx" is not
 * the answer. Servers from v0.20 to v0.42 answered an unranged download of a
 * big file on a slow storage with `202 {"state":"preparing",…}` — and a caller
 * that took any 2xx for the file wrote that JSON to disk under the file's name.
 * The sync engine then uploaded it over the real file (45 files lost on one
 * deployment). The same check guarded drag-out and "open with" here.
 *
 * Every download therefore asks for `Range: bytes=0-` — the one request no
 * server version answers with 202 — and accepts only a 200 (the whole object;
 * a server that ignores the Range) or a 206 that covers the whole object.
 *
 * Pure on purpose: no electron import, so node:test can drive it.
 */

/** The request header value every download sends. */
export const WHOLE_FILE_RANGE = 'bytes=0-';

export type DownloadVerdict = { ok: true } | { ok: false; reason: string };

/**
 * Decides whether a response to a `bytes=0-` download carries the whole file.
 * contentRange is the response's `Content-Range` header, if any.
 */
export function wholeFileVerdict(status: number, contentRange?: string | string[]): DownloadVerdict {
  if (status === 200) return { ok: true };
  if (status === 206) {
    const cr = Array.isArray(contentRange) ? contentRange[0] : contentRange;
    const m = /^bytes (\d+)-(\d+)\/(\d+)$/.exec((cr ?? '').trim());
    if (m && Number(m[1]) === 0 && Number(m[3]) > 0 && Number(m[2]) === Number(m[3]) - 1) {
      return { ok: true };
    }
    return { ok: false, reason: `the server sent part of the file (${cr ?? 'no Content-Range'}), not all of it` };
  }
  if (status === 202) {
    return { ok: false, reason: 'the server is still preparing this file; try again in a moment' };
  }
  return { ok: false, reason: `server said ${status}` };
}

// ── a download to disk, said while it runs and when it ends ────────────────
//
// ⚠ The explorer's Download and ⌘K save through the window's own download,
// and nothing listened to it: no progress, no end, and a failure said nothing
// at all. main.ts listens (will-download); the rules are here.

/**
 * Every download of the window added up, for the dock / taskbar bar: the
 * received bytes of all of them over their total. -1 when none runs (no
 * bar); 2 when one does not know its size (Electron draws a value above 1 as
 * an indeterminate bar, which is honest where 0% would read as stuck).
 */
export class DownloadTally {
  private readonly items = new Map<string, { received: number; total: number }>();

  update(id: string, received: number, total: number): void {
    this.items.set(id, { received: Math.max(0, received), total: Math.max(0, total) });
  }

  finish(id: string): void {
    this.items.delete(id);
  }

  fraction(): number {
    if (this.items.size === 0) return -1;
    let received = 0;
    let total = 0;
    for (const it of this.items.values()) {
      if (it.total <= 0) return 2;
      received += it.received;
      total += it.total;
    }
    return Math.min(1, received / total);
  }
}

/** What a download's end is worth saying: done, failed, or nothing (the person
 *  cancelled it themselves). */
export function downloadEnding(state: 'completed' | 'cancelled' | 'interrupted'): 'done' | 'failed' | null {
  if (state === 'completed') return 'done';
  if (state === 'interrupted') return 'failed';
  return null;
}
