// Why a file has no thumbnail, said on the file (0.50, lib/thumbNote).
//
// A file whose thumbnail will not come because of the file itself - it is
// damaged, it is encrypted (an office password, a zip's, filex's end-to-end
// encryption), it is too large - carries `thumb_note` in the listing. The
// owner's call: the list, the grid and the gallery mark its type icon with a
// small marker with an icon, and resting the pointer on it or tapping it says
// why, in English and in Turkish. A failure that may pass has no note (the
// server sends none). The marker belongs to the type icon: a file with a
// picture never wears it, and the list's type badge (0.50) is only on a
// picture, so the two never meet.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushPromises, mount } from '@vue/test-utils';
import { nextTick } from 'vue';

import ListView from '@brftech/filex-core/src/components/ListView.vue';
import GridView from '@brftech/filex-core/src/components/GridView.vue';
import GalleryView from '@brftech/filex-core/src/components/GalleryView.vue';
import { __resetNearViewport } from '@brftech/filex-core/src/lib/nearViewport';
import { thumbNoteOf, thumbNoteWords } from '@brftech/filex-core/src/lib/thumbNote';
import { en } from '@brftech/filex-core/src/locales/en';
import { tr } from '@brftech/filex-core/src/locales/tr';
import type { FileNode } from '@brftech/filex-core/src/types/FileNode';
import { teardownDom } from '../helpers/teardown';

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
    ...extra,
  };
}

/** A catalogue as `t`, with `{name}` placeholders filled. */
function tOf(cat: Record<string, string>) {
  return (key: string, vars?: Record<string, string | number>) =>
    (cat[key] ?? key).replace(/\{(\w+)\}/g, (_, k) => String(vars?.[k] ?? `{${k}}`));
}

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
afterEach(async () => {
  await teardownDom();
  vi.unstubAllGlobals();
});

describe('which files wear a note (lib/thumbNote, one place)', () => {
  it('the three notes the server sends', () => {
    for (const note of ['corrupt', 'encrypted', 'too_large'] as const) {
      expect(thumbNoteOf(file('a.docx', { thumb_note: note }))).toBe(note);
    }
  });

  it('none on a file with a picture, a folder, or a note this client does not know', () => {
    expect(thumbNoteOf(file('a.docx', { thumb_note: 'corrupt', thumb_url: '/api/files/thumb/1' }))).toBeNull();
    expect(thumbNoteOf({ ...file('Belgeler', { thumb_note: 'corrupt' }), type: 'dir' })).toBeNull();
    expect(thumbNoteOf(file('a.docx', { thumb_note: 'later' as never }))).toBeNull();
    expect(thumbNoteOf(file('a.docx'))).toBeNull();
  });

  it('says why in English and in Turkish, with the Turkish letters', () => {
    const d = thumbNoteWords(file('a.docx', { thumb_note: 'corrupt' }), { t: tOf(en) })!;
    expect(d.label).toBe('Damaged');
    expect(d.full).toBe(`Damaged. ${en['thumbNote.corrupt_why']}`);
    expect(d.paths.length).toBeGreaterThan(0);
    const e = thumbNoteWords(file('a.xlsx', { thumb_note: 'encrypted' }), { t: tOf(tr) })!;
    expect(e.label).toBe('Şifreli');
    expect(e.why).toContain('parolayla');
    expect(thumbNoteWords(file('a.pptx', { thumb_note: 'too_large' }), { t: tOf(tr) })!.label).toBe('Çok büyük');
    for (const k of ['corrupt', 'encrypted', 'too_large']) {
      for (const cat of [en, tr]) {
        expect(cat[`thumbNote.${k}`], k).toBeTruthy();
        expect(cat[`thumbNote.${k}_why`], k).toBeTruthy();
        expect(cat[`thumbNote.${k}_why`]).not.toMatch(/[\u2010-\u2015\u2212]/);
      }
    }
  });

  it('hands the tile the same object on every render', () => {
    const t = tOf(en);
    expect(thumbNoteWords(file('a.docx', { thumb_note: 'corrupt' }), { t })).toBe(
      thumbNoteWords(file('b.docx', { thumb_note: 'corrupt' }), { t }),
    );
  });
});

const files = () => [
  file('bozuk.docx', { thumb_note: 'corrupt' }),
  file('parolali.xlsx', { thumb_note: 'encrypted' }),
  file('buyuk.pptx', { thumb_note: 'too_large' }),
  file('bekleyen.docx'),
  file('cizildi.docx', { thumb_url: '/api/files/thumb/9?v=1' }),
];

async function mountView(view: 'list' | 'grid' | 'gallery', list: FileNode[], locale = 'en') {
  const props = {
    files: list,
    selected: new Set<string>(),
    locale,
    thumbSrc: (n: FileNode) => (n.thumb_url ? `blob:${n.basename}` : null),
  };
  const comp = view === 'list' ? ListView : view === 'grid' ? GridView : GalleryView;
  const w = mount(comp as never, { props: props as never, attachTo: document.body });
  await flushPromises();
  await nextTick();
  return w;
}

describe.each(['list', 'grid', 'gallery'] as const)('the %s view', (view) => {
  it('marks each file with its note, and only the type icon', async () => {
    const w = await mountView(view, files());
    const marks = w.findAll('[data-testid="thumb-note"]');
    expect(marks.map((m) => m.attributes('data-note')).sort()).toEqual(['corrupt', 'encrypted', 'too_large']);
    for (const m of marks) {
      expect(m.attributes('role')).toBe('img');
      expect(m.attributes('aria-label')).toMatch(/^(Damaged|Encrypted|Too large)\. No thumbnail: /);
      expect(m.find('svg path').exists(), 'it has an icon').toBe(true);
    }
    // The drawn file has its picture and no marker; the list's type badge is
    // on the picture only.
    expect(w.findAll('img').length).toBeGreaterThan(0);
    if (view === 'list') expect(w.findAll('.fe-thumb__type').length).toBe(1);
    w.unmount();
  });

  it('says why on a tap, without selecting or opening the file', async () => {
    const w = await mountView(view, files());
    const mark = w.get('[data-note="encrypted"]');
    await mark.trigger('click');
    await nextTick();
    const tip = document.body.querySelector('[data-testid="thumb-note-tip"]');
    expect(tip?.textContent).toBe(`Encrypted. ${en['thumbNote.encrypted_why']}`);
    expect(tip?.getAttribute('role')).toBe('tooltip');
    const emitted = Object.keys(w.emitted());
    expect(emitted.filter((e) => /click|dbl|select|open/.test(e)), 'the card did not take the tap').toEqual([]);
    await mark.trigger('click');
    await nextTick();
    expect(document.body.querySelector('[data-testid="thumb-note-tip"]')).toBeNull();
    w.unmount();
  });

  // 0.50 full round: a mouse's click on the marker came after its hover had
  // opened the sentence, and the click toggled it shut - on every engine.
  it('a mouse click keeps the sentence its hover opened', async () => {
    const w = await mountView(view, files());
    const mark = w.get('[data-note="encrypted"]');
    mark.element.dispatchEvent(Object.assign(new Event('pointerenter'), { pointerType: 'mouse' }));
    await nextTick();
    expect(document.body.querySelector('[data-testid="thumb-note-tip"]'), 'the hover opened it').not.toBeNull();
    mark.element.dispatchEvent(Object.assign(new Event('pointerdown', { bubbles: true }), { pointerType: 'mouse' }));
    await mark.trigger('click');
    await nextTick();
    expect(document.body.querySelector('[data-testid="thumb-note-tip"]')?.textContent).toBe(
      `Encrypted. ${en['thumbNote.encrypted_why']}`,
    );
    mark.element.dispatchEvent(Object.assign(new Event('pointerleave'), { pointerType: 'mouse' }));
    await nextTick();
    expect(document.body.querySelector('[data-testid="thumb-note-tip"]')).toBeNull();
    w.unmount();
  });

  it('shows it while a mouse rests on the marker, and in Turkish', async () => {
    const w = await mountView(view, files(), 'tr');
    const mark = w.get('[data-note="corrupt"]');
    mark.element.dispatchEvent(Object.assign(new Event('pointerenter'), { pointerType: 'mouse' }));
    await nextTick();
    await nextTick();
    const tip = document.body.querySelector('[data-testid="thumb-note-tip"]');
    expect(tip?.textContent).toBe(`Bozuk. ${tr['thumbNote.corrupt_why']}`);
    mark.element.dispatchEvent(Object.assign(new Event('pointerleave'), { pointerType: 'mouse' }));
    await nextTick();
    expect(document.body.querySelector('[data-testid="thumb-note-tip"]')).toBeNull();
    w.unmount();
  });
});
