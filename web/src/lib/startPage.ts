// Which page a person lands on when they open filex.
//
// The owner's decision, 2026-09-12: selectable, rather than one destination
// forced on everybody — and with NO stored choice the answer is Home, for
// everybody, admins included. The dashboard is not a landing page; it is one
// of the things you can go to, from the admin button in the header cluster.
// An operator who would rather open it every time says so in their profile
// settings, where this control already lives, and that choice is honoured.
//
// ⚠ This replaced a per-DOOR default (`/admin/` → dashboard, `/drive/` →
// Home), which made the product open on two different screens depending on
// which URL somebody had bookmarked.
//
// ⚠⚠ THE READER IS `router/index.ts`, and it is the whole point. Five fields
// were taken OUT of the settings modal for having no reader (its header note
// lists them); a "start page" that the router ignores would be the sixth and
// the most visible, because the user is told the answer every single time they
// open the product.
//
// Persistence is localStorage, like theme / palette / density and unlike
// locale / time zone. Not an oversight: there is no `users.start_page` column,
// and — more to the point — this preference has to be answered SYNCHRONOUSLY,
// inside the navigation guard, on the very first paint. Anything that has to
// wait for `/api/auth/me` would land the person on the old destination and
// then move them, which is the flash this is supposed to prevent.

export const START_PAGE_KEY = 'filex.startpage';

/**
 * `home`  — the end-user front door (drives, recents, starred).
 * `files` — straight into the explorer.
 * `admin` — the admin dashboard. Offered ONLY to admins, and never honoured
 *           for anyone else (see `startRouteName`).
 */
export type StartPage = 'home' | 'files' | 'admin';

const VALUES: StartPage[] = ['home', 'files', 'admin'];

/** The stored choice, or null when this person has never made one. */
export function getStartPage(): StartPage | null {
  try {
    const v = localStorage.getItem(START_PAGE_KEY);
    return v && (VALUES as string[]).includes(v) ? (v as StartPage) : null;
  } catch {
    // private mode / blocked site data — no stored choice is the same answer
    // as "never chose", and the defaults below are the current behaviour.
    return null;
  }
}

/** `null` clears the choice and restores the per-door default. */
export function setStartPage(v: StartPage | null): void {
  try {
    if (v === null) localStorage.removeItem(START_PAGE_KEY);
    else localStorage.setItem(START_PAGE_KEY, v);
  } catch {
    /* a landing preference is never worth taking the page down for */
  }
}

/**
 * The route the front door should open, given who is asking.
 *
 * ⚠ A saved choice must never trap anybody. An account that loses its admin
 * role still has `admin` in this browser's localStorage; honouring it would
 * send them to a route the guard immediately bounces, and a person whose every
 * launch bounced would have no way to guess that a setting three panes deep
 * was the cause. It silently degrades to Home instead — and because the option
 * is not even rendered for a non-admin, the only way to be in this state is to
 * have been demoted since.
 *
 * ⚠ With no stored choice the answer is Home — whoever is asking and whichever
 * door they came through. `userBase` is still taken because the caller has it
 * and the signature is read by the router's guard, but nothing below branches
 * on it any more: a default that depended on the URL prefix meant the same
 * account landed somewhere else depending on which link it had saved.
 */
export function startRouteName(opts: { isAdmin: boolean; userBase: boolean }): string {
  const choice = getStartPage();
  if (choice === 'files') return 'explore';
  if (choice === 'home') return 'home';
  if (choice === 'admin') return opts.isAdmin ? 'dashboard' : 'home';
  return 'home';
}
