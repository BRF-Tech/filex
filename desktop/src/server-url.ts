/**
 * Addresses on a filex server, for the main process.
 *
 * ⚠⚠ filex can be served under a sub-path behind a reverse proxy
 * (`https://example.com/filex/`, FILEX_BASE_PATH on the server). An account's
 * `serverUrl` then carries that path, and every address built from it has to
 * keep it. `new URL('/api/files/manager', serverUrl)` does NOT: a root-relative
 * path replaces the base's whole path, so a sub-path server was asked at
 * `https://example.com/api/…` — the host's root, somebody else's server. This
 * module is the one join, used for every request the main process makes.
 *
 * Pure on purpose: no electron import, so node:test can drive it.
 */

/**
 * A path on the server (`/api/files/manager`, `/files/edit`), joined onto the
 * server URL with the server's own path kept.
 */
export function serverUrl(server: string, path: string): URL {
  const base = server.endsWith('/') ? server : `${server}/`;
  return new URL(path.replace(/^\/+/, ''), base);
}

/**
 * True when `url` is on this server's API surface (`<server>/api/…`) — the
 * bytes the app downloads in place rather than handing to a browser.
 */
export function isServerApiUrl(url: string, server: string): boolean {
  try {
    const u = new URL(url);
    const api = serverUrl(server, '/api/');
    return u.origin === api.origin && u.pathname.startsWith(api.pathname);
  } catch {
    return false;
  }
}

/**
 * The first path segments that are filex's own pages and endpoints rather
 * than the prefix it is served under. What a person pastes into the sign-in
 * field is usually the address bar of a page — `…/admin/`, `…/drive/explore`,
 * a share `…/s/<token>` — and everything from the first of these on is not
 * part of the server's address.
 *
 * ⚠ So a server whose base path is itself one of these names (FILEX_BASE_PATH
 * =/drive) cannot be told apart from the page `/drive/` of a server at the
 * root; docs/DEPLOYMENT.md asks for a base that is not one of them.
 */
const APP_SEGMENTS = new Set(['admin', 'drive', 'files', 'api', 'dav', 's3', 's', 'd', 'u', 'z', 'p', 'embed', 'embed.js']);

/** The server's own path in a pasted address: `/filex` of `/filex/admin/login`. */
export function serverBasePath(pathname: string): string {
  const segs = pathname.split('/').filter(Boolean);
  const cut = segs.findIndex((s) => APP_SEGMENTS.has(s));
  const own = cut < 0 ? segs : segs.slice(0, cut);
  return own.length ? `/${own.join('/')}` : '';
}

/**
 * Normalizes what a human types: "fm.example.com", "https://fm.example.com/", "…/admin".
 *
 * ⚠ The server's own path is KEPT: filex can be served under a sub-path
 * (`https://example.com/filex`, FILEX_BASE_PATH), and dropping it pointed the
 * app at the host's root. Only the part that names one of filex's pages
 * (`/admin/login`, `/drive/explore`, …) goes — see serverBasePath.
 */
export function normalizeServerUrl(input: string): string {
  let s = (input || '').trim();
  if (!s) throw new Error('server address required');
  if (!/^https?:\/\//i.test(s)) s = `https://${s}`;
  const u = new URL(s);
  // Defaulting to https matters: a bare host typed into a desktop app must not
  // silently become a cleartext session carrying a durable token.
  if (u.protocol !== 'https:' && u.hostname !== 'localhost' && u.hostname !== '127.0.0.1') {
    throw new Error('server must be https (localhost excepted)');
  }
  return `${u.protocol}//${u.host}${serverBasePath(u.pathname)}`;
}
