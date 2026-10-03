// A user's page and list after 0.50's SSO rules (docs/SSO.md):
//   - an account an SSO sign-in opened switched off (its identity provider did
//     not confirm the address) says it waits for approval, in the list and on
//     its page, and switching it on IS the approval;
//   - any other switched-off account says it is off and can be switched on;
//   - an account bound to its SSO identity offers to remove the bind, after a
//     question, and the server is asked exactly that.
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { flushPromises, mount } from '@vue/test-utils';
import { createPinia, setActivePinia } from 'pinia';
import { createI18n } from 'vue-i18n';

import en from '@/locales/en.json';
import tr from '@/locales/tr.json';

const { usersApi, account } = vi.hoisted(() => {
  const account = {
    id: 7,
    email: 'ada@corp.example',
    display_name: 'Ada',
    role: 'user',
    created_at: '2026-10-01T00:00:00Z',
    updated_at: '2026-10-01T00:00:00Z',
    enabled: false,
    disabled_reason: 'pending_approval',
    sso_linked: true,
  };
  const usersApi = {
    get: vi.fn(async () => structuredClone(account)),
    update: vi.fn(async () => ({ ok: true })),
    list: vi.fn(),
    create: vi.fn(),
    remove: vi.fn(),
    resetPassword: vi.fn(),
  };
  return { usersApi, account };
});
vi.mock('@/api/users', () => ({ UsersApi: usersApi }));
vi.mock('@/api/roles', () => ({
  RolesApi: {
    listRules: vi.fn(async () => ({ rules: [], assignments: {} })),
    allOverrides: vi.fn(async () => ({})),
    userRoleDetail: vi.fn(async () => ({ role_id: null, group_role: null })),
    setUserRole: vi.fn(),
  },
}));
vi.mock('@/api/quota', () => ({
  quotaApi: { adminGet: vi.fn(async () => ({ quota_bytes: 0, used_bytes: 0, percent_used: 0, unlimited: true })) },
}));
vi.mock('vue-router', () => ({
  useRoute: () => ({ params: { id: '7' } }),
  useRouter: () => ({ push: vi.fn(), replace: vi.fn() }),
}));

import UserEdit from '@/views/UserEdit.vue';
import Users from '@/views/Users.vue';

const ModalStub = {
  props: ['modelValue', 'title'],
  template: '<div v-if="modelValue" data-testid="modal"><slot /><slot name="footer" /></div>',
};

async function open(row: Record<string, unknown> = {}, locale: 'en' | 'tr' = 'en') {
  usersApi.get.mockImplementation(async () => ({ ...structuredClone(account), ...row }));
  const i18n = createI18n({ legacy: false, locale, fallbackLocale: 'en', messages: { en, tr } });
  const w = mount(UserEdit, {
    global: {
      plugins: [i18n],
      stubs: { Modal: ModalStub, UserRolesCard: true, UserGroupsCard: true, ResetPasswordModal: true },
    },
  });
  await flushPromises();
  return w;
}

describe('a user’s page', () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    vi.clearAllMocks();
  });

  it('says an account waits for approval, and switching it on approves it', async () => {
    const w = await open();
    const off = w.get('[data-testid="user-account-off"]');
    expect(off.text()).toContain(en.users.account.pendingAbout);
    expect(off.text()).toContain(en.users.account.approve);
    usersApi.get.mockImplementation(async () => ({ ...structuredClone(account), enabled: true, disabled_reason: undefined }));
    await w.get('[data-testid="user-account-enable"]').trigger('click');
    await flushPromises();
    expect(usersApi.update).toHaveBeenCalledWith(7, { enabled: true });
    expect(w.find('[data-testid="user-account-off"]').exists(), 'switched on: nothing waits').toBe(false);
  });

  it('says another switched-off account is off, in Turkish too', async () => {
    const w = await open({ disabled_reason: undefined }, 'tr');
    const off = w.get('[data-testid="user-account-off"]');
    expect(off.text()).toContain(tr.users.account.disabledAbout);
    expect(off.text()).toContain(tr.users.account.enable);
    expect(off.text()).not.toContain(tr.users.account.pendingAbout);
  });

  it('an account that is on says nothing about it', async () => {
    const w = await open({ enabled: true, disabled_reason: undefined });
    expect(w.find('[data-testid="user-account-off"]').exists()).toBe(false);
  });

  it('removes the SSO bind only after the question', async () => {
    const w = await open({ enabled: true, disabled_reason: undefined });
    expect(w.get('[data-testid="user-sso"]').text()).toContain(en.users.sso.linked);
    await w.get('[data-testid="user-sso-unlink"]').trigger('click');
    expect(usersApi.update, 'asked first').not.toHaveBeenCalled();
    expect(w.get('[data-testid="modal"]').text()).toContain('ada@corp.example');
    usersApi.get.mockImplementation(async () => ({ ...structuredClone(account), enabled: true, sso_linked: false }));
    await w.get('[data-testid="user-sso-unlink-confirm"]').trigger('click');
    await flushPromises();
    expect(usersApi.update).toHaveBeenCalledWith(7, { sso_unlink: true });
    expect(w.find('[data-testid="user-sso"]').exists(), 'bound to nothing now').toBe(false);
  });

  it('an account bound to nothing offers nothing to remove', async () => {
    const w = await open({ enabled: true, sso_linked: false });
    expect(w.find('[data-testid="user-sso"]').exists()).toBe(false);
  });
});

describe('the users list', () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    vi.clearAllMocks();
  });

  it('marks an account waiting for approval, and one that is switched off', async () => {
    const rows = [
      { ...structuredClone(account), id: 7 },
      { ...structuredClone(account), id: 8, email: 'off@corp.example', disabled_reason: undefined },
      { ...structuredClone(account), id: 9, email: 'on@corp.example', enabled: true, disabled_reason: undefined },
    ];
    usersApi.list.mockImplementation(async () => ({ items: rows, total: rows.length, page: 1, page_size: 25 }));
    const i18n = createI18n({ legacy: false, locale: 'tr', fallbackLocale: 'en', messages: { en, tr } });
    const w = mount(Users, { global: { plugins: [i18n], stubs: { Modal: ModalStub, ResetPasswordModal: true } } });
    await flushPromises();
    const pending = w.get('[data-testid="user-pending-7"]');
    expect(pending.text()).toBe(tr.users.status.pending);
    expect(pending.attributes('title')).toBe(tr.users.status.pendingTitle);
    expect(w.get('[data-testid="user-disabled-8"]').text()).toBe(tr.users.status.disabled);
    expect(w.find('[data-testid="user-pending-8"]').exists()).toBe(false);
    expect(w.find('[data-testid="user-pending-9"]').exists()).toBe(false);
    expect(w.find('[data-testid="user-disabled-9"]').exists()).toBe(false);
  });
});
