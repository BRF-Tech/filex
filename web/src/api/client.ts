import axios, { AxiosError, AxiosInstance } from 'axios';
import type { Router } from 'vue-router';
import {
  DEFAULT_API_BASE,
  getApiBaseUrl,
  getBearerToken,
  getUseCredentials,
} from './runtimeConfig';

// Single shared axios instance. Same-origin by default — Vite dev proxy forwards
// /api -> http://localhost:5212; in production both UI and API are served by the
// Go binary so relative paths "just work". The baseURL here is only the boot
// default; the request interceptor re-reads it from runtimeConfig on every call
// so the Electron shell can point at a remote server chosen at login time
// (see runtimeConfig.ts). The web build never overrides it, so it stays '/api'.
export const api: AxiosInstance = axios.create({
  baseURL: DEFAULT_API_BASE,
  withCredentials: true,
  timeout: 30_000,
  headers: {
    // NO 'X-Requested-With'. The backend never reads it (grep: zero hits), and
    // it is not a CORS-safelisted header — so on any cross-origin call the
    // browser must ask for it in the preflight, and a server whose
    // AllowedHeaders list does not mention it fails the ENTIRE preflight with
    // no Access-Control-Allow-Origin at all. filex's own default allowlist is
    // Authorization / Content-Type / X-Filex-Pin, so the desktop shell (which
    // is cross-origin by construction, on app://) could not reach ANY server.
    //
    // Removing it here rather than widening the server's allowlist is
    // deliberate: the desktop app connects to arbitrary self-hosted servers and
    // must not require every one of them to be upgraded first.
    Accept: 'application/json',
  },
});

interface InterceptorOpts {
  router: Router;
  onUnauthorized?: () => void;
  onError?: (msg: string) => void;
}

let interceptorsInstalled = false;

export function installAxiosInterceptors(opts: InterceptorOpts): void {
  if (interceptorsInstalled) return;
  interceptorsInstalled = true;

  api.interceptors.request.use((config) => {
    // Re-read the API base per request so a runtime override (Electron pointing
    // at a remote server) takes effect without rebuilding the instance. Defaults
    // to '/api' in the web build, so same-origin behaviour is unchanged.
    config.baseURL = getApiBaseUrl();
    // Same story for credentials: the web build keeps sending cookies, while
    // Electron turns them off because a credentialed request cannot legally be
    // answered with `Access-Control-Allow-Origin: *` — which is what filex
    // serves by default, so every cross-origin call would be rejected.
    config.withCredentials = getUseCredentials();
    // Pull a CSRF token if the backend has set the cookie. Express/chi style.
    const csrf = readCookie('filex_csrf');
    if (csrf && config.method && /post|put|patch|delete/i.test(config.method)) {
      config.headers = config.headers ?? {};
      (config.headers as Record<string, string>)['X-CSRF-Token'] = csrf;
    }
    // Attach the bearer token. Precedence: an injected runtime token (Electron)
    // wins; otherwise fall back to the web session's sessionStorage token, so
    // the web build behaves exactly as before.
    const token = getBearerToken();
    if (token) {
      config.headers = config.headers ?? {};
      (config.headers as Record<string, string>).Authorization = `Bearer ${token}`;
    }
    return config;
  });

  api.interceptors.response.use(
    (r) => r,
    (err: AxiosError<{ error?: string; message?: string }>) => {
      const status = err.response?.status;
      const current = opts.router.currentRoute.value;
      const onLogin = current.name === 'login';
      // During the cold-load initial navigation currentRoute is still the
      // START_LOCATION (nothing matched yet). A 401 here (e.g. the router
      // guard's fetchMe) must NOT push to /login: the guard already routes
      // unauthenticated visitors, and a bare push would race the pending
      // navigation and strip the login page's query params (?local=1,
      // ?error=oidc, ?redirect=…).
      const navigating = current.matched.length === 0;

      if (status === 401 && !onLogin && !navigating) {
        opts.onUnauthorized?.();
      } else if (!err.response) {
        // No HTTP response = network/timeout. Surface globally because no
        // calling code can recover from this. Other statuses (4xx, 5xx) are
        // the caller's problem — they format their own messages.
        opts.onError?.(err.message || 'Network error');
      }
      return Promise.reject(err);
    },
  );
}

function readCookie(name: string): string | null {
  const prefix = `${name}=`;
  for (const part of document.cookie.split(';')) {
    const trimmed = part.trim();
    if (trimmed.startsWith(prefix)) return decodeURIComponent(trimmed.slice(prefix.length));
  }
  return null;
}

export function extractError(err: unknown, fallback = 'Unknown error'): string {
  if (axios.isAxiosError(err)) {
    // ⚠ `message` FIRST when both are present. A response that carries both is
    // one where `error` is a machine code and `message` is the sentence
    // written for the person reading it (`supertenant_only` +
    // "protection settings apply to the whole instance…", `plugins_disabled` +
    // which env var did it). Preferring `error` put the code on screen and
    // threw the sentence away. Handlers that return only `error` — the large
    // majority, and they put the sentence there — are unaffected.
    return (
      err.response?.data?.message ??
      err.response?.data?.error ??
      err.message ??
      fallback
    );
  }
  if (err instanceof Error) return err.message;
  return fallback;
}
