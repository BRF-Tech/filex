import axios, { AxiosError, AxiosInstance } from 'axios';
import type { Router } from 'vue-router';
import {
  foreignText,
  kindOfMethod,
  noteRequestFailed,
  noteRequestSucceeded,
  statusIsTelling,
  statusWords,
} from '@brftech/filex-core';
import { i18n, t } from '@/i18n';
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
    (r) => {
      // The server spoke: whatever the shared notice was standing in for is
      // over (core lib/connection). Recovery costs no request of its own —
      // it rides on the next call the page was going to make anyway.
      noteRequestSucceeded();
      return r;
    },
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

      // An answer arrived — even a refusal. See the success branch: the
      // connection is not what is wrong, so the shared notice must go.
      if (err.response) noteRequestSucceeded();

      if (status === 401 && !onLogin && !navigating) {
        opts.onUnauthorized?.();
      } else if (!err.response) {
        // No HTTP response = network/timeout. Surface globally because no
        // calling code can recover from this. Other statuses (4xx, 5xx) are
        // the caller's problem — they format their own messages.
        //
        // ⚠⚠ ONE NOTICE, NOT ONE PER REQUEST. This used to raise a toast from
        // every answerless refusal, and the explorer alone fires several calls
        // a second (the listing, a thumbnail per row, the bell's 15 s poll,
        // the pending-operations poll) — so a dropped connection filled the
        // corner with copies of one sentence (owner, 2026-09-24). The shared
        // rule in core `lib/connection` folds everything the PAGE started into
        // a single strip and lets everything the PERSON started keep its own
        // message, because a failed upload or rename is something they are
        // waiting on. Read vs write is the whole test (`kindOfMethod`).
        // ⚠ Not `err.message`: axios writes "Network Error" in English.
        if (noteRequestFailed(kindOfMethod(err.config?.method)) === 'say') {
          opts.onError?.(t('errors.network'));
        }
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

export function extractError(err: unknown, fallback?: string): string {
  if (axios.isAxiosError(err)) {
    // ⚠ 423 is an APP LOCK, not an error to print: an app (the signing app,
    // while signatures are collected) froze the file, and the server answers
    // every door with who holds it and why (handlers.lockedAnswer). Its
    // `message` is English and names a path; the person reads the app's name
    // and reason in their own language instead. One place, so every page that
    // shows a refused write says the same thing.
    if (err.response?.status === 423) return lockedText(err.response.data);
    // ⚠ `message` FIRST when both are present. A response that carries both is
    // one where `error` is a machine code and `message` is the sentence
    // written for the person reading it (`supertenant_only` +
    // "protection settings apply to the whole instance…", `plugins_disabled` +
    // which env var did it). Preferring `error` put the code on screen and
    // threw the sentence away. Handlers that return only `error` — the large
    // majority, and they put the sentence there — are unaffected.
    const said = err.response?.data?.message ?? err.response?.data?.error;
    // ⚠⚠ ISOLATED. This sentence was written by the SERVER (`server.*`,
    // internal/srvtext) or by an installed app, so it never passed through
    // vue-i18n and the panel's post-translation hook never saw it — the one
    // place a message reaches an Arabic screen without its machine runs
    // isolated. Measured on `server.token.scope_unknown`, which names the
    // syntax `root:<storage>://<folder>`: the closing `>` took the
    // paragraph's direction, jumped to the far left of the run and mirrored
    // into `<`. A pack cannot fix it — bidi controls are forbidden in a
    // translation — so it is fixed here (core lib/direction `foreignText`).
    if (typeof said === 'string' && said) return foreignText(String(i18n.global.locale.value), said);
    // ⚠ Never axios's own `err.message`: "Network Error" and "Request failed
    // with status code 500" are English plumbing, in every language (wave-2
    // wording sweep). No answer at all is the network; an answer without a
    // sentence is said by its status when the status tells the person
    // something (403, 404, 413…) and by the caller's words for what it was
    // doing otherwise — "Your changes could not be saved" says more than
    // "Server error". The status words are the explorer's (lib/errorWords).
    if (!err.response) return t('errors.network');
    const status = err.response.status;
    if (statusIsTelling(status) || !fallback) return statusWords(status, String(i18n.global.locale.value));
    return fallback;
  }
  // ⚠ A thrown JS error’s message is a developer’s words, not ours — all the
  // more reason to isolate its machine runs before an Arabic line draws it.
  if (err instanceof Error) return foreignText(String(i18n.global.locale.value), err.message);
  return fallback ?? t('errors.generic');
}

/** The sentence for a 423 app-lock refusal (see extractError). */
function lockedText(data: unknown): string {
  const d = (data ?? {}) as { plugin?: unknown; reason?: unknown };
  const app = typeof d.plugin === 'string' && d.plugin ? d.plugin : t('appLock.someApp');
  return typeof d.reason === 'string' && d.reason
    ? t('appLock.refusedReason', { app, reason: d.reason })
    : t('appLock.refused', { app });
}
