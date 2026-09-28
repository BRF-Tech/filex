/**
 * uploadSource — bytes for a staged upload that are MADE as they are sent.
 *
 * A file encrypted in the browser (a single encrypted file, a large file in an
 * encrypted folder, a password change that re-sends a body) is a stream: its
 * size is known up front, its bytes are not, and building it as one Blob first
 * would hold the whole thing in memory. `useUploadChunked` takes a source
 * instead of a File for those, and asks it for one chunk at a time.
 *
 * A source is read FORWARD. The staged protocol may ask for the chunk it last
 * got again (a retry after a lost answer) or restart inside it (the server's
 * offset moved), so the last range handed out is kept; asking for anything
 * before it is an error — a stream cannot rewind. That is also why a sourced
 * upload is not bookmarked for a resume after a reload: the bytes it would
 * resume are gone with the tab (and, for an encrypted file, with its key).
 */

import { ByteStreamReader } from './e2estream';

export interface UploadSource {
  /** Bytes the upload carries. */
  size: number;
  /** Bytes [start, end). Starts never go below the last range returned. */
  read(start: number, end: number): Promise<Blob>;
  /** Stop producing (the upload failed or was cancelled). */
  cancel?(): void;
}

/**
 * A source over a stream of exactly `size` bytes. The stream is not touched
 * until the first read: an upload refused before any byte was asked for (a
 * server with no staged path) leaves it whole, for the caller to send some
 * other way.
 */
export function streamUploadSource(stream: ReadableStream<Uint8Array>, size: number): UploadSource {
  let reader: ByteStreamReader | null = null;
  const open = () => (reader ??= new ByteStreamReader(stream));
  /** The retained window: bytes [winStart, winStart + win.length). */
  let winStart = 0;
  let win: Uint8Array<ArrayBuffer> = new Uint8Array(0);
  let pos = 0;
  let cancelled = false;
  return {
    size,
    async read(start, end) {
      if (cancelled) throw new Error('upload source cancelled');
      if (start < winStart || start > pos || end < start || end > size) {
        throw new Error(`upload source: cannot read [${start}, ${end}) after [${winStart}, ${pos})`);
      }
      // Whatever lies before `start` will not be asked for again.
      const keep = win.subarray(start - winStart);
      if (end > pos) {
        const more = await open().read(end - pos);
        if (more.length < end - pos) throw new Error('upload source: the stream ended early');
        const next = new Uint8Array(keep.length + more.length);
        next.set(keep, 0);
        next.set(more, keep.length);
        win = next;
        pos = end;
      } else {
        win = keep.slice();
      }
      winStart = start;
      return new Blob([win.subarray(0, end - start)]);
    },
    cancel() {
      if (cancelled) return;
      cancelled = true;
      void (reader ? reader.cancel() : stream.cancel()).catch(() => undefined);
    },
  };
}

/** Gather a SMALL stream into a File, for the single-POST upload path. */
export async function streamToFile(
  stream: ReadableStream<Uint8Array>,
  name: string,
  type = 'application/octet-stream',
): Promise<File> {
  const parts: Blob[] = [];
  const reader = stream.getReader();
  for (;;) {
    const { done, value } = await reader.read();
    if (done) break;
    parts.push(new Blob([value as Uint8Array<ArrayBuffer>]));
  }
  return new File(parts, name, { type });
}
