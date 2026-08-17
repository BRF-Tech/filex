// How this SPA authenticates the shared filex components.
//
// The explorer page, the connections page and the "how to connect" overlay
// all mount components from `@brftech/filex-core`, and all three need the
// same answer to "who is calling?". It was written out twice already (two
// copies of readCsrfCookie/readBearerToken in Explore.vue); a third copy is
// how one of them ends up not sending the token.

import type { AuthConfig } from '@brftech/filex-core';

export function readCsrfCookie(): string | null {
  const prefix = 'filex_csrf=';
  for (const part of document.cookie.split(';')) {
    const trimmed = part.trim();
    if (trimmed.startsWith(prefix)) return decodeURIComponent(trimmed.slice(prefix.length));
  }
  return null;
}

export function readBearerToken(): string | null {
  return sessionStorage.getItem('filex.bearer');
}

/**
 * The auth block for a core component.
 *
 * Bearer first (the Electron shell and the demo flow store one), CSRF
 * cookie second (a normal browser session), `none` last — which is honest:
 * the component then shows the server's 401 rather than pretending.
 */
export function explorerAuth(): AuthConfig {
  const bearer = readBearerToken();
  if (bearer) return { kind: 'bearer', token: bearer };
  const csrf = readCsrfCookie();
  if (csrf) return { kind: 'csrf', csrf };
  return { kind: 'none' };
}
