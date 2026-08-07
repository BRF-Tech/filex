import { shell } from 'electron';
import crypto from 'node:crypto';

// Desktop half of the browser authorization flow.
//
// The app never asks for a password. It sends the user to their own server in
// the SYSTEM browser and waits to be handed a credential over a `filex://`
// deep link. That is the only way an install behind Keycloak/OIDC — passkeys,
// MFA, corporate SSO — can be signed into at all, and it also means the user
// types their password into their IdP's real page rather than into our window.

export interface PendingAuth {
  state: string;
  verifier: string;
  serverUrl: string;
  /** The URL the browser was sent to. Kept so an automated run can drive the
   *  browser half itself instead of opening a real window on someone's
   *  desktop (see FILEX_NO_BROWSER). */
  authUrl: string;
}

function b64url(buf: Buffer): string {
  return buf.toString('base64url');
}

/** Normalizes what a human types: "fm.brf.sh", "https://fm.brf.sh/", "…/admin". */
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
  return `${u.protocol}//${u.host}`;
}

/** Opens the server's own login page in the system browser and returns the
 *  material needed to finish. */
export function beginBrowserAuth(serverInput: string): PendingAuth {
  const serverUrl = normalizeServerUrl(serverInput);
  const state = b64url(crypto.randomBytes(24));
  const verifier = b64url(crypto.randomBytes(32));
  const challenge = b64url(crypto.createHash('sha256').update(verifier).digest());

  const url = new URL(`${serverUrl}/admin/login`);
  url.searchParams.set('desktop_state', state);
  url.searchParams.set('desktop_challenge', challenge);
  const authUrl = url.toString();
  // FILEX_NO_BROWSER exists so an automated run can exercise this flow without
  // throwing a browser window onto the operator's desktop. It only suppresses
  // the launch — state, verifier and challenge are produced exactly as in a
  // real run, so the test still proves the real thing.
  if (process.env.FILEX_NO_BROWSER !== '1') {
    // The SYSTEM browser, not an embedded window: that is where the user's
    // existing SSO session and passkeys live, and where they can see the real
    // address bar before typing a password.
    void shell.openExternal(authUrl);
  }

  return { state, verifier, serverUrl, authUrl };
}

export interface ExchangeResult {
  token: string;
  email: string;
}

/** Trades the one-time code from the deep link for the durable token. */
export async function exchangeCode(
  pending: PendingAuth,
  state: string,
  code: string,
): Promise<ExchangeResult> {
  if (state !== pending.state) {
    // A deep link whose state does not match the attempt we started is either
    // stale or someone else's; completing it would bind the wrong account.
    throw new Error('authorization does not match the pending request');
  }
  const res = await fetch(`${pending.serverUrl}/api/auth/desktop/exchange`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ state, code, verifier: pending.verifier }),
  });
  if (!res.ok) {
    const body = await res.text().catch(() => '');
    throw new Error(`exchange failed (${res.status})${body ? `: ${body.slice(0, 200)}` : ''}`);
  }
  return (await res.json()) as ExchangeResult;
}

/** Parses `filex://auth?state=…&code=…`. Returns null for anything else so a
 *  stray deep link cannot drive the flow. */
export function parseAuthDeepLink(raw: string): { state: string; code: string } | null {
  try {
    const u = new URL(raw);
    if (u.protocol !== 'filex:') return null;
    if (u.host !== 'auth' && u.pathname.replace(/\//g, '') !== 'auth') return null;
    const state = u.searchParams.get('state');
    const code = u.searchParams.get('code');
    return state && code ? { state, code } : null;
  } catch {
    return null;
  }
}
