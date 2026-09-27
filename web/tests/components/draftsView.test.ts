// The Drafts view (issue #71): the person's drafts across every storage, in
// THE table — never a table of its own ("filex'te TEK TABLO"): the rows are
// DataTable's (`fe-list__row`), the verbs are its one Actions control, and the
// columns are name / where it will be saved / storage / modified.
import { afterEach, describe, expect, it } from 'vitest';
import { mount } from '@vue/test-utils';
import { nextTick } from 'vue';

import DraftsView from '@brftech/filex-core/src/components/DraftsView.vue';
import type { DraftDto } from '@brftech/filex-core/src/lib/drafts';
import { closeRowMenus, menuEntries, openRowMenu, pickMenuItem } from '../helpers/rowMenu';

const D = (key: string, name: string, dir: string, storage = 'docs'): DraftDto => ({
  key,
  name,
  path: `${storage}://.filex-drafts/7/${key}/${name}`,
  storage,
  target_dir: `${storage}://${dir}`,
  target: `${storage}://${dir ? dir + '/' : ''}${name}`,
  type: name.split('.').pop() ?? 'txt',
  size: 12,
  created_at: '2026-09-27T08:00:00Z',
  modified_at: '2026-09-27T09:30:00Z',
});

const TWO = [D('0123456789abcdef', 'notes.txt', 'Reports/2026'), D('fedcba9876543210', 'Plan.docx', '', 'arsiv')];

function view(extra: Record<string, unknown> = {}) {
  return mount(DraftsView, {
    attachTo: document.body,
    props: { drafts: TWO, locale: 'en', limit: 50, ...extra },
  });
}

afterEach(() => {
  closeRowMenus();
  document.body.innerHTML = '';
});

describe('the Drafts view', () => {
  it('draws the drafts in THE table, one row each', () => {
    const w = view();
    expect(w.find('.fe-list').exists()).toBe(true);
    expect(w.findAll('.fe-list__row')).toHaveLength(2);
    expect(w.find('table').exists()).toBe(false);
  });

  it('says the name, where it will be saved, the storage and when it changed', () => {
    const w = view();
    const row = w.get('[data-testid="draft-row-0123456789abcdef"]');
    expect(row.text()).toContain('notes.txt');
    expect(row.text()).toContain('Reports / 2026');
    expect(row.text()).toContain('docs');
    const other = w.get('[data-testid="draft-row-fedcba9876543210"]');
    expect(other.text()).toContain('arsiv');
    // The storage root reads as "/", not as an empty cell.
    expect(other.find('.fe-drafts__target').text()).toBe('/');
    const heads = w.findAll('.fe-list__head .fe-list__col').map((h) => h.text());
    expect(heads.join('|')).toMatch(/Name.*Save to.*Storage.*Modified/);
  });

  it('offers Open, Save to disk and Delete — and says which was picked', async () => {
    const w = view();
    await openRowMenu(w, 'draft-actions-0123456789abcdef');
    expect(menuEntries().map((e) => e.label)).toEqual(['Open', 'Save to disk', 'Delete']);
    expect(menuEntries().find((e) => e.label === 'Delete')?.danger).toBe(true);
    await pickMenuItem('draft-actions-0123456789abcdef-save');
    expect(w.emitted('save')?.[0]?.[0]).toMatchObject({ key: '0123456789abcdef' });

    await openRowMenu(w, 'draft-actions-0123456789abcdef');
    await pickMenuItem('draft-actions-0123456789abcdef-delete');
    expect(w.emitted('delete')?.[0]?.[0]).toMatchObject({ key: '0123456789abcdef' });

    await w.get('[data-testid="draft-open-fedcba9876543210"]').trigger('click');
    expect(w.emitted('open')?.[0]?.[0]).toMatchObject({ key: 'fedcba9876543210' });
  });

  it('greys a row’s verbs while one is running on it', async () => {
    const w = view({ busyKey: '0123456789abcdef' });
    await openRowMenu(w, 'draft-actions-0123456789abcdef');
    expect(menuEntries().every((e) => e.disabled)).toBe(true);
  });

  it('says how many of the limit are kept', () => {
    const w = view();
    expect(w.get('[data-testid="drafts-count-line"]').text()).toBe('2 of 50 drafts');
  });

  it('narrows by the shell’s name filter', async () => {
    const w = view({ nameFilter: 'plan' });
    await nextTick();
    expect(w.findAll('.fe-list__row')).toHaveLength(1);
    expect(w.text()).toContain('Plan.docx');
  });

  it('with no drafts, says what a draft is instead of an empty table', () => {
    const w = view({ drafts: [] });
    expect(w.find('.fe-list').exists()).toBe(false);
    expect(w.get('[data-testid="empty-drafts"]').text()).toContain('No drafts');
  });

  it('in Turkish, with the real letters', () => {
    const w = view({ locale: 'tr' });
    const heads = w.findAll('.fe-list__head .fe-list__col').map((h) => h.text()).join('|');
    expect(heads).toContain('Kaydedileceği yer');
    expect(heads).toContain('Depo');
    expect(w.text()).toContain('Henüz kaydedilmemiş yeni belgeler');
  });
});
