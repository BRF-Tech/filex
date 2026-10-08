/**
 * useUploadChunked — chunked, resumable uploads for every browser surface.
 *
 * It speaks the STAGED protocol (docs/UPLOADS.md), which works on every storage
 * driver:
 *
 *   POST   /api/files/upload/begin      {path,name,size,mime?,hash?,chunk_size?}
 *                                       → {id, chunk_size, offset, total_size}
 *   PUT    /api/files/upload/{id}       Content-Range: bytes A-B/total + body
 *                                       → {offset, received, total_size, state}
 *   GET    /api/files/upload/{id}       → {offset, state, complete, …}
 *   POST   /api/files/upload/{id}/commit→ 202 {op_id, node_id}
 *   DELETE /api/files/upload/{id}       → abort + delete staging
 *
 * It used to speak the S3-presigned one (`/upload/{init,finalize,abort}`), which
 * needed the S3 driver — every other backend answered 501 — and on which filex
 * never saw the bytes at all. That path still exists on the server and is
 * untouched; nothing in the client calls it any more.
 *
 * Three properties are the point of the rewrite, and each is a test:
 *
 *  - **The offset comes from the server.** After any failure the client asks
 *    GET /upload/{id} rather than trusting its own counter. A chunk can fail
 *    after its bytes landed (the response was lost); re-sending is merely slow,
 *    assuming success is wrong.
 *  - **Progress is filex's ingest.** `uploadedBytes` counts bytes filex has
 *    acknowledged plus the in-flight chunk — not bytes handed to a socket, and
 *    not the backend's own write, which happens afterwards in the ops worker
 *    and shows as the `transferring` phase.
 *  - **A reload is resumable, not a restart.** The upload id is bookmarked in
 *    localStorage (lib/uploadResume). The browser will not hand a File back
 *    without a fresh gesture, so recovery is "pick the same file and it
 *    continues from where filex stopped", with `job.resumedFrom` set so the UI
 *    can say so rather than silently starting over.
 */

import type { ExplorerConfig } from '../types/ExplorerConfig';
import type { FileApi } from './useFileApi';
import { networkFailure, requestFailure, wordsIn } from '../lib/errorWords';
// ⚠ An upload is an ACTION: it is never folded into the shared "no
// connection" notice, because the person is waiting to be told whether their
// file went (lib/connection).
import { noteRequestFailed, noteRequestSucceeded } from '../lib/connection';
import { resolveLocale } from '../locales/resolve';
import { newId } from '../lib/uid';
/* wiring:e2 stream — bytes made as they are sent (lib/uploadSource). */
import type { UploadSource } from '../lib/uploadSource';
import {
  clearResume,
  defaultResumeStorage,
  listResume,
  loadResume,
  resumeExpiry,
  saveResume,
  uploadFingerprint,
  type ResumeRecord,
  type ResumeStorage,
} from '../lib/uploadResume';

export interface UploadJob {
  id: string; // local uuid
  file: File;
  /** What to call the file on screen when it is not `file.name` — an upload
   *  into an encrypted-names folder carries its ciphertext name in `file`. */
  displayName?: string;
  path: string;
  /** Server-side staged upload id, once `begin` has answered. */
  uploadId?: string;
  totalBytes: number;
  /** Bytes filex has acknowledged (plus the chunk currently in flight). */
  uploadedBytes: number;
  percent: number;
  status:
    | 'pending'
    | 'initializing'
    | 'uploading'
    /** Every byte is in filex; the commit is being accepted. */
    | 'committing'
    /** Committed and listed; the ops worker is writing it to the backend. */
    | 'transferring'
    | 'done'
    | 'error'
    | 'aborted';
  /** Byte offset an interrupted session was picked up from, when it was. */
  resumedFrom?: number;
  /** Background transfer op, once the commit is accepted. */
  opId?: number;
  nodeId?: number;
  error?: string;
  cancel(): void;
}

export interface UploadOptions {
  path: string;
  /** The file — or, with `source`, only its identity (name, type). */
  file: File;
  /**
   * wiring:e2 stream — where the bytes come from when they are MADE as they
   * are sent (a file encrypted in the browser): `source.size` is the upload's
   * size and `file` only names it. Such an upload is not bookmarked for a
   * resume after a reload — its bytes are gone with the tab.
   */
  source?: UploadSource;
  /**
   * wiring:e2 convert - query parameters for the COMMIT, the moment the file
   * is replaced: `expect` (the overwrite precondition "<size>:<ms>") and
   * `e2e_convert` (an in-place E2E conversion write, which keeps no version
   * of the plaintext it replaces). The server checks both there
   * (upload_staged.go → Commit).
   */
  commitQuery?: Record<string, string>;
  chunkSize?: number;
  onProgress?: (job: UploadJob) => void;
  onDone?: (job: UploadJob, result: UploadResult) => void;
  onError?: (job: UploadJob, err: Error) => void;
  /**
   * Wait for the backend transfer before resolving. Defaults to TRUE, because
   * "the server has the bytes" and "the storage has the bytes" are different
   * claims and only the second one is an upload.
   *
   * ⚠ It used to default to false and no caller ever set it, so the job went
   * straight to `done` on the 202 — a transfer that failed afterwards in the
   * ops worker left the user looking at a finished upload for a file that was
   * never stored (issue #16: every file over the chunk threshold, on every S3
   * backend). The `transferring` phase is short for a healthy storage and is
   * the only place a failure can still be shown, so it is the default; pass
   * false to opt out where the wait is genuinely unwanted.
   */
  waitForTransfer?: boolean;
}

/** What `commit` answers, plus the fields a caller wants afterwards. */
export interface UploadResult {
  id: string;
  op_id?: number;
  node_id?: number;
  path?: string;
  transfer_state?: string;
}

interface BeginResponse {
  id: string;
  chunk_size?: number;
  chunkSize?: number;
  offset?: number;
  total_size?: number;
  /** When the server sweeps this upload's staging (RFC 3339) - the life of
   *  the resume bookmark (lib/uploadResume `expiresAt`). */
  expires_at?: string | null;
}

interface StatusResponse {
  id?: string;
  offset?: number;
  received?: number;
  total_size?: number;
  chunk_size?: number;
  chunkSize?: number;
  state?: string;
  complete?: boolean;
  error?: string;
  op_id?: number;
  node_id?: number;
  /** When the server sweeps this staging if nothing more arrives (RFC 3339):
   *  the last write + FILEX_UPLOAD_STAGING_TTL. On a chunk's answer too. */
  expires_at?: string | null;
}

/** Only a session still taking chunks can be continued. */
function resumableState(state: string | undefined): boolean {
  return !state || state === 'staging';
}

/** Error carrying "this server has no staged path" — the ONLY condition under
 *  which a caller may retry the file on the single-POST path. Any other
 *  failure has either sent bytes or been refused on the merits. */
export interface UnsupportedUploadError extends Error {
  stagedUnsupported?: true;
}

function markUnsupported(err: Error): UnsupportedUploadError {
  (err as UnsupportedUploadError).stagedUnsupported = true;
  return err;
}

/** True when the staged protocol is simply not there. */
export function isStagedUnsupported(err: unknown): boolean {
  return !!(err as UnsupportedUploadError)?.stagedUnsupported;
}

export function useUploadChunked(
  config: ExplorerConfig,
  api: FileApi,
  storage: ResumeStorage | null = defaultResumeStorage(),
) {
  /** Client-side default. The server's answer at `begin` is binding — this only
   *  decides what to ask for and what counts as "large enough to chunk". */
  const DEFAULT_CHUNK = config.chunkSize ?? 8 * 1024 * 1024;

  /** Base of the staged routes, derived from the manager endpoint the same way
   *  every other derived route is, so an embedder that passes only `endpoint`
   *  still reaches them. */
  function stagedBase(): string {
    const explicit = api.endpoints.uploadBegin;
    if (explicit) return explicit.replace(/\/begin$/, '');
    return api.endpoints.manager.replace(/\/manager(\?.*)?$/, '/upload');
  }

  /** Is the staged protocol reachable at all? */
  function available(): boolean {
    return !!stagedBase();
  }

  /** Files at or above this go chunked; smaller ones use the single-POST fast
   *  path, which is fine for a 20 KB text file. */
  function threshold(): number {
    return DEFAULT_CHUNK;
  }

  function shouldChunk(file: { size: number }): boolean {
    return available() && file.size > threshold();
  }

  /** The bookmark for this (destination, file), if there is one. Exposed so a
   *  host can tell the user an upload is resumable BEFORE starting it. */
  function resumableFor(path: string, file: File): ResumeRecord | null {
    return loadResume(storage, uploadFingerprint(path, file));
  }

  /** Every unfinished upload this browser remembers. */
  function listResumable(): ResumeRecord[] {
    return listResume(storage);
  }

  /** Forget a bookmark and, best effort, drop the server-side staging with it. */
  async function discardResumable(rec: ResumeRecord): Promise<void> {
    clearResume(storage, uploadFingerprint(rec.path, { name: rec.name, size: rec.size, lastModified: rec.lastModified }));
    try {
      await api.jsonFetch(`${stagedBase()}/${encodeURIComponent(rec.uploadId)}`, { method: 'DELETE' });
    } catch {
      /* already gone, or being transferred — the sweeper handles the rest */
    }
  }

  async function status(uploadId: string): Promise<StatusResponse> {
    return api.jsonFetch<StatusResponse>(`${stagedBase()}/${encodeURIComponent(uploadId)}`);
  }

  async function uploadFile(opts: UploadOptions): Promise<UploadResult> {
    const key = uploadFingerprint(opts.path, opts.file);
    /* wiring:e2 stream */
    const src = opts.source ?? null;
    const total = src ? src.size : opts.file.size;
    const bookmarks = src ? null : storage;

    const job: UploadJob = {
      id: newId(),
      file: opts.file,
      path: opts.path,
      totalBytes: total,
      uploadedBytes: 0,
      percent: 0,
      status: 'initializing',
      cancel: () => {},
    };

    let cancelled = false;
    let inFlight: XMLHttpRequest | null = null;
    // The sweep time the server said with its last chunk answer (epoch ms):
    // its staging lives FILEX_UPLOAD_STAGING_TTL past the LAST write, so each
    // acknowledged chunk moves it (lib/uploadResume `expiresAt`).
    let chunkExpiry: number | undefined;
    job.cancel = () => {
      cancelled = true;
      inFlight?.abort();
      src?.cancel?.();
    };

    /** Acknowledged bytes; the in-flight chunk is added on top for display. */
    let acked = 0;
    function report(inflight = 0) {
      job.uploadedBytes = Math.min(job.totalBytes, acked + inflight);
      job.percent =
        job.totalBytes > 0
          ? Math.min(100, Math.round((job.uploadedBytes / job.totalBytes) * 100))
          : job.status === 'done'
            ? 100
            : 0;
      opts.onProgress?.(job);
    }

    try {
      if (!available()) {
        throw markUnsupported(new Error('staged upload endpoint not configured'));
      }

      const base = stagedBase();
      let uploadId = '';
      let chunkSize = opts.chunkSize ?? DEFAULT_CHUNK;
      // The server's sweep time for this upload's staging, carried into
      // every bookmark write (lib/uploadResume): fixed at `begin`, so a
      // resumed upload keeps the moment its bookmark already holds.
      let expiresAt: number | undefined;
      chunkExpiry = undefined;

      // ── resume, or begin ────────────────────────────────────────────────
      const bookmark = loadResume(bookmarks, key);
      if (bookmark) {
        try {
          const st = await status(bookmark.uploadId);
          if (resumableState(st.state) && (st.total_size ?? 0) === total) {
            uploadId = bookmark.uploadId;
            expiresAt = resumeExpiry(st.expires_at) ?? bookmark.expiresAt;
            chunkSize = st.chunk_size ?? st.chunkSize ?? bookmark.chunkSize;
            acked = st.offset ?? 0;
            job.resumedFrom = acked;
          } else {
            clearResume(bookmarks, key);
          }
        } catch {
          // Swept, aborted, or belongs to someone else now. Not a failure —
          // `begin` below decides what happens next.
          clearResume(bookmarks, key);
        }
      }

      if (!uploadId) {
        let begun: BeginResponse;
        try {
          begun = await api.jsonFetch<BeginResponse>(`${base}/begin`, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({
              path: opts.path,
              name: opts.file.name,
              size: total,
              mime: opts.file.type || 'application/octet-stream',
              // Asked for, not imposed: the server clamps this to its own
              // limits and its answer is what the loop below uses.
              chunk_size: opts.chunkSize ?? DEFAULT_CHUNK,
            }),
          });
        } catch (err) {
          // 404 = an older server with no staged routes; 501 = staging not
          // configured on this instance. Both mean "use the other path", and
          // both happen before a byte is sent, so the caller can safely fall
          // back. Everything else is a real refusal (quota, permission, disk)
          // and must be reported, NOT retried as a whole-file POST.
          const st = (err as Error & { status?: number }).status;
          if (st === 404 || st === 501) markUnsupported(err as Error);
          throw err;
        }
        if (!begun?.id) throw new Error('begin returned no upload id');
        uploadId = begun.id;
        expiresAt = resumeExpiry(begun.expires_at);
        chunkSize = begun.chunk_size ?? begun.chunkSize ?? chunkSize;
        acked = begun.offset ?? 0;
      }

      job.uploadId = uploadId;
      job.status = 'uploading';
      // Bookmarked BEFORE the first chunk: a tab closed between `begin` and the
      // first PUT would otherwise leave a staging directory nobody can name.
      saveResume(bookmarks, key, {
        uploadId,
        path: opts.path,
        name: opts.file.name,
        size: total,
        lastModified: opts.file.lastModified,
        chunkSize,
        offset: acked,
        expiresAt,
      });
      report();

      // ── chunks ──────────────────────────────────────────────────────────
      const MAX_ATTEMPTS = 4;
      while (acked < total) {
        if (cancelled) throw new DOMException('Aborted by user', 'AbortError');
        const end = Math.min(acked + chunkSize, total);
        const blob = src ? await src.read(acked, end) : opts.file.slice(acked, end);

        let next = -1;
        let lastErr: Error | null = null;
        for (let attempt = 0; attempt < MAX_ATTEMPTS; attempt++) {
          if (attempt > 0) {
            await sleep(300 * attempt + Math.floor(Math.random() * 150));
            if (cancelled) throw new DOMException('Aborted by user', 'AbortError');
            // Re-ask before re-sending: the previous attempt may have landed
            // and lost only its response.
            try {
              const st = await status(uploadId);
              if ((st.offset ?? 0) >= end) {
                next = st.offset ?? end;
                break;
              }
              if ((st.offset ?? 0) !== acked) {
                acked = st.offset ?? acked;
                report();
                next = -2; // grid moved — recompute the slice
                break;
              }
            } catch {
              /* keep retrying the chunk itself */
            }
          }
          try {
            next = await putChunk(uploadId, blob, acked, end, total);
            break;
          } catch (err) {
            const e = err instanceof Error ? err : new Error(String(err));
            if (cancelled || e.name === 'AbortError') throw e;
            const st = (e as Error & { status?: number }).status;
            // 403/404/413 will not become true by repeating them; a dropped
            // body (400 SHORT_CHUNK) or a transient 5xx will.
            if (st && st !== 400 && st !== 409 && st < 500) throw e;
            lastErr = e;
          }
        }
        if (next === -2) continue; // offset moved under us; re-slice
        if (next < 0) throw lastErr ?? new Error('chunk upload failed');
        // Said, not "upload stalled at byte 8388608": the row is read by a
        // person, and the byte count is only for whoever is debugging.
        if (next <= acked) {
          console.warn('[filex] upload stalled at byte', acked);
          throw new Error(wordsIn(resolveLocale(config.locale))('err.upload_stalled'));
        }
        acked = next;
        if (chunkExpiry !== undefined) expiresAt = chunkExpiry;
        report();
        saveResume(bookmarks, key, {
          uploadId,
          path: opts.path,
          name: opts.file.name,
          size: total,
          lastModified: opts.file.lastModified,
          chunkSize,
          offset: acked,
          expiresAt,
        });
      }

      if (cancelled) throw new DOMException('Aborted by user', 'AbortError');

      // ── commit ──────────────────────────────────────────────────────────
      job.status = 'committing';
      report();
      const commitQs = new URLSearchParams(opts.commitQuery ?? {}).toString();
      const result = await api.jsonFetch<UploadResult>(
        `${base}/${encodeURIComponent(uploadId)}/commit${commitQs ? `?${commitQs}` : ''}`,
        { method: 'POST' },
      );
      // Committed: the node is listed and the bytes are filex's problem now,
      // so the bookmark has nothing left to recover.
      clearResume(bookmarks, key);
      job.opId = result?.op_id;
      job.nodeId = result?.node_id;

      if (opts.waitForTransfer !== false && result?.op_id && api.endpoints.opsShow) {
        job.status = 'transferring';
        report();
        await waitForOp(result.op_id);
      }

      job.status = 'done';
      acked = job.totalBytes;
      report();
      opts.onDone?.(job, result);
      return result;
    } catch (err) {
      const asError = err instanceof Error ? err : new Error(String(err));
      job.status = asError.name === 'AbortError' ? 'aborted' : 'error';
      job.error = asError.message;
      report();
      // A sourced upload stops producing — except when the server simply has
      // no staged path: nothing was read yet, and the caller may send the
      // same bytes the other way.
      if (!isStagedUnsupported(asError)) src?.cancel?.();

      // A user-cancelled upload is meant to be gone; a failed one is meant to
      // be resumable. So only the abort releases the server's staging — an
      // error keeps both the staging directory and the bookmark, which is what
      // makes the retry cost nothing.
      // wiring:e2 stream — a sourced upload cannot be resumed (its bytes are
      // gone), so its staging is released whatever ended it.
      if (job.uploadId && (job.status === 'aborted' || src)) {
        clearResume(bookmarks, key);
        try {
          await api.jsonFetch(`${stagedBase()}/${encodeURIComponent(job.uploadId)}`, {
            method: 'DELETE',
          });
        } catch {
          /* swallow — the staging sweeper collects it either way */
        }
      }

      opts.onError?.(job, asError);
      throw asError;
    }

    /** One chunk, over XHR because fetch cannot report upload progress. */
    function putChunk(
      uploadId: string,
      blob: Blob,
      start: number,
      end: number,
      total: number,
    ): Promise<number> {
      return new Promise<number>((resolve, reject) => {
        const xhr = new XMLHttpRequest();
        inFlight = xhr;
        xhr.open('PUT', `${stagedBase()}/${encodeURIComponent(uploadId)}`);
        const headers = api.authHeadersSync({
          'Content-Type': 'application/octet-stream',
          'Content-Range': `bytes ${start}-${end - 1}/${total}`,
        });
        for (const [k, v] of Object.entries(headers)) xhr.setRequestHeader(k, v);
        xhr.withCredentials = api.credentialsMode() === 'include';
        xhr.upload.onprogress = (ev) => {
          if (ev.lengthComputable) report(ev.loaded);
        };
        xhr.onload = () => {
          inFlight = null;
          noteRequestSucceeded();
          if (xhr.status >= 200 && xhr.status < 300) {
            try {
              const body = JSON.parse(xhr.responseText) as StatusResponse;
              chunkExpiry = resumeExpiry(body.expires_at) ?? chunkExpiry;
              resolve(body.offset ?? end);
            } catch {
              // A 2xx we cannot parse still means the bytes landed; the offset
              // is re-read from the server rather than guessed.
              resolve(end);
            }
            return;
          }
          // Said (lib/errorWords): the upload row used to read
          // "chunk 0-8388607 → 500". Status and body still ride along.
          reject(requestFailure(xhr.status, xhr.responseText || '', resolveLocale(config.locale)));
        };
        xhr.onerror = () => {
          inFlight = null;
          noteRequestFailed('action');
          reject(networkFailure(resolveLocale(config.locale)));
        };
        xhr.onabort = () => {
          inFlight = null;
          reject(new DOMException('Aborted', 'AbortError'));
        };
        xhr.send(blob);
      });
    }

    /** Poll the shared ops tray until the transfer leaves the queue. */
    async function waitForOp(opId: number): Promise<void> {
      const tmpl = api.endpoints.opsShow;
      if (!tmpl) return;
      const url = tmpl.replace('{id}', String(opId));
      let delay = 200;
      // ⚠ A tray read that keeps failing must not park the upload in
      // `transferring` for ever. One hiccup is not a verdict, but a row we
      // cannot read at all (purged, or the endpoint is gone) is unknowable, so
      // after a bounded run of failures the wait ends and the job settles the
      // way it did before the wait existed — optimistic, but not hung.
      let misses = 0;
      for (;;) {
        if (cancelled) throw new DOMException('Aborted by user', 'AbortError');
        try {
          const op = await api.jsonFetch<{ status?: string; error?: string }>(url);
          if (op?.status === 'ok') return;
          if (op?.status === 'failed' || op?.status === 'partial') {
            throw new TransferFailedError(op.error || 'transfer failed');
          }
          misses = 0;
        } catch (err) {
          // ⚠ The verdict is told apart from the hiccup by TYPE, not by
          // message. It used to be `message === 'transfer failed'`, which only
          // matched when the server sent NO error text: the moment it sent a
          // real one — which is every interesting failure — the throw above was
          // swallowed here and the poll span for ever. Harmless while nothing
          // set waitForTransfer; a hang the moment anything did.
          if (err instanceof TransferFailedError || (err as Error).name === 'AbortError') throw err;
          /* a hiccup reading the tray is not a failed transfer */
          if (++misses >= MAX_OP_POLL_MISSES) return;
        }
        await sleep(delay);
        delay = Math.min(delay * 2, 2000);
      }
    }
  }

  return {
    uploadFile,
    /** True when this file is big enough (and the endpoints exist) to chunk. */
    shouldChunk,
    /** The size at which chunking kicks in. */
    threshold,
    resumableFor,
    listResumable,
    discardResumable,
  };
}

/** How many consecutive unreadable op polls end the transfer wait (~7s). */
const MAX_OP_POLL_MISSES = 6;

/** The backend said the transfer failed — as opposed to "I could not ask". */
class TransferFailedError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'TransferFailedError';
  }
}

function sleep(ms: number): Promise<void> {
  return new Promise((r) => setTimeout(r, ms));
}
