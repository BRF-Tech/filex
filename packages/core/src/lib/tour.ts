/**
 * tour — "has this person already been offered the first-use tour?"
 *
 * ⚠⚠ Why this module exists (v0.43.0). The tour used to open 900 ms after
 * EVERY explorer mount whose browser had no `filex.tourDone` flag, and the
 * flag was written only when the tour was CLOSED. So a person who opened a
 * second tab before closing the tour got it again; so did every new tab, every
 * route that remounted the explorer, every other browser, every other device.
 * A browser-automation run met it on each fresh mount and its clicks landed on
 * the tour card one run in two. The owner's rule: the tour is offered to a
 * PERSON once — never once per mount.
 *
 * The answer is kept in two places and read as ONE (an "or"):
 *
 *   · the account document (`lib/prefs`, key `tour`) — the person's answer,
 *     on every browser and device that signs in as them;
 *   · this browser's `filex.tourDone` — the answer before the account's
 *     arrives, and the only one an embed without an account copy has.
 *
 * ⚠ It only ever goes one way. Nothing clears it: a tour that has been seen
 * cannot be un-seen, and "Restart the tour" opens it without resetting it.
 * That is why the local flag is NOT a per-person mirror cleared at sign-out —
 * a flag that can only say "yes" cannot leak a wrong "no" to the next person.
 *
 * ⚠ It is recorded when the tour is OFFERED, not when it is closed. Closing
 * was the old trigger, and it is exactly what made a second tab, opened while
 * the first still showed the tour, offer it again.
 */
import {
  currentPrefs,
  onPrefs,
  onPrefsSettled,
  prefsConfigured,
  prefsHydrated,
  prefsSettled,
  savePref,
} from './prefs';

/** This browser's copy. The name is the one every earlier release wrote. */
export const TOUR_LS_KEY = 'filex.tourDone';

/** The value the account document carries once the tour was offered. */
export const TOUR_DONE = 'done';

/** How long after a mount the tour opens — the listing and toolbar are laid out by then. */
export const TOUR_DELAY_MS = 900;

/**
 * How long a mount waits for the account's answer before it decides on this
 * browser's alone. Bounded: a server that never answers must not mean a tour
 * that never comes, nor one that lands in the middle of somebody's work.
 */
export const TOUR_ACCOUNT_WAIT_MS = 5000;

function seenHere(): boolean {
  try {
    return !!localStorage.getItem(TOUR_LS_KEY);
  } catch {
    // No storage (blocked site data, a private window that refuses it): never
    // auto-nag. The tour is one menu row away for anybody who wants it.
    return true;
  }
}

/** Has this person — here, or on the account — already been offered the tour? */
export function tourSeen(): boolean {
  return seenHere() || !!currentPrefs().tour;
}

let accountWriteQueued: (() => void) | null = null;

/**
 * Record that the tour has been offered: in this browser at once, and on the
 * account as soon as the account's document is known.
 *
 * ⚠⚠ Never written to the account BEFORE its document has arrived. `PUT
 * /api/me/prefs` replaces the whole document, and a write made from an empty
 * copy would erase the person's palette, density and language to record a
 * tour.
 */
export function markTourSeen(): void {
  try {
    localStorage.setItem(TOUR_LS_KEY, '1');
  } catch {
    /* see seenHere */
  }
  if (!prefsConfigured()) return;
  if (prefsHydrated()) {
    if (!currentPrefs().tour) savePref('tour', TOUR_DONE);
    return;
  }
  if (accountWriteQueued) return;
  const off = onPrefs((p) => {
    off();
    accountWriteQueued = null;
    if (!p.tour) savePref('tour', TOUR_DONE);
  });
  accountWriteQueued = off;
}

/**
 * Offer the tour once, `delayMs` after a mount, unless this person has already
 * been offered it. Returns a cancel for the unmount.
 *
 * With an account behind the explorer the decision waits for the account's
 * answer (at most `accountWaitMs`), so a person who took the tour on another
 * device is not shown it again while the answer is on its way.
 */
export function offerTourOnce(
  show: () => void,
  opts: { delayMs?: number; accountWaitMs?: number } = {},
): () => void {
  const delayMs = opts.delayMs ?? TOUR_DELAY_MS;
  const waitMs = opts.accountWaitMs ?? TOUR_ACCOUNT_WAIT_MS;
  let cancelled = false;
  let delay: ReturnType<typeof setTimeout> | undefined;
  let giveUp: ReturnType<typeof setTimeout> | undefined;
  let unwait: (() => void) | undefined;

  const cancel = () => {
    cancelled = true;
    if (delay) clearTimeout(delay);
    if (giveUp) clearTimeout(giveUp);
    unwait?.();
  };
  if (tourSeen()) return cancel;

  const decide = () => {
    if (giveUp) clearTimeout(giveUp);
    unwait?.();
    if (cancelled || tourSeen()) return;
    markTourSeen();
    show();
  };

  delay = setTimeout(() => {
    if (cancelled || tourSeen()) return;
    if (!prefsConfigured() || prefsSettled()) {
      decide();
      return;
    }
    giveUp = setTimeout(decide, waitMs);
    unwait = onPrefsSettled(decide);
  }, delayMs);
  return cancel;
}

/** Testing seam. */
export function resetTourState(): void {
  accountWriteQueued?.();
  accountWriteQueued = null;
}
