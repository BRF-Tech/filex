/**
 * sessionGone - what the panel does with a 401 (api/client.ts calls it from
 * its response interceptor, main.ts wires it): ask the server whether the
 * session is over, and only then go to the sign-in page, naming the page the
 * reader was on.
 *
 * ⚠⚠ Asked FIRST, navigated after (task #199). The panel used to push to the
 * sign-in page the moment a 401 arrived, while it still believed in the
 * session; the sign-in page's guard (router/index.ts) sends a signed-in
 * reader on to the start page, so the reader was bounced to Home on the way.
 * Home's own requests then answered 401 and pushed a second sign-in, named
 * `?redirect=/home`, which cancelled the one the page had asked for. Measured
 * on GitHub's full matrix (e2e 202, Chromium, runs 37598598805, 37661356185,
 * 37702037032): a store link opened in a tab whose session had ended came
 * back to Home after the sign-in, and the link was lost. Asking the server
 * also clears what the panel believed (stores/auth fetchMe), so the guard lets
 * the reader stay on the sign-in page.
 *
 * ⚠ A 401 is not always a session that ended: a wrong current password or
 * one-time code answers 401 too (handlers/auth_self.go). With the session
 * alive the reader stays where they are, and the page says what was refused.
 *
 * ⚠ One question at a time. The answer to it is itself a 401 when the session
 * is over (GET /api/auth/me), and every request the page had in flight
 * answers 401 as well: those join the question already asked instead of
 * asking again, which would never end.
 */
import type { Router } from 'vue-router';
import { dropStoreFragment } from './storeLink';

export interface SessionGoneDeps {
  router: Router;
  /** Who is signed in, asked of the server; null when nobody (stores/auth fetchMe). */
  askSession: () => Promise<unknown>;
}

/** Where the sign-in comes back to: the page the reader is on now. */
export function signInReturn(router: Router): string {
  // On a cold-load deep link vue-router already carries the #<folder> hash in
  // fullPath; after in-app navigation the explorer writes it via
  // replaceState behind the router's back - append it only in that case or
  // the hash doubles up.
  const current = router.currentRoute.value;
  let redirect = current.fullPath;
  if (!current.hash && window.location.hash) redirect += window.location.hash;
  // ⚠ Never a store link's token: it waits in this tab (lib/storeLink) and the
  // store page reads it after the sign-in. In `?redirect=` it would be in the
  // sign-in address, its history entry and an SSO's return address (store fe
  // review #2).
  return dropStoreFragment(redirect);
}

/**
 * The handler for api/client.ts's `onUnauthorized`. Resolves when the
 * question is answered and any navigation it started has settled (tests
 * await it; the interceptor does not).
 */
export function unauthorizedHandler(deps: SessionGoneDeps): () => Promise<void> {
  let asking: Promise<void> | null = null;
  return () => {
    if (asking) return asking;
    asking = (async () => {
      if (await deps.askSession()) return;
      // The reader may be on the sign-in page by now (a page that handles its
      // own 401, as the store page does, went there first): it already names
      // where to come back to.
      if (deps.router.currentRoute.value.name === 'login') return;
      const redirect = signInReturn(deps.router);
      await deps.router.push(redirect && redirect !== '/' ? { name: 'login', query: { redirect } } : { name: 'login' });
    })().finally(() => {
      asking = null;
    });
    return asking;
  };
}
