// Add user makes a LOCAL account — people of an LDAP directory or an SSO
// provider arrive by sign-in and sync. What it offers: a username and a
// display name suggested from the address, a password typed or generated —
// or an invitation instead — a line on what the role gives, the hand-made
// groups to join, and no account for an address a directory owns unless the
// administrator says so. The list, for its part, names two groups a row and
// counts the rest.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils';
import { createPinia, setActivePinia } from 'pinia';
import { createI18n } from 'vue-i18n';
import { createMemoryHistory, createRouter } from 'vue-router';

import en from '@/locales/en.json';
import tr from '@/locales/tr.json';

const { usersApi, groupsApi } = vi.hoisted(() => {
  const people = [
    { id: 1, email: 'admin@local', display_name: '', username: 'admin', role: 'admin', auth_source: 'local' },
    { id: 2, email: 'ada@local', display_name: 'Ada', username: 'ada', role: 'user', auth_source: 'ldap' },
  ];
  return {
    usersApi: {
      list: vi.fn(async () => ({ items: people, total: people.length, page: 1, page_size: 25 })),
      get: vi.fn(),
      create: vi.fn(),
      update: vi.fn(),
      remove: vi.fn(),
      resetPassword: vi.fn(),
    },
    groupsApi: {
      list: vi.fn(async () => [
        { id: 5, name: 'Finance', directory_id: null },
        { id: 6, name: 'Staff', directory_id: 'cn=staff,ou=groups' },
        { id: 8, name: 'Auditors', directory_id: null },
      ]),
      forUser: vi.fn(async () => []),
      addMembers: vi.fn(async () => undefined),
      memberships: vi.fn(async () => ({
        '2': [
          { id: 5, name: 'Finance', source: 'manual', in_use: true },
          { id: 6, name: 'Staff', source: 'ldap' },
          { id: 9, name: 'Wiki', source: 'ldap' },
          { id: 10, name: 'VPN', source: 'ldap' },
        ],
      })),
    },
  };
});

vi.mock('@/api/users', () => ({ UsersApi: usersApi }));
vi.mock('@/api/groups', () => ({ GroupsApi: groupsApi }));
vi.mock('@/api/roles', () => ({
  RolesApi: {
    allOverrides: vi.fn(async () => ({})),
    listRules: vi.fn(async () => ({ rules: [], assignments: {}, groupAssignments: {} })),
    setUserRole: vi.fn(),
  },
}));

import Users from '@/views/Users.vue';
import { useToastStore } from '@/stores/toast';

if (typeof HTMLDialogElement !== 'undefined' && !HTMLDialogElement.prototype.showModal) {
  HTMLDialogElement.prototype.showModal = function () {
    this.setAttribute('open', '');
  };
  HTMLDialogElement.prototype.close = function () {
    this.removeAttribute('open');
  };
}

const mounted: VueWrapper[] = [];
afterEach(() => {
  for (const w of mounted.splice(0)) w.unmount();
});

async function openAdd(locale: 'en' | 'tr' = 'en'): Promise<VueWrapper> {
  const i18n = createI18n({ legacy: false, locale, fallbackLocale: 'en', messages: { en, tr } });
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/users/:id', name: 'users.edit', component: { template: '<div />' } },
      { path: '/groups/:id', name: 'groups.edit', component: { template: '<div />' } },
      { path: '/auth-providers/:name', name: 'auth-providers.edit', component: { template: '<div />' } },
      { path: '/:p(.*)*', component: { template: '<div />' } },
    ],
  });
  await router.push('/');
  await router.isReady();
  const w = mount(Users, { global: { plugins: [i18n, router] }, attachTo: document.body });
  mounted.push(w);
  await flushPromises();
  const add = w.findAll('button').filter((b) => b.text().trim() === (locale === 'en' ? 'Add user' : 'Kullanıcı ekle'));
  await add[add.length - 1].trigger('click');
  await flushPromises();
  return w;
}

const byTestId = (w: VueWrapper, id: string) => w.find(`[data-testid="${id}"]`);
const inputValue = (w: VueWrapper, name: string) => (w.find(`input[name="${name}"]`).element as HTMLInputElement).value;

beforeEach(() => {
  setActivePinia(createPinia());
  vi.clearAllMocks();
});

describe('Add user: a local account', () => {
  it('says what it is for, and suggests the name and username from the address', async () => {
    const w = await openAdd();
    expect(w.text()).toContain('Add a local account');
    expect(w.text()).toContain('they appear when they sign in, or when their directory syncs');

    await w.find('input[name="new-user-email"]').setValue('Jane.Doe+files@corp.example');
    expect(inputValue(w, 'new-user-name')).toBe('Jane Doe Files');
    expect(inputValue(w, 'new-user-username')).toBe('jane.doe');

    // Typed by hand: the address no longer overwrites it.
    await w.find('input[name="new-user-username"]').setValue('jd');
    await w.find('input[name="new-user-email"]').setValue('john@corp.example');
    expect(inputValue(w, 'new-user-username')).toBe('jd');
    expect(inputValue(w, 'new-user-name')).toBe('John');
  });

  it('a password is needed — or generated — unless an invitation is sent', async () => {
    usersApi.create.mockResolvedValue({ id: 9, email: 'new@corp.example' });
    const w = await openAdd();
    await w.find('input[name="new-user-email"]').setValue('new@corp.example');
    await byTestId(w, 'user-create').trigger('click');
    await flushPromises();
    expect(usersApi.create).not.toHaveBeenCalled();
    expect(byTestId(w, 'user-create-error').text()).toContain('generate one');

    await byTestId(w, 'user-create-generate').trigger('click');
    const pw = inputValue(w, 'new-user-password');
    expect(pw).toHaveLength(16);
    expect(w.find('input[name="new-user-password"]').attributes('type')).toBe('text');
    await byTestId(w, 'user-create').trigger('click');
    await flushPromises();
    expect(usersApi.create).toHaveBeenCalledWith(
      expect.objectContaining({ email: 'new@corp.example', password: pw, username: 'new', display_name: 'New' }),
    );
    expect(usersApi.create.mock.calls[0][0].send_invite).toBeUndefined();
  });

  it('an invitation that could not be e-mailed shows its first password, once', async () => {
    usersApi.create.mockResolvedValue({ id: 9, email: 'new@corp.example', invite: { emailed: false, temp_password: 'Abc123-first-pw!' } });
    const w = await openAdd();
    await w.find('input[name="new-user-email"]').setValue('new@corp.example');
    await byTestId(w, 'user-create-access-invite').trigger('click');
    expect(w.find('input[name="new-user-password"]').isVisible()).toBe(false);
    await byTestId(w, 'user-create').trigger('click');
    await flushPromises();

    const call = usersApi.create.mock.calls[0][0];
    expect(call.send_invite).toBe(true);
    expect(call.password).toBeUndefined();
    expect(byTestId(w, 'user-invited-password').text()).toBe('Abc123-first-pw!');
  });

  it('an invitation that went out says so and closes', async () => {
    usersApi.create.mockResolvedValue({ id: 9, email: 'new@corp.example', invite: { emailed: true } });
    const w = await openAdd();
    await w.find('input[name="new-user-email"]').setValue('new@corp.example');
    await byTestId(w, 'user-create-access-invite').trigger('click');
    await byTestId(w, 'user-create').trigger('click');
    await flushPromises();
    expect(useToastStore().toasts.map((x) => x.message)).toContain('Invitation sent to new@corp.example');
    expect(w.find('dialog').attributes('open'), 'the dialog closed').toBeUndefined();
  });

  it('says what the picked role gives', async () => {
    const w = await openAdd();
    expect(byTestId(w, 'user-create-role-hint').text()).toBe(en.users.add.roleHint.viewer);
  });

  it('offers only the hand-made groups, and puts the new account in the ones picked', async () => {
    usersApi.create.mockResolvedValue({ id: 9, email: 'new@corp.example' });
    const w = await openAdd();
    const offered = byTestId(w, 'user-create-groups').findAll('button').map((b) => b.text());
    expect(offered).toEqual(['Auditors', 'Finance']);

    await w.find('input[name="new-user-email"]').setValue('new@corp.example');
    await w.find('input[name="new-user-password"]').setValue('s3cret-pass-1');
    await byTestId(w, 'user-create-group-5').trigger('click');
    await byTestId(w, 'user-create').trigger('click');
    await flushPromises();
    expect(groupsApi.addMembers).toHaveBeenCalledWith(5, [9]);
    expect(groupsApi.addMembers).toHaveBeenCalledTimes(1);
  });

  it('an address a directory owns is said, and made only when asked', async () => {
    usersApi.create.mockRejectedValueOnce({
      response: {
        status: 409,
        data: { error: 'directory_email', directory: 'ldap-partner', label: 'Partner LDAP', message: 'pat@partner.example comes from Partner LDAP.' },
      },
    });
    const w = await openAdd();
    await w.find('input[name="new-user-email"]').setValue('pat@partner.example');
    await w.find('input[name="new-user-password"]').setValue('s3cret-pass-1');
    await byTestId(w, 'user-create').trigger('click');
    await flushPromises();
    expect(byTestId(w, 'user-create-directory').text()).toContain('comes from Partner LDAP');

    usersApi.create.mockResolvedValueOnce({ id: 9, email: 'pat@partner.example' });
    await byTestId(w, 'user-create-anyway').trigger('click');
    await flushPromises();
    expect(usersApi.create).toHaveBeenLastCalledWith(expect.objectContaining({ allow_directory_email: true }));
  });

  it('Create and add another keeps the dialog open, empty, with the same role and access', async () => {
    usersApi.create.mockResolvedValue({ id: 9, email: 'new@corp.example' });
    const w = await openAdd();
    await w.find('input[name="new-user-email"]').setValue('new@corp.example');
    await byTestId(w, 'user-create-access-invite').trigger('click');
    await byTestId(w, 'user-create-another').trigger('click');
    await flushPromises();
    expect(usersApi.create).toHaveBeenCalledTimes(1);
    expect(byTestId(w, 'user-create-form').exists()).toBe(true);
    expect(inputValue(w, 'new-user-email')).toBe('');
    expect(byTestId(w, 'user-create-access-invite').attributes('aria-checked')).toBe('true');
  });
});

describe('Users list: groups column', () => {
  it('names two groups — those in use first, as the server orders them — and counts the rest', async () => {
    const w = await openAdd();
    const cell = byTestId(w, 'user-groups-2');
    expect(cell.text()).toContain('Finance');
    expect(cell.text()).toContain('Staff');
    expect(cell.text()).not.toContain('Wiki');
    const more = byTestId(w, 'user-groups-more-2');
    expect(more.text()).toBe('+2');
    expect(more.attributes('title')).toBe('Wiki, VPN');
    expect(more.attributes('href')).toBe('/users/2');
  });
});
