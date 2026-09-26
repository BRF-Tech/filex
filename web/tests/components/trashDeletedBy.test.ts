// The Trash says who put each item there.
//
// ⚠ In a shared storage the one question a person asks of a missing file is
// "who deleted this?", and the Trash said what, where from and when, never who.
// The listing now names the deleter (`deleted_by_*`, the shape it gives an
// owner) and the Trash draws it where an ordinary folder draws the owner.
import { describe, expect, it } from 'vitest';
import { mount } from '@vue/test-utils';

import ListView from '@brftech/filex-core/src/components/ListView.vue';
import { en } from '@brftech/filex-core/src/locales/en';
import { tr } from '@brftech/filex-core/src/locales/tr';
import type { FileNode } from '@brftech/filex-core/src/types/FileNode';

/** A row the way FileExplorer's `loadTrash` maps a trash entry. */
function trashed(name: string, by: { id?: number; name?: string; self?: boolean }): FileNode {
  return {
    id: 10,
    type: 'file',
    path: `depo://Projeler/${name}`,
    basename: name,
    extension: 'txt',
    storage: 'depo',
    size: 8,
    file_size: 8,
    last_modified: Date.parse('2026-09-22T11:11:26Z'),
    extra_metadata: { deleted_at: '2026-09-22T11:11:26Z', ttl_days: 29 },
    ...(by.id !== undefined ? { deleted_by_id: by.id } : {}),
    ...(by.name !== undefined ? { deleted_by_name: by.name } : {}),
    ...(by.self ? { deleted_by_self: true } : {}),
  } as unknown as FileNode;
}

function trashList(files: FileNode[], locale: 'en' | 'tr' = 'en') {
  return mount(ListView, {
    props: { selected: new Set<string>(), locale, trash: true, files, apiBase: '' },
  });
}

describe('who put it in the Trash', () => {
  it('is a column of the Trash, not the owner', () => {
    const w = trashList([trashed('a.txt', { id: 7, name: 'Bob Marley' })]);
    const head = w.findAll('.fe-list__head .fe-list__col').map((c) => c.text()).join(' | ');
    expect(head).toContain(en['col.deleted_by']);
    expect(head).not.toContain(en['col.owner']);
  });

  it('names the person, says "You" for the asker, and a dash when nobody is named', () => {
    const w = trashList([
      trashed('mine.txt', { id: 3, name: 'Ada Lovelace', self: true }),
      trashed('bobs.txt', { id: 7, name: 'Bob Marley' }),
      trashed('gone.txt', {}),
    ]);
    const cell = (name: string) =>
      w.findAll('.fe-list__row').find((r) => r.text().includes(name))!.get('.fe-list__col--owner');
    expect(cell('mine.txt').text()).toBe(en['owner.you']);
    expect(cell('bobs.txt').text()).toBe('Bob Marley');
    expect(cell('gone.txt').text()).toBe('—');
    expect(cell('gone.txt').attributes('title'), 'the dash does not say why').toBe(en['trash.deleted_by_nobody']);
  });

  it('an account it has no name for is not called nobody', () => {
    const w = trashList([trashed('a.txt', { id: 9 })]);
    expect(w.get('.fe-list__row .fe-list__col--owner').text()).toBe(en['owner.unknown']);
  });

  it('speaks Turkish in a Turkish explorer', () => {
    const w = trashList([trashed('mine.txt', { id: 3, name: 'Ada', self: true })], 'tr');
    const head = w.findAll('.fe-list__head .fe-list__col').map((c) => c.text()).join(' | ');
    expect(head).toContain(tr['col.deleted_by']);
    expect(w.get('.fe-list__row .fe-list__col--owner').text()).toBe(tr['owner.you']);
  });
});
