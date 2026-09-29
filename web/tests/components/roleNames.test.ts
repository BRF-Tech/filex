// A custom role in other languages (backend migration 00071): the role editor
// writes the translations, and every screen that shows a custom role names it
// in the panel's language — the Roles table, the delete-and-move dialog, the
// Users list, a person's page and the permission grid's "role “X”" — falling
// back to the role's own name where a language has none.
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils';
import { createPinia, setActivePinia } from 'pinia';
import { createI18n } from 'vue-i18n';
import { createMemoryHistory, createRouter } from 'vue-router';

import en from '@/locales/en.json';
import tr from '@/locales/tr.json';
import { openRowMenu, pickMenuItem } from '../helpers/rowMenu';

const { catalogue, accounting, roles, usersApi } = vi.hoisted(() => {
  const catalogue = {
    permissions: [
      { key: 'files.download', group: 'files' },
      { key: 'files.delete', group: 'files', viewer_capped: true },
      { key: 'admin.full', group: 'admin', role_only: true },
    ],
    presets: [
      { name: 'standard', permissions: ['files.download', 'files.delete'] },
      { name: 'read_only', permissions: ['files.download'] },
    ],
  };
  const accounting = {
    id: 7,
    name: 'Accounting',
    description: 'Invoices and payments',
    names: { tr: 'Muhasebe' } as Record<string, string>,
    descriptions: { tr: 'Faturalar ve ödemeler' } as Record<string, string>,
    enabled: true,
    permissions: ['files.download'],
    targets: [],
    effects: {},
    settings: {},
    conditions: {},
  };
  const other = { ...accounting, id: 8, name: 'Auditors', names: undefined, descriptions: undefined, description: '' };
  const roles = {
    catalogue: vi.fn(async () => catalogue),
    listRules: vi.fn(async () => ({
      rules: [accounting, other],
      assignments: { '2': 7 } as Record<string, number>,
      builtinMembers: { admin: 1, user: 1, viewer: 0 },
    })),
    createRule: vi.fn(async (b: object) => ({ ...b, id: 9 })),
    updateRule: vi.fn(async (id: number, b: object) => ({ ...b, id })),
    deleteRule: vi.fn(async () => undefined),
    forUser: vi.fn(async () => ({
      user_id: 2,
      role: 'user',
      overrides: {},
      effective: {
        permissions: [
          { key: 'files.download', allowed: true, source: { kind: 'rule', rule_id: 7, rule_name: 'Accounting' } },
          { key: 'files.delete', allowed: false, source: { kind: 'rule', rule_id: 7, rule_name: 'Accounting' } },
        ],
        allowed: ['files.download'],
        preset: '',
        settings: {},
        rules: [7],
        conditional_rules: [],
      },
    })),
    setOverrides: vi.fn(),
    allOverrides: vi.fn(async () => ({})),
    userRole: vi.fn(async () => 7 as number | null),
    setUserRole: vi.fn(),
  };
  const people = [
    { id: 1, email: 'admin@local', display_name: '', username: 'admin', role: 'admin' },
    { id: 2, email: 'demo@local', display_name: '', username: 'demo', role: 'user' },
  ];
  const usersApi = {
    list: vi.fn(async () => ({ items: people, total: people.length, page: 1, page_size: 25 })),
    get: vi.fn(async (id: number) => people.find((p) => p.id === id)),
    create: vi.fn(),
    update: vi.fn(),
    remove: vi.fn(),
    resetPassword: vi.fn(),
  };
  return { catalogue, accounting, roles, usersApi };
});

vi.mock('@/api/roles', () => ({ RolesApi: roles }));
vi.mock('@/api/users', () => ({ UsersApi: usersApi }));
vi.mock('@/api/storages', () => ({ StoragesApi: { list: vi.fn(async () => []) } }));
vi.mock('@/api/quota', () => ({
  quotaApi: {
    adminGet: vi.fn(async () => ({ used_bytes: 0, quota_bytes: 0 })),
    adminSet: vi.fn(),
    adminRecompute: vi.fn(),
  },
}));

import Roles from '@/views/Roles.vue';
import Users from '@/views/Users.vue';
import UserEdit from '@/views/UserEdit.vue';
import RoleEditor from '@/components/RoleEditor.vue';
import UserRolesCard from '@/components/UserRolesCard.vue';

if (typeof HTMLDialogElement !== 'undefined' && !HTMLDialogElement.prototype.showModal) {
  HTMLDialogElement.prototype.showModal = function () {
    this.setAttribute('open', '');
  };
  HTMLDialogElement.prototype.close = function () {
    this.removeAttribute('open');
  };
}

async function mountAt(view: unknown, opts: { props?: object; path?: string; locale?: 'en' | 'tr' } = {}): Promise<VueWrapper> {
  const i18n = createI18n({ legacy: false, locale: opts.locale ?? 'en', fallbackLocale: 'en', messages: { en, tr } });
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/users/:id', name: 'user-edit', component: { template: '<div />' } },
      { path: '/users', name: 'users', component: { template: '<div />' } },
      { path: '/:p(.*)*', component: { template: '<div />' } },
    ],
  });
  await router.push(opts.path ?? '/');
  await router.isReady();
  const w = mount(view as never, { props: opts.props, global: { plugins: [i18n, router] }, attachTo: document.body });
  await flushPromises();
  return w;
}

function q<T extends Element = HTMLElement>(sel: string): T {
  const el = document.body.querySelector<T>(sel);
  expect(el, `nothing matches ${sel}`).not.toBeNull();
  return el as T;
}

async function type(sel: string, value: string) {
  const el = q<HTMLInputElement>(sel);
  el.value = value;
  el.dispatchEvent(new Event('input'));
  await flushPromises();
}

beforeEach(() => {
  setActivePinia(createPinia());
  vi.clearAllMocks();
  document.body.innerHTML = '';
});

describe('Role editor: other languages', () => {
  it('a new role is written with its names in other languages, blanks left out', async () => {
    await mountAt(RoleEditor, { props: { modelValue: true, rule: null, catalogue, storages: [] } });
    // Folded, with a row per language the panel offers.
    const part = q<HTMLDetailsElement>('[data-testid="role-translations"]');
    expect(part.tagName).toBe('DETAILS');
    expect(part.open).toBe(false);
    q('[data-testid="role-translation-en"]');
    q('[data-testid="role-translation-tr"]');

    await type('[data-testid="rule-name"] input', 'Contractors');
    await type('[data-testid="role-translation-name-tr"] input', 'Yükleniciler');
    await type('[data-testid="role-translation-description-tr"] input', 'Dışarıdan çalışanlar');
    await type('[data-testid="role-translation-name-en"] input', '   ');
    expect(q('[data-testid="role-translations-count"]').textContent).toContain('1 language');
    // A blank box shows the role's own name.
    expect(q<HTMLInputElement>('[data-testid="role-translation-name-en"] input').placeholder).toBe('Contractors');

    q('[data-testid="rule-save"]').click();
    await flushPromises();
    expect(roles.createRule).toHaveBeenCalledOnce();
    const body = roles.createRule.mock.calls[0][0] as { name: string; names?: object; descriptions?: object };
    expect(body.name).toBe('Contractors');
    expect(body.names).toEqual({ tr: 'Yükleniciler' });
    expect(body.descriptions).toEqual({ tr: 'Dışarıdan çalışanlar' });
  });

  it('editing keeps what is there, and clearing a language removes it', async () => {
    await mountAt(RoleEditor, { props: { modelValue: true, rule: accounting, catalogue, storages: [] } });
    expect(q<HTMLInputElement>('[data-testid="role-translation-name-tr"] input').value).toBe('Muhasebe');
    await type('[data-testid="role-translation-name-tr"] input', '');
    await type('[data-testid="role-translation-name-en"] input', 'Bookkeeping');
    q('[data-testid="rule-save"]').click();
    await flushPromises();
    expect(roles.updateRule).toHaveBeenCalledWith(
      7,
      expect.objectContaining({
        name: 'Accounting',
        names: { en: 'Bookkeeping' },
        descriptions: { tr: 'Faturalar ve ödemeler' },
      }),
    );
  });

  it('a language the role was translated into that is no longer offered stays visible', async () => {
    await mountAt(RoleEditor, {
      props: { modelValue: true, rule: { ...accounting, names: { tr: 'Muhasebe', es: 'Contabilidad' } }, catalogue, storages: [] },
    });
    expect(q<HTMLInputElement>('[data-testid="role-translation-name-es"] input').value).toBe('Contabilidad');
  });
});

describe('A custom role is named in the panel’s language', () => {
  it('the Roles table, with its description, and the delete-and-move dialog', async () => {
    const w = await mountAt(Roles, { locale: 'tr' });
    expect(q('[data-testid="role-name-rule-7"]').textContent).toContain('Muhasebe');
    expect(q('[data-testid="role-name-rule-7"]').textContent).not.toContain('Accounting');
    expect(q('[data-testid="role-description-rule-7"]').textContent).toBe('Faturalar ve ödemeler');
    expect(q('[data-testid="role-name-rule-8"]').textContent, 'no Turkish name: its own').toContain('Auditors');

    // Deleting Auditors offers Accounting — by its Turkish name.
    roles.listRules.mockResolvedValueOnce({
      rules: [accounting, { ...accounting, id: 8, name: 'Auditors', names: undefined, descriptions: undefined }],
      assignments: { '2': 8 },
      builtinMembers: { admin: 1, user: 1, viewer: 0 },
    } as never);
    w.unmount();
    document.body.innerHTML = '';
    const w2 = await mountAt(Roles, { locale: 'tr' });
    await openRowMenu(w2, 'role-actions-rule-8');
    await pickMenuItem('role-actions-rule-8-delete');
    await flushPromises();
    const labels = [...q<HTMLSelectElement>('[data-testid="role-delete-move"] select').options].map((o) => o.textContent?.trim());
    expect(labels).toContain('Muhasebe');
    expect(labels).not.toContain('Accounting');
  });

  it('English reads the role’s own name', async () => {
    await mountAt(Roles, { locale: 'en' });
    expect(q('[data-testid="role-name-rule-7"]').textContent).toContain('Accounting');
    expect(q('[data-testid="role-description-rule-7"]').textContent).toBe('Invoices and payments');
  });

  it('the Users list: the badge and the role filter', async () => {
    await mountAt(Users, { locale: 'tr' });
    expect(document.body.textContent).toContain('Muhasebe');
    const filter = [...document.body.querySelectorAll('select')].find((x) =>
      [...x.options].some((o) => o.value === 'custom:7'),
    ) as HTMLSelectElement;
    expect([...filter.options].find((o) => o.value === 'custom:7')?.textContent?.trim()).toBe('Muhasebe');
    expect(document.body.textContent).not.toContain('Accounting');
  });

  it('a person’s page: the Role field, the badge, the card and the grid’s source', async () => {
    await mountAt(UserEdit, { path: '/users/2', locale: 'tr' });
    const field = q<HTMLSelectElement>('form select');
    expect([...field.options].find((o) => o.value === 'custom:7')?.textContent?.trim()).toBe('Muhasebe');
    expect(q('[data-testid="user-edit-role"]').textContent?.trim()).toBe('Muhasebe');
    expect(q('[data-testid="user-permissions-preset"]').textContent?.trim()).toBe('Muhasebe');
    expect(q('[data-testid="perm-row-files.delete"]').textContent).toContain('Muhasebe');
    expect(document.body.textContent).not.toContain('Accounting');
  });

  it('the card names a role it only has the server’s name for by that name', async () => {
    await mountAt(UserRolesCard, { props: { userId: 2, role: 'user', customRole: { name: 'Accounting' } }, locale: 'tr' });
    expect(q('[data-testid="perm-row-files.delete"]').textContent).toContain('Accounting');
  });
});
