import { defineStore } from 'pinia';
import { ref } from 'vue';
import { UsersApi, type UserCreateRequest, type UserListParams, type UserUpdateRequest } from '@/api/users';
import type { PaginatedResponse, User } from '@/api/types';
import { extractError } from '@/api/client';

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
      error.value = extractError(e, 'Failed to load users');
    } finally {
      loading.value = false;
    }
  }

  async function create(payload: UserCreateRequest): Promise<User> {
    const u = await UsersApi.create(payload);
    page.value = { ...page.value, items: [u, ...page.value.items], total: page.value.total + 1 };
    return u;
  }

  async function update(id: number, payload: UserUpdateRequest): Promise<User> {
    const u = await UsersApi.update(id, payload);
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
    const { password } = await UsersApi.resetPassword(id);
    return password;
  }

  return { page, loading, error, fetch, create, update, remove, resetPassword };
});
