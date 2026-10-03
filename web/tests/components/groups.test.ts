// The Groups pages (views/Groups.vue, views/GroupEdit.vue): the list says what
// each group carries, the edit page adds and removes members through the
// group's routes, keeps link kinds it does not edit, and leaves the SSO links
// to an administrator.
//
// ⚠ Every list here is tabular — the groups, a group's members, its folders —
// so each is THE table (core DataTable, docs/CONTRIBUTING.md → "One table"),
// with its own table id, never a <ul> or a <table> drawn by the page.
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { flushPromises, mount } from '@vue/test-utils';
import { createPinia, setActivePinia } from 'pinia';
import { createMemoryHistory, createRouter } from 'vue-router';
import { createI18n } from 'vue-i18n';

import en from '@/locales/en.json';

const { groupsApi } = vi.hoisted(() => {
  const detail = {
    group: {
      id: 3,
      name: 'Finance',
      description: 'money people',
      role_id: 7,
      priority: 2,
      links: [
        { kind: 'sso', value: 'finance' },
        { kind: 'other', value: 'cn=finance,ou=groups' },
      ],
    },
    members: [{ user_id: 2, email: 'ada@example.test', name: 'Ada', role: 'user', source: 'sso', added_at: '2026-09-01T00:00:00Z' }],
    grants: [{ id: 1, storage_id: 1, storage_name: 'depo', path_prefix: 'Reports', path: 'depo://Reports', is_dir: true, level: 'editor' }],
  };
  const groupsApi = {
    list: vi.fn(async () => [{ ...detail.group, member_count: 1, grant_count: 1 }]),
    get: vi.fn(async () => structuredClone(detail)),
    create: vi.fn(),
    update: vi.fn(async (_id: number, g: object) => ({ ...structuredClone(detail), group: { ...detail.group, ...g } })),
    remove: vi.fn(),
    addMembers: vi.fn(async () => structuredClone(detail)),
    removeMember: vi.fn(async () => ({ ...structuredClone(detail), members: [] })),
    forUser: vi.fn(async () => []),
  };
  return { groupsApi, detail };
});

vi.mock('@/api/groups', () => ({ GroupsApi: groupsApi }));
vi.mock('@/api/roles', () => ({
  RolesApi: { listRules: vi.fn(async () => ({ rules: [{ id: 7, name: 'Look only', enabled: true }], assignments: {} })) },
}));
vi.mock('@/api/users', () => ({
  UsersApi: {
    list: vi.fn(async () => ({
      items: [
        { id: 2, email: 'ada@example.test', display_name: 'Ada', role: 'user' },
        { id: 4, email: 'bob@example.test', display_name: 'Bob', role: 'user' },
        // An older server ignores ?q= and answers with everyone.
        { id: 5, email: 'carl@example.test', display_name: 'Carl', role: 'user' },
      ],
      total: 3,
      page: 1,
      page_size: 25,
    })),
  },
}));

import Groups from '@/views/Groups.vue';
import GroupEdit from '@/views/GroupEdit.vue';
import { useAuthStore } from '@/stores/auth';
import { DataTable } from '@brftech/filex-core';
import { closeRowMenus, openRowMenu, pickMenuItem } from '../helpers/rowMenu';

function tableIds(w: { findAllComponents: (c: unknown) => { props: (k: string) => unknown }[] }): unknown[] {
  return w.findAllComponents(DataTable).map((t) => t.props('tableId'));
}

function setup(isAdmin: boolean) {
  setActivePinia(createPinia());
  const auth = useAuthStore();
  auth.user = { id: 1, email: 'me@example.test', role: isAdmin ? 'admin' : 'user' } as never;
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/groups', name: 'groups', component: Groups },
      { path: '/groups/:id', name: 'groups.edit', component: GroupEdit },
      { path: '/users/:id', name: 'users.edit', component: { template: '<div />' } },
    ],
  });
  const i18n = createI18n({ legacy: false, locale: 'en', messages: { en } });
  return { router, plugins: [router, i18n] };
}

beforeEach(() => vi.clearAllMocks());

describe('Groups list', () => {
  it('is THE table and says what each group carries', async () => {
    const { router, plugins } = setup(true);
    await router.push('/groups');
    const w = mount(Groups, { global: { plugins }, attachTo: document.body });
    await flushPromises();
    expect(tableIds(w)).toEqual(['admin.groups']);
    expect(w.find('ul').exists(), 'no list drawn by the page').toBe(false);
    expect(w.find('table').exists()).toBe(false);
    const name = w.get('[data-testid="group-3"]');
    expect(name.text()).toContain('Finance');
    expect(name.text()).toContain('money people');
    const text = w.text();
    expect(text).toContain('Look only');
    expect(text).toContain('1 member');
    expect(text).toContain('1 folder');
    expect(text).toContain('finance');

    // Its verb is the row's one Actions control.
    await openRowMenu(w, 'group-actions-3');
    await pickMenuItem('group-actions-3-edit');
    await flushPromises();
    expect(router.currentRoute.value.name).toBe('groups.edit');
    closeRowMenus();
    w.unmount();
  });
});

describe('Group page', () => {
  it('adds a person through the group and removes one after saying they may come back', async () => {
    vi.useFakeTimers();
    const confirm = vi.spyOn(window, 'confirm').mockReturnValue(true);
    const { router, plugins } = setup(true);
    await router.push('/groups/3');
    const w = mount(GroupEdit, { global: { plugins }, attachTo: document.body });
    await flushPromises();

    const search = w.get('[data-testid="group-members"] input');
    await search.setValue('bo');
    vi.advanceTimersByTime(200);
    await flushPromises();
    expect(w.find('[data-testid="group-add-2"]').exists(), 'a member is not offered again').toBe(false);
    expect(w.find('[data-testid="group-add-5"]').exists(), 'someone who does not match what was typed is not offered').toBe(false);
    await w.get('[data-testid="group-add-4"]').trigger('click');
    await flushPromises();
    expect(groupsApi.addMembers).toHaveBeenCalledWith(3, [4]);

    expect(tableIds(w)).toEqual(['admin.groups.members', 'admin.groups.folders']);
    expect(w.find('[data-testid="group-members"] ul li button').exists(), 'no list drawn by the page').toBe(false);
    await openRowMenu(w, 'group-member-actions-2');
    await pickMenuItem('group-member-actions-2-remove');
    await flushPromises();
    expect(confirm.mock.calls[0][0]).toContain('next sign-in');
    expect(groupsApi.removeMember).toHaveBeenCalledWith(3, 2);
    closeRowMenus();
    vi.useRealTimers();
    w.unmount();
  });

  it('lists the folders the group reaches, with their level', async () => {
    const { router, plugins } = setup(true);
    await router.push('/groups/3');
    const w = mount(GroupEdit, { global: { plugins } });
    await flushPromises();
    const folders = w.get('[data-testid="group-grants"]');
    expect(folders.text()).toContain('depo://Reports');
    expect(folders.text()).toContain(en.groups.level.editor);
  });

  it('saves the SSO links one per line and keeps the kinds it does not edit', async () => {
    const { router, plugins } = setup(true);
    await router.push('/groups/3');
    const w = mount(GroupEdit, { global: { plugins } });
    await flushPromises();

    await w.get('[data-testid="group-sso"] textarea').setValue('finance\nFinance, Team\n\nfinance');
    await w.get('[data-testid="group-form"]').trigger('submit');
    await flushPromises();
    expect(groupsApi.update).toHaveBeenCalledWith(3, {
      name: 'Finance',
      description: 'money people',
      role_id: 7,
      priority: 2,
      links: [
        { kind: 'other', value: 'cn=finance,ou=groups' },
        { kind: 'sso', value: 'finance' },
        { kind: 'sso', value: 'Finance, Team' },
      ],
    });
  });

  it('offers a role priority only while the group has a role, and sends it', async () => {
    const { router, plugins } = setup(true);
    await router.push('/groups/3');
    const w = mount(GroupEdit, { global: { plugins } });
    await flushPromises();
    await w.get('[data-testid="group-priority"] input').setValue('9');
    await w.get('[data-testid="group-form"]').trigger('submit');
    await flushPromises();
    expect(groupsApi.update.mock.calls[0][1]).toMatchObject({ role_id: 7, priority: 9 });

    await w.get('[data-testid="group-role"] select').setValue('');
    expect(w.find('[data-testid="group-priority"]').exists()).toBe(false);
  });

  it('leaves the SSO links and the role priority to an administrator', async () => {
    const { router, plugins } = setup(false);
    await router.push('/groups/3');
    const w = mount(GroupEdit, { global: { plugins } });
    await flushPromises();
    expect(w.get('[data-testid="group-sso"] textarea').attributes('disabled')).toBeDefined();
    expect(w.text()).toContain(en.groups.fields.ssoAdminOnly);
    // …and which group's role wins.
    expect(w.get('[data-testid="group-priority"] input').attributes('disabled')).toBeDefined();
    expect(w.text()).toContain(en.groups.fields.priorityAdminOnly);
  });
});
