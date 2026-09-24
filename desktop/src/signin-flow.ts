// What the sign-in window shows — decided in the main process, never in the page.
//
// ⚠ No `electron` import, so node:test can drive it (the same boundary as
// src/account-state.ts and src/notifications.ts).
//
// ⚠⚠ Issue #36: "after a failed login the app drops to the first stage, where
// you type the server address, skipping the option to enter the code — which
// makes it impossible to log in." The attempt was never lost: the main process
// keeps it until an exchange SUCCEEDS, precisely so a failed one can be tried
// again. What was lost was the PAGE's memory of it. The waiting screen (the
// address to copy, the code box) lived in a variable of the sign-in page, and
// every path that touched the window reloaded that page:
//
//   - a sign-in link that failed (a stale one from an earlier try, a code the
//     server refused) re-opened the window on `#/connect` — the URL it was
//     already on, so a reload;
//   - clicking the tray icon, the Dock icon, or starting the app again while
//     it waited (`route()`) did exactly the same.
//
// The page took the attempt back only on `#/reconnect`. Everywhere else it came
// up on the server-address form, with the attempt still pending behind it and
// no way to reach it.
//
// So the page no longer decides. It asks for `signIn` and draws it: while an
// attempt is pending, the waiting screen for THAT attempt — whatever route or
// reload brought the page up — with the last failure said on it. The server
// form comes back only when nothing is pending: before the first attempt, or
// after the person pressed Cancel (which now ends the attempt here too).

export interface SignInAttempt {
  serverUrl: string;
  authUrl: string;
}

/**
 * Why the last hand-back failed, as far as the next step is concerned.
 *
 * ⚠ `stale` and `spent` need different advice, and the difference is on the
 * server: a link from an EARLIER attempt is refused here, before the server is
 * asked, and leaves the current attempt intact; an exchange the server
 * refused has used the attempt up — it deletes a pending authorization on any
 * exchange, so a code cannot be guessed by repeating — and only a new one in
 * the browser can finish.
 */
export interface SignInFailure {
  kind: 'stale' | 'spent';
  detail: string;
}

export type SignInView =
  | { view: 'waiting'; serverUrl: string; authUrl: string; failure: SignInFailure | null }
  | { view: 'connect'; failure: SignInFailure | null };

export function signInView(pending: SignInAttempt | null, failure: SignInFailure | null): SignInView {
  if (pending) {
    return { view: 'waiting', serverUrl: pending.serverUrl, authUrl: pending.authUrl, failure };
  }
  return { view: 'connect', failure };
}

/** A hand-back that failed: stale when it names an attempt other than the one
 *  pending (or none is), spent otherwise. */
export function failureOf(pendingState: string | null, linkState: string, err: unknown): SignInFailure {
  const detail = String((err as Error)?.message ?? err);
  return { kind: pendingState !== null && pendingState === linkState ? 'spent' : 'stale', detail };
}
