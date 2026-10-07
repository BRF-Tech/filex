// A person's groups on their page (components/UserGroupsCard.vue): the groups
// they are in, and how, are THE table (core DataTable, docs/CONTRIBUTING.md →
// "One table"); adding and removing go through the group's own routes.
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { flushPromises, mount } from '@vue/test-utils';
import { createPinia, setActivePinia } from 'pinia';
import { createMemoryHistory, createRouter } from 'vue-router';
import { createI18n } from 'vue-i18n';

import en from '@/locales/en.json';

const { groupsApi } = vi.hoisted(() => ({
  groupsApi: {
    forUser: vi.fn(async () => [{ id: 3, name: 'Finance', role_id: 7, source: 'sso' }]),
    list: vi.fn(async () => [
      { id: 3, name: 'Finance', description: '', role_id: 7, priority: 0, links: [] },
      { id: 4, name: 'Legal', description: '', role_id: null, priority: 0, links: [] },
    ]),
    addMembers: vi.fn(async () => ({})),
    removeMember: vi.fn(async () => ({})),
  },
}));
vi.mock('@/api/groups', () => ({ GroupsApi: groupsApi }));

import UserGroupsCard from '@/components/UserGroupsCard.vue';
import { DataTable } from '@brftech/filex-core';
import { closeRowMenus, openRowMenu, pickMenuItem } from '../helpers/rowMenu';
import { pickOption } from '../helpers/choiceSelect';

function mountCard() {
  setActivePinia(createPinia());
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/', component: { template: '<div />' } },
      { path: '/groups/:id', name: 'groups.edit', component: { template: '<div />' } },
    ],
  });
  const i18n = createI18n({ legacy: false, locale: 'en', messages: { en } });
  return mount(UserGroupsCard, {
    props: { userId: 9, userName: 'Ada' },
    global: { plugins: [router, i18n] },
    attachTo: document.body,
  });
}

beforeEach(() => vi.clearAllMocks());

describe("a person's groups", () => {
  it('are THE table, removed through the group', async () => {
    const confirm = vi.spyOn(window, 'confirm').mockReturnValue(true);
    const w = mountCard();
    await flushPromises();
    const tables = w.findAllComponents(DataTable);
    expect(tables.map((t) => t.props('tableId'))).toEqual(['admin.users.groups']);
    expect(w.find('ul').exists(), 'no list drawn by the card').toBe(false);
    expect(w.text()).toContain('Finance');
    expect(w.text()).toContain(en.groups.source.sso);

    await openRowMenu(w, 'user-group-actions-3');
    await pickMenuItem('user-group-actions-3-remove');
    await flushPromises();
    expect(confirm.mock.calls[0][0]).toContain('next sign-in');
    expect(groupsApi.removeMember).toHaveBeenCalledWith(3, 9);
    closeRowMenus();
    confirm.mockRestore();
    w.unmount();
  });

  it('adds the person to a group they are not in', async () => {
    const w = mountCard();
    await flushPromises();
    await pickOption(w.get('[data-testid="user-groups-card"]'), '4');
    await w.get('[data-testid="user-group-add"]').trigger('click');
    await flushPromises();
    expect(groupsApi.addMembers).toHaveBeenCalledWith(4, [9]);
    w.unmount();
  });
});
