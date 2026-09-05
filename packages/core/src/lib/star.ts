/**
 * star.ts — the ONE place that talks to `POST /api/files/manager/star`.
 *
 * Starring grew a second surface (the context menu, then the grid/gallery
 * cards) and a menu entry cannot render a component, so the request had to
 * come out of `StarButton.vue` — but only ONCE. `StarButton` calls this, the
 * explorer's menu action calls this, and there is no third copy of the URL,
 * the credentials rule or the payload shape anywhere.
 *
 * ⚠ `credentials` defaults to 'same-origin', never 'include': a credentialed
 * cross-origin request may not be answered with `Access-Control-Allow-Origin:
 * *`, which is what filex sends — hardcoding 'include' silently broke starring
 * in every embed served from a different origin to the API (the desktop app is
 * one). Same trap as `loadStarred`/`fetchNavRows` in FileExplorer.
 */

export interface StarRequestOptions {
  apiBase?: string;
  authHeaders?: () => Record<string, string> | Promise<Record<string, string>>;
  authCredentials?: RequestCredentials;
}

/** Toggle the starred flag for ONE node. Throws on a non-2xx answer so the
 *  caller can roll its optimistic state back. */
export async function setNodeStarred(
  nodeId: number,
  starred: boolean,
  opts: StarRequestOptions = {},
): Promise<void> {
  const headers = {
    'Content-Type': 'application/json',
    ...(await (opts.authHeaders ?? (() => ({})))()),
  };
  const base = opts.apiBase ?? '';
  const res = await fetch(`${base}/api/files/manager/star`, {
    method: 'POST',
    headers,
    credentials: opts.authCredentials ?? 'same-origin',
    body: JSON.stringify({ node_id: nodeId, starred }),
  });
  if (!res.ok) throw new Error(`star toggle failed: ${res.status}`);
}
