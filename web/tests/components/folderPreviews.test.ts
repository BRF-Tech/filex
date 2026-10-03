// Folder previews (0.50, docs/thumbnails.md → Folder previews).
//
// What is pinned here:
//   1. A folder whose row carries `preview` (its newest files, newest first)
//      is drawn with them: in the grid rising out of the folder (back, prints,
//      front), in the gallery fanned out in front of it, in the list the
//      newest one rising out of the row's small folder. The newest is on top
//      and in the middle; a file with no thumbnail is its type icon. Once any
//      folder in the view has files to show, every folder is drawn so (an
//      empty one empty; an encrypted one keeps its padlock); with none, the
//      views keep their classic folder icons.
//   2. Resting the mouse on a folder (grid card or list row) asks the pane to
//      peek; a touch does not; leaving ends it; with folder previews turned
//      off (Settings) nothing is peeked.
//   3. The peek waits, lists the folder once through the pane's `index`, drops
//      an answer that arrives after the pointer moved on, and keeps answers
//      for a while.
//   4. The peek says how many things are inside and names the first few,
//      with a thumbnail only for a file whose thumbnail is a picture.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushPromises, mount } from '@vue/test-utils';
import { nextTick } from 'vue';

import GridView from '@brftech/filex-core/src/components/GridView.vue';
import ListView from '@brftech/filex-core/src/components/ListView.vue';
import GalleryView from '@brftech/filex-core/src/components/GalleryView.vue';
import FolderPeek from '@brftech/filex-core/src/components/FolderPeek.vue';
import { useFolderPeek, PEEK_DELAY_MS } from '@brftech/filex-core/src/composables/useFolderPeek';
import { __resetNearViewport } from '@brftech/filex-core/src/lib/nearViewport';
import type { FileNode } from '@brftech/filex-core/src/types/FileNode';

function dir(name: string, preview?: FileNode['preview']): FileNode {
  return { id: name.length, path: `foto://${name}`, basename: name, type: 'dir', preview };
}
function file(name: string, extra: Partial<FileNode> = {}): FileNode {
  const ext = name.split('.').pop();
  return { id: name.length + 100, path: `foto://${name}`, basename: name, type: 'file', extension: ext, size: 10, ...extra };
}
const pics = (n: number) =>
  Array.from({ length: n }, (_, i) => ({ name: `${i}.jpg`, thumb_url: `/api/files/thumb/${i + 1}?v=1` }));

beforeEach(() => {
  __resetNearViewport();
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
afterEach(() => {
  vi.unstubAllGlobals();
  vi.useRealTimers();
});

async function grid(files: FileNode[]) {
  const asked: string[] = [];
  const w = mount(GridView, {
    props: {
      files,
      selected: new Set<string>(),
      locale: 'en',
      thumbSrc: (n: FileNode) => {
        asked.push(n.thumb_url ?? '');
        return n.thumb_url ? `blob:${n.basename}` : null;
      },
    },
  });
  await flushPromises();
  await nextTick();
  return { w, asked };
}

describe('the folder with its pictures', () => {
  it.each([1, 2, 3])('draws %i picture(s) rising out of the folder, through the thumbnail loader', async (n) => {
    const { w, asked } = await grid([dir('Tatil', pics(n)), file('a.txt')]);
    const card = w.find('.fe-grid__card--folder');
    const mosaic = card.find('.fe-fmosaic');
    expect(mosaic.classes()).toContain(`fe-fmosaic--${n}`);
    expect(mosaic.findAll('.fe-fmosaic__print img')).toHaveLength(n);
    expect(asked.filter((u) => u.startsWith('/api/files/thumb/'))).toHaveLength(n);
    // Back, the prints, then the front over their lower half: the order
    // is the depth.
    const stage = mosaic.find('.fe-fmosaic__stage').element;
    const order = [...stage.children].map((el) => el.getAttribute('class')?.split(' ')[0]);
    expect(order).toEqual(['fe-fmosaic__back', ...Array(n).fill('fe-fmosaic__print'), 'fe-fmosaic__front']);
  });

  it('never shows more than three', async () => {
    const { w } = await grid([dir('Tatil', pics(7))]);
    expect(w.find('.fe-fmosaic').findAll('.fe-fmosaic__print')).toHaveLength(3);
  });

  it('gives every folder the box once one has pictures, the same folder empty for the others', async () => {
    const { w } = await grid([dir('Tatil', pics(2)), dir('Belgeler')]);
    expect(w.find('.fe-grid').classes()).toContain('has-folder-previews');
    const boxes = w.findAll('.fe-grid__thumb--folder');
    expect(boxes).toHaveLength(2);
    const empty = boxes[1].find('.fe-fmosaic--0');
    expect(empty.find('.fe-fmosaic__back').exists()).toBe(true);
    expect(empty.find('.fe-fmosaic__front').exists()).toBe(true);
    expect(empty.findAll('.fe-fmosaic__print')).toHaveLength(0);
  });

  it('keeps the padlock for an encrypted folder', async () => {
    const locked: FileNode = { ...dir('Gizli'), e2e: true } as FileNode;
    const { w } = await grid([dir('Tatil', pics(1)), locked]);
    const boxes = w.findAll('.fe-grid__thumb--folder');
    expect(boxes[1].find('.fe-fmosaic__glyph').exists()).toBe(true);
    expect(boxes[1].find('.fe-fmosaic__stage').exists()).toBe(false);
  });

  it('keeps the compact folder cards when no folder has pictures', async () => {
    const { w } = await grid([dir('Belgeler'), dir('Arsiv'), file('a.txt')]);
    expect(w.find('.fe-grid').classes()).not.toContain('has-folder-previews');
    expect(w.find('.fe-grid__thumb--folder').exists()).toBe(false);
  });

  it('puts the newest file in the middle and on top, the older ones either side', async () => {
    const newestFirst = [
      { name: 'yeni.jpg', thumb_url: '/api/files/thumb/1?v=1' },
      { name: 'orta.jpg', thumb_url: '/api/files/thumb/2?v=1' },
      { name: 'eski.jpg', thumb_url: '/api/files/thumb/3?v=1' },
    ];
    const { w } = await grid([dir('Tatil', newestFirst)]);
    const prints = w.findAll('.fe-fmosaic__print');
    // Back to front: the oldest on the left, then the right, then the newest
    // in the middle, drawn last.
    const slots = prints.map((p) => [p.classes().find((c) => /--\d$/.test(c)), p.find('img').attributes('src')]);
    expect(slots).toEqual([
      ['fe-fmosaic__print--1', 'blob:eski.jpg'],
      ['fe-fmosaic__print--3', 'blob:orta.jpg'],
      ['fe-fmosaic__print--2', 'blob:yeni.jpg'],
    ]);
  });

  it('shows a file with no thumbnail as its type icon', async () => {
    const { w } = await grid([dir('Karisik', [{ name: 'yedek.zip' }, { name: 'foto.jpg', thumb_url: '/api/files/thumb/9?v=1' }])]);
    const prints = w.findAll('.fe-fmosaic__print');
    expect(prints).toHaveLength(2);
    const icon = prints.find((p) => p.classes().includes('is-icon'))!;
    expect(icon.find('img').exists()).toBe(false);
    expect(icon.find('.fe-fmosaic__icon svg').exists()).toBe(true);
  });
});

describe('the gallery and the list draw the same files', () => {
  it('the gallery fans the files out in front of the folder, and draws an empty folder empty', async () => {
    const w = mount(GalleryView, {
      props: {
        files: [dir('Tatil', pics(3)), dir('Bos'), file('a.txt')],
        selected: new Set<string>(),
        locale: 'en',
        thumbSrc: (n: FileNode) => (n.thumb_url ? `blob:${n.basename}` : null),
      },
    });
    await flushPromises();
    expect(w.findAll('.fe-fmosaic--fan')).toHaveLength(2);
    const full = w.find('[data-fe-path="foto://Tatil"] .fe-fmosaic--fan');
    expect(full.findAll('.fe-fmosaic__print img')).toHaveLength(3);
    // The folder is drawn behind the prints; no badge.
    expect(full.find('.fe-fmosaic__back').exists()).toBe(true);
    expect(full.find('.fe-fmosaic__front').exists()).toBe(true);
    const empty = w.find('[data-fe-path="foto://Bos"] .fe-fmosaic--fan');
    expect(empty.classes()).toContain('fe-fmosaic--0');
    expect(empty.findAll('.fe-fmosaic__print')).toHaveLength(0);
  });

  it('the list shows the newest file only, and every folder row the same small folder', async () => {
    const w = mount(ListView, {
      props: {
        files: [dir('Tatil', pics(3)), dir('Bos'), file('a.txt')],
        selected: new Set<string>(),
        locale: 'en',
        thumbSrc: (n: FileNode) => (n.thumb_url ? `blob:${n.basename}` : null),
      },
    });
    await flushPromises();
    expect(w.findAll('.fe-fmosaic--mini')).toHaveLength(2);
    const full = w.find('[data-fe-path="foto://Tatil"] .fe-fmosaic--mini');
    const prints = full.findAll('.fe-fmosaic__print');
    expect(prints).toHaveLength(1);
    expect(prints[0].find('img').attributes('src')).toBe('blob:0.jpg');
    const empty = w.find('[data-fe-path="foto://Bos"] .fe-fmosaic--mini');
    expect(empty.findAll('.fe-fmosaic__print')).toHaveLength(0);
    expect(empty.find('.fe-fmosaic__back').exists()).toBe(true);
  });

  it('no folder with files to show: the gallery and the list keep their classic icons', async () => {
    const files = [dir('Bos'), dir('Arsiv'), file('a.txt')];
    const g = mount(GalleryView, { props: { files, selected: new Set<string>(), locale: 'en' } });
    const l = mount(ListView, { props: { files, selected: new Set<string>(), locale: 'en' } });
    await flushPromises();
    expect(g.find('.fe-fmosaic').exists()).toBe(false);
    expect(l.find('.fe-fmosaic').exists()).toBe(false);
  });
});

describe('peeking into a folder', () => {
  it('a mouse resting on a folder card asks to peek; leaving and pressing end it', async () => {
    const { w } = await grid([dir('Tatil'), file('a.jpg')]);
    const card = w.find('.fe-grid__card--folder');
    await card.trigger('pointerenter', { pointerType: 'mouse' });
    const peek = w.emitted('peek')!;
    expect(peek).toHaveLength(1);
    expect((peek[0][0] as FileNode).basename).toBe('Tatil');
    expect(peek[0][2]).toBe('mouse');
    await card.trigger('pointerleave');
    await card.trigger('pointerdown');
    expect(w.emitted('peek-end')!.length).toBeGreaterThanOrEqual(2);
    // A file card does not peek.
    await w.find('.fe-grid__card--file').trigger('pointerenter', { pointerType: 'mouse' });
    expect(w.emitted('peek')).toHaveLength(1);
  });

  it('a folder row in the list asks to peek too', async () => {
    const w = mount(ListView, {
      props: { files: [dir('Tatil'), file('a.jpg')], selected: new Set<string>(), locale: 'en' },
    });
    await flushPromises();
    const row = w.find('[data-fe-path="foto://Tatil"]');
    await row.trigger('pointerenter', { pointerType: 'mouse' });
    expect(w.emitted('peek')).toHaveLength(1);
    await row.trigger('pointerleave');
    expect(w.emitted('peek-end')).toHaveLength(1);
  });

  it('waits, lists once, answers the latest hover only, and remembers', async () => {
    vi.useFakeTimers();
    const index = vi.fn(async (path: string) => ({
      files: path === 'foto://Tatil'
        ? [file('b.jpg'), dir('Alt'), file('a.jpg')]
        : [file('x.txt')],
    }));
    const p = useFolderPeek(index);
    const el = document.createElement('div');

    p.enter(dir('Tatil'), el, 'touch');
    await vi.advanceTimersByTimeAsync(PEEK_DELAY_MS + 50);
    expect(index).not.toHaveBeenCalled();

    p.enter(dir('Tatil'), el, 'mouse');
    await vi.advanceTimersByTimeAsync(PEEK_DELAY_MS - 100);
    expect(p.state.value).toBeNull();
    await vi.advanceTimersByTimeAsync(200);
    await flushPromises();
    expect(p.state.value?.loading).toBe(false);
    expect(p.state.value?.total).toBe(3);
    // Folders first, the rest in the order the listing gave.
    expect(p.state.value?.items.map((n) => n.basename)).toEqual(['Alt', 'b.jpg', 'a.jpg']);

    p.leave();
    expect(p.state.value).toBeNull();
    p.enter(dir('Tatil'), el, 'mouse');
    await vi.advanceTimersByTimeAsync(PEEK_DELAY_MS + 10);
    await flushPromises();
    expect(index).toHaveBeenCalledTimes(1);

    // An answer for a hover the pointer has left is dropped.
    let release: () => void = () => {};
    index.mockImplementationOnce(
      () => new Promise((r) => { release = () => r({ files: [file('late.txt')] }); }),
    );
    p.enter(dir('Yavas'), el, 'mouse');
    await vi.advanceTimersByTimeAsync(PEEK_DELAY_MS + 10);
    p.leave();
    release();
    await flushPromises();
    expect(p.state.value).toBeNull();
  });

  it('shows nothing while folder previews are turned off, and again once they are on', async () => {
    vi.useFakeTimers();
    const index = vi.fn(async () => ({ files: [file('a.jpg')] }));
    let on = false;
    const p = useFolderPeek(index, () => on);
    const el = document.createElement('div');
    p.enter(dir('Tatil'), el, 'mouse');
    await vi.advanceTimersByTimeAsync(PEEK_DELAY_MS + 50);
    await flushPromises();
    expect(index).not.toHaveBeenCalled();
    expect(p.state.value).toBeNull();

    on = true;
    p.enter(dir('Tatil'), el, 'mouse');
    await vi.advanceTimersByTimeAsync(PEEK_DELAY_MS + 50);
    await flushPromises();
    expect(p.state.value?.total).toBe(1);
  });

  it('says how many, names the first few, and pictures only what is a picture', async () => {
    const items = [dir('Alt'), file('foto.jpg', { thumb_url: '/api/files/thumb/5?v=1' }), file('notlar.txt', { thumb_url: '/api/files/thumb/6?v=1' })];
    const rect = new DOMRect(10, 10, 100, 50);
    const srcOf = vi.fn((n: FileNode) => (n.thumb_url ? `blob:${n.basename}` : null));
    const w = mount(FolderPeek, {
      props: { state: { node: dir('Tatil'), anchor: rect, loading: false, items, total: 11 }, srcOf, locale: 'en' },
      attachTo: document.body,
    });
    await flushPromises();
    await nextTick();
    const box = document.querySelector('[data-fe-peek]') as HTMLElement;
    expect(box).not.toBeNull();
    expect(box.textContent).toContain('11 items');
    expect(box.textContent).toContain('+8 more');
    expect([...box.querySelectorAll('.fe-fpeek__label')].map((x) => x.textContent)).toEqual(['Alt', 'foto.jpg', 'notlar.txt']);
    expect(box.querySelectorAll('img')).toHaveLength(1);
    expect(box.getAttribute('role')).toBe('tooltip');
    w.unmount();
  });
});
