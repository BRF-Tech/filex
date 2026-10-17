// #117 — a whole storage given to people and groups (api/storageAccess).
//
// No route of its own: an assignment is a grant on the storage's ROOT, made
// and read through the permission routes the explorer's access dialog uses.
// What is pinned here is the request the server receives - the root path,
// the person or the group, the level the account role allows - and that
// "revoke all" takes back the storage-wide rows one by one and nothing else.

import { beforeEach, describe, expect, it, vi } from 'vitest';

type Call = { method: string; url: string; params?: unknown; body?: unknown };
const calls: Call[] = [];
const answers = new Map<string, unknown>();
const failDelete = new Set<string>();

vi.mock('@/api/client', () => ({
  extractError: (_e: unknown, f: string) => f,
  api: {
    get: vi.fn(async (url: string, cfg?: { params?: unknown }) => {
      calls.push({ method: 'get', url, params: cfg?.params });
      return { data: answers.get(url) ?? {} };
    }),
    post: vi.fn(async (url: string, body: unknown) => {
      calls.push({ method: 'post', url, body });
      return { data: {} };
    }),
    patch: vi.fn(async (url: string, body: unknown) => {
      calls.push({ method: 'patch', url, body });
      return { data: {} };
    }),
    delete: vi.fn(async (url: string) => {
      calls.push({ method: 'delete', url });
      if (failDelete.has(url)) throw new Error('refused');
      return { data: { ok: true } };
    }),
  },
}));

import {
  StorageAccessApi,
  accessKey,
  assigneeName,
  levelFor,
  storageRootPath,
  type StorageAssignment,
} from '@/api/storageAccess';

beforeEach(() => {
  calls.length = 0;
  answers.clear();
  failDelete.clear();
});

describe('the storage root on the wire', () => {
  it('is `<storage>://` - the path every door reads as the whole storage', () => {
    expect(storageRootPath('alpha')).toBe('alpha://');
  });
});

describe('StorageAccessApi.list', () => {
  it('asks the permission route at the root and keeps its own grants, people and groups', async () => {
    answers.set('/files/permissions', {
      storage_rbac: true,
      direct: [
        { id: 4, level: 'editor', user_id: 9, user_email: 'alice@example.com', user_name: 'Alice' },
        { id: 4, kind: 'group', level: 'viewer', group_id: 2, group_name: 'Finance' },
      ],
      inherited: [],
    });
    const got = await StorageAccessApi.list('alpha');
    expect(calls).toEqual([{ method: 'get', url: '/files/permissions', params: { path: 'alpha://' } }]);
    expect(got.rbac).toBe(true);
    expect(got.assignments.map((a) => [a.kind, a.id])).toEqual([
      ['user', 4],
      ['group', 4],
    ]);
    // Two rows with the same id are still two rows: a group's grant has its own ids.
    expect(new Set(got.assignments.map((a) => accessKey(a.kind, a.id))).size).toBe(2);
  });

  it('says per-item access control is off, and reads an answer without rows as none', async () => {
    answers.set('/files/permissions', { storage_rbac: false });
    expect(await StorageAccessApi.list('alpha')).toEqual({ rbac: false, assignments: [] });
  });
});

describe('StorageAccessApi.assign', () => {
  it('gives a person the root, at the level asked', async () => {
    await StorageAccessApi.assign('alpha', { kind: 'user', id: 9, name: 'Alice', role: 'user' }, 'editor');
    expect(calls).toEqual([
      { method: 'post', url: '/files/permissions', body: { path: 'alpha://', user_id: 9, level: 'editor' } },
    ]);
  });

  it('gives a group the root by group_id', async () => {
    await StorageAccessApi.assign('alpha', { kind: 'group', id: 2, name: 'Finance' }, 'owner');
    expect(calls[0].body).toEqual({ path: 'alpha://', group_id: 2, level: 'owner' });
  });

  it('gives a viewer account viewer: the account role is the ceiling (the server answers 400 above it)', async () => {
    await StorageAccessApi.assign('alpha', { kind: 'user', id: 3, name: 'Bob', role: 'viewer' }, 'owner');
    expect(calls[0].body).toEqual({ path: 'alpha://', user_id: 3, level: 'viewer' });
    expect(levelFor({ kind: 'group' }, 'owner'), 'a group is held to each member’s role, not here').toBe('owner');
  });
});

describe('changing and taking back', () => {
  const person: StorageAssignment = { id: 4, kind: 'user', level: 'viewer', user_id: 9 };
  const group: StorageAssignment = { id: 4, kind: 'group', level: 'viewer', group_id: 2 };

  it('a person’s grant and a group’s go by their own routes', async () => {
    await StorageAccessApi.setLevel(person, 'editor');
    await StorageAccessApi.setLevel(group, 'owner');
    await StorageAccessApi.revoke(person);
    await StorageAccessApi.revoke(group);
    expect(calls).toEqual([
      { method: 'patch', url: '/files/permissions/4', body: { level: 'editor' } },
      { method: 'patch', url: '/files/permissions/groups/4', body: { level: 'owner' } },
      { method: 'delete', url: '/files/permissions/4' },
      { method: 'delete', url: '/files/permissions/groups/4' },
    ]);
  });

  it('"revoke all" takes back each storage-wide row - and answers the ones it could not', async () => {
    failDelete.add('/files/permissions/groups/4');
    const left = await StorageAccessApi.revokeAll([person, group]);
    expect(calls.map((c) => `${c.method} ${c.url}`)).toEqual([
      'delete /files/permissions/4',
      'delete /files/permissions/groups/4',
    ]);
    expect(left).toEqual([group]);
  });
});

describe('StorageAccessApi.search', () => {
  it('offers groups, then people, from the two lookups the access dialog makes', async () => {
    answers.set('/files/permissions/users', {
      users: [{ id: 9, email: 'alice@example.com', display_name: 'Alice', role: 'user' }],
    });
    answers.set('/files/permissions/groups', { groups: [{ id: 2, name: 'Finance', description: 'money people' }] });
    const got = await StorageAccessApi.search('fi');
    expect(calls.map((c) => [c.url, c.params])).toEqual([
      ['/files/permissions/users', { q: 'fi' }],
      ['/files/permissions/groups', { q: 'fi' }],
    ]);
    expect(got).toEqual([
      { kind: 'group', id: 2, name: 'Finance', detail: 'money people' },
      { kind: 'user', id: 9, name: 'Alice', detail: 'alice@example.com', role: 'user' },
    ]);
  });
});

describe('naming an assignment', () => {
  it('a person by the name every screen uses, a group by its name', () => {
    expect(assigneeName({ id: 1, kind: 'user', level: 'viewer', user_email: 'alice@example.com' })).toBe('alice@example.com');
    expect(assigneeName({ id: 1, kind: 'user', level: 'viewer', user_name: 'Alice', user_email: 'alice@example.com' })).toBe('Alice');
    expect(assigneeName({ id: 1, kind: 'group', level: 'viewer', group_name: 'Finance' })).toBe('Finance');
  });
});
