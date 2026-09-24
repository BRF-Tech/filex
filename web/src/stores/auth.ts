import { defineStore } from 'pinia';
import { computed, ref } from 'vue';
import { AuthApi } from '@/api/auth';
import type { LoginRequest, User } from '@/api/types';
import { extractError } from '@/api/client';
import { attachViewPrefsHttp, detachViewPrefsStore, forgetPersonalPrefs } from '@brftech/filex-core';
import { getApiBaseUrl, getBearerToken, getUseCredentials } from '@/api/runtimeConfig';

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
  const api = getApiBaseUrl().replace(/\/api\/?$/, '');
  attachViewPrefsHttp({
    apiBase: api,
    headers: (): Record<string, string> => {
      const bearer = getBearerToken();
      return bearer ? { Authorization: `Bearer ${bearer}` } : {};
    },
    credentials: getUseCredentials() ? 'include' : 'omit',
  });
}
import { applyAccountLocale, t } from '@/i18n';
import { applyAccountTimeZone } from '@/lib/timezone';

export const useAuthStore = defineStore('auth', () => {
  const user = ref<User | null>(null);
  const permissions = ref<string[]>([]);
  const loading = ref(false);
  const error = ref<string | null>(null);
  const ready = ref(false);

  const isAuthenticated = computed(() => user.value !== null);
  const isAdmin = computed(() => user.value?.role === 'admin');

  async function fetchMe(): Promise<User | null> {
    loading.value = true;
    try {
      const me = await AuthApi.me();
      user.value = me.user;
      permissions.value = me.permissions ?? [];
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
      return me.user;
    } catch (e: unknown) {
      // 401 is the normal "not logged in" path; don't surface as error.
      user.value = null;
      permissions.value = [];
      return null;
    } finally {
      loading.value = false;
      ready.value = true;
    }
  }

  async function login(payload: LoginRequest): Promise<boolean> {
    loading.value = true;
    error.value = null;
    try {
      const res = await AuthApi.login(payload);
      user.value = res.user;
      if (res.token) {
        sessionStorage.setItem('filex.bearer', res.token);
      }
      // Re-hydrate permissions in case login response is leaner than /me.
      await fetchMe();
      return true;
    } catch (e: unknown) {
      error.value = extractError(e, t('login.errGeneric'));
      return false;
    } finally {
      loading.value = false;
    }
  }

  /**
   * Ends the session. Resolves to the IdP's end-session URL when signing out
   * has to continue there (an SSO session whose IdP can end sessions), else
   * null. Navigating is the caller's job — see lib/signOut.
   */
  async function logout(returnTo?: string): Promise<string | null> {
    let idpLogout: string | null = null;
    try {
      const res = await AuthApi.logout(returnTo);
      idpLogout = res?.logout_url || null;
    } catch {
      // ignore — we still clear local state
    } finally {
      user.value = null;
      permissions.value = [];
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

  return {
    user,
    permissions,
    loading,
    error,
    ready,
    isAuthenticated,
    isAdmin,
    fetchMe,
    login,
    logout,
    can,
  };
});
