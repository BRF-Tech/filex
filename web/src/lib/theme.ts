// Theme handling: 'light' | 'dark' | 'auto'.
// Auto follows the OS via prefers-color-scheme.
//
// ⚠⚠ v3 — the choice lives on the ACCOUNT (`@brftech/filex-core` →
// `lib/prefs`, `GET/PUT /api/me/prefs?surface=web`), not in this browser.
// Light/dark, the palette, the row density and the language were each stored
// here and nowhere else, so a person who set the product up the way they like
// it on one machine met the defaults again on the next one.
//
// `localStorage` stays for ONE job: the first paint. `applyStoredTheme()`
// runs before the app mounts and the account's answer cannot beat the first
// frame, so the key below is the cache that stops the window flashing the
// wrong palette — and it is overwritten the moment the server answers.
const KEY = 'filex.theme';

import { readonly, ref, type Ref } from 'vue';
import { hasSession, savePref } from '@brftech/filex-core';

export type ThemeMode = 'light' | 'dark' | 'auto';

/**
 * ⚠⚠ NOT FOR A SIGNED-OUT WINDOW. Light or dark is a PERSON's answer, kept
 * on their account beside the palette, so with nobody signed in this reports
 * `'auto'` — which `effectiveTheme()` resolves through `prefers-color-scheme`,
 * i.e. the BROWSER decides. Owner, 2026-09-21: "logoutluyken … tarayıcı gece
 * modundaysa gece modunda olacak ya da light mode."
 *
 * ⚠ The mirror is deliberately not deleted, only unread. It is still this
 * browser's first-paint cache for the next sign-in, and wiping it on every
 * visit to the login page would make a returning person flash the wrong mode
 * on every load — and would destroy the pre-v3 carry-over `hydratePrefs`
 * leans on (a person whose preferences only ever lived in localStorage).
 *
 * ⚠ The only control that writes this key lives in the user-settings panel,
 * which a signed-out visitor cannot open (Login.vue's header comment), so
 * nothing reachable without a session is made unusable by the guard.
 */
export function getStoredTheme(): ThemeMode {
  if (!hasSession()) return 'auto';
  const v = localStorage.getItem(KEY);
  if (v === 'light' || v === 'dark' || v === 'auto') return v;
  return 'auto';
}

export function setStoredTheme(mode: ThemeMode): void {
  localStorage.setItem(KEY, mode);
  // ⚠⚠ `paint(mode)`, NOT `applyStoredTheme()`. Somebody clicking Dark in
  // the settings panel is manifestly present, so the choice must land whatever
  // `hasSession()` believes — and it can believe the wrong thing, because the
  // hint is a localStorage write that a private window or blocked site data
  // can swallow. Routed back through the guarded reader, the toggle wrote the
  // key and then painted 'auto' over it: a control that visibly does nothing,
  // which is a far worse failure than the leak the guard exists to close.
  // Caught by `web/tests/components/userSettings.test.ts` the first time.
  paint(mode);
  // ...and on the account, debounced. A failed write leaves this device
  // exactly as it was before the account copy existed.
  savePref('theme', mode);
}

/**
 * The account's light/dark choice has arrived: paint it, do not send it back.
 *
 * ⚠ Echoing a hydrated value back to the server turns a read into a write,
 * and every open tab into a writer. Separate function, deliberately.
 */
export function applyAccountTheme(mode: string | undefined | null): void {
  if (mode !== 'light' && mode !== 'dark' && mode !== 'auto') return;
  try {
    localStorage.setItem(KEY, mode);
  } catch {
    /* a display preference is never worth taking the page down for */
  }
  // ⚠ `paint(mode)` for the same reason as `setStoredTheme`: this only ever
  // runs with a session (App.vue asks first), and it paints what the ACCOUNT
  // said rather than re-deriving it through a guard that could disagree.
  paint(mode);
}

function isSystemDark(): boolean {
  return window.matchMedia?.('(prefers-color-scheme: dark)').matches ?? false;
}

function resolveMode(mode: ThemeMode): 'light' | 'dark' {
  if (mode === 'auto') return isSystemDark() ? 'dark' : 'light';
  return mode;
}

const painted = ref<'light' | 'dark'>('light');

/**
 * The mode this window is painted in RIGHT NOW, and every time it changes —
 * the settings switch, the account's answer arriving, the operating system
 * turning in `auto`.
 *
 * ⚠⚠ Hand a page THIS, never `computed(() => effectiveTheme())`. That
 * computed reads localStorage and matchMedia, which Vue cannot track, so it
 * is evaluated once and the page keeps the mode it was opened in: an app's
 * screen stayed dark on a window that had turned light (#57, measured —
 * `web/tests/lib/liveTheme.test.ts` refuses the pattern). It is written by
 * `paint()` below, the one writer of `<html class="dark">`, so it cannot
 * disagree with what is on the screen and needs no observer.
 */
export const liveTheme: Readonly<Ref<'light' | 'dark'>> = readonly(painted);

/** Put a mode on the document. The one place `<html class="dark">` is written. */
function paint(mode: ThemeMode): void {
  const dark = resolveMode(mode) === 'dark';
  document.documentElement.classList.toggle('dark', dark);
  document.documentElement.style.colorScheme = dark ? 'dark' : 'light';
  painted.value = dark ? 'dark' : 'light';
}

export function effectiveTheme(): 'light' | 'dark' {
  return resolveMode(getStoredTheme());
}

/**
 * Paint whatever this window SHOULD be in right now.
 *
 * ⚠ Through `getStoredTheme()`, so with no session it resolves the browser's
 * `prefers-color-scheme` rather than the last person's choice. Callers that
 * already hold the mode paint it directly — see `setStoredTheme`.
 */
export function applyStoredTheme(): void {
  paint(getStoredTheme());
}

// Until the first paint, what the first paint will be.
if (typeof window !== 'undefined') {
  try {
    painted.value = effectiveTheme();
  } catch {
    /* no storage, no matchMedia: light, as above */
  }
}

// React to OS changes when in 'auto' mode.
if (typeof window !== 'undefined' && window.matchMedia) {
  window.matchMedia('(prefers-color-scheme: dark)').addEventListener('change', () => {
    if (getStoredTheme() === 'auto') applyStoredTheme();
  });
}
