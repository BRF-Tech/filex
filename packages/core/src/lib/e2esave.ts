/**
 * e2esave — where a file decrypted in this tab is saved to.
 *
 * A file decrypted in the browser has no URL the browser can download, so the
 * bytes have to be handed to the person some other way. Two sinks, best
 * first (docs/E2E-ENCRYPTION.md → "Where a decrypted download goes"):
 *
 *   'fsa'   the File System Access API (`showSaveFilePicker`): Chrome, Edge,
 *           Opera and the filex desktop app. The person picks where it goes,
 *           and the plaintext is written to disk AS IT IS DECRYPTED — nothing
 *           is held in memory, any size works. The browser writes into a
 *           temporary file and moves it into place only when the stream ends
 *           cleanly; a decryption error aborts it and nothing is left behind.
 *   'blob'  everywhere else (Firefox, Safari): the plaintext is gathered into
 *           a Blob and handed to the normal download. That holds the whole
 *           file in memory, so it is limited (E2E_BLOB_SAVE_LIMIT) and the
 *           limit is SAID, with where to go instead.
 *
 * A streaming service-worker download (the third option some web apps use)
 * is not offered: the web app's worker is scoped to `/admin/` and the
 * explorer runs at `/drive/`, in the desktop app and inside other sites'
 * pages, where there is no worker of ours to answer the request.
 */

/** The most a Blob-sink save holds in memory. */
export const E2E_BLOB_SAVE_LIMIT = 1024 * 1024 * 1024;

export type SaveSinkKind = 'fsa' | 'blob';

/** The sink the last save used — read by the end-to-end tests. */
export let lastSaveSink: SaveSinkKind | null = null;

interface FsWritable {
  write(data: Uint8Array): Promise<void>;
  close(): Promise<void>;
  abort(reason?: unknown): Promise<void>;
}
interface FsHandle {
  createWritable(): Promise<FsWritable>;
  /** Chromium: removes the (empty) file the picker created. */
  remove?(): Promise<void>;
}
type ShowSaveFilePicker = (opts?: { suggestedName?: string }) => Promise<FsHandle>;

function picker(): ShowSaveFilePicker | null {
  if (typeof window === 'undefined') return null;
  const w = window as unknown as { showSaveFilePicker?: ShowSaveFilePicker; isSecureContext?: boolean };
  return typeof w.showSaveFilePicker === 'function' && w.isSecureContext !== false ? w.showSaveFilePicker.bind(window) : null;
}

/** True when this browser saves decrypted files as a stream, any size. */
export function streamingSaveAvailable(): boolean {
  return picker() !== null;
}

/** The person closed the save dialog. Not an error to report. */
export class SaveCancelled extends Error {
  constructor() {
    super('save cancelled');
    this.name = 'SaveCancelled';
  }
}

/** The browser wants a fresh click before it shows the save dialog. */
export class SaveNeedsGesture extends Error {
  constructor() {
    super('the save dialog needs a click');
    this.name = 'SaveNeedsGesture';
  }
}

/** Too big for this browser's in-memory save. */
export class SaveTooLarge extends Error {
  constructor(
    public size: number,
    public limit: number,
  ) {
    super(`e2e: ${size} bytes is more than this browser can save from memory (${limit})`);
    this.name = 'SaveTooLarge';
  }
}

export interface SaveTarget {
  kind: SaveSinkKind;
  name: string;
  /** Write the stream to the target; rejects (and discards) on any error. */
  write(stream: ReadableStream<Uint8Array>, opts?: { mime?: string }): Promise<void>;
  /** Give up before writing: the chosen file is not created. */
  discard(): Promise<void>;
}

/**
 * Pick where a decrypted file goes. Call it FIRST, in the click that asked
 * for the download: the browser only shows its save dialog from a click, and
 * the work can start once there is somewhere for it to go.
 *
 * `size` (when known) lets the Blob sink refuse before any byte is decrypted.
 */
export async function pickSaveTarget(name: string, size?: number): Promise<SaveTarget> {
  const show = picker();
  if (show) {
    let handle: FsHandle;
    try {
      handle = await show({ suggestedName: name });
    } catch (err) {
      const n = (err as Error)?.name;
      if (n === 'AbortError') throw new SaveCancelled();
      if (n === 'SecurityError' || n === 'NotAllowedError') throw new SaveNeedsGesture();
      throw err;
    }
    return fsaTarget(name, handle);
  }
  if (size !== undefined && size > E2E_BLOB_SAVE_LIMIT) throw new SaveTooLarge(size, E2E_BLOB_SAVE_LIMIT);
  return blobTarget(name);
}

function fsaTarget(name: string, handle: FsHandle): SaveTarget {
  let writable: FsWritable | null = null;
  return {
    kind: 'fsa',
    name,
    async write(stream) {
      lastSaveSink = 'fsa';
      writable = await handle.createWritable();
      const w = writable;
      const reader = stream.getReader();
      try {
        for (;;) {
          const { done, value } = await reader.read();
          if (done) break;
          await w.write(value);
        }
        await w.close();
      } catch (err) {
        // Aborting throws the browser's temporary file away: nothing half
        // decrypted is ever given the name the person chose.
        await w.abort(err).catch(() => undefined);
        await reader.cancel(err).catch(() => undefined);
        // Chromium creates the chosen file, empty, when the dialog closes;
        // an aborted write leaves that behind unless it is removed.
        await handle.remove?.().catch(() => undefined);
        throw err;
      }
    },
    async discard() {
      if (writable) await writable.abort().catch(() => undefined);
      await handle.remove?.().catch(() => undefined);
    },
  };
}

function blobTarget(name: string): SaveTarget {
  return {
    kind: 'blob',
    name,
    async write(stream, opts) {
      lastSaveSink = 'blob';
      const parts: Blob[] = [];
      let total = 0;
      const reader = stream.getReader();
      try {
        for (;;) {
          const { done, value } = await reader.read();
          if (done) break;
          total += value.length;
          if (total > E2E_BLOB_SAVE_LIMIT) throw new SaveTooLarge(total, E2E_BLOB_SAVE_LIMIT);
          // A Blob per piece: the browser may keep it outside the JS heap,
          // and the piece itself can be collected.
          parts.push(new Blob([value as Uint8Array<ArrayBuffer>]));
        }
      } catch (err) {
        await reader.cancel(err).catch(() => undefined);
        throw err;
      }
      // Only a stream that ended cleanly becomes a download.
      const blob = new Blob(parts, { type: opts?.mime || 'application/octet-stream' });
      const url = URL.createObjectURL(blob);
      const a = document.createElement('a');
      a.href = url;
      a.download = name;
      a.rel = 'noopener';
      document.body.appendChild(a);
      a.click();
      a.remove();
      setTimeout(() => URL.revokeObjectURL(url), 60_000);
    },
    async discard() {
      /* nothing was created */
    },
  };
}
