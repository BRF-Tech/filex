// Runtime-adjustable API configuration.
//
// Why this exists: the web deployment serves the SPA and the API from the same
// Go binary, so a hard-coded same-origin `/api` is correct there. The Electron
// shell (Dilim 2) runs the very same bundle from a custom `app://` origin and
// talks to a REMOTE server the user types on the login screen — so the API base
// is unknown at import time and MUST be settable at runtime. The identity story
// differs too: the web app rides the session cookie + `filex.bearer`; Electron
// has no cookie jar and injects a self-service token instead.
//
// The contract that keeps the web build byte-for-byte unchanged:
//   - default base stays '/api'
//   - the bearer falls back to sessionStorage('filex.bearer') exactly as before
//   - nothing here runs unless something calls the setters or injects the global
//
// Electron's preload sets `window.__FILEX_RUNTIME__` BEFORE the bundle boots;
// initRuntimeConfig() (called from main.ts) picks it up. In the plain web build
// that global is never present, so every code path below no-ops to the old
// behaviour.

export interface FilexRuntimeConfig {
  /** Absolute API base, e.g. "https://fm.brf.sh/api". Overrides the '/api' default. */
  apiBaseUrl?: string;
  /** Self-service API token used as `Authorization: Bearer <token>`. */
  bearerToken?: string;
  /**
   * Whether requests carry credentials (cookies). Defaults to true — the web
   * app rides a session cookie and must keep doing so.
   *
   * Electron sets this to FALSE, and that is not a preference: the desktop
   * renderer runs on an `app://` origin, so every call to the server is
   * cross-origin. filex answers `Access-Control-Allow-Origin: *` (the default,
   * and what fm.brf.sh serves today), and the Fetch spec forbids a wildcard
   * origin on a credentialed request — the browser rejects the response, so
   * EVERY API call fails. The desktop app authenticates with a bearer token and
   * has no cookie jar, so dropping credentials costs it nothing and makes the
   * wildcard perfectly legal.
   *
   * The alternative — echoing a specific origin server-side — was rejected: it
   * would force every self-hoster to add `app://…` to FILEX_CORS_ALLOWED_ORIGINS
   * and make the desktop app depend on every server it connects to being
   * configured for it.
   */
  useCredentials?: boolean;
}

declare global {
  interface Window {
    __FILEX_RUNTIME__?: FilexRuntimeConfig;
  }
}

/** The same-origin default. Keeping this as the fallback is what guarantees the
 *  web deployment's behaviour never changes. */
export const DEFAULT_API_BASE = '/api';

/** Cookies ride along by default — that is the web app's identity. */
export const DEFAULT_USE_CREDENTIALS = true;

let apiBaseUrl: string = DEFAULT_API_BASE;
let bearerToken: string | null = null;
let useCredentials: boolean = DEFAULT_USE_CREDENTIALS;

/**
 * Bootstrap from an injected global. Idempotent and safe to call in any
 * environment: if `window.__FILEX_RUNTIME__` is absent (the web build), the
 * defaults are left untouched. Call once, early, before the first request.
 */
export function initRuntimeConfig(): void {
  const injected = typeof window !== 'undefined' ? window.__FILEX_RUNTIME__ : undefined;
  if (injected?.apiBaseUrl) setApiBaseUrl(injected.apiBaseUrl);
  if (injected?.bearerToken) setBearerToken(injected.bearerToken);
  // Explicit false only: an absent field must not silently flip the web app.
  if (typeof injected?.useCredentials === 'boolean') setUseCredentials(injected.useCredentials);
}

/** The base every request should use, read at REQUEST time (not import time). */
export function getApiBaseUrl(): string {
  return apiBaseUrl;
}

/** Set the API base at runtime (login screen server field). Empty/blank resets
 *  to the same-origin default so a cleared field can never brick requests. */
export function setApiBaseUrl(url: string | null | undefined): void {
  apiBaseUrl = url && url.trim() ? url.trim() : DEFAULT_API_BASE;
}

/**
 * The bearer token for the current request, resolved at REQUEST time.
 * Precedence: an explicitly-set runtime token (Electron) wins; otherwise fall
 * back to the web session's sessionStorage token so the web build is unchanged.
 */
export function getBearerToken(): string | null {
  if (bearerToken) return bearerToken;
  if (typeof sessionStorage !== 'undefined') return sessionStorage.getItem('filex.bearer');
  return null;
}

/** Set (or clear, with null) the runtime bearer token. */
export function setBearerToken(token: string | null): void {
  bearerToken = token && token.trim() ? token : null;
}

/** Whether requests should carry cookies, read at REQUEST time. */
export function getUseCredentials(): boolean {
  return useCredentials;
}

/** Set the credentials mode (Electron injects false — see the interface docs). */
export function setUseCredentials(value: boolean): void {
  useCredentials = value;
}
