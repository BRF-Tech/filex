// #196 - while a menu is open its rows hold still.
//
// The 0.53 release run (e2e 115-tag-kinds, Firefox): a file's right-click menu
// opened before the server had answered "may this file be encrypted?" (POST
// /api/files/e2e/allowed). The answer landed 101 ms later, "Encrypt with
// E2EE…" was drawn under "Download" and every row below it moved one place
// down - between the press and the release of a click on "Tags", which then
// landed on "Star". On a slow server the row under the pointer can as well be
// "Delete". The core ContextMenu is the one menu every surface uses (the
// explorer, the selection bar's "⋯", the side panel, every table's row
// actions), so the rule is pinned on it: rows that were drawn keep their
// places until the menu closes; a late row is added at the end.
//
// The explorer half (a row's menu waits for its answers before it opens) is
// web/tests/components/menuWaitsForAnswers.test.ts; the arithmetic is
// web/tests/lib/heldMenuRows.test.ts; the browser measurement is
// e2e/tests/213-menu-rows-stay.spec.ts.
import { afterEach, describe, expect, it, vi } from 'vitest';
import { flushPromises, mount } from '@vue/test-utils';
import { nextTick } from 'vue';

import ContextMenu, { type ContextAction } from '@brftech/filex-core/src/components/ContextMenu.vue';

type Menu = {
  show: (e: { clientX: number; clientY: number }, n: unknown[]) => Promise<void>;
  hide: () => void;
};

const ENCRYPT = 'Encrypt with E2EE…';

/** A file's menu as the explorer lists it: the encryption row hidden until
 *  the server has answered for the file. */
function fileMenu(encrypt: 'pending' | 'allowed'): ContextAction[] {
  return [
    { key: 'open', label: 'Open' },
    { key: 'download', label: 'Download' },
    { key: 'fxe-encrypt', label: ENCRYPT, icon: 'lock', hidden: encrypt === 'pending' },
    { key: 'details', label: 'Details' },
    { divider: true, key: 'sep1', label: '' },
    { key: 'rename', label: 'Rename' },
    { divider: true, key: 'sep-meta', label: '' },
    { key: 'star', label: 'Star' },
    { key: 'tags', label: 'Tags…' },
    { divider: true, key: 'sep2', label: '' },
    { key: 'delete', label: 'Delete', danger: true },
  ];
}

async function openMenu(actions: ContextAction[], extra: { sheet?: boolean } = {}, at = { clientX: 40, clientY: 40 }) {
  const w = mount(ContextMenu, { props: { locale: 'en' as const, actions, ...extra } });
  await (w.vm as unknown as Menu).show(at, []);
  await nextTick();
  return w;
}

/** The entries on screen, in order (menu or sheet). */
function rows(scope = '.fe-ctx-backdrop'): HTMLButtonElement[] {
  return Array.from(document.querySelectorAll<HTMLButtonElement>(`${scope} .fe-ctx__item`));
}
const labels = (scope?: string) => rows(scope).map((b) => (b.querySelector('.fe-ctx__label')?.textContent ?? '').trim());

const innerHeight0 = window.innerHeight;
afterEach(() => {
  vi.restoreAllMocks();
  Object.defineProperty(window, 'innerHeight', { configurable: true, value: innerHeight0 });
});

describe('ContextMenu - an open menu holds its rows (#196)', () => {
  it('an answer that lands while the menu is open adds its row at the END: every row stays where it was drawn', async () => {
    const w = await openMenu(fileMenu('pending'));
    const before = labels();
    const tags = rows().find((b) => b.textContent?.includes('Tags'));
    expect(before).not.toContain(ENCRYPT);

    await w.setProps({ actions: fileMenu('allowed') });
    await nextTick();

    const after = labels();
    expect(after.slice(0, before.length), 'the rows that were on screen, in their places').toEqual(before);
    expect(after[after.length - 1]).toBe(ENCRYPT);
    // The very element the pointer was on is still the one at that index.
    expect(rows().indexOf(tags!)).toBe(before.indexOf('Tags…'));
  });

  it('a row that is taken back keeps its place, greyed, and a click on it does nothing', async () => {
    const w = await openMenu(fileMenu('allowed'));
    const before = labels();
    expect(before).toContain(ENCRYPT);

    await w.setProps({ actions: fileMenu('pending') });
    await nextTick();

    expect(labels()).toEqual(before);
    const row = rows().find((b) => b.textContent?.includes(ENCRYPT))!;
    expect(row.disabled).toBe(true);
    row.click();
    await nextTick();
    expect(w.emitted('select')).toBeUndefined();
  });

  it('a row that is still offered changes in place (label, grey)', async () => {
    const w = await openMenu(fileMenu('pending'));
    const before = labels();
    await w.setProps({
      actions: fileMenu('pending').map((a) => (a.key === 'star' ? { ...a, label: 'Unstar' } : a)),
    });
    await nextTick();
    expect(labels()).toEqual(before.map((l) => (l === 'Star' ? 'Unstar' : l)));
  });

  it('the next opening draws the menu in its own order again', async () => {
    const w = await openMenu(fileMenu('pending'));
    await w.setProps({ actions: fileMenu('allowed') });
    await nextTick();
    (w.vm as unknown as Menu).hide();
    await nextTick();
    await (w.vm as unknown as Menu).show({ clientX: 40, clientY: 40 }, []);
    await nextTick();
    expect(labels()).toEqual(['Open', 'Download', ENCRYPT, 'Details', 'Rename', 'Star', 'Tags…', 'Delete']);
  });

  it('near the bottom edge a late row makes the menu scroll inside itself; the menu does not move', async () => {
    // 40 px a row, measured from where the menu really is.
    vi.spyOn(Element.prototype, 'getBoundingClientRect').mockImplementation(function (this: Element) {
      const el = this as HTMLElement;
      if (!el.classList?.contains('fe-ctx')) return { left: 0, right: 0, top: 0, bottom: 0, width: 0, height: 0, x: 0, y: 0 } as DOMRect;
      const top = parseFloat(el.style.top) || 0;
      const height = el.querySelectorAll('.fe-ctx__item').length * 40;
      return { left: 40, right: 240, top, bottom: top + height, width: 200, height, x: 40, y: top } as DOMRect;
    });
    Object.defineProperty(window, 'innerHeight', { configurable: true, value: 600 });

    // 7 rows, 280 px, opened at y=300: its bottom at 580 fits above 592.
    const w = await openMenu(fileMenu('pending'), {}, { clientX: 40, clientY: 300 });
    const menu = document.querySelector('.fe-ctx') as HTMLElement;
    expect(menu.style.top).toBe('300px');
    expect(menu.style.maxHeight).toBe('');

    await w.setProps({ actions: fileMenu('allowed') });
    await flushPromises();

    expect(menu.style.top, 'the menu did not move').toBe('300px');
    // 8 rows would end at 620: it stops at the edge (600 - 8 - 300) and scrolls.
    expect(menu.style.maxHeight).toBe('292px');
    expect(menu.style.overflowY).toBe('auto');
    expect(labels()[labels().length - 1]).toBe(ENCRYPT);
  });

  it('the sheet keeps the height it opened with: a late row scrolls into it instead of lifting every row above it', async () => {
    vi.spyOn(Element.prototype, 'getBoundingClientRect').mockImplementation(function (this: Element) {
      const isSheet = (this as HTMLElement).classList?.contains('fe-sheet');
      return { left: 0, right: 390, top: 0, bottom: 0, width: isSheet ? 390 : 0, height: isSheet ? 352 : 0, x: 0, y: 0 } as DOMRect;
    });
    const w = await openMenu(fileMenu('pending'), { sheet: true });
    const sheet = document.querySelector('.fe-sheet') as HTMLElement;
    expect(sheet.style.height).toBe('352px');

    await w.setProps({ actions: fileMenu('allowed') });
    await nextTick();

    expect(sheet.style.height).toBe('352px');
    const after = labels('.fe-sheet');
    expect(after[after.length - 1]).toBe(ENCRYPT);
    expect(after.indexOf('Tags…'), 'Open, Download, Details, Rename, Star, Tags…').toBe(5);

    // Closing lets the height go: the next opening measures its own.
    (w.vm as unknown as Menu).hide();
    await nextTick();
    await (w.vm as unknown as Menu).show({ clientX: 0, clientY: 0 }, []);
    await nextTick();
    expect(labels('.fe-sheet').indexOf(ENCRYPT)).toBe(2);
  });
});
