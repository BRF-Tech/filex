// The app's language IS the account's (#191, 2026-10-08).
//
// ⚠⚠ There is no language of the app's own any more. The server says every
// notification - the bell, a push to the person's phone, an email, this app's
// toast - in the language of the person's ACCOUNT, translated at the last
// stop. An app that kept a language of its own (the old Settings → Language:
// System / English / Türkçe, stored in this computer's state file) showed its
// window in one language and the same person's notifications in another. Now
// the window, the tray menu, the file list inside the window and the sync
// engine's messages all follow the account; a language picked in this app's
// Settings is WRITTEN TO THE ACCOUNT (and so changes the web panel too); with
// nobody signed in the app speaks the system's language.
//
// ⚠ No `electron` import, so node:test can drive it (the same boundary as
// src/notifications.ts and src/account-state.ts).

/** The two languages this app's own chrome is drawn in. */
export type UiLocale = 'en' | 'tr';

function primary(tag: string | null | undefined): string {
  return String(tag ?? '').trim().toLowerCase().split(/[-_]/)[0] ?? '';
}

/**
 * The language this app draws its own chrome in, for an account's language
 * (users.locale) and the system's tag (app.getLocale()). The window and the
 * tray speak English and Turkish only, so an account in a language a language
 * pack adds on the server (`es`, `de`...) draws the window in the system's
 * guess - its notifications and the sync engine's messages are still said in
 * the account's own language by the server and the engine. No account (or one
 * whose language was never read): the system's.
 */
export function uiLocaleFor(account: string | null | undefined, system: string): UiLocale {
  const a = primary(account);
  if (a === 'en' || a === 'tr') return a;
  return primary(system) === 'tr' ? 'tr' : 'en';
}

/** users.locale out of `GET /api/auth/me` - '' when the account holds none. */
export function accountLocaleOf(me: unknown): string {
  const user = (me as { user?: { locale?: unknown } } | null | undefined)?.user;
  return typeof user?.locale === 'string' ? user.locale.trim() : '';
}

/**
 * The body for `PUT /api/me/prefs?surface=desktop` that sets the account's
 * language: the surface's document as `GET` answered it, with `locale` set.
 *
 * ⚠ The WHOLE document - the PUT replaces it (backend handlers/userprefs.go),
 * so sending `{locale}` alone would drop every other preference stored for
 * this surface. The server mirrors `locale` into the account (users.locale,
 * mirrorLocale), which is what every surface and every notification reads.
 * `openWith` is the account's, not the document's (the server strips it too).
 */
export function prefsWithLocale(got: unknown, code: string): { prefs: Record<string, unknown> } {
  const raw = (got as { prefs?: unknown } | null | undefined)?.prefs;
  const doc: Record<string, unknown> =
    raw && typeof raw === 'object' && !Array.isArray(raw) ? { ...(raw as Record<string, unknown>) } : {};
  delete doc.openWith;
  doc.locale = code;
  return { prefs: doc };
}

/**
 * An install from before 0.54 may have PINNED a language in this app
 * (state.locale 'en' | 'tr'). The first time its account's language is read,
 * that pin is handed to the account if the account holds none - the person
 * chose it, and it was the language they were reading in - and is forgotten
 * either way: an account that already has a language keeps it. Answers the
 * language to write to the account, or null for none.
 */
export function pinToAdopt(pinned: string | null | undefined, account: string): string | null {
  if (pinned !== 'en' && pinned !== 'tr') return null;
  return account ? null : pinned;
}
