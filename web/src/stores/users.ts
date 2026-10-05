import { defineStore } from 'pinia';
import { ref } from 'vue';
import { UsersApi, type CreatedUser, type UserCreateRequest, type UserListParams, type UserUpdateRequest } from '@/api/users';
import type { PaginatedResponse, User } from '@/api/types';
import { extractError } from '@/api/client';
import { t } from '@/i18n';

const EMPTY: PaginatedResponse<User> = { items: [], total: 0, page: 1, page_size: 25 };

export const useUsersStore = defineStore('users', () => {
  const page = ref<PaginatedResponse<User>>(EMPTY);
  const loading = ref(false);
  const error = ref<string | null>(null);

  async function fetch(params: UserListParams = {}): Promise<void> {
    loading.value = true;
    error.value = null;
    try {
      page.value = await UsersApi.list(params);
    } catch (e: unknown) {
      error.value = extractError(e, t('errors.loadFailed'));
    } finally {
      loading.value = false;
    }
  }

  async function create(payload: UserCreateRequest): Promise<CreatedUser> {
    const u = await UsersApi.create(payload);
    // The list keeps the account, not how it was invited (its first password).
    const { invite: _invite, ...user } = u;
    page.value = { ...page.value, items: [user, ...page.value.items], total: page.value.total + 1 };
    return u;
  }

  // ⚠ PATCH /api/admin/users/{id} answers {ok: true}, not the account
  // (handlers/users.go Update). Putting that answer in the list replaced the
  // row with an object that has no email, name or role: back on Admin ->
  // Users the account showed as an empty row until the list was read again,
  // and stayed so when that read failed. The row is read again instead - the
  // server's own, with whatever the change moved besides the fields sent (the
  // level a role sets, the reason an account was off).
  async function update(id: number, payload: UserUpdateRequest): Promise<User> {
    await UsersApi.update(id, payload);
    const u = await UsersApi.get(id);
    page.value = {
      ...page.value,
      items: page.value.items.map((x) => (x.id === id ? u : x)),
    };
    return u;
  }

  async function remove(id: number): Promise<void> {
    await UsersApi.remove(id);
    page.value = {
      ...page.value,
      items: page.value.items.filter((x) => x.id !== id),
      total: Math.max(0, page.value.total - 1),
    };
  }

  async function resetPassword(id: number): Promise<string> {
    const { new_password } = await UsersApi.resetPassword(id);
    return new_password;
  }

  return { page, loading, error, fetch, create, update, remove, resetPassword };
});
