import axios, { AxiosError, AxiosInstance } from 'axios';
import type { Router } from 'vue-router';

// Single shared axios instance. Same-origin by default — Vite dev proxy forwards
// /api -> http://localhost:5212; in production both UI and API are served by the
// Go binary so relative paths "just work".
export const api: AxiosInstance = axios.create({
  baseURL: '/api',
  withCredentials: true,
  timeout: 30_000,
  headers: {
    'X-Requested-With': 'XMLHttpRequest',
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
    // Pull a CSRF token if the backend has set the cookie. Express/chi style.
    const csrf = readCookie('filex_csrf');
    if (csrf && config.method && /post|put|patch|delete/i.test(config.method)) {
      config.headers = config.headers ?? {};
      (config.headers as Record<string, string>)['X-CSRF-Token'] = csrf;
    }
    // Allow manually-attached bearer tokens (e.g. from useAuthStore).
    const token = sessionStorage.getItem('filex.bearer');
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
    return (
      err.response?.data?.error ??
      err.response?.data?.message ??
      err.message ??
      fallback
    );
  }
  if (err instanceof Error) return err.message;
  return fallback;
}
