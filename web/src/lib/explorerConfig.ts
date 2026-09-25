// How this SPA authenticates the shared filex components.
//
// The explorer page, the connections page and the "how to connect" overlay
// all mount components from `@brftech/filex-core`, and all three need the
// same answer to "who is calling?". It was written out twice already (two
// copies of readCsrfCookie/readBearerToken in Explore.vue); a third copy is
// how one of them ends up not sending the token.

import { ref } from 'vue';
import type { AuthConfig } from '@brftech/filex-core';

/**
 * The mouse open gesture, a per-viewer preference stored in localStorage
 * (`filex.openTrigger`) and written by UserSettingsModal. Default `'double'` —
 * a single click selects, a double click opens; `'single'` restores one-click
 * open. Touch is never governed by this (a tap always opens). Kept here so the
 * explorer page and the embedded web component read the same answer. ⚠ e2e and
 * Cypress pin this to `'single'` so their single-click "open" steps keep
 * working — see the suites' setup.
 *
 * `openTriggerSignal` makes a `computed` that calls `openTriggerPref()` re-run
 * when the setting changes, so flipping the toggle re-applies live (the config
 * prop updates and FilePane reads it reactively) rather than waiting for a
 * reload — the same immediacy the desktop app's remount gives.
 */
export const openTriggerSignal = ref(0);

export function openTriggerPref(): 'single' | 'double' {
  void openTriggerSignal.value; // reactive dependency — see setOpenTriggerPref
  try {
    return localStorage.getItem('filex.openTrigger') === 'single' ? 'single' : 'double';
  } catch {
    return 'double';
  }
}

export function setOpenTriggerPref(value: 'single' | 'double'): void {
  try {
    localStorage.setItem('filex.openTrigger', value);
  } catch {
    /* private mode / blocked storage — the choice just does not persist */
  }
  openTriggerSignal.value++;
}

export function readCsrfCookie(): string | null {
  const prefix = 'filex_csrf=';
  for (const part of document.cookie.split(';')) {
    const trimmed = part.trim();
    if (trimmed.startsWith(prefix)) return decodeURIComponent(trimmed.slice(prefix.length));
  }
  return null;
}

export function readBearerToken(): string | null {
  return sessionStorage.getItem('filex.bearer');
}

/**
 * The auth block for a core component.
 *
 * Bearer first (the Electron shell and the demo flow store one), CSRF
 * cookie second (a normal browser session), `none` last — which is honest:
 * the component then shows the server's 401 rather than pretending.
 */
export function explorerAuth(): AuthConfig {
  const bearer = readBearerToken();
  if (bearer) return { kind: 'bearer', token: bearer };
  const csrf = readCsrfCookie();
  if (csrf) return { kind: 'csrf', csrf };
  return { kind: 'none' };
}
