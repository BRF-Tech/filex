// The admin's Trash page says who put each item there.
//
// ⚠ The page listed what was deleted, from which storage, when, and how long it
// had left — never who, which is the first thing an admin is asked about a
// missing file. The listing now names the deleter (`deleted_by_*`).
import { afterEach, describe, expect, it, vi } from 'vitest';
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils';
import { createPinia, setActivePinia } from 'pinia';
import { createI18n } from 'vue-i18n';

import Trash from '@/views/Trash.vue';
import en from '@/locales/en.json';
import tr from '@/locales/tr.json';

type Body = Record<string, unknown>;

let listed: Body[] = [];

vi.mock('@/api/client', () => ({
  extractError: (_e: unknown, f: string) => f,
  api: {
    get: vi.fn(async (url: string) => {
      if (url === '/admin/storages') return { data: [{ id: 2, name: 'Depo' }] };
      if (url === '/files/manager/trash') {
        return { data: { entries: listed, total: listed.length, limit: 50, offset: 0 } };
      }
      if (url === '/admin/trash/empty') return { data: { running: false } };
      throw new Error(`unexpected GET ${url}`);
    }),
    post: vi.fn(),
    delete: vi.fn(),
  },
}));

function entry(id: number, name: string, by: Body): Body {
  return {
    id,
    storage_id: 2,
    storage_name: 'Depo',
    path: `/${name}`,
    name,
    size: 1024,
    deleted_at: '2026-09-18T14:54:41Z',
    ttl_days: 24,
    ...by,
  };
}

const mounted: VueWrapper[] = [];

async function mountTrash(locale: 'en' | 'tr' = 'en') {
  const pinia = createPinia();
  setActivePinia(pinia);
  const i18n = createI18n({ legacy: false, locale, fallbackLocale: 'en', messages: { en, tr } });
  const w = mount(Trash, { global: { plugins: [pinia, i18n] } });
  mounted.push(w);
  await flushPromises();
  return w;
}

function cellOf(w: VueWrapper, id: number) {
  return w.get(`[data-testid="trash-deleted-by-${id}"]`);
}

afterEach(() => {
  while (mounted.length) mounted.pop()!.unmount();
  listed = [];
});

describe('Trash — who deleted it', () => {
  it('has a column for it', async () => {
    listed = [entry(1, 'a.pdf', { deleted_by_id: 7, deleted_by_name: 'Bob Marley' })];
    const w = await mountTrash();
    expect(w.text()).toContain(en.trash.col_deleted_by);
  });

  it('names the account, the asker included, and draws a dash when nobody is named', async () => {
    listed = [
      entry(1, 'mine.pdf', { deleted_by_id: 3, deleted_by_name: 'Ada Lovelace', deleted_by_self: true }),
      entry(2, 'bobs.pdf', { deleted_by_id: 7, deleted_by_name: 'Bob Marley' }),
      entry(3, 'gone.pdf', {}),
      entry(4, 'nameless.pdf', { deleted_by_id: 9 }),
    ];
    const w = await mountTrash();
    expect(cellOf(w, 1).text()).toBe('Ada Lovelace');
    expect(cellOf(w, 2).text()).toBe('Bob Marley');
    expect(cellOf(w, 3).text()).toBe('—');
    expect(cellOf(w, 3).attributes('title'), 'the dash does not say why').toBe(en.trash.deleted_by_nobody);
    expect(cellOf(w, 4).text(), 'an account with no name is not nobody').toBe('#9');
  });

  it('speaks Turkish on a Turkish panel', async () => {
    listed = [entry(1, 'a.pdf', {})];
    const w = await mountTrash('tr');
    expect(w.text()).toContain(tr.trash.col_deleted_by);
    expect(cellOf(w, 1).attributes('title')).toBe(tr.trash.deleted_by_nobody);
  });
});
