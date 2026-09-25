/**
 * ⌘K across accounts — the server calls the main process makes on behalf of
 * an account the window is NOT showing (task #47).
 *
 * The explorer is mounted for one account and is handed no credential for any
 * other (`config.accountSearch` in the shared component). When its palette
 * searches every account on the rail, and when a hit from another account is
 * downloaded, the request is made here, with the token this process keeps for
 * that account. These are the addresses it uses; they are the explorer's own
 * (`/api/files/search`, `/api/files/manager?action=download`), so every account
 * is searched and downloaded exactly the way the mounted one is.
 *
 * Pure on purpose: no electron import, so node:test can drive it.
 */

/** The palette never asks for more than this per account. */
const MAX_LIMIT = 50;
const SCOPES = new Set(['name', 'content', 'all']);

function apiUrl(serverUrl: string, rel: string): URL {
  // Keep a sub-path install's prefix: `new URL('/api/…', base)` would drop it.
  const base = serverUrl.endsWith('/') ? serverUrl : `${serverUrl}/`;
  return new URL(rel, base);
}

export function remoteSearchUrl(
  serverUrl: string,
  query: string,
  opts: { limit?: number; scope?: string } = {},
): string {
  const u = apiUrl(serverUrl, 'api/files/search');
  const limit = Math.max(1, Math.min(MAX_LIMIT, Math.floor(Number(opts.limit) || 8)));
  const scope = SCOPES.has(String(opts.scope)) ? String(opts.scope) : 'all';
  u.searchParams.set('q', String(query ?? ''));
  u.searchParams.set('limit', String(limit));
  u.searchParams.set('scope', scope);
  return u.toString();
}

/**
 * The download address for one hit, addressed as a listing row is
 * (`name://rel`). Anything else is refused: this runs with a credential, and
 * a path the explorer did not produce has no business reaching the server.
 */
export function remoteDownloadUrl(serverUrl: string, remote: string): string {
  if (!/^[^/:]+:\/\/./.test(String(remote ?? ''))) {
    throw new Error('not a storage path');
  }
  const u = apiUrl(serverUrl, 'api/files/manager');
  u.searchParams.set('action', 'download');
  u.searchParams.set('path', remote);
  return u.toString();
}

/** The hits in a `/api/files/search` answer — objects only, never a throw. */
export function searchResults(body: unknown): Array<Record<string, unknown>> {
  const rows = (body as { results?: unknown } | null)?.results;
  if (!Array.isArray(rows)) return [];
  return rows.filter((r): r is Record<string, unknown> => !!r && typeof r === 'object' && !Array.isArray(r));
}
