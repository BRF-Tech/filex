// Tests for src/stores/users.ts. UsersApi is mocked module-wide.
//
// PATCH /api/admin/users/{id} answers {ok: true}, not the account. The store
// used to put that answer in the list in place of the row: the account lost
// its email, name and role there (Admin -> Users showed an empty row) until
// the list was read again. The row is the server's account now, read again.
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { createPinia, setActivePinia } from 'pinia';

vi.mock('@/api/users', () => ({
  UsersApi: {
    list: vi.fn(),
    get: vi.fn(),
    create: vi.fn(),
    update: vi.fn(),
    remove: vi.fn(),
    resetPassword: vi.fn(),
  },
}));

import { useUsersStore } from '@/stores/users';
import { UsersApi } from '@/api/users';
import type { User } from '@/api/types';

const person = (id: number, email: string, name: string): User => ({
  id,
  email,
  display_name: name,
  role: 'user',
  created_at: '2026-10-01T00:00:00Z',
  updated_at: '2026-10-01T00:00:00Z',
  enabled: true,
});

describe('users store: update', () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    vi.clearAllMocks();
    const rows = [person(1, 'ada@corp.example', 'Ada'), person(2, 'bob@corp.example', 'Bob')];
    vi.mocked(UsersApi.list).mockResolvedValue({ items: rows, total: 2, page: 1, page_size: 25 });
    vi.mocked(UsersApi.update).mockResolvedValue({ ok: true } as never);
    vi.mocked(UsersApi.get).mockResolvedValue({ ...person(2, 'bob@corp.example', 'Robert'), updated_at: '2026-10-02T00:00:00Z' });
  });

  it('keeps the account in the list, as the server holds it after the change', async () => {
    const store = useUsersStore();
    await store.fetch();
    const got = await store.update(2, { display_name: 'Robert' });

    const answers = store.page.items.filter((u) => (u as unknown as { ok?: boolean }).ok === true);
    expect(answers, 'the PATCH answer {ok: true} was put in the list in place of the account').toEqual([]);
    const row = store.page.items.find((u) => u.id === 2);
    expect(row, 'the row is still there').toBeDefined();
    expect(row?.email, 'the row lost its email: the PATCH answer was put in its place').toBe('bob@corp.example');
    expect(row?.display_name).toBe('Robert');
    expect(row?.role).toBe('user');
    expect(got.email).toBe('bob@corp.example');
    expect(UsersApi.get).toHaveBeenCalledWith(2);
    // The other row is untouched.
    expect(store.page.items.find((u) => u.id === 1)?.display_name).toBe('Ada');
  });

  it('a refused change leaves the row as it was', async () => {
    vi.mocked(UsersApi.update).mockRejectedValueOnce(new Error('409 cannot demote the last admin'));
    const store = useUsersStore();
    await store.fetch();
    await expect(store.update(1, { role: 'viewer' })).rejects.toThrow();
    expect(store.page.items.find((u) => u.id === 1)).toEqual(person(1, 'ada@corp.example', 'Ada'));
    expect(UsersApi.get).not.toHaveBeenCalled();
  });
});
