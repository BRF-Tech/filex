/**
 * pdfjsLoader — the ONE way this package brings pdf.js in.
 *
 * `pdfjs-dist` is ~600 KB and an optional peer, so it is imported lazily and
 * its absence is an answer (`null`), not an exception. The worker script is
 * a separate file the browser fetches: the host may point at its own copy
 * (`pdfWorkerUrl` in the explorer config) and otherwise the pinned version
 * is taken from jsDelivr. `viewers/PdfViewer.vue` and the sign track's
 * `pdf-fields` node both open documents through here, so the legacy build,
 * the worker rule and the fallback cannot drift between them.
 */

/** The subset of the library the callers use; `any` on purpose — pdf.js ships no stable types for the legacy build. */
// eslint-disable-next-line @typescript-eslint/no-explicit-any
export type PdfjsLib = any;

let cached: Promise<PdfjsLib | null> | null = null;

/** The worker URL for a pdf.js version when the host names none. */
export function defaultPdfWorkerUrl(version: string): string {
  return `https://cdn.jsdelivr.net/npm/pdfjs-dist@${version}/legacy/build/pdf.worker.min.mjs`;
}

/**
 * The library, or `null` when it cannot be loaded (not installed, blocked).
 * The first caller's `workerUrl` wins; pdf.js holds one global worker path.
 */
export function loadPdfjs(workerUrl?: string | null): Promise<PdfjsLib | null> {
  if (!cached) {
    cached = (async () => {
      try {
        // The legacy build: broader browser support and a classic worker
        // bootstrap (no module-worker requirement).
        const mod = await import(/* @vite-ignore */ 'pdfjs-dist/legacy/build/pdf');
        const lib = mod.default ?? mod;
        if (lib.GlobalWorkerOptions && !lib.GlobalWorkerOptions.workerSrc) {
          lib.GlobalWorkerOptions.workerSrc = workerUrl || defaultPdfWorkerUrl(lib.version || '5.7.284');
        }
        return lib;
      } catch {
        return null;
      }
    })();
  }
  return cached;
}

/** Tests only: forget the cached import so a mocked module is picked up. */
export function resetPdfjsLoader(): void {
  cached = null;
}
