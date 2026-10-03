// The browser half of a sign-in handed to a tenant's own address.
//
// On a multi-tenant install a person may type their realm on the platform's
// sign-in page. When that tenant has an address of its own, the session
// belongs THERE (the cookie is host-bound), so the server answers with a
// one-use ticket instead of a session (handlers.Auth.handOff). This page then
// goes to the tenant's sign-in page with the ticket in the URL FRAGMENT, and
// that page redeems it (POST /api/auth/handoff).
//
// ⚠ The fragment is the point: a browser never sends it to a server, so the
// ticket appears in no access log, no proxy log and no Referer. The receiving
// page removes it from the address bar before it redeems it.

export interface Handoff {
  origin: string;
  code: string;
}

/**
 * The address the browser goes to: the tenant's origin (it carries the base
 * path the server serves filex under), the same front door and query this page
 * has (so `?redirect=` and a desktop authorization survive the move), and the
 * ticket in the fragment. Null when the origin is not an http(s) URL — the
 * answer is the server's, but a page never navigates to a scheme it did not
 * expect.
 *
 * `base` is the base path THIS page is served under (appBase()), which the
 * origin already carries for the tenant.
 */
export function handoffTarget(h: Handoff, loc: { pathname: string; search: string }, base: string): string | null {
  let origin: URL;
  try {
    origin = new URL(h.origin);
  } catch {
    return null;
  }
  if (origin.protocol !== 'https:' && origin.protocol !== 'http:') return null;
  if (!h.code) return null;
  const root = h.origin.replace(/\/+$/, '');
  const path = base && loc.pathname.startsWith(base) ? loc.pathname.slice(base.length) : loc.pathname;
  return `${root}${path || '/'}${loc.search}#handoff=${encodeURIComponent(h.code)}`;
}

/** Leaves this page for `url` (a seam, so a test can watch where it goes). */
export function navigateTo(url: string): void {
  window.location.assign(url);
}

/** The ticket in an address bar fragment (`#handoff=…`), or null. */
export function readHandoffFragment(hash: string): string | null {
  const m = /(?:^#|&)handoff=([^&]+)/.exec(hash || '');
  if (!m) return null;
  try {
    return decodeURIComponent(m[1]);
  } catch {
    return null;
  }
}
