// A share's public address built by THIS page — the fallback for a row the
// server sent without its `url` (a server older than the page). The server's
// own link wins everywhere it exists: it is built from the configured public
// origin, and the page's address can be one nobody else can open (issue #32).
//
// ⚠ Under the base path the app is served at (FILEX_BASE_PATH), like every
// address this app builds for the browser: `/filex/s/<token>`, not `/s/…`.
import { appBase, shareHref } from '@brftech/filex-core';

export function fallbackShareUrl(token: string): string {
  const path = shareHref(token, appBase());
  return typeof window === 'undefined' ? path : `${window.location.origin}${path}`;
}
