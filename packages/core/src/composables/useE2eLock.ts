/**
 * The explorer's encrypted-folder lock, for the panes it hosts (wiring:e2).
 *
 * The key ring is the explorer's and there is one per tab: a folder unlocked
 * in one pane is unlocked in the other. The split pane (FilePane, self-driven)
 * lists folders on its own, so when it stands inside a locked encrypted folder
 * it needs the same lock screen and the same unlock the main pane has — this
 * is how it reaches them without a second copy of the unlock code.
 */
import type { InjectionKey } from 'vue';

export interface E2eLockContext {
  /** Is the encrypted folder at this wire root locked in this tab? Reactive. */
  locked(root: string): boolean;
  /** Unlock it with its password: null when it opened, else what to show. */
  unlock(root: string, password: string): Promise<string | null>;
  /** The other doors (recovery key, escrow key): in the main pane, which has
   *  the dialogs for them — it goes to the folder and opens them there. */
  recovery(root: string): void;
}

export const E2E_LOCK: InjectionKey<E2eLockContext> = Symbol('filex.e2eLock');
