/**
 * useUploadChunked — S3 multipart upload orchestrator for the browser.
 *
 * Contract (matches the standard /api/files/upload/{init,finalize,abort}):
 *   1. POST init  {path, filename, size, mime}
 *        → {uploadId, parts: [{partNumber, presignedUrl}], chunkBytes, totalParts}
 *   2. For each part: PUT <presignedUrl> <chunk bytes>, collect ETag header
 *   3. POST finalize {uploadId, parts: [{partNumber, etag}]}
 *        → {s3Key, url, size}
 *   Abort path: POST abort {uploadId} any time.
 *
 * We parallelize up to `parallelChunks` uploads (default 4). Progress is
 * reported as an aggregate percentage across all parts — individual part
 * percentages are tracked internally and summed.
 */

import type { ExplorerConfig } from '../types/ExplorerConfig';
import type { FileApi } from './useFileApi';
import type { UploadInitResponse, UploadFinalizeResponse } from '../types/FileNode';

export interface UploadJob {
  id: string; // local uuid
  file: File;
  path: string;
  uploadId?: string;
  totalBytes: number;
  uploadedBytes: number;
  percent: number;
  status: 'pending' | 'initializing' | 'uploading' | 'finalizing' | 'done' | 'error' | 'aborted';
  error?: string;
  cancel(): void;
}

export interface UploadOptions {
  path: string;
  file: File;
  chunkSize?: number;
  parallelChunks?: number;
  onProgress?: (job: UploadJob) => void;
  onDone?: (job: UploadJob, result: UploadFinalizeResponse) => void;
  onError?: (job: UploadJob, err: Error) => void;
}

export function useUploadChunked(config: ExplorerConfig, api: FileApi) {
  const DEFAULT_CHUNK = config.chunkSize ?? 5 * 1024 * 1024;
  const DEFAULT_PARALLEL = Math.max(1, Math.min(8, config.parallelChunks ?? 4));

  async function uploadFile(opts: UploadOptions): Promise<UploadFinalizeResponse> {
    const chunkSize = opts.chunkSize ?? DEFAULT_CHUNK;
    const parallel = opts.parallelChunks ?? DEFAULT_PARALLEL;

    const job: UploadJob = {
      id: crypto.randomUUID(),
      file: opts.file,
      path: opts.path,
      totalBytes: opts.file.size,
      uploadedBytes: 0,
      percent: 0,
      status: 'initializing',
      cancel: () => {}, // rebound below
    };

    let cancelled = false;
    const aborters: AbortController[] = [];
    job.cancel = () => {
      cancelled = true;
      for (const a of aborters) a.abort();
    };

    function report() {
      job.percent =
        job.totalBytes > 0
          ? Math.min(100, Math.round((job.uploadedBytes / job.totalBytes) * 100))
          : 0;
      opts.onProgress?.(job);
    }

    try {
      if (!api.endpoints.uploadInit) throw new Error('uploadInit not configured');
      // 1) init
      const init = await api.jsonFetch<UploadInitResponse & { chunkBytes?: number; totalParts?: number }>(
        api.endpoints.uploadInit,
        {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            path: opts.path,
            filename: opts.file.name,
            size: opts.file.size,
            mime: opts.file.type || 'application/octet-stream',
          }),
        },
      );
      job.uploadId = init.uploadId;
      job.status = 'uploading';
      report();

      const actualChunk = init.chunkBytes ?? chunkSize;
      void actualChunk; // referenced below — keep TS happy with `chunkSize` shadow
      const partProgress = new Map<number, number>();

      // 2) upload parts in parallel windows
      const completed: Array<{ partNumber: number; etag: string }> = [];
      let cursor = 0;

      async function uploadPart(entry: { partNumber: number; presignedUrl: string }): Promise<void> {
        const idx = entry.partNumber - 1;
        const start = idx * (init.chunkBytes ?? chunkSize);
        const end = Math.min(start + (init.chunkBytes ?? chunkSize), opts.file.size);
        const blob = opts.file.slice(start, end);

        const ctl = new AbortController();
        aborters.push(ctl);

        await new Promise<void>((resolve, reject) => {
          const xhr = new XMLHttpRequest();
          xhr.open('PUT', entry.presignedUrl);
          xhr.upload.onprogress = (ev) => {
            if (ev.lengthComputable) {
              const prev = partProgress.get(entry.partNumber) ?? 0;
              const delta = ev.loaded - prev;
              partProgress.set(entry.partNumber, ev.loaded);
              job.uploadedBytes += delta;
              report();
            }
          };
          xhr.onload = () => {
            if (xhr.status >= 200 && xhr.status < 300) {
              const etag = (xhr.getResponseHeader('ETag') || xhr.getResponseHeader('etag') || '').trim();
              if (!etag) {
                reject(new Error('Missing ETag on S3 response'));
                return;
              }
              // Top off in case the last progress event under-reported.
              const blobSize = end - start;
              const prev = partProgress.get(entry.partNumber) ?? 0;
              if (prev < blobSize) {
                job.uploadedBytes += blobSize - prev;
                partProgress.set(entry.partNumber, blobSize);
              }
              completed.push({ partNumber: entry.partNumber, etag });
              resolve();
            } else {
              reject(
                new Error(`S3 PUT ${entry.partNumber} → ${xhr.status}: ${xhr.responseText.slice(0, 200)}`),
              );
            }
          };
          xhr.onerror = () => reject(new Error(`S3 PUT ${entry.partNumber} network error`));
          xhr.onabort = () => reject(new DOMException('Aborted', 'AbortError'));
          ctl.signal.addEventListener('abort', () => xhr.abort());
          xhr.send(blob);
        });
      }

      /**
       * Wrap `uploadPart` in a tiny exponential-backoff retry so a
       * single hiccup on the edge (intermittent TCP RST, transient
       * 5xx) doesn't kill the whole multipart session. Counter is
       * per-part — a flaky one doesn't burn the budget for healthy
       * neighbours. We DON'T retry user cancellations or AbortError.
       */
      const MAX_RETRIES = 2;
      async function uploadPartWithRetry(
        entry: { partNumber: number; presignedUrl: string },
        attempt = 0,
      ): Promise<void> {
        try {
          await uploadPart(entry);
        } catch (err) {
          if (cancelled) throw err;
          const e = err instanceof Error ? err : new Error(String(err));
          if (e.name === 'AbortError') throw e;
          if (attempt >= MAX_RETRIES) throw e;

          // Roll back any partial uploadedBytes so progress doesn't
          // double-count after the retry succeeds.
          const leaked = partProgress.get(entry.partNumber) ?? 0;
          if (leaked > 0) {
            job.uploadedBytes = Math.max(0, job.uploadedBytes - leaked);
            partProgress.delete(entry.partNumber);
            report();
          }

          // Exponential backoff with jitter — 500ms, 1s, 1.5s …
          const delay = 500 * (attempt + 1) + Math.floor(Math.random() * 250);
          await new Promise((r) => setTimeout(r, delay));
          return uploadPartWithRetry(entry, attempt + 1);
        }
      }

      async function worker() {
        while (!cancelled && cursor < init.parts.length) {
          const entry = init.parts[cursor++];
          await uploadPartWithRetry(entry);
        }
      }

      const workers: Promise<void>[] = [];
      for (let i = 0; i < parallel; i++) {
        workers.push(worker());
      }
      await Promise.all(workers);

      if (cancelled) {
        throw new DOMException('Aborted by user', 'AbortError');
      }

      // 3) finalize
      if (!api.endpoints.uploadFinalize) throw new Error('uploadFinalize not configured');
      job.status = 'finalizing';
      completed.sort((a, b) => a.partNumber - b.partNumber);
      const final = await api.jsonFetch<UploadFinalizeResponse>(api.endpoints.uploadFinalize, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ uploadId: init.uploadId, parts: completed }),
      });

      job.status = 'done';
      job.uploadedBytes = job.totalBytes;
      job.percent = 100;
      report();
      opts.onDone?.(job, final);
      return final;
    } catch (err) {
      const asError = err instanceof Error ? err : new Error(String(err));
      job.status = asError.name === 'AbortError' ? 'aborted' : 'error';
      job.error = asError.message;
      report();

      // Best-effort server-side cleanup
      if (job.uploadId && api.endpoints.uploadAbort && job.status === 'aborted') {
        try {
          await api.jsonFetch(api.endpoints.uploadAbort, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ uploadId: job.uploadId }),
          });
        } catch {
          /* swallow — DB row will expire, S3 TTL handles orphan parts */
        }
      }

      opts.onError?.(job, asError);
      throw asError;
    }
  }

  return { uploadFile };
}
