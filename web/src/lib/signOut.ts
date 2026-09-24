// Signing out — every "Sign out" in the app goes through here (TopNav, the
// explorer's account menu).
//
// ⚠ An SSO session has two halves: filex's own and the IdP's. Dropping only
// filex's was not signing out. The sign-in page starts SSO by itself
// (FILEX_OIDC_AUTO_REDIRECT), the IdP's session was still open, and the same
// account was back ~0.5 s later without a form (measured on Keycloak 26) —
// nobody could switch accounts, and on a shared computer the next person got
// the previous one's files.
//
// The server now answers with the IdP's end-session URL whenever there is an
// IdP half to end, and the browser goes there; the IdP sends it back to this
// front door's sign-in page with `?signed_out`, which does not start SSO by
// itself. With nothing to end at the IdP (a password session, an IdP without
// RP-initiated logout, FILEX_OIDC_LOGOUT=local) the same page is opened
// directly.
import type { RouteLocationRaw } from 'vue-router';

interface SignOutStore {
  logout(returnTo?: string): Promise<string | null>;
}

interface SignOutRouter {
  options: { history: { base: string } };
  push(to: RouteLocationRaw): Promise<unknown>;
}

/**
 * The sign-in page of the front door this document was served from
 * (router/index.ts mounts on /admin/ or /drive/). The server accepts exactly
 * these two as the place the IdP returns to.
 */
export function signInPage(router: Pick<SignOutRouter, 'options'>): '/admin/login' | '/drive/login' {
  return router.options.history.base.startsWith('/drive') ? '/drive/login' : '/admin/login';
}

export async function signOut(
  auth: SignOutStore,
  router: SignOutRouter,
  go: (url: string) => void = (url) => window.location.assign(url),
): Promise<void> {
  const idpLogout = await auth.logout(signInPage(router));
  if (idpLogout) {
    go(idpLogout);
    return;
  }
  await router.push({ name: 'login', query: { signed_out: '1' } });
}
