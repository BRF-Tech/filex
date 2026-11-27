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
      // #211 (audit B4): the server's suggestion, GET /api/admin/users/suggest.
      suggest: vi.fn(async (email: string) => {
        const answers: Record<string, { username: string; name: string }> = {
          'Jane.Doe+files@corp.example': { username: 'jane.doe', name: 'Jane Doe' },
          'john@corp.example': { username: 'john', name: 'John' },
          'gözlük@corp.example': { username: 'gozluk', name: 'Gözlük' },
          'new@corp.example': { username: 'new', name: 'New' },
        };
        return answers[email] ?? { username: '', name: '' };
      }),
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

// The server's account check (POST /api/auth/account/check), asked while the
// Add user form is typed (0.54 #209: the form keeps no rule of its own). A
// unit test never reaches the network (helpers/noNetwork), so it answers here.
const checkAccount = vi.fn(async (_q: { email?: string; username?: string; for?: string }) => ({}) as Record<string, { error: string; message: string }>);
vi.mock('@/api/auth', async (importOriginal) => {
  const real = await importOriginal<typeof import('@/api/auth')>();
  return { ...real, AuthApi: { ...real.AuthApi, checkAccount: (q: { email?: string; username?: string; for?: string }) => checkAccount(q) } };
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
/** The suggestion is asked a moment after typing stops (Users.vue SUGGEST_DELAY_MS). */
const suggested = async () => {
  await new Promise((r) => setTimeout(r, 300));
  await flushPromises();
};
const inputValue = (w: VueWrapper, name: string) => (w.find(`input[name="${name}"]`).element as HTMLInputElement).value;

beforeEach(() => {
  setActivePinia(createPinia());
  vi.clearAllMocks();
  checkAccount.mockImplementation(async () => ({}));
});

describe('Add user: a local account', () => {
  it('says what it is for, and fills in the name and username the server suggests for the address', async () => {
    const w = await openAdd();
    expect(w.text()).toContain('Add a local account');
    expect(w.text()).toContain('they appear when they sign in, or when their directory syncs');

    await w.find('input[name="new-user-email"]').setValue('Jane.Doe+files@corp.example');
    await suggested();
    expect(usersApi.suggest).toHaveBeenLastCalledWith('Jane.Doe+files@corp.example');
    expect(inputValue(w, 'new-user-name')).toBe('Jane Doe');
    expect(inputValue(w, 'new-user-username')).toBe('jane.doe');

    // The server's rule, not one of the page's: the letters a name contains
    // are transliterated, not turned into dots (the page's copy said g.zl.k).
    await w.find('input[name="new-user-email"]').setValue('gözlük@corp.example');
    await suggested();
    expect(inputValue(w, 'new-user-username')).toBe('gozluk');
    expect(inputValue(w, 'new-user-name')).toBe('Gözlük');

    // Typed by hand: the address no longer overwrites it.
    await w.find('input[name="new-user-username"]').setValue('jd');
    await w.find('input[name="new-user-email"]').setValue('john@corp.example');
    await suggested();
    expect(inputValue(w, 'new-user-username')).toBe('jd');
    expect(inputValue(w, 'new-user-name')).toBe('John');
  });

  it('a password is needed — or generated — unless an invitation is sent', async () => {
    usersApi.create.mockResolvedValue({ id: 9, email: 'new@corp.example' });
    const w = await openAdd();
    await w.find('input[name="new-user-email"]').setValue('new@corp.example');
    // The username and name sent are the SERVER's suggestion (#211), asked a
    // moment after the address is typed.
    await suggested();
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

  // Issue #25: an SSO account made ahead of its first sign-in, or one that
  // only uses API keys, has no password here - the dialog's third way in.
  it('makes an account with no password when asked to', async () => {
    usersApi.create.mockResolvedValue({ id: 9, email: 'sso@corp.example' });
    const w = await openAdd();
    await w.find('input[name="new-user-email"]').setValue('sso@corp.example');
    await byTestId(w, 'user-create-access-none').trigger('click');
    expect(w.find('input[name="new-user-password"]').isVisible()).toBe(false);
    expect(byTestId(w, 'user-create-none-hint').text()).toBe(en.users.add.noneHint);
    await byTestId(w, 'user-create').trigger('click');
    await flushPromises();
    const call = usersApi.create.mock.calls[0][0];
    expect(call.email).toBe('sso@corp.example');
    expect(call.password).toBeUndefined();
    expect(call.send_invite).toBeUndefined();
  });

  // Task #311 (Cypress spec 42, red on and off from 0.59.1): the address is
  // checked with the username still empty - the server refuses that: "Enter a
  // username." - and the address's suggestion fills the username in after
  // that answer is back. The old answer stood under the suggested name until
  // the next check came back, and the form, which sends nothing while a box
  // says what is wrong, dropped the Enter pressed in that window: the dialog
  // stayed open and nothing was made.
  it('a suggestion that lands late keeps the name typed meanwhile, and the Enter that follows makes the account', async () => {
    // The server's answer: an empty username is refused, a written one taken.
    checkAccount.mockImplementation(async (q) =>
      q.username !== undefined && !q.username.trim() ? { username: { error: 'username_invalid', message: 'Enter a username.' } } : {},
    );
    // Keyed by the address: a timer left by an earlier test asks too.
    let land!: (s: { username: string; name: string }) => void;
    const realSuggest = usersApi.suggest.getMockImplementation()!;
    usersApi.suggest.mockImplementation((email: string) =>
      email === 'late@corp.example' ? new Promise((r) => (land = r)) : realSuggest(email),
    );
    usersApi.create.mockResolvedValue({ id: 9, email: 'late@corp.example' });
    const w = await openAdd();
    await w.find('input[name="new-user-email"]').setValue('late@corp.example');
    await byTestId(w, 'user-create-access-none').trigger('click');
    // The address's check is back (the username was empty then); its
    // suggestion is still on the way while the name is typed.
    await suggested();
    expect(checkAccount).toHaveBeenCalledWith({ email: 'late@corp.example', username: '', for: 'new' });
    expect(usersApi.suggest).toHaveBeenCalledWith('late@corp.example');
    await w.find('input[name="new-user-name"]').setValue('SSO Fixture');

    land({ username: 'late', name: 'Late' });
    usersApi.suggest.mockImplementation(realSuggest);
    await flushPromises();
    // The name typed by hand stays; the username the person left alone follows the address.
    expect(inputValue(w, 'new-user-name')).toBe('SSO Fixture');
    expect(inputValue(w, 'new-user-username')).toBe('late');
    // RED before #311: "Enter a username." under the box that says "late".
    expect(w.find('[data-testid="user-create-form"]').text()).not.toContain('Enter a username.');

    // Enter, before the check of "late" has come back.
    await byTestId(w, 'user-create-form').trigger('submit');
    await flushPromises();
    expect(usersApi.create).toHaveBeenCalledTimes(1);
    expect(usersApi.create).toHaveBeenCalledWith(
      expect.objectContaining({ email: 'late@corp.example', username: 'late', display_name: 'SSO Fixture', password: undefined }),
    );
    expect(w.find('dialog').attributes('open'), 'the dialog closed').toBeUndefined();
  });

  it('a refusal for the username that IS in the box still holds the form back', async () => {
    checkAccount.mockImplementation(async (q) =>
      q.username === 'admin' ? { username: { error: 'username_taken', message: 'The username admin is taken.' } } : {},
    );
    const w = await openAdd();
    await w.find('input[name="new-user-email"]').setValue('new@corp.example');
    await w.find('input[name="new-user-username"]').setValue('admin');
    await byTestId(w, 'user-create-access-none').trigger('click');
    await suggested();
    expect(w.find('[data-testid="user-create-form"]').text()).toContain('The username admin is taken.');
    await byTestId(w, 'user-create-form').trigger('submit');
    await flushPromises();
    expect(usersApi.create).not.toHaveBeenCalled();
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
