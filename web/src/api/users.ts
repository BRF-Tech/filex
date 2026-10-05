import { api } from './client';
import type { PaginatedResponse, User, UserRole } from './types';

export interface UserCreateRequest {
  email: string;
  display_name: string;
  password?: string;
  role: UserRole;
  /** The login name (SFTP, FTP…); empty: derived from the address. */
  username?: string;
  /** Make a first password and e-mail it; `password` is then ignored. */
  send_invite?: boolean;
  /** Make it although an LDAP directory owns the address. */
  allow_directory_email?: boolean;
}

/** A new account and — invited — whether the letter went out, or its first
 *  password, shown once, when no mail could be sent. */
export type CreatedUser = User & { invite?: { emailed: boolean; temp_password?: string } };

export interface UserUpdateRequest {
  display_name?: string;
  role?: UserRole;
  password?: string;
  locale?: string;
  timezone?: string;
  /** Switch the account on or off; on approves one waiting for approval. */
  enabled?: boolean;
  /** Remove the account's SSO bind: its next SSO sign-in is matched by its
   *  email address again (docs/SSO.md). */
  sso_unlink?: boolean;
}

export interface UserListParams {
  q?: string;
  role?: UserRole;
  page?: number;
  page_size?: number;
}

export const UsersApi = {
  async list(params: UserListParams = {}): Promise<PaginatedResponse<User>> {
    // Backend handler currently returns a flat User[] array (other
    // internal callers depend on that shape). Normalize to the
    // paginated envelope the admin UI expects so views can render
    // without checking both shapes inline.
    const { data } = await api.get<PaginatedResponse<User> | User[]>('/admin/users', { params });
    if (Array.isArray(data)) {
      return {
        items: data,
        total: data.length,
        page: 1,
        page_size: data.length || 1,
      };
    }
    return {
      items: data.items ?? [],
      total: data.total ?? 0,
      page: data.page ?? 1,
      page_size: data.page_size ?? 0,
    };
  },

  async get(id: number): Promise<User> {
    const { data } = await api.get<User>(`/admin/users/${id}`);
    return data;
  },

  async create(payload: UserCreateRequest): Promise<CreatedUser> {
    const { data } = await api.post<CreatedUser>('/admin/users', payload);
    return data;
  },

  // The server answers {ok: true}: the account is read again with get()
  // (stores/users.ts update).
  async update(id: number, payload: UserUpdateRequest): Promise<{ ok: boolean }> {
    const { data } = await api.patch<{ ok: boolean }>(`/admin/users/${id}`, payload);
    return data;
  },

  async remove(id: number): Promise<void> {
    await api.delete(`/admin/users/${id}`);
  },

  // ⚠ The server answers `new_password` (handlers/users_admin.go). Reading
  // `password` here dropped the one-time value on the floor: the account was
  // reset and signed out, and nobody ever saw what it was reset to (issue #25).
  async resetPassword(id: number): Promise<{ new_password: string }> {
    const { data } = await api.post<{ new_password: string }>(`/admin/users/${id}/reset-password`);
    return data;
  },
};
