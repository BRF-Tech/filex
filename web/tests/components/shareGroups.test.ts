// Sharing with a group from the share dialog (backend internal/group): a
// group is offered beside people, granted by its id, listed by its name, and
// changed and removed through its OWN routes — a group's grant has its own id
// space, so the person's routes would hit a different row.
import { describe, expect, it, vi } from 'vitest';
import { flushPromises, mount } from '@vue/test-utils';

import PermissionsModal from '@brftech/filex-core/src/modals/PermissionsModal.vue';
import { en } from '@brftech/filex-core/src/locales/en';
import type { FileApi } from '@brftech/filex-core/src/composables/useFileApi';

function fakeApi() {
  const groupGrant = { id: 5, kind: 'group', group_id: 4, group_name: 'Finance', storage_id: 1, path_prefix: 'Projeler', level: 'viewer' };
  // The same id as the group's grant, on purpose: the two tables number apart.
  const personGrant = { id: 5, kind: 'user', user_id: 9, user_name: 'Ada', storage_id: 1, path_prefix: 'Projeler', level: 'editor' };
  const api = {
    listPermissions: vi.fn(async () => ({ direct: [groupGrant, personGrant], inherited: [], storage_rbac: true })),
    listShares: vi.fn(async () => ({ shares: [] })),
    searchUsers: vi.fn(async () => ({ users: [] })),
    searchGroups: vi.fn(async () => ({ groups: [{ id: 4, name: 'Finance', description: 'money' }] })),
    addPermission: vi.fn(async () => undefined),
    updatePermission: vi.fn(async () => undefined),
    deletePermission: vi.fn(async () => undefined),
    updateGroupPermission: vi.fn(async () => undefined),
    deleteGroupPermission: vi.fn(async () => undefined),
  };
  return api;
}

describe('sharing with a group', () => {
  it('offers the group beside people and grants it by its id', async () => {
    vi.useFakeTimers();
    const api = fakeApi();
    const w = mount(PermissionsModal, {
      props: { api: api as unknown as FileApi, path: 'depo://Projeler', isDir: true, locale: 'en', initialTab: 'perms' },
      attachTo: document.body,
    });
    await flushPromises();

    const input = w.get('[data-testid="share-add-person"] input');
    await input.setValue('fin');
    await input.trigger('input');
    vi.advanceTimersByTime(200);
    await flushPromises();
    const offered = w.get('[data-testid="share-suggest-group"]');
    expect(offered.text()).toContain('Finance');
    expect(offered.text()).toContain(en['access.ui.group']);

    await offered.trigger('mousedown');
    await flushPromises();
    expect(api.addPermission).toHaveBeenCalledWith(expect.objectContaining({ path: 'depo://Projeler', group_id: 4 }));
    expect(api.addPermission.mock.calls[0][0]).not.toHaveProperty('user_id');
    vi.useRealTimers();
    w.unmount();
  });

  it('asks before making a group an owner — every member could then manage the sharing', async () => {
    vi.useFakeTimers();
    const api = fakeApi();
    const confirm = vi.spyOn(window, 'confirm').mockReturnValue(false);
    const w = mount(PermissionsModal, {
      props: { api: api as unknown as FileApi, path: 'depo://Projeler', isDir: true, locale: 'en', initialTab: 'perms' },
      attachTo: document.body,
    });
    await flushPromises();
    await w.get('[data-testid="share-add-person"] select').setValue('owner');
    const input = w.get('[data-testid="share-add-person"] input');
    await input.setValue('fin');
    await input.trigger('input');
    vi.advanceTimersByTime(200);
    await flushPromises();
    await w.get('[data-testid="share-suggest-group"]').trigger('mousedown');
    await flushPromises();
    expect(confirm).toHaveBeenCalledOnce();
    expect(confirm.mock.calls[0][0]).toContain('Finance');
    expect(api.addPermission, 'No grants nothing').not.toHaveBeenCalled();

    await w.findAll('.fe-share__row')[0].get('select').setValue('owner');
    await flushPromises();
    expect(confirm).toHaveBeenCalledTimes(2);
    expect(api.updateGroupPermission).not.toHaveBeenCalled();
    confirm.mockRestore();
    vi.useRealTimers();
    w.unmount();
  });

  it('lists the group by name and changes it through the group routes', async () => {
    const api = fakeApi();
    vi.spyOn(window, 'confirm').mockReturnValue(true);
    const w = mount(PermissionsModal, {
      props: { api: api as unknown as FileApi, path: 'depo://Projeler', isDir: true, locale: 'en', initialTab: 'perms' },
      attachTo: document.body,
    });
    await flushPromises();

    const rows = w.findAll('.fe-share__row');
    expect(rows).toHaveLength(2, 'the same id twice is still two rows');
    expect(rows[0].text()).toContain('Finance');
    expect(rows[0].text()).toContain(en['access.ui.group']);

    await rows[0].get('select').setValue('editor');
    await flushPromises();
    expect(api.updateGroupPermission).toHaveBeenCalledWith(5, 'editor');
    expect(api.updatePermission).not.toHaveBeenCalled();

    await w.findAll('.fe-share__row')[0].get('.fe-share__del').trigger('click');
    await flushPromises();
    expect(api.deleteGroupPermission).toHaveBeenCalledWith(5);
    expect(api.deletePermission).not.toHaveBeenCalled();
    w.unmount();
  });
});
