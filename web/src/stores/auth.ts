import { defineStore } from 'pinia';
import { computed, ref } from 'vue';
import { AuthApi } from '@/api/auth';
import type { LoginHandoff, LoginRequest, LoginResponse, User } from '@/api/types';
import type { PermRuleSettings } from '@/api/roles';
import { extractError } from '@/api/client';
import { readLoginRefusal, type LoginRefusal } from '@/lib/loginRefusal';
import { ssoRefusalReason, type SsoRefusalReason } from '@/lib/ssoRefusal';
import axios from 'axios';
import { attachViewPrefsHttp, detachViewPrefsStore, forgetPersonalPrefs } from '@brftech/filex-core';
import { getBearerToken, getServerRoot, getUseCredentials } from '@/api/runtimeConfig';

/**
 * The per-person view document (`@brftech/filex-core` → lib/viewPrefs) —
 * attached the moment somebody is signed in, not only when the explorer
 * mounts.
 *
 * ⚠ Two things in the admin panel read it now: every table keeps its columns
 * and its sort there (lib/tablePrefs), and the person's settings hold their
 * DEFAULT folder view. Left to the explorer, a person who went straight to
 * /admin/users would resize a column into a session-only store and lose it on
 * reload, and the settings modal would show no default at all.
 *
 * ⚠ Attached per ACCOUNT: signing in as somebody else detaches first, so the
 * next person never sees the previous one's arrangements.
 */
let viewPrefsFor: number | null = null;
function attachViewPrefsFor(userId: number | null): void {
  if (userId === viewPrefsFor) return;
  detachViewPrefsStore();
  viewPrefsFor = userId;
  if (userId === null) return;
  attachViewPrefsHttp({
    apiBase: getServerRoot(),
    headers: (): Record<string, string> => {
      const bearer = getBearerToken();
      return bearer ? { Authorization: `Bearer ${bearer}` } : {};
    },
    credentials: getUseCredentials() ? 'include' : 'omit',
  });
}
import { applyAccountLocale, i18n, t } from '@/i18n';
import { applyAccountTimeZone } from '@/lib/timezone';
import { forgetWebPush } from '@/lib/webPush';

/**
 * What a failed `GET /api/auth/me` says about the session.
 *
 * ⚠⚠ ONLY TWO STATUSES MEAN "NOBODY". The server says it with 401 - no
 * session, one that lapsed, the maintenance lock-out
 * (auth/middleware.go, auth/maintenance.go) - and 403, an account that may
 * not have one: disabled (`reason: account_disabled`), or a token whose name
 * or verbs it refuses. A session waiting on two-factor enrolment is a 200
 * here (`two_factor_required`), never a 403. Everything else is the server
 * NOT answering, and nothing the store believes may change on it:
 *
 *   'busy'     reached, and could not answer: a 5xx (filex, or a proxy in
 *              front of it while it restarts) or a 429. Asked again a moment
 *              later (askWho).
 *   'silent'   no response at all - the browser dropped the request because
 *              the page is being left, the page cancelled it, the connection
 *              is down, it timed out - or any other status, which is not about
 *              the session.
 */
type MeFailure = 'nobody' | 'busy' | 'silent';

function statusOf(e: unknown): number | undefined {
  const status = (e as { response?: { status?: unknown } } | null | undefined)?.response?.status;
  return typeof status === 'number' ? status : undefined;
}

function meFailure(e: unknown): MeFailure {
  const status = statusOf(e);
  if (status === 401 || status === 403) return 'nobody';
  if (status === 429 || (status !== undefined && status >= 500 && status <= 599)) return 'busy';
  return 'silent';
}

/** How long to wait before asking again a server that could not answer. */
const ME_RETRY_MS = [500, 1500];
/** The most a 429's Retry-After may make a page wait for its session. */
const ME_RETRY_AFTER_CAP_MS = 3000;

function retryDelay(e: unknown, planned: number): number {
  const raw = (e as { response?: { headers?: Record<string, unknown> } } | null | undefined)?.response?.headers?.[
    'retry-after'
  ];
  const seconds = Number(raw);
  if (!Number.isFinite(seconds) || seconds <= 0) return planned;
  return Math.min(Math.max(planned, seconds * 1000), ME_RETRY_AFTER_CAP_MS);
}

/**
 * `GET /api/auth/me`, asked again while the server is reachable but cannot
 * answer ('busy'): twice, after 0.5 s and 1.5 s. A cold load that met a
 * restarting server would otherwise have no user, and the router guard would
 * send a person whose session is alive to the sign-in page.
 */
async function askWho(): Promise<Awaited<ReturnType<typeof AuthApi.me>>> {
  for (let attempt = 0; ; attempt++) {
    try {
      return await AuthApi.me();
    } catch (e) {
      if (attempt >= ME_RETRY_MS.length || meFailure(e) !== 'busy') throw e;
      const wait = retryDelay(e, ME_RETRY_MS[attempt] ?? 0);
      await new Promise((resolve) => setTimeout(resolve, wait));
    }
  }
}

export const useAuthStore = defineStore('auth', () => {
  const user = ref<User | null>(null);
  const permissions = ref<string[]>([]);
  /** Allowed only in some folders (a role's folder part) — offered, decided per file by the server. */
  const permissionsInFolders = ref<string[]>([]);
  /** Differ from folder to folder — the file browser asks per path. */
  const permissionsByFolder = ref<string[]>([]);
  const permissionSettings = ref<PermRuleSettings>({});
  const twoFactorRequired = ref(false);
  const loading = ref(false);
  const error = ref<string | null>(null);
  /** What the last refused sign-in said beyond "no": tries left, or a lock's wait. */
  const refusal = ref<LoginRefusal | null>(null);
  /**
   * Why the server refused the person it was told about without a form - the
   * header proxy named them, the first-login rule said no, a disabled account
   * - as the reason code on /api/auth/me's 401/403 (lib/ssoRefusal). The
   * sign-in page says it. Null after any answer that carried none.
   */
  const signInReason = ref<SsoRefusalReason | null>(null);
  const ready = ref(false);
  /**
   * Whether the server has said, in this document, who is signed in: a
   * `GET /api/auth/me` that got an answer (the person, or a 401/403), a
   * sign-in or a sign-out. False until then, and a question the server did
   * not answer leaves it as it was (meFailure, fetchMe).
   *
   * ⚠ `user === null` alone cannot tell "nobody is signed in" from "nobody has
   * been asked yet", and `App.vue` forgets the person's look and the session
   * hint on the first of the two only (see there).
   */
  const sessionAnswered = ref(false);

  const isAuthenticated = computed(() => user.value !== null);
  const isAdmin = computed(() => user.value?.role === 'admin');

  async function fetchMe(): Promise<User | null> {
    loading.value = true;
    try {
      const me = await askWho();
      sessionAnswered.value = true;
      user.value = me.user;
      permissions.value = me.permissions ?? [];
      permissionsInFolders.value = me.permissions_in_folders ?? [];
      permissionsByFolder.value = me.permissions_by_folder ?? [];
      permissionSettings.value = me.permission_settings ?? {};
      twoFactorRequired.value = me.two_factor_required === true;
      // ⚠ The account's saved language, applied at the one place every entry
      // path passes through — a cold load, a login and a re-hydration all end
      // up here. Putting it in login() alone would leave a returning session
      // (cookie still valid, no login form) in the wrong language.
      applyAccountLocale(me.user?.locale);
      // zaman:z1 — and the account's clock, at the same single choke point and
      // for the same reason. Unlike the language this one OUTRANKS the local
      // mirror: there is one control for it and it writes both halves, so a
      // difference is a stale cache rather than a newer decision
      // (lib/timezone's header).
      applyAccountTimeZone(me.user?.timezone);
      attachViewPrefsFor(me.user?.id ?? null);
      error.value = null;
      signInReason.value = null;
      return me.user;
    } catch (e: unknown) {
      // ⚠⚠ NO ANSWER IS NOT "NOBODY" (meFailure). A question the server did
      // not answer - no response, a 5xx or 429 still there after askWho's
      // retries, a status that is not about the session - changes nothing
      // the store believes, and returns who it believed. Taken as "signed
      // out", it nulled the user, and `App.vue`'s no-session branch then
      // cleared `filex.session` and the person's look from this browser's
      // storage, which the NEXT page reads at its first paint. WebKit and
      // Firefox run the error handlers of a page that is being left
      // (Chromium does not): leaving /admin/explore while its own `fetchMe`
      // was in flight booted /admin/users as a signed-out window, with the
      // stock palette instead of the person's (e2e 98, WebKit, 0.57 second
      // full chain); a proxy's 502 while filex restarted did the same on any
      // engine. Other tabs heard it too: the removed `filex.session` is the
      // cross-tab sign-out signal (lib/instanceThemes).
      if (meFailure(e) !== 'nobody') return user.value;
      sessionAnswered.value = true;
      // 401 (or 403) is the normal "not logged in" path; don't surface as error.
      signInReason.value = axios.isAxiosError(e)
        ? ssoRefusalReason((e.response?.data as Record<string, unknown> | undefined)?.reason)
        : null;
      user.value = null;
      permissions.value = [];
      permissionsInFolders.value = [];
      permissionsByFolder.value = [];
      twoFactorRequired.value = false;
      return null;
    } finally {
      loading.value = false;
      ready.value = true;
    }
  }

  /**
   * Set when the last sign-in was handed to a tenant's own address (a realm
   * typed on the platform's page of a multi-tenant install): no session was
   * opened here, and the page carries `code` to `origin` (Login.vue). Null
   * otherwise.
   */
  const handoff = ref<LoginHandoff | null>(null);

  async function login(payload: LoginRequest): Promise<boolean> {
    loading.value = true;
    error.value = null;
    refusal.value = null;
    handoff.value = null;
    try {
      // The language on screen goes with the sign-in: an account that holds
      // none is given it by the server (AuthApi.login).
      const res = await AuthApi.login(payload, String(i18n.global.locale.value));
      if (res.handoff) {
        // Signed in, but the session belongs to the tenant's address: nothing
        // is kept here. The caller navigates (see `handoff`).
        handoff.value = res.handoff;
        return false;
      }
      return await adopt(res);
    } catch (e: unknown) {
      error.value = extractError(e, t('login.errGeneric'));
      refusal.value = readLoginRefusal(e);
      return false;
    } finally {
      loading.value = false;
    }
  }

  /** Redeems a sign-in handed over from the platform's address: the session is
   *  opened on this one. False when the ticket was spent, expired or is not
   *  this address's. */
  async function redeemHandoff(code: string): Promise<boolean> {
    loading.value = true;
    error.value = null;
    refusal.value = null;
    try {
      return await adopt(await AuthApi.handoff(code, String(i18n.global.locale.value)));
    } catch (e: unknown) {
      error.value = extractError(e, t('login.errHandoff'));
      return false;
    } finally {
      loading.value = false;
    }
  }

  /** A sign-in answer that opened a session here. */
  async function adopt(res: LoginResponse): Promise<boolean> {
    user.value = res.user ?? null;
    if (res.token) {
      sessionStorage.setItem('filex.bearer', res.token);
    }
    // Re-hydrate permissions in case login response is leaner than /me.
    await fetchMe();
    return true;
  }

  /**
   * Ends the session. Resolves to the IdP's end-session URL when signing out
   * has to continue there (an SSO session whose IdP can end sessions), else
   * null. Navigating is the caller's job — see lib/signOut.
   */
  async function logout(returnTo?: string): Promise<string | null> {
    let idpLogout: string | null = null;
    try {
      // #191 - this browser stops receiving the person's push notifications
      // while the session can still say so (lib/webPush; bounded).
      if (user.value) await forgetWebPush(user.value.id);
      const res = await AuthApi.logout(returnTo);
      idpLogout = res?.logout_url || null;
    } catch {
      // ignore — we still clear local state
    } finally {
      sessionAnswered.value = true;
      user.value = null;
      permissions.value = [];
      permissionsInFolders.value = [];
      permissionsByFolder.value = [];
      twoFactorRequired.value = false;
      sessionStorage.removeItem('filex.bearer');
      attachViewPrefsFor(null);
      // ⚠⚠ And this browser's copy of what the PERSON liked — the palette,
      // light/dark, row density and language (`@brftech/filex-core` →
      // lib/prefs). Owner, 2026-09-21: *"oturum kapanınca temizlersek
      // localstorage'ı tamamız ya o kısımda"*. Somebody who has signed out has
      // left, and a shared machine should not go on holding their taste.
      //
      // ⚠ Here as well as in `App.vue`'s no-session branch, and the pair is
      // not redundant: this is the sign-out the app is TOLD about, that one is
      // every way a session can end without anybody saying so. Neither is
      // sufficient and the function is idempotent.
      //
      // ⚠ It writes `filex.session`, and that write is what carries the
      // sign-out to the OTHER tabs on this origin: the `storage` event it
      // raises is the only signal that crosses tabs, and `lib/instanceThemes`
      // listens for exactly that key. The palette and mode listeners cannot
      // do it — by the time they run `hasSession()` is already false and they
      // refuse the event by design.
      forgetPersonalPrefs();
    }
    return idpLogout;
  }

  function can(perm: string): boolean {
    if (isAdmin.value) return true;
    return permissions.value.includes(perm);
  }

  /**
   * May this account open the admin panel at all: an administrator, or a
   * delegated one holding at least one admin.* permission (users, grants,
   * shares, audit, monitor). Each page then asks for its own — see the
   * router's `adminPerm` meta.
   */
  const hasAdminArea = computed(() => isAdmin.value || permissions.value.some((p) => p.startsWith('admin.')));

  return {
    user,
    permissions,
    permissionsInFolders,
    permissionsByFolder,
    loading,
    error,
    refusal,
    signInReason,
    ready,
    sessionAnswered,
    isAuthenticated,
    isAdmin,
    hasAdminArea,
    permissionSettings,
    twoFactorRequired,
    fetchMe,
    login,
    handoff,
    redeemHandoff,
    logout,
    can,
  };
});
