/**
 * productVersion — the one line that says which filex this is: `filex 0.43.0`.
 *
 * The maintainer, 2026-09-24: the version should be somewhere a PERSON can find it —
 * the account menu, or user settings. It was only on the sign-in page (twice,
 * each written out by hand) and on the administrators' About page, so anybody
 * who is not an administrator and is already signed in had no way to say
 * which version they were looking at.
 *
 * ⚠⚠ NO CATALOGUE KEY, on purpose. A product name and a version number need
 * no translation, and a new key would drop every language pack below 100%
 * and put "99% translated" into the README's picture of the Apps list
 * (lesson #417). `filex` is the SOFTWARE's name, not the operator's brand:
 * the line answers "which filex is this", which a renamed instance is too.
 *
 * ⚠ The SERVER's own release (`GET /api/files/capabilities` → `release`, the
 * release alone; 0.54, #211 audit A11 - the commit and the build time are
 * `commit` and `built` beside it, and nothing parses the one-line `version`
 * any more): a development build says `0.1.0-dev`, and that is the honest
 * answer. A server older than 0.54 sends no `release`, and the line is not
 * drawn: unknown is said by saying nothing. The
 * one value it will not print is the client's own placeholder, `0.0.0` —
 * the capabilities store holds it until the real
 * answer lands, and a menu opened in that moment would otherwise announce a
 * version that has never existed. Unknown is said by saying nothing.
 */

/** The software's name, as every version line spells it. */
export const PRODUCT_NAME = 'filex';

/** The client's "not known yet" — never a real server's answer. */
const PLACEHOLDER = '0.0.0';

/** A commit hash as git shows it short: seven characters. */
export function shortCommit(commit: string): string {
  return /^[0-9a-f]{8,}$/i.test(commit) ? commit.slice(0, 7) : commit;
}

/** `filex 0.43.0` from the server's `release`, or '' while it is not known. */
export function productVersionLine(release: string | null | undefined): string {
  const r = String(release ?? '').trim();
  if (!r || r === PLACEHOLDER) return '';
  return `${PRODUCT_NAME} ${r}`;
}
