// #117 — the explorer's side of a whole storage given to people and groups.
//
// A storage row offers "Access…" to whoever owns its root (an administrator,
// or a person given the storage as owner), and it opens the SAME
// access dialog the Share action opens - on the storage's root. There the
// dialog offers people and groups only: no public link and no file request,
// because a link to a whole storage would hand everything in it to whoever
// holds the address (the server refuses both, public_link_rule.go). The
// decision is the path's (lib/storageRoot), so any host that opens the
// dialog on a root gets the same one.
import { readFileSync } from 'node:fs';
import path from 'node:path';
import { describe, expect, it, vi } from 'vitest';
import { flushPromises, mount } from '@vue/test-utils';

import PermissionsModal from '@brftech/filex-core/src/modals/PermissionsModal.vue';
import { en } from '@brftech/filex-core/src/locales/en';
import { ALL_SHARING } from '@brftech/filex-core/src/lib/sharingHeld';
import {
  isStorageRootPath,
  managesStorageRoot,
  rootLevelFor,
  rootLevelsOf,
  sharingAtTarget,
  storageRootTarget,
  storageRowMenu,
} from '@brftech/filex-core/src/lib/storageRoot';
import type { FileApi } from '@brftech/filex-core/src/composables/useFileApi';

describe('lib/storageRoot', () => {
  it('knows a storage root by its path', () => {
    expect(isStorageRootPath('alpha://')).toBe(true);
    expect(isStorageRootPath('alpha:///')).toBe(true);
    expect(isStorageRootPath('alpha://Team')).toBe(false);
    expect(isStorageRootPath('alpha')).toBe(false);
    expect(isStorageRootPath('')).toBe(false);
  });

  it('offers people and groups only at a root, and narrows nothing elsewhere', () => {
    expect(sharingAtTarget('alpha://', ALL_SHARING)).toEqual({ links: false, uploadLinks: false, users: true });
    expect(sharingAtTarget('alpha://', { links: true, uploadLinks: true, users: false }).users).toBe(false);
    expect(sharingAtTarget('alpha://Team', ALL_SHARING)).toEqual(ALL_SHARING);
  });

  it('turns a storage row into its root on the wire', () => {
    expect(storageRootTarget({ path: 'alpha', basename: 'Alpha drive', storage: 'alpha' })).toEqual({
      path: 'alpha://',
      basename: 'Alpha drive',
      type: 'dir',
      mime_type: 'inode/storage',
      perm: 'owner',
    });
    expect(storageRootTarget({ path: 'alpha', basename: '' }).path).toBe('alpha://');
  });

  it('a storage row offers "Access…" to whoever owns its root - an owner, an administrator - and nobody else', () => {
    // The server's rule for the panel routes (requireOwner): owner level on
    // the root, or an administrator. The menu offering it to administrators
    // alone hid from an owner of the root what the API let them do.
    const t = (k: string) => en[k as keyof typeof en] ?? k;
    const offered = (admin: boolean, level: string | undefined, shareUsers = true) =>
      storageRowMenu(t, { manages: managesStorageRoot({ admin, level }), shareUsers })
        .filter((r) => !r.hidden)
        .map((r) => r.key);
    expect(offered(false, 'owner'), 'an owner of the root').toEqual(['open', 'open-tab', 'storage-access']);
    expect(offered(true, undefined), 'an administrator, whatever the list says').toEqual(['open', 'open-tab', 'storage-access']);
    expect(offered(true, 'none'), 'an administrator').toEqual(['open', 'open-tab', 'storage-access']);
    for (const level of ['editor', 'viewer', 'none', undefined]) {
      expect(offered(false, level), `a ${level ?? 'level-less'} person`).toEqual(['open', 'open-tab']);
    }
    expect(offered(false, 'owner', false), 'without share.users').toEqual(['open', 'open-tab']);
    const row = storageRowMenu(t, { manages: true, shareUsers: true }).find((r) => r.key === 'storage-access');
    expect(row?.label).toBe(en['ctx.storage_access']);
    // Nothing that would rename, move, delete or link a whole storage.
    for (const key of ['rename', 'delete', 'cut', 'copy', 'access', 'download']) {
      expect(storageRowMenu(t, { manages: true, shareUsers: true }).map((r) => r.key)).not.toContain(key);
    }
  });

  it('the explorer asks it of the row it draws, in both places a storage row is drawn', () => {
    // A mounted explorer is not needed to see the wiring: both branches (the
    // virtual root and the split pane) build the row's menu through
    // storageRowActions, which asks managesStorageRoot with the row's level.
    const src = readFileSync(path.resolve(__dirname, '../../../packages/core/src/FileExplorer.vue'), 'utf8');
    expect(src.match(/return storageRowActions\(sel\);/g), 'both storage-row branches').toHaveLength(2);
    const fn = src.slice(src.indexOf('function storageRowActions('), src.indexOf('function keepActionsFor('));
    expect(fn).toMatch(/rootLevelFor\(name, props\.config\.storages, rootLevels\.value\)/);
    expect(fn).toMatch(/managesStorageRoot\(\{ admin: callerAdmin\.value, level \}\)/);
    expect(src, 'the level a listing says is kept').toMatch(/rootLevels\.value = rootLevelsOf\(resp\.storage_info\);/);
  });

  it('reads the level at each root from storage_info, and the host’s own list first', () => {
    const learned = rootLevelsOf([
      { name: 'team', perm: 'owner' },
      { name: 'shared', perm: 'editor' },
      { name: 'old' },
      { name: 'odd', perm: 'root' },
      { perm: 'owner' },
    ]);
    expect(learned).toEqual({ team: 'owner', shared: 'editor' });
    expect(rootLevelsOf(null)).toEqual({});
    expect(rootLevelFor('team', undefined, learned)).toBe('owner');
    expect(rootLevelFor('team', [{ name: 'team', perm: 'editor' }], learned), 'the host’s list wins').toBe('editor');
    expect(rootLevelFor('shared', [{ name: 'shared' }], learned), 'a host that says nothing').toBe('editor');
    expect(rootLevelFor('gone', [], learned)).toBeUndefined();
  });
});

function fakeApi(inherited: unknown[] = []) {
  return {
    listPermissions: vi.fn(async () => ({
      direct: [{ id: 4, kind: 'user', user_id: 9, user_name: 'Alice', user_email: 'alice@example.com', storage_id: 1, path_prefix: '', level: 'editor' }],
      inherited,
      storage_rbac: true,
    })),
    listShares: vi.fn(async () => ({ shares: [] })),
    createShare: vi.fn(),
    revokeShare: vi.fn(),
    searchUsers: vi.fn(async () => ({ users: [] })),
  };
}

function dialog(api: ReturnType<typeof fakeApi>, path: string) {
  return mount(PermissionsModal, {
    props: { api: api as unknown as FileApi, path, isDir: true, locale: 'en', shareMaxTtlDays: 7, perm: 'owner' },
    attachTo: document.body,
  });
}
const has = (w: ReturnType<typeof dialog>, id: string) => w.find(`[data-testid="${id}"]`).exists();

describe('the access dialog on a storage root', () => {
  it('is People only, opened, and names the whole storage', async () => {
    const api = fakeApi();
    const w = dialog(api, 'depo://');
    await flushPromises();
    expect(has(w, 'share-lead'), 'no link switch').toBe(false);
    expect(has(w, 'share-link-section'), 'no link options').toBe(false);
    expect(has(w, 'share-drop'), 'no file request').toBe(false);
    expect(has(w, 'share-people')).toBe(true);
    expect(w.get('[data-testid="share-people-toggle"]').attributes('aria-expanded')).toBe('true');
    expect(has(w, 'share-storage-root-hint')).toBe(true);
    expect(api.listPermissions).toHaveBeenCalledWith('depo://');
    expect(api.listShares, 'a root has no links to list').not.toHaveBeenCalled();
    expect(w.get('.fe-share__title').text()).toBe(en['access.title_storage'].replace('{name}', 'depo'));
    expect(w.get('.fe-share__path').text()).toBe(en['access.ui.whole_storage']);
    expect(w.text()).toContain('Alice');
    w.unmount();
  });

  it('even when the host passed every kind of sharing', async () => {
    const api = fakeApi();
    const w = mount(PermissionsModal, {
      props: { api: api as unknown as FileApi, path: 'depo://', isDir: true, locale: 'en', perm: 'owner', sharing: ALL_SHARING },
      attachTo: document.body,
    });
    await flushPromises();
    expect(has(w, 'share-lead')).toBe(false);
    expect(has(w, 'share-drop')).toBe(false);
    w.unmount();
  });

  it('a folder still gets the whole dialog', async () => {
    const api = fakeApi();
    const w = dialog(api, 'depo://Projeler');
    await flushPromises();
    expect(has(w, 'share-lead')).toBe(true);
    expect(has(w, 'share-drop')).toBe(true);
    expect(has(w, 'share-storage-root-hint')).toBe(false);
    w.unmount();
  });

  it('below the root, a grant on the whole storage is inherited from "Whole storage", not from "/"', async () => {
    const api = fakeApi([
      { id: 7, kind: 'group', group_id: 2, group_name: 'Finance', storage_id: 1, path_prefix: '', level: 'viewer', inherited: true },
    ]);
    const w = dialog(api, 'depo://Projeler');
    await flushPromises();
    await w.get('[data-testid="share-people-toggle"]').trigger('click');
    const from = w.get('.fe-share__from');
    expect(from.text()).toBe(en['access.ui.whole_storage']);
    expect(from.attributes('title')).toContain(en['access.ui.whole_storage']);
    w.unmount();
  });
});
