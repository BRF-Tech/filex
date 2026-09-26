// A listing being read again says so, over the rows it is about to replace.
//
// ⚠ Opening a folder, Refresh and a search keep the rows on screen until the
// answer comes, and nothing marked them as old: the list carried an
// `is-loading` class no style drew. On a large object-store folder that is
// seconds of the previous folder's rows looking like the answer.
import { afterEach, describe, expect, it } from 'vitest';
import { mount, type VueWrapper } from '@vue/test-utils';

import DataTable from '@brftech/filex-core/src/components/DataTable.vue';
import GridView from '@brftech/filex-core/src/components/GridView.vue';
import type { FileNode } from '@brftech/filex-core/src/types/FileNode';

const mounted: VueWrapper[] = [];
afterEach(() => mounted.splice(0).forEach((w) => w.unmount()));

const rows = [
  { id: 1, name: 'eski-a.txt' },
  { id: 2, name: 'eski-b.txt' },
];

function table(loading: boolean, data = rows) {
  const w = mount(DataTable, {
    props: { columns: [{ id: 'name', label: 'Name', width: 200 }], rows: data, rowKey: 'id', tableId: 'refresh.t', loading },
  });
  mounted.push(w);
  return w;
}

const file = (name: string): FileNode =>
  ({ path: `depo://${name}`, basename: name, type: 'file', extension: 'txt', size: 1 }) as unknown as FileNode;

describe('a listing read again', () => {
  it('marks the rows it still shows as being replaced', () => {
    const w = table(true);
    const bar = w.find('.fe-list__refreshing');
    expect(bar.exists(), 'nothing said the rows on screen were the old ones').toBe(true);
    expect(bar.attributes('role')).toBe('status');
    expect(w.findAll('.fe-list__row')).toHaveLength(2);
  });

  it('draws nothing once the answer is in', () => {
    expect(table(false).find('.fe-list__refreshing').exists()).toBe(false);
  });

  it('with nothing yet on screen it says "Loading" as before, not a bar over nothing', () => {
    const w = table(true, []);
    expect(w.find('.fe-list__refreshing').exists()).toBe(false);
    expect(w.find('.fe-list__empty--loading').exists()).toBe(true);
  });

  it('the grid says so too', () => {
    const w = mount(GridView, { props: { files: [file('a.txt')], selected: new Set<string>(), locale: 'en', loading: true } });
    mounted.push(w);
    expect(w.find('.fe-grid__refreshing').exists()).toBe(true);
  });
});
