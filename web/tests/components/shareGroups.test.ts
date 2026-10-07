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

  it('asks in the dialog - never the browser - before making a group an owner', async () => {
    // ⚠ A native confirm() never shows in an embed's sandboxed iframe without
    // allow-modals (it answers false at once), is not themed and is not RTL:
    // the question lives in the dialog (docs/CONTRIBUTING.md, Modal busyClose).
    vi.useFakeTimers();
    const api = fakeApi();
    const native = vi.spyOn(window, 'confirm').mockImplementation(() => {
      throw new Error('the browser was asked');
    });
    const w = mount(PermissionsModal, {
      props: { api: api as unknown as FileApi, path: 'depo://Projeler', isDir: true, locale: 'en', initialTab: 'perms' },
      attachTo: document.body,
    });
    await flushPromises();
    const offerFinance = async () => {
      const input = w.get('[data-testid="share-add-person"] input');
      await input.setValue('fin');
      await input.trigger('input');
      vi.advanceTimersByTime(200);
      await flushPromises();
      await w.get('[data-testid="share-suggest-group"]').trigger('mousedown');
      await flushPromises();
    };
    // The level is three buttons side by side (#160, the owner's call).
    await w.get('[data-testid="share-add-level-owner"]').trigger('click');

    await offerFinance();
    const ask = w.get('[data-testid="share-group-owner-ask"]');
    expect(ask.text()).toContain('Finance');
    expect(api.addPermission, 'nothing is granted before the answer').not.toHaveBeenCalled();
    await w.get('[data-testid="share-group-owner-no"]').trigger('click');
    await flushPromises();
    expect(w.find('[data-testid="share-group-owner-ask"]').exists()).toBe(false);
    expect(api.addPermission, 'No grants nothing').not.toHaveBeenCalled();

    await offerFinance();
    await w.get('[data-testid="share-group-owner-yes"]').trigger('click');
    await flushPromises();
    expect(api.addPermission).toHaveBeenCalledWith(expect.objectContaining({ group_id: 4, level: 'owner' }));

    // Raising the group's existing grant to Owner asks the same way.
    await w.findAll('.fe-share__row')[0].get('[data-testid="share-row-level-owner"]').trigger('click');
    await flushPromises();
    expect(api.updateGroupPermission).not.toHaveBeenCalled();
    await w.get('[data-testid="share-group-owner-no"]').trigger('click');
    await flushPromises();
    expect(api.updateGroupPermission).not.toHaveBeenCalled();
    await w.findAll('.fe-share__row')[0].get('[data-testid="share-row-level-owner"]').trigger('click');
    await flushPromises();
    await w.get('[data-testid="share-group-owner-yes"]').trigger('click');
    await flushPromises();
    expect(api.updateGroupPermission).toHaveBeenCalledWith(5, 'owner');

    expect(native).not.toHaveBeenCalled();
    native.mockRestore();
    vi.useRealTimers();
    w.unmount();
  });

  it('lists the group by name and changes it through the group routes', async () => {
    const api = fakeApi();
    const w = mount(PermissionsModal, {
      props: { api: api as unknown as FileApi, path: 'depo://Projeler', isDir: true, locale: 'en', initialTab: 'perms' },
      attachTo: document.body,
    });
    await flushPromises();

    const rows = w.findAll('.fe-share__row');
    expect(rows).toHaveLength(2, 'the same id twice is still two rows');
    expect(rows[0].text()).toContain('Finance');
    expect(rows[0].text()).toContain(en['access.ui.group']);

    await rows[0].get('[data-testid="share-row-level-editor"]').trigger('click');
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

// #160 - the owner, 2026-10-06: "yan yana düğmeler, üzerine gelince açıklaması
// yazar". The access level is a segmented strip, never a dropdown, and each
// level says what it means (a hover tip, a description, a line on touch).
describe('the access level: three buttons side by side, each saying what it means', () => {
  it('the add row and every grant row draw the strip, with the meaning beside each level', async () => {
    const api = fakeApi();
    const w = mount(PermissionsModal, {
      props: { api: api as unknown as FileApi, path: 'depo://Projeler', isDir: true, locale: 'en', initialTab: 'perms' },
      attachTo: document.body,
    });
    await flushPromises();

    const add = w.get('[data-testid="share-add-level"]');
    expect(add.attributes('role')).toBe('radiogroup');
    expect(add.classes()).toContain('fe-choice--segmented');
    expect(w.find('[data-testid="share-add-person"] [role="combobox"]').exists(), 'no list for three answers').toBe(false);
    const radios = add.findAll('[role="radio"]');
    expect(radios.map((r) => r.text())).toEqual([
      en['access.ui.level_viewer'],
      en['access.ui.level_editor'],
      en['access.ui.level_owner'],
    ]);
    const meanings = [en['access.ui.level_viewer_desc'], en['access.ui.level_editor_desc'], en['access.ui.level_owner_desc']];
    radios.forEach((r, i) => {
      const tip = document.getElementById(r.attributes('aria-describedby')!);
      expect(tip?.textContent).toBe(meanings[i]);
    });
    // The chosen level's meaning, for a screen that cannot hover.
    expect(w.get('[data-testid="share-add-level-note"]').text()).toBe(en['access.ui.level_viewer_desc']);
    await w.get('[data-testid="share-add-level-owner"]').trigger('click');
    expect(w.get('[data-testid="share-add-level-owner"]').attributes('aria-checked')).toBe('true');
    expect(w.get('[data-testid="share-add-level-note"]').text()).toBe(en['access.ui.level_owner_desc']);

    // A grant's row: its own strip, named by the person, on the grant's level.
    const ada = w.findAll('.fe-share__row')[1];
    expect(ada.classes()).toContain('fe-share__row--grant');
    expect(ada.get('[data-testid="share-row-level"]').attributes('aria-label')).toContain('Ada');
    expect(ada.get('[data-testid="share-row-level-editor"]').attributes('aria-checked')).toBe('true');
    w.unmount();
  });
});
