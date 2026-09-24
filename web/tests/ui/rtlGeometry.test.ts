/**
 * RIGHT TO LEFT — the gestures, not just the paint.
 *
 * Logical CSS turns a screen around by itself; a POINTER does not. `clientX`
 * grows to the right in every language, so every gesture that means "toward
 * the end of the line" was written for one direction and did the opposite in
 * the other: a column edge that grew while the pointer went the other way, a
 * column dropped on the wrong side of its neighbour, a frozen column that
 * never drew its edge (`scrollLeft` is NEGATIVE in a right-to-left scroller),
 * menus that opened off the end of the pointer. Each is pinned here in both
 * directions — LTR to prove nothing moved, RTL to prove it turned.
 *
 * ⚠ The offered-language list is mocked so `ar` is right to left (the flag
 * the server sets from `wire.IsRTL`); the table's own maths reads the
 * direction it is DRAWN in, which is the `dir` wrapper around it.
 */
import { afterEach, describe, expect, it, vi } from 'vitest';
import { mount, type VueWrapper } from '@vue/test-utils';
import { nextTick } from 'vue';

vi.mock('@brftech/filex-core/src/lib/uiLocales', async (orig) => {
  const real = (await orig()) as Record<string, unknown>;
  return {
    ...real,
    availableLocales: () => [
      { code: 'en', label: 'English', source: 'builtin' },
      { code: 'tr', label: 'Türkçe', source: 'builtin' },
      { code: 'ar', label: 'العربية', source: 'plugin', plugin: 'lang-ar', rtl: true },
    ],
  };
});

import DataTable from '@brftech/filex-core/src/components/DataTable.vue';
import ContextMenu from '@brftech/filex-core/src/components/ContextMenu.vue';
import QuickLook from '@brftech/filex-core/src/components/QuickLook.vue';
import ChoiceButtons from '@brftech/filex-core/src/components/ChoiceButtons.vue';
import { __resetViewPrefs } from '@brftech/filex-core/src/lib/viewPrefs';

type Row = { id: number; name: string; type: string; size: number };
const ROWS: Row[] = [
  { id: 1, name: 'beta', type: 'PDF', size: 20 },
  { id: 2, name: 'alpha', type: 'Image', size: 300 },
];
const COLUMNS = [
  { id: 'name', label: 'Name', width: 200 },
  { id: 'type', label: 'Type', width: 200, hideable: true },
  { id: 'size', label: 'Size', width: 200, hideable: true, align: 'right' as const },
];

const mounted: VueWrapper[] = [];
let tableSeq = 0;
function drawTable(dir: 'ltr' | 'rtl', extra: Record<string, unknown> = {}) {
  const host = document.createElement('div');
  host.setAttribute('dir', dir);
  document.body.appendChild(host);
  const w = mount(DataTable, {
    attachTo: host,
    props: {
      columns: COLUMNS,
      rows: ROWS,
      rowKey: 'id',
      tableId: `rtl.test.${++tableSeq}`,
      locale: dir === 'rtl' ? 'ar' : 'en',
      ...extra,
    },
  });
  mounted.push(w);
  return w;
}

afterEach(() => {
  while (mounted.length) mounted.pop()!.unmount();
  document.body.innerHTML = '';
  __resetViewPrefs();
  vi.restoreAllMocks();
});

const widthOf = (w: VueWrapper, col: string) =>
  /flex-basis:\s*(\d+)px/.exec(w.find(`.fe-list__head [data-col="${col}"]`).attributes('style') ?? '')?.[1];

function pointer(type: string, clientX: number): Event {
  const ev = new MouseEvent(type, { clientX, bubbles: true, cancelable: true, button: 0 });
  return ev;
}

describe('column resize — the edge follows the pointer', () => {
  it('LTR: dragging the handle RIGHT widens the column (unchanged)', async () => {
    const w = drawTable('ltr');
    const handle = w.find('.fe-list__head [data-col="size"] .fe-list__resize');
    handle.element.dispatchEvent(pointer('pointerdown', 500));
    window.dispatchEvent(pointer('pointermove', 530));
    window.dispatchEvent(pointer('pointerup', 530));
    await nextTick();
    expect(widthOf(w, 'size')).toBe('230');
  });

  it('RTL: the handle is on the column’s LEFT edge, so dragging LEFT widens it', async () => {
    const w = drawTable('rtl');
    const handle = w.find('.fe-list__head [data-col="size"] .fe-list__resize');
    handle.element.dispatchEvent(pointer('pointerdown', 500));
    window.dispatchEvent(pointer('pointermove', 470));
    window.dispatchEvent(pointer('pointerup', 470));
    await nextTick();
    expect(widthOf(w, 'size')).toBe('230');
  });

  it('the arrow keys move the edge the way they point: → in LTR, ← in RTL widens', async () => {
    const ltr = drawTable('ltr');
    await ltr.find('.fe-list__head [data-col="size"] .fe-list__resize').trigger('keydown', { key: 'ArrowRight' });
    expect(widthOf(ltr, 'size')).toBe('216');
    const rtl = drawTable('rtl');
    await rtl.find('.fe-list__head [data-col="size"] .fe-list__resize').trigger('keydown', { key: 'ArrowLeft' });
    expect(widthOf(rtl, 'size')).toBe('216');
  });
});

describe('the frozen columns draw their edge once something scrolls under them', () => {
  it('RTL: scrollLeft goes NEGATIVE toward the end, and still counts as scrolled', async () => {
    const w = drawTable('rtl');
    const list = w.find('.fe-list');
    Object.defineProperty(list.element, 'scrollLeft', { configurable: true, get: () => -40 });
    await list.trigger('scroll');
    expect(list.classes()).toContain('is-scrolled-x');
  });

  it('LTR: a positive scrollLeft, as before', async () => {
    const w = drawTable('ltr');
    const list = w.find('.fe-list');
    Object.defineProperty(list.element, 'scrollLeft', { configurable: true, get: () => 40 });
    await list.trigger('scroll');
    expect(list.classes()).toContain('is-scrolled-x');
  });
});

/** Lay the header cells out as the browser would, in `dir`. */
function layOutHeader(w: VueWrapper, dir: 'ltr' | 'rtl') {
  const order = ['name', 'type', 'size'];
  vi.spyOn(Element.prototype, 'getBoundingClientRect').mockImplementation(function (this: Element) {
    const col = (this as HTMLElement).getAttribute?.('data-col');
    const i = col ? order.indexOf(col) : -1;
    if (i < 0) return { left: 0, right: 0, top: 0, bottom: 0, width: 0, height: 0, x: 0, y: 0 } as DOMRect;
    // 200px columns; LTR runs from x=0, RTL from the right edge of a 1200px table.
    const left = dir === 'ltr' ? i * 200 : 1200 - (i + 1) * 200;
    return { left, right: left + 200, top: 0, bottom: 30, width: 200, height: 30, x: left, y: 0 } as DOMRect;
  });
  void w;
}

const headerOrder = (w: VueWrapper) =>
  w.findAll('.fe-list__head [data-col]').map((c) => c.attributes('data-col'));

describe('dragging a header drops it on the side of the neighbour the pointer is on', () => {
  it('LTR: Size dropped on the LEFT half of Type lands before it (unchanged)', async () => {
    const w = drawTable('ltr');
    layOutHeader(w, 'ltr');
    // Type is x 200–400 (middle 300); Size 400–600.
    w.find('.fe-list__head [data-col="size"]').element.dispatchEvent(pointer('pointerdown', 500));
    window.dispatchEvent(pointer('pointermove', 250));
    window.dispatchEvent(pointer('pointerup', 250));
    await nextTick();
    expect(headerOrder(w)).toEqual(['name', 'size', 'type']);
  });

  it('RTL: Size dropped on the RIGHT half of Type — its START side — lands before it', async () => {
    const w = drawTable('rtl');
    layOutHeader(w, 'rtl');
    // RTL: Name 1000–1200, Type 800–1000 (middle 900), Size 600–800.
    w.find('.fe-list__head [data-col="size"]').element.dispatchEvent(pointer('pointerdown', 700));
    window.dispatchEvent(pointer('pointermove', 950));
    window.dispatchEvent(pointer('pointerup', 950));
    await nextTick();
    expect(headerOrder(w)).toEqual(['name', 'size', 'type']);
  });

  it('Ctrl + the arrow that points toward the start moves the column earlier — → in RTL', async () => {
    const w = drawTable('rtl');
    await w.find('.fe-list__head [data-col="size"]').trigger('keydown', { key: 'ArrowRight', ctrlKey: true });
    expect(headerOrder(w)).toEqual(['name', 'size', 'type']);
  });
});

describe('the column menu', () => {
  it('opens down-left of the pointer in RTL, and carries the language’s direction under <body>', async () => {
    const w = drawTable('rtl');
    vi.spyOn(Element.prototype, 'getBoundingClientRect').mockImplementation(function (this: Element) {
      const isMenu = (this as HTMLElement).classList?.contains('fe-colmenu');
      return { left: 0, right: isMenu ? 240 : 0, top: 0, bottom: 0, width: isMenu ? 240 : 0, height: 0, x: 0, y: 0 } as DOMRect;
    });
    Object.defineProperty(window, 'innerWidth', { configurable: true, value: 1440 });
    await w.find('.fe-list__colmenu-btn').trigger('click', { clientX: 700, clientY: 40 });
    await nextTick();
    await nextTick();
    const backdrop = document.querySelector('.fe-colmenu__backdrop') as HTMLElement;
    expect(backdrop.getAttribute('dir')).toBe('rtl');
    const menu = document.querySelector('.fe-colmenu') as HTMLElement;
    expect(menu.style.left).toBe('460px');
  });

  it('the nudges follow reading order, and each one’s arrow and label say where it moves', async () => {
    const w = drawTable('rtl');
    await w.find('.fe-list__colmenu-btn').trigger('click');
    await nextTick();
    const sizeLine = [...document.querySelectorAll('.fe-colmenu__line')].find((l) => l.textContent?.includes('Size'))!;
    const [earlier, later] = [...sizeLine.querySelectorAll<HTMLButtonElement>('.fe-colmenu__nudge')];
    // In RTL, one place EARLIER is one place to the RIGHT.
    expect(earlier.textContent?.trim()).toBe(String.fromCharCode(0x2192));
    expect(earlier.getAttribute('aria-label')).toMatch(/right/i);
    expect(later.textContent?.trim()).toBe(String.fromCharCode(0x2190));
    expect(later.getAttribute('aria-label')).toMatch(/left/i);
    earlier.click();
    await nextTick();
    expect(headerOrder(w)).toEqual(['name', 'size', 'type']);
  });

  it('LTR keeps ← then →, "left" then "right" (unchanged)', async () => {
    const w = drawTable('ltr');
    await w.find('.fe-list__colmenu-btn').trigger('click');
    await nextTick();
    const typeLine = [...document.querySelectorAll('.fe-colmenu__line')].find((l) => l.textContent?.includes('Type'))!;
    const [first, second] = [...typeLine.querySelectorAll<HTMLButtonElement>('.fe-colmenu__nudge')];
    expect(first.textContent?.trim()).toBe(String.fromCharCode(0x2190));
    expect(first.getAttribute('aria-label')).toMatch(/left/i);
    expect(second.textContent?.trim()).toBe(String.fromCharCode(0x2192));
    expect(second.getAttribute('aria-label')).toMatch(/right/i);
  });
});

describe('the context menu', () => {
  function openAt(locale: string, x: number) {
    const w = mount(ContextMenu, { props: { locale, actions: [{ key: 'open', label: 'Open' }] } });
    mounted.push(w);
    vi.spyOn(Element.prototype, 'getBoundingClientRect').mockImplementation(function (this: Element) {
      const isMenu = (this as HTMLElement).classList?.contains('fe-ctx');
      return { left: 0, right: 0, top: 0, bottom: 0, width: isMenu ? 200 : 0, height: isMenu ? 100 : 0, x: 0, y: 0 } as DOMRect;
    });
    Object.defineProperty(window, 'innerWidth', { configurable: true, value: 1440 });
    Object.defineProperty(window, 'innerHeight', { configurable: true, value: 900 });
    (w.vm as unknown as { show: (e: { clientX: number; clientY: number }, n: []) => Promise<void> }).show({ clientX: x, clientY: 100 }, []);
    return w;
  }

  it('LTR: its left edge at the pointer (unchanged)', async () => {
    openAt('en', 700);
    await nextTick();
    await nextTick();
    const menu = document.querySelector('.fe-ctx') as HTMLElement;
    expect(menu.style.left).toBe('700px');
    expect(document.querySelector('.fe-ctx-backdrop')!.getAttribute('dir')).toBe('ltr');
  });

  it('RTL: its RIGHT edge at the pointer — it opens the way the line reads', async () => {
    openAt('ar', 700);
    await nextTick();
    await nextTick();
    const menu = document.querySelector('.fe-ctx') as HTMLElement;
    expect(menu.style.left).toBe('500px');
    expect(document.querySelector('.fe-ctx-backdrop')!.getAttribute('dir')).toBe('rtl');
  });
});

describe('quick look — the arrow that points the way the line reads is "next"', () => {
  function peek(locale: string) {
    const w = mount(QuickLook, {
      props: {
        open: true,
        locale,
        file: null,
        previewUrl: (p: string) => p,
        downloadUrl: (p: string) => p,
      },
      global: { stubs: { PreviewModal: true } },
    });
    mounted.push(w);
    return w;
  }
  const press = (key: string) => window.dispatchEvent(new KeyboardEvent('keydown', { key, bubbles: true, cancelable: true }));

  it('LTR: → next, ← previous (unchanged)', async () => {
    const w = peek('en');
    await nextTick();
    press('ArrowRight');
    press('ArrowLeft');
    expect(w.emitted('nav')).toEqual([[1], [-1]]);
  });

  it('RTL: ← next, → previous; ↓ is next in both', async () => {
    const w = peek('ar');
    await nextTick();
    press('ArrowLeft');
    press('ArrowRight');
    press('ArrowDown');
    expect(w.emitted('nav')).toEqual([[1], [-1], [1]]);
  });
});

describe('a radio row — the arrow that points to an option chooses it', () => {
  function row(dir: 'ltr' | 'rtl') {
    const host = document.createElement('div');
    host.setAttribute('dir', dir);
    document.body.appendChild(host);
    const w = mount(ChoiceButtons, {
      attachTo: host,
      props: { modelValue: 'b', options: [{ value: 'a', label: 'A' }, { value: 'b', label: 'B' }, { value: 'c', label: 'C' }] },
    });
    mounted.push(w);
    return w;
  }

  it('LTR: → chooses the next option (unchanged)', async () => {
    const w = row('ltr');
    await w.findAll('button')[1].trigger('keydown', { key: 'ArrowRight' });
    expect(w.emitted('update:modelValue')?.[0]).toEqual(['c']);
  });

  it('RTL: ← chooses the next option — it is the one on the left', async () => {
    const w = row('rtl');
    await w.findAll('button')[1].trigger('keydown', { key: 'ArrowLeft' });
    expect(w.emitted('update:modelValue')?.[0]).toEqual(['c']);
  });
});
