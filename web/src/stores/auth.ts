import { defineStore } from 'pinia';
import { computed, ref } from 'vue';
import { AuthApi } from '@/api/auth';
import type { LoginRequest, User } from '@/api/types';
import { extractError } from '@/api/client';

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
      error.value = extractError(e, 'Login failed');
      return false;
    } finally {
      loading.value = false;
    }
  }

  async function logout(): Promise<void> {
    try {
      await AuthApi.logout();
    } catch {
      // ignore — we still clear local state
    } finally {
      user.value = null;
      permissions.value = [];
      sessionStorage.removeItem('filex.bearer');
    }
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
