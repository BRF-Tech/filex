// The explorer's Trash view (QA, 2026-09-21, #32).
//
// ⚠⚠ What it showed: every row's date read "—" (a deleted item has no
// modification date, and the list only knew that column), nothing said how long
// an item had left or where it had been, the Owner column read "System" for
// every row (the trash listing carries no owner), and "+ New" stood above it all
// with every entry greyed. The admin's Trash page said "Deleted" and "Time left"
// the whole time — the explorer's table now carries the same facts.
import { describe, expect, it } from 'vitest';
import { mount } from '@vue/test-utils';

import ListView from '@brftech/filex-core/src/components/ListView.vue';
import SideNav from '@brftech/filex-core/src/components/SideNav.vue';
import { en } from '@brftech/filex-core/src/locales/en';
import type { FileNode } from '@brftech/filex-core/src/types/FileNode';

/** A row the way FileExplorer's `loadTrash` maps a trash entry. */
function trashed(rel: string, deletedAt: string, ttl: number | null): FileNode {
  const basename = rel.split('/').pop() ?? rel;
  return {
    id: 10,
    type: 'file',
    path: `depo://${rel}`,
    basename,
    extension: basename.split('.').pop() ?? '',
    storage: 'depo',
    size: 8,
    file_size: 8,
    last_modified: Date.parse(deletedAt),
    extra_metadata: { deleted_at: deletedAt, ttl_days: ttl },
  } as unknown as FileNode;
}

function trashList(files: FileNode[]) {
  return mount(ListView, {
    props: { selected: new Set<string>(), locale: 'en' as const, trash: true, files, apiBase: '' },
  });
}

describe('the Trash’s own columns', () => {
  it('says when it was deleted, where from and how long it has left — and draws no owner or star', () => {
    const w = trashList([trashed('Projeler/eski-rapor.txt', '2026-09-22T11:11:26Z', 29)]);
    const head = w.findAll('.fe-list__head .fe-list__col').map((c) => c.text());
    expect(head.join(' | ')).toContain(en['col.deleted']);
    expect(head.join(' | ')).toContain(en['col.deleted_from']);
    expect(head.join(' | ')).toContain(en['col.remaining']);
    expect(head.join(' | ')).not.toContain(en['col.owner']);
    expect(head.join(' | ')).not.toContain(en['col.modified']);

    const row = w.get('.fe-list__row');
    expect(row.get('.fe-list__col--mod').text()).not.toBe('—');
    expect(row.get('.fe-list__col--mod').text()).toContain('2026');
    expect(row.get('.fe-list__col--location').text()).toBe('depo/Projeler');
    expect(row.get('.fe-list__col--remaining').text()).toBe('29 days');
    expect(row.find('.fe-list__col--owner').exists()).toBe(false);
    expect(row.find('.fe-list__col--star').exists()).toBe(false);
  });

  it('one day, due now, and no count at all are three different sentences', () => {
    const w = trashList([
      trashed('a.txt', '2026-09-22T11:11:26Z', 1),
      trashed('b.txt', '2026-09-22T11:11:26Z', 0),
      trashed('c.txt', '2026-09-22T11:11:26Z', null),
    ]);
    const left = w.findAll('.fe-list__row .fe-list__col--remaining').map((c) => c.text());
    expect(left).toEqual(['1 day', en['trash.days_remaining_due'], '—']);
  });

  it('an ordinary folder keeps its ordinary columns', () => {
    const w = mount(ListView, {
      props: {
        selected: new Set<string>(),
        locale: 'en' as const,
        apiBase: '',
        files: [{ ...trashed('a.txt', '2026-09-22T11:11:26Z', 3), extra_metadata: undefined } as FileNode],
      },
    });
    const head = w.findAll('.fe-list__head .fe-list__col').map((c) => c.text()).join(' | ');
    expect(head).toContain(en['col.modified']);
    expect(head).toContain(en['col.owner']);
    expect(head).not.toContain(en['col.remaining']);
  });
});

describe('"+ New" in the Trash', () => {
  const nav = (activeView: string) =>
    mount(SideNav, {
      props: { expanded: true, activeView, storages: [{ name: 'depo' }], locale: 'en', canWrite: false },
    });

  it('is gone — nothing is made inside the Trash — not greyed', () => {
    expect(nav('trash').find('[data-testid="sidenav-new"]').exists()).toBe(false);
  });

  it('stays everywhere else', () => {
    expect(nav('').find('[data-testid="sidenav-new"]').exists()).toBe(true);
    expect(nav('recent').find('[data-testid="sidenav-new"]').exists()).toBe(true);
  });
});
