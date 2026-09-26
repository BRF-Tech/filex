/**
 * The base path the run serves filex under (`node e2e/run.mjs local
 * --base-path /filex`, FILEX_BASE_PATH), '' at the root.
 *
 * The specs address filex with root-relative paths and the sub-path proxy
 * carries those into the base (e2e/lib/subpath-proxy.mjs). Two kinds of spec
 * have to say the base themselves:
 *
 *   - one that asserts an address EXACTLY (a pathname, a Location header) —
 *     the product puts the base in front, correctly;
 *   - one that refuses redirects (`maxRedirects: 0`) — it is measuring the
 *     answer to that very request, not reaching it through the proxy's 307.
 */
export const BASE_PATH = process.env.E2E_BASE_PATH ?? '';

/** An application path (`/s/<token>`) as the server under test answers it. */
export function underBase(path: string): string {
  return `${BASE_PATH}${path}`;
}
