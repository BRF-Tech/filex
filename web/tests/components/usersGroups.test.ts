// The Users list says where each account comes from (Source) and which
// groups it is in (Groups, with how — added, SSO or LDAP), and can be
// narrowed to one group.
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
    { id: 3, email: 'bob@local', display_name: 'Bob', username: 'bob', role: 'user', auth_source: 'sso' },
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
      list: vi.fn(async () => []),
      forUser: vi.fn(async () => []),
      memberships: vi.fn(async () => ({
        '2': [
          { id: 5, name: 'Finance', source: 'manual' },
          { id: 6, name: 'Staff', source: 'ldap' },
        ],
        '3': [{ id: 7, name: 'Auditors', source: 'sso' }],
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
  },
}));

import Users from '@/views/Users.vue';
import { optionLabels, pickOption } from '../helpers/choiceSelect';

const mounted: VueWrapper[] = [];
afterEach(() => {
  for (const w of mounted.splice(0)) w.unmount();
});

async function mountUsers(locale: 'en' | 'tr' = 'en') {
  const i18n = createI18n({ legacy: false, locale, fallbackLocale: 'en', messages: { en, tr } });
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/groups/:id', name: 'groups.edit', component: { template: '<div />' } },
      { path: '/:p(.*)*', component: { template: '<div />' } },
    ],
  });
  await router.push('/');
  await router.isReady();
  mounted.push(mount(Users, { global: { plugins: [i18n, router] }, attachTo: document.body }));
  await flushPromises();
}

const rowText = () => [...document.body.querySelectorAll('[role="row"]')].map((r) => r.textContent ?? '');

beforeEach(() => {
  setActivePinia(createPinia());
  vi.clearAllMocks();
});

describe('Users list: source and groups', () => {
  it('shows each account’s source and its groups, each linked to its page', async () => {
    await mountUsers();
    expect(document.body.querySelector('[data-testid="source-account-ldap"]')?.textContent?.trim()).toBe('LDAP');
    expect(document.body.querySelector('[data-testid="source-account-sso"]')?.textContent?.trim()).toBe('SSO');

    const ada = document.body.querySelector('[data-testid="user-groups-2"]');
    expect(ada?.textContent).toContain('Finance');
    expect(ada?.textContent).toContain('Staff');
    const staff = [...(ada?.querySelectorAll('a') ?? [])].find((a) => a.textContent?.includes('Staff'));
    expect(staff?.getAttribute('href')).toBe('/groups/6');
    expect(staff?.getAttribute('title')).toContain('LDAP');
    expect(document.body.querySelector('[data-testid="user-groups-1"]')).toBeNull();
  });

  it('narrows the list to one group', async () => {
    await mountUsers();
    const filter = document.body.querySelector('[data-testid="users-group-filter"]')!;
    expect(await optionLabels(filter)).toEqual(['All groups', 'Auditors', 'Finance', 'Staff']);
    await pickOption(filter, '7');
    await flushPromises();
    const rows = rowText();
    expect(rows.some((r) => r.includes('bob@local'))).toBe(true);
    expect(rows.some((r) => r.includes('ada@local'))).toBe(false);
  });

  it('speaks Turkish', async () => {
    await mountUsers('tr');
    const head = [...document.body.querySelectorAll('[role="columnheader"]')].map((h) => h.textContent?.trim());
    expect(head).toContain('Gruplar');
    expect(head).toContain('Kaynak');
  });

  it('pages the whole list itself: Next shows the next 25, and a sort orders every account', async () => {
    const many = Array.from({ length: 60 }, (_, i) => ({
      id: 100 + i,
      email: `p${String(i).padStart(2, '0')}@example.com`,
      display_name: '',
      username: `p${i}`,
      role: 'user',
      auth_source: 'ldap',
    }));
    // The server answers every account in one list, whatever page is asked.
    usersApi.list.mockResolvedValueOnce({ items: many, total: many.length, page: 1, page_size: many.length } as never);
    await mountUsers();
    const emails = () => rowText().filter((r) => r.includes('@example.com')).map((r) => r.match(/p\d\d@example\.com/)![0]);
    expect(emails()).toHaveLength(25);
    expect(emails()[0]).toBe('p00@example.com');
    expect(document.body.textContent).toContain('1 - 25 / 60');

    const next = [...document.body.querySelectorAll('button')].find((b) => /next/i.test(b.getAttribute('aria-label') ?? b.title ?? ''))!;
    next.click();
    await flushPromises();
    expect(emails()[0]).toBe('p25@example.com');
    expect(document.body.textContent).toContain('26 - 50 / 60');
    expect(usersApi.list).toHaveBeenCalledTimes(1);

    // Sorting by email, descending, orders all 60 — page 1 starts at the end.
    const header = [...document.body.querySelectorAll('[role="columnheader"]')].find((h) => h.textContent?.includes('Email'))!;
    const button = (header.querySelector('button') ?? header) as HTMLElement;
    button.click();
    await flushPromises();
    button.click();
    await flushPromises();
    expect(emails()[0]).toBe('p59@example.com');
    expect(document.body.textContent).toContain('1 - 25 / 60');
  });
});

// A disabled account says so next to its email, and says when LDAP disabled
// it (directory sync: disabled_reason 'directory') rather than an administrator.
describe('Users list: disabled accounts', () => {
  it('marks disabled accounts, and those LDAP disabled', async () => {
    const people = [
      { id: 1, email: 'admin@local', display_name: '', username: 'admin', role: 'admin', auth_source: 'local', enabled: true },
      { id: 2, email: 'ada@local', display_name: 'Ada', username: 'ada', role: 'user', auth_source: 'ldap', enabled: false, disabled_reason: 'directory' },
      { id: 3, email: 'bob@local', display_name: 'Bob', username: 'bob', role: 'user', auth_source: 'local', enabled: false },
    ];
    usersApi.list.mockResolvedValueOnce({ items: people, total: people.length, page: 1, page_size: 25 });
    await mountUsers();
    expect(document.body.querySelector('[data-testid="user-disabled-1"]')).toBeNull();
    expect(document.body.querySelector('[data-testid="user-disabled-2"]')?.textContent?.trim()).toBe('Disabled by LDAP');
    expect(document.body.querySelector('[data-testid="user-disabled-3"]')?.textContent?.trim()).toBe('Disabled');
  });
});
