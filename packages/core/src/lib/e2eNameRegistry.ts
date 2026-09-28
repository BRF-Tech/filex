/**
 * e2eNameRegistry — how a surface OUTSIDE the explorer (the notification bell
 * and panel) names an item inside an encrypted folder.
 *
 * The names of a level-2 folder are ciphertext on the server; only an
 * explorer that has the folder unlocked holds the plaintext (its name view,
 * composables/useE2eNames). A mounted explorer registers a resolver here; the
 * bell asks it. Nothing is copied out of the explorer and nothing is kept:
 * locking the folder (or unmounting the explorer) makes the answer null
 * again, and the bell goes back to "🔒 Encrypted item".
 *
 * Reactive: `resolveE2eName` reads a version that changes when a resolver
 * comes or goes, and each resolver reads its name view's own version, so a
 * row rendered while a name was still being decrypted re-renders with it.
 */
import { ref } from 'vue';

export type E2eNameResolver = (wire: string, root: string) => { name: string; path: string } | null;

const resolvers = new Set<E2eNameResolver>();
const version = ref(0);

/** Register a resolver; returns its unregister. */
export function registerE2eNameResolver(r: E2eNameResolver): () => void {
  resolvers.add(r);
  version.value++;
  return () => {
    if (resolvers.delete(r)) version.value++;
  };
}

/** The plaintext of `wire` (inside the encrypted folder `root`), or null. */
export function resolveE2eName(wire: string, root: string): { name: string; path: string } | null {
  void version.value;
  for (const r of resolvers) {
    const out = r(wire, root);
    if (out) return out;
  }
  return null;
}
