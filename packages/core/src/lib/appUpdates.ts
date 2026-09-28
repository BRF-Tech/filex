/**
 * An app's approved version changed — an administrator approved a newer one,
 * or went back to the one before (realtime `app.updated`, backend
 * realtime/apps.go). Every open AppFrame on that app hears it here and offers
 * to reload; the explorer's cached menu rows are dropped so the next opening
 * gets the new interface's address.
 *
 * ⚠ One place for every surface (explorer, app page, embeds): the frame
 * decides what to do with the notice, not the page that happens to host it.
 */
type Listener = (app: string, version: string) => void;

const listeners = new Set<Listener>();

/** Tell every open frame that `app` now runs at `version`. */
export function announceAppUpdated(app: string, version: string): void {
  for (const l of [...listeners]) {
    try {
      l(app, version);
    } catch {
      /* one frame's trouble is not the others' */
    }
  }
}

/** Hear it; the returned function stops hearing it. */
export function onAppUpdated(l: Listener): () => void {
  listeners.add(l);
  return () => {
    listeners.delete(l);
  };
}
