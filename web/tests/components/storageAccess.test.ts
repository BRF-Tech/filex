// #117 — a storage's "Access" section (components/StorageAccess.vue) on the
// storage's own page (views/StorageEdit.vue): the whole storage given to
// people and groups.
//
// Through the REAL page, section, picker and api module; only the HTTP layer
// is fake, so what is asserted is the request the server receives:
//   - the storage-wide assignments are THE table (core DataTable), their
//     level changed in the row and the row's one verb "Revoke";
//   - "Revoke all access" deletes the storage-wide rows and nothing else;
//   - with per-item access control off, nobody can be given anything there:
//     the section says so and offers to turn it on - in a dialog that
//     collects who keeps access FIRST, then switches it on, then gives them
//     the storage. The form's switch follows, so the next Save cannot turn it
//     off again.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushPromises, mount } from '@vue/test-utils';
import { createPinia, setActivePinia } from 'pinia';
import { createI18n } from 'vue-i18n';
import { createMemoryHistory, createRouter } from 'vue-router';
import { DataTable } from '@brftech/filex-core';

import StorageEdit from '@/views/StorageEdit.vue';
import en from '@/locales/en.json';
import tr from '@/locales/tr.json';
import { closeRowMenus, openRowMenu, pickMenuItem } from '../helpers/rowMenu';

type Call = { method: string; url: string; body?: Record<string, unknown>; params?: Record<string, unknown> };
const calls: Call[] = [];
const state = {
  rbac: true,
  direct: [] as Array<Record<string, unknown>>,
};

vi.mock('@/api/client', () => ({
  extractError: (_e: unknown, f: string) => f,
  api: {
    get: vi.fn(async (url: string, cfg?: { params?: Record<string, unknown> }) => {
      calls.push({ method: 'get', url, params: cfg?.params });
      if (url === '/admin/storages/7') {
        return {
          data: {
            id: 7, name: 'medya', driver: 'local', enabled: true, read_only: false, rbac_enabled: state.rbac,
            sync_interval_s: 900, config: { path: '/srv/media' },
          },
        };
      }
      if (url === '/files/permissions') return { data: { storage_rbac: state.rbac, direct: state.direct, inherited: [] } };
      if (url === '/files/permissions/users') {
        return { data: { users: [{ id: 5, email: 'alice@example.com', display_name: 'Alice', role: 'user' }] } };
      }
      if (url === '/files/permissions/groups') return { data: { groups: [{ id: 2, name: 'Finance', description: '' }] } };
      return { data: [] };
    }),
    post: vi.fn(async (url: string, body: Record<string, unknown>) => {
      calls.push({ method: 'post', url, body });
      return { data: { id: 40, ...body } };
    }),
    patch: vi.fn(async (url: string, body: Record<string, unknown>) => {
      calls.push({ method: 'patch', url, body });
      if (url === '/admin/storages/7') {
        state.rbac = body.rbac_enabled === true || state.rbac;
        return { data: { id: 7, name: 'medya', driver: 'local', enabled: true, read_only: false, ...body } };
      }
      return { data: { ok: true } };
    }),
    delete: vi.fn(async (url: string) => {
      calls.push({ method: 'delete', url });
      return { data: { ok: true } };
    }),
  },
}));

// The "turn it on" dialog is a <dialog>; a DOM without showModal gets the
// attribute it would set.
if (typeof HTMLDialogElement !== 'undefined' && !HTMLDialogElement.prototype.showModal) {
  HTMLDialogElement.prototype.showModal = function () {
    this.setAttribute('open', '');
  };
  HTMLDialogElement.prototype.close = function () {
    this.removeAttribute('open');
  };
}

async function mountEdit(locale = 'en') {
  const i18n = createI18n({ legacy: false, locale, fallbackLocale: 'en', messages: { en, tr } });
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/storages', name: 'storages', component: { template: '<div />' } },
      { path: '/storages/:id', name: 'storages.edit', component: { template: '<div />' } },
      { path: '/grants', name: 'grants', component: { template: '<div />' } },
    ],
  });
  await router.push('/storages/7');
  await router.isReady();
  const w = mount(StorageEdit, { global: { plugins: [i18n, router] }, attachTo: document.body });
  await flushPromises();
  return w;
}

/** Type into a picker's search and let its pause pass. */
async function search(w: Awaited<ReturnType<typeof mountEdit>>, picker: string, text: string) {
  await w.get(`[data-testid="${picker}-search"] input`).setValue(text);
  await new Promise((r) => setTimeout(r, 220));
  await flushPromises();
}

const writes = () => calls.filter((c) => c.method !== 'get');

beforeEach(() => {
  setActivePinia(createPinia());
  calls.length = 0;
  state.rbac = true;
  state.direct = [];
});
afterEach(() => {
  closeRowMenus();
  vi.restoreAllMocks();
});

describe('the Access section, per-item access control on', () => {
  it('lists the storage-wide assignments in THE table, read at the storage root', async () => {
    state.direct = [
      { id: 4, kind: 'user', level: 'editor', user_id: 5, user_email: 'alice@example.com', user_name: 'Alice' },
      { id: 4, kind: 'group', level: 'viewer', group_id: 2, group_name: 'Finance' },
    ];
    const w = await mountEdit();
    const section = w.get('[data-testid="storage-access"]');
    expect(section.text()).toContain(en.storages.access.title);
    expect(calls).toContainEqual({ method: 'get', url: '/files/permissions', params: { path: 'medya://' } });

    const table = w.findAllComponents(DataTable).find((d) => d.props('tableId') === 'admin.storage.access');
    expect(table, 'THE table, remembered under its own id').toBeTruthy();
    expect(w.find('[data-testid="storage-access-row-u4"]').text()).toContain('Alice');
    expect(w.find('[data-testid="storage-access-row-g4"]').text()).toContain('Finance');
    // 0.57 shots: the group's name and its pill were welded ("DesignGroup").
    expect(w.find('[data-testid="storage-access-row-g4"]').text()).toContain(`Finance ${en.grants.group}`);
    expect(w.find('[data-testid="storage-access-rbac-off"]').exists()).toBe(false);
    // The folder grants are elsewhere, and the page says where.
    expect(w.get('[data-testid="storage-access-folder-grants"]').attributes('href')).toContain('/grants');
    w.unmount();
  });

  it('gives a person, and a group, the whole storage from the picker', async () => {
    const w = await mountEdit();
    await search(w, 'storage-access-add', 'al');
    await w.get('[data-testid="storage-access-add-level-editor"]').trigger('click');
    await w.get('[data-testid="storage-access-add-option-u5"]').trigger('click');
    await flushPromises();
    expect(writes()).toEqual([
      { method: 'post', url: '/files/permissions', body: { path: 'medya://', user_id: 5, level: 'editor' } },
    ]);

    calls.length = 0;
    await search(w, 'storage-access-add', 'fi');
    await w.get('[data-testid="storage-access-add-option-g2"]').trigger('click');
    await flushPromises();
    expect(writes()[0].body).toEqual({ path: 'medya://', group_id: 2, level: 'editor' });
    w.unmount();
  });

  it('changes a level in the row and revokes by the row’s one verb', async () => {
    vi.spyOn(window, 'confirm').mockReturnValue(true);
    state.direct = [{ id: 4, kind: 'user', level: 'viewer', user_id: 5, user_email: 'alice@example.com' }];
    const w = await mountEdit();
    await w.get('[data-testid="storage-access-level-u4-owner"]').trigger('click');
    await flushPromises();
    expect(writes()).toEqual([{ method: 'patch', url: '/files/permissions/4', body: { level: 'owner' } }]);

    calls.length = 0;
    await openRowMenu(w, 'storage-access-actions-u4');
    await pickMenuItem('storage-access-actions-u4-revoke');
    await flushPromises();
    expect(writes()).toEqual([{ method: 'delete', url: '/files/permissions/4' }]);
    w.unmount();
  });

  it('"Revoke all access" deletes the storage-wide rows, each by its own route, and nothing else', async () => {
    const confirm = vi.spyOn(window, 'confirm').mockReturnValue(true);
    state.direct = [
      { id: 4, kind: 'user', level: 'viewer', user_id: 5 },
      { id: 9, kind: 'group', level: 'editor', group_id: 2, group_name: 'Finance' },
    ];
    const w = await mountEdit();
    await w.get('[data-testid="storage-access-revoke-all"]').trigger('click');
    await flushPromises();
    expect(String(confirm.mock.calls[0][0])).toContain('Folder grants inside it are kept');
    expect(writes()).toEqual([
      { method: 'delete', url: '/files/permissions/4' },
      { method: 'delete', url: '/files/permissions/groups/9' },
    ]);
    w.unmount();
  });
});

describe('the Access section, per-item access control off', () => {
  beforeEach(() => {
    state.rbac = false;
  });

  it('offers no picker, says why, and offers to turn it on in place', async () => {
    const w = await mountEdit('tr');
    const off = w.get('[data-testid="storage-access-rbac-off"]');
    expect(off.text()).toContain(tr.storages.access.rbacOff);
    expect(w.get('[data-testid="storage-access-enable"]').text()).toBe('Öğe bazlı erişimi aç');
    expect(w.find('[data-testid="storage-access-add"]').exists()).toBe(false);
    w.unmount();
  });

  it('collects who keeps access first, then turns it on, then gives them the storage', async () => {
    const w = await mountEdit();
    await w.get('[data-testid="storage-access-enable"]').trigger('click');
    await flushPromises();
    const dialog = w.get('[data-testid="storage-access-enable-dialog"]');
    expect(dialog.get('[data-testid="storage-access-enable-warning"]').text()).toContain(
      'Choose who keeps access first, then turn it on',
    );
    expect(dialog.find('[data-testid="storage-access-keepers-none"]').exists()).toBe(true);

    await search(w, 'storage-access-keep', 'al');
    await w.get('[data-testid="storage-access-keep-option-u5"]').trigger('click');
    await flushPromises();
    expect(w.get('[data-testid="storage-access-keepers"]').text()).toContain('Alice');
    expect(writes(), 'nothing changes while the list is being made').toEqual([]);

    await w.get('[data-testid="storage-access-enable-confirm"]').trigger('click');
    await flushPromises();
    expect(writes()).toEqual([
      { method: 'patch', url: '/admin/storages/7', body: { rbac_enabled: true } },
      { method: 'post', url: '/files/permissions', body: { path: 'medya://', user_id: 5, level: 'viewer' } },
    ]);
    expect(w.find('[data-testid="storage-access-rbac-off"]').exists(), 'the section follows').toBe(false);
    expect(w.find('[data-testid="storage-access-add"]').exists()).toBe(true);

    // The form's switch followed: saving the form keeps it on.
    calls.length = 0;
    await w.find('form').trigger('submit');
    await flushPromises();
    const save = writes().find((c) => c.method === 'patch' && c.url === '/admin/storages/7');
    expect(save?.body?.rbac_enabled).toBe(true);
    w.unmount();
  });
});
