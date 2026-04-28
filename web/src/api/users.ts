import { api } from './client';
import type { PaginatedResponse, User, UserRole } from './types';

export interface UserCreateRequest {
  email: string;
  display_name: string;
  password?: string;
  role: UserRole;
  oidc_subject?: string | null;
  send_invite?: boolean;
}

export interface UserUpdateRequest {
  email?: string;
  display_name?: string;
  role?: UserRole;
  oidc_subject?: string | null;
  password?: string;
  locale?: string;
  timezone?: string;
}

export interface UserListParams {
  q?: string;
  role?: UserRole;
  page?: number;
  page_size?: number;
}

export const UsersApi = {
  async list(params: UserListParams = {}): Promise<PaginatedResponse<User>> {
    const { data } = await api.get<PaginatedResponse<User>>('/admin/users', { params });
    return data;
  },

  async get(id: number): Promise<User> {
    const { data } = await api.get<User>(`/admin/users/${id}`);
    return data;
  },

  async create(payload: UserCreateRequest): Promise<User> {
    const { data } = await api.post<User>('/admin/users', payload);
    return data;
  },

  async update(id: number, payload: UserUpdateRequest): Promise<User> {
    const { data } = await api.patch<User>(`/admin/users/${id}`, payload);
    return data;
  },

  async remove(id: number): Promise<void> {
    await api.delete(`/admin/users/${id}`);
  },

  async resetPassword(id: number): Promise<{ password: string }> {
    const { data } = await api.post<{ password: string }>(`/admin/users/${id}/reset-password`);
    return data;
  },
};
