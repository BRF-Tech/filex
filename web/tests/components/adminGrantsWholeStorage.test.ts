// #117 — Folder access (views/AdminGrants.vue) names a grant on a storage's
// root as what it is: the whole storage. It used to print "/" in the Path
// column - a path nobody typed - on the one page whose job is to say who has
// what, where; the confirmation asked to revoke access to "alpha://". A
// group's folders (views/GroupEdit.vue) say it the same way (lib/grantPlace).
import { afterEach, describe, expect, it, vi } from 'vitest';
import { flushPromises, mount } from '@vue/test-utils';
import { createPinia, setActivePinia } from 'pinia';
import { createI18n } from 'vue-i18n';

import en from '@/locales/en.json';
import tr from '@/locales/tr.json';
import { grantPlace, isWholeStorage } from '@/lib/grantPlace';
import { closeRowMenus, openRowMenu, pickMenuItem } from '../helpers/rowMenu';

const { grantsApi } = vi.hoisted(() => ({
  grantsApi: {
    list: vi.fn(async () => [
      {
        id: 1, storage_id: 1, storage_name: 'alpha', path: 'alpha://', path_prefix: '', is_dir: true,
        kind: 'user', user_id: 5, user_email: 'alice@example.com', user_name: 'Alice', level: 'editor', created_at: '',
      },
      {
        id: 2, storage_id: 1, storage_name: 'alpha', path: 'alpha://Team', path_prefix: 'Team', is_dir: true,
        kind: 'group', group_id: 3, group_name: 'Finance', level: 'viewer', created_at: '',
      },
    ]),
    remove: vi.fn(async () => {}),
  },
}));
vi.mock('@/api/grants', () => ({ AdminGrantsApi: grantsApi }));

import AdminGrants from '@/views/AdminGrants.vue';

const t = (key: string, values?: Record<string, unknown>) =>
  createI18n({ legacy: false, locale: 'en', messages: { en } }).global.t(key, values ?? {});

afterEach(() => {
  closeRowMenus();
  vi.restoreAllMocks();
});

describe('lib/grantPlace', () => {
  it('a grant on the root is the whole storage; any other is its address', () => {
    expect(isWholeStorage({ path_prefix: '' })).toBe(true);
    expect(isWholeStorage({ path_prefix: '/' })).toBe(true);
    expect(isWholeStorage({ path_prefix: 'Team' })).toBe(false);
    expect(grantPlace({ storage_name: 'alpha', path_prefix: '' }, t)).toBe('alpha (whole storage)');
    expect(grantPlace({ storage_name: 'alpha', path_prefix: 'Team/Reports' }, t)).toBe('alpha://Team/Reports');
  });
});

describe('Folder access', () => {
  async function page(locale: 'en' | 'tr' = 'en') {
    setActivePinia(createPinia());
    const i18n = createI18n({ legacy: false, locale, fallbackLocale: 'en', messages: { en, tr } });
    const w = mount(AdminGrants, { global: { plugins: [i18n] }, attachTo: document.body });
    await flushPromises();
    return w;
  }

  it('says "Whole storage" for a grant on the root, never "/"', async () => {
    const w = await page('tr');
    const badge = w.get('[data-testid="grant-whole-storage"]');
    expect(badge.text()).toBe('Tüm depo');
    const cells = w.findAll('.tbl-mono').map((c) => c.text());
    expect(cells).toContain('Team/…');
    expect(cells, 'the root is not a path of "/"').not.toContain('/');
    w.unmount();
  });

  it('asks to revoke "alpha (whole storage)", and finds it by those words', async () => {
    const confirm = vi.spyOn(window, 'confirm').mockReturnValue(false);
    const w = await page();
    await w.get('input[type="search"]').setValue('whole');
    await flushPromises();
    expect(w.findAll('[data-testid="grant-whole-storage"]')).toHaveLength(1);
    expect(w.text()).not.toContain('Finance');

    await openRowMenu(w, 'grant-actions-u1');
    await pickMenuItem('grant-actions-u1-revoke');
    expect(String(confirm.mock.calls[0][0])).toContain('alpha (whole storage)');
    expect(grantsApi.remove).not.toHaveBeenCalled();
    w.unmount();
  });
});
