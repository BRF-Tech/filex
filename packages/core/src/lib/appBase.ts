/**
 * appBase — the path prefix the filex web app is served under.
 *
 * filex can run under a sub-path behind a reverse proxy
 * (`https://example.com/filex/`, FILEX_BASE_PATH on the server). The web app
 * is built once and runs at the root and under any prefix: the server tells
 * the document which one it is in by adding
 *
 *     <meta name="filex-base" content="/filex">
 *
 * to the index.html it serves (backend/internal/api/spa_shell.go). At the
 * root there is no tag, and everything here answers `''` — the address the
 * app always used.
 *
 * ⚠⚠ The ONE reader of that tag. The admin/drive app, the explorer and every
 * default below ask here; a second reader is how one of them ends up still
 * talking to the host's root.
 *
 * ⚠ It is the base of the DOCUMENT, not of the API. An explorer embedded in
 * another application has no such tag (the host page is not filex), so its
 * defaults stay what they were and it keeps using the `apiBase` its host
 * configured. The desktop app loads its own pages and has none either.
 */

/** The meta tag's name. Mirrors spa_shell.go. */
export const APP_BASE_META = 'filex-base';

/**
 * What a base may look like: `/seg[/seg…]` with the URL's unreserved
 * characters, and no `.`/`..` segment. The same rule the server validates
 * FILEX_BASE_PATH with (backend/internal/basepath.Normalize), repeated here so
 * a tag that did not come from filex cannot aim the app somewhere else.
 */
const BASE_RE = /^(\/[A-Za-z0-9_~.-]+)+$/;

/** Reads the base from a document; `''` when it has none, or a bad one. */
export function readAppBase(doc?: Pick<Document, 'querySelector'> | null): string {
  const d = doc ?? (typeof document !== 'undefined' ? document : null);
  if (!d) return '';
  let raw = '';
  try {
    raw = d.querySelector(`meta[name="${APP_BASE_META}"]`)?.getAttribute('content')?.trim() ?? '';
  } catch {
    return '';
  }
  if (!raw || !BASE_RE.test(raw)) return '';
  if (raw.split('/').some((seg) => seg === '.' || seg === '..')) return '';
  return raw;
}

/** The base this document was served under: `''` at the root, else `/filex`. */
export function appBase(): string {
  return readAppBase();
}

/**
 * An application path (`/files/edit`, `/api/files/archive/list`) as the
 * browser has to ask for it from this document: with the base in front.
 */
export function withAppBase(path: string): string {
  return appBase() + path;
}

/**
 * A path relative to the SERVER ROOT, joined onto an API base, keeping the
 * base's own path.
 *
 * The server hands out some addresses relative to its root — a thumbnail's
 * `thumb_url`, a selection archive's `/z/<ticket>` — because it cannot know
 * where its reader sees it: the admin app at `/filex`, the desktop app at
 * `https://example.com/filex`, an embed through its host's proxy at
 * `https://host.example/files-proxy`. The reader knows, and its API base
 * (`apiBase`, the server root WITHOUT `/api`) is that answer.
 *
 * ⚠ Not `new URL(path, apiBase)`: a root-relative path resolved that way drops
 * the base's path, which is precisely the prefix a sub-path deployment or a
 * proxying host lives under.
 *
 * An absolute `path` is returned unchanged; an empty base leaves the path
 * relative to the page's own origin (same-origin at the root, as before).
 */
export function underApiBase(apiBase: string | null | undefined, path: string): string {
  if (/^[a-z][a-z0-9+.-]*:\/\//i.test(path)) return path;
  const base = String(apiBase ?? '').replace(/\/+$/, '');
  if (!base || !path.startsWith('/')) return path;
  return base + path;
}

/**
 * The API root a module-level default talks to: the one its caller gave, else
 * this document's base path — `''` at the root, so an unconfigured caller
 * behaves exactly as before, and `/filex` for the admin app under a sub-path.
 *
 * ⚠ For the modules that fetch on their own at boot (account preferences,
 * the language list, the public branding) and are configured with no base by
 * the admin app: an unset base used to mean "the host's root".
 */
export function apiRootOr(base: string | null | undefined): string {
  return String(base ?? appBase()).replace(/\/+$/, '');
}
