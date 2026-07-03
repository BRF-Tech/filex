/**
 * useMonacoLoader — fire-and-forget Monaco preloader.
 *
 * The preview/edit flow wants Monaco when available (best-in-class
 * editor) and highlight.js for read-only syntax colour as a graceful
 * fallback. Monaco is heavy (~5 MB), so we kick off the dynamic import
 * at module-init time — by the time the user clicks "edit" on a code
 * file (typically several seconds in), the chunk is already cached.
 *
 *   - `monacoPromise`     resolves to the Monaco namespace, or `null`
 *                         if the package is not installed (peer
 *                         missing) / load fails.
 *   - `highlightPromise`  resolves to the highlight.js export, or
 *                         `null` similarly.
 *   - `getMonaco()`       returns the cached module synchronously
 *                         (after `await monacoPromise`) so a click
 *                         handler doesn't re-trigger the import.
 *
 * Both modules are externalized in the bundler config — the consumer
 * either declares them as direct deps (Monaco UX) or doesn't (the
 * highlight.js read-only path takes over). No build-time hard dep here.
 */

let monacoPromise: Promise<unknown | null> | null = null;
let highlightPromise: Promise<unknown | null> | null = null;
let monacoCached: unknown | null = null;
let highlightCached: unknown | null = null;

/**
 * Kick off the Monaco import the first time someone calls
 * `preloadEditor()` (typically the FileExplorer onMounted hook). Cheap
 * to call repeatedly — only the first call actually triggers the
 * dynamic import.
 */
export function preloadEditor(): void {
  if (!monacoPromise) {
    monacoPromise = import(/* @vite-ignore */ 'monaco-editor')
      .then((mod) => {
        monacoCached = (mod as { default?: unknown }).default ?? mod;
        return monacoCached;
      })
      .catch(() => {
        monacoCached = null;
        return null;
      });
  }
  if (!highlightPromise) {
    highlightPromise = import(/* @vite-ignore */ 'highlight.js')
      .then((mod) => {
        highlightCached = (mod as { default?: unknown }).default ?? mod;
        return highlightCached;
      })
      .catch(() => {
        highlightCached = null;
        return null;
      });
  }
}

/**
 * Resolve Monaco. If preload hasn't been kicked off yet, this triggers
 * it (and pays the load cost on the spot). Returns `null` when the
 * package isn't installed.
 */
export async function ensureMonaco(): Promise<unknown | null> {
  if (!monacoPromise) preloadEditor();
  return monacoPromise!;
}

export async function ensureHighlight(): Promise<unknown | null> {
  if (!highlightPromise) preloadEditor();
  return highlightPromise!;
}

/** Synchronous cached lookup — null if Monaco not yet ready or absent. */
export function getMonaco(): unknown | null {
  return monacoCached;
}

export function getHighlight(): unknown | null {
  return highlightCached;
}

/**
 * Composable wrapper — components that want reactive "is Monaco ready
 * yet?" semantics can read the resolved promise. The implementation is
 * intentionally tiny: components don't need anything fancier than
 * `await ensureMonaco()` inside the click handler.
 */
export function useMonacoLoader() {
  preloadEditor();
  return {
    ensureMonaco,
    ensureHighlight,
    getMonaco,
    getHighlight,
  };
}
