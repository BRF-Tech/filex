/**
 * productVersion — the one line that says which filex this is: `filex 0.43.0`.
 *
 * Burak, 2026-09-24: the version should be somewhere a PERSON can find it —
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
 * ⚠ The SERVER's own string (`GET /api/files/capabilities` → `version`),
 * kept as it is: a development build says `0.1.0-dev`, and that is the
 * honest answer. The one value it will not print is the client's own
 * placeholder, `0.0.0` — the capabilities store holds it until the real
 * answer lands, and a menu opened in that moment would otherwise announce a
 * version that has never existed. Unknown is said by saying nothing.
 */

/** The software's name, as every version line spells it. */
export const PRODUCT_NAME = 'filex';

/** The client's "not known yet" — never a real server's answer. */
const PLACEHOLDER = '0.0.0';

/** `filex 0.43.0`, or '' while the version is not known. */
export function productVersionLine(version: string | null | undefined): string {
  const v = String(version ?? '').trim();
  if (!v || v === PLACEHOLDER) return '';
  return `${PRODUCT_NAME} ${v}`;
}
