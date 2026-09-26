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
 * ⚠ The SERVER's own release (`GET /api/files/capabilities` → `version`,
 * without the commit and build time it adds — parseServerVersion): a
 * development build says `0.1.0-dev`, and that is the honest answer. The
 * one value it will not print is the client's own placeholder, `0.0.0` —
 * the capabilities store holds it until the real
 * answer lands, and a menu opened in that moment would otherwise announce a
 * version that has never existed. Unknown is said by saying nothing.
 */

/** The software's name, as every version line spells it. */
export const PRODUCT_NAME = 'filex';

/** The client's "not known yet" — never a real server's answer. */
const PLACEHOLDER = '0.0.0';

/** The parts of the server's version string. A part it left out is ''. */
export interface ServerVersion {
  /** `v0.46.0`, `0.1.0-dev` — the release, as the server spells it. */
  release: string;
  /** The full commit hash the build was made from. */
  commit: string;
  /** The build time, as the build stamped it (ISO 8601). */
  built: string;
}

/**
 * `v0.46.0 (a2d7e34d19…, 2026-09-26T03:41:30Z)` → its parts.
 *
 * ⚠ That is the REAL shape of `capabilities.version` (backend
 * internal/version.String): the release, then the commit and the build time
 * in brackets, either of which a build may leave out. Printed whole, the 40
 * hex digits and the timestamp gave the avatar menu a sideways scroll bar and
 * ran off the About page (Burak, 2026-09-26).
 */
export function parseServerVersion(version: string | null | undefined): ServerVersion {
  const v = String(version ?? '').trim();
  const m = /^(\S+)\s*\(([^)]*)\)$/.exec(v);
  if (!m) return { release: v, commit: '', built: '' };
  let commit = '';
  let built = '';
  for (const part of m[2].split(',').map((p) => p.trim())) {
    if (/^\d{4}-\d{2}-\d{2}/.test(part)) built = part;
    else if (part && !commit) commit = part;
  }
  return { release: m[1], commit, built };
}

/** A commit hash as git shows it short: seven characters. */
export function shortCommit(commit: string): string {
  return /^[0-9a-f]{8,}$/i.test(commit) ? commit.slice(0, 7) : commit;
}

/** `filex 0.43.0` — the release only — or '' while the version is not known. */
export function productVersionLine(version: string | null | undefined): string {
  const { release } = parseServerVersion(version);
  if (!release || release === PLACEHOLDER) return '';
  return `${PRODUCT_NAME} ${release}`;
}
