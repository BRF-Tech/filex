// Browser half of the desktop authorization hand-off.
//
// The desktop app cannot show a login form of its own without cutting off every
// install that authenticates through an IdP. So it opens THIS app in the
// browser with two parameters and waits. Once the user is signed in — by
// whatever means this server supports — we mint the desktop's token and hand
// back a one-time code over a `filex://` deep link.
//
// ⚠ The parameters are stashed in sessionStorage rather than read straight off
// the URL at completion time, because the OIDC round-trip DESTROYS the query:
// the SPA bounces to the IdP and the backend callback lands the browser on
// /admin/ with a clean URL. Stashing on arrival is what makes the SSO path work
// at all — which is the entire reason this flow exists.

import { api } from '../api/client';

const KEY = 'filex.desktopHandoff';

interface Handoff {
  state: string;
  challenge: string;
}

/** Called on the login screen: remember a desktop authorization if one is in
 *  flight. No-op for ordinary web visitors. */
export function stashDesktopHandoff(state?: unknown, challenge?: unknown): void {
  if (typeof state !== 'string' || typeof challenge !== 'string') return;
  if (!state || !challenge) return;
  try {
    sessionStorage.setItem(KEY, JSON.stringify({ state, challenge } satisfies Handoff));
  } catch {
    /* private mode — the hand-off simply won't complete, and the app says so */
  }
}

export function hasPendingHandoff(): boolean {
  try {
    return sessionStorage.getItem(KEY) !== null;
  } catch {
    return false;
  }
}

/**
 * Called once the browser session is authenticated. Mints the token server-side
 * and navigates to the deep link.
 *
 * Returns the one-time code when a hand-off was completed (null when there was
 * none). The caller SHOWS that code: the deep link may silently do nothing, and
 * the code is the manual way back into the app.
 */
export async function completeDesktopHandoff(): Promise<string | null> {
  let raw: string | null = null;
  try {
    raw = sessionStorage.getItem(KEY);
  } catch {
    return null;
  }
  if (!raw) return null;
  // Clear FIRST: a failed attempt must not leave a stash that re-fires on every
  // later navigation.
  try {
    sessionStorage.removeItem(KEY);
  } catch {
    /* ignore */
  }

  const { state, challenge } = JSON.parse(raw) as Handoff;
  const { data } = await api.post('/auth/desktop/complete', {
    state,
    challenge,
    label: `filex desktop — ${navigator.platform || 'unknown'}`,
  });
  // Only the code travels in the URL; the token stays on the wire. The desktop
  // exchanges it using a verifier that never left that process.
  const code = data.code as string;
  // Try the deep link. There is no reliable way to detect that it failed — the
  // browser simply does nothing when no handler is registered — so the caller
  // ALSO shows the code. A user whose OS never registered the filex:// scheme
  // (portable browser, locked-down machine, or they finished sign-in on a
  // phone) can then type it into the app's waiting screen by hand.
  window.location.href = `filex://auth?state=${encodeURIComponent(state)}&code=${encodeURIComponent(code)}`;
  return code;
}
