/**
 * Short links beside filex's own links (filex 0.60, docs/SHARING.md → Short
 * links): the two small facts every surface that offers "Get short link"
 * needs, written once.
 *
 * ⚠ A short address may hold emoji or letters outside ASCII. The server
 * answers it percent-encoded (`https://l.example.com/%F0%9F%98%80…`), and that
 * spelling is what is COPIED and mailed: a messenger that would stop a link at
 * the first emoji sees one unbroken address. A screen shows it decoded, so a
 * person reads the emoji they will be sent.
 */

/** The token of a public link from its address (`…/s/<token>`, `…/d/<token>`),
 *  "" when the address is not one. The listing names a link by its numeric
 *  id; the short-link routes name it by its token, which is the address's
 *  last segment. */
export function linkTokenOf(url: string | null | undefined): string {
  if (!url) return '';
  let path = url;
  try {
    path = new URL(url, 'http://local.invalid').pathname;
  } catch {
    return '';
  }
  const m = /\/[sd]\/([A-Za-z0-9_-]+)\/?$/.exec(path);
  return m ? m[1] : '';
}

/** The address as a person reads it: percent escapes decoded where that is
 *  safe (decodeURI keeps `/ ? #` and friends escaped), the address as it came
 *  when it cannot be decoded. */
export function displayLink(url: string | null | undefined): string {
  if (!url) return '';
  try {
    return decodeURI(url);
  } catch {
    return url;
  }
}
