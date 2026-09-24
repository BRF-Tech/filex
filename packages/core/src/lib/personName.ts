/**
 * personName — how filex names a person on screen. ONE rule, every surface.
 *
 * ⚠⚠ QA, 2026-09-21: one administrator was "admin2" in the explorer's Owner
 * column, "admin@local" in notifications, the permissions panel and the admin
 * tables, and a full name on signatures. Each place had its own chain —
 * display name → username → e-mail (the Owner column, on the server), display
 * name → e-mail (the share dialog, the details panel, the admin header),
 * display name → e-mail local part (the presence strip). One chain now:
 *
 *   the name the server already worked out (`name` — a row's `owner_name`,
 *   `user_name`, `creator_name`: model.PersonLabel on the server, the same
 *   rule) → the display name → the username → the e-mail address.
 *
 * The server's twin is `model.PersonLabel` (backend/internal/model/user.go);
 * web/tests/lib/personName.test.ts and the Go test hold both to the same
 * cases. docs/CONTRIBUTING.md → "A person is named one way". The e-mail is a
 * second line or a tooltip where it helps — never the name while a name exists.
 */
export interface PersonLike {
  /** A label the server already resolved by the same rule. */
  name?: string | null;
  display_name?: string | null;
  username?: string | null;
  email?: string | null;
}

export function personName(p: PersonLike | null | undefined): string {
  if (!p) return '';
  for (const v of [p.name, p.display_name, p.username, p.email]) {
    const s = typeof v === 'string' ? v.trim() : '';
    if (s) return s;
  }
  return '';
}

/**
 * The letter for a person's avatar circle: the first character of their name
 * — a whole character (an emoji or an astral letter is a surrogate PAIR, and
 * one half of it prints as �), upper-cased in the viewer's language (Turkish
 * `i` → `İ`, never `I`). '' when there is no name; the caller draws a glyph.
 */
export function personInitial(p: PersonLike | null | undefined, tag?: string): string {
  const n = personName(p);
  if (!n) return '';
  return [...n][0].toLocaleUpperCase(tag);
}
