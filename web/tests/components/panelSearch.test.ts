// Core's PanelSearch (task #168, docs/ADMIN-PANEL.md → Search) - the admin
// panel's search box, its panel and its keyboard.
//
// What is pinned here, in order:
//   1. the wiring a screen reader reads: the box names the search and says
//      whether its panel is open; the field is a combobox that owns a
//      listbox; rows are options in groups labelled by their headings; the
//      row under the arrow keys is the field's active descendant; a live
//      region counts the rows;
//   2. the owner's decisions: without a prefix at most three files and a
//      "Search files" row that turns the search into `file:`; a prefix keeps
//      its kind alone; recent searches when the field is empty, removable one
//      by one or all at once;
//   3. the keyboard: ↓/↑ move, Enter opens and the query is remembered, Esc
//      closes and gives focus back to the box, the palette key (Ctrl+K)
//      opens it from anywhere but another field being typed in;
//   4. a phone: a button and a layer over the window, with its own close;
//   5. a failure is said, never drawn as an empty answer.
import { describe, expect, it, vi } from 'vitest';
import { flushPromises, mount } from '@vue/test-utils';
import { nextTick } from 'vue';

import PanelSearch from '@brftech/filex-core/src/components/PanelSearch.vue';
import type { PanelRecentSearch, PanelRecentStore, PanelSearchItem, PanelSearchQuery } from '@brftech/filex-core/src/lib/panelSearch';

const ITEMS: PanelSearchItem[] = [
  { id: 'tab:plugins:apps', kind: 'page', label: 'Apps', detail: 'Plugins', target: { route: { name: 'plugins', query: { tab: 'apps' } } } },
  { id: 'page:users', kind: 'page', label: 'Users', detail: 'Accounts and their roles', aliases: ['people'] },
  { id: 'setting:require-2fa', kind: 'setting', label: 'Require two-factor authentication', aliases: ['2FA'] },
];

const SERVER: PanelSearchItem[] = [
  { id: 'app:convert', kind: 'app', label: 'Convert' },
  { id: 'user:12', kind: 'user', label: 'Convert Bot', detail: 'bot@example.com' },
];

function recentStore(initial: PanelRecentSearch[] = [{ id: 1, query: 'convert' }, { id: 2, query: 'file:rapor' }]) {
  let rows = [...initial];
  return {
    list: vi.fn(async () => rows.map((r) => ({ ...r }))),
    add: vi.fn(async (q: string) => {
      rows = [{ id: 100 + rows.length, query: q }, ...rows.filter((r) => r.query !== q)];
    }),
    remove: vi.fn(async (id: PanelRecentSearch['id']) => {
      rows = rows.filter((r) => r.id !== id);
    }),
    clear: vi.fn(async () => {
      rows = [];
    }),
  } satisfies PanelRecentStore;
}

/* Every page mounted here is taken down by the harness after each test
   (tests/setup.ts → teardownDom), <body> emptied with it. */
function mountSearch(props: Record<string, unknown> = {}) {
  const remote = vi.fn(async (_q: PanelSearchQuery) => SERVER);
  const files = vi.fn(async (text: string, limit: number) =>
    Array.from({ length: Math.min(limit, 5) }, (_, i): PanelSearchItem => ({ id: `file:${i}`, kind: 'file', label: `${text}-${i}.txt` })),
  );
  const recent = recentStore();
  const w = mount(PanelSearch, {
    props: { locale: 'en', items: ITEMS, remote, files, recent, ...props },
    attachTo: document.body,
  });
  return { w, remote, files, recent };
}

const q = <T extends Element = HTMLElement>(sel: string) => document.querySelector<T>(sel);
const qa = (sel: string) => Array.from(document.querySelectorAll<HTMLElement>(sel));
const layer = () => q('[data-testid="panel-search"]');
const input = () => q<HTMLInputElement>('[data-testid="panel-search-input"]')!;
const trigger = () => q<HTMLButtonElement>('[data-testid="panel-search-open"]')!;

/** The debounce, then the answers. */
async function settle() {
  await new Promise((r) => setTimeout(r, 260));
  await flushPromises();
  await nextTick();
}

async function open() {
  trigger().click();
  await flushPromises();
  await nextTick();
}

async function type(text: string) {
  const el = input();
  el.value = text;
  el.dispatchEvent(new Event('input'));
  await nextTick();
  await settle();
}

function key(el: Element, k: string, extra: KeyboardEventInit = {}) {
  el.dispatchEvent(new KeyboardEvent('keydown', { key: k, bubbles: true, cancelable: true, ...extra }));
}

describe('the box and its panel', () => {
  it('names the search, says when its panel is open, and the field is a combobox owning a listbox', async () => {
    mountSearch();
    expect(trigger().getAttribute('aria-label')).toBe('Search the admin panel');
    expect(trigger().getAttribute('aria-expanded')).toBe('false');
    expect(layer()).toBeNull();

    await open();
    expect(trigger().getAttribute('aria-expanded')).toBe('true');
    expect(layer()).not.toBeNull();
    const field = input();
    expect(field.getAttribute('role')).toBe('combobox');
    const list = document.getElementById(field.getAttribute('aria-controls')!);
    expect(list?.getAttribute('role')).toBe('listbox');
    expect(document.activeElement).toBe(field);
  });

  it('with an empty field: the recent searches, newest first', async () => {
    const { recent } = mountSearch();
    await open();
    expect(recent.list).toHaveBeenCalled();
    expect(qa('[data-testid="panel-search-recent"]').map((r) => r.textContent?.trim())).toEqual(['convert', 'file:rapor']);
    expect(q('[data-testid="panel-search-group-recent"] [role="presentation"]')?.textContent).toBe('Recent searches');
  });
});

describe('what a query finds', () => {
  it('"convert": the app, the person, the first three files and a row that searches every file', async () => {
    const { remote, files } = mountSearch();
    await open();
    await type('convert');
    expect(remote).toHaveBeenCalledTimes(1);
    expect(remote.mock.calls[0][0]).toMatchObject({ kind: null, text: 'convert' });
    expect(files).toHaveBeenCalledWith('convert', 3);

    const groups = qa('[role="listbox"] > [role="group"]').map((g) => g.dataset.testid);
    expect(groups).toEqual(['panel-search-group-app', 'panel-search-group-user', 'panel-search-group-file']);
    expect(qa('[data-testid="panel-search-row-file"]')).toHaveLength(3);
    const all = q('[data-testid="panel-search-files-all"]');
    expect(all?.textContent).toContain('Search files: convert');

    // Every group is labelled by its heading.
    for (const g of qa('[role="listbox"] > [role="group"]')) {
      const heading = document.getElementById(g.getAttribute('aria-labelledby')!);
      expect(heading?.textContent?.trim().length).toBeGreaterThan(0);
    }
    // The live region counts the rows (the "search files" row is not a result).
    expect(q('[role="status"]')?.textContent).toBe('5 results');
  });

  it('"Search files" turns the search into `file:` - files alone, a full page of them', async () => {
    const { files } = mountSearch();
    await open();
    await type('convert');
    q<HTMLElement>('[data-testid="panel-search-files-all"]')!.click();
    await nextTick();
    expect(input().value).toBe('file:convert');
    await settle();
    expect(files).toHaveBeenLastCalledWith('convert', 25);
    expect(qa('[role="listbox"] > [role="group"]').map((g) => g.dataset.testid)).toEqual(['panel-search-group-file']);
    expect(qa('[data-testid="panel-search-row-file"]')).toHaveLength(5);
    expect(q('[data-testid="panel-search-files-all"]')).toBeNull();
  });

  it('a prefix chip narrows to its kind; pressed again, it lets go', async () => {
    const { remote } = mountSearch();
    await open();
    await type('convert');
    q<HTMLElement>('[data-testid="panel-search-prefix-user"]')!.click();
    await nextTick();
    expect(input().value).toBe('user:convert');
    await settle();
    expect(remote.mock.calls.at(-1)![0]).toMatchObject({ kind: 'user', text: 'convert' });
    expect(qa('[role="listbox"] > [role="group"]').map((g) => g.dataset.testid)).toEqual(['panel-search-group-user']);
    expect(q('[data-testid="panel-search-prefix-user"]')?.getAttribute('aria-pressed')).toBe('true');

    q<HTMLElement>('[data-testid="panel-search-prefix-user"]')!.click();
    await nextTick();
    expect(input().value).toBe('convert');
  });

  it('a page by a synonym, a setting by its name', async () => {
    mountSearch();
    await open();
    await type('people');
    expect(q('[role="option"][aria-selected="true"]')?.dataset.id).toBe('page:users');
    await type('2fa');
    expect(q('[role="option"][aria-selected="true"]')?.dataset.id).toBe('setting:require-2fa');
  });

  it('a failure is said, and what did come is still shown', async () => {
    mountSearch({ remote: vi.fn(async () => Promise.reject(new Error('down'))) });
    await open();
    await type('convert');
    expect(q('[data-testid="panel-search-failed"]')?.getAttribute('role')).toBe('alert');
    expect(qa('[data-testid="panel-search-row-file"]').length).toBeGreaterThan(0);
  });

  it('nothing found is said, with the words', async () => {
    mountSearch({ remote: vi.fn(async () => []), files: vi.fn(async () => []) });
    await open();
    await type('zzzz');
    expect(q('[data-testid="panel-search-empty"]')?.textContent).toContain('zzzz');
  });
});

describe('the keyboard', () => {
  it('↓ moves the active row, Enter opens it, the query is remembered and the panel closes', async () => {
    const { w, recent } = mountSearch();
    await open();
    await type('convert');
    const field = input();
    const firstRow = q('[role="option"][aria-selected="true"]')!;
    expect(firstRow.dataset.id).toBe('app:convert');
    expect(field.getAttribute('aria-activedescendant')).toBe(firstRow.id);

    key(field, 'ArrowDown');
    await nextTick();
    const second = q('[role="option"][aria-selected="true"]')!;
    expect(second.dataset.id).toBe('user:12');
    expect(field.getAttribute('aria-activedescendant')).toBe(second.id);

    key(field, 'Enter');
    await flushPromises();
    await nextTick();
    const chosen = w.emitted('choose') as Array<[PanelSearchItem]>;
    expect(chosen).toHaveLength(1);
    expect(chosen[0][0].id).toBe('user:12');
    expect(recent.add).toHaveBeenCalledWith('convert');
    expect(layer()).toBeNull();
  });

  it('↑ from the first row wraps to the last', async () => {
    mountSearch();
    await open();
    await type('convert');
    key(input(), 'ArrowUp');
    await nextTick();
    expect(q('[role="option"][aria-selected="true"]')?.dataset.testid).toBe('panel-search-files-all');
  });

  it('Esc closes and gives focus back to the box', async () => {
    mountSearch();
    await open();
    key(input(), 'Escape');
    await flushPromises();
    await nextTick();
    expect(layer()).toBeNull();
    expect(document.activeElement).toBe(trigger());
  });

  it('the palette key opens it from anywhere on the page', async () => {
    mountSearch();
    key(document.body, 'k', { ctrlKey: true });
    await flushPromises();
    await nextTick();
    expect(layer()).not.toBeNull();
    expect(document.activeElement).toBe(input());
  });

  it('…but not while somebody is typing in another field: there the key is the field’s', async () => {
    mountSearch();
    const other = document.createElement('input');
    document.body.appendChild(other);
    other.focus();
    key(other, 'k', { ctrlKey: true });
    await flushPromises();
    await nextTick();
    expect(layer()).toBeNull();
  });

  it('typing on the box starts the search with that letter', async () => {
    mountSearch();
    key(trigger(), 'u');
    await flushPromises();
    await nextTick();
    expect(input().value).toBe('u');
  });
});

describe('recent searches', () => {
  it('Enter on one puts it back in the field; Delete removes it; the list can be cleared', async () => {
    const { recent } = mountSearch();
    await open();
    key(input(), 'Enter');
    await nextTick();
    expect(input().value).toBe('convert');

    input().value = '';
    input().dispatchEvent(new Event('input'));
    await nextTick();
    key(input(), 'Delete');
    await flushPromises();
    await nextTick();
    expect(recent.remove).toHaveBeenCalledWith(1);
    expect(qa('[data-testid="panel-search-recent"]').map((r) => r.textContent?.trim())).toEqual(['file:rapor']);

    q<HTMLElement>('[data-testid="panel-search-recent-clear"]')!.click();
    await flushPromises();
    await nextTick();
    expect(recent.clear).toHaveBeenCalled();
    expect(q('[data-testid="panel-search-group-recent"]')).toBeNull();
  });

  it('the × on a row removes that one', async () => {
    const { recent } = mountSearch();
    await open();
    qa('[data-testid="panel-search-recent-remove"]')[1].click();
    await flushPromises();
    expect(recent.remove).toHaveBeenCalledWith(2);
  });
});

describe('on a phone', () => {
  it('a button and a layer over the window, with a close of its own', async () => {
    mountSearch({ compact: true });
    expect(q('.fx-psearch__trigger-text')).toBeNull();
    await open();
    expect(layer()?.classList.contains('fx-psearch__layer--full')).toBe(true);
    expect(layer()?.getAttribute('aria-modal')).toBe('true');
    q<HTMLButtonElement>('[data-testid="panel-search-close"]')!.click();
    await flushPromises();
    await nextTick();
    expect(layer()).toBeNull();
  });
});
