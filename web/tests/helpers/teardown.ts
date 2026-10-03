/**
 * The end of every test, in the one order that is safe.
 *
 * setup.ts calls `teardownDom()` after each test, for every file, so a test
 * does NOT unmount what it mounted and does NOT clear <body> itself:
 *
 *   1. `flushPromises()` — what the page still has in flight lands now, while
 *      the page is whole;
 *   2. every wrapper `mount()` returned that is still mounted is unmounted
 *      (the test never has to keep a list);
 *   3. only then is <body> emptied.
 *
 * ⚠ Why the order (task #127, v0.49.0 release run): a test that empties
 * <body> while its page is still MOUNTED leaves a live component whose DOM is
 * gone. The moment anything it awaits answers — often in the next test — it
 * re-draws into that missing DOM (a menu teleported under <body> is the
 * usual victim): "Cannot read properties of null (reading 'insertBefore')",
 * an unhandled rejection that makes vitest exit 1 with every test green.
 *
 * A test that starts over half-way (mounts a second page in one test) calls
 * `await teardownDom()` itself — never `document.body.innerHTML = ''` alone.
 */
import { config, flushPromises, type VueWrapper } from '@vue/test-utils';

const live = new Set<VueWrapper>();

// Every VueWrapper passes through the plugin hook as it is made; only the
// root one (the one mount() returns) owns an app, and only that one unmounts.
// ⚠ @vue/test-utils is loaded by Node, not per test file: should a worker
// ever run two files (vitest --no-isolate), a second install() would leave
// the first file's handler filling a Set nobody empties. So the hook is
// installed once and forwards to whichever file's setup ran last.
type Track = (w: VueWrapper) => void;
const TRACK = Symbol.for('filex.tests.teardown.track');
const hooks = config.plugins.VueWrapper as unknown as Record<symbol, Track | undefined>;
if (!hooks[TRACK]) {
  config.plugins.VueWrapper.install((wrapper) => {
    hooks[TRACK]?.(wrapper as VueWrapper);
    return {};
  });
}
hooks[TRACK] = (w) => {
  if ((w as unknown as { __app?: unknown }).__app) live.add(w);
};

// A test may have unmounted its page already; unmounting an app twice only
// earns a Vue warning, but the warning is noise. Vue clears `_instance` when
// the app is unmounted (and a functional component has no vm to ask).
function stillMounted(w: VueWrapper): boolean {
  const app = (w as unknown as { __app?: { _instance?: unknown } }).__app;
  return !!app && app._instance != null;
}

/** Unmount every page still mounted, then empty <body>. No waiting. */
export function unmountAll(): void {
  for (const w of [...live]) {
    live.delete(w);
    if (stillMounted(w)) w.unmount();
  }
  if (typeof document !== 'undefined' && document.body) document.body.innerHTML = '';
}

/** Let what is in flight land, unmount every page, then empty <body>. */
export async function teardownDom(): Promise<void> {
  await flushPromises();
  unmountAll();
}
