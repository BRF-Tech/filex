// The list view's type badge on a page thumbnail (0.50).
//
// 0.50 is the first release that draws real pages in the list: the first page
// of an office document or a PDF, a text file's first lines, an archive's
// list. At 24 pixels each of those is a white square on a white row, and the
// row looked as if it had lost its icon (0.49 drew a coloured card with the
// extension on it). The owner's call: in the LIST, a small coloured badge
// naming the kind sits in a corner of such a thumbnail; no frame; the grid and
// the gallery are left alone.
//
// What these tests hold the code to:
//   - one rule (lib/filePreview thumbTypeBadge): every thumbnail but a
//     photograph or a video frame gets a badge - office, PDF, text, Markdown,
//     archives, and whatever an app draws, a kind with no family of its own in
//     the unknown type's neutral grey; packages (.jar, .apk, .whl…) are
//     archives (lib/fileIcons) but keep their own name in the Type column;
//   - the badge's words and colour come from the type tile's own sources
//     (the uppercased extension, the family's `fe-ftile--<family>` colour);
//   - it is drawn only while the thumbnail is: a row still showing its
//     coloured type tile does not say the same thing twice;
//   - it is decorative, and it sits at the inline END, so a right-to-left
//     list puts it in the other corner by itself;
//   - the folder peek is a list too: it pictures and badges by the same rule.
import { readFileSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushPromises, mount } from '@vue/test-utils';
import { nextTick } from 'vue';

import ListView from '@brftech/filex-core/src/components/ListView.vue';
import GridView from '@brftech/filex-core/src/components/GridView.vue';
import FolderPeek from '@brftech/filex-core/src/components/FolderPeek.vue';
import { __resetNearViewport } from '@brftech/filex-core/src/lib/nearViewport';
import { drawsOnPaper, thumbTypeBadge } from '@brftech/filex-core/src/lib/filePreview';
import { iconFamilyFor, typeLabelFor } from '@brftech/filex-core/src/lib/fileIcons';
import type { FileNode } from '@brftech/filex-core/src/types/FileNode';
import { teardownDom } from '../helpers/teardown';

const BASE_CSS = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../../../packages/core/src/styles/base.css');

let seq = 0;
function file(name: string, extra: Partial<FileNode> = {}): FileNode {
  const dot = name.lastIndexOf('.');
  const id = ++seq;
  return {
    id,
    path: `Depo://belgeler/${name}`,
    basename: name,
    type: 'file',
    extension: dot > 0 ? name.slice(dot + 1) : '',
    size: 2048,
    last_modified: 1_790_000_000_000,
    thumb_url: `/api/files/thumb/${id}?v=1`,
    ...extra,
  };
}

beforeEach(() => {
  __resetNearViewport();
  // Every tile is near the viewport at once, so ThumbTile asks for its picture.
  class FakeIO {
    constructor(private cb: IntersectionObserverCallback) {}
    observe(el: Element) {
      queueMicrotask(() =>
        this.cb([{ target: el, isIntersecting: true, intersectionRatio: 1 } as unknown as IntersectionObserverEntry], this as never),
      );
    }
    unobserve() {}
    disconnect() {}
    takeRecords() {
      return [];
    }
  }
  vi.stubGlobal('IntersectionObserver', FakeIO);
});
afterEach(async () => {
  await teardownDom();
  vi.unstubAllGlobals();
});

/** A list of `files`; `pictured` says which of them have a thumbnail drawn. */
async function list(files: FileNode[], pictured: (n: FileNode) => boolean = () => true, attachTo?: Element) {
  const w = mount(ListView, {
    attachTo,
    props: {
      files,
      selected: new Set<string>(),
      locale: 'en',
      thumbSrc: (n: FileNode) => (pictured(n) ? `blob:${n.basename}` : null),
    },
  });
  await flushPromises();
  await nextTick();
  return w;
}

/** The name cell of the row showing `name`. */
function cellOf(w: Awaited<ReturnType<typeof list>>, name: string) {
  // The header's Name cell carries the same class and no `.fe-list__name`.
  const cell = w.findAll('.fe-list__col--name').find((c) => {
    const n = c.find('.fe-list__name');
    return n.exists() && n.text().includes(name);
  });
  if (!cell) throw new Error(`no row for ${name}`);
  return cell;
}

describe('which thumbnails are drawn on paper (lib/filePreview, one place)', () => {
  it.each(['a.docx', 'a.xlsx', 'a.pptx', 'a.odt', 'a.ods', 'a.odp', 'a.pdf', 'a.txt', 'a.md', 'a.zip', 'a.tar'])(
    '%s is',
    (name) => {
      expect(drawsOnPaper(file(name))).toBe(true);
    },
  );

  it.each(['a.jpg', 'a.png', 'a.heic', 'a.mp4', 'a.mov', 'a.mp3', 'a.ts'])('%s is not', (name) => {
    expect(drawsOnPaper(file(name))).toBe(false);
  });

  it('a folder is not, whatever it is called', () => {
    expect(drawsOnPaper({ ...file('Raporlar.pdf'), type: 'dir' })).toBe(false);
  });

  it('the badge names the extension in capitals and wears its family', () => {
    expect(thumbTypeBadge(file('Teklif.Docx'))).toEqual({ family: 'doc', label: 'DOCX' });
    expect(thumbTypeBadge(file('b.xlsx'))).toEqual({ family: 'sheet', label: 'XLSX' });
    expect(thumbTypeBadge(file('c.zip'))).toEqual({ family: 'archive', label: 'ZIP' });
    expect(thumbTypeBadge(file('d.jpg'))).toBeNull();
    // Nothing to name: a text file with no extension keeps no badge.
    expect(thumbTypeBadge(file('LICENSE', { mime_type: 'text/plain' }))).toBeNull();
    // One object per kind, so a re-render hands the tile the same value.
    expect(thumbTypeBadge(file('x.docx'))).toBe(thumbTypeBadge(file('y.docx')));
  });

  it('every thumbnail but a picture names its kind: what an app draws too', () => {
    // The v0.50.0 release shots: pkglist drew each package as the list of
    // what it holds - the white square on a white row - and none had a badge.
    expect(thumbTypeBadge(file('plan.board'))).toEqual({ family: 'unknown', label: 'BOARD' });
    expect(thumbTypeBadge(file('kayit.ts'))).toEqual({ family: 'code', label: 'TS' });
    expect(thumbTypeBadge(file('ses.mp3'))).toEqual({ family: 'audio', label: 'MP3' });
    for (const name of ['a.jpg', 'a.png', 'a.heic', 'a.svg', 'a.mp4', 'a.mov']) {
      expect(thumbTypeBadge(file(name)), name).toBeNull();
    }
    expect(thumbTypeBadge({ ...file('Raporlar.pdf'), type: 'dir' }), 'a folder').toBeNull();
  });

  it.each(['jar', 'war', 'aar', 'apk', 'aab', 'ipa', 'whl', 'egg', 'vsix', 'nupkg', 'xpi', 'crx', 'msix', 'deb', 'rpm', 'gem'])(
    'a .%s is a package: the archive family, its own name in the Type column and on the badge',
    (ext) => {
      const n = file(`paket.${ext}`);
      expect(iconFamilyFor(n)).toBe('archive');
      expect(thumbTypeBadge(n)).toEqual({ family: 'archive', label: ext.toUpperCase() });
      expect(typeLabelFor(n, (k) => `t:${k}`)).toBe(ext.toUpperCase());
    },
  );

  it('a plain archive is still called an archive in the Type column', () => {
    expect(typeLabelFor(file('a.zip'), (k) => `t:${k}`)).toBe('t:ftype.archive');
  });
});

describe('the list view', () => {
  it.each([
    ['Teklif.docx', 'DOCX', 'doc'],
    ['Bütçe.xlsx', 'XLSX', 'sheet'],
    ['Sunum.pptx', 'PPTX', 'slides'],
    ['Rapor.pdf', 'PDF', 'pdf'],
    ['Arşiv.zip', 'ZIP', 'archive'],
    ['billing-service-2.4.1.jar', 'JAR', 'archive'],
    ['reportgen-0.9.0-py3-none-any.whl', 'WHL', 'archive'],
    ['Launch plan.board', 'BOARD', 'unknown'],
  ])('a %s whose thumbnail is drawn carries a %s badge in its type colour', async (name, label, family) => {
    // Longer than four letters is set smaller, so it fits the tile.
    const long = label.length > 4;
    const w = await list([file(name)]);
    const cell = cellOf(w, name);
    expect(cell.find('img').attributes('src')).toBe(`blob:${name}`);
    const badge = cell.find('.fe-thumb__type');
    expect(badge.exists()).toBe(true);
    expect(badge.text()).toBe(label);
    expect(badge.classes()).toContain(`fe-ftile--${family}`);
    expect(badge.classes().includes('fe-thumb__type--long'), `${label} is ${long ? '' : 'not '}long`).toBe(long);
    // Decorative: the row is named by its file name, not by this.
    expect(badge.attributes('aria-hidden')).toBe('true');
    // Placed against the thumbnail's own box, not the cell.
    expect(badge.element.parentElement?.classList.contains('fe-list__thumb')).toBe(true);
  });

  it('a photograph and a video frame carry none', async () => {
    const w = await list([file('Tatil.jpg'), file('Klip.mp4')]);
    for (const name of ['Tatil.jpg', 'Klip.mp4']) {
      const cell = cellOf(w, name);
      expect(cell.find('img').exists()).toBe(true);
      expect(cell.find('.fe-thumb__type').exists()).toBe(false);
    }
  });

  it('a document with no thumbnail keeps its type tile and no badge on top', async () => {
    const w = await list([file('Taslak.docx', { thumb_url: null })], () => false);
    const cell = cellOf(w, 'Taslak.docx');
    expect(cell.find('img').exists()).toBe(false);
    expect(cell.find('.fe-list__icon--svg .fe-ftile--doc').exists()).toBe(true);
    expect(cell.find('.fe-thumb__type').exists()).toBe(false);
  });

  it('every row of a mixed folder gets the right answer', async () => {
    const w = await list(
      [file('a.docx'), file('b.jpg'), file('c.pdf', { thumb_url: null })],
      (n) => n.basename !== 'c.pdf',
    );
    expect(w.findAll('.fe-thumb__type').map((b) => b.text())).toEqual(['DOCX']);
  });

  it('right to left, the badge sits at the inline end: the other corner, with no rule of its own', async () => {
    const host = document.createElement('div');
    host.setAttribute('dir', 'rtl');
    document.body.appendChild(host);
    const w = await list([file('تقرير.docx')], () => true, host);
    const badge = cellOf(w, 'تقرير.docx').find('.fe-thumb__type');
    expect(badge.exists()).toBe(true);
    expect(badge.element.closest('[dir]')?.getAttribute('dir')).toBe('rtl');

    // happy-dom lays nothing out, so the placement is read where it is made:
    // the badge's rule. Logical insets turn under dir="rtl" by themselves; a
    // physical `right:` would keep it in the LTR corner of an Arabic list.
    const css = readFileSync(BASE_CSS, 'utf8');
    const rule = /\n\.fe-thumb__type \{([^}]*)\}/.exec(css)?.[1] ?? '';
    expect(rule, 'the .fe-thumb__type rule').not.toBe('');
    expect(rule).toMatch(/position:\s*absolute/);
    expect(rule).toMatch(/inset-inline-end:/);
    expect(rule).toMatch(/inset-block-end:/);
    // Inside the tile's corner, never over it: the list cell and the peek's
    // tile both clip (overflow: hidden), and a badge hanging 3px below the
    // tile came out with its lower edge cut off in the v0.50.0 shots.
    expect(rule, 'the badge does not hang outside the tile').not.toMatch(/inset-(block|inline)-end:\s*-/);
    // …nor wider than it: capped at 30px on a 24px tile, "BOARD" grew out of
    // the tile's other side and the cell cut its B off.
    expect(rule, 'never wider than the tile').toMatch(/max-width:\s*100%/);
    expect(css, 'a long name is set smaller').toMatch(/\n\.fe-thumb__type--long \{[^}]*font-size:/);
    expect(rule).not.toMatch(/(^|[\s;])(left|right|margin-left|margin-right):/);
    // The colour is the type tile's class; a background here would paint
    // every badge one colour.
    expect(rule).not.toMatch(/background/);
    const box = /\n\.fe-list__thumb \{([^}]*)\}/.exec(css)?.[1] ?? '';
    expect(box).toMatch(/position:\s*relative/);
  });
});

describe('the folder peek, the other list', () => {
  it('pictures a page by the same rule and badges it; a photo gets no badge, a short text keeps its tile', async () => {
    const items = [
      file('Teklif.docx'),
      file('Bütçe.xlsx'),
      file('Tatil.jpg'),
      file('notlar.md'),
    ];
    const folder: FileNode = { id: 999, path: 'Depo://belgeler', basename: 'belgeler', type: 'dir' };
    mount(FolderPeek, {
      props: {
        state: { node: folder, anchor: new DOMRect(10, 10, 100, 50), loading: false, items, total: items.length },
        srcOf: (n: FileNode) => `blob:${n.basename}`,
        locale: 'en',
      },
      attachTo: document.body,
    });
    await flushPromises();
    await nextTick();
    const box = document.querySelector('[data-fe-peek]') as HTMLElement;
    expect(box).not.toBeNull();
    const row = (name: string) =>
      [...box.querySelectorAll('.fe-fpeek__row')].find((r) => r.querySelector('.fe-fpeek__label')?.textContent === name)!;
    const badgeOf = (name: string) => row(name).querySelector('.fe-thumb__type');

    expect(row('Teklif.docx').querySelector('img')).not.toBeNull();
    expect(badgeOf('Teklif.docx')?.textContent).toBe('DOCX');
    expect(badgeOf('Teklif.docx')?.classList.contains('fe-ftile--doc')).toBe(true);
    expect(badgeOf('Teklif.docx')?.getAttribute('aria-hidden')).toBe('true');
    // A spreadsheet's page: the peek's old private set left it on its tile.
    expect(row('Bütçe.xlsx').querySelector('img')).not.toBeNull();
    expect(badgeOf('Bütçe.xlsx')?.textContent).toBe('XLSX');
    expect(row('Tatil.jpg').querySelector('img')).not.toBeNull();
    expect(badgeOf('Tatil.jpg')).toBeNull();
    // Under the preview ceiling the list draws a text as its tile; so does the peek.
    expect(row('notlar.md').querySelector('img')).toBeNull();
    expect(badgeOf('notlar.md')).toBeNull();
  });
});

describe('the grid is left alone', () => {
  // A guard, not a regression test: the grid's preview box is large enough to
  // read, and the owner kept the badge to the list.
  it('a document card with its thumbnail carries no type badge', async () => {
    const w = mount(GridView, {
      props: {
        files: [file('Teklif.docx')],
        selected: new Set<string>(),
        locale: 'en',
        thumbSrc: (n: FileNode) => `blob:${n.basename}`,
      },
    });
    await flushPromises();
    await nextTick();
    expect(w.find('img').exists()).toBe(true);
    expect(w.find('.fe-thumb__type').exists()).toBe(false);
  });
});
