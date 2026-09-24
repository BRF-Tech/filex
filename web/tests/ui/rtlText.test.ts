/**
 * RTL — the person's text is isolated wherever the interface prints it.
 *
 * A file name, a path, a folder, a cell's value is the PERSON's text, not the
 * interface's: `<bdi>` lets its own letters decide its direction, so
 * `2026 report.pdf` keeps its number in front in an Arabic list and an Arabic
 * name keeps its order in an English one (measured before the fix: in the
 * English list `تقرير المبيعات 2026.pdf` drew as `2026 تقرير المبيعات.pdf`).
 * And a number pair a template draws (`3 / 10`) reads left to right.
 */
import { readFileSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { afterEach, describe, expect, it } from 'vitest';
import { mount, type VueWrapper } from '@vue/test-utils';

import ListView from '@brftech/filex-core/src/components/ListView.vue';
import DataTable from '@brftech/filex-core/src/components/DataTable.vue';
import { contentDir } from '@brftech/filex-core/src/lib/direction';

const mounted: VueWrapper[] = [];
afterEach(() => {
  while (mounted.length) mounted.pop()!.unmount();
  document.body.innerHTML = '';
});

describe('file names are isolated', () => {
  it('the explorer list draws each name inside a <bdi>', () => {
    const name = 'تقرير المبيعات 2026.pdf';
    const w = mount(ListView, {
      props: {
        files: [{ path: `s://${name}`, basename: name, type: 'file', size: 1, extension: 'pdf' }],
        selected: new Set<string>(),
        locale: 'en',
      },
    });
    mounted.push(w);
    const bdi = w.find('.fe-list__name bdi');
    expect(bdi.exists()).toBe(true);
    expect(bdi.text()).toBe(name);
  });

  it('a table’s plain cells are isolated, and its pager’s "n / m" reads left to right', () => {
    const rows = Array.from({ length: 30 }, (_, i) => ({ id: i + 1, name: `r${i}` }));
    const w = mount(DataTable, {
      props: {
        columns: [{ id: 'name', label: 'Name', width: 200 }],
        rows,
        rowKey: 'id',
        tableId: 'rtl.text.1',
        pageSize: 10,
        total: 30,
        page: 1,
      },
    });
    mounted.push(w);
    expect(w.find('.fe-list__row .fe-list__cell-text bdi').text()).toBe('r0');
    const pager = w.find('.tbl-pager__at');
    expect(pager.exists(), 'a 30-row list in pages of 10 draws its pager').toBe(true);
    expect(pager.attributes('dir')).toBe('ltr');
    expect(pager.text()).toBe('1 / 3');
  });
});

describe('a document reads in its own direction, not the interface’s', () => {
  const REPO = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../../..');
  const src = (f: string) => readFileSync(path.join(REPO, f), 'utf8');

  it('a card’s preview: code left to right, prose by its first letter, a table like the interface', () => {
    expect(contentDir('code')).toBe('ltr');
    expect(contentDir('text')).toBe('auto');
    expect(contentDir('table')).toBeUndefined();
    for (const f of ['packages/core/src/components/GridView.vue', 'packages/core/src/components/GalleryView.vue']) {
      expect(src(f), f).toMatch(/class="fe-fprev"\s*\n\s*:dir="contentDir\(p\.kind\)"/);
    }
  });

  it('rendered markdown — the viewer and a notebook — decides from its own first letter', () => {
    const preview = src('packages/core/src/modals/PreviewModal.vue');
    expect(preview.match(/class="fe-preview__md(?:-split-output fe-preview__md)?"[^>]*dir="auto"/g)?.length ?? 0).toBe(2);
    expect(src('packages/core/src/viewers/IpynbViewer.vue')).toMatch(/class="filex-viewer-ipynb__md"\s*\n\s*dir="auto"/);
  });
});
