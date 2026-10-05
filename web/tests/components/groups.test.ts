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
    detach: vi.fn(),
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
      gives_admin: false,
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

  it('lets an administrator make the members administrators, with no other role or priority', async () => {
    const { router, plugins } = setup(true);
    await router.push('/groups/3');
    const w = mount(GroupEdit, { global: { plugins } });
    await flushPromises();
    await w.get('[data-testid="group-role"] select').setValue('admin');
    expect(w.find('[data-testid="group-priority"]').exists()).toBe(false);
    expect(w.text()).toContain(en.groups.fields.adminHint);
    await w.get('[data-testid="group-form"]').trigger('submit');
    await flushPromises();
    expect(groupsApi.update.mock.calls[0][1]).toMatchObject({ role_id: null, gives_admin: true });
  });

  it('does not offer Administrator to a delegated administrator', async () => {
    const { router, plugins } = setup(false);
    await router.push('/groups/3');
    const w = mount(GroupEdit, { global: { plugins } });
    await flushPromises();
    const values = w.findAll('[data-testid="group-role"] option').map((o) => o.attributes('value'));
    expect(values).not.toContain('admin');
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

/** The groups the table shows, by their name cell. */
const rowIds = (w: { findAll: (sel: string) => { attributes: (n: string) => string | undefined }[] }) =>
  w.findAll('[data-testid^="group-"]').map((a) => a.attributes('data-testid')!).filter((id) => /^group-\d+$/.test(id));

describe('Groups from directory sync', () => {
  const synced = {
    id: 9,
    name: 'dept-legal',
    description: 'From the directory: cn=dept-legal,ou=groups,dc=example,dc=com',
    role_id: null,
    priority: 0,
    links: [{ kind: 'ldap', value: 'cn=dept-legal,ou=groups,dc=example,dc=com' }],
    directory_id: 'ldap:u-legal',
    directory_name: 'dept-legal',
  };
  const gone = { ...synced, id: 10, name: 'old-team', directory_id: 'ldap:u-old', directory_name: 'old-team', directory_state: 'removed' };

  it('flags a group whose LDAP group is gone, and filters by where members come from', async () => {
    groupsApi.list.mockResolvedValueOnce([
      { ...groupsApi.detail?.group, id: 3, name: 'Finance', description: '', role_id: null, priority: 0, links: [{ kind: 'sso', value: 'finance' }] },
      synced,
      gone,
    ] as never);
    const { router, plugins } = setup(true);
    await router.push('/groups');
    const w = mount(Groups, { global: { plugins } });
    await flushPromises();
    expect(w.find('[data-testid="group-removed-10"]').text()).toBe(en.groups.directory.removed);
    expect(w.find('[data-testid="group-removed-9"]').exists()).toBe(false);

    await w.get('[data-testid="groups-kind"] select').setValue('removed');
    expect(rowIds(w)).toEqual(['group-10']);
    await w.get('[data-testid="groups-kind"] select').setValue('synced');
    expect(rowIds(w)).toEqual(['group-9']);
    await w.get('[data-testid="groups-kind"] select').setValue('sso');
    expect(rowIds(w)).toEqual(['group-3']);
  });

  it('a synced group: says so, leaves its LDAP link to the directory, warns before a delete', async () => {
    groupsApi.get.mockResolvedValueOnce({ group: synced, members: [], grants: [] } as never);
    const { router, plugins } = setup(true);
    await router.push('/groups/9');
    const w = mount(GroupEdit, { global: { plugins }, attachTo: document.body });
    await flushPromises();
    expect(w.find('[data-testid="group-synced"]').text()).toContain('dept-legal');
    expect(w.get('[data-testid="group-ldap"] textarea').attributes('disabled')).toBeDefined();
    expect(w.find('[data-testid="group-detach"]').exists()).toBe(false);
    w.unmount();
  });

  it('a group whose LDAP group is gone: keep it as a filex group', async () => {
    groupsApi.get.mockResolvedValueOnce({ group: gone, members: [], grants: [] } as never);
    groupsApi.detach.mockResolvedValueOnce({ group: { ...gone, links: [], directory_id: undefined, directory_state: undefined }, members: [], grants: [] } as never);
    const { router, plugins } = setup(true);
    await router.push('/groups/10');
    const w = mount(GroupEdit, { global: { plugins } });
    await flushPromises();
    expect(w.find('[data-testid="group-directory-removed"]').text()).toContain('old-team');
    await w.get('[data-testid="group-detach"]').trigger('click');
    await flushPromises();
    expect(groupsApi.detach).toHaveBeenCalledWith(10);
    expect(w.find('[data-testid="group-directory-removed"]').exists()).toBe(false);
    expect(w.get('[data-testid="group-ldap"] textarea').attributes('disabled')).toBeUndefined();
  });
});

describe('what a group row says', () => {
  it('where its members come from: the groups it is linked to, the directory it follows, or by hand', async () => {
    groupsApi.list.mockResolvedValueOnce([
      { id: 1, name: 'Finance', description: 'Invoice approvers', role_id: null, priority: 0, links: [{ kind: 'sso', value: 'finance' }, { kind: 'ldap', value: 'dept-finance' }], member_count: 50, grant_count: 2 },
      { id: 2, name: 'Contractors', description: '', role_id: null, priority: 0, links: [], member_count: 2, grant_count: 0 },
      { id: 3, name: 'guests', description: 'From the directory: cn=guests,dc=partner', role_id: null, priority: 0, links: [{ kind: 'ldap', value: 'cn=guests,dc=partner' }], directory_id: 'ldap-partner:u-1', directory_name: 'partner-guests', member_count: 3, grant_count: 0 },
    ] as never);
    const { router, plugins } = setup(true);
    await router.push('/groups');
    const w = mount(Groups, { global: { plugins } });
    await flushPromises();
    expect(w.get('[data-testid="group-1"]').text()).toContain('Invoice approvers');
    expect(w.get('[data-testid="group-origin-1"]').text()).toBe('SSO: finance · LDAP: dept-finance');
    expect(w.get('[data-testid="group-origin-2"]').text()).toBe(en.groups.card.byHand);
    // Another provider's group, renamed here: which directory, and its name there.
    expect(w.get('[data-testid="group-origin-3"]').text()).toBe(`${en.groups.card.fromDirectory.replace('{name}', 'ldap-partner')} · cn=partner-guests`);
  });
});

