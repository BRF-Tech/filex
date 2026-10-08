/**
 * A dialog or panel the explorer loads when it mounts it, not with itself.
 *
 * ⚠ The web app's main chunk carries the explorer and has to fit workbox's
 * 2 MiB precache limit (web/pwa.config.ts), or the build fails. The surfaces a
 * person opens - the viewer, sharing, settings, the encryption dialogs - are
 * not needed to draw the explorer, so FileExplorer.vue holds them through this
 * instead of an `import`: the component is the same, its code is a chunk of
 * its own, fetched when the explorer first mounts it. Which surfaces, and why
 * the library build needs a rule of its own for them, is
 * packages/core/lazySurfaces.ts; web/tests/quality/lazySurfaces.test.ts holds
 * the explorer to that list.
 *
 * The loader must be written at the call site as a literal
 * `() => import('./x.vue')`: that is what the bundlers split on.
 *
 * `suspensible: false`: a dialog that is still loading never makes a host's
 * `<Suspense>` hold back the whole explorer. Until it has loaded it draws
 * nothing, which for a closed dialog is what it drew anyway.
 */
import { defineAsyncComponent, type AsyncComponentLoader, type Component } from 'vue';

export function lazySurface<T extends Component>(loader: AsyncComponentLoader<T>): T {
  return defineAsyncComponent<T>({ loader, suspensible: false });
}
