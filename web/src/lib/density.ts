// The file list's row density — "comfortable" or "compact".
//
// ⚠ This module does NOT own the preference. `packages/core`'s Toolbar does:
// it declares `DENSITY_LS_KEY = 'filex.density'`, reads it at setup and emits
// the value up so both views mirror it onto a root class. Everything here is a
// SECOND WRITER of that same key, which is the only honest way for a settings
// modal outside the explorer to move a preference the explorer owns.
//
// Because it is a second writer, the key is the whole contract, and a rename
// on core's side would leave this switch writing to a key nobody reads — the
// exact "saves, reads back, does nothing" shape this surface exists to avoid.
// `web/tests/lib/density.test.ts` parses Toolbar.vue and fails when the two
// names drift.
//
// It applies immediately: core's Toolbar now listens for the `storage` event
// (another tab) and for the `filex:density` event this module fires (same tab,
// where the browser sends no storage event to the window that wrote).

import { savePref, setLocalPref } from '@brftech/filex-core';

export const DENSITY_KEY = 'filex.density';

export type Density = 'comfortable' | 'compact';

/**
 * The account's density has arrived: apply it, without sending it back.
 *
 * Fires the same `filex:density` event a click would, so an explorer already
 * on screen re-lays-out instead of waiting for a navigation.
 */
export function applyAccountDensity(d: string | undefined | null): void {
  if (d !== 'comfortable' && d !== 'compact') return;
  setLocalPref('density', d);
  try {
    window.dispatchEvent(new CustomEvent('filex:density', { detail: d }));
  } catch {
    /* older engine without CustomEvent constructor */
  }
}

export function getDensity(): Density {
  try {
    return localStorage.getItem(DENSITY_KEY) === 'compact' ? 'compact' : 'comfortable';
  } catch {
    // private mode / blocked site data — the explorer's own read falls back
    // the same way, so "comfortable" is what it will show too.
    return 'comfortable';
  }
}

export function setDensity(d: Density): void {
  try {
    localStorage.setItem(DENSITY_KEY, d);
  } catch {
    /* a display preference is never worth taking the page down for */
  }
  // ⚠ v3 — and on the ACCOUNT, so the next browser opens the same list
  // (`@brftech/filex-core` → lib/prefs). localStorage above is now the
  // first-paint cache, not the preference.
  savePref('density', d);
  // A tab does not receive its own `storage` event, so say it out loud. The
  // explorer may not be mounted, and nobody has to be listening.
  try {
    window.dispatchEvent(new CustomEvent('filex:density', { detail: d }));
  } catch {
    /* older engine without CustomEvent constructor */
  }
}
